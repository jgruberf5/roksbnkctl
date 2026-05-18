package tf

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func TestFetchSource_Local_OK(t *testing.T) {
	tmp := t.TempDir()
	cfg := config.TFSourceCfg{Type: "local", Path: tmp}
	got, err := FetchSource(context.Background(), cfg, "")
	if err != nil {
		t.Fatalf("FetchSource: %v", err)
	}
	if got != tmp {
		t.Errorf("got %q, want %q", got, tmp)
	}
}

func TestFetchSource_Local_NotADir(t *testing.T) {
	tmp := t.TempDir()
	file := filepath.Join(tmp, "regular-file")
	if err := os.WriteFile(file, []byte("hi"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.TFSourceCfg{Type: "local", Path: file}
	if _, err := FetchSource(context.Background(), cfg, ""); err == nil {
		t.Error("expected error for non-directory path")
	}
}

func TestFetchSource_Local_Missing(t *testing.T) {
	cfg := config.TFSourceCfg{Type: "local", Path: "/this/path/should/not/exist/roksbnkctl-test"}
	if _, err := FetchSource(context.Background(), cfg, ""); err == nil {
		t.Error("expected error for missing path")
	}
}

func TestFetchSource_Local_EmptyPath(t *testing.T) {
	cfg := config.TFSourceCfg{Type: "local", Path: ""}
	if _, err := FetchSource(context.Background(), cfg, ""); err == nil {
		t.Error("expected error for empty path")
	}
}

// TestFetchSource_Local_RelativePathSelfHeals — staff Issue 2 (Sprint
// 12 pull-in) self-heal: a config.yaml written before the init-time
// resolveLocalTFSource normalization may carry a *relative* local path.
// FetchSource runs effectively from the per-phase state dir CWD, so it
// must absolutize a relative src.Path against the invocation CWD rather
// than os.Stat it relative to terraform's state dir.
func TestFetchSource_Local_RelativePathSelfHeals(t *testing.T) {
	tmp := t.TempDir()
	sub := filepath.Join(tmp, "legacy-tf")
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

	// Pre-fix-style config.yaml entry: a relative local path.
	cfg := config.TFSourceCfg{Type: "local", Path: "./legacy-tf"}
	got, err := FetchSource(context.Background(), cfg, "")
	if err != nil {
		t.Fatalf("FetchSource(relative local): %v", err)
	}
	if !filepath.IsAbs(got) {
		t.Errorf("expected self-healed absolute path, got %q", got)
	}
	wantAbs, err := filepath.EvalSymlinks(sub)
	if err != nil {
		t.Fatalf("EvalSymlinks(fixture): %v", err)
	}
	gotAbs, err := filepath.EvalSymlinks(got)
	if err != nil {
		t.Fatalf("EvalSymlinks(got): %v", err)
	}
	if gotAbs != wantAbs {
		t.Errorf("self-heal mismatch:\n  got  %q\n  want %q", gotAbs, wantAbs)
	}
}

func TestFetchSource_GitHub_NeedsRepoAndRef(t *testing.T) {
	cfg := config.TFSourceCfg{Type: "github", Repo: "", Ref: ""}
	if _, err := FetchSource(context.Background(), cfg, t.TempDir()); err == nil {
		t.Error("expected error for empty github repo/ref")
	}
}

func TestFetchSource_UnknownType(t *testing.T) {
	cfg := config.TFSourceCfg{Type: "ftp"}
	if _, err := FetchSource(context.Background(), cfg, ""); err == nil {
		t.Error("expected error for unknown source type")
	}
}
