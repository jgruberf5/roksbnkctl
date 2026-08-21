package config

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// #171. BNK 2.4 subsumes work this tool does for 2.3, and the guide's own
// "Changes From BNK Release-2.3" section is mostly SUBTRACTIONS — which are easy
// to miss when scoping from a PRD that enumerates additions.
//
// Each of these must be gated on the line rather than deleted: 2.3 is still the
// default manifest version, and removing any of them there breaks a shipping
// configuration.
//
// Asserted as gating EXPRESSIONS, not mentions. A Contains for the local's name
// is satisfied by the comment that explains the gate — checked by mutation, and
// it is the way three earlier guards in this repo passed against their own
// defect.
func tfSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRootForDemoTest(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// countGate matches `count = <something> && local.line_pre_24 ? 1 : 0` on the
// resource whose name is given — the actual gate, in the actual resource.
func assertCountGated(t *testing.T, src, resource string) {
	t.Helper()
	i := strings.Index(src, resource+"\" {")
	if i < 0 {
		t.Errorf("resource %s not found; this guard can no longer see what it checks", resource)
		return
	}
	// Look only at the resource's own head, not the whole file.
	head := src[i:min(i+400, len(src))]
	gated := regexp.MustCompile(`(?m)^\s*(?:count|for_each)\s*=[^\n]*local\.line_pre_24`)
	if !gated.MatchString(head) {
		t.Errorf("%s is created on both lines. BNK 2.4 does not need it, and shipping it there "+
			"either duplicates what FLO now does or orphans an object the product owns.\n"+
			"--- resource head ---\n%s", resource, head)
	}
}

func TestTheWorkTwoFourSubsumesIsGatedOffOnTwoFour(t *testing.T) {
	flo := tfSource(t, "terraform/modules/flo/modules/flo/main.tf")

	// 1. CIS is integrated into FLO on 2.4 — no separate chart, release or SCC.
	for _, r := range []string{
		`resource "null_resource" "cis_chart_pull`,
		`resource "helm_release" "cis`,
		`resource "kubectl_manifest" "cis_scc_privileged`,
	} {
		assertCountGated(t, flo, r)
	}

	// 2. Node labelling with app=f5-tmm is not required on 2.4. Dropping the Job
	// also drops an unpinned bitnami/kubectl:latest we would otherwise keep
	// shipping.
	for _, r := range []string{
		`resource "kubectl_manifest" "node_labeler_sa`,
		`resource "kubectl_manifest" "node_labeler_role`,
		`resource "kubectl_manifest" "node_labeler_binding`,
		`resource "kubectl_manifest" "node_labeler_job`,
	} {
		assertCountGated(t, flo, r)
	}

	// 3. macvlan-conf: the product creates its own internal NAD on 2.4. The name
	// must come out of the attachment list AND the resource must be gated, or the
	// object is orphaned against a name the guide reserves.
	assertCountGated(t, flo, `resource "kubectl_manifest" "nad_macvlan`)
	if !strings.Contains(flo, `local.line_pre_24 ? [local.nad_name_computed, "macvlan-conf"] : [local.nad_name_computed]`) {
		t.Error("macvlan-conf is still advertised to the CNEInstance on 2.4; the product supplies " +
			"its own internal NAD there and a second one conflicts")
	}
}

// 4. The SCC surface collapses from ~19 bindings to the one the install needs.
// Verified by reading the selector, and the counts themselves are pinned by a
// real `terraform console` in the commit that introduced them (19 -> 1).
func TestTheSCCSetIsLineSelected(t *testing.T) {
	src := tfSource(t, "terraform/modules/cne_instance/modules/cneinstance/main.tf")

	if !strings.Contains(src, "scc_policy_assignments = local.line_pre_24 ? local.scc_policy_assignments_23 : local.scc_policy_assignments_24") {
		t.Error("the SCC assignment set is not line-selected. 2.4's FLO grants its own components " +
			"what they need; shipping 2.3's nineteen privileged bindings there is an over-broad " +
			"grant nobody asked for.")
	}
	// The 2.4 set must be the FLO operator. An empty set would also "collapse"
	// and would fail at pod admission on a running cluster.
	if !strings.Contains(src, `service_account = "flo-f5-lifecycle-operator"`) {
		t.Error("the 2.4 SCC set must contain the FLO operator binding the install requires")
	}
}

// An unrecognised line must keep the 2.3 behaviour. Treating an unknown release
// as 2.4 would strip privileges and drop resources for something nobody has
// characterised, and the failure would land at pod admission on a live cluster.
func TestAnUnknownLineKeepsTheTwoThreeBehaviour(t *testing.T) {
	for _, rel := range []string{
		"terraform/modules/flo/modules/flo/main.tf",
		"terraform/modules/cne_instance/modules/cneinstance/main.tf",
	} {
		src := tfSource(t, rel)
		if !strings.Contains(src, `line_pre_24 = var.bnk_line != "2.4"`) {
			t.Errorf("%s should define the gate as `!= \"2.4\"`, not `== \"2.3\"`: an unrecognised "+
				"line must keep the additive 2.3 behaviour rather than silently taking 2.4's "+
				"subtractions", rel)
		}
	}
}
