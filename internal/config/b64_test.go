package config

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"
)

func TestDecodeB64Field(t *testing.T) {
	want := "hello config"
	enc := base64.StdEncoding.EncodeToString([]byte(want))

	t.Run("plain value decodes", func(t *testing.T) {
		got, err := DecodeB64Field("some.field", enc)
		if err != nil || string(got) != want {
			t.Fatalf("got %q, %v", got, err)
		}
	})

	t.Run("line-wrapped and padded values decode", func(t *testing.T) {
		// GNU `base64` wraps its output at 76 columns; env plumbing adds
		// leading/trailing whitespace. Neither may fail the decode.
		for _, in := range []string{wrap76(enc), " " + enc + "\n", "\t" + wrap76(enc) + "\n"} {
			got, err := DecodeB64Field("some.field", in)
			if err != nil || string(got) != want {
				t.Fatalf("DecodeB64Field(%q) = %q, %v", in, got, err)
			}
		}
	})

	t.Run("the error names the field", func(t *testing.T) {
		_, err := DecodeB64Field("bnkforge.ca_b64", "!!!not base64!!!")
		if err == nil || !strings.Contains(err.Error(), "bnkforge.ca_b64") {
			t.Fatalf("error must name the field: %v", err)
		}
	})
}

// wrap76 re-wraps s at 76 columns, the way GNU `base64` emits it.
func wrap76(s string) string {
	var b strings.Builder
	for len(s) > 76 {
		b.WriteString(s[:76])
		b.WriteByte('\n')
		s = s[76:]
	}
	b.WriteString(s)
	return b.String()
}

// testCAPEM returns a minimal self-signed certificate in PEM, for tests that
// need a value x509.CertPool.AppendCertsFromPEM accepts.
func testCAPEM(t *testing.T) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "roksbnkctl test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
