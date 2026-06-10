package doctor

// Pin the Service Quotas feature-flag toggle. The doctor
// row is OFF by default; the operator opts in via
// AWSBNKCTL_DOCTOR_SERVICE_QUOTAS=1. These tests pin the accepted
// truthy values + the off-by-default invariant so a future refactor
// can't silently enable the live API probe.

import (
	"testing"
)

// TestServiceQuotasEnabled_OffByDefault pins the most-important
// invariant: with the env unset, the probe is OFF. Doctor falls back
// to the "default 5 instances / 80 vCPU" pointer in the existing vCPU
// quota row.
func TestServiceQuotasEnabled_OffByDefault(t *testing.T) {
	t.Setenv(serviceQuotasFeatureFlagEnv, "")
	if serviceQuotasEnabled() {
		t.Fatal("serviceQuotasEnabled() returned true with the env var unset; expected off-by-default")
	}
}

// TestServiceQuotasEnabled_AcceptedTruthyValues pins the four
// case-insensitive truthy spellings. Any one of `1`, `true`, `yes`,
// `on` flips the flag on; whitespace + casing don't matter.
func TestServiceQuotasEnabled_AcceptedTruthyValues(t *testing.T) {
	for _, v := range []string{"1", "true", "TRUE", "yes", "YES", "on", "ON", " true ", "\tyes\n"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(serviceQuotasFeatureFlagEnv, v)
			if !serviceQuotasEnabled() {
				t.Errorf("serviceQuotasEnabled() returned false for %q; expected true", v)
			}
		})
	}
}

// TestServiceQuotasEnabled_RejectsOtherValues pins the inverse —
// strings that aren't in the truthy set keep the probe off.
func TestServiceQuotasEnabled_RejectsOtherValues(t *testing.T) {
	for _, v := range []string{"0", "false", "no", "off", "maybe", "enabled"} {
		t.Run(v, func(t *testing.T) {
			t.Setenv(serviceQuotasFeatureFlagEnv, v)
			if serviceQuotasEnabled() {
				t.Errorf("serviceQuotasEnabled() returned true for %q; expected false", v)
			}
		})
	}
}
