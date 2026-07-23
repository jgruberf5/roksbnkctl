package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExtractChartVersion(t *testing.T) {
	// The BNK manifest shape: a chart path line followed by a version line — the
	// same adjacency the shell `grep -A1 ... | grep version` relied on.
	manifest := []byte(`charts:
  - name: charts/f5-lifecycle-operator
    version: 2.3.10
  - name: charts/f5-license-proxy
    version: 1.2.3-0.0.7
  - name: charts/f5-cis
    version: 2.19.1
`)
	cases := map[string]string{
		"charts/f5-license-proxy":      "1.2.3-0.0.7",
		"charts/f5-lifecycle-operator": "2.3.10",
		"charts/f5-cis":                "2.19.1",
	}
	for sub, want := range cases {
		got, err := extractChartVersion(manifest, sub)
		if err != nil || got != want {
			t.Errorf("extractChartVersion(%q) = %q,%v want %q", sub, got, err, want)
		}
	}
	if _, err := extractChartVersion(manifest, "charts/does-not-exist"); err == nil {
		t.Error("a missing sub-chart must error, not return a stray version")
	}
}

func TestExtractChartVersion_QuotedAndInline(t *testing.T) {
	// Tolerate quoting and a same-line version.
	m := []byte(`- {name: "charts/f5-license-proxy", version: "9.9.9"}`)
	got, err := extractChartVersion(m, "charts/f5-license-proxy")
	if err != nil || got != "9.9.9" {
		t.Errorf("inline/quoted version = %q,%v want 9.9.9", got, err)
	}
}

func TestCopyFileContents(t *testing.T) {
	dir := t.TempDir()
	src := filepath.Join(dir, "src.txt")
	dst := filepath.Join(dir, "out", "dst.txt")
	if err := os.WriteFile(src, []byte("jwks-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFileContents(src, dst); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(dst)
	if strings.TrimSpace(string(b)) != "jwks-bytes" {
		t.Errorf("copied contents = %q", b)
	}
}
