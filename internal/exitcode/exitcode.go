// Package exitcode defines roksbnkctl's exit-code contract and the error type
// that carries a code out of a command.
//
// Every demo and CI path in this repo is a shell script that branches on `$?`,
// so the codes are an interface. They used to be set from eighteen places in
// five packages with no shared policy: `roksbnkctl init` exited 130 on
// interrupt while every other command turned the same Ctrl-C into 1,
// indistinguishable from a real failure; `internal/remote` defined a meaningful
// scheme that nothing else participated in; and because os.Exit terminates the
// test binary, no test could assert any of it.
//
// Commands now RETURN a coded error and the CLI root maps it to a status, so
// the contract is stated once and can be tested.
package exitcode

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"syscall"
)

// The contract. Values follow shell convention where one exists, so a script
// author's existing intuition is right.
const (
	// OK — the command did what was asked.
	OK = 0

	// Failure — anything that went wrong without a more specific code. The
	// default for a plain error.
	Failure = 1

	// Usage — the invocation itself was rejected: a malformed flag, an argument
	// the command cannot accept. Nothing was attempted.
	Usage = 2

	// SelfUpdateStranded — an upgrade removed the old binary and could not put
	// anything back, so there is no roksbnkctl at the install path and a human
	// has to rename the sidecar by hand.
	//
	// Distinct from Failure because the two need opposite responses: an ordinary
	// failed upgrade is safe to retry, and this one cannot be retried at all —
	// there is nothing left to run. A wrapper that treats them alike either
	// loops forever on a machine that will never recover, or reports a bricked
	// install as a routine error. See #154.
	//
	// 125 is free: nothing in scripts/, .github/ or the Makefile keys on it, and
	// it sits below the 126/127 pair rather than colliding with them.
	//
	// It DOES overlap the range a wrapped tool's own status is passed through
	// on — a child exiting 125 propagates as 125, and that is unchanged. The
	// two never meet in one invocation: `upgrade` and `self update` spawn no
	// child process at all (no os/exec in that path; extraction, checksums and
	// the install are all in-process), so a 125 from those commands has exactly
	// one meaning. This is the same bargain already struck for Usage=2 against
	// a child that exits 2.
	SelfUpdateStranded = 125

	// AuthFailed — "permission denied": SSH authentication, a host-key
	// mismatch, a credential the remote end refused.
	AuthFailed = 126

	// ConnectFailed — "command not found" analog: the target could not be
	// reached at all.
	ConnectFailed = 127

	// Interrupted — the operator stopped it (SIGINT). Distinct from Failure so
	// a script can tell "someone pressed Ctrl-C" from "it broke".
	Interrupted = 130
)

// Error is an error carrying the exit status the process should end with.
type Error struct {
	Code int
	Err  error
}

func (e *Error) Error() string {
	if e.Err == nil {
		return fmt.Sprintf("exit status %d", e.Code)
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error { return e.Err }

// Wrap tags err with an exit code. A nil err returns nil, so it composes with
// the usual `return Wrap(code, doThing())` shape without a guard.
func Wrap(code int, err error) error {
	if err == nil {
		return nil
	}
	return &Error{Code: code, Err: err}
}

// Newf builds a coded error from a message.
func Newf(code int, format string, a ...any) error {
	return &Error{Code: code, Err: fmt.Errorf(format, a...)}
}

// Silent is a coded error with no message, for a path that has already told the
// user what happened — a wrapped tool that streamed its own diagnostics, or a
// check that printed its own report. The root prints nothing extra and exits
// with the code.
func Silent(code int) error { return &Error{Code: code} }

// IsSilent reports whether err is a coded error carrying no message, so the
// root can exit without printing an empty line.
func IsSilent(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Err == nil
}

// FromError resolves the status a command's error should produce.
//
// Cancellation outranks a code: an error whose chain carries context.Canceled
// happened BECAUSE the operator pressed Ctrl-C — the connect that was aborted,
// the child that was killed — and reporting the failure class it happened to
// land in (127, 126, …) would send a script retrying or rotating credentials
// over a deliberate interrupt. Checking it first means no call site can
// accidentally code a cancellation into something else. A Silent child status
// is unaffected (it wraps no error), and a deadline is a real failure — nobody
// pressed anything. After that a coded error wins, and everything else is
// Failure.
func FromError(err error) int {
	if err == nil {
		return OK
	}
	if errors.Is(err, context.Canceled) {
		return Interrupted
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return Failure
}

// FromChildExit maps a finished child's *exec.ExitError to the status the
// parent should propagate: the child's own exit code, or 128+signum when the
// child died to a signal — the shell convention, which makes a SIGINT-killed
// child propagate Interrupted=130.
//
// The naive ee.ExitCode() is -1 for a signal death, and exiting with -1
// becomes shell status 255 with no explanation; that is the bug this helper
// exists to keep out of the call sites.
func FromChildExit(ee *exec.ExitError) int {
	if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if c := ee.ExitCode(); c >= 0 {
		return c
	}
	return Failure
}
