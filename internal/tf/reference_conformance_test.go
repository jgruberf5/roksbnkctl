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
