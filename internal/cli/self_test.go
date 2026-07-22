package cli

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeTag(t *testing.T) {
	cases := map[string]string{
		"":         "",
		"  ":       "",
		"1.20.1":   "v1.20.1",
		"v1.20.1":  "v1.20.1",
		" v1.2.0 ": "v1.2.0",
		" 1.2.0 ":  "v1.2.0",
	}
	for in, want := range cases {
		if got := normalizeTag(in); got != want {
			t.Errorf("normalizeTag(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSameVersion(t *testing.T) {
	// goreleaser stamps Version without the leading v; tag names carry it.
	if !sameVersion("1.20.1", "v1.20.1") {
		t.Error("sameVersion should ignore a leading v mismatch")
	}
	if !sameVersion("v1.20.1", "1.20.1") {
		t.Error("sameVersion should be symmetric about the v")
	}
	if sameVersion("1.20.0", "v1.20.1") {
		t.Error("distinct versions must not compare equal")
	}
}

func TestExtractFromTarGz(t *testing.T) {
	want := []byte("\x7fELF-unix-binary")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	// LICENSE first, so the extractor must skip past it to the binary.
	writeTar(t, tw, "LICENSE", []byte("MIT"))
	writeTar(t, tw, "roksbnkctl", want)
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractFromTarGz(buf.Bytes(), "roksbnkctl")
	if err != nil {
		t.Fatalf("extractFromTarGz: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted %q, want %q", got, want)
	}

	if _, err := extractFromTarGz(buf.Bytes(), "nope"); err == nil {
		t.Error("expected error when the wanted binary is absent")
	}
}

func TestExtractFromZip(t *testing.T) {
	want := []byte("MZ-windows-binary")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	writeZip(t, zw, "README.md", []byte("readme"))
	writeZip(t, zw, "roksbnkctl.exe", want)
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}

	got, err := extractFromZip(buf.Bytes(), "roksbnkctl.exe")
	if err != nil {
		t.Fatalf("extractFromZip: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("extracted %q, want %q", got, want)
	}

	if _, err := extractFromZip(buf.Bytes(), "roksbnkctl"); err == nil {
		t.Error("expected error when the wanted binary is absent")
	}
}

// TestInstallByMoveAside exercises the Windows-safe replace strategy portably:
// it's pure file operations, so it runs the same on any OS. (On a real Windows
// host the .old removal at the end is a no-op while the process runs; here the
// file isn't locked, so it's cleaned up — either way target ends up swapped.)
func TestInstallByMoveAside(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "roksbnkctl.exe")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}
	staged := filepath.Join(dir, ".staged")
	if err := os.WriteFile(staged, []byte("NEW"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := installByMoveAside(target, staged); err != nil {
		t.Fatalf("installByMoveAside: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEW" {
		t.Errorf("target content = %q, want NEW", got)
	}
	if _, err := os.Stat(staged); !os.IsNotExist(err) {
		t.Error("staged file should have been renamed away")
	}
}

// TestReplaceBinaryUnixPath covers the end-to-end stage-and-install on the unix
// (atomic rename) path — the default on the Linux test host.
func TestReplaceBinaryUnixPath(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "roksbnkctl")
	if err := os.WriteFile(target, []byte("OLD"), 0o755); err != nil {
		t.Fatal(err)
	}

	if err := replaceBinary(target, []byte("NEWBINARY")); err != nil {
		t.Fatalf("replaceBinary: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "NEWBINARY" {
		t.Errorf("target content = %q, want NEWBINARY", got)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o100 == 0 {
		t.Errorf("installed binary is not executable: mode %v", info.Mode())
	}
	// No stray temp/staged files left in the dir.
	entries, _ := os.ReadDir(dir)
	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("expected only the target to remain, got %v", names)
	}
}

func TestChecksumFor(t *testing.T) {
	// checksumFor pulls the line matching the asset name out of a
	// goreleaser-style checksums.txt served over HTTP; test the parse via a
	// local server so no network is needed.
	body := "aaaa1111  roksbnkctl_1.20.1_linux_amd64.tar.gz\n" +
		"bbbb2222  roksbnkctl_1.20.1_windows_amd64.zip\n"
	srv := newStringServer(t, body)
	defer srv.Close()

	got, err := checksumFor(t.Context(), srv.URL, "roksbnkctl_1.20.1_windows_amd64.zip")
	if err != nil {
		t.Fatalf("checksumFor: %v", err)
	}
	if got != "bbbb2222" {
		t.Errorf("checksum = %q, want bbbb2222", got)
	}
	if _, err := checksumFor(t.Context(), srv.URL, "missing.tar.gz"); err == nil {
		t.Error("expected error for an absent filename")
	}
}

// ── helpers ─────────────────────────────────────────────────────────

func writeTar(t *testing.T, tw *tar.Writer, name string, data []byte) {
	t.Helper()
	if err := tw.WriteHeader(&tar.Header{
		Name: name, Mode: 0o755, Size: int64(len(data)), Typeflag: tar.TypeReg,
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatal(err)
	}
}

func writeZip(t *testing.T, zw *zip.Writer, name string, data []byte) {
	t.Helper()
	w, err := zw.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write(data); err != nil {
		t.Fatal(err)
	}
}

func newStringServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}
