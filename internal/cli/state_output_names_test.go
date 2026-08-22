package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// Every name the CLI reads out of terraform state has to exist as a ROOT
// output. config.ReadStateOutputs reads only the root state's .outputs, so a
// module-level output that nobody re-exports is not a broken build, a failing
// test, or an error at runtime — outString just returns "" and the caller takes
// its fallback path. On 2.4 that made `gateway status` look in the wrong
// namespace and skip the Infra and GatewaySettings blocks entirely, while the
// book described output the command could never produce.
//
// This is the second time on this branch that something was declared, wired
// halfway, documented, and read by nothing. Both times every text search for
// the name succeeded, because the name genuinely was present on both sides.
// What was missing was the join, so this test checks the join.
//
// The Go side is read from the AST rather than by regex: a name inside a
// comment is not a call, and a comment is not in the AST.
func TestEveryStateOutputTheCLIReadsExistsAtTheRoot(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	declared := map[string]bool{}
	outputRe := regexp.MustCompile(`(?m)^output\s+"([^"]+)"`)
	tfFiles, err := filepath.Glob(filepath.Join(root, "terraform", "*.tf"))
	if err != nil || len(tfFiles) == 0 {
		t.Fatalf("no root terraform files found: %v", err)
	}
	for _, f := range tfFiles {
		b, err := os.ReadFile(f)
		if err != nil {
			t.Fatalf("read %s: %v", f, err)
		}
		for _, m := range outputRe.FindAllStringSubmatch(string(b), -1) {
			declared[m[1]] = true
		}
	}
	if len(declared) == 0 {
		t.Fatal("parsed no root outputs at all; this test would pass vacuously")
	}

	// Helpers that pull a named value out of the state outputs map. Any
	// function whose first parameter is the outputs map and whose second is a
	// string literal name counts.
	readers := map[string]bool{"outString": true, "outStringSlice": true, "outBool": true}

	used := map[string]token.Position{}
	fset := token.NewFileSet()
	pkgDir := filepath.Join(root, "internal", "cli")
	pkgs, err := parser.ParseDir(fset, pkgDir, nil, 0)
	if err != nil {
		t.Fatalf("parse %s: %v", pkgDir, err)
	}
	for _, pkg := range pkgs {
		for name, file := range pkg.Files {
			if strings.HasSuffix(name, "_test.go") {
				continue
			}
			ast.Inspect(file, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok || len(call.Args) < 2 {
					return true
				}
				ident, ok := call.Fun.(*ast.Ident)
				if !ok || !readers[ident.Name] {
					return true
				}
				lit, ok := call.Args[1].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					return true
				}
				s, err := strconv.Unquote(lit.Value)
				if err != nil {
					return true
				}
				used[s] = fset.Position(lit.Pos())
				return true
			})
		}
	}
	if len(used) == 0 {
		t.Fatal("found no state-output reads in internal/cli; this test would pass vacuously")
	}

	for name, pos := range used {
		if !declared[name] {
			t.Errorf("%s reads terraform output %q, which no root module output declares.\n"+
				"  ReadStateOutputs sees only the ROOT state's .outputs, so this read always returns \"\" "+
				"and the caller silently takes its fallback path.\n"+
				"  Add `output %q { value = module.<m>.%s }` to terraform/outputs.tf.",
				pos, name, name, name)
		}
	}
}
