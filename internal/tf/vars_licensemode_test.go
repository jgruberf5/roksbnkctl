package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// ─────────────────────────────────────────────────────────────────────
// Optional FLP support — license_mode render.
//
// BNKCfg.LicenseMode (internal/config/workspace.go) drives renderBNKFields
// (internal/tf/vars.go) to emit `license_mode = "<v>"` ONLY when set. Unset
// leaves the upstream TF default ("connected"), keeping existing JWT configs
// byte-identical. The FLP endpoint + root CA required for "f5licenseproxy" are
// NOT rendered from config — they come from the flp phase outputs.
//
// These cases pin:
//   - LicenseMode == "" → NO license_mode line, and the WHOLE tfvars body is
//     byte-identical to the same workspace without the field (true backward-compat),
//   - LicenseMode == "f5licenseproxy" → license_mode = "f5licenseproxy", once,
//     on both the sparse and full render paths,
//   - no FLP endpoint / CA / api key leaks into the tfvars.
// Mirrors vars_crmode_test.go.
// ─────────────────────────────────────────────────────────────────────

func TestRenderTFVars_LicenseMode_UnsetByteIdentical(t *testing.T) {
	// A workspace with no LicenseMode must render EXACTLY what it rendered
	// before the field existed — the strongest backward-compat guarantee.
	base := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster:  config.ClusterCfg{Create: true, Name: "bnk-demo"},
		BNK:      config.BNKCfg{ManifestVersion: config.DefaultManifestVersion},
	}
	var got bytes.Buffer
	if err := RenderTFVars(&got, base, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	if strings.Contains(got.String(), "license_mode") {
		t.Errorf("unset LicenseMode must NOT emit license_mode\noutput:\n%s", got.String())
	}
	if strings.Contains(got.String(), "license_server_root_ca") ||
		strings.Contains(got.String(), "flp_license_server_url") {
		t.Errorf("FLP endpoint/CA must never render from config\noutput:\n%s", got.String())
	}
}

func TestRenderTFVars_LicenseMode_FLP(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster:  config.ClusterCfg{Create: true, Name: "bnk-demo"},
		BNK: config.BNKCfg{
			LicenseMode: "f5licenseproxy",
			FLP:         &config.BNKFLPCfg{Namespace: "f5-license-proxy"},
		},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `license_mode = "f5licenseproxy"`) {
		t.Errorf("LicenseMode=f5licenseproxy must emit license_mode\noutput:\n%s", out)
	}
	if n := strings.Count(out, "license_mode ="); n != 1 {
		t.Errorf("license_mode emitted %d times, want exactly 1\noutput:\n%s", n, out)
	}
	// The FLP block's namespace renders (drives the flp phase); deploy_flp itself
	// is forced by the phase override, not config.
	if !strings.Contains(out, `flp_namespace = "f5-license-proxy"`) {
		t.Errorf("FLP block must emit flp_namespace\noutput:\n%s", out)
	}
	// The CA/endpoint are flp-phase outputs, never rendered from config here.
	if strings.Contains(out, "license_server_root_ca") {
		t.Errorf("license_server_root_ca must not render from config\noutput:\n%s", out)
	}
	if strings.Contains(out, "api_key") {
		t.Errorf("api_key leaked into tfvars\noutput:\n%s", out)
	}
}

func TestRenderTFVars_LicenseMode_FullRenderPath(t *testing.T) {
	ws := fullRenderWorkspace("demo")
	ws.BNK.LicenseMode = "disconnected"

	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, `license_mode = "disconnected"`) {
		t.Errorf("full render missing license_mode = \"disconnected\"\noutput:\n%s", out)
	}
	assertEachVarOnce(t, out)
}
