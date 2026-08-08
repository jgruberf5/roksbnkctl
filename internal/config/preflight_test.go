package config

import (
	"testing"
	"time"
)

func intp(n int) *int { return &n }

// The defaults must survive every shape of "the config never mentioned this", because
// that is what almost every workspace looks like.
func TestReachabilityDefaults(t *testing.T) {
	for _, w := range []*Workspace{nil, {}, {BNK: BNKCfg{}}, {BNK: BNKCfg{Preflight: &BNKPreflightCfg{}}}} {
		if got := w.ReachabilityRetrySeconds(); got != DefaultReachabilityRetrySeconds {
			t.Errorf("retry default = %d, want %d", got, DefaultReachabilityRetrySeconds)
		}
		want := time.Duration(DefaultReachabilityTimeoutSeconds) * time.Second
		if got := w.ReachabilityTimeout(); got != want {
			t.Errorf("timeout default = %s, want %s", got, want)
		}
	}
}

// A configured 0 is a real answer — "one shot, this environment never races" — and
// must not be mistaken for "unset". That is the whole reason the fields are pointers.
func TestReachabilityRetry_ZeroIsHonoured(t *testing.T) {
	w := &Workspace{BNK: BNKCfg{Preflight: &BNKPreflightCfg{ReachabilityRetrySeconds: intp(0)}}}
	if got := w.ReachabilityRetrySeconds(); got != 0 {
		t.Errorf("an explicit 0 must disable retrying, got %d", got)
	}
}

func TestReachabilityRetry_Configured(t *testing.T) {
	w := &Workspace{BNK: BNKCfg{Preflight: &BNKPreflightCfg{ReachabilityRetrySeconds: intp(600)}}}
	if got := w.ReachabilityRetrySeconds(); got != 600 {
		t.Errorf("retry = %d, want 600", got)
	}
}

// Raising the retry budget without raising the timeout is a contradiction — "keep
// retrying for ten minutes, but give up waiting after three" — and the failure it
// produces is the confusing kind: a timeout that reads as a network problem but is
// really the configuration. Take the intent and give the wait enough room.
func TestReachabilityTimeout_ClampedAboveRetry(t *testing.T) {
	w := &Workspace{BNK: BNKCfg{Preflight: &BNKPreflightCfg{
		ReachabilityRetrySeconds:   intp(600),
		ReachabilityTimeoutSeconds: intp(180),
	}}}
	got := w.ReachabilityTimeout()
	if got <= 600*time.Second {
		t.Errorf("timeout %s must exceed the 600s retry budget it is waiting for", got)
	}
	if want := 720 * time.Second; got != want {
		t.Errorf("timeout = %s, want %s (retry + start-up margin)", got, want)
	}
}

// A timeout comfortably above the retry budget is left exactly as written — the clamp
// is a floor, not a policy.
func TestReachabilityTimeout_GenerousValueIsUntouched(t *testing.T) {
	w := &Workspace{BNK: BNKCfg{Preflight: &BNKPreflightCfg{
		ReachabilityRetrySeconds:   intp(60),
		ReachabilityTimeoutSeconds: intp(1800),
	}}}
	if got, want := w.ReachabilityTimeout(), 1800*time.Second; got != want {
		t.Errorf("timeout = %s, want %s", got, want)
	}
}
