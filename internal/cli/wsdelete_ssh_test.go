package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRemoveCopiedSSHKeys verifies ws delete removes exactly the recorded
// (init-copied) ~/.ssh files and never touches an unrecorded user file.
func TestRemoveCopiedSSHKeys(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	write := func(name string) {
		if err := os.WriteFile(filepath.Join(sshDir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("k1")       // recorded (ours)
	write("k1.pub")   // recorded (ours)
	write("user-key") // NOT recorded (the user's own key)

	removeCopiedSSHKeys([]string{"k1", "k1.pub"})

	for _, gone := range []string{"k1", "k1.pub"} {
		if _, err := os.Stat(filepath.Join(sshDir, gone)); !os.IsNotExist(err) {
			t.Errorf("recorded key %s was not removed", gone)
		}
	}
	if _, err := os.Stat(filepath.Join(sshDir, "user-key")); err != nil {
		t.Errorf("unrecorded user-key must NOT be removed: %v", err)
	}

	// Empty/nil list is a no-op (must not panic or touch ~/.ssh).
	removeCopiedSSHKeys(nil)
}
