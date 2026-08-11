package tf

import (
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// renderFor is a thin helper: render a workspace and hand back the tfvars text.
func renderFor(t *testing.T, mut func(*config.Workspace)) string {
	t.Helper()
	// A prefix selects the PLANNED renderer (derived names + the resources block);
	// without one the sparse legacy render runs and never reaches these fields.
	ws := &config.Workspace{
		Prefix:   "acme",
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster:  config.ClusterCfg{Create: true, Name: "byo"},
		BNK:      config.BNKCfg{ManifestVersion: config.DefaultManifestVersion},
	}
	ws.Resources = config.DefaultResources()
	mut(ws)
	var b strings.Builder
	if err := RenderTFVars(&b, ws, "/kc", "/scratch"); err != nil {
		t.Fatalf("render: %v", err)
	}
	return b.String()
}

// #61 — adopting subnets. The VPC half already worked; without this the cluster
// lands in three subnets it invented inside the adopted VPC, outside whatever
// ACLs and routing made that network acceptable in the first place.
func TestRenderExistingClusterSubnets(t *testing.T) {
	out := renderFor(t, func(ws *config.Workspace) {
		ws.Resources.ClusterVPC = config.ResourceToggle{Create: false, Existing: "r014-vpc"}
		ws.Cluster.ExistingSubnetIDs = []string{"0757-a", "0757-b", "0757-c"}
	})
	for _, want := range []string{
		"use_existing_cluster_vpc = true",
		`existing_cluster_vpc_id = "r014-vpc"`,
		"use_existing_cluster_subnets = true",
		`existing_cluster_subnet_ids = ["0757-a", "0757-b", "0757-c"]`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// The default path must stay byte-identical, or every existing workspace gets a
// plan diff on upgrade. Absent settings must emit NOTHING, not `false`.
func TestRenderBYONetwork_SilentWhenUnset(t *testing.T) {
	out := renderFor(t, func(ws *config.Workspace) {})
	for _, absent := range []string{
		"use_existing_cluster_subnets",
		"existing_cluster_subnet_ids",
		"flp_vsi_create_vpc",
		"flp_vsi_vpc_name",
		"flp_vsi_subnet_cidr",
	} {
		if strings.Contains(out, absent) {
			t.Errorf("%q must not be emitted when unset — it changes the plan for existing workspaces", absent)
		}
	}
}

// #60 — the proxy building its own network. Only the values that were set are
// emitted; the rest fall to the terraform defaults.
func TestRenderFLPCreateVPC(t *testing.T) {
	out := renderFor(t, func(ws *config.Workspace) {
		ws.BNK.FLP = &config.BNKFLPCfg{
			Mode: "vsi",
			VSI: &config.BNKFLPVSICfg{
				CreateVPC:  true,
				VPCName:    "acme-flp-vpc",
				SubnetCIDR: "10.250.0.0/24",
			},
		}
	})
	for _, want := range []string{
		"flp_vsi_create_vpc = true",
		`flp_vsi_vpc_name = "acme-flp-vpc"`,
		`flp_vsi_subnet_cidr = "10.250.0.0/24"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

// Opting in without naming anything is legitimate — the terraform defaults cover
// both — so the toggle must still render on its own.
func TestRenderFLPCreateVPC_DefaultsOnly(t *testing.T) {
	out := renderFor(t, func(ws *config.Workspace) {
		ws.BNK.FLP = &config.BNKFLPCfg{Mode: "vsi", VSI: &config.BNKFLPVSICfg{CreateVPC: true}}
	})
	if !strings.Contains(out, "flp_vsi_create_vpc = true") {
		t.Errorf("the toggle must render alone:\n%s", out)
	}
	if strings.Contains(out, "flp_vsi_vpc_name") {
		t.Error("an unset name must not be emitted as an empty string")
	}
}

// Contradictory settings must be REFUSED, not silently resolved. Both of these
// fail invisibly in the modules — the ignored half is simply never read — so the
// operator would learn about it from a proxy on the wrong network, or a cluster
// in subnets they did not choose.
func TestBYONetwork_ContradictionsRefused(t *testing.T) {
	render := func(mut func(*config.Workspace)) error {
		ws := &config.Workspace{
			Prefix:   "acme",
			IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
			Cluster:  config.ClusterCfg{Create: true, Name: "byo"},
			BNK:      config.BNKCfg{ManifestVersion: config.DefaultManifestVersion},
		}
		ws.Resources = config.DefaultResources()
		mut(ws)
		var b strings.Builder
		return RenderTFVars(&b, ws, "/kc", "/scratch")
	}

	t.Run("flp vpc and create_vpc together", func(t *testing.T) {
		err := render(func(ws *config.Workspace) {
			ws.BNK.FLP = &config.BNKFLPCfg{Mode: "vsi", VSI: &config.BNKFLPVSICfg{
				VPC: "r014-adopted", CreateVPC: true,
			}}
		})
		if err == nil {
			t.Fatal("both set must be refused — create_vpc wins silently in the module")
		}
		if !strings.Contains(err.Error(), "mutually exclusive") {
			t.Errorf("the error must say why: %v", err)
		}
	})

	t.Run("subnets adopted without the VPC", func(t *testing.T) {
		err := render(func(ws *config.Workspace) {
			ws.Cluster.ExistingSubnetIDs = []string{"a", "b", "c"} // cluster_vpc left at create:true
		})
		if err == nil {
			t.Fatal("adopting subnets without adopting their VPC must be refused")
		}
		if !strings.Contains(err.Error(), "cluster_vpc") {
			t.Errorf("the error must name the missing half: %v", err)
		}
	})

	t.Run("each alone is fine", func(t *testing.T) {
		if err := render(func(ws *config.Workspace) {
			ws.BNK.FLP = &config.BNKFLPCfg{Mode: "vsi", VSI: &config.BNKFLPVSICfg{CreateVPC: true}}
		}); err != nil {
			t.Errorf("create_vpc alone must render: %v", err)
		}
		if err := render(func(ws *config.Workspace) {
			ws.BNK.FLP = &config.BNKFLPCfg{Mode: "vsi", VSI: &config.BNKFLPVSICfg{VPC: "r014-adopted"}}
		}); err != nil {
			t.Errorf("vpc alone must render: %v", err)
		}
	})
}
