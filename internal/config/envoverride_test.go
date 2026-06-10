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
