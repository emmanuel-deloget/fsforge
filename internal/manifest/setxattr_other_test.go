//go:build unix && !linux

package manifest

import "errors"

func setxattr(string, string, []byte) error {
	return errors.New("setxattr is only wired up on linux")
}
