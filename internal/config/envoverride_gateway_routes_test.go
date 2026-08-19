package config

import "testing"

// Comma-separated, matching ROKSBNKCTL_TRUSTED_PROFILE_ROLES — the one other
// list-valued override. The blank-entry cases are the ones that matter: a
// trailing comma in a CI variable is ordinary, and it must not produce an empty
// route kind that terraform then rejects with a confusing plan-time error.
func TestGatewayRouteExamplesEnvOverride(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []string
	}{
		{"single", "GRPCRoute", []string{"GRPCRoute"}},
		{"both", "GRPCRoute,L4Route", []string{"GRPCRoute", "L4Route"}},
		{"spaces are trimmed", " GRPCRoute , L4Route ", []string{"GRPCRoute", "L4Route"}},
		{"trailing comma yields no empty kind", "GRPCRoute,", []string{"GRPCRoute"}},
		{"only commas yields nothing", ",,", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("ROKSBNKCTL_GATEWAY_ROUTE_EXAMPLES", c.env)
			ws := &Workspace{}
			OverrideFromEnv(ws)
			got := ws.Gateway.RouteExamples
			if len(got) != len(c.want) {
				t.Fatalf("got %v, want %v", got, c.want)
			}
			for i := range got {
				if got[i] != c.want[i] {
					t.Fatalf("got %v, want %v", got, c.want)
				}
			}
		})
	}
}

// An unparseable or non-positive port must leave the field alone rather than
// writing 0, which the tfvars renderer treats as "unset" — so a typo would
// silently mean "use the default" instead of failing.
func TestGatewayL4ListenerPortEnvOverride(t *testing.T) {
	for _, c := range []struct {
		env  string
		want int
	}{
		{"9090", 9090},
		{"not-a-port", 0},
		{"0", 0},
		{"-1", 0},
	} {
		t.Run(c.env, func(t *testing.T) {
			t.Setenv("ROKSBNKCTL_GATEWAY_L4_LISTENER_PORT", c.env)
			ws := &Workspace{}
			OverrideFromEnv(ws)
			if ws.Gateway.L4ListenerPort != c.want {
				t.Errorf("port %q → %d, want %d", c.env, ws.Gateway.L4ListenerPort, c.want)
			}
		})
	}
}
