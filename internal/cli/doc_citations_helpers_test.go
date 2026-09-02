package cli

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// repoRootForDocTest walks up from the package directory to the module root.
func repoRootForDocTest(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for i := 0; i < 6; i++ {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}
	t.Skip("module root not found")
	return ""
}

func walkGoFiles(t *testing.T, root string, fn func(path string, lines []string)) {
	t.Helper()
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".go") {
			return nil //nolint:nilerr // an unreadable tree is not this test's subject
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return nil //nolint:nilerr
		}
		fn(p, strings.Split(string(b), "\n"))
		return nil
	})
}
