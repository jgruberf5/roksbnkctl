package orchestration

import (
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #170. An earlier reading of the 2.4 crd-installer's graceful-skip log
// concluded the admission sweep was redundant. It is not — that log is the
// installer HANDLING the policy by giving up, leaving the cluster on whatever
// Gateway API bundle OpenShift ships.
//
// The 2.4 guide still requires deleting the ValidatingAdmissionPolicy and its
// binding, and warns about exactly the race the sweep was built for:
// "may have to be retried if the validating admission policy comes back
// quickly". What changed is WHEN: only an mTLS install needs Gateway API 1.5.0
// standard. A base install needs neither.

func wsFor(manifest string, mtls bool) *config.Context {
	return &config.Context{
		WorkspaceName: "t",
		Workspace: &config.Workspace{
			BNK: config.BNKCfg{ManifestVersion: manifest, GatewayAPIMTLS: mtls},
		},
	}
}

func TestTheSweepAlwaysRunsOnTwoThree(t *testing.T) {
	// 2.3's crd-installer FORCES the CRDs and is blocked without the sweep. The
	// symptom is not a CRD error — it is CNEControllerAvailable never appearing
	// fifteen minutes later.
	if !admissionSweepNeeded(wsFor("2.3.0-3.2598.3-0.0.170", false)) {
		t.Error("2.3 must always sweep; without it the FLO crd-installer stays blocked")
	}
	// The mTLS flag is a 2.4 concept and must not turn the sweep OFF on 2.3.
	if !admissionSweepNeeded(wsFor("2.3.0-3.2598.3-0.0.170", true)) {
		t.Error("the mTLS flag must not disable the sweep on 2.3")
	}
}

func TestTwoFourSweepsOnlyForMTLS(t *testing.T) {
	if admissionSweepNeeded(wsFor("2.4.0-EA", false)) {
		t.Error("a base 2.4 install does not need the newer Gateway API bundle, and deleting a " +
			"platform admission policy on a cluster that does not need it is a change nobody " +
			"asked for")
	}
	if !admissionSweepNeeded(wsFor("2.4.0-EA", true)) {
		t.Error("an mTLS 2.4 install needs Gateway API 1.5.0 standard, which means winning the " +
			"race against the ingress-operator recreating its policy")
	}
}

// Unknown or unreadable input keeps today's behaviour. The asymmetry is
// deliberate: sweeping when it was not needed costs a goroutine that finds
// nothing, and skipping when it was needed costs a fifteen-minute timeout with
// no useful error.
func TestUnknownInputKeepsSweeping(t *testing.T) {
	for name, cctx := range map[string]*config.Context{
		"nil context":   nil,
		"nil workspace": {WorkspaceName: "t"},
		"unparseable":   wsFor("not-a-version", false),
		"empty":         wsFor("", false),
		"future line":   wsFor("9.9.9", false),
	} {
		if !admissionSweepNeeded(cctx) {
			t.Errorf("%s: must default to sweeping — the cost of a needless sweep is a goroutine, "+
				"the cost of a missing one is a 15-minute timeout with no useful error", name)
		}
	}
}
