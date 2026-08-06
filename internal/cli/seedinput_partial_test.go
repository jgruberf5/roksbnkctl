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
		// cluster_vpc, not client_vpc: the testing client defaults OFF now, so it
		// cannot witness "the pre-seed survived a partial file" any more.
		"cluster_vpc": ws.Resources.ClusterVPC.Create,
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

// A jumphost with nowhere to live must fail at init, not mid-apply: terraform
// resolves its VPC as "the one we created, else the named existing one", so with
// neither the data source looks up an empty name and fails opaquely much later.
func TestInvalidResourceCombo(t *testing.T) {
	mk := func(jump, create bool, existing string) *config.Workspace {
		return &config.Workspace{Resources: &config.ResourcesCfg{
			TGWJumphost: config.ResourceToggle{Create: jump},
			ClientVPC:   config.ResourceToggle{Create: create, Existing: existing},
		}}
	}
	cases := []struct {
		name    string
		ws      *config.Workspace
		wantErr bool
	}{
		{"no jumphost at all", mk(false, false, ""), false},
		{"jumphost + new client VPC", mk(true, true, ""), false},
		{"jumphost + adopted client VPC", mk(true, false, "shared-vpc"), false},
		{"jumphost with neither", mk(true, false, ""), true},
		{"jumphost, existing is whitespace", mk(true, false, "   "), true},
		{"nil resources", &config.Workspace{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := invalidResourceCombo(tc.ws)
			if tc.wantErr && err == nil {
				t.Fatal("want an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("want no error, got %v", err)
			}
		})
	}
}
