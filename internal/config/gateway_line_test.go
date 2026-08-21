package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #173. The gateway phase targeted the 2.3 API in three ways, each of which
// fails differently under 2.4:
//
//   - parametersRef pointed at k8s.f5net.com/F5BnkGateway. 2.4 wants
//     gateway.k8s.f5.com/GatewaySettings, and a Gateway whose parametersRef
//     names a kind the controller does not read simply never programs.
//   - the Gateway lived in the application namespace. The 2.4 guide requires
//     GatewaySettings and Gateway to share one, and puts both in the FLO
//     namespace.
//   - VIPs were computed from gateway_vip_start_host. Under 2.4 IPAM allocates
//     them and the address is only knowable from status.

func gwSource(t *testing.T, rel string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(repoRootForDemoTest(t), filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTheGatewayParametersRefIsLineSelected(t *testing.T) {
	src := gwSource(t, "terraform/modules/gateway/main.tf")

	if !strings.Contains(src, `kind  = "GatewaySettings"`) {
		t.Error("the Gateway's parametersRef never names GatewaySettings, so a 2.4 Gateway " +
			"points at a kind the controller does not read and never programs")
	}
	if !strings.Contains(src, "gateway_parameters_ref = local.line_pre_24 ?") {
		t.Error("parametersRef must be line-selected; 2.3 needs F5BnkGateway and 2.4 needs " +
			"GatewaySettings, and neither works on the other line")
	}
}

// The Gateway moves namespace on 2.4, and every route's parentRef has to follow
// it. A route left pointing at the app namespace attaches to nothing.
func TestEveryRouteFollowsTheGatewayNamespace(t *testing.T) {
	src := gwSource(t, "terraform/modules/gateway/main.tf")

	if !strings.Contains(src, "gateway_ns_effective = local.line_pre_24 ? var.app_namespace : var.flo_namespace") {
		t.Error("the Gateway namespace is not line-selected; the 2.4 guide requires " +
			"GatewaySettings and Gateway to share a namespace")
	}
	// All three routes (http, grpc, l4) must use it. Counting rather than
	// spot-checking: the failure mode is one route left behind, which attaches to
	// nothing and is invisible until traffic is tried.
	if n := strings.Count(src, "namespace   = local.gateway_ns_effective"); n < 3 {
		t.Errorf("only %d route parentRef(s) follow the Gateway namespace; expected 3 "+
			"(http, grpc, l4). A route left pointing at the app namespace attaches to nothing.", n)
	}
	if strings.Contains(src, "namespace   = var.app_namespace") {
		t.Error("a parentRef still hardcodes the application namespace")
	}
}

// The allocated VIP is only knowable from status under 2.4. `gateway status`
// already reads Gateway.status.addresses — but it must look in the namespace the
// Gateway is actually in, or it reports nothing on 2.4 and the operator has no
// way to learn the address.
func TestGatewayStatusLooksInTheRightNamespace(t *testing.T) {
	src := gwSource(t, "internal/cli/phase_status.go")

	if !strings.Contains(src, `outString(outs, "gateway_namespace")`) {
		t.Error("gateway status looks the Gateway up in a fixed namespace; on 2.4 the Gateway " +
			"is in the FLO namespace and the lookup finds nothing, so the IPAM-allocated VIP " +
			"is unreportable")
	}
	// Older workspaces have no gateway_namespace output. Falling back keeps them
	// working rather than making an upgrade a silent regression.
	if !strings.Contains(src, `gwNS = outString(outs, "gateway_app_namespace")`) {
		t.Error("a workspace whose outputs predate gateway_namespace must still resolve")
	}
	// And the 2.4 CRs should be reported, or a 2.4 Gateway shows with no visible
	// source of truth behind it.
	for _, kind := range []string{"GatewaySettings", "Infra"} {
		if !strings.Contains(src, `"`+kind+`"`) {
			t.Errorf("gateway status does not report %s; on 2.4 that is where the Gateway's "+
				"configuration and addressing actually live", kind)
		}
	}
}
