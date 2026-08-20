package config

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"
)

// #114. OverrideFromEnv spelled each variable's name three times — to read it,
// to assign it, and to report it — and the three could drift independently.
// Worse, the surface was not enumerable: the guards that keep .env.example and
// the docs honest discovered overrides by regex-scraping this file, so they
// silently covered less whenever the code's shape changed.
//
// SupportedOverrideNames is now the authoritative list, and this guard is what
// makes it trustworthy: it compares the list BIDIRECTIONALLY against every
// ROKSBNKCTL_* string literal the package's code carries. The first cut of the
// list checked only literal envValue("X") reads and silently omitted 19 live
// overrides — the code reads variables in four shapes (a literal, a
// stringOverrides row, the hoisted tables in envoverride_flp.go, and the
// computed per-zone family), and a guard that sees fewer shapes than the code
// uses recreates the drift it exists to stop, one level up.
func TestSupportedOverrideNamesMatchWhatTheCodeCarries(t *testing.T) {
	carried := overrideNamesInSource(t)
	supported := SupportedOverrideNames()

	for _, name := range carried {
		if name == zoneOverridePrefix {
			// The computed family's literal half. The full names are enumerated
			// by zoneOverrideNames, checked below.
			continue
		}
		if !slices.Contains(supported, name) {
			t.Errorf("%s appears in the override code but not in SupportedOverrideNames() — "+
				"the .env.example parity guard and the documentation guard cannot see it, so it "+
				"can drop out of the demos and docs in total silence.", name)
		}
	}

	for _, name := range supported {
		if strings.HasPrefix(name, zoneOverridePrefix) && zoneIndexed(name) {
			continue // computed at read time; enumerated from zoneFields, not a literal
		}
		if !slices.Contains(carried, name) {
			t.Errorf("%s is declared supported but no code carries it — drop it, or the "+
				"documented surface promises a variable that does nothing.", name)
		}
	}

	// The computed family must stay derived from the SAME declarations the
	// reader uses: maxNetworkZones × zoneFields. If either changes, the surface
	// follows automatically; this pins that the derivation stays hooked up.
	if got, want := len(zoneOverrideNames()), maxNetworkZones*len(zoneFields); got != want {
		t.Errorf("zoneOverrideNames() returned %d names, want %d (maxNetworkZones × zoneFields)", got, want)
	}
	for _, n := range zoneOverrideNames() {
		if !slices.Contains(supported, n) {
			t.Errorf("computed zone override %s missing from SupportedOverrideNames()", n)
		}
	}
}

// The regression that motivated the guard above: the surface once omitted 19
// live overrides, so trimming ROKSBNKCTL_COS_BUCKET (** REQUIRED, no default **)
// from the demo .env.example passed the parity test while every Argo blueprint
// silently lost the variable. One representative per formerly-missing family
// stays pinned here by name.
func TestFormerlyMissingFamiliesAreOnTheSurface(t *testing.T) {
	supported := SupportedOverrideNames()
	for _, name := range []string{
		"ROKSBNKCTL_COS_BUCKET",                 // cosOverrides
		"ROKSBNKCTL_FLP_VSI_SSH_KEY",            // flpVSIStringOverrides
		"ROKSBNKCTL_FLP_VSI_NAME_PREFIX",        // moved out of the misnested bespoke block
		"ROKSBNKCTL_CLUSTER_HTTP_ALLOWED_CIDRS", // sgCIDROverrides
		"ROKSBNKCTL_VLAN_PREFIXLEN_EXTERNAL",    // vlanPerVLANOverrides
		"ROKSBNKCTL_ZONE1_EXT_VLAN_CIDR",        // computed zone family
		"ROKSBNKCTL_ZONE3_INTERNAL_SELFIP",      // last of the computed family
	} {
		if !slices.Contains(supported, name) {
			t.Errorf("%s missing from SupportedOverrideNames() — the 19-name coverage "+
				"regression is back for its family", name)
		}
	}
}

// The package doc's env-to-field tables are the biggest in-repo documentation
// of this surface, and until this test they were checked by nothing — the
// CHANGELOG claimed a documentation guard that did not exist. Every supported
// name must be documented in a comment in the two override files, and every
// name those comments mention must be supported, so the tables can neither
// fall behind the surface nor promise variables that do nothing.
func TestOverrideDocsMatchTheSurface(t *testing.T) {
	documented := overrideNamesInComments(t)
	supported := SupportedOverrideNames()

	for _, name := range supported {
		if strings.HasPrefix(name, zoneOverridePrefix) && zoneIndexed(name) {
			continue // documented as the ROKSBNKCTL_ZONE<n>_* family, checked below
		}
		if !slices.Contains(documented, name) {
			t.Errorf("%s is supported but appears in no comment in envoverride.go / "+
				"envoverride_flp.go — add it to the env-to-field table", name)
		}
	}
	if !slices.Contains(documented, zoneOverridePrefix) {
		t.Errorf("the computed ROKSBNKCTL_ZONE<n>_* family is not documented")
	}
	isFamilyShorthand := func(name string) bool {
		// "every other ROKSBNKCTL_FLP_VSI_* variable" and the like — a
		// documented prefix that real supported names extend.
		for _, s := range supported {
			if strings.HasPrefix(s, name+"_") {
				return true
			}
		}
		return false
	}
	for _, name := range documented {
		if name == zoneOverridePrefix || (strings.HasPrefix(name, zoneOverridePrefix) && zoneIndexed(name)) {
			// The family and its members are documented as ROKSBNKCTL_ZONE<n>_*
			// shorthand; membership is pinned against zoneFields above.
			continue
		}
		if isFamilyShorthand(name) {
			continue
		}
		if !slices.Contains(supported, name) {
			t.Errorf("%s is documented but not supported — the table promises a variable "+
				"that does nothing", name)
		}
	}
}

// The report string is derived from the row, so it cannot disagree with the
// variable it describes. This pins that: before the table, "field (ENV)" was
// typed out separately from the envValue call above it.
func TestTheAppliedReportNamesTheVariableThatWasSet(t *testing.T) {
	for _, o := range stringOverrides {
		if o.env == "" || o.field == "" || o.set == nil {
			t.Errorf("incomplete stringOverride row: %+v", o.env)
			continue
		}
		if !strings.HasPrefix(o.env, "ROKSBNKCTL_") {
			t.Errorf("%s does not use the ROKSBNKCTL_ prefix", o.env)
		}
		ws := &Workspace{}
		t.Setenv(o.env, "sentinel-value")
		applied := OverrideFromEnv(ws)

		want := o.field + " (" + o.env + ")"
		if !slices.Contains(applied, want) {
			t.Errorf("setting %s did not report %q; got %v", o.env, want, applied)
		}
	}
}

// No variable may appear twice across the tables, the bespoke list, and the
// computed family: it would be applied twice and reported twice, and which one
// wins would depend on ordering.
func TestNoOverrideIsDeclaredTwice(t *testing.T) {
	seen := map[string]int{}
	for _, o := range stringOverrides {
		seen[o.env]++
	}
	for _, o := range sgCIDROverrides {
		seen[o.env]++
	}
	for _, o := range vlanPerVLANOverrides {
		seen[o.env]++
	}
	for _, o := range flpVSIStringOverrides {
		seen[o.env]++
	}
	for _, o := range cosOverrides {
		seen[o.env]++
	}
	for _, n := range bespokeOverrideNames {
		seen[n]++
	}
	for _, n := range zoneOverrideNames() {
		seen[n]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("%s is declared %d times across the override tables and lists", name, n)
		}
	}
}

// zoneIndexed reports whether name is a full per-zone variable
// (ROKSBNKCTL_ZONE<digit>...), as opposed to the bare computed prefix.
func zoneIndexed(name string) bool {
	rest := strings.TrimPrefix(name, zoneOverridePrefix)
	return rest != "" && rest[0] >= '0' && rest[0] <= '9'
}

var overrideNameRE = regexp.MustCompile(`(ROKSBNKCTL_[A-Z0-9_]+|IBMCLOUD_API_KEY)`)

// overrideSourceFiles are where the override surface lives; both the code scan
// and the docs scan read exactly these.
var overrideSourceFiles = []string{"envoverride.go", "envoverride_flp.go"}

// overrideNamesInSource extracts every override-shaped name from the STRING
// LITERALS of the override files — reads, table rows, and applied-report
// labels alike. Parsed from the AST rather than regexed from raw text, so
// comments cannot satisfy it and a "//" inside a string cannot confuse it.
// This is deliberately shape-blind: it sees a literal wherever it appears,
// which is what closes the gap the envValue("X")-only scrape left open.
func overrideNamesInSource(t *testing.T) []string {
	t.Helper()
	return extractOverrideNames(t, func(f *ast.File, collect func(string)) {
		ast.Inspect(f, func(n ast.Node) bool {
			lit, ok := n.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			s, err := strconv.Unquote(lit.Value)
			if err != nil {
				return true
			}
			collect(s)
			return true
		})
	})
}

// overrideNamesInComments extracts every override-shaped name mentioned in the
// override files' comments — the env-to-field documentation tables.
func overrideNamesInComments(t *testing.T) []string {
	t.Helper()
	return extractOverrideNames(t, func(f *ast.File, collect func(string)) {
		for _, cg := range f.Comments {
			collect(cg.Text())
		}
	})
}

func extractOverrideNames(t *testing.T, visit func(*ast.File, func(string))) []string {
	t.Helper()
	seen := map[string]bool{}
	var out []string
	fset := token.NewFileSet()
	for _, name := range overrideSourceFiles {
		f, err := parser.ParseFile(fset, filepath.Clean(name), nil, parser.ParseComments)
		if err != nil {
			t.Fatalf("parsing %s: %v", name, err)
		}
		visit(f, func(text string) {
			for _, m := range overrideNameRE.FindAllString(text, -1) {
				// A trailing underscore is a prefix under construction (a label
				// like "ROKSBNKCTL_ZONE*_*" or the computed-prefix literal);
				// normalise it to the bare prefix.
				m = strings.TrimRight(m, "_")
				if m == "ROKSBNKCTL" {
					// A bare prefix (e.g. the doc shorthand "ROKSBNKCTL_{A,B}")
					// names no variable.
					continue
				}
				if !seen[m] {
					seen[m] = true
					out = append(out, m)
				}
			}
		})
	}
	if len(out) == 0 {
		t.Fatal("found no override names — the extraction has drifted")
	}
	slices.Sort(out)
	return out
}
