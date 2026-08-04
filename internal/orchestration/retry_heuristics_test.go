package orchestration

import (
	"errors"
	"testing"
)

// looksTransient gates apply-time retries. looksTransientDestroy + applyDecision
// are covered by destroy_retry_test.go / three_phase_dispatch_test.go; this fills
// the remaining gap — the up-path heuristic — so a change to the pattern list is
// caught, and a genuine (non-retryable) error is never spun on.
func TestLooksTransient(t *testing.T) {
	transient := []string{
		"dial tcp: i/o timeout",
		"connection refused",
		"no such host",
		"failed calling webhook f5validate.f5net.com",
		"admission webhook denied: status unknown for quota",
		"server gave HTTP response to HTTPS client",
	}
	for _, s := range transient {
		if !looksTransient(errors.New(s)) {
			t.Errorf("looksTransient(%q) = false, want true (should retry)", s)
		}
	}
	// Genuine, non-retryable errors must NOT read as transient.
	notTransient := []string{
		"Error: Invalid count argument",
		"The VPC is in use and cannot be deleted",
		"BucketAlreadyExists",
	}
	for _, s := range notTransient {
		if looksTransient(errors.New(s)) {
			t.Errorf("looksTransient(%q) = true, want false (must not retry a real error)", s)
		}
	}
	if looksTransient(nil) {
		t.Error("looksTransient(nil) must be false")
	}
}
