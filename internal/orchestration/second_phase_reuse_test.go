package orchestration

// Additive Issue 2 (round 2) regression — proves the corrected,
// architectural phase-handoff: when a workspace already has a
// cluster-outputs.json the second/bnk phase must layer a forced override
// that turns OFF every cluster-shared CREATE (so module.roks_cluster +
// module.testing resolve the cluster by data source instead of
// re-provisioning the network the cluster phase already built), and when
// there is none it must add no override at all (fresh / legacy
// single-state / cluster-only parity). No pre-existing _test.go is
// edited; this file is new.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

func TestLoadReuseClusterOutputs_MissingIsNoOverride(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	co, err := loadReuseClusterOutputs("ws-no-cluster")
	if err != nil {
		t.Fatalf("loadReuseClusterOutputs (missing): %v", err)
	}
	if co != nil {
		t.Fatalf("expected nil ClusterOutputs for a workspace with no cluster-outputs.json, got %+v", co)
	}
}

func TestLoadReuseClusterOutputs_PresentIsHandoff(t *testing.T) {
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	want := &config.ClusterOutputs{
		ClusterName: "canada-roks",
		ClusterID:   "abc-cluster-id",
		VPCID:       "r038-ef6305af-vpc",
		Source:      "cluster-up",
	}
	if err := config.WriteClusterOutputs("canada-roks", want); err != nil {
		t.Fatalf("WriteClusterOutputs: %v", err)
	}
	co, err := loadReuseClusterOutputs("canada-roks")
	if err != nil {
		t.Fatalf("loadReuseClusterOutputs (present): %v", err)
	}
	if co == nil || co.VPCID != want.VPCID {
		t.Fatalf("expected handoff ClusterOutputs with VPCID %q, got %+v", want.VPCID, co)
	}
}

// TestWriteBnkPhaseOverride_TurnsAllClusterSharedOff is the core
// architectural assertion: the override the second phase layers must
// force EVERY cluster-shared create off — not just the VPC. This is the
// regression the round-1 per-toggle model failed (live run-id
// 20260519-181511): cluster subnets / public gateways / transit gateway
// / client VPC / jumphost subnets / jumphost SG were all re-created.
func TestWriteBnkPhaseOverride_TurnsAllClusterSharedOff(t *testing.T) {
	dir := t.TempDir()

	co := &config.ClusterOutputs{
		ClusterName: "canada-roks",
		ClusterID:   "crt-cluster-id",
		VPCID:       "r038-ef6305af-vpc",
		Source:      "cluster-up",
	}
	p, err := writeBnkPhaseOverrideAt(dir, co)
	if err != nil {
		t.Fatalf("writeBnkPhaseOverrideAt: %v", err)
	}
	if filepath.Base(p) != bnkPhaseOverrideFile {
		t.Fatalf("override path %q must end in %q", p, bnkPhaseOverrideFile)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading override: %v", err)
	}
	got := string(body)

	// Every cluster-shared create must be forced off. create_cluster
	// false → no cluster subnets / public gateways / cluster resource;
	// use_existing_cluster_vpc true → no ibm_is_vpc.cluster_vpc;
	// create_roks_transit_gateway false → no ibm_tg_gateway; the three
	// testing_create_* false → no client VPC / jumphost subnets / SG.
	want := []string{
		`create_roks_cluster = false`,
		`roks_cluster_id_or_name = "crt-cluster-id"`,
		`use_existing_cluster_vpc = true`,
		`existing_cluster_vpc_id = "r038-ef6305af-vpc"`,
		`create_roks_transit_gateway = false`,
		`testing_create_cluster_jumphosts = false`,
		`testing_create_tgw_jumphost = false`,
		`testing_create_client_vpc = false`,
	}
	for _, s := range want {
		if !strings.Contains(got, s) {
			t.Errorf("bnk-phase override missing forced setting %q\n--- override ---\n%s", s, got)
		}
	}

	// The duplicate-create regression signature must NOT survive: the
	// second phase must never plan to create the cluster or its VPC.
	for _, bad := range []string{
		`create_roks_cluster = true`,
		`use_existing_cluster_vpc = false`,
	} {
		if strings.Contains(got, bad) {
			t.Errorf("override still carries the duplicate-create signature %q\n--- override ---\n%s", bad, got)
		}
	}

	// The API key must never reach a rendered var-file.
	if strings.Contains(got, "api_key") {
		t.Errorf("api_key leaked into the bnk-phase override; env-var path is mandatory\n--- override ---\n%s", got)
	}
}

// TestClusterIdentity_PrefersIDThenName documents the data-lookup
// identity selection (data.ibm_container_vpc_cluster.existing_cluster
// accepts an id or a name for `name`).
func TestClusterIdentity_PrefersIDThenName(t *testing.T) {
	if got := clusterIdentity(&config.ClusterOutputs{ClusterID: "id-1", ClusterName: "n-1"}); got != "id-1" {
		t.Errorf("clusterIdentity: want id-1, got %q", got)
	}
	if got := clusterIdentity(&config.ClusterOutputs{ClusterName: "n-1"}); got != "n-1" {
		t.Errorf("clusterIdentity: want n-1 (no id), got %q", got)
	}
}
