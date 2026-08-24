package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #185. bnk.gateway_api_bundle_url is acted on by the Go BNK phase, not by
// terraform — but it is still RENDERED, and that is not decoration: terraform's
// validation block is what rejects a malformed URL at plan time, and it can only
// do that on a value it was given. Unrendered, the typo survives until the fetch
// fails mid-apply with the admission sweep already running.
func TestRenderTFVars_GatewayAPIBundleURL(t *testing.T) {
	ws := &config.Workspace{
		Prefix:   "tf",
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
		BNK: config.BNKCfg{
			ManifestVersion:     "2.4.0-3.2600.1-0.0.1",
			GatewayAPIMTLS:      true,
			GatewayAPIBundleURL: "https://proxy.internal/gw/standard-install.yaml",
		},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	want := `gateway_api_bundle_url = "https://proxy.internal/gw/standard-install.yaml"`
	if !strings.Contains(buf.String(), want) {
		t.Errorf("rendered tfvars missing %q, got:\n%s", want, buf.String())
	}
}

// Unset renders nothing, so the terraform default ("" — derive the upstream
// release) applies and an untouched workspace's tfvars are byte-identical to
// what they were.
func TestRenderTFVars_GatewayAPIBundleURLUnsetRendersNothing(t *testing.T) {
	ws := &config.Workspace{
		Prefix:   "tf",
		IBMCloud: config.IBMCloudCfg{Region: "us-south"},
		BNK:      config.BNKCfg{ManifestVersion: "2.4.0-3.2600.1-0.0.1", GatewayAPIMTLS: true},
	}
	var buf bytes.Buffer
	if err := RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	if strings.Contains(buf.String(), "gateway_api_bundle_url") {
		t.Errorf("an unset bundle URL rendered a tfvar:\n%s", buf.String())
	}
}
