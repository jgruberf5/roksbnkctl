package exitcode

import (
	"context"
	"errors"
	"fmt"
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
		// An explicit code wins over the context inference.
		{"a coded cancellation", Wrap(Usage, context.Canceled), Usage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromError(tc.err); got != tc.want {
				t.Errorf("FromError(%v) = %d, want %d", tc.err, got, tc.want)
			}
		})
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
		"OK":            {OK, 0},
		"Failure":       {Failure, 1},
		"Usage":         {Usage, 2},
		"AuthFailed":    {AuthFailed, 126},
		"ConnectFailed": {ConnectFailed, 127},
		"Interrupted":   {Interrupted, 130},
	} {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d — scripts branch on these; changing one breaks callers", name, tc.got, tc.want)
		}
	}
}
