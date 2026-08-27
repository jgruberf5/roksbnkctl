package config

import (
	"strings"
	"testing"
)

// #246. OverridePaths derives its mapping by probing, and the probe used to
// disambiguate a side effect from the real write by looking for the path whose
// VALUE equalled the probe. That works only while the probe value is
// distinctive, and #234 removed the guarantee: routing the resources block
// through DefaultResources() -- the correct fix for a real defect -- makes all
// eight resources.*.create paths appear, and four of them default to true, which
// IS the boolean probe. Five paths matched, disambiguation gave up, and all eight
// were recorded comma-joined.
//
// The damage was downstream and in three different directions:
//
//	config env --from-yaml   DROPPED resources.*.create -- a round-trip turned
//	                         "adopt the existing transit gateway" into "create one"
//	config-cheatsheet.html   printed "—", i.e. no override exists
//	env.example              claimed each variable wrote all eight toggles
//
// So this pins the single-path property directly, on the eight toggles that broke
// it. A comma in any of these means the derivation has gone ambiguous again.
func TestEveryResourceCreateOverrideMapsToExactlyOnePath(t *testing.T) {
	paths := OverridePaths()

	want := map[string]string{
		"ROKSBNKCTL_BNK_CREATE":               "resources.bnk.create",
		"ROKSBNKCTL_CERT_MANAGER_CREATE":      "resources.cert_manager.create",
		"ROKSBNKCTL_CLIENT_VPC_CREATE":        "resources.client_vpc.create",
		"ROKSBNKCTL_CLUSTER_JUMPHOSTS_CREATE": "resources.cluster_jumphosts.create",
		"ROKSBNKCTL_CLUSTER_VPC_CREATE":       "resources.cluster_vpc.create",
		"ROKSBNKCTL_REGISTRY_COS_CREATE":      "resources.registry_cos.create",
		"ROKSBNKCTL_TGW_JUMPHOST_CREATE":      "resources.tgw_jumphost.create",
		"ROKSBNKCTL_TRANSIT_GATEWAY_CREATE":   "resources.transit_gateway.create",
	}

	for name, wantPath := range want {
		got, ok := paths[name]
		if !ok {
			t.Errorf("%s is not in OverridePaths at all", name)
			continue
		}
		if strings.Contains(got, ",") {
			t.Errorf("%s maps to %d paths:\n  %s\nIt writes exactly one. A comma-joined value is "+
				"what makes `config env --from-yaml` drop the setting entirely, because the lookup "+
				"is by a single dotted path and never matches.",
				name, strings.Count(got, ",")+1, strings.ReplaceAll(got, ",", "\n  "))
			continue
		}
		if got != wantPath {
			t.Errorf("%s -> %q, want %q", name, got, wantPath)
		}
	}
}
