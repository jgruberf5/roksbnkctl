package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExpandHomePath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	cases := map[string]string{
		"~":            home,
		"~/":           home,
		"~/a/b.tgz":    filepath.Join(home, "a/b.tgz"),
		"/abs/path":    "/abs/path",
		"relative.jwt": "relative.jwt",
		"~notme/x":     "~notme/x", // only ~/ (or bare ~) expands, not ~user
	}
	for in, want := range cases {
		if got := expandHomePath(in); got != want {
			t.Errorf("expandHomePath(%q) = %q, want %q", in, got, want)
		}
	}
}
