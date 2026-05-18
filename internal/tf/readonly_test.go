package tf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// TestOpenReadOnly_NeverApplied_NoStateNoSideEffects asserts the Sprint
// 13 Issue 2 hard requirement 4: a never-applied workspace phase (no
// terraform.tfstate) yields ErrNoState and does NOT fetch source / run
// init as a side effect.
func TestOpenReadOnly_NeverApplied_NoStateNoSideEffects(t *testing.T) {
	stateDir := t.TempDir()
	// A valid local TF source exists, but the phase was never applied
	// (no terraform.tfstate under stateDir). OpenReadOnly must bail
	// BEFORE touching it.
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "main.tf"), []byte("# empty\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ws := &config.Workspace{}
	ws.TFSource = config.TFSourceCfg{Type: "local", Path: srcDir}

	_, err := OpenReadOnly(context.Background(), "ro-ws", ws, stateDir)
	if !errors.Is(err, ErrNoState) {
		t.Fatalf("OpenReadOnly on never-applied phase = %v, want ErrNoState", err)
	}
	// Side-effect check: Open() would have created <stateDir>/tf-source.
	// OpenReadOnly must not have, since it returned before delegating.
	if _, statErr := os.Stat(filepath.Join(stateDir, "tf-source")); !os.IsNotExist(statErr) {
		t.Errorf("OpenReadOnly fetched source for a never-applied phase (tf-source exists) — side-effect leak")
	}
	if _, statErr := os.Stat(filepath.Join(stateDir, "terraform")); !os.IsNotExist(statErr) {
		t.Errorf("OpenReadOnly created TF_DATA_DIR for a never-applied phase — side-effect leak")
	}
}

// TestOpenReadOnly_NilConfig: defensive nil-config guard.
func TestOpenReadOnly_NilConfig(t *testing.T) {
	if _, err := OpenReadOnly(context.Background(), "x", nil, t.TempDir()); err == nil {
		t.Errorf("OpenReadOnly(nil cfg) should error")
	}
}

// TestRunReadOnly_NotOpened: RunReadOnly on a zero Workspace errors
// rather than panicking.
func TestRunReadOnly_NotOpened(t *testing.T) {
	w := &Workspace{}
	if _, err := w.RunReadOnly(context.Background(), []string{"version"}); err == nil {
		t.Errorf("RunReadOnly on unopened workspace should error")
	}
	if _, err := w.RunReadOnly(context.Background(), nil); err == nil {
		t.Errorf("RunReadOnly with empty argv should error")
	}
}
