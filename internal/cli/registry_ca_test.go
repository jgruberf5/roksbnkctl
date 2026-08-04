package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
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

// servedFingerprint is the SHA-256 of the root the test server presents — the
// value an operator would read off the box out of band and configure as the pin.
func servedFingerprint(t *testing.T, ts *httptest.Server) string {
	t.Helper()
	certs := ts.TLS.Certificates
	if len(certs) == 0 || len(certs[0].Certificate) == 0 {
		t.Fatal("test server presented no certificate")
	}
	der := certs[0].Certificate[len(certs[0].Certificate)-1]
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

// A self-signed host with a MATCHING pin is adopted: this is the supported path
// for a mirror you generated yourself.
func TestCaptureRegistryCA_PinnedSelfSigned(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "https://")

	pem, err := captureRegistryCA(host, caCaptureOpts{PinSHA256: servedFingerprint(t, ts)})
	if err != nil {
		t.Fatalf("pinned capture errored: %v", err)
	}
	if !strings.Contains(pem, "BEGIN CERTIFICATE") {
		t.Fatalf("expected a PEM certificate, got %q", pem)
	}
}

// The pin is accepted in the shapes people actually paste.
func TestCaptureRegistryCA_PinFormats(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "https://")
	fp := servedFingerprint(t, ts)

	colonned := ""
	for i := 0; i < len(fp); i += 2 {
		if colonned != "" {
			colonned += ":"
		}
		colonned += fp[i : i+2]
	}
	for _, form := range []string{
		fp,
		"sha256:" + fp,
		strings.ToUpper(colonned),
		"SHA256 Fingerprint=" + strings.ToUpper(colonned), // openssl x509 output
	} {
		if _, err := captureRegistryCA(host, caCaptureOpts{PinSHA256: form}); err != nil {
			t.Errorf("pin form %q rejected: %v", form, err)
		}
	}
}

// THE POINT OF THE CHANGE: a self-signed CA is NOT adopted without a pin, because
// it would be installed into every node's trust store.
func TestCaptureRegistryCA_UnpinnedSelfSignedRefused(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "https://")

	_, err := captureRegistryCA(host, caCaptureOpts{})
	if err == nil {
		t.Fatal("expected an unpinned self-signed capture to be refused")
	}
	if !errors.Is(err, errUnpinnedPrivateCA) {
		t.Fatalf("expected errUnpinnedPrivateCA, got %v", err)
	}
	// The refusal must name the fingerprint actually served, so a first-time
	// operator can record the pin without reaching for openssl.
	if !strings.Contains(err.Error(), servedFingerprint(t, ts)) {
		t.Errorf("refusal should quote the served fingerprint, got: %v", err)
	}
}

// A WRONG pin is rejected — the MITM case the alert is about.
func TestCaptureRegistryCA_PinMismatchRefused(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "https://")

	wrong := strings.Repeat("ab", sha256.Size)
	_, err := captureRegistryCA(host, caCaptureOpts{PinSHA256: wrong})
	if err == nil {
		t.Fatal("expected a mismatched pin to be refused")
	}
	if !errors.Is(err, errCAPinMismatch) {
		t.Fatalf("expected errCAPinMismatch, got %v", err)
	}
}

// The explicit opt-out still works, for operators who accept trust-on-first-use.
func TestCaptureRegistryCA_InsecureOptIn(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "https://")

	pem, err := captureRegistryCA(host, caCaptureOpts{AllowUnpinned: true})
	if err != nil {
		t.Fatalf("--insecure-capture-ca capture errored: %v", err)
	}
	if !strings.Contains(pem, "BEGIN CERTIFICATE") {
		t.Fatalf("expected a PEM certificate, got %q", pem)
	}
}

// An unreachable host yields a transport error, not a policy refusal — the caller
// treats those differently (transport is best-effort, policy is fatal).
func TestCaptureRegistryCA_Unreachable(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	addr := ln.Addr().String()
	ln.Close()

	_, err = captureRegistryCA(addr, caCaptureOpts{AllowUnpinned: true})
	if err == nil {
		t.Fatal("expected an error dialing a refused host, got nil")
	}
	if errors.Is(err, errUnpinnedPrivateCA) || errors.Is(err, errCAPinMismatch) {
		t.Fatalf("a refused connection must not surface as a policy error: %v", err)
	}
}

func TestNormalizeCAPin(t *testing.T) {
	want := "ab12cd34"
	for _, in := range []string{
		"ab12cd34", "AB12CD34", "sha256:ab12cd34", "ab:12:cd:34",
		"SHA256 Fingerprint=AB:12:CD:34", "  ab12cd34  ", "ab-12-cd-34",
	} {
		if got := normalizeCAPin(in); got != want {
			t.Errorf("normalizeCAPin(%q) = %q, want %q", in, got, want)
		}
	}
}

// pemRootFingerprint pins the ROOT (last cert), which is what lands in node trust.
func TestPemRootFingerprint(t *testing.T) {
	ts := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer ts.Close()
	host := strings.TrimPrefix(ts.URL, "https://")

	pemText, err := captureRegistryCA(host, caCaptureOpts{AllowUnpinned: true})
	if err != nil {
		t.Fatalf("capture errored: %v", err)
	}
	fp, err := pemRootFingerprint(pemText)
	if err != nil {
		t.Fatalf("pemRootFingerprint errored: %v", err)
	}
	if fp != servedFingerprint(t, ts) {
		t.Errorf("pemRootFingerprint = %q, want the served root %q", fp, servedFingerprint(t, ts))
	}
	if _, err := pemRootFingerprint("not a pem"); err == nil {
		t.Error("expected an error for non-PEM input")
	}
}
