package source

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"
)

func writeTGZ(t *testing.T, files map[string]string) string {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "far-auth.tgz")
	if err := os.WriteFile(p, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestExtractServiceAccountFromTarball(t *testing.T) {
	// The single .json entry is the SA; surrounding whitespace is trimmed;
	// non-json entries are ignored.
	p := writeTGZ(t, map[string]string{
		"README.txt":       "ignore me",
		"cne_pull_64.json": "  eyJ0eXAiOiJKV1QifQ==  \n",
	})
	sa, err := ExtractServiceAccountFromTarball(p)
	if err != nil {
		t.Fatal(err)
	}
	if sa != "eyJ0eXAiOiJKV1QifQ==" {
		t.Errorf("SA = %q, want the trimmed base64", sa)
	}

	// No .json → a clear error.
	if _, err := ExtractServiceAccountFromTarball(writeTGZ(t, map[string]string{"only.txt": "x"})); err == nil {
		t.Error("want an error when the tarball has no .json")
	}

	// Empty .json → error.
	if _, err := ExtractServiceAccountFromTarball(writeTGZ(t, map[string]string{"sa.json": "   "})); err == nil {
		t.Error("want an error when the .json is empty")
	}
}
