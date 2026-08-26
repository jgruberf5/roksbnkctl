package cli

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

// #222. Each of these produces a credential Forge stores happily and can never
// use — and Forge then reports infrastructure access as CONFIGURED, so every
// later failure points somewhere other than the credential. That is the whole
// reason they are refused here rather than left to the server.
func TestSSHCredentialInputsThatCanNeverWorkAreRefused(t *testing.T) {
	const keyOK, hostOK, userOK = "/tmp/id", "52.116.1.2", "ubuntu"

	cases := []struct {
		name             string
		host, user, key  string
		wantErrSubstring string
	}{
		{"no key", hostOK, userOK, "", "--key is required"},
		{"no host", "", userOK, keyOK, "--host is required"},
		{
			// The reported trap: `flp status` prints a services-VPC endpoint
			// Forge sits outside of, so pasting it produces a credential that
			// can never connect.
			"an endpoint URL instead of a host",
			"https://10.243.1.4:8443", userOK, keyOK,
			"looks like an endpoint URL",
		},
		{"empty ssh user", hostOK, "", keyOK, "--ssh-username cannot be empty"},
		{"whitespace ssh user", hostOK, "   ", keyOK, "--ssh-username cannot be empty"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := validateSSHCredentialInputs(c.host, c.user, c.key)
			if err == nil {
				t.Fatalf("accepted %q/%q/%q — Forge would store a credential that cannot log in",
					c.host, c.user, c.key)
			}
			if !strings.Contains(err.Error(), c.wantErrSubstring) {
				t.Errorf("error = %q, want it to mention %q", err, c.wantErrSubstring)
			}
		})
	}

	if err := validateSSHCredentialInputs(hostOK, userOK, keyOK); err != nil {
		t.Errorf("a valid set was rejected: %v", err)
	}
}

// Every other bnkforge subcommand uses --username for the FORGE login. Reusing
// it for the appliance's SSH user meant `--username admin` silently set the SSH
// user while the operator believed they had authenticated to Forge — storing a
// credential that cannot log in, which is the failure --expect-fingerprint
// exists to prevent, arriving through the flag names instead.
func TestTheSSHCredentialFlagsDoNotOverloadUsername(t *testing.T) {
	f := bnkforgeSSHCredentialCmd.Flags()

	u := f.Lookup("username")
	if u == nil {
		t.Fatal("no --username flag: every other bnkforge subcommand has one for the Forge login")
	}
	if !strings.Contains(strings.ToLower(u.Usage), "forge") {
		t.Errorf("--username is documented as %q — on every other bnkforge subcommand it is the "+
			"FORGE login, and overloading it here is silently destructive", u.Usage)
	}
	s := f.Lookup("ssh-username")
	if s == nil {
		t.Fatal("no --ssh-username flag: the appliance user is the odd one out and takes the qualifier")
	}
	if s.DefValue != "ubuntu" {
		t.Errorf("--ssh-username default = %q, want ubuntu", s.DefValue)
	}
	// The two must not be the same variable, or setting one sets the other.
	if u.Value == s.Value {
		t.Error("--username and --ssh-username are bound to the same variable")
	}
}

// The credential link alone leaves infra_enabled false and the appliance
// unreachable — the original #222 symptom. ConfigureSSH is the call that turns
// infrastructure access on, and dropping it leaves the build green, the suite
// green, and the command reporting a project it has not actually configured.
//
// Parsed as an AST rather than grepped because a comment naming the call would
// satisfy a grep and prove nothing.
func TestTheSSHCredentialCommandConfiguresInfrastructureAccess(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "bnkforge_sshcred.go", nil, 0)
	if err != nil {
		t.Fatalf("parsing bnkforge_sshcred.go: %v", err)
	}
	var fn *ast.FuncDecl
	ast.Inspect(f, func(n ast.Node) bool {
		if d, ok := n.(*ast.FuncDecl); ok && d.Name.Name == "runBNKForgeSSHCredential" {
			fn = d
			return false
		}
		return true
	})
	if fn == nil {
		t.Fatal("runBNKForgeSSHCredential not found")
	}

	var calls []string
	ast.Inspect(fn.Body, func(n ast.Node) bool {
		c, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		if sel, ok := c.Fun.(*ast.SelectorExpr); ok {
			calls = append(calls, sel.Sel.Name)
		}
		return true
	})

	want := map[string]string{
		"EnsureSSHCredential": "stores the key in Forge",
		"ConfigureSSH":        "turns infrastructure access on — without it infra_enabled stays false and the appliance is unreachable (#222)",
		"AttachSSHCredential": "links the credential to the project and reads back what stuck",
	}
	for name, why := range want {
		found := false
		for _, c := range calls {
			if c == name {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("runBNKForgeSSHCredential never calls %s — %s.\ncalls: %v", name, why, calls)
		}
	}
}
