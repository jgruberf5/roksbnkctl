package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Unset must render NOTHING. This is the compatibility guarantee the whole
// feature rests on: a workspace that never asks for route examples produces
// byte-identical tfvars to before, so no existing deployment moves.
func TestGatewayRouteExamplesOmittedWhenUnset(t *testing.T) {
	ws := &config.Workspace{
		BNK:     config.BNKCfg{ManifestVersion: config.DefaultManifestVersion},
		Gateway: config.GatewayCfg{},
	}
	var buf bytes.Buffer
	renderGatewayFields(&buf, ws)
	out := buf.String()
	for _, name := range []string{"gateway_route_examples", "gateway_l4_listener_port"} {
		if strings.Contains(out, name) {
			t.Errorf("%s must not be rendered when unset, got:\n%s", name, out)
		}
	}
}

func TestGatewayRouteExamplesRenderAsAnHCLList(t *testing.T) {
	ws := &config.Workspace{
		BNK: config.BNKCfg{ManifestVersion: config.DefaultManifestVersion},
		Gateway: config.GatewayCfg{
			RouteExamples:  []string{"GRPCRoute", "L4Route"},
			L4ListenerPort: 9090,
		},
	}
	var buf bytes.Buffer
	renderGatewayFields(&buf, ws)
	out := buf.String()
	for _, want := range []string{
		`gateway_route_examples = ["GRPCRoute", "L4Route"]`,
		`gateway_l4_listener_port = 9090`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}
