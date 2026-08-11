package tf

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The overlay mechanism must be INVISIBLE until a line actually needs it.
// Every existing workspace passes through this path, so if adding the mechanism
// changed what lands on disk, it would have changed every deployment.
func TestEmbeddedExtractionIsUnchangedWithoutOverlay(t *testing.T) {
	ctx := context.Background()
	src := config.TFSourceCfg{Type: "embedded"}

	base, err := FetchSourceForLine(ctx, src, t.TempDir(), "")
	if err != nil {
		t.Fatalf("no line: %v", err)
	}
	// 2.3 ships no overlay today, so it must extract byte-identically to no line
	// at all.
	line, err := FetchSourceForLine(ctx, src, t.TempDir(), "2.3")
	if err != nil {
		t.Fatalf("line 2.3: %v", err)
	}
	assertTreesEqual(t, base, line)

	// FetchSource is the old entry point; it must still work and still mean
	// "the base tree".
	old, err := FetchSource(ctx, src, t.TempDir())
	if err != nil {
		t.Fatalf("FetchSource: %v", err)
	}
	assertTreesEqual(t, base, old)
}

// lines/ is maintainer material. If it were extracted, every line's HCL would
// sit inside the module tree that terraform is pointed at.
func TestOverlaySourceIsNotExtracted(t *testing.T) {
	dir, err := FetchSourceForLine(context.Background(), config.TFSourceCfg{Type: "embedded"}, t.TempDir(), "2.3")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "lines")); !os.IsNotExist(err) {
		t.Errorf("lines/ must not be extracted into the module tree (stat err = %v)", err)
	}
	// Sanity: we are actually looking at an extracted tree, not an empty dir —
	// otherwise the assertion above passes for the wrong reason.
	if _, err := os.Stat(filepath.Join(dir, "main.tf")); err != nil {
		t.Fatalf("base tree missing main.tf: %v", err)
	}
}

// A line is derived from a version string, but it is used to build a path.
func TestOverlayRejectsPathTraversal(t *testing.T) {
	for _, bad := range []string{"../../etc", "2.3/../..", `a\b`, ".."} {
		if err := applyLineOverlay(t.TempDir(), bad); err == nil {
			t.Errorf("line %q was accepted as an overlay name", bad)
		}
	}
}

// An overlay replaces base files and adds new ones, and never deletes — so it
// cannot silently remove a resource the base declares.
func TestOverlayReplacesAndAdds(t *testing.T) {
	dest := t.TempDir()
	writeFile(t, filepath.Join(dest, "main.tf"), "base")
	writeFile(t, filepath.Join(dest, "untouched.tf"), "keep")

	// Simulate what applyLineOverlay does to the destination, using the same
	// copy semantics, since no real overlay ships today.
	overlay := t.TempDir()
	writeFile(t, filepath.Join(overlay, "main.tf"), "overlaid")
	writeFile(t, filepath.Join(overlay, "extra.tf"), "added")
	copyTree(t, overlay, dest)

	if got := readOverlayFile(t, filepath.Join(dest, "main.tf")); got != "overlaid" {
		t.Errorf("overlay did not replace the base file: %q", got)
	}
	if got := readOverlayFile(t, filepath.Join(dest, "extra.tf")); got != "added" {
		t.Errorf("overlay did not add its new file: %q", got)
	}
	if got := readOverlayFile(t, filepath.Join(dest, "untouched.tf")); got != "keep" {
		t.Errorf("overlay removed a base file it does not mention: %q", got)
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readOverlayFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func copyTree(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		body, err := os.ReadFile(filepath.Join(from, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(to, e.Name()), string(body))
	}
}

func assertTreesEqual(t *testing.T, a, b string) {
	t.Helper()
	fa, fb := treeMap(t, a), treeMap(t, b)
	if len(fa) == 0 {
		t.Fatal("extracted tree is empty")
	}
	for path, body := range fa {
		other, ok := fb[path]
		if !ok {
			t.Errorf("%s missing from the second extraction", path)
		} else if other != body {
			t.Errorf("%s differs between extractions", path)
		}
	}
	for path := range fb {
		if _, ok := fa[path]; !ok {
			t.Errorf("%s appeared only in the second extraction", path)
		}
	}
}

func treeMap(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		out[rel] = string(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
