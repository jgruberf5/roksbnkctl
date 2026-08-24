package roksbnkctl

import (
	"io/fs"
	"path"
	"regexp"
	"sort"
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

// TestEveryRootVariableReachesTheModuleThatReadsIt catches the layer of
// camouflage that let twenty-four module inputs ship inert past the test above.
//
// terraform/main.tf declared `cneinstance_tmm_replicas` (and twenty-two
// siblings: the whole TMM placement set, externalBigip, gateway_api_version,
// demo_mode, whole_cluster_override, tcp_settings, hugepages). The Go renderer
// wrote each into terraform.tfvars. The config reference documented them. And
// the `module "cne_instance"` block never passed a single one down. The wrapping
// module declares variables of the SAME NAMES with the SAME defaults, so
// `var.cneinstance_tmm_replicas` inside it resolved to the default and the
// operator's value went nowhere.
//
// TestEveryRootVariableIsRead greps the whole tree for `var.<name>`, finds the
// hit inside the child module, and pronounces the root variable read. Its own
// doc comment states the principle it then fell to: a grep proves a name EXISTS,
// never that its two halves are JOINED. Same-named variables at two levels is
// precisely the case where existence and joining come apart.
//
// So this checks the JOIN, at every module call in the tree: if a parent
// declares X and the child it calls also declares X, the call has to pass it.
// Terraform will not say a word otherwise — an unassigned module input silently
// takes the child's default, which is exactly what "inert end to end" looks like
// from the inside.
func TestEveryRootVariableReachesTheModuleThatReadsIt(t *testing.T) {
	dirs := map[string][]string{} // dir -> .tf file paths
	err := fs.WalkDir(EmbeddedTerraform, "terraform", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".tf" {
			return err
		}
		dir := path.Dir(p)
		dirs[dir] = append(dirs[dir], p)
		return nil
	})
	if err != nil {
		t.Fatalf("walk terraform tree: %v", err)
	}

	varDecl := regexp.MustCompile(`(?m)^variable\s+"([^"]+)"`)
	declaredIn := func(dir string) map[string]bool {
		out := map[string]bool{}
		for _, f := range dirs[dir] {
			b, err := fs.ReadFile(EmbeddedTerraform, f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			for _, m := range varDecl.FindAllStringSubmatch(string(b), -1) {
				out[m[1]] = true
			}
		}
		return out
	}
	sourceArg := regexp.MustCompile(`(?m)^\s*source\s*=\s*"([^"]+)"`)

	calls := 0
	for dir, files := range dirs {
		parentVars := declaredIn(dir)
		for _, f := range files {
			b, err := fs.ReadFile(EmbeddedTerraform, f)
			if err != nil {
				t.Fatalf("read %s: %v", f, err)
			}
			for _, blk := range hclModuleBlocks(string(b)) {
				src := sourceArg.FindStringSubmatch(blk.body)
				if src == nil || !strings.HasPrefix(src[1], ".") {
					continue // registry / remote module: no local declarations to compare against
				}
				child := path.Clean(path.Join(dir, src[1]))
				if _, ok := dirs[child]; !ok {
					t.Errorf("%s: module %q sources %q, which holds no .tf files", f, blk.name, src[1])
					continue
				}
				calls++
				assigned := blk.arguments()
				var dropped []string
				for v := range declaredIn(child) {
					if parentVars[v] && !assigned[v] {
						dropped = append(dropped, v)
					}
				}
				sort.Strings(dropped)
				for _, v := range dropped {
					t.Errorf("%s: module %q declares its own %q and so does this module, but the call does not pass it — "+
						"terraform silently uses the child's default, so setting %s here changes nothing. "+
						"Add `%s = var.%s` to the module block, or delete the parent's declaration.",
						f, blk.name, v, v, v, v)
				}
			}
		}
	}

	// A parse that quietly stopped matching would report zero problems, which is
	// indistinguishable from a healthy tree.
	if calls < 8 {
		t.Fatalf("only %d local module calls parsed out of the terraform tree; the block parser has stopped matching", calls)
	}
}

type hclBlock struct {
	name string
	body string // between the braces
}

// arguments returns the top-level `name =` keys assigned in the block. Depth is
// tracked so an argument name inside a nested object literal is not mistaken for
// an input to the module itself.
func (b hclBlock) arguments() map[string]bool {
	arg := regexp.MustCompile(`^([a-z0-9_]+)\s*=`)
	out := map[string]bool{}
	depth := 0
	for _, line := range strings.Split(b.body, "\n") {
		trimmed := strings.TrimSpace(line)
		if depth == 0 && !strings.HasPrefix(trimmed, "#") && !strings.HasPrefix(trimmed, "//") {
			if m := arg.FindStringSubmatch(trimmed); m != nil {
				out[m[1]] = true
			}
		}
		if i := strings.IndexAny(line, "#"); i >= 0 {
			line = line[:i]
		}
		depth += strings.Count(line, "{") + strings.Count(line, "[") + strings.Count(line, "(")
		depth -= strings.Count(line, "}") + strings.Count(line, "]") + strings.Count(line, ")")
		if depth < 0 {
			depth = 0
		}
	}
	return out
}

// hclModuleBlocks returns every top-level `module "x" { ... }` block, matched by
// balancing braces rather than by looking for a closing line — a module block
// containing a nested object would otherwise end at the wrong place and its
// remaining arguments would read as unassigned.
func hclModuleBlocks(src string) []hclBlock {
	var out []hclBlock
	head := regexp.MustCompile(`(?m)^module\s+"([^"]+)"\s*\{`)
	for _, loc := range head.FindAllStringSubmatchIndex(src, -1) {
		name := src[loc[2]:loc[3]]
		depth := 1
		i := loc[1]
		inStr := false
		for ; i < len(src) && depth > 0; i++ {
			switch c := src[i]; {
			case inStr:
				if c == '\\' {
					i++
				} else if c == '"' {
					inStr = false
				}
			case c == '"':
				inStr = true
			case c == '#':
				for i < len(src) && src[i] != '\n' {
					i++
				}
			case c == '{':
				depth++
			case c == '}':
				depth--
			}
		}
		if depth == 0 {
			out = append(out, hclBlock{name: name, body: src[loc[1] : i-1]})
		}
	}
	return out
}
