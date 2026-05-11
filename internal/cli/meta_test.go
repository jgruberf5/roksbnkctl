package cli

import (
	"bytes"
	"strings"
	"testing"
)

// TestVersionCmd_OutputShape pins the two-line shape of `roksbnkctl
// version`: line 1 is the historical "roksbnkctl <ver> (commit <c>,
// built <d>)" string that pre-v1.0 scripts may grep; line 2 is the
// `Docs: <url>` addition landed for v1.0 (PLAN.md §"Sprint 7" row 2).
func TestVersionCmd_OutputShape(t *testing.T) {
	prevV, prevC, prevD := Version, Commit, BuildDate
	t.Cleanup(func() {
		Version = prevV
		Commit = prevC
		BuildDate = prevD
	})
	Version = "v1.0.0"
	Commit = "abc1234"
	BuildDate = "2026-05-24"

	var buf bytes.Buffer
	versionCmd.SetOut(&buf)
	versionCmd.SetErr(&buf)
	if err := versionCmd.RunE(versionCmd, nil); err != nil {
		t.Fatalf("versionCmd.RunE: %v", err)
	}
	got := buf.String()

	wantSubstrings := []string{
		"roksbnkctl v1.0.0",
		"commit abc1234",
		"built 2026-05-24",
		DocsURL,
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(got, want) {
			t.Errorf("version output missing %q\n--- got ---\n%s", want, got)
		}
	}

	lines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	if len(lines) != 2 {
		t.Errorf("expected 2-line output, got %d lines:\n%s", len(lines), got)
	}
	// First line must be byte-identical to the pre-v1.0 shape so
	// downstream scripts that pin on it don't break.
	wantFirst := "roksbnkctl v1.0.0 (commit abc1234, built 2026-05-24)"
	if len(lines) > 0 && lines[0] != wantFirst {
		t.Errorf("first line mismatch\n  want: %q\n  got:  %q", wantFirst, lines[0])
	}
	// Second line surfaces the docs URL.
	wantSecond := "Docs: " + DocsURL
	if len(lines) > 1 && lines[1] != wantSecond {
		t.Errorf("second line mismatch\n  want: %q\n  got:  %q", wantSecond, lines[1])
	}
}

// TestDocsURL_Value pins the canonical docs URL constant so a stray
// renamings of the GitHub Pages site (or rolling to a versioned path)
// fail loudly here rather than silently in user-facing output.
func TestDocsURL_Value(t *testing.T) {
	const want = "https://jgruberf5.github.io/roksbnkctl/book/"
	if DocsURL != want {
		t.Errorf("DocsURL drift: want %q, got %q", want, DocsURL)
	}
}
