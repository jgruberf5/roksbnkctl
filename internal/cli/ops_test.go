package cli

// Sprint 9 / PRD 04 — `roksbnkctl ops install --trusted-profile` flag
// validation tests. The flag has three valid values (auto|on|off);
// anything else must error at flag-parse / PreRunE time so users get
// actionable feedback before any IBM Cloud / kubernetes API round-trip.

import (
	"strings"
	"testing"
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
