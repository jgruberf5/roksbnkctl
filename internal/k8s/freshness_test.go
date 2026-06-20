package k8s

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"math/big"
	"testing"
	"time"
)

// makeJWT builds a syntactically valid JWT whose payload carries the given
// exp. Header and signature are dummies — MinExpiry only base64url-decodes
// the payload.
func makeJWT(t *testing.T, exp time.Time) string {
	t.Helper()
	hdr := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload, _ := json.Marshal(map[string]any{"exp": exp.Unix()})
	body := base64.RawURLEncoding.EncodeToString(payload)
	return hdr + "." + body + ".sig"
}

func tokenKubeconfig(jwt string) []byte {
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: https://x:1, certificate-authority-data: Q0E=}
users:
- name: c-token
  user: {token: %s}
`, jwt))
}

// makeCertKubeconfig builds a kubeconfig whose user has a client cert with
// the given NotAfter.
func makeCertKubeconfig(t *testing.T, notAfter time.Time) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "admin"},
		NotBefore:    notAfter.Add(-24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	b64 := base64.StdEncoding.EncodeToString(pemBytes)
	return []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: c
  cluster: {server: https://x:1, certificate-authority-data: Q0E=}
users:
- name: admin
  user: {client-certificate-data: %s, client-key-data: S0VZ}
`, b64))
}

func TestMinExpiry_TokenPastIsExpired(t *testing.T) {
	kc := tokenKubeconfig(makeJWT(t, time.Now().Add(-time.Hour)))
	exp, err := MinExpiry(kc)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) > 0 {
		t.Errorf("expected expired, got ttl %s", time.Until(exp))
	}
}

func TestMinExpiry_TokenFutureIsFresh(t *testing.T) {
	kc := tokenKubeconfig(makeJWT(t, time.Now().Add(48*time.Hour)))
	exp, err := MinExpiry(kc)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) < 24*time.Hour {
		t.Errorf("expected far-future expiry, got ttl %s", time.Until(exp))
	}
}

func TestMinExpiry_BadTokenTreatedExpired(t *testing.T) {
	kc := tokenKubeconfig("not-a-jwt")
	exp, err := MinExpiry(kc)
	if err != nil {
		t.Fatal(err)
	}
	if time.Until(exp) > 0 {
		t.Errorf("unparseable token should be treated as expired, got ttl %s", time.Until(exp))
	}
}

func TestMinExpiry_CertNotAfter(t *testing.T) {
	past := makeCertKubeconfig(t, time.Now().Add(-time.Hour))
	if exp, err := MinExpiry(past); err != nil || time.Until(exp) > 0 {
		t.Errorf("expired cert: ttl=%s err=%v, want expired", time.Until(exp), err)
	}
	future := makeCertKubeconfig(t, time.Now().Add(720*time.Hour))
	if exp, err := MinExpiry(future); err != nil || time.Until(exp) < time.Hour {
		t.Errorf("future cert: ttl=%s err=%v, want fresh", time.Until(exp), err)
	}
}

func TestMinExpiry_NoCredentialsErrors(t *testing.T) {
	kc := []byte("apiVersion: v1\nkind: Config\nusers: []\n")
	if _, err := MinExpiry(kc); err == nil {
		t.Fatal("expected error for kubeconfig with no credentials")
	}
}

func TestClassify(t *testing.T) {
	if got := Classify(tokenKubeconfig(makeJWT(t, time.Now()))); got != ClassToken {
		t.Errorf("token kubeconfig classified %v, want ClassToken", got)
	}
	if got := Classify(makeCertKubeconfig(t, time.Now())); got != ClassCert {
		t.Errorf("cert kubeconfig classified %v, want ClassCert", got)
	}
	if got := Classify([]byte("apiVersion: v1\nkind: Config\nusers: []\n")); got != ClassUnknown {
		t.Errorf("empty kubeconfig classified %v, want ClassUnknown", got)
	}
}
