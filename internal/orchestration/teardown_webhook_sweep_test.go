package orchestration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// #208. The sweep only helps if it runs BEFORE terraform destroys the namespace,
// and on EVERY backend — the webhook deadlock is a property of the cluster, not
// of which process runs terraform.
//
// Both call sites are one line each, and deleting either leaves the build green,
// the suite green, and `bnk down` silently back to hanging in Terminating. The
// AST is parsed rather than the text scanned because a comment naming the
// function would satisfy a grep and prove nothing.
func TestRunTrialDownSweepsWebhooksOnEveryBackend(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "lifecycle.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing lifecycle.go: %v", err)
	}

	var fn *ast.FuncDecl
	ast.Inspect(f, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == "RunTrialDown" {
			fn = d
			return false
		}
		return true
	})
	if fn == nil {
		t.Fatal("RunTrialDown not found in lifecycle.go")
	}

	// Every call in the function body, in source order, so ordering can be checked.
	var calls []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		switch fun := c.Fun.(type) {
		case *ast.Ident:
			calls = append(calls, fun.Name)
		case *ast.SelectorExpr:
			calls = append(calls, fun.Sel.Name)
		}
		return true
	})

	sweeps, dockers := 0, 0
	firstSweep, firstDocker := -1, -1
	for i, c := range calls {
		switch c {
		case "sweepTeardownWebhooks":
			sweeps++
			if firstSweep < 0 {
				firstSweep = i
			}
		case "runTerraformLifecycleDocker":
			dockers++
			if firstDocker < 0 {
				firstDocker = i
			}
		}
	}

	// One for the local backend, one for the containerised path.
	if sweeps < 2 {
		t.Errorf("RunTrialDown calls sweepTeardownWebhooks %d time(s); want 2 — the "+
			"local and docker/ssh backends both destroy the namespace, and a backend "+
			"without the sweep hangs in Terminating with no sign of why", sweeps)
	}

	// On the containerised path the sweep must come first, or terraform starts
	// destroying before the webhook is gone and the deadlock is back.
	if dockers > 0 && firstSweep > firstDocker {
		t.Error("the docker/ssh dispatch happens before sweepTeardownWebhooks; the " +
			"namespace destroy would begin with the webhook still installed")
	}
}
