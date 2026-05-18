package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/orchestration"
)

// Sprint 15 code deliverable 3(a) — the chokepoint-invariant guard.
//
// The recurring "a value correct in the invocation context is wrong
// once it crosses a boundary" bug class (Sprint 12 Issues 1/2 +
// Sprint 13 Issue 1) was retired structurally by collapsing all
// path/env normalization into a single chokepoint
// (root PersistentPreRunE → resolveInvocationContext, backed by
// internal/orchestration). These tests fail loudly if a future
// contributor reopens the class by re-deriving a path or env in a RunE
// or in a dispatchRemote caller instead of consuming the chokepoint.
//
// They are CI-asserted, greppable invariants — not behavior tests, so
// they do not perturb the Sprint 14 parity harness.

// reDerivationPattern is a forbidden per-RunE / per-dispatch re-derivation:
//
//   - a per-RunE `flagVarFiles = resolveVarFiles(...)` / `= resolved`
//     fan-out (the Sprint 12 Issue 1 shape: 8+ scattered call sites),
//   - a per-call-site `resolveLocalTFSource(flagTFSource)` re-derivation
//     (the Sprint 12 Issue 2 shape),
//   - a scattered KUBECONFIG-scrub list outside the single
//     orchestration.LocalOnlyEnvKeys classification (Sprint 13 Issue 1).
//
// The single chokepoint mutates flagVarFiles/flagTFSource exactly once
// in root.go's PersistentPreRunE; the thin wrappers
// (resolveVarFiles/resolveLocalTFSource/remoteSafeEnv) only delegate to
// orchestration. Any OTHER occurrence is a reopening of the class.
func TestChokepointInvariant_NoPerRunEReDerivation(t *testing.T) {
	// Files in scope: the RunE-bearing lifecycle/cluster/bnk/init files
	// plus the remote dispatch. root.go is the ALLOWED single mutation
	// site; the wrapper bodies in lifecycle.go/cluster.go/init.go are
	// allowed one delegating call each.
	type rule struct {
		file    string
		pattern *regexp.Regexp
		// maxAllowed is how many matches are legitimate (the thin
		// wrapper's own single delegating reference). Anything beyond is
		// a re-derivation.
		maxAllowed int
		what       string
	}
	rules := []rule{
		// The per-RunE `flagVarFiles = <resolved>` write must exist
		// ONLY in root.go (the chokepoint). Zero in any RunE file.
		{"lifecycle.go", regexp.MustCompile(`flagVarFiles\s*=\s*resolved`), 0, "per-RunE flagVarFiles reassignment"},
		{"bnk_phase.go", regexp.MustCompile(`flagVarFiles\s*=\s*resolved`), 0, "per-RunE flagVarFiles reassignment"},
		{"cluster_phase.go", regexp.MustCompile(`flagVarFiles\s*=\s*resolved`), 0, "per-RunE flagVarFiles reassignment"},
		// resolveVarFiles must be CALLED only as the thin wrapper's own
		// body delegation (1 occurrence in lifecycle.go: the func def +
		// its single orchestration call live there). No RunE calls it.
		{"bnk_phase.go", regexp.MustCompile(`resolveVarFiles\(`), 0, "RunE calling resolveVarFiles"},
		{"cluster_phase.go", regexp.MustCompile(`resolveVarFiles\(`), 0, "RunE calling resolveVarFiles"},
		// resolveLocalTFSource must NOT be called from init.go's RunE
		// flow anymore (the chokepoint normalizes flagTFSource once);
		// only its own wrapper definition references the name.
		{"init.go", regexp.MustCompile(`=\s*resolveLocalTFSource\(`), 0, "per-call-site --tf-source re-derivation"},
		// No scattered local-path scrub list outside orchestration.
		{"cluster.go", regexp.MustCompile(`localPathEnvKeys`), 0, "scattered local-path-env scrub list (must be the single orchestration.LocalOnlyEnvKeys)"},
		{"remote.go", regexp.MustCompile(`localPathEnvKeys`), 0, "scattered local-path-env scrub list"},
	}

	root := repoRel(t, "internal", "cli")
	for _, r := range rules {
		src, err := os.ReadFile(filepath.Join(root, r.file))
		if err != nil {
			t.Fatalf("read %s: %v", r.file, err)
		}
		// Strip line comments so a doc-comment mentioning the retired
		// pattern doesn't trip the structural scan.
		var code strings.Builder
		for _, ln := range strings.Split(string(src), "\n") {
			trimmed := strings.TrimSpace(ln)
			if strings.HasPrefix(trimmed, "//") {
				continue
			}
			code.WriteString(ln)
			code.WriteString("\n")
		}
		got := len(r.pattern.FindAllString(code.String(), -1))
		if got > r.maxAllowed {
			t.Errorf("%s: %s — found %d match(es) of %q, want ≤ %d. "+
				"The path/env chokepoint was reopened: route this through "+
				"resolveInvocationContext (root PersistentPreRunE), do not "+
				"re-derive per RunE/dispatch.",
				r.file, r.what, got, r.pattern, r.maxAllowed)
		}
	}
}

// TestChokepointInvariant_ResolveIsSingleSourceOfTruth pins that the
// chokepoint actually normalizes both path-valued flags through the one
// orchestration.Resolve entry point and that downstream consumes the
// resolved struct (not a re-derivation). Behavioral, but it exercises
// only the chokepoint primitive — it does not touch the Sprint 14
// parity harness.
func TestChokepointInvariant_ResolveIsSingleSourceOfTruth(t *testing.T) {
	tmp := t.TempDir()
	vf := filepath.Join(tmp, "extra.tfvars")
	if err := os.WriteFile(vf, []byte("x = 1\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	origCWD, _ := os.Getwd()
	t.Cleanup(func() { _ = os.Chdir(origCWD) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}

	rf, err := orchestration.Resolve([]string{"./extra.tfvars"}, "./localtf")
	if err != nil {
		t.Fatalf("orchestration.Resolve: %v", err)
	}
	if len(rf.VarFiles) != 1 || !filepath.IsAbs(rf.VarFiles[0]) {
		t.Errorf("Resolve must normalize --var-file to absolute, got %v", rf.VarFiles)
	}
	if !filepath.IsAbs(rf.TFSource) {
		t.Errorf("Resolve must normalize local --tf-source to absolute, got %q", rf.TFSource)
	}

	// resolvedFlags is the single resolved-invocation context the cobra
	// adapter publishes from rootPersistentPreRunE; the wrappers and the
	// chokepoint are its only producers. Assigning here proves the
	// struct is a real consumed surface (and keeps the symbol a genuine
	// non-dead consumer, not just a write-only field).
	prev := resolvedFlags
	t.Cleanup(func() { resolvedFlags = prev })
	resolvedFlags = rf
	if resolvedFlags.VarFiles[0] != rf.VarFiles[0] {
		t.Errorf("resolvedFlags must hold the single chokepoint result")
	}
}

// repoRel resolves a path relative to the repo root from the test's CWD
// (which is the package dir under `go test`). internal/cli → repo root
// is two levels up.
func repoRel(t *testing.T, parts ...string) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// internal/cli test CWD → ../../ is repo root.
	root := filepath.Clean(filepath.Join(wd, "..", ".."))
	return filepath.Join(append([]string{root}, parts...)...)
}
