package cli

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/exitcode"
)

// #154. errRollbackFailed reports the one state a self-update cannot recover
// from: no binary at the install path, the only copy sitting at target.old.
// It carried no exit code, so exitcode.FromError returned Failure (1) and a
// bricked install was indistinguishable from an ordinary failed upgrade.
//
// That matters because the two need opposite responses. An ordinary failure is
// safe to retry; this one cannot be retried at all, because there is nothing
// left to run. The internal/exitcode package doc says why the distinction is
// worth paying for: "Every demo and CI path in this repo is a shell script that
// branches on $?, so the codes are an interface."

// The mapping IS the interface, so it is asserted through the real function
// rather than by constructing the error directly.
func TestAStrandedInstallGetsItsOwnExitCode(t *testing.T) {
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
		if calls == 1 { // let the move-aside through, fail everything after
			return orig(from, to)
		}
		return fs.ErrPermission
	}

	err := installByMoveAside(target, staged)
	if err == nil {
		t.Fatal("both the install and the rollback failed; this must return an error")
	}
	if got := exitcode.FromError(err); got != exitcode.SelfUpdateStranded {
		t.Errorf("a stranded install exits %d, want %d (SelfUpdateStranded).\n"+
			"At %d a wrapper cannot tell this from an ordinary failed upgrade, so it either "+
			"retries forever on a machine that will never recover, or reports a bricked "+
			"install as routine.", got, exitcode.SelfUpdateStranded, got)
	}

	// The code must not cost the message or the causes. Wrapping is additive.
	if !errors.Is(err, fs.ErrPermission) {
		t.Error("the underlying cause must still be reachable through the coded wrapper")
	}
	var rf errRollbackFailed
	if !errors.As(err, &rf) {
		t.Fatal("errors.As must still reach errRollbackFailed through the coded wrapper")
	}
	if rf.target != target || rf.old != target+".old" {
		t.Errorf("the wrapped error lost its paths: target=%q old=%q", rf.target, rf.old)
	}
}

// A RECOVERABLE failure must keep the ordinary code. Without this, wrapping
// everything in the new code would pass the test above and destroy the very
// distinction it exists to create.
func TestARecoverableUpgradeFailureStillExitsFailure(t *testing.T) {
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
		if calls == 2 { // fail only the install; let the rollback succeed
			return fs.ErrPermission
		}
		return orig(from, to)
	}

	err := installByMoveAside(target, staged)
	if err == nil {
		t.Fatal("the install failed, so an error is expected")
	}
	if got := exitcode.FromError(err); got != exitcode.Failure {
		t.Errorf("the rollback worked and the binary is back — this is an ordinary retryable "+
			"failure and must exit %d, got %d", exitcode.Failure, got)
	}
	// And the binary really is back, so the code is telling the truth.
	if _, serr := os.Stat(target); serr != nil {
		t.Errorf("precondition: the rollback should have restored target: %v", serr)
	}
}

// Cancellation outranks a carried code in FromError, and that ordering is
// already pinned by TestFromErrorResolvesTheContract in internal/exitcode. What
// is worth recording HERE is the consequence of the ordering meeting this
// particular error, which is not obvious and is not covered there.
//
// errRollbackFailed.Unwrap() returns BOTH causes, so errors.Is walks into each.
// If a cancellation ever reaches either one, FromError returns Interrupted and
// the 125 is lost — a bricked install would report as a clean Ctrl-C.
//
// Not reachable today: renameFile is os.Rename, which never returns
// context.Canceled, and installByMoveAside never consults a ctx. But renameFile
// is a package var installed as a test seam, and runUpgrade holds a cancellable
// ctx a few frames up, so a ctx-aware rename is the natural future edit that
// would silently flip this. The test states the current answer so a change to
// it is a decision rather than an accident.
func TestACancellationInEitherCauseOutranksTheStrandedCode(t *testing.T) {
	stranded := exitcode.Wrap(exitcode.SelfUpdateStranded, errRollbackFailed{
		install:  context.Canceled,
		rollback: fs.ErrPermission,
		old:      "a", target: "b",
	})
	if got := exitcode.FromError(stranded); got != exitcode.Interrupted {
		t.Errorf("a cancellation in the install cause should give %d, got %d",
			exitcode.Interrupted, got)
	}

	// And through the rollback cause, which is the second element of Unwrap.
	stranded = exitcode.Wrap(exitcode.SelfUpdateStranded, errRollbackFailed{
		install:  fs.ErrPermission,
		rollback: context.Canceled,
		old:      "a", target: "b",
	})
	if got := exitcode.FromError(stranded); got != exitcode.Interrupted {
		t.Errorf("a cancellation in the rollback cause should give %d, got %d",
			exitcode.Interrupted, got)
	}

	// With no cancellation anywhere, the code stands.
	stranded = exitcode.Wrap(exitcode.SelfUpdateStranded, errRollbackFailed{
		install:  fs.ErrPermission,
		rollback: fs.ErrPermission,
		old:      "a", target: "b",
	})
	if got := exitcode.FromError(stranded); got != exitcode.SelfUpdateStranded {
		t.Errorf("without a cancellation the stranded code must stand, got %d", got)
	}
}
