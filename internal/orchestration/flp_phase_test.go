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
	p, err := writeBnkFLPOverride(stateDir, ws)
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
	if _, err := writeBnkFLPOverride(t.TempDir(), "no-flp-here"); err == nil {
		t.Fatal("expected an error when flp-outputs.json is absent")
	}
}
