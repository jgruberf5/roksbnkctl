package tf

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Appendix C recommends a cluster shape per sizing, and scripts/sizing/sizing-matrix.sh
// is what proves those shapes actually run BNK. The two spell the same numbers
// independently, so they can drift: an appendix edit that nobody mirrors into the
// script leaves the script validating the OLD recommendation while the book ships
// the new one, and the run still reports PASS.
//
// This crosses that boundary with the real bytes of both files.
func TestSizingScriptMatchesAppendixC(t *testing.T) {
	book := readRepoFile(t, "book", "src", "appendix-c-single-nic-sizing.md")
	script := readRepoFile(t, "scripts", "sizing", "sizing-matrix.sh")

	rows := parseAppendixRows(t, book)

	// column index per sizing in the appendix table (0 = Small, 1 = Medium, 2 = Large)
	for col, sizing := range []string{"small", "medium", "large"} {
		flavour := rows["Recommended flavour"][col]
		depSize := rows["deploymentSize"][col]
		nodes := leadingInt(t, rows["Worker nodes"][col])

		line := caseLine(t, script, sizing)

		if !strings.Contains(line, "FLAVOR="+flavour) {
			t.Errorf("%s: appendix recommends flavour %q, script's case line has %q",
				sizing, flavour, line)
		}
		if !strings.Contains(line, "DEPLOYMENT_SIZE="+depSize) {
			t.Errorf("%s: appendix says deploymentSize %q, script's case line has %q",
				sizing, depSize, line)
		}
		// Worker nodes is stated as a total; the script sets a per-zone count over 3 AZs.
		perZone := nodes / 3
		if nodes%3 != 0 {
			t.Errorf("%s: appendix worker-node count %d is not divisible by 3 AZs", sizing, nodes)
		}
		if !strings.Contains(line, "WORKERS_PER_ZONE="+itoa(perZone)) {
			t.Errorf("%s: appendix says %d nodes (%d per AZ), script's case line has %q",
				sizing, nodes, perZone, line)
		}
		// The post-install node assertion is DERIVED from the per-zone count rather
		// than restated per sizing, so what this guards is the derivation itself: if
		// it stops being "per-zone x 3", the per-zone checks above no longer imply
		// the appendix's totals and this test would silently mean less.
		if !regexp.MustCompile(`WANT_NODES=\$\(\(WORKERS_PER_ZONE \* 3\)\)`).MatchString(script) {
			t.Error("sizing-matrix.sh no longer derives WANT_NODES as WORKERS_PER_ZONE * 3; " +
				"the per-zone assertions above stop implying the appendix's node totals")
		}
	}
}

// parseAppendixRows turns the sizing table into row-label -> three cell values,
// stripped of the markdown emphasis and code ticks the table uses for emphasis.
func parseAppendixRows(t *testing.T, md string) map[string][]string {
	t.Helper()
	rows := map[string][]string{}
	for _, line := range strings.Split(md, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), "|") {
			continue
		}
		cells := strings.Split(strings.Trim(strings.TrimSpace(line), "|"), "|")
		if len(cells) != 4 {
			continue
		}
		for i := range cells {
			cells[i] = strings.TrimSpace(strings.NewReplacer("`", "", "*", "").Replace(cells[i]))
		}
		if cells[0] == "" {
			continue
		}
		rows[cells[0]] = cells[1:]
	}
	for _, need := range []string{"Recommended flavour", "deploymentSize", "Worker nodes"} {
		if len(rows[need]) != 3 {
			t.Fatalf("appendix C: could not parse row %q (got %v) — "+
				"the sizing table's shape changed", need, rows[need])
		}
	}
	return rows
}

// caseLine returns the script's case arm for one sizing, so assertions are scoped
// to that arm rather than matching a value that happens to appear elsewhere.
func caseLine(t *testing.T, script, sizing string) string {
	t.Helper()
	re := regexp.MustCompile(`(?m)^\s*` + sizing + `\)\s+FLAVOR=.*$`)
	m := re.FindString(script)
	if m == "" {
		t.Fatalf("sizing-matrix.sh has no %q case arm setting FLAVOR", sizing)
	}
	return m
}

func leadingInt(t *testing.T, s string) int {
	t.Helper()
	m := regexp.MustCompile(`^\d+`).FindString(strings.TrimSpace(s))
	if m == "" {
		t.Fatalf("no leading integer in %q", s)
	}
	return atoi(t, m)
}

func atoi(t *testing.T, s string) int {
	t.Helper()
	n := 0
	for _, c := range s {
		n = n*10 + int(c-'0')
	}
	return n
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"..", ".."}, parts...)...))
	if err != nil {
		t.Fatalf("read %v: %v", parts, err)
	}
	return string(b)
}
