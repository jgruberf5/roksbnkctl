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

// TestWriteBnkPhaseOverride_SuppressesRegistryCOSAndJumphostKey is the
// Sprint 23 regression: the 2026-05-27 live evidence
// (issues/issue_sprint23_staff.md) shows two cluster-shared resources
// landing as MANAGED entries in the trial state on every Split-shape
// `roksbnkctl up` —
// `module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`
// and `module.testing.tls_private_key.jumphost_shared_key`. The fix has
// two halves and this test pins the override half:
//
//   - the override must explicitly force create_roks_registry_cos_instance =
//     false, so the inner cluster module's
//     `count = var.create_cluster && var.create_cos_instance ? 1 : 0`
//     evaluates to 0 from BOTH halves of the &&, not just the
//     create_roks_cluster=false half (defense in depth against any code
//     path — refresh-on-name, partial-apply carry-over — that might leak
//     the cluster-phase COS into trial state). Pre-Sprint-23 the override
//     omitted this flag → tfvars default `true` flowed through.
//
// The companion half — gating tls_private_key.jumphost_shared_key on
// (testing_create_cluster_jumphosts || testing_create_tgw_jumphost) so
// the resource itself flips to count=0 when the existing
// testing_create_*_jumphost=false override lines fire — lives in
// terraform/modules/testing/main.tf and is covered by the
// `roksbnkctl !` live-verify recipe documented in the Sprint 23 closure.
//
// Additive — no pre-existing test case is edited.
func TestWriteBnkPhaseOverride_SuppressesRegistryCOSAndJumphostKey(t *testing.T) {
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
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading override: %v", err)
	}
	got := string(body)

	// The Sprint 23 addition: the override must carry the explicit
	// registry-COS gate, not lean on the create_roks_cluster=false
	// half of the inner count expression alone.
	const wantCOS = `create_roks_registry_cos_instance = false`
	if !strings.Contains(got, wantCOS) {
		t.Errorf("bnk-phase override missing Sprint 23 gate %q — registry COS will leak into trial state\n--- override ---\n%s", wantCOS, got)
	}

	// The Sprint 23 companion: the two testing_create_* gates that
	// drive the new tls_private_key.jumphost_shared_key count gate
	// (in terraform/modules/testing/main.tf) must BOTH be in the
	// override. Already in the round-2 contract — re-asserted here so
	// any future regression that drops them also fails THIS test,
	// keeping the jumphost-key-leak signature locked.
	for _, want := range []string{
		`testing_create_cluster_jumphosts = false`,
		`testing_create_tgw_jumphost = false`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("bnk-phase override missing jumphost-key-gate driver %q\n--- override ---\n%s", want, got)
		}
	}

	// And: the override must NOT carry the true-form of the registry-COS
	// flag (a defensive guard — if someone ever re-derives the override
	// from upstream defaults the test catches the regression).
	if strings.Contains(got, `create_roks_registry_cos_instance = true`) {
		t.Errorf("override still carries the leak signature `create_roks_registry_cos_instance = true`\n--- override ---\n%s", got)
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

// TestWriteBnkPhaseOverride_Sprint23ByteIdenticalBlock is the Sprint 23
// validator-side additive regression. It complements the staff-side
// TestWriteBnkPhaseOverride_SuppressesRegistryCOSAndJumphostKey by
// pinning the BYTE-IDENTICAL ordered block of forced tfvars lines —
// not just presence. The staff test asserts the new
// `create_roks_registry_cos_instance = false` gate is somewhere in the
// override; this test additionally pins its exact ADJACENCY: it must
// sit between `create_roks_transit_gateway = false` and
// `testing_create_cluster_jumphosts = false`, in that order, with no
// intervening lines.
//
// Why the stronger assertion: pre-Sprint-23 the override was the round-2
// 8-line block (no registry-COS gate). The Sprint 23 fix inserts ONE
// new line at a specific position. A future refactor that re-orders the
// override (or accidentally drops the new gate while leaving its
// neighbours intact) would slip past a per-line Contains check but is
// caught here. Parity discipline (per the validator brief) is
// byte-identical match on the new lines, not a regex.
//
// Live-verify recipe (DO NOT execute in tests — the integrator runs it
// via `!`): see issues/issue_sprint23_validator.md §"Live-verify recipe".
// On a fresh Split-shape workspace, after a successful `up`, the trial
// state file must carry ZERO managed resources under
// `module.roks_cluster.*` or `module.testing.*` prefixes (jq filter in
// the closure). If non-empty after this fix lands, the hermetic test
// passing is necessary but not sufficient and the integrator must NOT
// run `roksbnkctl bnk down` (resource-damage hazard); full
// `roksbnkctl down` + re-investigate.
//
// Additive — no pre-existing test case is edited.
func TestWriteBnkPhaseOverride_Sprint23ByteIdenticalBlock(t *testing.T) {
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
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading override: %v", err)
	}
	got := string(body)

	// Byte-identical ordered block. The full 10-line Sprint-23-round-2
	// forced tfvars sequence, in exact order, with single-LF separators.
	// Any re-ordering, any inserted whitespace, any dropped line fails
	// the match. The new `deploy_cert_manager = false` line lands between
	// `create_roks_registry_cos_instance = false` and the testing_*
	// block; pre-round-2 was 9 lines.
	const wantBlock = "create_roks_cluster = false\n" +
		"roks_cluster_id_or_name = \"crt-cluster-id\"\n" +
		"use_existing_cluster_vpc = true\n" +
		"existing_cluster_vpc_id = \"r038-ef6305af-vpc\"\n" +
		"create_roks_transit_gateway = false\n" +
		"create_roks_registry_cos_instance = false\n" +
		"deploy_cert_manager = true\n" +
		"testing_create_cluster_jumphosts = false\n" +
		"testing_create_tgw_jumphost = false\n" +
		"testing_create_client_vpc = false\n"
	if !strings.Contains(got, wantBlock) {
		t.Fatalf("bnk-phase override is missing the Sprint 23 byte-identical block.\n--- want block ---\n%s\n--- got file ---\n%s",
			wantBlock, got)
	}

	// Pin the exact adjacency of the two Sprint 23 gates:
	//   create_roks_registry_cos_instance = false  (round 1)
	//   deploy_cert_manager               = false  (round 2 — the
	//     cert_manager leak surfaced by the 2026-05-27 canada-roks live
	//     verify, when the round-1 fix shipped without it)
	// Both sit between the transit-gateway gate and the testing_* block.
	// A reorder regression would pass the Contains() check above (block
	// still appears as a substring) only if the WHOLE block survives —
	// but if a future edit splits the block, this targeted check still
	// fires.
	const wantNeighbours = "create_roks_transit_gateway = false\n" +
		"create_roks_registry_cos_instance = false\n" +
		"deploy_cert_manager = true\n" +
		"testing_create_cluster_jumphosts = false\n"
	if !strings.Contains(got, wantNeighbours) {
		t.Errorf("Sprint 23 round-1+2 gates are not adjacent to their required neighbours.\n--- want ---\n%s\n--- got ---\n%s",
			wantNeighbours, got)
	}

	// Defence in depth. create_roks_registry_cos_instance must stay false
	// (Sprint 23 round-1 leak guard — the cluster phase owns the registry
	// COS). deploy_cert_manager must stay TRUE here: Sprint 27 moved
	// cert-manager INTO the bnk phase (it's provider-based and can't deploy
	// during cluster creation), so an active `deploy_cert_manager = false`
	// in this override is now the regression. (Header comments legitimately
	// mention these flags by name; an active assignment is `= <value>` with
	// no leading `#`, so the substring forms below only match real lines.)
	for _, leak := range []string{
		`create_roks_registry_cos_instance = true`,
		`create_roks_registry_cos_instance=true`,
		"\ndeploy_cert_manager = false\n",
		"\ndeploy_cert_manager=false\n",
	} {
		if strings.Contains(got, leak) {
			t.Errorf("override carries a phase-gating regression %q\n--- override ---\n%s", leak, got)
		}
	}
}

// ── Sprint 28: testing-phase override byte-exact pins ───────────────────

// TestWriteTestingPhaseOverride_ByteExactBlock_WithTGW pins the EXACT bytes
// the new testing-phase override emits when the cluster handoff carries a
// transit-gateway name. Mirrors the byte-exact discipline of the Sprint 23
// bnk-override test above: any reorder, any whitespace drift, any dropped or
// added line fails the match.
//
// Per the architect's §2a the override sets ONLY the architectural-off flags
// (create_roks_cluster / deploy_bnk / deploy_cert_manager /
// create_roks_transit_gateway / create_roks_registry_cos_instance = false) +
// the VPC/TGW reuse inputs. It must NOT pin testing_create_* — those flow from
// the user's render so "TGW jumphost only, no cluster jumphosts" keeps working.
func TestWriteTestingPhaseOverride_ByteExactBlock_WithTGW(t *testing.T) {
	dir := t.TempDir()
	co := &config.ClusterOutputs{
		ClusterName:        "canada-roks",
		ClusterID:          "crt-cluster-id",
		VPCID:              "r038-ef6305af-vpc",
		TransitGatewayName: "canada-roks-tgw",
		Source:             "cluster-up",
	}
	p, err := writeTestingPhaseOverrideAt(dir, co)
	if err != nil {
		t.Fatalf("writeTestingPhaseOverrideAt: %v", err)
	}
	if filepath.Base(p) != testingPhaseOverrideFile {
		t.Fatalf("override path %q must end in %q", p, testingPhaseOverrideFile)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading override: %v", err)
	}
	got := string(body)

	// The full forced block, byte-identical, in exact order with single-LF
	// separators. clusterIdentity prefers the ID, so roks_cluster_id_or_name
	// is the cluster ID. The TGW name is carried by the DECLARED root var
	// roks_transit_gateway_name (testing_transit_gateway_name is a module input).
	const wantBlock = "create_roks_cluster = false\n" +
		"roks_cluster_id_or_name = \"crt-cluster-id\"\n" +
		"use_existing_cluster_vpc = true\n" +
		"existing_cluster_vpc_id = \"r038-ef6305af-vpc\"\n" +
		"create_roks_transit_gateway = false\n" +
		"create_roks_registry_cos_instance = false\n" +
		"deploy_bnk = false\n" +
		"deploy_cert_manager = false\n" +
		"roks_transit_gateway_name = \"canada-roks-tgw\"\n"
	if !strings.Contains(got, wantBlock) {
		t.Fatalf("testing-phase override missing the byte-identical block.\n--- want block ---\n%s\n--- got file ---\n%s", wantBlock, got)
	}

	// The override must NOT pin any testing_create_* toggle (those flow from
	// the user's render — §2a). A literal active assignment is `= <value>`
	// with no leading `#`; assert none of the three appear in any form.
	for _, forbidden := range []string{
		"testing_create_tgw_jumphost",
		"testing_create_cluster_jumphosts",
		"testing_create_client_vpc",
	} {
		if strings.Contains(got, forbidden) {
			t.Errorf("testing-phase override must NOT pin %q (it flows from the user render); found it\n--- override ---\n%s", forbidden, got)
		}
	}

	// And it must never re-enable the cluster/BNK it is suppressing, nor leak
	// the API key.
	for _, bad := range []string{
		"create_roks_cluster = true",
		"deploy_bnk = true",
		"use_existing_cluster_vpc = false",
		"api_key",
	} {
		if strings.Contains(got, bad) {
			t.Errorf("testing-phase override carries forbidden content %q\n--- override ---\n%s", bad, got)
		}
	}
}

// TestWriteTestingPhaseOverride_OmitsTGWLineWhenNameEmpty pins §2a's
// conditional: when the handoff has no transit_gateway_name (e.g. a
// `cluster register` with no TGW output), the testing_transit_gateway_name
// line is OMITTED entirely so the normal config.yaml render supplies it. The
// file then ends right after `deploy_cert_manager = false`.
func TestWriteTestingPhaseOverride_OmitsTGWLineWhenNameEmpty(t *testing.T) {
	dir := t.TempDir()
	co := &config.ClusterOutputs{
		ClusterName: "canada-roks",
		ClusterID:   "crt-cluster-id",
		VPCID:       "r038-ef6305af-vpc",
		// TransitGatewayName intentionally empty.
		Source: "cluster-register",
	}
	p, err := writeTestingPhaseOverrideAt(dir, co)
	if err != nil {
		t.Fatalf("writeTestingPhaseOverrideAt: %v", err)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading override: %v", err)
	}
	got := string(body)

	if strings.Contains(got, "roks_transit_gateway_name") {
		t.Errorf("empty TGW name must OMIT the roks_transit_gateway_name line\n--- override ---\n%s", got)
	}
	// The architectural-off block + reuse inputs are still all present and the
	// file ends at deploy_cert_manager = false (no dangling TGW line).
	const wantTail = "deploy_bnk = false\n" +
		"deploy_cert_manager = false\n"
	if !strings.HasSuffix(got, wantTail) {
		t.Errorf("override (no TGW) must end at deploy_cert_manager = false\n--- override ---\n%q", got)
	}
	// VPC reuse inputs still pinned.
	for _, want := range []string{
		`existing_cluster_vpc_id = "r038-ef6305af-vpc"`,
		`cluster_vpc_id = "r038-ef6305af-vpc"`,
		`roks_cluster_id_or_name = "crt-cluster-id"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("override (no TGW) missing reuse input %q\n--- override ---\n%s", want, got)
		}
	}
}

// TestWriteBnkPhaseOverride_Sprint28ByteUnchanged re-pins (Sprint 28 frame)
// that the bnk-phase override's forced VALUE block is BYTE-UNCHANGED by the
// three-phase split — only its header comment was updated to note Testing is
// now its own phase. This is the validator's standing byte-stability gate: the
// Testing split must not perturb the BNK override the live BNK phase depends
// on. Identical assertion content to the Sprint 23 block test, restated here
// as the Sprint 28 stability anchor.
func TestWriteBnkPhaseOverride_Sprint28ByteUnchanged(t *testing.T) {
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
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading override: %v", err)
	}
	got := string(body)

	const wantBlock = "create_roks_cluster = false\n" +
		"roks_cluster_id_or_name = \"crt-cluster-id\"\n" +
		"use_existing_cluster_vpc = true\n" +
		"existing_cluster_vpc_id = \"r038-ef6305af-vpc\"\n" +
		"create_roks_transit_gateway = false\n" +
		"create_roks_registry_cos_instance = false\n" +
		"deploy_cert_manager = true\n" +
		"testing_create_cluster_jumphosts = false\n" +
		"testing_create_tgw_jumphost = false\n" +
		"testing_create_client_vpc = false\n"
	if !strings.Contains(got, wantBlock) {
		t.Fatalf("bnk-phase override forced block drifted under the Sprint 28 split (must be byte-unchanged).\n--- want ---\n%s\n--- got ---\n%s", wantBlock, got)
	}
}

// TestWriteGatewayPhaseOverride_ForcedBlock asserts the gateway-phase override
// forces every OTHER phase's creation off and deploy_gateway on, reusing the
// cluster VPC from cluster-outputs.json.
func TestWriteFLPPhaseOverride_ForcedBlock(t *testing.T) {
	dir := t.TempDir()
	co := &config.ClusterOutputs{
		ClusterID: "crt-cluster-id",
		VPCID:     "r038-ef6305af-vpc",
		Source:    "cluster-up",
	}
	p, err := writeFLPPhaseOverrideAt(dir, co)
	if err != nil {
		t.Fatalf("writeFLPPhaseOverrideAt: %v", err)
	}
	if filepath.Base(p) != flpPhaseOverrideFile {
		t.Fatalf("override path %q must end in %q", p, flpPhaseOverrideFile)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading override: %v", err)
	}
	got := string(body)
	// FLP phase forces every OTHER phase off (incl. deploy_gateway) and deploy_flp on.
	for _, want := range []string{
		"create_roks_cluster = false\n",
		"roks_cluster_id_or_name = \"crt-cluster-id\"\n",
		"existing_cluster_vpc_id = \"r038-ef6305af-vpc\"\n",
		"deploy_bnk = false\n",
		"deploy_cert_manager = false\n",
		"testing_create_tgw_jumphost = false\n",
		"testing_create_cluster_jumphosts = false\n",
		"testing_create_client_vpc = false\n",
		"deploy_gateway = false\n",
		"deploy_flp = true\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("flp override missing %q\n--- got ---\n%s", want, got)
		}
	}
}

func TestWriteGatewayPhaseOverride_ForcedBlock(t *testing.T) {
	dir := t.TempDir()
	co := &config.ClusterOutputs{
		ClusterID: "crt-cluster-id",
		VPCID:     "r038-ef6305af-vpc",
		Source:    "cluster-up",
	}
	p, err := writeGatewayPhaseOverrideAt(dir, co)
	if err != nil {
		t.Fatalf("writeGatewayPhaseOverrideAt: %v", err)
	}
	if filepath.Base(p) != gatewayPhaseOverrideFile {
		t.Fatalf("override path %q must end in %q", p, gatewayPhaseOverrideFile)
	}
	body, err := os.ReadFile(p)
	if err != nil {
		t.Fatalf("reading override: %v", err)
	}
	got := string(body)
	for _, want := range []string{
		"create_roks_cluster = false\n",
		"roks_cluster_id_or_name = \"crt-cluster-id\"\n",
		"existing_cluster_vpc_id = \"r038-ef6305af-vpc\"\n",
		"cluster_vpc_id = \"r038-ef6305af-vpc\"\n",
		"deploy_bnk = false\n",
		"deploy_cert_manager = false\n",
		"testing_create_tgw_jumphost = false\n",
		"testing_create_cluster_jumphosts = false\n",
		"testing_create_client_vpc = false\n",
		"deploy_gateway = true\n",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("gateway override missing %q\n--- got ---\n%s", want, got)
		}
	}
}
