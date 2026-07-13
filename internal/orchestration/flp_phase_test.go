package orchestration

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// writeBnkFLPOverride reads the FLP phase handoff (flp-outputs.json) and emits
// flp_license_server_url + the DECODED root-CA PEM as forced tfvars. This pins
// that read/decode and the actionable error when the handoff is missing.
func TestWriteBnkFLPOverride(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "flp-override-test"
	wsDir, err := config.WorkspaceDir(ws)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(wsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	pem := "-----BEGIN CERTIFICATE-----\nMIIBdummy\n-----END CERTIFICATE-----\n"
	if err := config.WriteFLPOutputs(ws, &config.FLPOutputs{
		RootCAB64: base64.StdEncoding.EncodeToString([]byte(pem)),
		Endpoint:  "https://f5-license-proxy.f5-license-proxy.svc.cluster.local:8443",
		Namespace: "f5-license-proxy",
	}); err != nil {
		t.Fatalf("WriteFLPOutputs: %v", err)
	}

	stateDir := t.TempDir()
	p, err := writeBnkFLPOverride(stateDir, ws, &config.Workspace{})
	if err != nil {
		t.Fatalf("writeBnkFLPOverride: %v", err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, `flp_license_server_url = "https://f5-license-proxy.f5-license-proxy.svc.cluster.local:8443"`) {
		t.Errorf("missing endpoint tfvar\n%s", got)
	}
	// The CA must be DECODED to PEM (not left base64).
	if !strings.Contains(got, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("root CA not decoded to PEM\n%s", got)
	}
}

func TestWriteBnkFLPOverride_MissingHandoffErrors(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	if _, err := writeBnkFLPOverride(t.TempDir(), "no-flp-here", &config.Workspace{}); err == nil {
		t.Fatal("expected an error when flp-outputs.json is absent")
	}
}

// A workspace can license against a FOREIGN proxy — one deployed by a different
// workspace/cluster (the shared-licensing-cluster topology). bnk.flp.external then
// supplies the endpoint + CA, and NO `flp up` (and no flp-outputs.json) is needed
// in this workspace.
func TestWriteBnkFLPOverride_ExternalProxy(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "consumer" // deliberately has NO flp-outputs.json

	caPEM := "-----BEGIN CERTIFICATE-----\nremote\n-----END CERTIFICATE-----\n"
	wsCfg := &config.Workspace{}
	wsCfg.BNK.FLP = &config.BNKFLPCfg{
		External: &config.BNKFLPExternalCfg{
			URL:       "https://10.240.64.5:30001",
			RootCAB64: base64.StdEncoding.EncodeToString([]byte(caPEM)),
		},
	}

	stateDir := t.TempDir()
	p, err := writeBnkFLPOverride(stateDir, ws, wsCfg)
	if err != nil {
		t.Fatalf("a foreign proxy must not require a local flp up: %v", err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, `flp_license_server_url = "https://10.240.64.5:30001"`) {
		t.Errorf("external URL not rendered:\n%s", got)
	}
	if !strings.Contains(got, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("external root CA not decoded into the override:\n%s", got)
	}
}

// An external block missing either half is an error naming the fix — a URL with no
// CA would leave the CWC unable to verify the proxy's certificate.
func TestWriteBnkFLPOverride_ExternalIncomplete(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	wsCfg := &config.Workspace{}
	wsCfg.BNK.FLP = &config.BNKFLPCfg{
		External: &config.BNKFLPExternalCfg{URL: "https://10.240.64.5:30001"}, // no CA
	}
	_, err := writeBnkFLPOverride(t.TempDir(), "consumer", wsCfg)
	if err == nil {
		t.Fatal("want an error when bnk.flp.external has a url but no root_ca_b64")
	}
	if !strings.Contains(err.Error(), "root_ca_b64") {
		t.Errorf("error should name the missing field, got: %v", err)
	}
}
