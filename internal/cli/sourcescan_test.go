package cli

import (
	"os"
	"strings"
	"testing"
)

// Several guards in this package assert on the SOURCE rather than on behaviour,
// because what they check is a wiring property that no runtime call can reach:
// that a guard is actually invoked at three call sites, that os.Exit has not
// crept back into a command, that both branches of one long function report a
// failure the same way. Each was written on its own branch, and each grew its
// own copy of "find a function and return its body".
//
// One copy, two entry points: funcBody when the caller already holds the
// source, packageFuncBody when it only knows a name.

// funcBody returns the body of `func <name>` within src, delimited by the
// closing brace in column 0.
func funcBody(t *testing.T, src, name string) string {
	t.Helper()
	body, ok := findFuncBody(src, name)
	if !ok {
		t.Fatalf("%s not found — this test can no longer detect the gap", name)
	}
	return body
}

// packageFuncBody finds `func <name>` in any non-test file of this package and
// returns its body.
//
// Searching by name rather than reading one named file is deliberate: the
// registry subcommands moved out of registry.go into one file each (#117), and
// a guard with the filename hard-coded failed rather than silently stopping
// checking. Locating by name means the next move costs nothing.
func packageFuncBody(t *testing.T, name string) string {
	t.Helper()
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatalf("reading the package directory: %v", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") || strings.HasSuffix(e.Name(), "_test.go") {
			continue
		}
		src, rerr := os.ReadFile(e.Name())
		if rerr != nil {
			t.Fatalf("reading %s: %v", e.Name(), rerr)
		}
		if body, ok := findFuncBody(string(src), name); ok {
			return body
		}
	}
	t.Fatalf("%s not found in any file in this package — this test can no longer detect the gap", name)
	return ""
}

func findFuncBody(src, name string) (string, bool) {
	start := strings.Index(src, "func "+name+"(")
	if start < 0 {
		return "", false
	}
	rest := src[start:]
	end := strings.Index(rest, "\n}\n")
	if end < 0 {
		return "", false
	}
	return rest[:end], true
}
