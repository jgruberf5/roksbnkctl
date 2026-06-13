package orchestration

import (
	"errors"
	"testing"
)

// TestLooksTransientDestroy pins the teardown-specific retry classifier. The
// live trigger was a cluster-phase `terraform destroy` aborting on
// "DeletePublicGatewayWithContext failed: Public Gateway not found" (a parallel
// delete-race) — which leaked the un-reached VPC. destroyWithRetry must treat
// that (and the shared looksTransient patterns) as retryable, and must NOT
// retry on genuine errors.
func TestLooksTransientDestroy(t *testing.T) {
	retryable := []string{
		// The exact live failure that motivated this fix.
		"Error: DeletePublicGatewayWithContext failed: Public Gateway not found",
		// Generic IBM provider delete-race shape: <Op>WithContext failed: … not found.
		"DeleteSubnetWithContext failed: Subnet not found",
		"DeleteVPCWithContext failed: VPC not found",
		// Targeted extras.
		"Cannot delete VPC: resources still attached",
		"the floating IP is still attached",
		"rate limited, please retry the request",
		// Shared transient patterns (looksTransient) must also retry under destroy.
		"connection refused",
		"TLS handshake timeout",
	}
	for _, msg := range retryable {
		err := errors.New(msg)
		if !(looksTransientDestroy(err) || looksTransient(err)) {
			t.Errorf("expected RETRYABLE for destroy: %q", msg)
		}
	}

	// Genuine, non-transient failures must fail fast (no retry).
	nonRetryable := []string{
		"Error: Invalid value for variable",
		"Error: insufficient permissions to delete resource",
		"Error: a resource named foo already exists", // an apply-shaped error, not a delete-race
		"some unrelated not found in a data lookup",  // "not found" without the WithContext-failed pairing
	}
	for _, msg := range nonRetryable {
		err := errors.New(msg)
		if looksTransientDestroy(err) {
			t.Errorf("expected NON-retryable (looksTransientDestroy=false): %q", msg)
		}
	}

	if looksTransientDestroy(nil) {
		t.Error("nil error must not be retryable")
	}
}
