package cli

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRegistryHostFromPath(t *testing.T) {
	cases := map[string]string{
		"10.241.0.4/bnk-mirror":      "10.241.0.4",
		"image-registry.svc:5000/ns": "image-registry.svc:5000",
		"de.icr.io/bnk-mirror/sub":   "de.icr.io",
		"bare-host":                  "bare-host",
		"  10.241.0.4/bnk-mirror  ":  "10.241.0.4",
		"":                           "",
	}
	for in, want := range cases {
		if got := registryHostFromPath(in); got != want {
			t.Errorf("registryHostFromPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestCaptureRegistryCA_SelfSigned: an httptest TLS server presents a
// self-signed cert not in the system roots, so the verified dial fails and
// capture returns the served PEM (the private CA nodes must trust).
func TestCaptureRegistryCA_SelfSigned(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer ts.Close()

	host := strings.TrimPrefix(ts.URL, "https://")
	pem, err := captureRegistryCA(host)
	if err != nil {
		t.Fatalf("captureRegistryCA(%q) errored: %v", host, err)
	}
	if !strings.Contains(pem, "BEGIN CERTIFICATE") {
		t.Fatalf("expected a PEM certificate for a self-signed host, got %q", pem)
	}
}

// TestCaptureRegistryCA_Unreachable: a refused port yields a dial error, not a
// silent empty result (so replicate can warn the operator).
func TestCaptureRegistryCA_Unreachable(t *testing.T) {
	// Bind a port then close it so the OS refuses connects immediately
	// (deterministic + fast, unlike an arbitrary "probably unused" port).
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	if _, err := captureRegistryCA(addr); err == nil {
		t.Fatal("expected an error dialing a refused host, got nil")
	}
}
