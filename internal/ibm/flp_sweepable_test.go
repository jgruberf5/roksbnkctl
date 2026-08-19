package ibm

import "testing"

// The claimed benefit of #88: prefixed FLP resources become visible to the
// orphan sweep. Assert it rather than asserting it in a PR description.
func TestPrefixedFLPResourcesAreSweepable(t *testing.T) {
	const prefix = "bnk-ci"
	for _, n := range []string{
		"bnk-ci-flp-vsi", "bnk-ci-flp-vsi-subnet", "bnk-ci-flp-vsi-sg",
		"bnk-ci-flp-vsi-fip", "bnk-ci-flp-vsi-vpc", "bnk-ci-flp-vsi-egress",
	} {
		if !matchesPrefix(n, prefix) {
			t.Errorf("%s must be sweepable by cleanup under prefix %q", n, prefix)
		}
	}
	// And the legacy names still are NOT — the gap the issue records, kept
	// visible so nobody assumes upgrading fixes already-deployed proxies.
	for _, n := range []string{"flp-vsi", "flp-vsi-subnet"} {
		if matchesPrefix(n, prefix) {
			t.Errorf("%s should NOT match — legacy names remain unsweepable by design", n)
		}
	}
}
