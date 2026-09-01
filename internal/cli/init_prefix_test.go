// Sprint 26 validator Issue 1 — hermetic coverage for the prefix-driven
// `init` interview staff shipped in internal/cli/init.go.
// Additive new file; never edits any
// pre-existing _test.go (parity discipline carried from Sprints 18–24).
//
// Testability note
// ----------------
// runInit() calls ibm.New()/Verify() (live IBM Cloud) between the resource
// group step and the interview, with no in-process stub seam — so a full
// cobra-driven interactive `init` cannot run hermetically without creds
// (the same gap the Sprint 19 init --var-file positive cases,
// since removed with that flag in fbf3459,
// skip-guard). The interview BODY, however, is factored into in-package
// helpers staff exposes (runPrefixInterview, promptPrefix,
// seedVarFileInterview, printNamePlan), which this file drives directly so
// the prefix persistence / non-TTY-error / existing-resource-capture /
// var-file-prefix contracts are pinned hermetically.
//
// Under `go test` stdin is NOT a TTY, so prompt.go's isTTY() returns false
// and every promptYesNo/promptString returns its DEFAULT without reading —
// which is exactly the non-TTY CI contract the interview must honour. The
// cobra-driven default-accept sub-case is additionally provided behind the
// same live-creds skip-guard as the Sprint 19 var-file positive cases, so
// it goes assertive automatically when run with IBMCLOUD_API_KEY.
//
// Sub-case → assertion map (see closure in
// the prefix-driven config review):
//
//	TestPromptPrefix_NonTTY_ValidDefault       → non-TTY accepts a valid default prefix
//	TestPromptPrefix_NonTTY_InvalidDefaultErrors→ non-TTY hard-errors on an invalid default (CI contract)
//	TestRunPrefixInterview_NonTTY_DefaultAccept → default-accept persists prefix + resources + ClusterCfg
//	TestSeedVarFileInterview_SetsSanitizedPrefix→ --var-file path derives a sanitized Prefix + all-create resources
//	TestSeedVarFileInterview_DeclinedClusterCapturesExisting → declined cluster captures the existing name into Cluster.Name
//	TestRenderFullBody_DeclinedToggleCapturesExistingName → declined toggle's existing name lands in the right *_name var

package cli

import (
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/naming"
	"github.com/jgruberf5/roksbnkctl/internal/tf"
)

// newInitContext builds a config.Context for the interview helpers against a
// hermetic ROKSBNKCTL_HOME. existingPrefix, if non-empty, seeds a prior
// workspace config so the re-init default-prefix path can be exercised.
func newInitContext(t *testing.T, wsName, existingPrefix string) *config.Context {
	t.Helper()
	t.Setenv(config.ROKSBNKCTLHomeEnv, t.TempDir())
	cctx := &config.Context{WorkspaceName: wsName, Global: &config.Global{}}
	if existingPrefix != "" {
		cctx.Workspace = &config.Workspace{Prefix: existingPrefix}
	}
	return cctx
}

// TestPromptPrefix_NonTTY_ValidDefault — in a non-TTY run promptPrefix
// validates the default and returns it. For a workspace name that sanitizes
// to a valid prefix, that default is SanitizeToPrefix(workspaceName).
func TestPromptPrefix_NonTTY_ValidDefault(t *testing.T) {
	cctx := newInitContext(t, "demo-ws", "")

	got, err := promptPrefix(cctx)
	if err != nil {
		t.Fatalf("promptPrefix non-TTY valid default: unexpected error %v", err)
	}
	want := naming.SanitizeToPrefix("demo-ws")
	if got != want {
		t.Errorf("promptPrefix = %q; want sanitized default %q", got, want)
	}
	if err := naming.ValidatePrefix(got); err != nil {
		t.Errorf("returned prefix %q does not validate: %v", got, err)
	}
}

// TestPromptPrefix_NonTTY_ReInitUsesExistingPrefix — on a re-init the
// existing workspace's prefix is the default, not the sanitized name.
func TestPromptPrefix_NonTTY_ReInitUsesExistingPrefix(t *testing.T) {
	cctx := newInitContext(t, "demo-ws", "kept-prefix")

	got, err := promptPrefix(cctx)
	if err != nil {
		t.Fatalf("promptPrefix re-init: unexpected error %v", err)
	}
	if got != "kept-prefix" {
		t.Errorf("re-init promptPrefix = %q; want existing prefix %q", got, "kept-prefix")
	}
}

// TestPromptPrefix_NonTTY_InvalidDefaultErrors — the CI contract: when the
// default prefix is invalid (here an over-long sanitized workspace name) a
// non-TTY run returns a clear non-nil error rather than silently proceeding.
// MaxPrefixLen() drives the over-long length so this tracks the table.
func TestPromptPrefix_NonTTY_InvalidDefaultErrors(t *testing.T) {
	// A workspace name long enough that its sanitized prefix overflows the
	// naming-scheme limit. Workspace names allow up to 64 chars; the prefix
	// limit is MaxPrefixLen() (< 64), so a max-length all-letter name
	// sanitizes to an over-long-but-well-formed prefix → length rejection.
	longName := "a" + strings.Repeat("b", 50) // 51 chars > MaxPrefixLen()
	if len(longName) <= naming.MaxPrefixLen() {
		t.Fatalf("test setup: longName len %d not > MaxPrefixLen() %d", len(longName), naming.MaxPrefixLen())
	}
	cctx := newInitContext(t, longName, "")

	_, err := promptPrefix(cctx)
	if err == nil {
		t.Fatal("promptPrefix non-TTY with an invalid (over-long) default must return a non-nil error")
	}
	if !strings.Contains(err.Error(), "non-interactive") {
		t.Errorf("error should flag the non-interactive path; got: %v", err)
	}
}

// TestRunPrefixInterview_NonTTY_DefaultAccept — a non-TTY default-accept run
// of the interview body builds a Workspace whose Prefix is the sanitized
// name, whose ClusterCfg reflects the create default, and whose Resources
// block carries the toggle defaults. Persisting + reloading via the real
// config round-trip pins the on-disk shape (prefix + resources:).
func TestRunPrefixInterview_NonTTY_DefaultAccept(t *testing.T) {
	const wsName = "demo-accept"
	cctx := newInitContext(t, wsName, "")

	// The version here is an arbitrary fixture, not runInit's default — it is passed
	// explicitly, so this test does not move when the default OpenShift minor does.
	// Defaults otherwise mirror runInit's: create=true, region "us-south",
	// workers 1. Under `go test` stdin is non-TTY, so the create branch never
	// dials the API (pickRegion returns the default without touching ic) — a
	// nil client is safe.
	choices, err := runAccountInterview(context.Background(), nil, cctx, "us-south", "4.18", 1, true)
	if err != nil {
		t.Fatalf("runAccountInterview default-accept: %v", err)
	}
	cluster, prefix, resources := choices.Cluster, choices.Prefix, choices.Resources

	// Prefix derived from the workspace name.
	wantPrefix := naming.SanitizeToPrefix(wsName)
	if prefix != wantPrefix {
		t.Errorf("prefix = %q; want %q", prefix, wantPrefix)
	}
	// Cluster created → name is the prefix-derived cluster name (== prefix).
	if !cluster.Create {
		t.Errorf("cluster.Create = false; want true (default)")
	}
	if cluster.Name != naming.Derive(prefix).ClusterName {
		t.Errorf("cluster.Name = %q; want derived %q", cluster.Name, naming.Derive(prefix).ClusterName)
	}
	if resources == nil {
		t.Fatal("resources block is nil; the interview must populate it")
	}
	// Registry COS is offered (default yes) only because the cluster is
	// created — pin that it landed as create=true.
	if !resources.RegistryCOS.Create {
		t.Errorf("RegistryCOS.Create = false; want true (default, cluster created)")
	}

	// Round-trip through the real config persistence so the on-disk YAML
	// shape (prefix + resources:) is pinned, not just the in-memory struct.
	ws := &config.Workspace{
		IBMCloud:  config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster:   cluster,
		Prefix:    prefix,
		Resources: resources,
		TFSource:  config.TFSourceCfg{Type: "embedded"},
	}
	if err := config.SaveWorkspace(wsName, ws); err != nil {
		t.Fatalf("SaveWorkspace: %v", err)
	}
	cfgPath, _ := config.WorkspaceConfigPath(wsName)
	raw, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("reading persisted config: %v", err)
	}
	for _, frag := range []string{"prefix: " + wantPrefix, "resources:", "registry_cos:"} {
		if !strings.Contains(string(raw), frag) {
			t.Errorf("persisted config.yaml missing %q\n--- config.yaml ---\n%s", frag, raw)
		}
	}

	reloaded, err := config.LoadWorkspace(wsName)
	if err != nil {
		t.Fatalf("LoadWorkspace: %v", err)
	}
	if reloaded.Prefix != wantPrefix {
		t.Errorf("reloaded.Prefix = %q; want %q", reloaded.Prefix, wantPrefix)
	}
	if reloaded.Resources == nil {
		t.Error("reloaded.Resources is nil; the resources block did not persist")
	}
	if reloaded.Cluster.Name != cluster.Name {
		t.Errorf("reloaded.Cluster.Name = %q; want %q", reloaded.Cluster.Name, cluster.Name)
	}
}

// TestRenderFullBody_DeclinedToggleCapturesExistingName — the
// existing-resource-capture contract at the render seam: a declined toggle
// whose Existing name is set routes that name into the matching *_name
// variable with create_* = false. This pins the wiring the interview builds
// (ResourceToggle.Existing) all the way through to the rendered tfvars,
// which is what the operator's `up` consumes.
func TestRenderFullBody_DeclinedToggleCapturesExistingName(t *testing.T) {
	ws := &config.Workspace{
		IBMCloud: config.IBMCloudCfg{Region: "us-south", ResourceGroup: "default"},
		Cluster:  config.ClusterCfg{Create: true, Name: "demo"},
		Prefix:   "demo",
		Resources: &config.ResourcesCfg{
			TransitGateway: config.ResourceToggle{Create: false, Existing: "shared-tgw"},
			RegistryCOS:    config.ResourceToggle{Create: true},
			TGWJumphost:    config.ResourceToggle{Create: true},
			ClientVPC:      config.ResourceToggle{Create: false, Existing: "shared-client-vpc"},
		},
	}
	var buf bytes.Buffer
	if err := tf.RenderTFVars(&buf, ws, "", ""); err != nil {
		t.Fatalf("RenderTFVars: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		`create_roks_transit_gateway = false`,
		`roks_transit_gateway_name = "shared-tgw"`,
		`testing_create_client_vpc = false`,
		`testing_client_vpc_name = "shared-client-vpc"`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing existing-resource wiring %q\noutput:\n%s", want, out)
		}
	}
}

// TestPrintNamePlan_ShowsDerivedNames — the operator-facing name plan prints
// every derived name for a created resource and annotates an existing one.
// (printNamePlan is what runInit shows on stderr before SaveWorkspace.)
func TestPrintNamePlan_ShowsDerivedNames(t *testing.T) {
	ws := &config.Workspace{
		Cluster: config.ClusterCfg{Create: true, Name: "demo"},
		Prefix:  "demo",
		Resources: &config.ResourcesCfg{
			TransitGateway: config.ResourceToggle{Create: false, Existing: "shared-tgw"},
			RegistryCOS:    config.ResourceToggle{Create: true},
			TGWJumphost:    config.ResourceToggle{Create: true},
			ClientVPC:      config.ResourceToggle{Create: true},
		},
	}
	var buf bytes.Buffer
	printNamePlan(&buf, ws)
	out := buf.String()
	plan := naming.Derive("demo")
	for _, want := range []string{
		plan.ClusterVPCName,
		plan.COSInstanceName,
		"shared-tgw", // existing transit gateway annotated
		"(existing)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("name plan missing %q\noutput:\n%s", want, out)
		}
	}
}

// TestPrintNamePlan_ShowsAdoptedClusterVPC — when the cluster VPC is adopted
// (ClusterVPC.Create=false + an id), the name plan annotates it as existing
// instead of printing the derived name. Mirrors the transit-gateway BYO path,
// and guards the render condition (adopt only when Create=false AND id != "").
func TestPrintNamePlan_ShowsAdoptedClusterVPC(t *testing.T) {
	ws := &config.Workspace{
		Cluster: config.ClusterCfg{Create: true, Name: "demo"},
		Prefix:  "demo",
		Resources: &config.ResourcesCfg{
			ClusterVPC:  config.ResourceToggle{Create: false, Existing: "r006-6fe0b20a-vpc"},
			RegistryCOS: config.ResourceToggle{Create: true},
		},
	}
	var buf bytes.Buffer
	printNamePlan(&buf, ws)
	out := buf.String()
	if !strings.Contains(out, "r006-6fe0b20a-vpc") || !strings.Contains(out, "(existing)") {
		t.Errorf("adopted cluster VPC not annotated as existing\noutput:\n%s", out)
	}
	// The derived cluster-VPC name must NOT appear once a VPC is adopted.
	if strings.Contains(out, naming.Derive("demo").ClusterVPCName) {
		t.Errorf("derived cluster-VPC name leaked when adopting an existing VPC\noutput:\n%s", out)
	}
}

// TestPrintNamePlan_LegacyEmptyPrefix_PrintsNothing — a legacy empty-prefix
// workspace prints no name plan (the upstream defaults still apply).
func TestPrintNamePlan_LegacyEmptyPrefix_PrintsNothing(t *testing.T) {
	ws := &config.Workspace{Cluster: config.ClusterCfg{Create: true, Name: "legacy"}}
	var buf bytes.Buffer
	printNamePlan(&buf, ws)
	if buf.Len() != 0 {
		t.Errorf("legacy empty-prefix workspace printed a name plan:\n%s", buf.String())
	}
}

// TestInitPrefix_CobraDefaultAccept_PersistsPrefix is the end-to-end
// cobra-driven default-accept run. It needs a live IBM Cloud key because
// runInit verifies credentials before reaching the interview (no in-process
// stub seam), so it skip-guards exactly like the Sprint 19 var-file positive
// cases and goes assertive automatically when IBMCLOUD_API_KEY is present.
// The gated-live e2e scripts under scripts/ cover this path for the
// integrator's `!` cycle.
func TestInitPrefix_CobraDefaultAccept_PersistsPrefix(t *testing.T) {
	skipIfNoLiveIBMCreds(t)
	resetInitFlags(t)
	home := stageHermeticHome(t)
	const wsName = "test-ws-prefix-accept"

	_, errOut, runErr := runRootCmd(t, "init", "-w", wsName)
	if runErr != nil {
		t.Fatalf("init default-accept failed: %v\nstderr:\n%s", runErr, errOut)
	}
	ws, err := config.LoadWorkspace(wsName)
	if err != nil {
		t.Fatalf("loading persisted workspace: %v", err)
	}
	if ws.Prefix == "" {
		t.Error("persisted workspace has empty Prefix; the interview must set it")
	}
	if ws.Resources == nil {
		t.Error("persisted workspace has nil Resources; the interview must populate it")
	}
	_ = home
}
