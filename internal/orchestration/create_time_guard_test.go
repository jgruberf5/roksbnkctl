package orchestration

import (
	"bytes"
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
