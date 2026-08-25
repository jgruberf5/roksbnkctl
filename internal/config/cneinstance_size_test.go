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

// The surface stays configurable ON PURPOSE, and the default carries the load.
//
// On ROKS, Tiny is the only deploymentSize that can run: everything above it
// requests hugepages (Small 4Gi, Medium 8Gi) that the platform has no supported
// way to allocate (#203). The tempting fix is to hardcode Tiny and drop the
// field. That would be wrong: if IBM ships a worker profile supporting
// hugepages, the platform's answer changes and nothing about this tool's surface
// should have to. So the field and its ROKSBNKCTL_* override stay, and Tiny is
// reached by DEFAULT rather than by removal of the choice.
//
// This pins both halves of that bargain: the field must stay settable, and
// leaving it unset must not silently pick a size ROKS cannot run.
func TestCNEInstanceSizeStaysConfigurableSoTinyIsADefaultNotAHardcode(t *testing.T) {
	// Half one: an explicit value still reaches the config untouched, so a
	// future hugepage-capable worker needs no change here.
	for _, size := range []string{"Small", "Medium", "Max"} {
		t.Setenv("ROKSBNKCTL_CNEINSTANCE_SIZE", size)
		ws := &Workspace{}
		OverrideFromEnv(ws)
		if ws.BNK.CNEInstanceSize != size {
			t.Errorf("explicit %q did not reach the config (got %q); the field must stay "+
				"settable, or a platform that gains hugepage support would need a code change",
				size, ws.BNK.CNEInstanceSize)
		}
	}

	// Half two: unset stays EMPTY here rather than being filled in with a size.
	// Empty is the tri-state the terraform side reads to apply the line default
	// (Small on 2.3, Tiny on 2.4). If this layer ever substituted a concrete
	// value, the 2.4 default would become unreachable and every workspace would
	// carry whatever this layer chose.
	// t.Setenv restores at test END, not between iterations, so the loop above
	// leaves its last value set. Clear it explicitly or this asserts against
	// whatever the loop happened to finish on.
	t.Setenv("ROKSBNKCTL_CNEINSTANCE_SIZE", "")
	ws := &Workspace{}
	OverrideFromEnv(ws)
	if ws.BNK.CNEInstanceSize != "" {
		t.Errorf("unset cneinstance_size became %q; it must stay empty so the terraform "+
			"line default (Tiny on 2.4) applies", ws.BNK.CNEInstanceSize)
	}
}
