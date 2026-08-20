package config

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// #114. OverrideFromEnv spelled each variable's name three times — to read it,
// to assign it, and to report it — and the three could drift independently.
// Worse, the surface was not enumerable: the guards that keep .env.example and
// the docs honest discovered overrides by regex-scraping this file, so they
// silently covered less whenever the code's shape changed.
//
// SupportedOverrideNames is now the authoritative list. This is what makes it
// trustworthy: it must match every variable the code actually reads. An
// override added without landing in stringOverrides or bespokeOverrideNames
// fails here rather than quietly dropping out of the docs and the demo
// allowlist.
func TestBespokeOverridesMatchWhatTheCodeReads(t *testing.T) {
	// After the table, a literal envValue("NAME") in the source is BY
	// DEFINITION a bespoke override — the uniform ones are read through
	// envValue(o.env) from the table. So these two sets must be equal, and any
	// difference is a real gap in the declared surface.
	literals := envVarLiteralsInSource(t)
	declared := slices.Clone(bespokeOverrideNames)
	slices.Sort(declared)

	for _, name := range literals {
		if !slices.Contains(declared, name) {
			t.Errorf("%s is read by its own block but is not in bespokeOverrideNames — "+
				"SupportedOverrideNames() omits it, so the .env.example parity guard and the "+
				"documentation guards cannot see it and it can be forgotten silently.", name)
		}
	}
	for _, name := range declared {
		if !slices.Contains(literals, name) {
			t.Errorf("%s is declared bespoke but no block reads it — drop it, or the "+
				"documented surface promises a variable that does nothing.", name)
		}
	}
}

// The whole surface is the two lists together, and every name in it must be
// unique and reachable.
func TestSupportedOverrideNamesIsTheWholeSurface(t *testing.T) {
	all := SupportedOverrideNames()
	if got, want := len(all), len(stringOverrides)+len(bespokeOverrideNames); got != want {
		t.Errorf("SupportedOverrideNames() returned %d names, want %d (%d table + %d bespoke)",
			got, want, len(stringOverrides), len(bespokeOverrideNames))
	}
	if !slices.IsSorted(all) {
		t.Error("SupportedOverrideNames() is not sorted; callers diff this list")
	}
	for _, o := range stringOverrides {
		if !slices.Contains(all, o.env) {
			t.Errorf("table row %s missing from SupportedOverrideNames()", o.env)
		}
	}
	for _, n := range bespokeOverrideNames {
		if !slices.Contains(all, n) {
			t.Errorf("bespoke override %s missing from SupportedOverrideNames()", n)
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

// Every table row must actually reach a field. A row whose setter writes
// nowhere reports success and changes nothing — the silent failure this whole
// surface is prone to.
func TestEveryTableRowWritesSomething(t *testing.T) {
	for _, o := range stringOverrides {
		// Workspace holds slices and maps, so it is not comparable; marshal to
		// YAML — which is the shape that actually gets persisted — and compare
		// that. A setter that writes nowhere leaves it identical.
		before, err := yaml.Marshal(&Workspace{})
		if err != nil {
			t.Fatal(err)
		}
		ws := &Workspace{}
		o.set(ws, "sentinel-value")
		after, err := yaml.Marshal(ws)
		if err != nil {
			t.Fatal(err)
		}
		if string(before) == string(after) {
			t.Errorf("%s (%s): its setter left the workspace unchanged — the override "+
				"would report success and do nothing", o.env, o.field)
		}
		if !strings.Contains(string(after), "sentinel-value") {
			t.Errorf("%s (%s): the value did not reach any persisted field", o.env, o.field)
		}
	}
}

// No variable may appear in both tables: it would be applied twice and reported
// twice, and which one wins would depend on ordering.
func TestNoOverrideIsDeclaredTwice(t *testing.T) {
	seen := map[string]int{}
	for _, o := range stringOverrides {
		seen[o.env]++
	}
	for _, n := range bespokeOverrideNames {
		seen[n]++
	}
	for name, n := range seen {
		if n > 1 {
			t.Errorf("%s is declared %d times across stringOverrides and bespokeOverrideNames", name, n)
		}
	}
}

// envVarLiteralsInSource finds every envValue("LITERAL") in the config package.
//
// A scrape is the wrong foundation for the parity guards — replacing it is what
// this change is for — but it is the right tool HERE, precisely because it is
// independent of the declaration it checks. After the table, a literal is by
// definition a bespoke override.
func envVarLiteralsInSource(t *testing.T) []string {
	t.Helper()
	re := regexp.MustCompile(`envValue\("([A-Z0-9_]+)"\)`)
	var out []string
	seen := map[string]bool{}
	root := filepath.Join("..", "..")
	err := filepath.Walk(filepath.Join(root, "internal", "config"), func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		for _, m := range re.FindAllStringSubmatch(string(b), -1) {
			if !seen[m[1]] {
				seen[m[1]] = true
				out = append(out, m[1])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking internal/config: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("found no overrides — the extraction regex has drifted")
	}
	slices.Sort(out)
	return out
}
