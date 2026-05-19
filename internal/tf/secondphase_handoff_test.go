package tf

// Sprint 16 follow-up — validator Issue 2 hermetic regression test.
//
// Why this file exists
// --------------------
// Validator Issue 1's behavior-parity gate is GREEN and correct, but it
// is structurally blind to Issue 2: no hermetic test exercises a
// workspace that has *already completed the cluster phase*. The live
// `roksbnkctl up` regression — the second (bnk/testing) phase re-creates
// the cluster VPC / transit gateway / client VPC, and IBM Cloud rejects
// the duplicate names — therefore slipped past a green unit suite.
//
// This test closes that blind spot at the unit level. It is the
// cross-agent seam named in RenderTFVarsWithClusterOutputs's doc
// comment: staff owns the renderer fix, the validator owns this test.
// RenderTFVars / WriteTFVars signatures stay frozen (the pre-existing
// internal/tf/vars_test.go pins them), so validator Issue 1's parity
// gate is untouched; this file is additive and edits no pre-existing
// _test.go. The operator-run live verifier is scripts/e2e-phase-handoff.sh
// (NOT a CI job).
//
// Asserted contract (mirrors RenderTFVarsWithClusterOutputs's doc):
//
//   - co == nil               -> output byte-identical to RenderTFVars
//                                 (first/cluster phase unperturbed).
//   - co != nil, co.VPCID==""  -> defensive create path (a half-written
//                                 cluster-outputs.json must not flip
//                                 use_existing_cluster_vpc=true).
//   - co != nil, co.VPCID!=""  -> reuse toggles appended:
//         use_existing_cluster_vpc    = true
//         existing_cluster_vpc_id     = "<co.VPCID>"
//         create_roks_transit_gateway = false
//         testing_create_client_vpc   = false

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// baseWorkspace mirrors vars_test.go's TestRenderTFVars_CreateMode
// inputs so a reviewer sees the reuse-toggle delta against a
// known-good create-path baseline.
func baseWorkspace() *config.Workspace {
	return &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "ca-tor", ResourceGroup: "default"},
		Cluster: config.ClusterCfg{
			Create:           true,
			Name:             "canada-roks",
			OpenShiftVersion: "4.18",
			WorkersPerZone:   2,
		},
	}
}

// TestSecondPhaseTFVars_ReusesExistingClusterVPC is the regression that
// would have caught Issue 2: with cluster outputs present (the cluster
// phase already created + tracked the VPC), the second-phase tfvars
// must reuse rather than re-create.
func TestSecondPhaseTFVars_ReusesExistingClusterVPC(t *testing.T) {
	ws := baseWorkspace()
	co := &config.ClusterOutputs{
		ClusterName: "canada-roks",
		VPCID:       "r038-abc123-vpc-id",
		VPCName:     "canada-roks-vpc",
		Source:      "cluster-up",
	}

	var buf bytes.Buffer
	if err := RenderTFVarsWithClusterOutputs(&buf, ws, co, "", ""); err != nil {
		t.Fatalf("RenderTFVarsWithClusterOutputs: %v", err)
	}
	out := buf.String()

	// The four reuse toggles that stop the second phase from planning
	// duplicate cluster_vpc / transit_gateway / client_vpc creates.
	want := []string{
		`use_existing_cluster_vpc = true`,
		`existing_cluster_vpc_id = "r038-abc123-vpc-id"`,
		`create_roks_transit_gateway = false`,
		`testing_create_client_vpc = false`,
	}
	for _, w := range want {
		if !strings.Contains(out, w) {
			t.Errorf("second-phase tfvars missing reuse toggle %q\noutput:\n%s", w, out)
		}
	}

	// Guard against a naive fix that drops the id but leaves the
	// boolean false — the duplicate-create regression signature.
	if strings.Contains(out, "use_existing_cluster_vpc = false") {
		t.Errorf("second phase still flags use_existing_cluster_vpc = false with cluster outputs present — duplicate-create regression\noutput:\n%s", out)
	}

	// Safety: the API key must never reach a rendered tfvars file.
	if strings.Contains(out, "api_key") {
		t.Errorf("api_key leaked into second-phase tfvars; env-var path is mandatory\noutput:\n%s", out)
	}
}

// TestSecondPhaseTFVars_NoOutputsIsCreatePathParity is the parity half:
// with no cluster outputs (first/cluster phase or fresh workspace) the
// render must be byte-identical to the pre-existing RenderTFVars create
// path. This keeps validator Issue 1's parity gate GREEN — the fix
// changes only the *second* phase.
func TestSecondPhaseTFVars_NoOutputsIsCreatePathParity(t *testing.T) {
	ws := baseWorkspace()

	var got bytes.Buffer
	if err := RenderTFVarsWithClusterOutputs(&got, ws, nil, "", ""); err != nil {
		t.Fatalf("RenderTFVarsWithClusterOutputs(nil): %v", err)
	}

	var want bytes.Buffer
	if err := RenderTFVars(&want, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars baseline: %v", err)
	}

	if got.String() != want.String() {
		t.Errorf("nil cluster-outputs render is NOT byte-identical to RenderTFVars create path — first-phase parity broken\n--- got ---\n%s\n--- want ---\n%s",
			got.String(), want.String())
	}

	for _, toggle := range []string{
		"use_existing_cluster_vpc",
		"existing_cluster_vpc_id",
		"create_roks_transit_gateway",
		"testing_create_client_vpc",
	} {
		if strings.Contains(got.String(), toggle) {
			t.Errorf("first/cluster phase must not emit reuse toggle %q (no cluster outputs yet)\noutput:\n%s", toggle, got.String())
		}
	}
}

// TestSecondPhaseTFVars_EmptyVPCIDIsDefensiveCreatePath guards the
// half-written cluster-outputs.json case: a ClusterOutputs whose VPCID
// is empty must NOT flip use_existing_cluster_vpc = true (an empty
// existing_cluster_vpc_id would fail the submodule's data lookup).
// Absent a usable handoff id, fall back to the create path — same shape
// as the no-outputs parity case.
func TestSecondPhaseTFVars_EmptyVPCIDIsDefensiveCreatePath(t *testing.T) {
	ws := baseWorkspace()
	co := &config.ClusterOutputs{ClusterName: "canada-roks", Source: "cluster-register"} // VPCID == ""

	var got bytes.Buffer
	if err := RenderTFVarsWithClusterOutputs(&got, ws, co, "", ""); err != nil {
		t.Fatalf("RenderTFVarsWithClusterOutputs(empty VPCID): %v", err)
	}

	var want bytes.Buffer
	if err := RenderTFVars(&want, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars baseline: %v", err)
	}

	if got.String() != want.String() {
		t.Errorf("empty VPCID must fall back to the byte-identical create path\n--- got ---\n%s\n--- want ---\n%s",
			got.String(), want.String())
	}
	if strings.Contains(got.String(), "use_existing_cluster_vpc = true") {
		t.Errorf("empty VPCID must not enable VPC reuse (empty existing_cluster_vpc_id would fail the data lookup)\noutput:\n%s", got.String())
	}
}
