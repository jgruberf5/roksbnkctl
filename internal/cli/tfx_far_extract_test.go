package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

// writeFARTarball builds a minimal FAR auth tarball containing one .json entry.
func writeFARTarball(t *testing.T, dir, saJSON string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// a non-json entry that must be ignored, then the service-account json
	for _, e := range []struct{ name, body string }{
		{"README", "ignore me"},
		{"cne_pull_64.json", saJSON},
	} {
		_ = tw.WriteHeader(&tar.Header{Name: e.name, Mode: 0o600, Size: int64(len(e.body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(e.body))
	}
	_ = tw.Close()
	_ = gz.Close()
	p := filepath.Join(dir, "f5-far-auth-key.tgz")
	if err := os.WriteFile(p, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunTFXFarExtract(t *testing.T) {
	dir := t.TempDir()
	const sa = `{"_json_key_base64":"abc123"}`
	tgz := writeFARTarball(t, dir, sa)

	flagFarTarball = tgz
	flagFarOut = filepath.Join(dir, "sub", "far-sa.json") // sub/ must be created
	if err := runTFXFarExtract(nil, nil); err != nil {
		t.Fatalf("far-extract failed: %v", err)
	}
	got, err := os.ReadFile(flagFarOut)
	if err != nil {
		t.Fatalf("output not written: %v", err)
	}
	if string(got) != sa {
		t.Errorf("extracted SA = %q, want %q", got, sa)
	}
}

func TestRunTFXFarExtract_MissingFlags(t *testing.T) {
	flagFarTarball, flagFarOut = "", ""
	if err := runTFXFarExtract(nil, nil); err == nil {
		t.Error("missing --tarball/--out should error")
	}
}
