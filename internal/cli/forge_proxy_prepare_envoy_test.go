package cli

// forge_proxy_prepare_envoy_test.go — unit tests for WS-E2 prepare-envoy additions.
//
// Tests:
//   - isNoMatchError: retry-predicate for the CRD-Established race (Fix 2)

import (
	"fmt"
	"testing"
)

// ---------------------------------------------------------------------------
// isNoMatchError — retry predicate for CRD Established race (Fix 2)
// ---------------------------------------------------------------------------

func TestIsNoMatchError_NoMatchErrors(t *testing.T) {
	// These error strings should be detected as no-match errors and trigger retry.
	cases := []struct {
		name string
		err  error
	}{
		{"no matches for kind", fmt.Errorf("apply: no matches for kind \"EnvoyProxy\" in group \"gateway.envoyproxy.io\"")},
		{"no match for kind", fmt.Errorf("no match for kind EnvoyProxy")},
		{"RESTMapping in message", fmt.Errorf("RESTMapping not found for EnvoyProxy")},
		{"wrapped no matches", fmt.Errorf("apply EnvoyProxy CR: %w", fmt.Errorf("no matches for kind \"EnvoyProxy\""))},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if !isNoMatchError(tc.err) {
				t.Errorf("isNoMatchError(%v) = false, want true", tc.err)
			}
		})
	}
}

func TestIsNoMatchError_NonRetryableErrors(t *testing.T) {
	// These errors must NOT trigger retry.
	cases := []struct {
		name string
		err  error
	}{
		{"nil", nil},
		{"permission denied", fmt.Errorf("apply: forbidden: User cannot create resource")},
		{"connection refused", fmt.Errorf("dial tcp: connection refused")},
		{"timeout", fmt.Errorf("context deadline exceeded")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if isNoMatchError(tc.err) {
				t.Errorf("isNoMatchError(%v) = true, want false", tc.err)
			}
		})
	}
}
