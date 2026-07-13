package cli

import (
	"bytes"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"gopkg.in/yaml.v3"
)

// A --config-file that sets ONE resource toggle must not zero the rest. Before
// the DefaultResources() pre-seed, `resources: {transit_gateway: {create: false}}`
// left BNK / registry-COS / cert-manager at the bool zero value (false), silently
// disabling them.
func TestConfigFilePartialResourcesKeepsDefaults(t *testing.T) {
	body := []byte(`
prefix: demo
cluster:
  create: true
  name: demo
resources:
  transit_gateway:
    create: false
`)
	decode := func(seed bool) config.Workspace {
		t.Helper()
		var ws config.Workspace
		if seed {
			ws.Resources = config.DefaultResources() // what runInitFromConfigFile does
		}
		dec := yaml.NewDecoder(bytes.NewReader(body))
		dec.KnownFields(true)
		if err := dec.Decode(&ws); err != nil {
			t.Fatalf("decode: %v", err)
		}
		return ws
	}

	// With the pre-seed: the file's toggle wins, the rest keep their defaults.
	ws := decode(true)
	if ws.Resources.TransitGateway.Create {
		t.Error("transit_gateway.create should be false — the file said so")
	}
	for name, got := range map[string]bool{
		"bnk":          ws.Resources.BNK.Create,
		"registry_cos": ws.Resources.RegistryCOS.Create,
		"cert_manager": ws.Resources.CertManager.Create,
		"client_vpc":   ws.Resources.ClientVPC.Create,
	} {
		if !got {
			t.Errorf("%s.create = false, want true — a partial resources block must not zero the other toggles", name)
		}
	}

	// Without it, this is the bug: yaml only sets what the file mentions, so every
	// other toggle lands on the bool zero value. Pinned so the pre-seed cannot be
	// dropped without a failure here.
	if bare := decode(false); bare.Resources.BNK.Create || bare.Resources.RegistryCOS.Create {
		t.Error("expected the un-seeded decode to zero the unmentioned toggles; " +
			"if this now passes, yaml.v3 changed and the pre-seed rationale needs revisiting")
	}
}
