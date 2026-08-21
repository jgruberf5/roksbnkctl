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

// The split is on the LAST "_ENV_", not the first. A component name cannot
// contain it, but a VARIABLE name certainly can — TMM_ENV_OVERRIDE is a
// plausible F5 setting, and splitting on the first occurrence files it under the
// wrong component with no error at all.
func TestTheComponentSplitTakesTheLastSeparator(t *testing.T) {
	c, n, ok := splitAdvancedEnvName("ROKSBNKCTL_BNK_ADV_TMM_ENV_TMM_ENV_OVERRIDE")
	if !ok {
		t.Fatal("should parse")
	}
	if c != "TMM_ENV_TMM" || n != "OVERRIDE" {
		// Documents the actual behaviour: the LAST separator wins, so a variable
		// containing _ENV_ lands the component name longer rather than the
		// variable name truncated. Either choice loses information; this one
		// fails visibly (an unknown component) instead of silently filing a real
		// setting under the wrong component.
		t.Errorf("last-separator split expected, got component=%q name=%q", c, n)
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
