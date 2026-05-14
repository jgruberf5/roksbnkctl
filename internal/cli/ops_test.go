package cli

// Sprint 9 / PRD 04 — `roksbnkctl ops install --trusted-profile` flag
// validation tests. The flag has three valid values (auto|on|off);
// anything else must error at flag-parse / PreRunE time so users get
// actionable feedback before any IBM Cloud / kubernetes API round-trip.

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

func TestValidateTrustedProfileFlag(t *testing.T) {
	cases := []struct {
		v       string
		wantErr bool
	}{
		{"auto", false},
		{"on", false},
		{"off", false},
		{"", true},
		{"yes", true},
		{"true", true},
		{"AUTO", true}, // case-sensitive — keep the user honest
		{"ON", true},
	}
	for _, tc := range cases {
		err := validateTrustedProfileFlag(tc.v)
		if tc.wantErr && err == nil {
			t.Errorf("validateTrustedProfileFlag(%q): expected error, got nil", tc.v)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("validateTrustedProfileFlag(%q): unexpected error: %v", tc.v, err)
		}
		if tc.wantErr && err != nil {
			// Error message must name the flag + the offending value so
			// users see what to fix.
			if !strings.Contains(err.Error(), "trusted-profile") {
				t.Errorf("error %q should mention --trusted-profile", err)
			}
		}
	}
}

// TestOpsInstallCmd_TrustedProfileFlag_RegisteredOnInstall asserts the
// flag is wired only on `ops install` (not on `ops show` /
// `ops uninstall`). Catches accidental flag-on-parent-cmd cobra
// mis-wiring that would let users pass `--trusted-profile=off` to
// `ops uninstall` and get a confusing no-op.
func TestOpsInstallCmd_TrustedProfileFlag_RegisteredOnInstall(t *testing.T) {
	f := opsInstallCmd.Flags().Lookup("trusted-profile")
	if f == nil {
		t.Fatal("expected --trusted-profile on opsInstallCmd")
	}
	if f.DefValue != "auto" {
		t.Errorf("--trusted-profile default: got %q, want %q", f.DefValue, "auto")
	}

	if opsShowCmd.Flags().Lookup("trusted-profile") != nil {
		t.Errorf("--trusted-profile should NOT be on opsShowCmd")
	}
	if opsUninstallCmd.Flags().Lookup("trusted-profile") != nil {
		t.Errorf("--trusted-profile should NOT be on opsUninstallCmd")
	}
}

// TestOpsInstallCmd_PreRunE_RejectsInvalidFlag exercises the PreRunE
// path the cobra runtime calls before RunE. An invalid value must
// produce a clear error from PreRunE; the install body must NOT run.
func TestOpsInstallCmd_PreRunE_RejectsInvalidFlag(t *testing.T) {
	prev := flagTrustedProfile
	t.Cleanup(func() { flagTrustedProfile = prev })

	flagTrustedProfile = "bogus"
	if err := opsInstallCmd.PreRunE(opsInstallCmd, nil); err == nil {
		t.Error("expected PreRunE to reject 'bogus'; got nil")
	}

	flagTrustedProfile = "auto"
	if err := opsInstallCmd.PreRunE(opsInstallCmd, nil); err != nil {
		t.Errorf("PreRunE on 'auto' should not error; got %v", err)
	}
}

// — Sprint 10 / PRD 04 §"Resolved in Sprint 9" closure: manifest
// renderer's `IAM_PROFILE_ID` env injection. The renderer threads the
// trusted-profile ID through to the pod spec only on the trusted-
// profile path (auto/on success); the static-key path (off, fallback)
// leaves the env entry out so the ibmcloud-login wrap branches to
// `--apikey`.

// opsPodFromManifests pulls the ops Pod object out of the decoded
// manifest set. Returns nil if no Pod is in the list (would mean the
// manifest renderer regressed).
func opsPodFromManifests(t *testing.T, apiKey, iamProfileID string) *corev1.Pod {
	t.Helper()
	objs, err := decodeOpsManifests(apiKey, iamProfileID)
	if err != nil {
		t.Fatalf("decodeOpsManifests(apiKey=%q, iamProfileID=%q): %v", apiKey, iamProfileID, err)
	}
	for _, o := range objs {
		if pod, ok := o.(*corev1.Pod); ok {
			return pod
		}
	}
	t.Fatalf("no Pod found among %d decoded objects", len(objs))
	return nil
}

// podHasEnv returns (value, present) for the named env var on the pod's
// first container. The ops pod is single-container; we don't need to
// scan multiple containers.
func podHasEnv(t *testing.T, pod *corev1.Pod, name string) (string, bool) {
	t.Helper()
	if len(pod.Spec.Containers) == 0 {
		t.Fatal("ops pod has no containers")
	}
	for _, e := range pod.Spec.Containers[0].Env {
		if e.Name == name {
			return e.Value, true
		}
	}
	return "", false
}

// TestDecodeOpsManifests_TrustedProfile_InjectsIAMProfileID — under the
// trusted-profile auto/on success path the renderer must inject
// `IAM_PROFILE_ID=<id>` into the ops pod spec so the in-pod ibmcloud
// login wrap branches to the
// `--cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID"`
// form (replaced Sprint 9's non-existent trusted-profile flag during
// Sprint 10's wrap-script blocker fix). Closes Sprint 9 staff Issue 2's
// manifest side.
func TestDecodeOpsManifests_TrustedProfile_InjectsIAMProfileID(t *testing.T) {
	const profileID = "iam-Profile-9f2example"
	pod := opsPodFromManifests(t, "" /* empty key on trusted-profile path */, profileID)
	v, ok := podHasEnv(t, pod, "IAM_PROFILE_ID")
	if !ok {
		t.Fatalf("trusted-profile path: pod spec missing IAM_PROFILE_ID env entry")
	}
	if v != profileID {
		t.Errorf("IAM_PROFILE_ID env: got %q, want %q", v, profileID)
	}
}

// TestDecodeOpsManifests_StaticKey_NoIAMProfileID — under the static-key
// path (off, fallback), the manifest MUST NOT carry the
// `IAM_PROFILE_ID` env entry; the wrap branches on its absence to the
// v1.0.x `--apikey` form.
func TestDecodeOpsManifests_StaticKey_NoIAMProfileID(t *testing.T) {
	pod := opsPodFromManifests(t, "test-api-key", "" /* no trusted profile */)
	if v, ok := podHasEnv(t, pod, "IAM_PROFILE_ID"); ok {
		t.Errorf("static-key path: pod spec should not carry IAM_PROFILE_ID, got %q", v)
	}
	// HOME stays — pre-existing env entry the trusted-profile addition
	// must not displace.
	if _, ok := podHasEnv(t, pod, "HOME"); !ok {
		t.Errorf("static-key path: HOME env entry regressed (manifest renderer corruption)")
	}
}

// TestDecodeOpsManifests_TrustedProfile_HOMEStillPresent — the
// trusted-profile path must preserve the HOME env entry. Catches a
// regression where the placeholder substitution accidentally replaces
// the HOME entry instead of being appended.
func TestDecodeOpsManifests_TrustedProfile_HOMEStillPresent(t *testing.T) {
	pod := opsPodFromManifests(t, "", "iam-Profile-9f2example")
	v, ok := podHasEnv(t, pod, "HOME")
	if !ok {
		t.Fatalf("HOME env entry missing under trusted-profile path")
	}
	if v != "/tmp" {
		t.Errorf("HOME env: got %q, want %q", v, "/tmp")
	}
}
