package ociremote

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Registries answer an unauthenticated request with 401 and a header saying
// where to go and ask for a token. That exchange is the whole of what a public
// pull needs — anonymous tokens are handed out freely, and the scope in the
// challenge is what limits them to reading one repository.

// authenticator holds whatever credential the last challenge produced, keyed by
// the scope it was issued for, so pulling several blobs costs one token.
type authenticator struct {
	client   *http.Client
	username string
	password string
	tokens   map[string]string
}

func newAuthenticator(c *http.Client, username, password string) *authenticator {
	return &authenticator{client: c, username: username, password: password, tokens: map[string]string{}}
}

// challenge is a parsed WWW-Authenticate header.
type challenge struct {
	scheme string
	realm  string
	params map[string]string
}

// do sends req, answering an authentication challenge once and retrying.
//
// The retry is deliberately not a loop: a registry that answers a token-bearing
// request with another challenge is either misconfigured or refusing the
// credential, and retrying would turn that into a spin rather than an error.
func (a *authenticator) do(req *http.Request, scope string) (*http.Response, error) {
	if tok, ok := a.tokens[scope]; ok {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusUnauthorized {
		return resp, nil
	}

	ch := parseChallenge(resp.Header.Get("WWW-Authenticate"))
	drain(resp)
	if ch == nil {
		return nil, fmt.Errorf("ociremote: %s refused the request and offered no way to authenticate", req.URL.Host)
	}

	switch strings.ToLower(ch.scheme) {
	case "bearer":
		tok, err := a.fetchToken(ch, scope)
		if err != nil {
			return nil, err
		}
		a.tokens[scope] = tok
		req.Header.Set("Authorization", "Bearer "+tok)
	case "basic":
		if a.username == "" {
			return nil, fmt.Errorf("ociremote: %s needs credentials", req.URL.Host)
		}
		req.Header.Set("Authorization", "Basic "+basicAuth(a.username, a.password))
	default:
		return nil, fmt.Errorf("ociremote: unsupported authentication scheme %q", ch.scheme)
	}

	retry, err := cloneRequest(req)
	if err != nil {
		return nil, err
	}
	return a.client.Do(retry)
}

// fetchToken performs the token exchange the challenge described.
func (a *authenticator) fetchToken(ch *challenge, scope string) (string, error) {
	if ch.realm == "" {
		return "", fmt.Errorf("ociremote: authentication challenge names no realm")
	}
	u, err := url.Parse(ch.realm)
	if err != nil {
		return "", fmt.Errorf("ociremote: bad realm %q: %w", ch.realm, err)
	}
	q := u.Query()
	if svc := ch.params["service"]; svc != "" {
		q.Set("service", svc)
	}
	if scope != "" {
		q.Set("scope", scope)
	}
	u.RawQuery = q.Encode()

	req, err := http.NewRequest(http.MethodGet, u.String(), nil)
	if err != nil {
		return "", err
	}
	if a.username != "" {
		req.Header.Set("Authorization", "Basic "+basicAuth(a.username, a.password))
	}
	resp, err := a.client.Do(req)
	if err != nil {
		return "", err
	}
	defer drain(resp)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("ociremote: token request to %s returned %s", u.Host, resp.Status)
	}

	// Registries disagree about the field name; both mean the same thing.
	var body struct {
		Token       string `json:"token"`
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&body); err != nil {
		return "", fmt.Errorf("ociremote: unreadable token response: %w", err)
	}
	if body.Token != "" {
		return body.Token, nil
	}
	if body.AccessToken != "" {
		return body.AccessToken, nil
	}
	return "", fmt.Errorf("ociremote: token response from %s carried no token", u.Host)
}

// parseChallenge reads a WWW-Authenticate header: a scheme, then comma-separated
// key="value" pairs.
func parseChallenge(h string) *challenge {
	h = strings.TrimSpace(h)
	if h == "" {
		return nil
	}
	scheme, rest, _ := strings.Cut(h, " ")
	ch := &challenge{scheme: scheme, params: map[string]string{}}
	for _, part := range splitParams(rest) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"`)
		ch.params[k] = v
		if k == "realm" {
			ch.realm = v
		}
	}
	return ch
}

// splitParams splits on commas that are not inside quotes, since a realm may
// hold one.
func splitParams(s string) []string {
	var out []string
	var cur strings.Builder
	inQuotes := false
	for _, r := range s {
		switch {
		case r == '"':
			inQuotes = !inQuotes
			cur.WriteRune(r)
		case r == ',' && !inQuotes:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}

// cloneRequest makes a request reusable after a challenge. Only GETs are sent
// here, so there is no body to rewind.
func cloneRequest(req *http.Request) (*http.Request, error) {
	out, err := http.NewRequest(req.Method, req.URL.String(), nil)
	if err != nil {
		return nil, err
	}
	out.Header = req.Header.Clone()
	return out, nil
}

// drain closes a response body, reading a little first so the connection can be
// reused rather than torn down.
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	io.CopyN(io.Discard, resp.Body, 64<<10)
	resp.Body.Close()
}
