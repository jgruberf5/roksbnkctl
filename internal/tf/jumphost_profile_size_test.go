package tf

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// #312: the jumphost auto-select sorted profile NAMES, and sort() is
// lexicographic, so among the eligible bx2 profiles
//
//	"bx2-128x512" < "bx2-16x64" < "bx2-2x8" < "bx2-32x128" < "bx2-4x16"
//
// and [0] was the LARGEST eligible profile. A workspace with
// cluster_jumphosts.create and no explicit profile got three 128 vCPU / 512 GB
// VMs — roughly $440/day — to run curl and iperf3.
//
// The selection depends on data.ibm_is_instance_profiles, so it cannot be
// evaluated the way the other terraform guards in this package are: a data
// source needs credentials. Rather than re-implement the expression in Go and
// test a copy (which would survive the product expression being wrong), these
// EXTRACT THE EXPRESSION TEXT FROM main.tf, substitute a synthetic profile
// list for the eligible-profiles reference, and evaluate that. If the shipped
// expression picks the largest, this fails.
//
// Extraction failing is a test failure, not a skip: a guard that silently stops
// checking is the thing this repo keeps getting caught by.

// synthetic profiles, deliberately in an order where both "first in the list"
// and "lexicographically first by name" are the WRONG answer.
const syntheticProfiles = `[
  { name = "bx2-16x64",   vcpu_count = [{ value = 16 }],  memory = [{ value = 64 }] },
  { name = "bx2-128x512", vcpu_count = [{ value = 128 }], memory = [{ value = 512 }] },
  { name = "bx2-2x8",     vcpu_count = [{ value = 2 }],   memory = [{ value = 8 }] },
  { name = "bx2-32x128",  vcpu_count = [{ value = 32 }],  memory = [{ value = 128 }] },
  { name = "bx2-4x16",    vcpu_count = [{ value = 4 }],   memory = [{ value = 16 }] },
]`

// extractLocal pulls the multi-line body of `<name> = ...` out of the testing
// module, from its opening line through the line that closes it.
func extractLocal(t *testing.T, name string) string {
	t.Helper()

	src, err := filepath.Abs(filepath.Join("..", "..", "terraform", "modules", "testing", "main.tf"))
	if err != nil {
		t.Fatalf("resolve module: %v", err)
	}
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read testing/main.tf: %v", err)
	}

	lines := strings.Split(string(b), "\n")
	start := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), name+" = ") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("local %q not found in testing/main.tf — the guard cannot check what it cannot find", name)
	}
	for i := start; i < len(lines) && i < start+20; i++ {
		if strings.TrimSpace(lines[i]) == "})[0]" {
			expr := strings.Join(lines[start:i+1], "\n")
			// drop the "<name> = " prefix, keep the expression itself
			eq := strings.Index(expr, "= ")
			return expr[eq+2:]
		}
	}
	t.Fatalf("local %q found but its closing `})[0]` was not within 20 lines; "+
		"the expression shape changed and this guard must be updated deliberately", name)
	return ""
}

// evalExpr evaluates a self-contained HCL expression with terraform console.
// No providers and no module — pure functions only, so init is trivial.
func evalExpr(t *testing.T, expr string) string {
	t.Helper()

	tf, err := exec.LookPath("terraform")
	if err != nil {
		t.Skip("terraform not on PATH")
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "main.tf"), []byte("locals {\n  chosen = "+expr+"\n}\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// NOT a skip. The shared console harness skips here because it initialises a
	// real module and an offline runner may have no provider mirror. This
	// fixture has no providers and no backend, so the only way init fails is a
	// malformed expression — which is precisely what this guard exists to catch.
	// Skipping made a mutation that broke the expression pass silently, and the
	// mutation run for #312 caught exactly that.
	init := exec.Command(tf, "init", "-backend=false", "-input=false")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Fatalf("terraform init failed on a provider-free fixture, so the expression itself is bad: %v\n%s", err, out)
	}

	cmd := exec.Command(tf, "console")
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader("jsonencode(local.chosen)\n")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("terraform console failed: %v\n%s", err, out)
	}

	// console echoes the value as a quoted JSON string, so it decodes twice:
	// the outer literal, then the jsonencode payload inside it.
	var line string
	for _, l := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			line = s
		}
	}
	var encoded string
	if err := json.Unmarshal([]byte(line), &encoded); err != nil {
		t.Fatalf("console output was not a JSON string: %q\nfull output:\n%s", line, out)
	}
	var s string
	if err := json.Unmarshal([]byte(encoded), &s); err != nil {
		t.Fatalf("decode %q: %v\nfull output:\n%s", encoded, err, out)
	}
	return s
}

// The shipped expression must pick the SMALLEST eligible profile.
func TestJumphostProfilePicksTheSmallestEligible(t *testing.T) {
	for _, tc := range []struct{ local, listRef string }{
		{"tgw_smallest_eligible_profile", "local.tgw_eligible_profiles"},
		{"cluster_smallest_eligible_profile", "local.cluster_eligible_profiles"},
	} {
		t.Run(tc.local, func(t *testing.T) {
			expr := extractLocal(t, tc.local)
			if !strings.Contains(expr, tc.listRef) {
				t.Fatalf("expression does not reference %s; extraction is checking the wrong thing:\n%s", tc.listRef, expr)
			}
			got := evalExpr(t, strings.ReplaceAll(expr, tc.listRef, syntheticProfiles))
			if got != "bx2-2x8" {
				t.Errorf("chose %q, want the smallest eligible profile \"bx2-2x8\".\n"+
					"\"bx2-128x512\" means the sort is lexicographic on names again (#312); "+
					"\"bx2-16x64\" means it is taking the list in API order.", got)
			}
		})
	}
}

// Memory must break a vCPU tie, so two profiles with equal vCPU do not pick the
// fatter one. Without this, format() could drop the memory field and the test
// above would still pass.
func TestJumphostProfileBreaksVCPUTiesByMemory(t *testing.T) {
	// The names are deliberately chosen so that NAME order and MEMORY order
	// disagree: "aaa-64gb" sorts before "zzz-16gb", but 16 GB is the smaller
	// machine. A key that drops the memory field picks "aaa-64gb" and fails
	// here. With the first fixture this test used (bx2-4x64 / bx2-4x16) the two
	// orders agreed, so dropping memory changed nothing and the mutation
	// survived.
	const tied = `[
  { name = "aaa-64gb", vcpu_count = [{ value = 4 }], memory = [{ value = 64 }] },
  { name = "zzz-16gb", vcpu_count = [{ value = 4 }], memory = [{ value = 16 }] },
]`
	expr := extractLocal(t, "cluster_smallest_eligible_profile")
	got := evalExpr(t, strings.ReplaceAll(expr, "local.cluster_eligible_profiles", tied))
	if got != "zzz-16gb" {
		t.Errorf("chose %q on a vCPU tie, want the lower-memory \"zzz-16gb\" — "+
			"\"aaa-64gb\" means the key dropped the memory field and fell back to name order", got)
	}
}

// An empty eligible list must yield "", which is what makes the caller fall
// back to the hard-coded default instead of indexing an empty collection.
func TestJumphostProfileEmptyListIsEmptyString(t *testing.T) {
	expr := extractLocal(t, "cluster_smallest_eligible_profile")
	if got := evalExpr(t, strings.ReplaceAll(expr, "local.cluster_eligible_profiles", "[]")); got != "" {
		t.Errorf("empty eligible list gave %q, want \"\"", got)
	}
}

// Zero-padding must survive a vCPU count wide enough to change the ordering if
// the key were unpadded: "8" > "128" lexicographically.
func TestJumphostProfilePaddingSurvivesWideVCPUCounts(t *testing.T) {
	const wide = `[
  { name = "big",   vcpu_count = [{ value = 128 }], memory = [{ value = 512 }] },
  { name = "small", vcpu_count = [{ value = 8 }],   memory = [{ value = 32 }] },
]`
	expr := extractLocal(t, "cluster_smallest_eligible_profile")
	got := evalExpr(t, strings.ReplaceAll(expr, "local.cluster_eligible_profiles", wide))
	if got != "small" {
		t.Errorf("chose %q, want \"small\" — an unpadded key sorts \"128\" before \"8\"", got)
	}
}
