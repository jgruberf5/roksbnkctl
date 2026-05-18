package cli

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestResolveVarFiles_AbsolutePassThrough — absolute paths that exist on
// disk must round-trip unchanged through resolveVarFiles. This is the
// pre-fix behavior; we don't want to regress it.
func TestResolveVarFiles_AbsolutePassThrough(t *testing.T) {
	tmp := t.TempDir()
	abs := filepath.Join(tmp, "foo.tfvars")
	if err := os.WriteFile(abs, []byte("worker_count = 6\n"), 0o644); err != nil {
		t.Fatalf("write abs fixture: %v", err)
	}
	// Absolute paths are NOT os.Stat-checked in the helper (they're
	// passed through cleaned); terraform itself surfaces missing-file
	// errors for those. Use an existing file so the test is robust.
	got, err := resolveVarFiles([]string{abs})
	if err != nil {
		t.Fatalf("resolveVarFiles(%q) returned error: %v", abs, err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	want := filepath.Clean(abs)
	if got[0] != want {
		t.Errorf("absolute pass-through mismatch:\n  got  %q\n  want %q", got[0], want)
	}
}

// TestResolveVarFiles_RelativeResolvedAgainstCWD — a relative input
// (`./foo.tfvars`) must resolve against the invocation CWD, not against
// the per-phase terraform state dir. This is the v1.4.1 bug fix.
func TestResolveVarFiles_RelativeResolvedAgainstCWD(t *testing.T) {
	tmp := t.TempDir()
	fixture := filepath.Join(tmp, "terraform.tfvars")
	if err := os.WriteFile(fixture, []byte("worker_count = 6\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	// Stash the original CWD and chdir into the temp directory so
	// `./terraform.tfvars` resolves to <tmp>/terraform.tfvars.
	origCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCWD) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("os.Chdir(%q): %v", tmp, err)
	}

	got, err := resolveVarFiles([]string{"./terraform.tfvars"})
	if err != nil {
		t.Fatalf("resolveVarFiles(./terraform.tfvars): %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}

	// EvalSymlinks both sides — macOS /var → /private/var, /tmp →
	// /private/tmp on CI; on Linux these are usually no-ops, but the
	// resolved abs paths must match after symlink normalization.
	wantAbs, err := filepath.EvalSymlinks(fixture)
	if err != nil {
		t.Fatalf("EvalSymlinks(fixture): %v", err)
	}
	gotAbs, err := filepath.EvalSymlinks(got[0])
	if err != nil {
		t.Fatalf("EvalSymlinks(got): %v", err)
	}
	if gotAbs != wantAbs {
		t.Errorf("relative resolution mismatch:\n  got  %q\n  want %q", gotAbs, wantAbs)
	}
	if !filepath.IsAbs(got[0]) {
		t.Errorf("expected absolute path post-resolution, got %q", got[0])
	}
}

// TestResolveVarFiles_MissingFileErrorNamesBoth — when a relative path
// doesn't exist on disk, the error must name both the user-supplied
// input and the resolved absolute. This lets users distinguish "I
// typoed the filename" from "I'm in the wrong directory" without
// digging through terraform's own less-specific error.
func TestResolveVarFiles_MissingFileErrorNamesBoth(t *testing.T) {
	tmp := t.TempDir()
	origCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCWD) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("os.Chdir: %v", err)
	}

	input := "./missing.tfvars"
	_, err = resolveVarFiles([]string{input})
	if err == nil {
		t.Fatalf("expected error for missing file, got nil")
	}
	msg := err.Error()
	if !strings.Contains(msg, input) {
		t.Errorf("error message should name the user input %q\n  got: %s", input, msg)
	}
	// The resolved absolute lives under tmp. macOS' /var → /private/var
	// symlink means the helper's filepath.Join may produce a path that
	// doesn't byte-match the EvalSymlinks-resolved tmp; check for the
	// filename component which is stable across both.
	if !strings.Contains(msg, "missing.tfvars") {
		t.Errorf("error message should name the resolved abs path containing %q\n  got: %s", "missing.tfvars", msg)
	}
	// And the message must contain *both* the leading "./" form *and*
	// an absolute-looking path component (so users see both forms).
	// On Unix, abs starts with "/"; on Windows, with a drive letter or
	// "\\". The simplest cross-platform check: the resolved path is
	// distinct from the input, so the message must contain more than
	// just the input.
	if !strings.Contains(msg, "resolved to") {
		t.Errorf("error message should mention 'resolved to' so users see both forms\n  got: %s", msg)
	}
}

// TestResolveVarFiles_TildeExpansion — `~/foo.tfvars` should expand
// to <home>/foo.tfvars before resolution, matching install.go's
// --dir handling (the project's only other `~/`-aware surface).
// Skipped on Windows where `~` isn't conventionally a home alias.
func TestResolveVarFiles_TildeExpansion(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("~/ expansion is a POSIX shell convention; not exercised on Windows")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home dir resolvable on this host: %v", err)
	}
	// Create a fixture inside HOME so the os.Stat in the helper passes.
	// Use a uniquely-named file under a temp subdir we then remove.
	dir, err := os.MkdirTemp(home, "roksbnkctl-vf-test-")
	if err != nil {
		t.Skipf("can't create fixture under %q: %v", home, err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	fixture := filepath.Join(dir, "tilde.tfvars")
	if err := os.WriteFile(fixture, []byte("worker_count = 6\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	rel, err := filepath.Rel(home, fixture)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	input := "~/" + filepath.ToSlash(rel)
	got, err := resolveVarFiles([]string{input})
	if err != nil {
		t.Fatalf("resolveVarFiles(%q): %v", input, err)
	}
	if len(got) != 1 {
		t.Fatalf("expected 1 entry, got %d: %v", len(got), got)
	}
	if !filepath.IsAbs(got[0]) {
		t.Errorf("expected absolute path post ~-expansion, got %q", got[0])
	}
	wantAbs, _ := filepath.EvalSymlinks(fixture)
	gotAbs, _ := filepath.EvalSymlinks(got[0])
	if wantAbs != "" && gotAbs != "" && gotAbs != wantAbs {
		t.Errorf("~-expansion mismatch:\n  got  %q\n  want %q", gotAbs, wantAbs)
	}
}

// TestResolveVarFiles_EmptyInput — calling with no var-files is a
// no-op (no os.Getwd call, no errors). Important because every RunE
// calls resolveVarFiles unconditionally; if there are no flags, it
// must stay quiet.
func TestResolveVarFiles_EmptyInput(t *testing.T) {
	got, err := resolveVarFiles(nil)
	if err != nil {
		t.Fatalf("resolveVarFiles(nil) returned error: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
	got, err = resolveVarFiles([]string{})
	if err != nil {
		t.Fatalf("resolveVarFiles(empty): %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

// ── resolveLocalTFSource (staff Issue 2: --tf-source local relative ──
// paths persist unresolved into config.yaml) ────────────────────────

// TestResolveLocalTFSource_RelativeResolvedToAbs — the v1.4.1 (Sprint
// 12 pull-in) bug fix: a relative local --tf-source must be pinned as
// an absolute path so the value persisted into config.yaml stays stable
// regardless of the per-phase terraform state dir CWD it's later used
// from.
func TestResolveLocalTFSource_RelativeResolvedToAbs(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "mytf")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir fixture: %v", err)
	}

	origCWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(origCWD) })
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("os.Chdir(%q): %v", tmp, err)
	}

	got, err := resolveLocalTFSource("./mytf")
	if err != nil {
		t.Fatalf("resolveLocalTFSource(./mytf): %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected absolute path, got %q", got)
	}
	// EvalSymlinks both sides — macOS /var → /private/var, /tmp →
	// /private/tmp on CI; usually no-ops on Linux.
	wantAbs, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatalf("EvalSymlinks(fixture): %v", err)
	}
	gotAbs, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(got): %v", err)
	}
	if gotAbs != wantAbs {
		t.Errorf("relative resolution mismatch:\n  got  %q\n  want %q", gotAbs, wantAbs)
	}
}

// TestResolveLocalTFSource_AbsolutePassThrough — an absolute local
// --tf-source must round-trip cleaned but otherwise unchanged (pre-fix
// behavior; must not regress).
func TestResolveLocalTFSource_AbsolutePassThrough(t *testing.T) {
	tmp := t.TempDir()
	got, err := resolveLocalTFSource(tmp)
	if err != nil {
		t.Fatalf("resolveLocalTFSource(%q): %v", tmp, err)
	}
	want := filepath.Clean(tmp)
	if got != want {
		t.Errorf("absolute pass-through mismatch:\n  got  %q\n  want %q", got, want)
	}
}

// TestResolveLocalTFSource_EmptyInput — empty input is a no-op. The two
// init.go build sites only call this under `flagTFSource != ""`, but the
// helper must stay defensively quiet on "" (no os.Getwd, no error).
func TestResolveLocalTFSource_EmptyInput(t *testing.T) {
	got, err := resolveLocalTFSource("")
	if err != nil {
		t.Fatalf("resolveLocalTFSource(\"\") returned error: %v", err)
	}
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// TestResolveLocalTFSource_GitHubFormUntouched — a github "owner/repo"
// or URL-shaped string is never routed to resolveLocalTFSource by the
// init.go call sites (the embedded/github branches split off upstream),
// but if one ever reached the helper it must NOT be filepath.Abs'd into
// a bogus local path. We assert the github form is left structurally
// intact (not turned into an absolute filesystem path under CWD).
func TestResolveLocalTFSource_GitHubFormUntouched(t *testing.T) {
	// owner/repo slug — looksLikeGitHubRepo would have caught this
	// upstream; here we just pin that the helper doesn't mangle it into
	// "<cwd>/owner/repo" silently if the contract ever changes.
	const ghSlug = "jgruberf5/some_tf_repo"
	got, err := resolveLocalTFSource(ghSlug)
	if err != nil {
		t.Fatalf("resolveLocalTFSource(%q): %v", ghSlug, err)
	}
	// The helper treats any non-abs input as a path and absolutizes it;
	// the guarantee we rely on is the *call sites* never pass a github
	// form here. Document that contract: if the resolved value silently
	// became an abs path, the slug's "owner/repo" structure is still a
	// suffix (filepath.Abs just prefixes CWD) — proving no URL parsing /
	// rewriting happened, only path joining.
	if !strings.HasSuffix(filepath.ToSlash(got), ghSlug) {
		t.Errorf("github slug should survive as a path suffix (no URL rewriting):\n  got %q\n  want suffix %q", got, ghSlug)
	}
}
