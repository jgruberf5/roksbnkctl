package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// ─────────────────────────────────────────────────────────────────────
// Sprint 27 — install-mode render (validator Issue 1, last bullet).
//
// Staff added BNKCfg.CRMode (internal/config/workspace.go) and wired
// renderBNKFields (internal/tf/vars.go) to emit `bnk_cr_mode = "<v>"`
// ONLY when CRMode is set. An unset value lets the upstream TF default
// (kubectl) stand, keeping older configs byte-identical. The `--legacy-bnk`
// flag sets CRMode = "legacy_curl" at runtime (internal/orchestration).
//
// These cases pin:
//   - CRMode == "" → NO bnk_cr_mode line (default kubectl, byte-compat),
//   - CRMode == "kubectl" → bnk_cr_mode = "kubectl",
//   - CRMode == "legacy_curl" → bnk_cr_mode = "legacy_curl",
//   - the line is emitted exactly once and on BOTH render paths
//     (sparse / empty-Prefix and full / prefix-driven),
//   - the api key is never rendered.
// Mirrors the existing vars_test.go patterns.
// ─────────────────────────────────────────────────────────────────────

func TestRenderTFVars_CRMode_UnsetOmitsLine(t *testing.T) {
	// Default (unset) CRMode must NOT emit bnk_cr_mode — older configs stay
	// byte-identical and the upstream TF default (kubectl) stands.
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster:  config.ClusterCfg{Create: true, Name: "bnk-demo"},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	if strings.Contains(buf.String(), "bnk_cr_mode") {
		t.Errorf("unset CRMode must NOT emit bnk_cr_mode (default kubectl, byte-compat)\noutput:\n%s", buf.String())
	}
}

func TestRenderTFVars_CRMode_Kubectl(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster:  config.ClusterCfg{Create: true, Name: "bnk-demo"},
		BNK:      config.BNKCfg{CRMode: "kubectl"},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `bnk_cr_mode = "kubectl"`) {
		t.Errorf("CRMode=kubectl must emit bnk_cr_mode = \"kubectl\"\noutput:\n%s", out)
	}
	// Emitted exactly once — an in-file dup is a terraform error.
	if n := strings.Count(out, "bnk_cr_mode ="); n != 1 {
		t.Errorf("bnk_cr_mode emitted %d times, want exactly 1\noutput:\n%s", n, out)
	}
	if strings.Contains(out, "api_key") {
		t.Errorf("api_key leaked into tfvars\noutput:\n%s", out)
	}
}

func TestRenderTFVars_CRMode_LegacyCurl(t *testing.T) {
	// This is the `--legacy-bnk` runtime path: orchestration sets
	// CRMode = "legacy_curl" and the render carries it to the tfvar that
	// selects the null_resource/curl/time_sleep baseline.
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster:  config.ClusterCfg{Create: true, Name: "bnk-demo"},
		BNK:      config.BNKCfg{CRMode: "legacy_curl"},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `bnk_cr_mode = "legacy_curl"`) {
		t.Errorf("CRMode=legacy_curl must emit bnk_cr_mode = \"legacy_curl\"\noutput:\n%s", out)
	}
	if n := strings.Count(out, "bnk_cr_mode ="); n != 1 {
		t.Errorf("bnk_cr_mode emitted %d times, want exactly 1\noutput:\n%s", n, out)
	}
}

// TestRenderTFVars_CRMode_FullRenderPath pins that the toggle also flows
// through the Sprint 26 prefix-driven (full) render — renderBNKFields is
// shared by both bodies, so a prefix-set workspace must emit it too, exactly
// once, with no duplicate.
func TestRenderTFVars_CRMode_FullRenderPath(t *testing.T) {
	ws := fullRenderWorkspace("demo")
	ws.BNK.CRMode = "legacy_curl"

	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `bnk_cr_mode = "legacy_curl"`) {
		t.Errorf("full render missing bnk_cr_mode = \"legacy_curl\"\noutput:\n%s", out)
	}
	// No in-file duplicate of any variable (incl. bnk_cr_mode).
	assertEachVarOnce(t, out)
}
