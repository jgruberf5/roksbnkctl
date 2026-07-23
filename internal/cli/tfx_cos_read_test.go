package cli

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGetter records the download call and returns a canned result.
type fakeGetter struct {
	bucket, key, out string
	err              error
	called           bool
}

func (f *fakeGetter) GetObjectToFile(_ context.Context, bucket, key, localPath string) error {
	f.called, f.bucket, f.key, f.out = true, bucket, key, localPath
	return f.err
}

func TestRunTFXCosGet_DownloadsAndCreatesDir(t *testing.T) {
	dir := t.TempDir()
	out := filepath.Join(dir, "sub", "far.tgz") // sub/ must be created
	g := &fakeGetter{}
	if err := runTFXCosGet(context.Background(), g, "bnk-artifacts", "f5-far-auth-key.tgz", out, io.Discard); err != nil {
		t.Fatalf("cos-get failed: %v", err)
	}
	if !g.called || g.bucket != "bnk-artifacts" || g.key != "f5-far-auth-key.tgz" || g.out != out {
		t.Errorf("getter called with wrong args: %+v", g)
	}
	if _, err := os.Stat(filepath.Dir(out)); err != nil {
		t.Errorf("out dir not created: %v", err)
	}
}

func TestRunTFXCosGet_SurfacesGetterError(t *testing.T) {
	g := &fakeGetter{err: errors.New("403 forbidden")}
	err := runTFXCosGet(context.Background(), g, "b", "k", filepath.Join(t.TempDir(), "x"), io.Discard)
	if err == nil || !strings.Contains(err.Error(), "downloading b/k") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("want a wrapped download error, got %v", err)
	}
}

func TestWriteReadJSON(t *testing.T) {
	var b strings.Builder
	if err := writeReadJSON(&b, "v", "  1.3.11\n", false); err != nil {
		t.Fatal(err)
	}
	// Trimmed value, flat map, exactly what data.external consumes.
	if got := strings.TrimSpace(b.String()); got != `{"v":"1.3.11"}` {
		t.Errorf("read-json = %q, want {\"v\":\"1.3.11\"}", got)
	}

	b.Reset()
	if err := writeReadJSON(&b, "", "x", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `"v":"x"`) {
		t.Errorf("empty key should default to v: %q", b.String())
	}

	b.Reset()
	_ = writeReadJSON(&b, "k", " keep me ", true)
	if !strings.Contains(b.String(), `"k":" keep me "`) {
		t.Errorf("--raw should not trim: %q", b.String())
	}
}
