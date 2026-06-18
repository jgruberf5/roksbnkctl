package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// clearAPIKeyEnv blanks every env var the resolver consults so a test can drive
// the non-env sources deterministically.
func clearAPIKeyEnv(t *testing.T) {
	t.Helper()
	for _, v := range []string{
		"IBMCLOUD_API_KEY", "IC_API_KEY",
		"TF_VAR_ibmcloud_api_key", "TF_VAR_IBMCLOUD_API_KEY", "TF_VAR_IC_API_KEY",
	} {
		t.Setenv(v, "")
	}
}

// TestRunAPIKey_PrintsResolvedKey pins that `roksbnkctl apikey` resolves and
// prints the key (here via the env source — the chain's first step, which also
// covers any key roksbnkctl loaded from $PWD/.env).
func TestRunAPIKey_PrintsResolvedKey(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if err := config.SaveWorkspace("demo", &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent("demo"); err != nil {
		t.Fatal(err)
	}
	clearAPIKeyEnv(t)
	t.Setenv("IBMCLOUD_API_KEY", "env-secret-123")

	old := flagWorkspace
	flagWorkspace = ""
	t.Cleanup(func() { flagWorkspace = old })

	var buf bytes.Buffer
	cmd := &cobra.Command{}
	cmd.SetOut(&buf)
	if err := runAPIKey(cmd, nil); err != nil {
		t.Fatalf("runAPIKey: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "env-secret-123" {
		t.Errorf("apikey = %q, want env-secret-123", got)
	}
}

// TestRunAPIKey_NoKeyErrors pins the non-interactive contract: with no key in
// any source, the command errors (never prompts). A unique workspace name keeps
// the keychain lookup a guaranteed miss.
func TestRunAPIKey_NoKeyErrors(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "demo-apikey-absent"
	if err := config.SaveWorkspace(ws, &config.Workspace{}); err != nil {
		t.Fatal(err)
	}
	if err := config.SetCurrent(ws); err != nil {
		t.Fatal(err)
	}
	clearAPIKeyEnv(t)

	old := flagWorkspace
	flagWorkspace = ""
	t.Cleanup(func() { flagWorkspace = old })

	cmd := &cobra.Command{}
	cmd.SetOut(&bytes.Buffer{})
	if err := runAPIKey(cmd, nil); err == nil {
		t.Error("want an error when no key is resolvable (non-interactive)")
	}
}
