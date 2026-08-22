package orchestration

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// stageGuardWS points $ROKSBNKCTL_HOME at a tempdir and returns a Context whose
// workspace asks for `mode`. If recorded is non-nil it is written as the
// cluster's contract, i.e. what the cluster actually IS.
func stageGuardWS(t *testing.T, mode string, recorded *config.ClusterOutputs) *config.Context {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	const ws = "guard-test-ws"
	if recorded != nil {
		if err := config.WriteClusterOutputs(ws, recorded); err != nil {
			t.Fatalf("staging cluster record: %v", err)
		}
	}
	return &config.Context{
		WorkspaceName: ws,
		Workspace: &config.Workspace{
			Cluster: config.ClusterCfg{NetworkMode: mode},
			BNK:     config.BNKCfg{ManifestVersion: "2.3.0-3.2598.3-0.0.170"},
		},
	}
}

// The whole point of the guard: changing network_mode on a live cluster plans a
// REPLACEMENT that reads like an update. It has to stop before the plan.
func TestNetworkModeChangeIsRefused(t *testing.T) {
	cctx := stageGuardWS(t, config.NetworkModeMultiNIC, &config.ClusterOutputs{
		ClusterName: "prod", ClusterID: "abc123",
		NetworkMode: config.NetworkModeSingleNIC,
	})
	var buf bytes.Buffer
	err := guardCreateTimeSettings(cctx, &buf)
	if err == nil {
		t.Fatal("changing network_mode on an existing cluster must be refused")
	}
	// The message has to say which is which, or the reader cannot tell whether
	// to change the config or build a new cluster.
	for _, want := range []string{"prod", config.NetworkModeSingleNIC, config.NetworkModeMultiNIC} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error does not mention %q: %v", want, err)
		}
	}
}

// A cluster built before network_mode existed records nothing, and an existing
// config asks for nothing. Both default to single-nic, so this must be silent —
// otherwise the guard breaks every deployment that exists today.
func TestUnsetModeAgainstLegacyRecordIsSilent(t *testing.T) {
	cctx := stageGuardWS(t, "", &config.ClusterOutputs{ClusterName: "old", ClusterID: "old1"})
	var buf bytes.Buffer
	if err := guardCreateTimeSettings(cctx, &buf); err != nil {
		t.Fatalf("a legacy cluster with no config change must pass: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("nothing changed, so nothing should be said: %q", buf.String())
	}
}

// Explicit single-nic against a legacy (unrecorded) cluster is the same cluster
// said two ways. Refusing it would punish being explicit.
func TestExplicitSingleNICMatchesLegacyRecord(t *testing.T) {
	cctx := stageGuardWS(t, config.NetworkModeSingleNIC, &config.ClusterOutputs{
		ClusterName: "old", ClusterID: "old1",
	})
	if err := guardCreateTimeSettings(cctx, &bytes.Buffer{}); err != nil {
		t.Fatalf("explicit single-nic must match an unrecorded cluster: %v", err)
	}
}

// No cluster recorded means nothing to contradict. The guard exists to catch a
// contradiction, not to add a new way for a first run to fail.
func TestNoClusterRecordIsSilent(t *testing.T) {
	cctx := stageGuardWS(t, config.NetworkModeMultiNIC, nil)
	var buf bytes.Buffer
	if err := guardCreateTimeSettings(cctx, &buf); err != nil {
		t.Fatalf("a first run has nothing to contradict: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("unexpected output on a first run: %q", buf.String())
	}
}

// vpc_cidr is a PRE-EXISTING contract that was never enforced. Turning it into a
// refusal now would break somebody today, so it warns and refuses later.
func TestVPCCIDRChangeWarnsButDoesNotRefuse(t *testing.T) {
	cctx := stageGuardWS(t, "", &config.ClusterOutputs{
		ClusterName: "prod", ClusterID: "abc", VPCID: "vpc-1",
		VPCCIDR: "10.242.0.0/16",
	})
	cctx.Workspace.Cluster.VPCCIDR = "10.99.0.0/16" // changed after the cluster was built

	var buf bytes.Buffer
	if err := guardCreateTimeSettings(cctx, &buf); err != nil {
		t.Fatalf("vpc_cidr must not refuse yet: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "vpc_cidr") {
		t.Errorf("the warning must name the setting: %q", out)
	}
	// Both values, or the reader cannot tell which one is live.
	for _, want := range []string{"10.242.0.0/16", "10.99.0.0/16"} {
		if !strings.Contains(out, want) {
			t.Errorf("the warning must show %s: %q", want, out)
		}
	}
	if !strings.Contains(out, "future release") {
		t.Errorf("the warning is the deprecation notice; it must say what comes next: %q", out)
	}
}

// The steady state: vpc_cidr was set, the cluster was built from it, nothing has
// changed since. Warning here would fire on every run of a correct workspace,
// which is how warnings become noise people filter out.
func TestVPCCIDRUnchangedIsSilent(t *testing.T) {
	cctx := stageGuardWS(t, "", &config.ClusterOutputs{
		ClusterName: "prod", ClusterID: "abc", VPCID: "vpc-1",
		VPCCIDR: "10.242.0.0/16",
	})
	cctx.Workspace.Cluster.VPCCIDR = "10.242.0.0/16"

	var buf bytes.Buffer
	if err := guardCreateTimeSettings(cctx, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("nothing changed, so nothing should be said: %q", buf.String())
	}
}

// A record with no vpc_cidr is an ADOPTED VPC or a pre-schema-2 file. Nothing is
// known to disagree with, and guessing recreates the false positive.
func TestVPCCIDRAgainstUnrecordedIsSilent(t *testing.T) {
	cctx := stageGuardWS(t, "", &config.ClusterOutputs{
		ClusterName: "prod", ClusterID: "abc", VPCID: "vpc-1",
	})
	cctx.Workspace.Cluster.VPCCIDR = "10.99.0.0/16"

	var buf bytes.Buffer
	if err := guardCreateTimeSettings(cctx, &buf); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("an unrecorded cidr cannot disagree: %q", buf.String())
	}
}

// The matrix check is what turns an unsupported pairing into a plan-time
// refusal instead of a failure against a real cluster.
func TestSupportedCombinationGuard(t *testing.T) {
	// 2.3 cannot express multi-nic.
	cctx := stageGuardWS(t, config.NetworkModeMultiNIC, &config.ClusterOutputs{
		ClusterName: "m", ClusterID: "m1",
		SchemaVersion: config.ContractSchemaVersion,
		NetworkMode:   config.NetworkModeMultiNIC,
	})
	if err := guardSupportedCombination(cctx, &bytes.Buffer{}); err == nil {
		t.Error("2.3 driving a multi-nic cluster must be refused")
	}

	// Nor can 2.4, on the evidence we have — the 2.4 EA install guide is
	// single-NIC throughout (docs/prd/18-BNK-2-4-SUPPORT.md §3.1). The matrix row
	// used to claim multi-NIC on an expectation, so this guard approved a plan
	// nothing backed.
	cctx.Workspace.BNK.ManifestVersion = "2.4.0-1.2.3-0.0.1"
	if err := guardSupportedCombination(cctx, &bytes.Buffer{}); err == nil {
		t.Error("no shipped BNK line expresses multi-nic; 2.4 driving one must be refused")
	}

	// 2.4 must still drive a single-nic cluster, including one created before
	// multi-NIC existed — otherwise adopting 2.4 would force a rebuild.
	single := stageGuardWS(t, config.NetworkModeSingleNIC, &config.ClusterOutputs{
		ClusterName: "s", ClusterID: "s1",
		SchemaVersion: config.ContractSchemaVersion,
		NetworkMode:   config.NetworkModeSingleNIC,
	})
	single.Workspace.BNK.ManifestVersion = "2.4.0-1.2.3-0.0.1"
	if err := guardSupportedCombination(single, &bytes.Buffer{}); err != nil {
		t.Errorf("2.4 + single-nic must be supported: %v", err)
	}

	legacy := stageGuardWS(t, "", &config.ClusterOutputs{ClusterName: "old", ClusterID: "o1"})
	legacy.Workspace.BNK.ManifestVersion = "2.4.0-1.2.3-0.0.1"
	if err := guardSupportedCombination(legacy, &bytes.Buffer{}); err != nil {
		t.Errorf("2.4 must drive a cluster created before multi-nic existed: %v", err)
	}
}

// A manifest version we cannot parse must not silently pick a line: that would
// choose a terraform layer and a CRD set on a guess.
func TestUnparseableManifestIsRefused(t *testing.T) {
	cctx := stageGuardWS(t, "", nil)
	cctx.Workspace.BNK.ManifestVersion = "not-a-version"
	if err := guardSupportedCombination(cctx, &bytes.Buffer{}); err == nil {
		t.Error("an unparseable manifest version must be refused, not guessed")
	}
}

// Nil inputs must degrade, not panic: these run early, before much is resolved.
func TestGuardsTolerateMissingWorkspace(t *testing.T) {
	if err := guardCreateTimeSettings(nil, &bytes.Buffer{}); err != nil {
		t.Errorf("nil context: %v", err)
	}
	if err := guardSupportedCombination(&config.Context{}, &bytes.Buffer{}); err != nil {
		t.Errorf("context with no workspace: %v", err)
	}
}

// The rule lives in config so both entry points give the same answer. Check the
// bnk path actually reaches it — an unknown mode must not be planned anywhere.
func TestInvalidNetworkModeIsRefusedOnTheBNKPath(t *testing.T) {
	cctx := stageGuardWS(t, "dual-nic", nil)
	if err := guardCreateTimeSettings(cctx, &bytes.Buffer{}); err == nil {
		t.Error("an unknown network mode must be refused before planning")
	}
}

// A release newer than this binary is the ORDINARY way to meet an unknown line —
// the matrix ships inside the binary, and the BNK Forge modules pin the runner
// image by digest, so "use a newer build" is not available to someone choosing a
// BNK release. Refusing would make every build refuse every release that ships
// after it.
func TestUnknownBNKLineWarnsAndProceeds(t *testing.T) {
	cctx := stageGuardWS(t, "", &config.ClusterOutputs{ClusterName: "c", ClusterID: "c1"})
	cctx.Workspace.BNK.ManifestVersion = "9.9.0-1.2.3-0.0.1"

	var buf bytes.Buffer
	if err := guardSupportedCombination(cctx, &buf); err != nil {
		t.Fatalf("an unknown line is missing information, not a known incompatibility: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "9.9") {
		t.Errorf("the warning must name the line it does not know: %q", out)
	}
	// A silent proceed is worse than either alternative — it claims verification
	// that did not happen.
	if out == "" {
		t.Error("proceeding silently claims a check that was never made")
	}
}

// The distinction that keeps BNK Forge working: silence is not an assertion.
// Forge regenerates config.yaml per step from a curated env list, so a mode the
// cluster-creating step set is simply ABSENT when the installing step runs.
// Only an EXPLICIT contradiction may refuse.
func TestUnsetModeDefersToTheRecord(t *testing.T) {
	cctx := stageGuardWS(t, "", &config.ClusterOutputs{
		ClusterName: "multi", ClusterID: "m1",
		SchemaVersion: config.ContractSchemaVersion,
		NetworkMode:   config.NetworkModeMultiNIC,
	})
	if err := guardCreateTimeSettings(cctx, &bytes.Buffer{}); err != nil {
		t.Fatalf("an unset network_mode cannot contradict the record: %v", err)
	}

	// But an explicit one still can — that is the case worth refusing.
	cctx.Workspace.Cluster.NetworkMode = config.NetworkModeSingleNIC
	if err := guardCreateTimeSettings(cctx, &bytes.Buffer{}); err == nil {
		t.Error("an explicit single-nic against a multi-nic cluster must still be refused")
	}
}

// stageBNKInstall records a BNK phase applied-tfvars snapshot for ws, the way a
// real apply would: by handing WriteAppliedTFVars a var-file to render from.
// Round-tripping through the real writer is the point — a hand-written snapshot
// would pass even if the writer stopped recording the namespaces.
func stageBNKInstall(t *testing.T, ws, flo, utils string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "terraform.tfvars")
	body := fmt.Sprintf("flo_namespace = %q\nflo_utils_namespace = %q\n", flo, utils)
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("staging var-file: %v", err)
	}
	if err := config.WriteAppliedTFVars(ws, bnkPhaseSnapshotLabel, []string{src}); err != nil {
		t.Fatalf("staging applied tfvars: %v", err)
	}
	stageBNKStatePresent(t, ws)
}

// stageBNKInstallLine records a snapshot whose bnk_line is the line the
// workspace was installed on, which is what CheckLineChange compares against.
func stageBNKInstallLine(t *testing.T, ws, line string) {
	t.Helper()
	src := filepath.Join(t.TempDir(), "terraform.tfvars")
	body := fmt.Sprintf("bnk_line = %q\nflo_namespace = \"f5-bnk\"\nflo_utils_namespace = \"f5-utils\"\n", line)
	if err := os.WriteFile(src, []byte(body), 0o600); err != nil {
		t.Fatalf("staging var-file: %v", err)
	}
	if err := config.WriteAppliedTFVars(ws, bnkPhaseSnapshotLabel, []string{src}); err != nil {
		t.Fatalf("staging applied tfvars: %v", err)
	}
	stageBNKStatePresent(t, ws)
}

// stageBNKStatePresent writes a BNK state file with one managed resource, which
// is what config.DetectPresence reads. The snapshot alone does NOT mean an
// install exists: Destroy deliberately leaves the snapshot behind, so a
// workspace that has been torn down still has one. Tests that mean "installed"
// have to say so here.
func stageBNKStatePresent(t *testing.T, ws string) {
	t.Helper()
	dir, err := config.WorkspaceStateDir(ws)
	if err != nil {
		t.Fatalf("state dir: %v", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir state dir: %v", err)
	}
	const body = `{"version":4,"resources":[{"mode":"managed","type":"kubectl_manifest","name":"x","instances":[{}]}]}`
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(body), 0o600); err != nil {
		t.Fatalf("staging bnk state: %v", err)
	}
}

// stageBNKTornDown reproduces what `bnk down` actually leaves: an EMPTY state
// and the applied-tfvars snapshot still in place. Both live in the same
// directory, so removing the directory would delete the snapshot too — and then
// the guard skips because it cannot read the snapshot, which is not the
// behaviour under test and would make the test pass for the wrong reason.
func stageBNKTornDown(t *testing.T, ws string) {
	t.Helper()
	dir, err := config.WorkspaceStateDir(ws)
	if err != nil {
		t.Fatalf("state dir: %v", err)
	}
	const empty = `{"version":4,"resources":[]}`
	if err := os.WriteFile(filepath.Join(dir, "terraform.tfstate"), []byte(empty), 0o600); err != nil {
		t.Fatalf("emptying bnk state: %v", err)
	}
	// The snapshot must survive, or the test proves nothing.
	if _, err := config.ReadAppliedTFVarsReplayAssignments(ws, bnkPhaseSnapshotLabel); err != nil {
		t.Fatalf("snapshot must still be readable after teardown, else this test is vacuous: %v", err)
	}
}

// guardWSWithNamespaces stages a workspace asking for the given namespaces, with
// a cluster already recorded so nothing else in the guard short-circuits.
func guardWSWithNamespaces(t *testing.T, flo, utils string) *config.Context {
	t.Helper()
	cctx := stageGuardWS(t, "", &config.ClusterOutputs{ClusterName: "c", ClusterID: "c1"})
	cctx.Workspace.BNK.FLONamespace = flo
	cctx.Workspace.BNK.FLOUtilsNamespace = utils
	return cctx
}

// The destructive case, end to end through the guard the BNK phase actually
// calls: collapsing a two-namespace install deletes the utils namespace and
// every shared component in it.
func TestCollapsingNamespacesOnAnInstalledWorkspaceIsRefused(t *testing.T) {
	cctx := guardWSWithNamespaces(t, "f5-bnk", "f5-bnk")
	stageBNKInstall(t, cctx.WorkspaceName, "f5-bnk", "f5-utils")

	var buf bytes.Buffer
	err := guardCreateTimeSettings(cctx, &buf)
	if err == nil {
		t.Fatal("collapsing the namespaces on an installed workspace must be refused")
	}
	if !strings.Contains(err.Error(), "f5-utils") {
		t.Errorf("refusal must name the namespace that would be deleted: %v", err)
	}
}

// One namespace on a workspace that has never applied is the supported way to
// get one namespace. The guard must not stand in front of it.
func TestCollapsedNamespacesOnAFreshWorkspacePass(t *testing.T) {
	cctx := guardWSWithNamespaces(t, "f5-bnk", "f5-bnk")
	var buf bytes.Buffer
	if err := guardCreateTimeSettings(cctx, &buf); err != nil {
		t.Fatalf("a first install may choose one namespace: %v", err)
	}
}

// Steady state for a single-namespace customer: installed collapsed, re-running
// unchanged. Refusing here would make the feature unusable after day one.
func TestUnchangedCollapsedNamespacesConverge(t *testing.T) {
	cctx := guardWSWithNamespaces(t, "f5-bnk", "f5-bnk")
	stageBNKInstall(t, cctx.WorkspaceName, "f5-bnk", "f5-bnk")

	var buf bytes.Buffer
	if err := guardCreateTimeSettings(cctx, &buf); err != nil {
		t.Fatalf("re-applying an unchanged single-namespace install must converge: %v", err)
	}
}

// The default two-namespace install, re-run with the fields left unset. This is
// the overwhelmingly common path and it must stay silent.
func TestUnchangedDefaultNamespacesConverge(t *testing.T) {
	cctx := guardWSWithNamespaces(t, "", "")
	stageBNKInstall(t, cctx.WorkspaceName, config.DefaultFLONamespace, config.DefaultFLOUtilsNamespace)

	var buf bytes.Buffer
	if err := guardCreateTimeSettings(cctx, &buf); err != nil {
		t.Fatalf("an unchanged default install must converge: %v", err)
	}
}

// After `bnk down` the snapshot still describes the install that WAS there, so
// reading it alone refuses a legitimate reinstall: a workspace is told to move
// to a new one to change its line, and told that collapsing its namespaces
// would delete a namespace that no longer exists. Nothing is installed, so
// there is nothing for either guard to protect.
func TestAfterTeardownTheCreateTimeGuardsDoNotRefuseAReinstall(t *testing.T) {
	t.Run("namespace topology", func(t *testing.T) {
		cctx := guardWSWithNamespaces(t, "f5-bnk", "f5-bnk")
		stageBNKInstall(t, cctx.WorkspaceName, "f5-bnk", "f5-utils")
		stageBNKTornDown(t, cctx.WorkspaceName)

		var buf bytes.Buffer
		if err := guardCreateTimeSettings(cctx, &buf); err != nil {
			t.Errorf("collapsing namespaces on a torn-down workspace must be allowed: %v", err)
		}
	})

	t.Run("release line", func(t *testing.T) {
		cctx := guardWSWithNamespaces(t, "f5-bnk", "f5-utils")
		cctx.Workspace.BNK.ManifestVersion = "2.4.0-EA"
		stageBNKInstallLine(t, cctx.WorkspaceName, "2.3")
		stageBNKTornDown(t, cctx.WorkspaceName)

		var buf bytes.Buffer
		if err := guardCreateTimeSettings(cctx, &buf); err != nil {
			t.Errorf("moving line on a torn-down workspace must be allowed: %v", err)
		}
	})
}
