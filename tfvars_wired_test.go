package roksbnkctl

import (
	"io/fs"
	"path"
	"regexp"
	"strings"
	"testing"
)

// validationOnlyRootVars are root variables that terraform deliberately never
// reads with `var.X` outside their own declaration. Their whole job is the
// validation block inside that declaration: the Go side acts on the setting
// (it shapes the cluster before terraform runs), and terraform's copy exists so
// a bad value is rejected up front rather than halfway through an apply.
//
// Keep this list SHORT and justified. Every entry is a variable whose value
// changes nothing in the HCL, so an entry added carelessly hides exactly the
// defect this test exists to catch.
var validationOnlyRootVars = map[string]string{
	"cluster_network_mode": "single-nic/multi-nic is applied by the Go cluster phase; the HCL keeps it only to reject a bad value early",
}

// TestEveryRootVariableIsRead catches the defect class that has now shipped
// twice: a setting that is declared as a root variable, rendered into
// terraform.tfvars by the Go renderer, documented in the book and the config
// reference, covered by passing Go tests — and read by no terraform anywhere.
// The feature is a no-op end to end.
//
//   - cneinstance_advanced_env was inert for its entire life. Every grep for the
//     name found it: in the declaration, the renderer, the docs and the tests.
//     That is precisely why it survived — a grep proves a name EXISTS, never
//     that its two halves are JOINED.
//   - install_cert_manager (resources.cert_manager.create) had the same shape.
//     Its own description promised "when false, cert_manager_namespace is passed
//     directly to flo"; nothing implemented that, so adopting a cluster that
//     already ran cert-manager was impossible and `bnk up` died on
//     `namespaces "cert-manager" already exists`.
//
// A variable nothing reads cannot change an apply, so this is cheap to check and
// would have caught both on the commit that introduced them.
func TestEveryRootVariableIsRead(t *testing.T) {
	varDecl := regexp.MustCompile(`(?m)^variable\s+"([^"]+)"`)

	src, err := fs.ReadFile(EmbeddedTerraform, "terraform/variables.tf")
	if err != nil {
		t.Fatalf("read root variables.tf: %v", err)
	}
	var declared []string
	for _, m := range varDecl.FindAllStringSubmatch(string(src), -1) {
		declared = append(declared, m[1])
	}
	if len(declared) < 50 {
		t.Fatalf("only %d root variables parsed; the regex has stopped matching", len(declared))
	}

	// Every .tf in the tree EXCEPT the root declaration file itself. A
	// validation block lives inside the `variable` block, so including
	// variables.tf would make every variable trivially "read".
	var body strings.Builder
	err = fs.WalkDir(EmbeddedTerraform, "terraform", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".tf" || p == "terraform/variables.tf" {
			return err
		}
		b, err := fs.ReadFile(EmbeddedTerraform, p)
		if err != nil {
			return err
		}
		body.Write(b)
		body.WriteString("\n")
		return nil
	})
	if err != nil {
		t.Fatalf("walk terraform tree: %v", err)
	}
	hcl := body.String()

	for _, name := range declared {
		read := regexp.MustCompile(`\bvar\.` + regexp.QuoteMeta(name) + `\b`).MatchString(hcl)
		why, exempt := validationOnlyRootVars[name]

		switch {
		case read && exempt:
			t.Errorf("var.%s IS read by terraform, so remove it from validationOnlyRootVars (was: %s)", name, why)
		case !read && !exempt:
			t.Errorf("root variable %q is declared, and the renderer may emit it, but NO terraform reads var.%s — "+
				"the setting it represents is a no-op end to end. Wire it to the resource it is supposed to gate, "+
				"or delete it. If it exists purely for its validation block, add it to validationOnlyRootVars with a reason.", name, name)
		}
	}
}
