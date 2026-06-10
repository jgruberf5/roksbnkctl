package orchestration

// PRD 12 — best-effort auto-derivation of the gateway client-subnet values
// from the deployed Testing phase. `gateway up` is always run after the
// Testing phase is up, so when the operator hasn't set the client subnets
// we can read the jumphosts' private IPs and install static routes that
// actually reach the test clients — instead of the module's placeholder
// defaults (10.244.64.12 / 10.245.64.5), which are wrong for every real
// workspace.

import (
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// tryAutoGatewayClientSubnets fills empty ws.Gateway.ClientSubnet{Local,
// Remote} from the Testing phase's jumphost subnet CIDRs. It mutates ws in
// place BEFORE tfvars are rendered, so the values flow into the base
// tfvars layer (vars.go renders gateway_client_subnet_* only when the
// config list is non-empty). That layer is the LOWEST precedence, so a
// value set in config.yaml, a user tfvars file, or --var-file always wins
// — this only ever fills a gap.
//
// The values are LISTS (the module installs one static route per entry ×
// zone), so the matrix can drive traffic from every cluster jumphost —
// same-zone AND different-zone — and each has a TMM return route:
//   - local  ← every cluster-VPC jumphost subnet CIDR (one per AZ).
//   - remote ← the client-VPC jumphost subnet CIDR (over the TGW).
//
// Best-effort and non-fatal (mirrors tryAutoJumphost's posture): when the
// Testing phase isn't deployed (or its state predates the subnet-CIDR
// outputs) and a field is still empty, warn once and leave it to the
// module default — `gateway up` never fails on this.
func tryAutoGatewayClientSubnets(ws *config.Workspace, workspace string, w io.Writer) {
	if ws == nil {
		return
	}
	needLocal := len(ws.Gateway.ClientSubnetLocal) == 0
	needRemote := len(ws.Gateway.ClientSubnetRemote) == 0
	if !needLocal && !needRemote {
		return // fully specified by config/user — nothing to derive
	}

	tgwCIDR, clusterCIDRs, ok := config.TestingJumphostSubnetCIDRs(workspace)
	if !ok {
		fmt.Fprintln(w, "warning: gateway client subnet(s) unset and the Testing phase exposes no jumphost subnet CIDRs "+
			"(testing not deployed, or its state predates this build) — falling back to config/module defaults. "+
			"Set gateway.client_subnet_{local,remote} if the defaults don't match your clients.")
		return
	}

	if needRemote && tgwCIDR != "" {
		ws.Gateway.ClientSubnetRemote = []string{tgwCIDR}
		fmt.Fprintf(w, "✓ gateway_client_subnet_remote auto-derived from the TGW jumphost subnet: %s\n", tgwCIDR)
	}
	if needLocal {
		if cidrs := sortedClusterCIDRs(clusterCIDRs); len(cidrs) > 0 {
			ws.Gateway.ClientSubnetLocal = cidrs
			fmt.Fprintf(w, "✓ gateway_client_subnet_local auto-derived from %d cluster jumphost subnet(s): %s\n",
				len(cidrs), strings.Join(cidrs, ", "))
		}
	}

	// A partially-populated rig (e.g. only cluster jumphosts, no TGW one)
	// can leave a list empty; say which so the module default isn't a
	// silent surprise.
	if (needRemote && len(ws.Gateway.ClientSubnetRemote) == 0) || (needLocal && len(ws.Gateway.ClientSubnetLocal) == 0) {
		fmt.Fprintln(w, "warning: could not fully auto-derive gateway client subnets from the Testing phase "+
			"(a jumphost subnet CIDR was missing) — the unfilled value falls back to the module default.")
	}
}

// sortedClusterCIDRs returns the cluster jumphost subnet CIDRs in
// deterministic order (by zone name), dropping blanks. One per AZ, so the
// local static routes cover every cluster-VPC client.
func sortedClusterCIDRs(clusterCIDRs map[string]string) []string {
	zones := make([]string, 0, len(clusterCIDRs))
	for z := range clusterCIDRs {
		zones = append(zones, z)
	}
	sort.Strings(zones)
	out := make([]string, 0, len(zones))
	for _, z := range zones {
		if v := strings.TrimSpace(clusterCIDRs[z]); v != "" {
			out = append(out, v)
		}
	}
	return out
}
