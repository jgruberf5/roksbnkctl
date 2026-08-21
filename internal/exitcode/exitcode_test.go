package exitcode

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
)

// #118. The codes were set from eighteen places in five packages with no shared
// policy, and because os.Exit terminates the test binary nothing could assert
// any of it. That is why the inconsistencies went unnoticed rather than being
// caught: there was no way to notice.
func TestFromErrorResolvesTheContract(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"no error", nil, OK},
		{"a plain error", errors.New("boom"), Failure},
		{"a coded error", Wrap(ConnectFailed, errors.New("no route")), ConnectFailed},
		{"a coded error built from a message", Newf(AuthFailed, "denied for %s", "bastion"), AuthFailed},
		{"a silent code", Silent(3), 3},
		{"a coded error wrapped again", fmt.Errorf("context: %w", Wrap(AuthFailed, errors.New("x"))), AuthFailed},

		// The headline inconsistency: Ctrl-C during `init` exited 130 while
		// Ctrl-C during everything else became 1 — indistinguishable from a
		// real failure, so a script could not tell "the operator stopped it"
		// from "it broke".
		{"a cancelled context", context.Canceled, Interrupted},
		{"a cancelled context, wrapped", fmt.Errorf("applying: %w", context.Canceled), Interrupted},

		// A deadline is a failure, not an interrupt: nobody pressed anything.
		{"a deadline", context.DeadlineExceeded, Failure},
		// Cancellation outranks the code. A coded error wrapping
		// context.Canceled is a failure that happened BECAUSE of the Ctrl-C —
		// a connect aborted mid-handshake gets coded ConnectFailed on its way
		// out — and reporting 127 for a deliberate interrupt sends a CI
		// wrapper retrying an unreachable-looking target the operator just
		// stopped. No call site can accidentally code a cancellation into
		// something else.
		{"a coded cancellation", Wrap(ConnectFailed, context.Canceled), Interrupted},
		{"a coded error whose chain carries the cancellation",
			Newf(AuthFailed, "remote run: %w", fmt.Errorf("session: %w", context.Canceled)), Interrupted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromError(tc.err); got != tc.want {
				t.Errorf("FromError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
	}
}

// A signal-killed child's ExitCode() is -1, and os.Exit(-1) becomes shell
// status 255 with no explanation. FromChildExit maps it to 128+signum (the
// shell convention), so a SIGINT-killed child propagates Interrupted=130.
func TestFromChildExitMapsSignalsToShellConvention(t *testing.T) {
	run := func(t *testing.T, script string) *exec.ExitError {
		t.Helper()
		err := exec.Command("sh", "-c", script).Run()
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("expected *exec.ExitError, got %v", err)
		}
		return ee
	}

	if got := FromChildExit(run(t, "exit 3")); got != 3 {
		t.Errorf("a plain exit code must pass through: got %d, want 3", got)
	}
	if got := FromChildExit(run(t, "kill -INT $$")); got != Interrupted {
		t.Errorf("a SIGINT-killed child = %d, want %d (128+SIGINT, the shell convention)", got, Interrupted)
	}
	if got := FromChildExit(run(t, "kill -KILL $$")); got != 137 {
		t.Errorf("a SIGKILL-killed child = %d, want 137 (128+SIGKILL)", got)
	}
}

// Wrap composes with `return Wrap(code, doThing())`, so a nil must stay nil —
// otherwise every success path starts returning a non-nil error.
func TestWrapPassesNilThrough(t *testing.T) {
	if err := Wrap(ConnectFailed, nil); err != nil {
		t.Errorf("Wrap(code, nil) = %v, want nil", err)
	}
}

// A coded error must stay inspectable: wrapping is for the exit status, not a
// place for the cause to disappear into.
func TestCodedErrorsKeepTheirCause(t *testing.T) {
	cause := errors.New("connection refused")
	err := Wrap(ConnectFailed, cause)

	if !errors.Is(err, cause) {
		t.Error("errors.Is cannot see through a coded error — the cause is lost")
	}
	if got, want := err.Error(), cause.Error(); got != want {
		t.Errorf("Error() = %q, want the cause's message %q — a code is not a message", got, want)
	}
}

// Silent errors exist for paths that already printed their own report (a
// wrapped tool's diagnostics, doctor's check table). The root must not add a
// bare "roksbnkctl: " line on top of them.
func TestSilentErrorsAreDistinguishable(t *testing.T) {
	if !IsSilent(Silent(Failure)) {
		t.Error("a silent error must be recognisable so the root prints nothing extra")
	}
	if IsSilent(Wrap(Failure, errors.New("something to say"))) {
		t.Error("an error carrying a message is not silent — that message would be swallowed")
	}
	if IsSilent(errors.New("plain")) {
		t.Error("a plain error is not silent")
	}
	if IsSilent(nil) {
		t.Error("nil is not a silent error")
	}
	if got := Silent(7).Error(); got != "exit status 7" {
		t.Errorf("a silent error should still describe itself for logs, got %q", got)
	}
}

// The values are a published interface — every demo and CI script in this repo
// branches on them, so a change here is a breaking change for callers that
// cannot be seen from inside this package.
func TestTheContractsValuesAreStable(t *testing.T) {
	for name, tc := range map[string]struct{ got, want int }{
		"OK":      {OK, 0},
		"Failure": {Failure, 1},
		"Usage":   {Usage, 2},
		// 125 was chosen because nothing in scripts/, .github/ or the Makefile
		// keys on it, and it sits below the 126/127 pair. Moving it would break
		// any wrapper that learned to retry on 1 but not on this.
		"SelfUpdateStranded": {SelfUpdateStranded, 125},
		"AuthFailed":         {AuthFailed, 126},
		"ConnectFailed":      {ConnectFailed, 127},
		"Interrupted":        {Interrupted, 130},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d — scripts branch on these; changing one breaks callers", name, tc.got, tc.want)
		}
	}
}
