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
// A coded error wins. Otherwise a cancelled context means the operator
// interrupted us — that is the case that used to collapse into a generic 1,
// leaving a script unable to tell a Ctrl-C from a real failure. Everything else
// is Failure.
func FromError(err error) int {
	if err == nil {
		return OK
	}
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	if errors.Is(err, context.Canceled) {
		return Interrupted
	}
	return Failure
}
