package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-containerregistry/pkg/authn"
)

// writeChartTgz builds a helm-chart-shaped .tgz at
// <dir>/f5-bigip-k8s-manifest-<version>.tgz containing the inner manifest YAML
// (plus a Chart.yaml decoy), mirroring the real artifact layout.
func writeChartTgz(t *testing.T, dir, version string, manifestBody []byte) string {
	t.Helper()
	root := fmt.Sprintf("f5-bigip-k8s-manifest-%s", version)
	entries := []struct {
		name string
		body []byte
	}{
		{root + "/Chart.yaml", []byte("apiVersion: v2\nname: f5-bigip-k8s-manifest\n")},
		{root + "/" + manifestYAMLName(version), manifestBody},
	}

	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// a directory entry, then the files — matches `helm pull` output shape.
	if err := tw.WriteHeader(&tar.Header{Name: root + "/", Typeflag: tar.TypeDir, Mode: 0o755}); err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if err := tw.WriteHeader(&tar.Header{Name: e.name, Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len(e.body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	p := filepath.Join(dir, fmt.Sprintf("f5-bigip-k8s-manifest-%s.tgz", version))
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractManifestYAML(t *testing.T) {
	dir := t.TempDir()
	version := "9.9.9-test.0"
	want := []byte("f5_helm_repo: oci://repo.f5.com\nf5_docker_repo: repo.f5.com\nreleases: []\n")
	tgz := writeChartTgz(t, dir, version, want)

	got, err := extractManifestYAML(tgz, version)
	if err != nil {
		t.Fatalf("extractManifestYAML: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted bytes = %q, want %q", got, want)
	}
}

func TestExtractManifestYAML_Missing(t *testing.T) {
	dir := t.TempDir()
	// Build an archive for one version, ask for another → not found.
	tgz := writeChartTgz(t, dir, "1.0.0", []byte("x: y\n"))
	if _, err := extractManifestYAML(tgz, "2.0.0"); err == nil {
		t.Fatal("want error for missing manifest YAML, got nil")
	}
}

func TestFarHost(t *testing.T) {
	cases := map[string]string{
		"":                              FARHost,
		"repo.f5.com":                   "repo.f5.com",
		"oci://repo.f5.com":             "repo.f5.com",
		"oci://repo.f5.com/release":     "repo.f5.com",
		"https://my.registry.local:443": "my.registry.local:443",
		"my.mirror.io/charts":           "my.mirror.io",
	}
	for in, want := range cases {
		if got := farHost(in); got != want {
			t.Errorf("farHost(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSourceAuth(t *testing.T) {
	// With a service account: FAR host gets a _json_key_base64 Basic cred,
	// other hosts get nil (anonymous).
	resolve := SourceAuth("", "c2EtanNvbg==")
	a := resolve(FARHost)
	if a == nil {
		t.Fatalf("SourceAuth(FARHost) = nil, want a credential")
	}
	basic, ok := a.(*authn.Basic)
	if !ok {
		t.Fatalf("SourceAuth(FARHost) = %T, want *authn.Basic", a)
	}
	if basic.Username != jsonKeyUser || basic.Password != "c2EtanNvbg==" {
		t.Errorf("SourceAuth basic = %+v, want user=%q pass=base64", basic, jsonKeyUser)
	}
	if got := resolve("quay.io"); got != nil {
		t.Errorf("SourceAuth(quay.io) = %v, want nil (anonymous)", got)
	}

	// Without a service account: even the FAR host is anonymous.
	if got := SourceAuth("", "")(FARHost); got != nil {
		t.Errorf("SourceAuth with no SA: FAR host = %v, want nil", got)
	}
}

func TestDecodeServiceAccount(t *testing.T) {
	raw, err := DecodeServiceAccount("eyJrIjoidiJ9") // {"k":"v"}
	if err != nil {
		t.Fatalf("DecodeServiceAccount: %v", err)
	}
	if string(raw) != `{"k":"v"}` {
		t.Errorf("decoded = %q", raw)
	}
	if _, err := DecodeServiceAccount("!!!not-base64!!!"); err == nil {
		t.Error("want error on malformed base64, got nil")
	}
}
