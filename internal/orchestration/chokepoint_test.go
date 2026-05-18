package orchestration

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Sprint 15 code deliverable 3 — unit coverage for the single
// path/env normalization chokepoint. These exercise the orchestration
// primitives directly (the cli-layer wrappers are thin delegators); they
// are a NEW test file (an addition, not an edit), so they do not perturb
// the Sprint 14 behavior-parity harness.

func TestNormalizeVarFiles_AbsolutePassThroughCleaned(t *testing.T) {
	in := []string{"/abs/../abs/foo.tfvars"}
	out, err := NormalizeVarFiles(in)
	if err != nil {
		t.Fatalf("NormalizeVarFiles: %v", err)
	}
	if out[0] != filepath.Clean("/abs/../abs/foo.tfvars") || !filepath.IsAbs(out[0]) {
		t.Errorf("absolute path must pass through cleaned, got %q", out[0])
	}
}

func TestNormalizeVarFiles_RelativeResolvedAgainstCWD(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "v.tfvars"), []byte("x=1\n"), 0o644); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	t.Chdir(tmp)
	out, err := NormalizeVarFiles([]string{"./v.tfvars"})
	if err != nil {
		t.Fatalf("NormalizeVarFiles: %v", err)
	}
	if !filepath.IsAbs(out[0]) || filepath.Dir(out[0]) != tmp {
		t.Errorf("relative path must resolve against CWD %q, got %q", tmp, out[0])
	}
}

func TestNormalizeVarFiles_MissingNamesBothPaths(t *testing.T) {
	tmp := t.TempDir()
	t.Chdir(tmp)
	_, err := NormalizeVarFiles([]string{"./nope.tfvars"})
	if err == nil {
		t.Fatal("missing var-file must error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "./nope.tfvars") || !strings.Contains(msg, tmp) {
		t.Errorf("error must name both the user input and the resolved abs path, got: %v", err)
	}
}

func TestNormalizeVarFiles_EmptyRoundTrips(t *testing.T) {
	out, err := NormalizeVarFiles(nil)
	if err != nil || out != nil {
		t.Errorf("nil input must round-trip nil with no error, got %v / %v", out, err)
	}
}

func TestNormalizeVarFiles_TildeExpansion(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.WriteFile(filepath.Join(home, "h.tfvars"), []byte("x=1\n"), 0o644); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	out, err := NormalizeVarFiles([]string{"~/h.tfvars"})
	if err != nil {
		t.Fatalf("NormalizeVarFiles ~: %v", err)
	}
	if out[0] != filepath.Join(home, "h.tfvars") {
		t.Errorf("~ must expand to HOME, got %q", out[0])
	}
}

func TestNormalizeLocalPath(t *testing.T) {
	if got, _ := NormalizeLocalPath(""); got != "" {
		t.Errorf("empty must round-trip empty, got %q", got)
	}
	if got, err := NormalizeLocalPath("/a/../a/tf"); err != nil || got != filepath.Clean("/a/../a/tf") {
		t.Errorf("absolute must pass through cleaned, got %q / %v", got, err)
	}
	tmp := t.TempDir()
	t.Chdir(tmp)
	got, err := NormalizeLocalPath("./mytf")
	if err != nil || !filepath.IsAbs(got) || filepath.Dir(got) != tmp {
		t.Errorf("relative must resolve to abs against CWD, got %q / %v", got, err)
	}
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got, err := NormalizeLocalPath("~/src"); err != nil || got != filepath.Join(home, "src") {
		t.Errorf("~ must expand to HOME, got %q / %v", got, err)
	}
}

func TestScrubLocalOnly_StripsKubeconfigKeepsValues(t *testing.T) {
	in := []string{
		"PATH=/usr/bin",
		"KUBECONFIG=/home/u/.kube/config",
		"IBMCLOUD_API_KEY=k",
		"IBMCLOUD_REGION=us-south",
	}
	out := ScrubLocalOnly(in)
	for _, kv := range out {
		if strings.HasPrefix(kv, "KUBECONFIG=") {
			t.Errorf("ScrubLocalOnly must strip KUBECONFIG, got %v", out)
		}
	}
	for _, want := range []string{"IBMCLOUD_API_KEY=k", "IBMCLOUD_REGION=us-south", "PATH=/usr/bin"} {
		found := false
		for _, kv := range out {
			if kv == want {
				found = true
			}
		}
		if !found {
			t.Errorf("ScrubLocalOnly dropped a non-local-only var %q: %v", want, out)
		}
	}
}

func TestScrubLocalOnly_NilAndMalformed(t *testing.T) {
	if out := ScrubLocalOnly(nil); out != nil {
		t.Errorf("nil must round-trip nil, got %v", out)
	}
	// Malformed entries (no '=', leading '=') are not local-only and
	// must pass through unmangled; a real KUBECONFIG among them is still
	// stripped.
	in := []string{"NOEQUALS", "=leadingeq", "OK=1", "KUBECONFIG=/x"}
	out := ScrubLocalOnly(in)
	for _, kv := range out {
		if kv == "KUBECONFIG=/x" {
			t.Errorf("KUBECONFIG not stripped among malformed entries: %v", out)
		}
	}
	for _, want := range []string{"NOEQUALS", "=leadingeq", "OK=1"} {
		found := false
		for _, kv := range out {
			if kv == want {
				found = true
			}
		}
		if !found {
			t.Errorf("malformed/normal entry %q must pass through, got %v", want, out)
		}
	}
}

func TestIsLocalOnlyEnv(t *testing.T) {
	cases := map[string]bool{
		"KUBECONFIG=/x":      true,
		"IBMCLOUD_API_KEY=k": false,
		"NOEQUALS":           false,
		"=leadingeq":         false,
		"KUBECONFIGX=/x":     false,
	}
	for kv, want := range cases {
		if got := IsLocalOnlyEnv(kv); got != want {
			t.Errorf("IsLocalOnlyEnv(%q) = %v, want %v", kv, got, want)
		}
	}
}

func TestResolve_NormalizesBothFlagsOnce(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "extra.tfvars"), []byte("x=1\n"), 0o644); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	t.Chdir(tmp)
	rf, err := Resolve([]string{"./extra.tfvars"}, "./localtf")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(rf.VarFiles) != 1 || !filepath.IsAbs(rf.VarFiles[0]) {
		t.Errorf("Resolve must normalize --var-file to absolute, got %v", rf.VarFiles)
	}
	if !filepath.IsAbs(rf.TFSource) {
		t.Errorf("Resolve must normalize local --tf-source to absolute, got %q", rf.TFSource)
	}
	// Idempotent: re-resolving an already-absolute slice is a no-op.
	rf2, err := Resolve(rf.VarFiles, rf.TFSource)
	if err != nil || rf2.VarFiles[0] != rf.VarFiles[0] || rf2.TFSource != rf.TFSource {
		t.Errorf("Resolve must be idempotent on absolute inputs, got %v / %v", rf2, err)
	}
}
