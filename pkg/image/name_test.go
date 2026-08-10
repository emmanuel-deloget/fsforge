package image

import (
	"errors"
	"testing"
)

func TestValidName(t *testing.T) {
	bad := []string{"", ".", "..", "/", "a/b", "../etc", "etc/", "a/"}
	for _, name := range bad {
		if err := ValidName(name); !errors.Is(err, ErrBadName) {
			t.Errorf("ValidName(%q) = %v, want ErrBadName", name, err)
		}
	}
	// Legal on Linux and therefore legal here: a backslash is an ordinary
	// character, as are spaces, newlines and leading dots.
	good := []string{"a", "..a", "a..", "...", `a\b`, "a b", "a\nb", ".hidden", "lost+found"}
	for _, name := range good {
		if err := ValidName(name); err != nil {
			t.Errorf("ValidName(%q) = %v, want nil", name, err)
		}
	}
}

func TestAddChildRejectsBadNames(t *testing.T) {
	parent := &Node{}
	if err := parent.AddChild("..", &Node{}); !errors.Is(err, ErrBadName) {
		t.Fatalf("AddChild(\"..\") = %v, want ErrBadName", err)
	}
	if len(parent.Children) != 0 {
		t.Fatalf("rejected name still entered the tree: %v", parent.Children)
	}
	if err := parent.AddChild("ok", &Node{}); err != nil {
		t.Fatalf("AddChild(\"ok\") = %v, want nil", err)
	}
	if len(parent.Children) != 1 || parent.Children[0].Name != "ok" {
		t.Fatalf("child not added: %v", parent.Children)
	}
}
