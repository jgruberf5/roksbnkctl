package tf

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
)

// Our 2.4 CNEInstance is checked against F5's OWN reference install — the
// last-applied-configuration from their live 2.4 cluster, committed under
// testdata.
//
// The reference is taken from `last-applied-configuration` rather than the live
// object on purpose: a live CR is fattened with defaults the operator filled in,
// and comparing against those produces a long list of differences that are not
// differences. What F5 actually APPLIED is the apples-to-apples comparison.
//
// This found four spec keys we never emitted at all — tmmReplicas,
// watchNamespaces, placement and externalBigip. `placement` is the mechanism
// that replaced the node-labeler on 2.4: #171 removed the labeler as
// unnecessary, and nothing added what took over, so 2.4 shipped with neither and
// TMM spreading across nodes and zones was the scheduler's discretion rather
// than a requirement.
func TestOurTwoFourSpecCarriesEveryKeyF5Applies(t *testing.T) {
	b, err := os.ReadFile("testdata/f5-reference-2.4-cneinstance.json")
	if err != nil {
		t.Fatalf("reference: %v", err)
	}
	var ref map[string]any
	if err := json.Unmarshal(b, &ref); err != nil {
		t.Fatalf("decode reference: %v", err)
	}
	if len(ref) == 0 {
		t.Fatal("reference is empty; this test would pass vacuously")
	}

	ours := consoleJSON(t, []string{"cne_instance", "modules", "cneinstance"},
		"bnk_line = \"2.4\"\n", "nonsensitive(keys(local.cneinstance_spec))")
	var keys []string
	if err := json.Unmarshal([]byte(ours), &keys); err != nil {
		t.Fatalf("decode our keys from %q: %v", ours, err)
	}
	have := map[string]bool{}
	for _, k := range keys {
		have[k] = true
	}

	var missing []string
	for k := range ref {
		if !have[k] {
			missing = append(missing, k)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		t.Errorf("our 2.4 CNEInstance omits %d key(s) F5's reference applies: %s\n"+
			"Reference: internal/tf/testdata/f5-reference-2.4-cneinstance.json\n"+
			"A key F5 sets and we do not is a setting the operator defaults for us, which is a "+
			"choice nobody made.", len(missing), strings.Join(missing, ", "))
	}
}

// The three TMM settings F5 carries that this tree did not, plus the two things
// the reference pins that we were leaving to the operator.
func TestTwoFourMatchesTheReferenceOnTheSettingsThatDiffered(t *testing.T) {
	const mod = "cne_instance"
	tmm := consoleStrings(t, []string{mod, "modules", "cneinstance"}, "bnk_line = \"2.4\"\n",
		`nonsensitive([for e in local.adv_env["tmm"] : e.name])`)
	for _, want := range []string{"TMM_IGNORE_GATEWAYS", "DISABLE_HT", "ENABLE_K8S_ROUTES"} {
		found := false
		for _, n := range tmm {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Errorf("2.4 TMM env is missing %s, which F5's reference sets. got %v", want, tmm)
		}
	}

	ctrl := consoleStrings(t, []string{mod, "modules", "cneinstance"}, "bnk_line = \"2.4\"\n",
		`nonsensitive([for e in local.adv_env["cneController"] : "${e.name}=${e.value}" if e.name == "GATEWAY_API_VERSION"])`)
	if len(ctrl) != 1 || ctrl[0] != "GATEWAY_API_VERSION=1.5.0" {
		t.Errorf("2.4 must pin GATEWAY_API_VERSION to F5's 1.5.0; got %v.\n"+
			"Set nowhere, the controller runs on the operator default — v1.4.1 on the verified "+
			"cluster — and the 2.4 EA guide requires the 1.5 bundle for mTLS.", ctrl)
	}

	// demoMode: off on 2.4 (reference), still on for 2.3 (what has shipped).
	for _, tc := range []struct {
		line string
		want string
	}{{"2.4", "false"}, {"2.3", "true"}} {
		got := consoleJSON(t, []string{mod, "modules", "cneinstance"},
			"bnk_line = \""+tc.line+"\"\n",
			`nonsensitive(tostring(local.cneinstance_spec.advanced.demoMode.enabled))`)
		var s string
		if err := json.Unmarshal([]byte(got), &s); err != nil {
			t.Fatalf("decode demoMode for %s: %v", tc.line, err)
		}
		if s != tc.want {
			t.Errorf("demoMode.enabled on %s = %s, want %s", tc.line, s, tc.want)
		}
	}
}

// Placement is emitted with F5's shape, and disappears entirely when turned off
// rather than leaving an empty object for the CR to interpret.
func TestTwoFourPlacementMatchesTheReferenceAndCanBeTurnedOff(t *testing.T) {
	mod := []string{"cne_instance", "modules", "cneinstance"}

	on := consoleJSON(t, mod, "bnk_line = \"2.4\"\n",
		"nonsensitive(local.cneinstance_spec.placement)")
	for _, want := range []string{
		"podAntiAffinity", "kubernetes.io/hostname",
		"topologySpreadConstraints", "topology.kubernetes.io/zone",
		"DoNotSchedule", "f5-tmm",
	} {
		if !strings.Contains(on, want) {
			t.Errorf("2.4 placement is missing %q.\ngot: %s", want, on)
		}
	}

	off := consoleStrings(t, mod,
		"bnk_line = \"2.4\"\ncneinstance_tmm_anti_affinity = false\ncneinstance_tmm_zone_spread = false\n",
		"nonsensitive(keys(local.cneinstance_spec))")
	for _, k := range off {
		if k == "placement" {
			t.Error("with both placement toggles off, no placement key should be emitted at all — " +
				"an empty object is a thing the CR has to interpret")
		}
	}
}

// wholeCluster and watchNamespaces are validated TOGETHER by the product, and
// this test exists because I broke that.
//
// Conforming watchNamespaces to the reference's ["All"] while leaving
// wholeCluster at the true this tree had always rendered produced a CR the API
// server rejected outright:
//
//	CNEInstance "f5-bnk-f5-cne-controller" is invalid: spec: Invalid value:
//	"object": Invalid product configuration, please check WholeCluster,
//	WatchNamespaces and GatewayAPI settings
//
// Saying "watch everything" twice, in two ways that contradict, is not
// emphasis. It cost a full run: cluster build, install, failure — about an hour.
//
// The pair is asserted on both lines, because the bug was conforming one line's
// half of a pair.
func TestWholeClusterAndWatchNamespacesStayConsistent(t *testing.T) {
	mod := []string{"cne_instance", "modules", "cneinstance"}

	for _, tc := range []struct {
		line             string
		wantWholeCluster string
		wantWatch        string
	}{
		// 2.4 conforms to the reference: watch All, wholeCluster off.
		{"2.4", "false", `["All"]`},
		// 2.3 is untouched: wholeCluster true, no watchNamespaces at all.
		{"2.3", "true", `"(absent)"`},
	} {
		t.Run(tc.line, func(t *testing.T) {
			vars := "bnk_line = \"" + tc.line + "\"\ncneinstance_whole_cluster = true\n"
			got := consoleJSON(t, mod, vars,
				`nonsensitive("${local.cneinstance_spec.wholeCluster}|${jsonencode(try(local.cneinstance_spec.watchNamespaces, "(absent)"))}")`)
			var s string
			if err := json.Unmarshal([]byte(got), &s); err != nil {
				t.Fatalf("decode %q: %v", got, err)
			}
			want := tc.wantWholeCluster + "|" + tc.wantWatch
			if s != want {
				t.Errorf("wholeCluster|watchNamespaces on %s = %s, want %s.\n"+
					"These are validated together; wholeCluster=true with watchNamespaces=[\"All\"] "+
					"is rejected by the API server as an invalid product configuration.", tc.line, s, want)
			}
		})
	}
}

// deploymentSize defaults to Tiny on 2.4, matching both F5's reference and this
// tree's OWN variable description ("Tiny is what the BNK 2.4 install guide
// uses") — which had been contradicted by a Small default on both lines.
//
// This is not cosmetic. Small makes TMM request 4Gi of hugepages-2Mi, and a
// stock ROKS worker reports hugepages-2Mi=0 — including on F5's reference
// cluster, whose TMM pods request no hugepages at all and run fine on Tiny.
//
// It stayed invisible while demoMode was true, because demo mode drops the
// hugepage request. Turning demoMode off to conform exposed it: three TMM pods
// Pending on "0/3 nodes are available: 3 Insufficient hugepages-2Mi", followed
// by a 15-minute wait for an Available that could never arrive. Two settings
// that each looked right alone were wrong together.
func TestDeploymentSizeDefaultsToTheReferenceSizeOnTwoFour(t *testing.T) {
	mod := []string{"cne_instance", "modules", "cneinstance"}

	for _, tc := range []struct {
		name, vars, want string
	}{
		{"2.4 unset takes the reference size", "bnk_line = \"2.4\"\n", "Tiny"},
		{"2.3 unset is unchanged", "bnk_line = \"2.3\"\n", "Small"},
		// Explicit Small must be reachable on 2.4. Using the value itself as the
		// tri-state would have made the reference default unaskable-for.
		{"2.4 explicit Small is honoured", "bnk_line = \"2.4\"\ncneinstance_deployment_size = \"Small\"\n", "Small"},
		{"2.4 explicit Medium is honoured", "bnk_line = \"2.4\"\ncneinstance_deployment_size = \"Medium\"\n", "Medium"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := consoleJSON(t, mod, tc.vars, "nonsensitive(local.cneinstance_spec.deploymentSize)")
			var s string
			if err := json.Unmarshal([]byte(got), &s); err != nil {
				t.Fatalf("decode %q: %v", got, err)
			}
			if s != tc.want {
				t.Errorf("deploymentSize = %q, want %q", s, tc.want)
			}
		})
	}
}

// #187. Every other conformance test here asserts what the 2.4 spec CONTAINS.
// None asserted what it must NOT contain, and that is the gap the defect lived
// in: 2.4 kept emitting the 2.3 network surface — cloud-network-mapping, the two
// F5SPKVlan CRs, and the CLOUD_NETWORK_CONFIGMAP env pointing at it — alongside
// the 2.4 model. Both formats on one cluster is the device-IP conflict the guide
// warns about, and it left F5Tmm at Reconciled=False with the internal NAD never
// created, on an install that otherwise reported 18/18 conditions True.
//
// Ten of eleven reference checks passed on the broken cluster. A conformance
// suite that only asserts presence cannot see a thing that should be gone.
//
// Evaluated through terraform console rather than read from the source, so a
// gate that is present but wired to the wrong local still fails.
func TestTwoFourDoesNotCarryTheTwoThreeNetworkEnv(t *testing.T) {
	names := func(line string) map[string]bool {
		out := consoleJSON(t, []string{"cne_instance", "modules", "cneinstance"},
			"bnk_line = \""+line+"\"\n",
			"nonsensitive([for e in local.adv_env_defaults.cneController : e.name])")
		var got []string
		if err := json.Unmarshal([]byte(out), &got); err != nil {
			t.Fatalf("decode controller env names on %s from %q: %v", line, out, err)
		}
		if len(got) == 0 {
			t.Fatalf("no controller env names on %s; this test would pass vacuously", line)
		}
		set := map[string]bool{}
		for _, n := range got {
			set[n] = true
		}
		return set
	}

	if names("2.4")["CLOUD_NETWORK_CONFIGMAP"] {
		t.Error("the 2.4 controller env still carries CLOUD_NETWORK_CONFIGMAP.\n" +
			"  On 2.4 the controller reads Infra + GatewaySettings; pointing it at the 2.3\n" +
			"  ConfigMap as well leaves F5Tmm at Reconciled=False and the internal macvlan\n" +
			"  NAD uncreated, on an install that still reports every CNEInstance condition True.")
	}

	// 2.3 is still a shipping line and it is the only network model there.
	if !names("2.3")["CLOUD_NETWORK_CONFIGMAP"] {
		t.Error("the 2.3 controller env has lost CLOUD_NETWORK_CONFIGMAP — it must be gated, not deleted")
	}
}
