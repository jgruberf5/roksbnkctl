package cli

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDirWritable(t *testing.T) {
	dir := t.TempDir()
	if !dirWritable(dir) {
		t.Errorf("a fresh temp dir should be writable")
	}
	// A path that does not exist is not writable (dirWritable never creates it).
	if dirWritable(filepath.Join(dir, "does-not-exist")) {
		t.Errorf("a non-existent dir must report not-writable")
	}
	// A regular file is not a writable directory.
	f := filepath.Join(dir, "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirWritable(f) {
		t.Errorf("a regular file must not report as a writable directory")
	}
}

func TestIsUnderDir(t *testing.T) {
	base := t.TempDir()
	child := filepath.Join(base, "a", "b")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	if !isUnderDir(child, base) {
		t.Errorf("%s should be under %s", child, base)
	}
	if !isUnderDir(base, base) {
		t.Errorf("a dir should count as under itself")
	}
	if isUnderDir(base, child) {
		t.Errorf("%s must NOT be under its own descendant %s", base, child)
	}
	if isUnderDir(child, "") {
		t.Errorf("nothing is under an empty base dir")
	}
	// A sibling with a shared name prefix must not be considered "under" (guards
	// the string-prefix check from matching /foo-bar against /foo).
	sib := base + "-sibling"
	if err := os.MkdirAll(sib, 0o755); err != nil {
		t.Fatal(err)
	}
	if isUnderDir(sib, base) {
		t.Errorf("%s shares a prefix but is not under %s", sib, base)
	}
}
