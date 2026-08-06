package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The guard's early returns run BEFORE any credential resolution or IBM API call.
// That matters twice over: a guard that fires when it shouldn't would demand
// credentials (and network) from workspaces that never needed them, and a guard
// that returns early when it shouldn't would silently stop protecting anything.
// These cases are reachable with no API, so they are worth pinning exactly.
func TestGuardVPCPrefixOverlap_SkipsWithoutAPICall(t *testing.T) {
	tgwAdopted := &config.ResourcesCfg{
		TransitGateway: config.ResourceToggle{Create: false, Existing: "shared-corp-tgw"},
	}
	tgwCreated := &config.ResourcesCfg{
		TransitGateway: config.ResourceToggle{Create: true},
	}

	cases := []struct {
		name string
		ws   *config.Workspace
		why  string
	}{
		{
			name: "no workspace",
			ws:   nil,
			why:  "nothing to check",
		},
		{
			name: "adopting an existing cluster",
			ws:   &config.Workspace{Cluster: config.ClusterCfg{Create: false}, Resources: tgwAdopted},
			why:  "the VPC already exists with its own prefixes; cluster up only attaches",
		},
		{
			name: "creating its own gateway",
			ws:   &config.Workspace{Cluster: config.ClusterCfg{Create: true}, Resources: tgwCreated},
			why:  "a brand-new gateway has no other VPC to collide with",
		},
		{
			name: "no resources block at all",
			ws:   &config.Workspace{Cluster: config.ClusterCfg{Create: true}},
			why:  "no adopted gateway is named",
		},
		{
			name: "adopt toggle set but gateway name blank",
			ws: &config.Workspace{
				Cluster:   config.ClusterCfg{Create: true},
				Resources: &config.ResourcesCfg{TransitGateway: config.ResourceToggle{Create: false, Existing: "  "}},
			},
			why: "there is no gateway to inspect",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// A blank APIKeySource would fail credential resolution, so reaching the
			// resolver at all shows up here as a non-nil error rather than passing by luck.
			cctx := &config.Context{WorkspaceName: "t", Workspace: tc.ws}
			if err := guardVPCPrefixOverlap(context.Background(), cctx); err != nil {
				t.Fatalf("guard should skip (%s), got: %v", tc.why, err)
			}
		})
	}
}

// A malformed cluster.vpc_cidr is the operator's own input, not an API condition —
// it must fail loudly rather than degrade to "could not check". Regression guard:
// folding this into the best-effort path would hide a typo'd CIDR until apply.
func TestGuardVPCPrefixOverlap_BadCIDRFailsLoudly(t *testing.T) {
	cctx := &config.Context{
		WorkspaceName: "t",
		Workspace: &config.Workspace{
			Cluster: config.ClusterCfg{Create: true, VPCCIDR: "10.242.0.0/24"}, // too small: split three ways
			Resources: &config.ResourcesCfg{
				TransitGateway: config.ResourceToggle{Create: false, Existing: "shared-corp-tgw"},
			},
		},
	}
	err := guardVPCPrefixOverlap(context.Background(), cctx)
	if err == nil {
		t.Fatal("a /24 cluster.vpc_cidr must be rejected, not silently accepted")
	}
	if !strings.Contains(err.Error(), "vpc_cidr") {
		t.Errorf("the error must name the offending setting, got: %v", err)
	}
}
