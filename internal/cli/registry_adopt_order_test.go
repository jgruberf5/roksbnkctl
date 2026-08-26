package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #224. `adopt --verify-contents` reported 87 of 94 artifacts as missing against
// a mirror that was complete, because the engine captured the FAR credential
// BEFORE buildBOM had resolved it:
//
//	✗ charts/coremond: resolve source: ... DENIED: Unauthenticated request
//
// registryEngine copies source.SourceAuth(in.FARRepoURL, in.SourceSAB64) at
// construction; buildBOM is what fills in.SourceSAB64 from COS. The 7 that
// passed were the non-F5 dependencies, whose sources are public — which is what
// made it look like a mirror problem rather than a credential one.
//
// The ordering is one line and swapping it back leaves the build green, the
// suite green, and adopt --verify-contents broken against every private mirror.
// Parsed as an AST rather than grepped because a comment naming the call would
// satisfy a grep and prove nothing.
func TestTheRegistryEngineIsBuiltAfterTheBOM(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "registry_adopt.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing registry_adopt.go: %v", err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(f, func(n ast.Node) bool {
		d, ok := n.(*ast.FuncDecl)
		if !ok {
			return true
		}
		// The adopt command's RunE body, whichever name it carries.
		ast.Inspect(d.Body, func(m ast.Node) bool {
			if c, ok := m.(*ast.CallExpr); ok {
				if id, ok := c.Fun.(*ast.Ident); ok && id.Name == "buildBOM" {
					fn = d
				}
			}
			return true
		})
		return true
	})
	if fn == nil {
		t.Fatal("no function in registry_adopt.go calls buildBOM")
	}

	type call struct {
		name string
		pos  token.Pos
	}
	var calls []call
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := c.Fun.(*ast.Ident); ok {
			calls = append(calls, call{id.Name, c.Pos()})
		}
		return true
	})

	firstBOM, firstEngine := token.NoPos, token.NoPos
	for _, c := range calls {
		if c.name == "buildBOM" && firstBOM == token.NoPos {
			firstBOM = c.pos
		}
		if c.name == "registryEngine" && firstEngine == token.NoPos {
			firstEngine = c.pos
		}
	}
	if firstBOM == token.NoPos || firstEngine == token.NoPos {
		t.Fatalf("expected both buildBOM and registryEngine in %s; got %v", fn.Name.Name, calls)
	}
	if firstEngine < firstBOM {
		t.Errorf("registryEngine is constructed at %s, before buildBOM at %s.\n"+
			"registryEngine copies the FAR credential at construction and buildBOM is what resolves "+
			"it, so every repo.f5.com source resolves unauthenticated and --verify-contents reports "+
			"a complete mirror as missing (#224).",
			fset.Position(firstEngine), fset.Position(firstBOM))
	}
}
