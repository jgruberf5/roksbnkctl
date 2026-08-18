package tf

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// The GatewayClass identity settings must be ABSENT from the rendered tfvars
// unless config.yaml sets them. That is not a style preference: an empty
// gateway_controller_name is the terraform module's DERIVE sentinel (resolve it
// from flo_namespace), so emitting `gateway_controller_name = ""` and omitting
// the line mean the same thing today — but emitting it would make any future
// change to what "" means silently change behaviour for workspaces that never
// set the field. Omission keeps "unset" expressed as unset.
func TestGatewayIdentityOmittedWhenUnset(t *testing.T) {
	ws := &config.Workspace{
		BNK:     config.BNKCfg{ManifestVersion: config.DefaultManifestVersion},
		Gateway: config.GatewayCfg{},
	}
	var buf bytes.Buffer
	renderGatewayFields(&buf, ws)
	out := buf.String()
	for _, name := range []string{"gateway_class_name", "gateway_controller_name"} {
		if strings.Contains(out, name) {
			t.Errorf("%s must not be rendered when unset, got:\n%s", name, out)
		}
	}
}

func TestGatewayIdentityRendersWhenSet(t *testing.T) {
	ws := &config.Workspace{
		BNK: config.BNKCfg{ManifestVersion: config.DefaultManifestVersion},
		Gateway: config.GatewayCfg{
			ClassName:      "gateway-class-b",
			ControllerName: "f5.com/tenant-b-f5-cne-controller",
		},
	}
	var buf bytes.Buffer
	renderGatewayFields(&buf, ws)
	out := buf.String()
	for _, want := range []string{
		`gateway_class_name = "gateway-class-b"`,
		`gateway_controller_name = "f5.com/tenant-b-f5-cne-controller"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in:\n%s", want, out)
		}
	}
}
