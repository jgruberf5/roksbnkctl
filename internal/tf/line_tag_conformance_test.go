package tf

import (
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
)

// WHAT THIS FILE PINS, AND WHY IT RUNS TERRAFORM TO DO IT.
//
// book/src/28-configuration-reference.md has a `line` column saying which BNK
// release line each config field applies to. It comes from a `line:"2.3"` /
// `line:"2.4"` struct tag in internal/config/workspace.go, and an absent tag
// means both. For 189 fields that column said "2.3 + 2.4" because nothing had
// been asserted, not because anyone had checked (#182).
//
// The answer is not derivable from the struct. It lives in terraform, and — this
// is the part that is easy to get backwards — it lives on the RENDERED OBJECT,
// not on the field. `cloud-network-mapping` and the F5SPKVlan CRs are 2.3-only,
// which makes it look as though every zone CIDR that feeds them is 2.3-only.
// Three of them are not: terraform/modules/gateway/infra_24.tf builds the 2.4
// Infra CR's IPAM pools out of ext_vlan_cidr, int_vip_cidr and int_snat_cidr.
// Following "this resource is 2.3-only, therefore its inputs are" would have
// hidden the addressing a 2.4 operator most needs to get right.
//
// So the test asks terraform the question directly, per line:
//
//	does perturbing this variable change the set of objects the apply PERSISTS?
//
// The surface is assembled from each resource's OWN `count` / `for_each`
// expression, lifted out of the HCL and evaluated by `terraform console`. A gate
// that is commented out, inverted, or rewritten changes what terraform returns
// here, because nothing in this file reads the gate as text — it reads the value
// terraform computes from it.
//
// DRY RUNS ARE NOT PART OF THE SURFACE. modules/cne_instance uses the external
// F5SPKVlan body as the payload of a server-side dry-run that waits for the
// f5validate webhook to serve TLS. That interpolates the self-IPs on 2.4 and
// persists nothing, so it is a readiness probe, not configuration, and it is
// deliberately excluded. Including it would report the self-IPs as "both" and
// promise a 2.4 operator a setting that changes nothing on their cluster.

// ── the evidence table ──────────────────────────────────────────────────────

// lineProbe is one config field, the terraform variable internal/tf/vars.go
// renders it to, and the perturbation that proves which lines consume it.
type lineProbe struct {
	Struct string // Go struct in internal/config/workspace.go
	Key    string // its yaml key
	Want   string // "2.3", "2.4" or "both" — must match the struct tag
	Var    string // the terraform variable the renderer emits
	Values []string
	// With are variables held at the SAME value in the baseline render and the
	// perturbed one, for a setting that only has an effect when a companion
	// feature is on (advanced.externalBigip.env is emitted only when
	// externalBigip is enabled).
	With map[string]string
	// Modules whose persisted surface to check. Influence is the OR across them:
	// a variable read by two modules is consumed on a line if EITHER reads it
	// there. cneinstance_network_zones is the case that matters — 2.4 reads it
	// only in the gateway module.
	Modules []string
	Why     string
}

// The 2.4 set. Every one of these lands in a `!local.line_pre_24` block in
// terraform/modules/cne_instance/modules/cneinstance/main.tf — the conformance
// merge, the placement block, the externalBigip env, the rolling-update policy
// or the 2.4-only advanced env.
//
// They were ALSO, until the commit that added this file, inert on both lines:
// the root module declared each one, the Go renderer wrote it into
// terraform.tfvars, and terraform/main.tf never passed it into
// module.cne_instance. The wrapper declares identically-named variables with the
// same defaults, so `var.cneinstance_tmm_replicas` inside it resolved to the
// default and the operator's value went nowhere.
//
// This test does NOT see that: it evaluates the leaf module directly, where the
// variable is read, and a broken hand-off two levels up is invisible from there.
// TestEveryRootVariableReachesTheModuleThatReadsIt (root package) is the guard
// for the hand-off. The two answer different questions and both are needed —
// "which line reads this" and "does the operator's value get there at all".
var lineProbes = []lineProbe{
	{
		Struct: "BNKCfg", Key: "tmm_replicas", Want: "2.4",
		Var: "cneinstance_tmm_replicas", Values: []string{"7"},
		Modules: []string{"cneinstance"},
		Why:     "spec.tmmReplicas, inside cneinstance_conformance_24",
	},
	{
		Struct: "BNKCfg", Key: "watch_namespaces", Want: "2.4",
		Var: "cneinstance_watch_namespaces", Values: []string{`["probe-ns"]`},
		Modules: []string{"cneinstance"},
		Why:     "spec.watchNamespaces, inside cneinstance_conformance_24",
	},
	{
		Struct: "BNKCfg", Key: "tmm_anti_affinity", Want: "2.4",
		Var: "cneinstance_tmm_anti_affinity", Values: []string{"false"},
		Modules: []string{"cneinstance"},
		Why:     "spec.placement.dataPlane.affinity, inside cneinstance_placement_24",
	},
	{
		Struct: "BNKCfg", Key: "tmm_anti_affinity_topology_key", Want: "2.4",
		Var: "cneinstance_tmm_anti_affinity_topology_key", Values: []string{`"probe/anti"`},
		Modules: []string{"cneinstance"},
		Why:     "topologyKey of the anti-affinity term, inside cneinstance_placement_24",
	},
	{
		Struct: "BNKCfg", Key: "tmm_zone_spread", Want: "2.4",
		Var: "cneinstance_tmm_zone_spread", Values: []string{"false"},
		Modules: []string{"cneinstance"},
		Why:     "spec.placement.dataPlane.topologySpreadConstraints, inside cneinstance_placement_24",
	},
	{
		Struct: "BNKCfg", Key: "tmm_zone_topology_key", Want: "2.4",
		Var: "cneinstance_tmm_zone_topology_key", Values: []string{`"probe/zone"`},
		Modules: []string{"cneinstance"},
		Why:     "topologyKey of the zone-spread term, inside cneinstance_placement_24",
	},
	{
		Struct: "BNKCfg", Key: "tmm_zone_max_skew", Want: "2.4",
		Var: "cneinstance_tmm_zone_max_skew", Values: []string{"9"},
		Modules: []string{"cneinstance"},
		Why:     "maxSkew of the zone-spread term, inside cneinstance_placement_24",
	},
	{
		Struct: "BNKCfg", Key: "tmm_zone_when_unsatisfiable", Want: "2.4",
		Var: "cneinstance_tmm_zone_when_unsatisfiable", Values: []string{`"ScheduleAnyway"`},
		Modules: []string{"cneinstance"},
		Why:     "whenUnsatisfiable of the zone-spread term, inside cneinstance_placement_24",
	},
	{
		Struct: "BNKCfg", Key: "tmm_pod_label", Want: "2.4",
		Var: "cneinstance_tmm_pod_label", Values: []string{`"probe-tmm"`},
		Modules: []string{"cneinstance"},
		Why:     "the labelSelector both placement terms match on, inside cneinstance_placement_24",
	},
	{
		Struct: "BNKCfg", Key: "tmm_rolling_update", Want: "2.4",
		Var: "cneinstance_tmm_rolling_update", Values: []string{"false"},
		Modules: []string{"cneinstance"},
		Why:     "advanced.tmm.rollingUpdate, inside adv_tmm_rolling_24",
	},
	{
		Struct: "BNKCfg", Key: "external_bigip", Want: "2.4",
		Var: "cneinstance_external_bigip", Values: []string{"true"},
		Modules: []string{"cneinstance"},
		Why:     "spec.externalBigip.enabled, inside cneinstance_conformance_24",
	},
	{
		Struct: "BNKCfg", Key: "external_bigip_login_secret", Want: "2.4",
		Var: "cneinstance_external_bigip_login_secret", Values: []string{`"probe-secret"`},
		With:    map[string]string{"cneinstance_external_bigip": "true"},
		Modules: []string{"cneinstance"},
		Why:     "EXTERNAL_BIGIP_LOGIN_SECRET in adv_external_bigip_24, which needs the feature on",
	},
	{
		Struct: "BNKCfg", Key: "cluster_identifier", Want: "2.4",
		Var: "cneinstance_cluster_identifier", Values: []string{`"probe-cluster"`},
		With:    map[string]string{"cneinstance_external_bigip": "true"},
		Modules: []string{"cneinstance"},
		Why:     "CLUSTER_IDENTIFIER in adv_external_bigip_24, which needs the feature on",
	},
	{
		Struct: "BNKCfg", Key: "gateway_api_version", Want: "2.4",
		Var: "cneinstance_gateway_api_version", Values: []string{`"9.9.9"`},
		Modules: []string{"cneinstance"},
		Why:     "GATEWAY_API_VERSION in adv_env_line, which is empty on 2.3",
	},

	// ── the 2.3 set ───────────────────────────────────────────────────────────
	{
		Struct: "BNKNetworkCfg", Key: "vlan_prefixlen", Want: "2.3",
		Var: "cneinstance_vlan_prefixlen", Values: []string{"29"},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "spec.prefixlen_v4 on the F5SPKVlan pair, both count-gated to line_pre_24",
	},
	{
		Struct: "BNKNetworkCfg", Key: "vlan_prefixlen_external", Want: "2.3",
		Var: "cneinstance_vlan_prefixlen_external", Values: []string{"28"},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "overrides prefixlen_v4 on the external F5SPKVlan only",
	},
	{
		Struct: "BNKNetworkCfg", Key: "vlan_prefixlen_internal", Want: "2.3",
		Var: "cneinstance_vlan_prefixlen_internal", Values: []string{"27"},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "overrides prefixlen_v4 on the internal F5SPKVlan only",
	},
	{
		Struct: "BNKZoneCfg", Key: "int_vlan_cidr", Want: "2.3",
		Var: "cneinstance_network_zones", Values: []string{zonesWith("int_vlan_cidr", `"10.202.0.0/24"`)},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "an int-vlan subnet in cloud-network-mapping; Infra has no internal-VLAN pool",
	},
	{
		Struct: "BNKZoneCfg", Key: "external_selfip", Want: "2.3",
		Var: "cneinstance_network_zones", Values: []string{zonesWith("external_selfip", `"10.101.0.20"`)},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "selfip_v4s on the external F5SPKVlan; 2.4 allocates from external-vlan-ipam",
	},
	{
		Struct: "BNKZoneCfg", Key: "internal_selfip", Want: "2.3",
		Var: "cneinstance_network_zones", Values: []string{zonesWith("internal_selfip", `"10.102.0.20"`)},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "selfip_v4s on the internal F5SPKVlan; 2.4 has no internal VLAN to address",
	},

	// ── the "both" set ────────────────────────────────────────────────────────
	//
	// These carry no tag, so the reference reports them as 2.3 + 2.4. That is the
	// unset DEFAULT for every other field, which is exactly the complaint in #182
	// — so for the fields where a plausible reading says "2.3-only", the claim is
	// pinned here rather than left to the default. Each one below would have been
	// mis-tagged by reasoning from the 2.3-only resource it feeds.
	{
		Struct: "BNKZoneCfg", Key: "ext_vlan_cidr", Want: "both",
		Var: "cneinstance_network_zones", Values: []string{zonesWith("ext_vlan_cidr", `"10.201.0.0/24"`)},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "2.3: cloud-network-mapping + F5SPKStaticRoute gateway. 2.4: Infra external-vlan-ipam + staticRoutes nextHop",
	},
	{
		Struct: "BNKZoneCfg", Key: "int_snat_cidr", Want: "both",
		Var: "cneinstance_network_zones", Values: []string{zonesWith("int_snat_cidr", `"10.203.0.0/24"`)},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "2.3: cloud-network-mapping + F5SPKSnatpool addressList. 2.4: Infra egress-snat-ipam",
	},
	{
		Struct: "BNKZoneCfg", Key: "int_vip_cidr", Want: "both",
		Var: "cneinstance_network_zones", Values: []string{zonesWith("int_vip_cidr", `"10.204.0.0/24"`)},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "2.3: cloud-network-mapping + F5BnkGateway listener networks. 2.4: Infra vip-listener-ipam",
	},
	{
		Struct: "GatewayCfg", Key: "egress_mode", Want: "both",
		Var: "gateway_egress_mode", Values: []string{`"automap"`},
		Modules: []string{"gateway"},
		Why:     "2.3: which F5SPKSnatpool/F5SPKEgress CRs exist. 2.4: GatewaySettings sourceNATPools + egressConfigs",
	},
	{
		Struct: "GatewayCfg", Key: "client_subnet_local", Want: "both",
		Var: "gateway_client_subnet_local", Values: []string{`["10.190.0.0/24"]`},
		Modules: []string{"gateway"},
		Why:     "2.3: one F5SPKStaticRoute per subnet per zone. 2.4: Infra.spec.staticRoutes destinations",
	},
	{
		Struct: "GatewayCfg", Key: "client_subnet_remote", Want: "both",
		Var: "gateway_client_subnet_remote", Values: []string{`["10.191.0.0/24"]`},
		Modules: []string{"gateway"},
		Why:     "as client_subnet_local",
	},
	{
		Struct: "BNKCfg", Key: "cneinstance_size", Want: "both",
		Var: "cneinstance_deployment_size", Values: []string{`"Medium"`},
		Modules: []string{"cneinstance"},
		Why:     "spec.deploymentSize; only the DEFAULT is line-selected (Small on 2.3, Tiny on 2.4)",
	},
	{
		Struct: "BNKNetworkCfg", Key: "tmm_k8s_routes", Want: "both",
		Var: "cneinstance_tmm_k8s_routes", Values: []string{`"10.199.0.0/18"`},
		Modules: []string{"cneinstance"},
		Why:     "advanced.tmm.env TMM_K8S_ROUTES, in the shared defaults rather than adv_env_line",
	},
	{
		// Two values because the DEFAULT is line-dependent: true on 2.3, false on
		// 2.4. A single probe value matches one line's default and would report
		// that line as not consuming the field at all.
		Struct: "BNKCfg", Key: "demo_mode", Want: "both",
		Var: "cneinstance_demo_mode", Values: []string{`"true"`, `"false"`},
		Modules: []string{"cneinstance"},
		Why:     "advanced.demoMode.enabled; an explicit setting wins on either line",
	},
	{
		Struct: "BNKCfg", Key: "whole_cluster", Want: "both",
		Var: "cneinstance_whole_cluster_override", Values: []string{`"true"`, `"false"`},
		Modules: []string{"cneinstance"},
		Why:     "spec.wholeCluster; an explicit setting wins on either line",
	},
	{
		Struct: "BNKCfg", Key: "flo_namespace", Want: "both",
		Var: "flo_namespace", Values: []string{`"probe-flo"`},
		Modules: []string{"cneinstance", "gateway"},
		Why:     "every CR's namespace, the controller name the GatewayClass matches, and the 2.4 Gateway's own namespace",
	},
	{
		Struct: "BNKCfg", Key: "gslb_datacenter_name", Want: "both",
		Var: "cneinstance_gslb_datacenter_name", Values: []string{`"probe-dc"`},
		Modules: []string{"cneinstance"},
		Why:     "GSLB_DATACENTER_NAME in the shared cneController env, not adv_env_line",
	},
	{
		Struct: "BNKCfg", Key: "manifest_version", Want: "both",
		Var: "f5_bigip_k8s_manifest_version", Values: []string{`"9.9.9-probe"`},
		Modules: []string{"cneinstance"},
		Why:     "spec.manifestVersion. It also SELECTS the line, which is not the same as being line-specific",
	},
	{
		Struct: "BNKCfg", Key: "advanced", Want: "both",
		Var: "cneinstance_advanced_env", Values: []string{`{ tmm = { PROBE_KEY = "probe" } }`},
		Modules: []string{"cneinstance"},
		Why:     "advanced.<component>.env overrides, merged over the defaults on either line",
	},
}

// zonesWith renders a one-zone cneinstance_network_zones list with a single
// field perturbed, so each member of BNKZoneCfg can be probed on its own. The
// CIDRs stay routable-looking because cidrhost() is applied to three of them.
func zonesWith(field, value string) string {
	base := [][2]string{
		{"ext_vlan_cidr", `"10.101.0.0/24"`},
		{"int_vlan_cidr", `"10.102.0.0/24"`},
		{"int_snat_cidr", `"10.103.0.0/24"`},
		{"int_vip_cidr", `"10.104.0.0/24"`},
		{"external_selfip", `"10.101.0.10"`},
		{"internal_selfip", `"10.102.0.10"`},
	}
	var parts []string
	found := false
	for _, kv := range base {
		v := kv[1]
		if kv[0] == field {
			v = value
			found = true
		}
		parts = append(parts, kv[0]+" = "+v)
	}
	if !found {
		panic("zonesWith: unknown zone field " + field)
	}
	return "[{ " + strings.Join(parts, ", ") + " }]"
}

// ── the surfaces ────────────────────────────────────────────────────────────

// surfaceEntry is one object an apply persists: the terraform expression for its
// body, and the resource whose count/for_each decides whether it exists at all.
// The gate is NOT written here — it is read out of the module's HCL, so this
// table cannot drift from the resource it claims to describe.
type surfaceEntry struct {
	Resource string // `kubectl_manifest.<name>`, or "" for an always-present body
	Body     string
}

type probeModule struct {
	Path    []string // under terraform/modules
	Vars    map[string]string
	Entries []surfaceEntry
}

var probeModules = map[string]probeModule{
	"cneinstance": {
		Path: []string{"cne_instance", "modules", "cneinstance"},
		Vars: map[string]string{
			"enabled":                  "true",
			"flo_namespace":            `"f5-bnk"`,
			"cneinstance_cloud_region": `"us-south"`,
		},
		Entries: []surfaceEntry{
			{Resource: "kubectl_manifest.cneinstance", Body: "local.cneinstance_manifest"},
			{Resource: "kubectl_manifest.cneinstance_scc_policies", Body: "local.scc_policy_assignments"},
			{Resource: "kubectl_manifest.cloud_network_mapping", Body: "local.cloud_network_mapping_manifest"},
			{Resource: "kubectl_manifest.external_vlan", Body: "local.external_vlan_manifest"},
			{Resource: "kubectl_manifest.internal_vlan", Body: "local.internal_vlan_manifest"},
		},
	},
	"gateway": {
		Path: []string{"gateway"},
		Vars: map[string]string{
			"deploy_gateway":          "true",
			"create_roks_cluster":     "false",
			"flo_namespace":           `"f5-bnk"`,
			"ibmcloud_cluster_region": `"us-south"`,
			"roks_cluster_name_or_id": `"probe-cluster"`,
		},
		Entries: []surfaceEntry{
			{Resource: "kubectl_manifest.gateway_class", Body: "local.gateway_controller_name"},
			{Resource: "kubectl_manifest.gateway", Body: "[local.gateway_listeners, local.gateway_ns_effective, local.gateway_parameters_ref]"},
			{Resource: "kubectl_manifest.bnk_gateway", Body: "local.default_listener_networks"},
			{Resource: "kubectl_manifest.snatpool", Body: "local.snat_address_list"},
			{Resource: "kubectl_manifest.egress_automap", Body: "local.egress_pseudo_cni"},
			{Resource: "kubectl_manifest.static_route", Body: "local.static_routes"},
			{Resource: "kubectl_manifest.infra_24", Body: "local.infra_manifest_24"},
			{Resource: "kubectl_manifest.gateway_settings_24", Body: "local.gateway_settings_manifest_24"},
		},
	},
}

// baseProbeVars are set for every module. cneinstance_network_zones has to carry
// a real zone or the 2.3 objects and the 2.4 Infra pools are both empty lists and
// every zone probe compares nothing against nothing.
var baseProbeVars = map[string]string{
	"far_repo_url":                 `"https://repo.f5.com"`,
	"cneinstance_network_zones":    zonesWith("ext_vlan_cidr", `"10.101.0.0/24"`),
	"gateway_client_subnet_local":  `["10.150.0.0/24"]`,
	"gateway_client_subnet_remote": `["10.151.0.0/24"]`,
}

// ── the test ────────────────────────────────────────────────────────────────

func TestConfigLineTagsMatchWhatTerraformRenders(t *testing.T) {
	tags := workspaceLineTags(t)

	for _, p := range lineProbes {
		if p.Want != "2.3" && p.Want != "2.4" && p.Want != "both" {
			t.Fatalf("%s.%s: probe Want must be 2.3, 2.4 or both; got %q", p.Struct, p.Key, p.Want)
		}
		// A probe with no values or no modules asks terraform nothing and reports
		// "consumed nowhere", which reads as a real finding for a 2.3/2.4 field and
		// silently agrees with itself.
		if len(p.Values) == 0 || len(p.Modules) == 0 {
			t.Fatalf("%s.%s: a probe needs at least one value and one module, or it checks nothing", p.Struct, p.Key)
		}
		for _, m := range p.Modules {
			if _, ok := probeModules[m]; !ok {
				t.Fatalf("%s.%s: unknown probe module %q", p.Struct, p.Key, m)
			}
		}
		got, ok := tags[p.Struct+"."+p.Key]
		if !ok {
			t.Errorf("probe names %s.%s, which is not a yaml field of that struct in internal/config/workspace.go", p.Struct, p.Key)
			continue
		}
		if got != p.Want {
			t.Errorf("%s.%s carries line tag %q but the evidence table says %q (%s)", p.Struct, p.Key, got, p.Want, p.Why)
		}
	}

	// Nothing below runs without terraform, so the tag/table agreement above is
	// still checked on a machine that cannot evaluate the modules.
	if _, err := exec.LookPath("terraform"); err != nil {
		t.Skip("terraform not on PATH; tag/evidence-table agreement checked, terraform evaluation skipped")
	}

	ev := newLineEvaluator(t)
	for _, p := range lineProbes {
		t.Run(p.Struct+"."+p.Key, func(t *testing.T) {
			t.Parallel()
			on23 := ev.consumes(t, p, "2.3")
			on24 := ev.consumes(t, p, "2.4")

			want23 := p.Want == "2.3" || p.Want == "both"
			want24 := p.Want == "2.4" || p.Want == "both"

			if on23 == want23 && on24 == want24 {
				return
			}
			t.Errorf("%s.%s (terraform variable %s) is consumed on 2.3=%v 2.4=%v, "+
				"but the reference claims %q.\n"+
				"  evidence on record: %s\n"+
				"  \"consumed\" means: perturbing the variable changes an object the apply PERSISTS on that line.\n"+
				"  If terraform is right and the tag is wrong, a field claimed for a line it does not reach is the\n"+
				"  worse direction — it hides configuration from the operator who needs it. Fix the tag, regenerate\n"+
				"  the reference (go run ./tools/refgen/config-md > book/src/28-configuration-reference.md), and\n"+
				"  update this table with the consumer you found.",
				p.Struct, p.Key, p.Var, on23, on24, p.Want, p.Why)
		})
	}
}

// TestEveryLineTagHasEvidence is the ratchet. A `line:` tag is a claim that a
// setting does nothing on the other release line, and a reader has no way to
// check it. Adding one now means adding the terraform perturbation that shows it.
func TestEveryLineTagHasEvidence(t *testing.T) {
	probed := map[string]bool{}
	for _, p := range lineProbes {
		probed[p.Struct+"."+p.Key] = true
	}

	// gateway_api_mtls is the one line-gated setting that never reaches
	// terraform: it selects whether roksbnkctl runs the gateway-api
	// admission-policy sweep during the apply, and that decision is made in Go.
	// internal/orchestration's TestTheSweepAlwaysRunsOnTwoThree and
	// TestTwoFourSweepsOnlyForMTLS exercise it from both sides: on 2.3 the sweep
	// runs whatever the field says, on 2.4 the field is what decides. Anything
	// else added here needs the same: a named test that runs the behaviour.
	// A Go-side gate needs BOTH the test that exercises it and the line the tag is
	// expected to claim. Recording only the test left the tag itself unverified:
	// flipping gateway_api_bundle_url to "2.3" still passed, because the entry
	// exempted the field from every check rather than from the terraform probe
	// alone. The reference would then have published the wrong line.
	goSideEvidence := map[string]struct{ line, test string }{
		"BNKCfg.gateway_api_mtls": {"2.4",
			"internal/orchestration: TestTheSweepAlwaysRunsOnTwoThree + TestTwoFourSweepsOnlyForMTLS"},
		// Consumed only when GatewayAPIBundleNeeded() is true — the 2.4 line AND
		// gateway_api_mtls. That gate is in Go, not a terraform count, so no
		// lineProbe can reach it: the tfvar it renders is validation-only.
		"BNKCfg.gateway_api_bundle_url": {"2.4",
			"internal/config: TestGatewayAPIBundleIsNeededOnlyOnTwoFourWithMTLS"},
	}

	var untested []string
	for name, line := range workspaceLineTags(t) {
		if line == "both" {
			continue
		}
		if ev, ok := goSideEvidence[name]; ok {
			if ev.line != line {
				t.Errorf("%s is tagged %q but its Go-side evidence claims %q (%s). "+
					"The reference publishes the tag, so the two must agree.", name, line, ev.line, ev.test)
			}
			continue
		}
		if probed[name] {
			continue
		}
		untested = append(untested, name+" -> "+line)
	}
	sort.Strings(untested)
	if len(untested) > 0 {
		t.Errorf("these fields claim a release line with nothing backing it:\n  %s\n"+
			"Add a lineProbe naming the terraform variable and a value that changes the rendered surface on that "+
			"line and not the other. If the gate is in Go rather than terraform, add the field to goSideEvidence "+
			"with the test that exercises it.", strings.Join(untested, "\n  "))
	}
}

// TestOnlyKnownLineTagValuesAreUsed keeps the reference's `line` column to values
// it can render. A typo ("2,4", "24", "v2.4") is not rejected anywhere else: it
// is not "both", so the column prints it verbatim and the chapter ships a line
// that does not exist.
func TestOnlyKnownLineTagValuesAreUsed(t *testing.T) {
	for name, line := range workspaceLineTags(t) {
		switch line {
		case "both", "2.3", "2.4":
		default:
			t.Errorf("%s has line:%q; only 2.3 and 2.4 are release lines (absent means both)", name, line)
		}
	}
}

// ── reading the struct ──────────────────────────────────────────────────────

// workspaceLineTags maps "<Struct>.<yamlKey>" to its line tag, using "both" for
// an absent tag. Parsed from the AST rather than reflected over, for the same
// reason tools/refgen/config-md parses it: the tag lives on the source field.
func workspaceLineTags(t *testing.T) map[string]string {
	t.Helper()
	const src = "../config/workspace.go"
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, src, nil, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", src, err)
	}
	out := map[string]string{}
	for _, decl := range f.Decls {
		gd, ok := decl.(*ast.GenDecl)
		if !ok || gd.Tok != token.TYPE {
			continue
		}
		for _, spec := range gd.Specs {
			ts, ok := spec.(*ast.TypeSpec)
			if !ok {
				continue
			}
			st, ok := ts.Type.(*ast.StructType)
			if !ok {
				continue
			}
			for _, fl := range st.Fields.List {
				if fl.Tag == nil {
					continue
				}
				tag := reflect.StructTag(strings.Trim(fl.Tag.Value, "`"))
				y := tag.Get("yaml")
				key := strings.Split(y, ",")[0]
				if key == "" || key == "-" {
					continue
				}
				line := tag.Get("line")
				if line == "" {
					line = "both"
				}
				out[ts.Name.Name+"."+key] = line
			}
		}
	}
	if len(out) < 100 {
		t.Fatalf("only %d yaml fields parsed out of workspace.go; the AST walk has stopped matching", len(out))
	}
	return out
}

// ── the terraform side ──────────────────────────────────────────────────────

type lineEvaluator struct {
	dirs  map[string]string // module key -> initialised copy
	mu    sync.Mutex
	cache map[string]string // memoised console results
}

func newLineEvaluator(t *testing.T) *lineEvaluator {
	t.Helper()
	e := &lineEvaluator{dirs: map[string]string{}, cache: map[string]string{}}
	for name, m := range probeModules {
		e.dirs[name] = initProbeModule(t, m.Path)
	}
	return e
}

// consumes reports whether ANY of the probe's values changes the set of objects
// the given line persists, across every module the probe names.
func (e *lineEvaluator) consumes(t *testing.T, p lineProbe, line string) bool {
	t.Helper()
	consumed := false
	for _, mod := range p.Modules {
		base := e.surface(t, mod, line, p.With, nil)
		for _, v := range p.Values {
			got := e.surface(t, mod, line, p.With, map[string]string{p.Var: v})
			if got != base {
				consumed = true
			}
		}
	}
	return consumed
}

// surface renders the module's persisted objects on a line, as a single string.
// Each entry contributes its body when its resource's own count/for_each is
// live, and a fixed "<off>" marker when it is not — so both what an object SAYS
// and whether it EXISTS are part of the comparison.
func (e *lineEvaluator) surface(t *testing.T, mod, line string, with, extra map[string]string) string {
	t.Helper()
	m := probeModules[mod]

	vars := map[string]string{}
	for k, v := range baseProbeVars {
		vars[k] = v
	}
	for k, v := range m.Vars {
		vars[k] = v
	}
	for k, v := range with {
		vars[k] = v
	}
	for k, v := range extra {
		vars[k] = v
	}
	vars["bnk_line"] = `"` + line + `"`

	var parts []string
	for _, ent := range m.Entries {
		gate := e.gate(t, mod, ent.Resource)
		parts = append(parts, "("+gate+") ? jsonencode("+ent.Body+") : \"<off>\"")
	}
	list := "[" + strings.Join(parts, ", ") + "]"
	// Some of these bodies carry a variable declared `sensitive`, which makes the
	// whole encoding sensitive and unprintable. nonsensitive() errors when the
	// value is NOT sensitive, so it cannot be applied unconditionally.
	expr := fmt.Sprintf("try(nonsensitive(jsonencode(%s)), jsonencode(%s))", list, list)

	return e.console(t, mod, vars, expr)
}

// gate returns the resource's count (or for_each) expression as a BOOLEAN
// terraform expression, lifted from the module's own HCL. Reading it here rather
// than restating it is what keeps this test honest: invert a `? 1 : 0`, comment a
// gate out, or swap count for a hard-coded 1 and the surface this test compares
// changes with it.
func (e *lineEvaluator) gate(t *testing.T, mod, resource string) string {
	t.Helper()
	if resource == "" {
		return "true"
	}
	kind, name, ok := strings.Cut(resource, ".")
	if !ok {
		t.Fatalf("resource %q is not <type>.<name>", resource)
	}
	src := e.moduleHCL(t, mod)

	// Anchor on the resource's own opening line, then take the first count /
	// for_each meta-argument inside its block.
	head := regexp.MustCompile(`(?m)^resource\s+"` + regexp.QuoteMeta(kind) + `"\s+"` + regexp.QuoteMeta(name) + `"\s*\{`)
	loc := head.FindStringIndex(src)
	if loc == nil {
		t.Fatalf("resource %q not found in module %s; the surface table names a resource that no longer exists", resource, mod)
	}
	body := src[loc[1]:]
	if end := strings.Index(body, "\n}\n"); end >= 0 {
		body = body[:end]
	}
	meta := regexp.MustCompile(`(?m)^\s*(count|for_each)\s*=\s*`).FindStringSubmatchIndex(body)
	if meta == nil {
		t.Fatalf("resource %q in module %s has no count or for_each meta-argument. "+
			"If its gate moved, this test cannot see it any more — update the extraction rather than "+
			"hard-coding the condition here.", resource, mod)
	}
	kw := body[meta[2]:meta[3]]
	expr := hclExpression(body[meta[1]:])
	if expr == "" {
		t.Fatalf("could not read the %s expression of %q in module %s", kw, resource, mod)
	}
	if kw == "for_each" {
		return "length(" + expr + ") > 0"
	}
	return "(" + expr + ") > 0"
}

// hclExpression takes the text immediately after a `<name> = ` and returns the
// expression, which may run over several lines: `for_each = local.enabled ? {
// for x in ... } : {}` is one expression and four lines. It ends at the first
// newline reached with every bracket closed. Quotes and comments are tracked so a
// brace inside either does not move the depth.
func hclExpression(s string) string {
	var out strings.Builder
	depth := 0
	inStr := false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case inStr:
			out.WriteByte(c)
			if c == '\\' && i+1 < len(s) {
				i++
				out.WriteByte(s[i])
			} else if c == '"' {
				inStr = false
			}
		case c == '"':
			inStr = true
			out.WriteByte(c)
		case c == '#' || (c == '/' && i+1 < len(s) && s[i+1] == '/'):
			// A comment inside the expression: drop it, keeping the newline's
			// separating effect. `terraform console` reads one line at a time, so
			// the result has to survive being collapsed onto one.
			for i < len(s) && s[i] != '\n' {
				i++
			}
			if depth == 0 {
				return strings.TrimSpace(out.String())
			}
			out.WriteByte(' ')
		case c == '{' || c == '[' || c == '(':
			depth++
			out.WriteByte(c)
		case c == '}' || c == ']' || c == ')':
			depth--
			out.WriteByte(c)
		case c == '\n':
			if depth == 0 {
				return strings.TrimSpace(out.String())
			}
			out.WriteByte(' ')
		default:
			out.WriteByte(c)
		}
	}
	return ""
}

var moduleHCLCache sync.Map // module key -> concatenated .tf source

func (e *lineEvaluator) moduleHCL(t *testing.T, mod string) string {
	t.Helper()
	if v, ok := moduleHCLCache.Load(mod); ok {
		return v.(string)
	}
	dir := e.dirs[mod]
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read module %s: %v", mod, err)
	}
	var b strings.Builder
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".tf") {
			continue
		}
		src, err := os.ReadFile(filepath.Join(dir, ent.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", ent.Name(), err)
		}
		b.Write(src)
		b.WriteString("\n")
	}
	moduleHCLCache.Store(mod, b.String())
	return b.String()
}

// initProbeModule copies a leaf module's .tf files somewhere writable and runs
// `terraform init -backend=false` once, so the many console calls below pay for
// initialisation a single time.
func initProbeModule(t *testing.T, modulePath []string) string {
	t.Helper()
	parts := append([]string{"..", "..", "terraform", "modules"}, modulePath...)
	src, err := filepath.Abs(filepath.Join(parts...))
	if err != nil {
		t.Fatalf("resolve module: %v", err)
	}
	dir := t.TempDir()
	entries, err := os.ReadDir(src)
	if err != nil {
		t.Fatalf("read module %s: %v", src, err)
	}
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".tf") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(src, ent.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", ent.Name(), err)
		}
		if err := os.WriteFile(filepath.Join(dir, ent.Name()), b, 0o644); err != nil {
			t.Fatalf("write %s: %v", ent.Name(), err)
		}
	}
	init := exec.Command("terraform", "init", "-backend=false", "-input=false")
	init.Dir = dir
	if out, err := init.CombinedOutput(); err != nil {
		t.Skipf("terraform init unavailable offline: %v\n%s", err, out)
	}
	return dir
}

func (e *lineEvaluator) console(t *testing.T, mod string, vars map[string]string, expr string) string {
	t.Helper()

	keys := make([]string, 0, len(vars))
	for k := range vars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var tfvars strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&tfvars, "%s = %s\n", k, vars[k])
	}

	// The same (module, vars, expr) is asked for repeatedly — every probe shares
	// a baseline render with every other probe that has the same `With`.
	key := mod + "\x00" + tfvars.String() + "\x00" + expr
	e.mu.Lock()
	if v, ok := e.cache[key]; ok {
		e.mu.Unlock()
		return v
	}
	e.mu.Unlock()

	// -var-file rather than an auto-loaded tfvars in the module directory: the
	// probes run in parallel against the same initialised copy, and a shared
	// mutable file there would have each of them evaluating another's variables.
	varFile := filepath.Join(t.TempDir(), "probe.tfvars")
	if err := os.WriteFile(varFile, []byte(tfvars.String()), 0o644); err != nil {
		t.Fatalf("write probe tfvars: %v", err)
	}
	dir := e.dirs[mod]
	cmd := exec.Command("terraform", "console", "-var-file="+varFile)
	cmd.Dir = dir
	cmd.Stdin = strings.NewReader(expr + "\n")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("terraform console in %s failed: %v\nvars:\n%s\nexpr:\n%s", mod, err, tfvars.String(), expr)
	}

	var last string
	for _, l := range strings.Split(string(out), "\n") {
		if s := strings.TrimSpace(l); s != "" {
			last = s
		}
	}
	var encoded string
	if err := json.Unmarshal([]byte(last), &encoded); err != nil {
		// "(known after apply)" is the failure that matters: it means a value in
		// the surface depends on a resource or data source, so every comparison
		// against it is unknown == unknown and every probe passes vacuously.
		t.Fatalf("module %s did not evaluate to a printable value (got %q).\n"+
			"If this is \"(known after apply)\", a variable the surface depends on is unset — every probe would "+
			"then compare unknown against unknown and pass without checking anything.\nvars:\n%s", mod, last, tfvars.String())
	}

	e.mu.Lock()
	e.cache[key] = encoded
	e.mu.Unlock()
	return encoded
}
