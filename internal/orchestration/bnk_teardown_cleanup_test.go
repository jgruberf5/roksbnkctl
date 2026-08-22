package orchestration

import (
	"os"
	"strings"
	"testing"
)

// #172. Three teardown gaps, all of which produce leftover state that breaks the
// NEXT install rather than the current one — the failure mode hardest to
// attribute, because the run that caused it reported success.

// The guide's own cleanup block is wrong in two ways that leave objects behind
// if copied as written. Both corrections have to survive.
func TestTheCRDGroupListCorrectsTheGuide(t *testing.T) {
	groups := strings.Join(BNKCRDGroups, " ")

	// The guide greps metrics.f5.NET; the group on the cluster is metrics.f5.COM,
	// so that line matches nothing and the telemetry CRDs stay.
	if !strings.Contains(groups, "metrics.f5.com") {
		t.Error("metrics.f5.com missing — the guide's own block greps metrics.f5.net, which " +
			"matches nothing on the cluster")
	}
	if strings.Contains(groups, "metrics.f5.net") {
		t.Error("metrics.f5.net is the guide's typo; the group on the cluster is metrics.f5.com")
	}
	// The guide never mentions gateway.k8s.f5.com, so 2.4's six new gateway CRDs
	// are left behind entirely.
	if !strings.Contains(groups, "gateway.k8s.f5.com") {
		t.Error("gateway.k8s.f5.com missing — 2.4's six gateway CRDs would be left behind; " +
			"the guide does not mention this group at all")
	}
	// Both lines' object families, and the controller-generated IPAM group.
	for _, g := range []string{"k8s.f5.com", "k8s.f5net.com", "fic.f5.com"} {
		if !strings.Contains(groups, g) {
			t.Errorf("%s missing from the CRD group list", g)
		}
	}
}

// The IPAM checkpoint is the step that is easy to skip and expensive to skip.
// IPAM/IPAMRange are controller-generated, so removing the CNEInstance first
// takes away the thing that would have cleaned them up.
func TestTheUninstallOrderVerifiesIPAMBeforeTheCNEInstance(t *testing.T) {
	ipam, cne := -1, -1
	for i, step := range UninstallOrder {
		if strings.Contains(step, "IPAM") {
			ipam = i
		}
		if strings.Contains(step, "CNEInstance") {
			cne = i
		}
	}
	if ipam < 0 || cne < 0 {
		t.Fatalf("the order must name both the IPAM check and the CNEInstance; got %v", UninstallOrder)
	}
	if ipam > cne {
		t.Errorf("IPAM/IPAMRange must be verified gone BEFORE the CNEInstance is removed "+
			"(step %d vs %d). They are controller-generated, so removing the CNEInstance first "+
			"takes away what would have cleaned them up.", ipam, cne)
	}
}

// The secret list is spelled out rather than prefix-matched, because these sit
// in a namespace with unrelated secrets and a wildcard sweep is a delete against
// someone else's data.
func TestTheLicenceSecretListIsExplicitAndComplete(t *testing.T) {
	if len(LicenseSecretNames) < 28 {
		t.Errorf("only %d licence secrets listed; 28 were observed on a live cluster after a "+
			"teardown, and a missed one leaves the next install unable to re-license",
			len(LicenseSecretNames))
	}
	// Spot-check the ones whose absence produces the confusing failure: an install
	// that finds a previous activation and does not re-activate.
	for _, want := range []string{"activationstatus", "licensekey", "entitlements", "cwcstate", "jwtsecret"} {
		found := false
		for _, s := range LicenseSecretNames {
			if s == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("%q missing from the licence secret list", want)
		}
	}
	// No duplicates: a duplicate is a sign the list was edited by hand against a
	// screenshot rather than the guide.
	seen := map[string]bool{}
	for _, s := range LicenseSecretNames {
		if seen[s] {
			t.Errorf("duplicate entry %q", s)
		}
		seen[s] = true
	}
}

// The sweep must be namespace-scoped and must not touch CRDs. CRDs are
// cluster-scoped and shared with any other BNK install on the cluster; removing
// them as a side effect of one workspace's teardown would break the others.
func TestTheSweepDoesNotDeleteCRDs(t *testing.T) {
	// Structural: the sweep works through the typed Secrets client only. If it
	// ever grows a dynamic client, this is the test that should make someone
	// justify it.
	src := readSourceFile(t, "bnk_teardown_cleanup.go")
	if strings.Contains(src, "CustomResourceDefinition") || strings.Contains(src, "apiextensions") {
		t.Error("the teardown sweep reaches for CRDs. They are cluster-scoped and shared with " +
			"any other BNK install on the cluster — removing them is an explicit operator " +
			"decision, not a side effect of one workspace's teardown.")
	}
	if !strings.Contains(src, "Secrets(ns).Delete") {
		t.Error("the sweep should delete namespace-scoped secrets by exact name")
	}
}

// readSourceFile reads a file from this package's own directory, so a structural
// assertion names the file it is about rather than a path relative to the test
// runner's working directory.
func readSourceFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("reading %s: %v", name, err)
	}
	return string(b)
}
