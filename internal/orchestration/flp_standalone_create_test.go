package orchestration

import (
	"bytes"
	"os"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// #76: create_vpc could not deploy a cluster-less proxy. The gate keyed on
// vsi.vpc, which is mutually exclusive with create_vpc, so no configuration both
// opted into creating a VPC and opened the gate — create_vpc alone left it shut
// and `flp up` failed with "no cluster found".
//
// This is the exact environment a cluster-less runner sets.
func TestStandaloneFLPVSIOpensForCreateVPC(t *testing.T) {
	t.Setenv("ROKSBNKCTL_FLP_MODE", "vsi")
	t.Setenv("ROKSBNKCTL_FLP_VSI_CREATE_VPC", "true")
	t.Setenv("ROKSBNKCTL_FLP_VSI_VPC_NAME", "flp-own-vpc")
	t.Setenv("ROKSBNKCTL_FLP_VSI_SUBNET_CIDR", "10.250.0.0/24")

	ws := &config.Workspace{}
	config.OverrideFromEnv(ws)

	if !StandaloneFLPVSI(ws) {
		t.Fatal("create_vpc must open the standalone gate — it is the whole point of #60")
	}
}

func TestStandaloneFLPVSIStillOpensForAdoptedVPC(t *testing.T) {
	ws := &config.Workspace{BNK: config.BNKCfg{FLP: &config.BNKFLPCfg{
		Mode: "vsi", VSI: &config.BNKFLPVSICfg{VPC: "r014-abc"},
	}}}
	if !StandaloneFLPVSI(ws) {
		t.Error("adopting a VPC must still open the gate — unchanged behaviour")
	}
}

// Neither is not standalone: the proxy has no network, so it belongs in a
// cluster's VPC and the cluster-required precondition should apply.
func TestStandaloneFLPVSIClosedWithoutANetwork(t *testing.T) {
	for _, ws := range []*config.Workspace{
		{},
		{BNK: config.BNKCfg{FLP: &config.BNKFLPCfg{Mode: "vsi"}}},
		{BNK: config.BNKCfg{FLP: &config.BNKFLPCfg{Mode: "vsi", VSI: &config.BNKFLPVSICfg{}}}},
		// helm mode is never standalone, whatever the VSI block says
		{BNK: config.BNKCfg{FLP: &config.BNKFLPCfg{Mode: "helm", VSI: &config.BNKFLPVSICfg{CreateVPC: true}}}},
	} {
		if StandaloneFLPVSI(ws) {
			t.Errorf("gate opened with no network / wrong mode: %+v", ws.BNK.FLP)
		}
	}
}

// The override forced use_existing_cluster_vpc = true with whatever id it had.
// On the create path that id is empty, and an empty adopt fails at plan — which
// is the second half of why create_vpc was unusable.
func TestFLPOverrideDoesNotAdoptWhenCreating(t *testing.T) {
	dir := t.TempDir()
	p, err := writeFLPPhaseOverrideAt(dir, &config.ClusterOutputs{VPCID: ""}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "use_existing_cluster_vpc = false") {
		t.Errorf("create path must not adopt a VPC:\n%s", got)
	}
	if strings.Contains(got, "existing_cluster_vpc_id") {
		t.Errorf("create path must not emit an empty adopt id:\n%s", got)
	}
	// Everything else the standalone override forces off must be unchanged.
	for _, want := range []string{
		"create_roks_cluster = false",
		"deploy_flp_vsi = true",
		"cluster_absent = true",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q:\n%s", want, got)
		}
	}
}

// Adopting is unchanged: id present, adopt true.
func TestFLPOverrideStillAdoptsWhenGivenAVPC(t *testing.T) {
	dir := t.TempDir()
	p, err := writeFLPPhaseOverrideAt(dir, &config.ClusterOutputs{VPCID: "r014-abc"}, true, true)
	if err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	got := string(body)
	if !strings.Contains(got, "use_existing_cluster_vpc = true") ||
		!strings.Contains(got, `existing_cluster_vpc_id = "r014-abc"`) {
		t.Errorf("adopt path changed:\n%s", got)
	}
}

// The generated override is read by humans debugging a phase. It used to assert
// "The cluster already exists" on every path, including the standalone one where
// cluster_absent = true says the opposite three lines below — a file that
// contradicts itself reads as a bug in the tool.
func TestFLPOverrideHeaderMatchesThePath(t *testing.T) {
	for _, c := range []struct {
		name          string
		clusterAbsent bool
		want, notWant string
	}{
		{"standalone", true, "STANDALONE", "The cluster already exists"},
		{"with a cluster", false, "The cluster already exists", "STANDALONE"},
	} {
		t.Run(c.name, func(t *testing.T) {
			p, err := writeFLPPhaseOverrideAt(t.TempDir(),
				&config.ClusterOutputs{VPCID: "r014-abc"}, true, c.clusterAbsent)
			if err != nil {
				t.Fatal(err)
			}
			body, _ := os.ReadFile(p)
			got := string(body)
			if !strings.Contains(got, c.want) {
				t.Errorf("header missing %q:\n%s", c.want, got)
			}
			if strings.Contains(got, c.notWant) {
				t.Errorf("header contradicts the path (%q present):\n%s", c.notWant, got)
			}
			// A format-arity slip would land here rather than in a tfvars file
			// terraform then fails on confusingly.
			if strings.Contains(got, "%!") {
				t.Errorf("format verb/argument mismatch:\n%s", got)
			}
		})
	}
}

// The gate now accepts either field, so the mutual exclusion has to be enforced
// somewhere else — renderTFVars. Nothing asserted that it still fires.
func TestVPCAndCreateVPCStillMutuallyExclusive(t *testing.T) {
	ws := &config.Workspace{BNK: config.BNKCfg{FLP: &config.BNKFLPCfg{
		Mode: "vsi",
		VSI:  &config.BNKFLPVSICfg{VPC: "r014-abc", CreateVPC: true},
	}}}
	// The gate opens — that is correct and deliberate; it is not the gate's job
	// to police the combination.
	if !StandaloneFLPVSI(ws) {
		t.Error("gate should open when either field is set")
	}
	// The refusal has to come from the render.
	var buf bytes.Buffer
	if err := tf.RenderTFVars(&buf, ws, "", ""); err == nil {
		t.Error("vsi.vpc + create_vpc must still be refused by the tfvars render")
	}
}
