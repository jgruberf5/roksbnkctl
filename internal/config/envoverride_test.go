package config

import (
	"encoding/base64"
	"strings"
	"testing"
)

func TestOverrideFromEnv(t *testing.T) {
	// Isolate from the ambient environment: clear every mapped var, then set
	// only what each sub-case exercises.
	clearAll := func(t *testing.T) {
		for _, e := range []string{
			"IBMCLOUD_API_KEY", "ROKSBNKCTL_API_KEY_B64", "ROKSBNKCTL_PREFIX",
			"ROKSBNKCTL_REGION", "ROKSBNKCTL_RESOURCE_GROUP", "ROKSBNKCTL_TESTING_SSH_KEY_NAME",
			"ROKSBNKCTL_GENERIC_PASSWORD", "ROKSBNKCTL_LICENSE_MODE", "ROKSBNKCTL_FLP_NAMESPACE",
			"ROKSBNKCTL_REGISTRY_TARGET", "ROKSBNKCTL_GENERIC_HOST",
			"ROKSBNKCTL_GENERIC_REPO_PREFIX", "ROKSBNKCTL_GENERIC_USERNAME",
			"ROKSBNKCTL_FLP_EXTERNAL_URL", "ROKSBNKCTL_FLP_ROOT_CA_B64",
		} {
			t.Setenv(e, "")
		}
	}

	t.Run("raw api key is base64-encoded, label hides the value", func(t *testing.T) {
		clearAll(t)
		t.Setenv("IBMCLOUD_API_KEY", "raw-secret")
		ws := &Workspace{}
		applied := OverrideFromEnv(ws)
		want := base64.StdEncoding.EncodeToString([]byte("raw-secret"))
		if ws.IBMCloud.APIKeyB64 != want {
			t.Fatalf("api_key_b64 = %q, want %q", ws.IBMCloud.APIKeyB64, want)
		}
		for _, a := range applied {
			if strings.Contains(a, "raw-secret") || strings.Contains(a, want) {
				t.Errorf("override label leaks the secret: %q", a)
			}
		}
	})

	t.Run("pre-encoded ROKSBNKCTL_API_KEY_B64 wins over raw", func(t *testing.T) {
		clearAll(t)
		t.Setenv("IBMCLOUD_API_KEY", "raw")
		t.Setenv("ROKSBNKCTL_API_KEY_B64", "PREENC==")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.IBMCloud.APIKeyB64 != "PREENC==" {
			t.Fatalf("api_key_b64 = %q, want PREENC==", ws.IBMCloud.APIKeyB64)
		}
	})

	t.Run("scalars + ssh key (nil Resources gets allocated)", func(t *testing.T) {
		clearAll(t)
		t.Setenv("ROKSBNKCTL_PREFIX", "p1")
		t.Setenv("ROKSBNKCTL_REGION", "eu-de")
		t.Setenv("ROKSBNKCTL_RESOURCE_GROUP", "rg1")
		t.Setenv("ROKSBNKCTL_TESTING_SSH_KEY_NAME", "k1")
		ws := &Workspace{} // Resources is nil
		OverrideFromEnv(ws)
		if ws.Prefix != "p1" || ws.IBMCloud.Region != "eu-de" || ws.IBMCloud.ResourceGroup != "rg1" {
			t.Fatalf("scalars not applied: %+v", ws)
		}
		if ws.Resources == nil || ws.Resources.TestingSSHKeyName != "k1" {
			t.Fatalf("ssh key not applied: %+v", ws.Resources)
		}
	})

	t.Run("generic registry password is base64-encoded into a nil Registry", func(t *testing.T) {
		clearAll(t)
		t.Setenv("ROKSBNKCTL_GENERIC_PASSWORD", "art-token")
		ws := &Workspace{} // Registry is nil
		OverrideFromEnv(ws)
		want := base64.StdEncoding.EncodeToString([]byte("art-token"))
		if ws.Registry == nil || ws.Registry.GenericPasswordB64 != want {
			t.Fatalf("generic_password_b64 not applied: %+v", ws.Registry)
		}
	})

	t.Run("license mode f5licenseproxy seeds an flp block", func(t *testing.T) {
		clearAll(t)
		t.Setenv("ROKSBNKCTL_LICENSE_MODE", "f5licenseproxy")
		t.Setenv("ROKSBNKCTL_FLP_NAMESPACE", "flp-ns")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.LicenseMode != "f5licenseproxy" {
			t.Fatalf("license_mode = %q, want f5licenseproxy", ws.BNK.LicenseMode)
		}
		if ws.BNK.FLP == nil || ws.BNK.FLP.Namespace != "flp-ns" {
			t.Fatalf("flp block not seeded: %+v", ws.BNK.FLP)
		}
	})

	t.Run("no license-mode env leaves BNK untouched (JWT default)", func(t *testing.T) {
		clearAll(t)
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.LicenseMode != "" || ws.BNK.FLP != nil {
			t.Fatalf("absent env must not touch licensing: mode=%q flp=%+v", ws.BNK.LicenseMode, ws.BNK.FLP)
		}
	})

	t.Run("env wins over a seeded value", func(t *testing.T) {
		clearAll(t)
		t.Setenv("ROKSBNKCTL_PREFIX", "fromenv")
		ws := &Workspace{Prefix: "fromfile"}
		OverrideFromEnv(ws)
		if ws.Prefix != "fromenv" {
			t.Fatalf("prefix = %q, want fromenv (env must win)", ws.Prefix)
		}
	})

	t.Run("unset vars are inert", func(t *testing.T) {
		clearAll(t)
		ws := &Workspace{Prefix: "keep"}
		applied := OverrideFromEnv(ws)
		if len(applied) != 0 {
			t.Fatalf("applied = %v, want none", applied)
		}
		if ws.Prefix != "keep" {
			t.Fatalf("prefix clobbered to %q", ws.Prefix)
		}
	})
}

// TestOverrideFromEnv_CIPipelineSurface covers the variables a CI pipeline needs
// to run the shared-licensing topology with NO config file to template: where the
// registry is, and the proxy handoff from the job that owns the proxy.
func TestOverrideFromEnv_CIPipelineSurface(t *testing.T) {
	t.Run("registry target comes entirely from env", func(t *testing.T) {
		t.Setenv("ROKSBNKCTL_REGISTRY_TARGET", "generic")
		t.Setenv("ROKSBNKCTL_GENERIC_HOST", "harbor.example.com")
		t.Setenv("ROKSBNKCTL_GENERIC_REPO_PREFIX", "bnk-mirror")
		t.Setenv("ROKSBNKCTL_GENERIC_USERNAME", "admin")
		t.Setenv("ROKSBNKCTL_GENERIC_PASSWORD", "s3cret")

		// A workspace with NO registry block at all — the normal CI case.
		ws := &Workspace{}
		applied := OverrideFromEnv(ws)

		if ws.Registry == nil {
			t.Fatal("registry block was not created")
		}
		if ws.Registry.Target != "generic" {
			t.Errorf("target = %q, want generic", ws.Registry.Target)
		}
		if ws.Registry.GenericHost != "harbor.example.com" {
			t.Errorf("generic_host = %q", ws.Registry.GenericHost)
		}
		if ws.Registry.GenericRepoPrefix != "bnk-mirror" {
			t.Errorf("generic_repo_prefix = %q", ws.Registry.GenericRepoPrefix)
		}
		if ws.Registry.GenericUsername != "admin" {
			t.Errorf("generic_username = %q", ws.Registry.GenericUsername)
		}
		want := base64.StdEncoding.EncodeToString([]byte("s3cret"))
		if ws.Registry.GenericPasswordB64 != want {
			t.Errorf("password not base64-encoded: %q", ws.Registry.GenericPasswordB64)
		}
		for _, a := range applied {
			if strings.Contains(a, "s3cret") || strings.Contains(a, want) {
				t.Errorf("override label leaks the registry password: %q", a)
			}
		}
	})

	t.Run("foreign-proxy handoff on a config that never mentioned the FLP", func(t *testing.T) {
		// The nil-pointer case: bnk.flp and bnk.flp.external are both pointers, so
		// a pipeline setting ONLY these two must not panic.
		t.Setenv("ROKSBNKCTL_LICENSE_MODE", "f5licenseproxy")
		t.Setenv("ROKSBNKCTL_FLP_EXTERNAL_URL", "https://10.242.0.9:30001")
		t.Setenv("ROKSBNKCTL_FLP_ROOT_CA_B64", "TFMwdExTMUNSVWRK")

		ws := &Workspace{}
		OverrideFromEnv(ws)

		if ws.BNK.LicenseMode != "f5licenseproxy" {
			t.Fatalf("license_mode = %q", ws.BNK.LicenseMode)
		}
		if ws.BNK.FLP == nil || ws.BNK.FLP.External == nil {
			t.Fatal("bnk.flp.external was not created")
		}
		if got := ws.BNK.FLP.External.URL; got != "https://10.242.0.9:30001" {
			t.Errorf("external.url = %q", got)
		}
		// Already base64 (that is how `flp output flp_root_ca` emits it) — must be
		// stored VERBATIM, not re-encoded, or the CA the CWC gets is garbage.
		if got := ws.BNK.FLP.External.RootCAB64; got != "TFMwdExTMUNSVWRK" {
			t.Errorf("root_ca_b64 = %q, want the value verbatim (double-encoding it corrupts the CA)", got)
		}
	})

	t.Run("the handoff vars work with no license_mode set", func(t *testing.T) {
		// Order-independence: flpExternal must create the blocks itself, not rely
		// on ROKSBNKCTL_LICENSE_MODE having seeded them first.
		t.Setenv("ROKSBNKCTL_FLP_EXTERNAL_URL", "https://10.0.0.1:30001")
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.FLP == nil || ws.BNK.FLP.External == nil {
			t.Fatal("bnk.flp.external must be created without license_mode")
		}
	})
}
