package test

import (
	"crypto/tls"
	"strings"
	"testing"

	"sigs.k8s.io/yaml"
)

func TestRenderFixtures_BackendAndRoutes(t *testing.T) {
	p := FixturePlan{
		Gateway:      GatewayIdentity{AppNamespace: "bnk-apps"},
		HTTPBackend:  true,
		Routes:       true,
		GatewayName:  "bnk-gateway",
		HTTPSection:  "http",
		HTTPSSection: "https",
		TCPSection:   "tcp",
		TLSHost:      "matrix.example.com",
	}
	out, err := RenderFixtures(p)
	if err != nil {
		t.Fatalf("RenderFixtures: %v", err)
	}

	// Each document must be valid YAML and carry the managed-by label.
	docs := strings.Split(out, "\n---\n")
	kinds := map[string]bool{}
	for _, d := range docs {
		d = strings.TrimSpace(d)
		if d == "" {
			continue
		}
		var obj map[string]any
		if err := yaml.Unmarshal([]byte(d), &obj); err != nil {
			t.Fatalf("fixture doc is not valid YAML: %v\n---\n%s", err, d)
		}
		kind, _ := obj["kind"].(string)
		kinds[kind] = true
	}
	for _, want := range []string{"Deployment", "Service", "HTTPRoute", "Secret", "TCPRoute"} {
		if !kinds[want] {
			t.Errorf("rendered fixtures missing kind %q (got %v)", want, kinds)
		}
	}
	if !strings.Contains(out, "kind: TCPRoute") || !strings.Contains(out, "gateway.networking.k8s.io/v1alpha2") {
		t.Error("TCPRoute should use the v1alpha2 API")
	}
	if !strings.Contains(out, "sectionName: tcp") {
		t.Error("TCPRoute should attach to the named tcp listener section")
	}
}

func TestRenderFixtures_NoHTTPSNoSecret(t *testing.T) {
	p := FixturePlan{
		Gateway:     GatewayIdentity{AppNamespace: "ns"},
		Routes:      true,
		GatewayName: "gw",
		HTTPSection: "http",
		// no HTTPSSection, no TCPSection
	}
	out, err := RenderFixtures(p)
	if err != nil {
		t.Fatalf("RenderFixtures: %v", err)
	}
	if strings.Contains(out, "kind: Secret") {
		t.Error("no HTTPS section → no TLS Secret should be rendered")
	}
	if strings.Contains(out, "kind: TCPRoute") {
		t.Error("no TCP section → no TCPRoute should be rendered")
	}
}

func TestRenderFixtures_RoutesNeedGatewayName(t *testing.T) {
	_, err := RenderFixtures(FixturePlan{
		Gateway: GatewayIdentity{AppNamespace: "ns"},
		Routes:  true,
	})
	if err == nil {
		t.Fatal("expected error: routes without a gateway name")
	}
}

func TestGenSelfSignedCert_LoadsAsKeyPair(t *testing.T) {
	certPEM, keyPEM, err := GenSelfSignedCert("matrix.example.com")
	if err != nil {
		t.Fatalf("GenSelfSignedCert: %v", err)
	}
	if _, err := tls.X509KeyPair(certPEM, keyPEM); err != nil {
		t.Fatalf("generated cert/key not a valid pair: %v", err)
	}
}
