package config

import (
	"strings"
	"testing"
)

// #175. The 2.4 CNEInstance takes an advanced.<component>.env[] list for each of
// ~26 components. A fixed override table cannot enumerate that and would go
// stale the first time F5 adds one, so this is a COMPUTED family like the
// per-zone variables.
//
// Not polish: `init --non-interactive` builds config.yaml from the environment
// alone and every BNK Forge module configures the tool that way, so a field
// reachable only through YAML cannot be used by a blueprint at all.

func TestAdvancedEnvReachesTheConfig(t *testing.T) {
	t.Setenv("ROKSBNKCTL_BNK_ADV_TMM_ENV_TMM_DEFAULT_MTU", "9000")
	t.Setenv("ROKSBNKCTL_BNK_ADV_CNECONTROLLER_ENV_USE_GATEWAY_SETTINGS", "true")

	ws := &Workspace{}
	applied := OverrideAdvancedEnvFromEnv(ws)
	if len(applied) != 2 {
		t.Fatalf("expected 2 overrides applied, got %d: %v", len(applied), applied)
	}
	// The component key must be the CNEInstance's own camelCase spelling — the
	// env-var name cannot preserve it, and a CR key of "cnecontroller" is not the
	// one the product reads.
	if got := ws.BNK.Advanced["cneController"].Env["USE_GATEWAY_SETTINGS"]; got != "true" {
		t.Errorf("cneController env not set correctly: %#v", ws.BNK.Advanced)
	}
	if got := ws.BNK.Advanced["tmm"].Env["TMM_DEFAULT_MTU"]; got != "9000" {
		t.Errorf("tmm env not set correctly: %#v", ws.BNK.Advanced)
	}
}

// The split is on the FIRST "_ENV_". A component name cannot contain it — the
// component set is camelCase words like tmm, cneController, pseudoCNI — but a
// VARIABLE name certainly can, and TMM_ENV_OVERRIDE is a plausible F5 setting.
//
// This test previously asserted the LAST separator and justified it in a
// comment claiming last-split was what kept TMM_ENV_OVERRIDE working. It was
// exactly backwards, and the test locked the defect in: last-split files that
// variable under a component "TMM_ENV_TMM" that does not exist, and because an
// unrecognised component is passed through by design rather than refused, it
// reaches the CR with no error anywhere.
func TestTheComponentSplitTakesTheFirstSeparator(t *testing.T) {
	for _, tc := range []struct {
		in, component, name string
	}{
		// The case the old comment named. Component is the real one.
		{"ROKSBNKCTL_BNK_ADV_TMM_ENV_TMM_ENV_OVERRIDE", "TMM", "TMM_ENV_OVERRIDE"},
		// Any variable whose own name contains the separator.
		{"ROKSBNKCTL_BNK_ADV_TMM_ENV_MY_ENV_VAR", "TMM", "MY_ENV_VAR"},
		// The ordinary case must be unaffected.
		{"ROKSBNKCTL_BNK_ADV_CNECONTROLLER_ENV_USE_GATEWAY_SETTINGS", "CNECONTROLLER", "USE_GATEWAY_SETTINGS"},
	} {
		c, n, ok := splitAdvancedEnvName(tc.in)
		if !ok {
			t.Fatalf("%s should parse", tc.in)
		}
		if c != tc.component || n != tc.name {
			t.Errorf("%s\n got component=%q name=%q\nwant component=%q name=%q",
				tc.in, c, n, tc.component, tc.name)
		}
	}
}

// Two spellings that canonicalise to the same component and variable must not
// silently merge. Before this, the loser was discarded by sort order and BOTH
// were reported as applied — a log line asserting a value that is not in the
// config.
func TestACaseCollisionKeepsOneValueAndSaysSo(t *testing.T) {
	t.Setenv("ROKSBNKCTL_BNK_ADV_TMM_ENV_FOO", "upper")
	t.Setenv("ROKSBNKCTL_BNK_ADV_Tmm_ENV_FOO", "mixed")

	ws := &Workspace{}
	applied := OverrideAdvancedEnvFromEnv(ws)

	if got := len(ws.BNK.Advanced["tmm"].Env); got != 1 {
		t.Fatalf("both spellings must land on one entry, got %d: %#v", got, ws.BNK.Advanced)
	}
	if len(applied) != 1 {
		t.Errorf("only the surviving value may be reported as applied, got %v", applied)
	}
	surviving := ws.BNK.Advanced["tmm"].Env["FOO"]
	if len(applied) == 1 && !strings.Contains(applied[0], "tmm") {
		t.Errorf("the applied line must name the component: %v", applied)
	}
	if surviving != "upper" && surviving != "mixed" {
		t.Errorf("unexpected surviving value %q", surviving)
	}
}

// A malformed name is ignored rather than creating a component called "" — a
// typo should cost nothing, not produce a config entry nobody can explain.
func TestMalformedNamesAreIgnored(t *testing.T) {
	for _, bad := range []string{
		"ROKSBNKCTL_BNK_ADV_",
		"ROKSBNKCTL_BNK_ADV_TMM",
		"ROKSBNKCTL_BNK_ADV__ENV_X",
		"ROKSBNKCTL_BNK_ADV_TMM_ENV_",
	} {
		if _, _, ok := splitAdvancedEnvName(bad); ok {
			t.Errorf("%q should not parse as an advanced-env override", bad)
		}
	}
}

// An unknown component passes through lowercased rather than being refused. The
// component set belongs to the product; this tool should not be the reason a
// newly shipped component is unreachable.
func TestAnUnknownComponentPassesThrough(t *testing.T) {
	t.Setenv("ROKSBNKCTL_BNK_ADV_SOMETHINGNEW_ENV_FLAG", "1")
	ws := &Workspace{}
	OverrideAdvancedEnvFromEnv(ws)
	if got := ws.BNK.Advanced["somethingnew"].Env["FLAG"]; got != "1" {
		t.Errorf("an unknown component should still reach the config, got %#v", ws.BNK.Advanced)
	}
}

// Unset contributes nothing to the reported surface, so this changes no existing
// report and the .env.example parity guard stays quiet for anyone not using it.
func TestUnsetContributesNothingToTheSurface(t *testing.T) {
	for _, n := range AdvancedEnvOverrideNames() {
		if strings.HasPrefix(n, advEnvPrefix) {
			// Only fails if the ambient environment happens to carry one, which
			// would itself be worth knowing about in a test run.
			t.Logf("ambient advanced-env override present: %s", n)
		}
	}
	ws := &Workspace{}
	if applied := OverrideAdvancedEnvFromEnv(ws); len(applied) != 0 {
		t.Errorf("no advanced env set, but %d override(s) applied", len(applied))
	}
	if ws.BNK.Advanced != nil {
		t.Error("an unset family must not allocate the map; the renderer keys on len() and the " +
			"additive guarantee depends on emitting nothing")
	}
}
