package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// A variable declared at several module layers must not disagree about its
// default, because the OUTER default wins: it is passed down as an explicit
// value, and the inner module can then no longer tell "unset" from "deliberately
// set to the default".
//
// This exists because I shipped exactly that bug. Making 2.4 default
// deploymentSize to Tiny, I cleared the default on the innermost module and left
// `default = "Small"` on the root and the wrapper. The root passed Small down,
// the inner module's line-aware logic saw an explicit request and honoured it,
// and every 2.4 install still got Small — with TMM then demanding hugepages the
// nodes do not have.
//
// The unit test I had written passed throughout, because it evaluates the inner
// module in ISOLATION where no outer default exists. A test that cannot see the
// layer above it cannot see this class of bug, which is why this one reads the
// declarations instead of evaluating one module.
func TestLayeredVariableDefaultsAgree(t *testing.T) {
	root := repoRootForDemoTest(t)

	var files []string
	err := filepath.Walk(filepath.Join(root, "terraform"), func(p string, info os.FileInfo, err error) error {
		if err == nil && !info.IsDir() && info.Name() == "variables.tf" {
			files = append(files, p)
		}
		return nil
	})
	if err != nil || len(files) < 3 {
		t.Fatalf("expected several variables.tf files, found %d (%v)", len(files), err)
	}

	type decl struct{ file, def string }
	seen := map[string][]decl{}

	block := regexp.MustCompile(`(?s)variable\s+"([a-z0-9_]+)"\s*\{(.*?)\n\}`)
	defRe := regexp.MustCompile(`(?m)^\s*default\s*=\s*(.+?)\s*$`)

	for _, f := range files {
		b, rerr := os.ReadFile(f)
		if rerr != nil {
			continue
		}
		rel, _ := filepath.Rel(root, f)
		for _, m := range block.FindAllStringSubmatch(string(b), -1) {
			d := "(none)"
			if dm := defRe.FindStringSubmatch(m[2]); dm != nil {
				d = strings.TrimSpace(dm[1])
			}
			seen[m[1]] = append(seen[m[1]], decl{rel, d})
		}
	}
	if len(seen) == 0 {
		t.Fatal("parsed no variable declarations; this test would pass vacuously")
	}

	// Only compare declarations in the SAME chain — one module's directory an
	// ancestor of the other's. A name shared by unrelated trees (`enabled` in flo
	// and in license) is two different variables that never see each other, and
	// comparing those buries the real finding in noise.
	sameChain := func(a, b string) bool {
		da, db := filepath.Dir(a)+"/", filepath.Dir(b)+"/"
		return strings.HasPrefix(da, db) || strings.HasPrefix(db, da)
	}

	var conflicts []string
	checked := 0
	for name, decls := range seen {
		if len(decls) < 2 {
			continue
		}
		var chained []decl
		for i := range decls {
			for j := range decls {
				if i != j && sameChain(decls[i].file, decls[j].file) {
					chained = append(chained, decls[i])
					break
				}
			}
		}
		if len(chained) < 2 {
			continue
		}
		checked++

		var first *decl
		for i := range chained {
			if chained[i].def == "(none)" {
				continue
			}
			if first == nil {
				first = &chained[i]
				continue
			}
			if chained[i].def != first.def {
				var where []string
				for _, d := range chained {
					where = append(where, d.file+" = "+d.def)
				}
				conflicts = append(conflicts, name+":\n  "+strings.Join(where, "\n  "))
				break
			}
		}
	}
	if checked == 0 {
		t.Fatal("no variable is declared at more than one layer; the walk or the regex is wrong")
	}

	// THE PART THAT MATTERS. A variable whose inner module chooses its default
	// CONDITIONALLY — per release line — is broken outright by any outer default,
	// because the outer value arrives as an explicit setting and the condition
	// never fires. These must carry no real default above the module that decides.
	for _, name := range []string{
		"cneinstance_deployment_size",
		"cneinstance_demo_mode",
		"cneinstance_whole_cluster_override",
	} {
		if len(seen[name]) == 0 {
			t.Errorf("%s is not declared anywhere; this guard no longer covers what it names", name)
			continue
		}
		for _, d := range seen[name] {
			inner := strings.Contains(filepath.ToSlash(d.file), "modules/cneinstance/")
			if !inner && d.def != "(none)" && d.def != `""` {
				t.Errorf("%s declares default %s in %s, above the module that chooses it per line.\n"+
					"That value is passed down as an EXPLICIT setting, so the line-aware default never "+
					"fires. This exact mistake shipped Small to every 2.4 install while the inner module "+
					"said Tiny, and cost a full validation run to find.", name, d.def, d.file)
			}
		}
	}

	// The rest is a RATCHET, not a wall. Conflicting defaults across a chain are
	// a widespread pre-existing pattern here, and most are harmless because the
	// value is always passed explicitly from above anyway. Failing all of them
	// would force a refactor nobody asked for; this stops the number growing.
	const ceiling = 17
	if len(conflicts) > ceiling {
		t.Errorf("%d chained variables declare conflicting defaults, up from %d:\n%s",
			len(conflicts), ceiling, strings.Join(conflicts, "\n"))
	}
	t.Logf("checked %d chained variables; %d carry conflicting defaults (ceiling %d)",
		checked, len(conflicts), ceiling)
}
