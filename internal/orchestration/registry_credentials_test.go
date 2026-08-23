package orchestration

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// A generic mirror with a username and no password cannot authenticate. The
// pull falls through to the literal username "unused" and an external registry
// answers 401 — but not until flo is installed and IAM trusted profiles are
// created, roughly fifteen minutes into the apply. This is knowable before
// anything is touched.
func TestCheckMirrorCredentials(t *testing.T) {
	ws := func(target, host, user, pass string) *config.Context {
		return &config.Context{Workspace: &config.Workspace{
			Registry: &config.RegistryCfg{
				Target:             target,
				GenericHost:        host,
				GenericUsername:    user,
				GenericPasswordB64: pass,
			},
		}}
	}

	cases := []struct {
		name    string
		cctx    *config.Context
		wantErr bool
	}{
		{"username with no password is refused", ws("generic", "artifactory.example.com", "admin", ""), true},
		{"username with a password is fine", ws("generic", "artifactory.example.com", "admin", "dG9rZW4="), false},
		{"anonymous mirror: neither set", ws("generic", "artifactory.example.com", "", ""), false},
		{"icr is a different credential path", ws("icr", "", "admin", ""), false},
		{"empty target resolves to icr, not generic", ws("", "", "admin", ""), false},
		{"no host configured yet", ws("generic", "", "admin", ""), false},
		{"target casing must not matter", ws("Generic", "artifactory.example.com", "admin", ""), true},
		{"whitespace is not a password", ws("generic", "artifactory.example.com", "admin", "   "), true},
		{"nil workspace", &config.Context{}, false},
		{"nil registry: no mirror configured at all", &config.Context{Workspace: &config.Workspace{}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := checkMirrorCredentials(c.cctx, io.Discard)
			if c.wantErr && err == nil {
				t.Fatal("expected a refusal, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected no error, got %v", err)
			}
			if !c.wantErr {
				return
			}
			// The message has to name the settings that fix it, in both the
			// file and the environment, or it is no better than the 401.
			for _, want := range []string{
				"registry.generic_password_b64",
				"ROKSBNKCTL_GENERIC_PASSWORD",
				"401",
				"artifactory.example.com",
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal does not mention %q:\n%v", want, err)
				}
			}
		})
	}
}

// A preflight nothing calls is the defect that shipped twice in this repo
// already: declared, tested, documented, and inert. Comments are not in the AST,
// so this cannot be satisfied by prose describing the call.
func TestCheckMirrorCredentialsIsWiredIntoTheApplyPath(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "lifecycle.go", nil, 0)
	if err != nil {
		t.Fatalf("parse lifecycle.go: %v", err)
	}
	var called bool
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if id, ok := call.Fun.(*ast.Ident); ok && id.Name == "checkMirrorCredentials" {
			called = true
		}
		return true
	})
	if !called {
		t.Error("lifecycle.go never calls checkMirrorCredentials — the preflight cannot run, " +
			"and an unauthenticated mirror will fail 401 part-way through the apply instead")
	}
}
