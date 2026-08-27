package cli

import (
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// #246, at the level where it actually cost something. A workspace that ADOPTS a
// transit gateway must still be adopting one after config.yaml -> .env.
//
// It was not. OverridePaths recorded ROKSBNKCTL_TRANSIT_GATEWAY_CREATE against
// all eight resources.*.create paths comma-joined, envLinesFor looks the value up
// as a single dotted path, the lookup missed, and the line was omitted. `existing`
// survived, `create` did not -- so a round-trip returned create to its default of
// true and the next `up` would PROVISION a transit gateway rather than adopt
// bnkci-testing. On a shared TGW that is not a cosmetic loss.
func TestAdoptingATransitGatewaySurvivesTheEnvRoundTrip(t *testing.T) {
	r := config.DefaultResources()
	r.TransitGateway.Create = false
	r.TransitGateway.Existing = "bnkci-testing"

	lines, err := envLinesFor(&config.Workspace{Prefix: "rt", Resources: r})
	if err != nil {
		t.Fatalf("envLinesFor: %v", err)
	}
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "ROKSBNKCTL_TRANSIT_GATEWAY_CREATE=false") {
		var kept []string
		for _, l := range lines {
			if strings.Contains(l, "TRANSIT_GATEWAY") {
				kept = append(kept, strings.TrimSpace(l))
			}
		}
		t.Errorf("resources.transit_gateway.create=false was dropped from the .env form.\n"+
			"Only these survived: %v\n"+
			"A round-trip therefore returns create to its default of true, and the next `up` "+
			"provisions a transit gateway instead of adopting the existing one.", kept)
	}
	if !strings.Contains(joined, "ROKSBNKCTL_TRANSIT_GATEWAY_NAME=bnkci-testing") {
		t.Errorf("the adopted gateway's name did not survive either:\n%s", joined)
	}
}

// The same property for every other resource toggle, so the fix is not pinned to
// the one resource that happened to expose it.
func TestEveryResourceCreateToggleSurvivesTheEnvRoundTrip(t *testing.T) {
	for _, tc := range []struct {
		env string
		set func(*config.ResourcesCfg)
	}{
		{"ROKSBNKCTL_REGISTRY_COS_CREATE", func(r *config.ResourcesCfg) { r.RegistryCOS.Create = false }},
		{"ROKSBNKCTL_CERT_MANAGER_CREATE", func(r *config.ResourcesCfg) { r.CertManager.Create = false }},
		{"ROKSBNKCTL_BNK_CREATE", func(r *config.ResourcesCfg) { r.BNK.Create = false }},
		{"ROKSBNKCTL_CLUSTER_VPC_CREATE", func(r *config.ResourcesCfg) { r.ClusterVPC.Create = false }},
		{"ROKSBNKCTL_CLIENT_VPC_CREATE", func(r *config.ResourcesCfg) { r.ClientVPC.Create = true }},
		{"ROKSBNKCTL_CLUSTER_JUMPHOSTS_CREATE", func(r *config.ResourcesCfg) { r.ClusterJumphosts.Create = true }},
		{"ROKSBNKCTL_TGW_JUMPHOST_CREATE", func(r *config.ResourcesCfg) { r.TGWJumphost.Create = true }},
	} {
		t.Run(tc.env, func(t *testing.T) {
			r := config.DefaultResources()
			tc.set(r)
			lines, err := envLinesFor(&config.Workspace{Prefix: "rt", Resources: r})
			if err != nil {
				t.Fatalf("envLinesFor: %v", err)
			}
			if !strings.Contains(strings.Join(lines, "\n"), tc.env+"=") {
				t.Errorf("%s was dropped from the .env form — the toggle does not survive a round-trip", tc.env)
			}
		})
	}
}
