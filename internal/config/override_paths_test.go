package config

import (
	"sort"
	"strings"
	"testing"
)

// OverridePaths is derived by probing the override machinery rather than
// declared in a table, so the thing most worth testing is that the derivation
// still reaches the surface it is supposed to.
func TestOverridePathsMapsTheOverridesItShould(t *testing.T) {
	m := OverridePaths()

	if len(m) != len(SupportedOverrideNames()) {
		t.Fatalf("OverridePaths returned %d entries for %d supported overrides",
			len(m), len(SupportedOverrideNames()))
	}

	// Spot-check across the shapes — string, int, bool, nested block — because a
	// probe that only satisfied one parser would still look plausible.
	for name, want := range map[string]string{
		"ROKSBNKCTL_WORKER_FLAVOR":    "cluster.worker_flavor",
		"ROKSBNKCTL_TMM_REPLICAS":     "bnk.tmm_replicas",
		"ROKSBNKCTL_CNEINSTANCE_SIZE": "bnk.cneinstance_size",
		"ROKSBNKCTL_FLO_NAMESPACE":    "bnk.flo_namespace",
		"ROKSBNKCTL_MANIFEST_VERSION": "bnk.manifest_version",
	} {
		if got := m[name]; got != want {
			t.Errorf("%s -> %q; want %q", name, got, want)
		}
	}
}

// EVERY override must resolve to a config path. There are no exceptions left,
// and that is the point: an override that sets nothing a marshal can see is the
// inert-setting defect this codebase has shipped three times (#175, #186, #210).
//
// Two shapes used to sit here as "known unmappable", and both turned out to be
// the probe's fault rather than the code's:
//
//   - the per-zone family needs ALL six of a zone's fields before an entry is
//     built, so probing one variable at a time moved nothing. Multi-zone is the
//     only shape this tool deploys, so eighteen variables were being dropped
//     from every conversion.
//   - bnkforge.ca_b64 decodes its value and requires a parseable certificate.
//     The probe now generates a real one rather than the validation being
//     loosened -- a malformed CA silently accepted means the pin does not happen
//     and the session token travels unauthenticated.
//
// Anything appearing here now is a genuine finding, not a probe limitation.
func TestEveryOverrideResolvesToAConfigPath(t *testing.T) {
	var unmapped []string
	for name, path := range OverridePaths() {
		if path == "" {
			unmapped = append(unmapped, name)
		}
	}
	sort.Strings(unmapped)
	if len(unmapped) > 0 {
		t.Errorf("%d override(s) set nothing a marshal can see, which is how an inert "+
			"setting looks -- each needs checking against the field it claims to "+
			"write:\n  %s", len(unmapped), strings.Join(unmapped, "\n  "))
	}
}

// The per-zone family is index-sensitive: ZONE2_* must land on zones[1], not
// zones[0]. Filling one zone at a time makes it the only entry in the list, so
// every zone resolved to zones[0] and a three-zone config emitted zone 1's
// addresses three times -- wrong in a way a count of mapped variables cannot show.
func TestZoneOverridesMapToTheirOwnIndex(t *testing.T) {
	m := OverridePaths()
	for zone, want := range map[string]string{
		"ROKSBNKCTL_ZONE1_EXT_VLAN_CIDR": "bnk.network.zones[0].ext_vlan_cidr",
		"ROKSBNKCTL_ZONE2_EXT_VLAN_CIDR": "bnk.network.zones[1].ext_vlan_cidr",
		"ROKSBNKCTL_ZONE3_EXT_VLAN_CIDR": "bnk.network.zones[2].ext_vlan_cidr",
	} {
		if got := m[zone]; got != want {
			t.Errorf("%s -> %q; want %q", zone, got, want)
		}
	}
}

// OverrideFromMap must behave exactly as OverrideFromEnv, since it exists so the
// tables stay the one description of the surface. If the two could disagree, the
// map path would be a second implementation with its own bugs.
func TestOverrideFromMapMatchesOverrideFromEnv(t *testing.T) {
	const name, val = "ROKSBNKCTL_TMM_REPLICAS", "7"

	t.Setenv(name, val)
	var viaEnv Workspace
	appliedEnv := OverrideFromEnv(&viaEnv)

	var viaMap Workspace
	appliedMap := OverrideFromMap(&viaMap, map[string]string{name: val})

	if viaEnv.BNK.TMMReplicas != viaMap.BNK.TMMReplicas {
		t.Errorf("env path set %d, map path set %d", viaEnv.BNK.TMMReplicas, viaMap.BNK.TMMReplicas)
	}
	if strings.Join(appliedEnv, "|") != strings.Join(appliedMap, "|") {
		t.Errorf("applied reports differ:\n  env: %v\n  map: %v", appliedEnv, appliedMap)
	}
}

// The lookup swap must be restored, or one call to OverrideFromMap would leave
// every later override reading from a stale map instead of the environment.
func TestOverrideFromMapRestoresTheEnvironmentLookup(t *testing.T) {
	const name = "ROKSBNKCTL_TMM_REPLICAS"
	t.Setenv(name, "5")

	var ignored Workspace
	OverrideFromMap(&ignored, map[string]string{name: "7"})

	var after Workspace
	OverrideFromEnv(&after)
	if after.BNK.TMMReplicas != 5 {
		t.Errorf("after OverrideFromMap, the env path read %d; want 5 — the lookup was "+
			"not restored, so every later override reads the map", after.BNK.TMMReplicas)
	}
}
