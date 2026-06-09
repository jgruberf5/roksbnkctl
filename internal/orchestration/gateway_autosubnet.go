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
// Remote} from the Testing phase's jumphost private IPs. It mutates ws in
// place BEFORE tfvars are rendered, so the values flow into the base
// tfvars layer (vars.go renders gateway_client_subnet_* only when the
// config field is non-empty). That layer is the LOWEST precedence, so a
// value set in config.yaml, a user tfvars file, or --var-file always wins
// — this only ever fills a gap.
//
// Best-effort and non-fatal (mirrors tryAutoJumphost's posture):
//   - remote ← the TGW jumphost private IP (client VPC, over the TGW) —
//     a clean 1:1 mapping.
//   - local  ← one cluster-VPC jumphost private IP (lowest zone name),
//     logged with the single-client caveat: the var is scalar but the rig
//     may have several cluster jumphosts, so set it explicitly to cover a
//     wider subnet.
//   - When the Testing phase isn't deployed (or its state predates the
//     private-IP outputs) and a field is still empty, warn once and leave
//     it to the module default — `gateway up` never fails on this.
func tryAutoGatewayClientSubnets(ws *config.Workspace, workspace string, w io.Writer) {
	if ws == nil {
		return
	}
	needLocal := ws.Gateway.ClientSubnetLocal == ""
	needRemote := ws.Gateway.ClientSubnetRemote == ""
	if !needLocal && !needRemote {
		return // fully specified by config/user — nothing to derive
	}

	tgwIP, clusterIPs, ok := config.TestingJumphostPrivateIPs(workspace)
	if !ok {
		fmt.Fprintln(w, "warning: gateway client subnet(s) unset and the Testing phase exposes no jumphost private IPs "+
			"(testing not deployed, or its state predates this build) — falling back to config/module defaults. "+
			"Set gateway.client_subnet_{local,remote} if the defaults don't match your clients.")
		return
	}

	if needRemote && tgwIP != "" {
		ws.Gateway.ClientSubnetRemote = tgwIP
		fmt.Fprintf(w, "✓ gateway_client_subnet_remote auto-derived from the TGW jumphost: %s\n", tgwIP)
	}
	if needLocal {
		if ip, zone := pickClusterJumphost(clusterIPs); ip != "" {
			ws.Gateway.ClientSubnetLocal = ip
			fmt.Fprintf(w, "✓ gateway_client_subnet_local auto-derived from cluster jumphost %s: %s "+
				"(single client — set gateway.client_subnet_local explicitly to cover a wider subnet)\n", zone, ip)
		}
	}

	// A partially-populated rig (e.g. only cluster jumphosts, no TGW one)
	// can leave a field empty; say which so the module default isn't a
	// silent surprise.
	if (needRemote && ws.Gateway.ClientSubnetRemote == "") || (needLocal && ws.Gateway.ClientSubnetLocal == "") {
		fmt.Fprintln(w, "warning: could not fully auto-derive gateway client subnets from the Testing phase "+
			"(a jumphost private IP was missing) — the unfilled value falls back to the module default.")
	}
}

// pickClusterJumphost deterministically selects one cluster jumphost
// (lowest zone name with a non-empty IP) from the {zone => private-IP}
// map. Returns ("","") for an empty/all-blank map.
func pickClusterJumphost(clusterIPs map[string]string) (ip, zone string) {
	zones := make([]string, 0, len(clusterIPs))
	for z := range clusterIPs {
		zones = append(zones, z)
	}
	sort.Strings(zones)
	for _, z := range zones {
		if v := strings.TrimSpace(clusterIPs[z]); v != "" {
			return v, z
		}
	}
	return "", ""
}
