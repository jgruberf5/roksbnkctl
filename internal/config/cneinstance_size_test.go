package config

import (
	"strings"
	"testing"
)

// #175 item 1. bnk.cneinstance_size was reachable from YAML and from nowhere
// else. `init --non-interactive` builds config.yaml from the environment alone,
// and every BNK Forge module configures the tool that way, so a field with no
// override cannot be used by a blueprint at all.
func TestCNEInstanceSizeHasAnOverride(t *testing.T) {
	found := false
	for _, n := range SupportedOverrideNames() {
		if n == "ROKSBNKCTL_CNEINSTANCE_SIZE" {
			found = true
			break
		}
	}
	if !found {
		t.Fatal("bnk.cneinstance_size has no environment override, so it cannot reach a blueprint")
	}

	t.Setenv("ROKSBNKCTL_CNEINSTANCE_SIZE", "Tiny")
	ws := &Workspace{}
	applied := OverrideFromEnv(ws)
	if ws.BNK.CNEInstanceSize != "Tiny" {
		t.Errorf("the override did not reach the config, got %q", ws.BNK.CNEInstanceSize)
	}
	if !strings.Contains(strings.Join(applied, " "), "bnk.cneinstance_size") {
		t.Error("the applied-overrides report should name the field it set")
	}
}

// Deliberately unvalidated. The legal set of sizes is a property of the BNK
// manifest, not of this tool: hardcoding a list would go stale the first time F5
// adds one, and would then refuse a size the product supports. The operator on
// the cluster is the right place to reject a bad value.
func TestCNEInstanceSizeIsNotValidatedAgainstAFixedList(t *testing.T) {
	t.Setenv("ROKSBNKCTL_CNEINSTANCE_SIZE", "SomeSizeF5AddsLater")
	ws := &Workspace{}
	OverrideFromEnv(ws)
	if ws.BNK.CNEInstanceSize != "SomeSizeF5AddsLater" {
		t.Error("an unrecognised size must pass through; the legal set belongs to the manifest, " +
			"and a hardcoded list would refuse sizes the product supports")
	}
}
