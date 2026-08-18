package config

import "testing"

// Both settings exist so a CI matrix can stand two BNK installs up in one
// cluster without a per-job config.yaml. An env surface that does not actually
// reach the field is the failure mode this guards.
func TestGatewayIdentityEnvOverrides(t *testing.T) {
	t.Setenv("ROKSBNKCTL_GATEWAY_CLASS_NAME", "gateway-class-b")
	t.Setenv("ROKSBNKCTL_GATEWAY_CONTROLLER_NAME", "f5.com/tenant-b-f5-cne-controller")

	ws := &Workspace{}
	applied := OverrideFromEnv(ws)

	if ws.Gateway.ClassName != "gateway-class-b" {
		t.Errorf("gateway.class_name = %q", ws.Gateway.ClassName)
	}
	if ws.Gateway.ControllerName != "f5.com/tenant-b-f5-cne-controller" {
		t.Errorf("gateway.controller_name = %q", ws.Gateway.ControllerName)
	}

	// The applied list is what `init --override-from-env` prints, and an override
	// that takes effect without being reported is indistinguishable from a
	// config.yaml that already said so.
	want := map[string]bool{
		"gateway.class_name (ROKSBNKCTL_GATEWAY_CLASS_NAME)":           false,
		"gateway.controller_name (ROKSBNKCTL_GATEWAY_CONTROLLER_NAME)": false,
	}
	for _, a := range applied {
		if _, ok := want[a]; ok {
			want[a] = true
		}
	}
	for label, seen := range want {
		if !seen {
			t.Errorf("%q was not reported as applied", label)
		}
	}
}

// Unset must stay unset. Empty gateway.controller_name is the terraform DERIVE
// sentinel, so an env surface that wrote "" would turn "derive it from
// flo_namespace" into an explicit empty controllerName if the sentinel ever
// changed meaning.
func TestGatewayIdentityUnsetEnvLeavesConfigAlone(t *testing.T) {
	ws := &Workspace{Gateway: GatewayCfg{ClassName: "from-config"}}
	OverrideFromEnv(ws)
	if ws.Gateway.ClassName != "from-config" {
		t.Errorf("an unset env var overwrote config.yaml: %q", ws.Gateway.ClassName)
	}
	if ws.Gateway.ControllerName != "" {
		t.Errorf("controller_name should still be the derive sentinel, got %q", ws.Gateway.ControllerName)
	}
}
