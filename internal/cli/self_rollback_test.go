package cli

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #146. installByMoveAside documented a guarantee — "the caller is never left
// without a working binary" — that the code did not keep: the rollback's error
// was discarded with `_ =`, so a failed rollback returned an error naming the
// wrong problem ("installing new binary") while target no longer existed.
//
// The path exists BECAUSE Windows locks a running .exe, which is exactly where
// a second process holding a handle mid-update is routine rather than exotic.

// The unrecoverable state, driven through the real function: the staged binary
// will not move into place AND the old one will not move back. Ordinary
// permissions cannot produce this — target and target.old share a directory, so
// anything that fails the rollback also fails the first rename — so the rename
// syscall is stubbed for the duration.
//
// This is the branch that strands a user, and it is the reason the fix exists;
// leaving it to a message-only test would cover the wording and not the path.
func TestUnrecoverableRollbackLeavesNoBinaryAndSaysSo(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "roksbnkctl")
	if err := os.WriteFile(target, []byte("the real binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "roksbnkctl.new")
	if err := os.WriteFile(staged, []byte("the new binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Let the first rename through (that is the move-aside), then fail every
	// rename after it: the install AND the rollback.
	orig := renameFile
	t.Cleanup(func() { renameFile = orig })
	calls := 0
	renameFile = func(from, to string) error {
		calls++
		if calls == 1 {
			return orig(from, to)
		}
		return fs.ErrPermission
	}

	err := installByMoveAside(target, staged)
	if err == nil {
		t.Fatal("both the install and the rollback failed; this must return an error")
	}
	if calls != 3 {
		t.Errorf("expected move-aside, install, rollback = 3 renames, got %d", calls)
	}

	// The filesystem is genuinely in the bad state — this is not a simulated
	// message, the binary really is gone from target.
	if _, serr := os.Stat(target); !os.IsNotExist(serr) {
		t.Errorf("target should not exist after a failed rollback, stat gave: %v", serr)
	}
	if _, serr := os.Stat(target + ".old"); serr != nil {
		t.Fatalf("the only surviving binary should be the sidecar: %v", serr)
	}

	msg := err.Error()
	if !strings.Contains(msg, "NO binary") {
		t.Errorf("the error must state that no binary remains:\n%s", msg)
	}
	if !strings.Contains(msg, target+".old") || !strings.Contains(msg, "mv ") {
		t.Errorf("the error must name the sidecar and the recovery command:\n%s", msg)
	}

	// The precise regression: the old code returned this text alone, which
	// points at the new binary and hides the missing one.
	if msg == "installing new binary: "+fs.ErrPermission.Error() {
		t.Error("this is the pre-fix message — it names the wrong problem and omits the recovery")
	}
}

// A rollback that SUCCEEDS is the ordinary recoverable failure, and must not
// alarm: the binary is back, so the error stays the plain one. Without this,
// a fix that always reported the scary message would pass the test above.
func TestSuccessfulRollbackKeepsThePlainError(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "roksbnkctl")
	if err := os.WriteFile(target, []byte("the real binary"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "roksbnkctl.new")
	if err := os.WriteFile(staged, []byte("the new binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	orig := renameFile
	t.Cleanup(func() { renameFile = orig })
	calls := 0
	renameFile = func(from, to string) error {
		calls++
		if calls == 2 { // fail only the install; let the rollback through
			return fs.ErrPermission
		}
		return orig(from, to)
	}

	err := installByMoveAside(target, staged)
	if err == nil {
		t.Fatal("the install failed, so an error is expected")
	}
	if strings.Contains(err.Error(), "NO binary") {
		t.Errorf("the rollback worked — the error must not claim the binary is gone:\n%s", err)
	}

	// And the original binary really is back in place.
	got, rerr := os.ReadFile(target)
	if rerr != nil {
		t.Fatalf("the rollback should have restored target: %v", rerr)
	}
	if string(got) != "the real binary" {
		t.Errorf("target holds %q, want the original binary", got)
	}
}

// The unrecoverable case, driven directly: both renames fail. Constructed by
// calling the error type the way installByMoveAside does, and asserting the
// message carries what a stranded user needs.
func TestRollbackFailureMessageNamesBothFilesAndTheCommand(t *testing.T) {
	install := errors.New("access is denied")
	rollback := fs.ErrPermission
	e := errRollbackFailed{
		install:  install,
		rollback: rollback,
		old:      `C:\tools\roksbnkctl.exe.old`,
		target:   `C:\tools\roksbnkctl.exe`,
	}

	msg := e.Error()
	for _, want := range []string{
		"NO binary",                   // states the actual condition
		`C:\tools\roksbnkctl.exe.old`, // where the real binary is
		`C:\tools\roksbnkctl.exe`,     // where it needs to go
		"mv ",                         // the recovery command
		"access is denied",            // why the install failed
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("the message must contain %q:\n%s", want, msg)
		}
	}

	// It must NOT read as a plain install failure — that is the misdirection
	// this fixes. The old code returned "installing new binary: ..." and said
	// nothing about the missing file.
	if !strings.Contains(msg, "rolling back also failed") {
		t.Errorf("the message must distinguish itself from an ordinary install failure:\n%s", msg)
	}

	// Both causes stay reachable, so a caller inspecting for a permission
	// problem still finds it through either one.
	if !errors.Is(e, fs.ErrPermission) {
		t.Error("errors.Is must match the rollback cause")
	}
	if !errors.Is(e, install) {
		t.Error("errors.Is must match the install cause")
	}
}

// The happy path must be untouched: a successful replace leaves the new binary
// at target and no error.
func TestSuccessfulMoveAsideStillReplacesTheBinary(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "roksbnkctl")
	if err := os.WriteFile(target, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, "staged")
	if err := os.WriteFile(staged, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := installByMoveAside(target, staged); err != nil {
		t.Fatalf("installByMoveAside: %v", err)
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("target must exist after a successful install: %v", err)
	}
	if string(got) != "new" {
		t.Errorf("target holds %q, want the staged contents", got)
	}
}
