package bigipcis_test

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/demo"
	dembigipcis "github.com/JLCode-tech/awsbnkctl/internal/demo/bigipcis"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	"github.com/JLCode-tech/awsbnkctl/internal/scenarios"
)

func makeCluster() *intent.Cluster {
	return &intent.Cluster{
		Metadata: intent.Metadata{
			Name:   "test-cluster",
			Region: "ap-southeast-2",
		},
		Network: intent.Network{
			VPCCidr: "10.0.0.0/16",
			AZs:     []string{"ap-southeast-2a"},
			Subnets: intent.Subnets{
				Public:  []intent.SubnetSpec{{CIDR: "10.0.1.0/24", AZ: "ap-southeast-2a"}},
				Private: []intent.SubnetSpec{{CIDR: "10.0.11.0/24", AZ: "ap-southeast-2a"}, {CIDR: "10.0.12.0/24", AZ: "ap-southeast-2a"}},
			},
			DataPath: &intent.DataPathSpec{
				External: intent.SubnetSpec{CIDR: "10.0.10.0/24", AZ: "ap-southeast-2a"},
				Internal: intent.SubnetSpec{CIDR: "10.0.20.0/24", AZ: "ap-southeast-2a"},
			},
		},
		Pattern: "dual-interface",
		BigIPVE: &intent.BigIPVESpec{Enabled: true},
	}
}

// onboardedState returns a State pre-seeded with a fully-onboarded BIG-IP and
// jumphost, so Verify proceeds past the gating checks.
func onboardedState(t *testing.T) *state.State {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	st.Set("BIGIP_ONBOARDED", "true")
	st.Set("BIGIP_MGMT_IP", "10.0.1.50")
	st.Set("BIGIP_VIP", "10.0.10.120")
	st.Set("JUMPHOST_INSTANCE_ID", "i-test")
	st.Set("JUMPHOST_BNK_EXT_ENI_IP", "10.0.10.200")
	return st
}

// TestVerifyCallOrder guards the load-bearing step order in Verify:
//
//	waitDeploymentAvailable(cis-backend) →
//	waitDeploymentAvailable(CIS controller) →
//	CheckVSProgrammed →
//	RunBNKProbe
func TestVerifyCallOrder(t *testing.T) {
	var calls []string

	deps := dembigipcis.VerifyDeps{
		WaitDeploymentAvailableFn: func(_ context.Context, _ *scenarios.Context, _, name string, _ time.Duration) error {
			calls = append(calls, "waitDeploymentAvailable("+name+")")
			return nil
		},
		CheckVSProgrammedFn: func(_ context.Context, _ *scenarios.Context, _, _ string) (bool, string) {
			calls = append(calls, "CheckVSProgrammed")
			return true, "ok"
		},
		RunBNKProbeFn: func(_ context.Context, _ *scenarios.Context, _ string, _ time.Duration) (bool, string) {
			calls = append(calls, "RunBNKProbe")
			return true, "ok"
		},
	}

	s := newTestScenario(deps)
	sctx := &scenarios.Context{
		Ctx:     context.Background(),
		Cluster: makeCluster(),
		State:   onboardedState(t),
		Out:     io.Discard,
		Options: map[string]string{},
	}

	s.Verify(sctx)

	want := []string{
		"waitDeploymentAvailable(cis-backend)",
		"waitDeploymentAvailable(bigip-cis-f5-bigip-ctlr)",
		"CheckVSProgrammed",
		"RunBNKProbe",
	}
	if len(calls) != len(want) {
		t.Fatalf("call sequence = %v\nwant            = %v", calls, want)
	}
	for i, got := range calls {
		if got != want[i] {
			t.Errorf("calls[%d] = %q, want %q", i, got, want[i])
		}
	}
}

func newTestScenario(deps dembigipcis.VerifyDeps) scenarios.Scenario {
	return dembigipcis.NewScenarioForTest(dembigipcis.ScenarioTestConfig{VDeps: deps})
}

// TestVerify_GateOnNotOnboarded asserts Verify fails fast with a clear message
// when BIGIP_ONBOARDED is not "true" — and never reaches the probe hooks.
func TestVerify_GateOnNotOnboarded(t *testing.T) {
	called := false
	deps := dembigipcis.VerifyDeps{
		WaitDeploymentAvailableFn: func(_ context.Context, _ *scenarios.Context, _, _ string, _ time.Duration) error {
			called = true
			return nil
		},
		CheckVSProgrammedFn: func(_ context.Context, _ *scenarios.Context, _, _ string) (bool, string) { return true, "" },
		RunBNKProbeFn: func(_ context.Context, _ *scenarios.Context, _ string, _ time.Duration) (bool, string) {
			return true, ""
		},
	}
	s := newTestScenario(deps)

	dir := t.TempDir()
	st, _ := state.Load(dir) // BIGIP_ONBOARDED unset
	sctx := &scenarios.Context{
		Ctx:     context.Background(),
		Cluster: makeCluster(),
		State:   st,
		Out:     io.Discard,
		Options: map[string]string{},
	}

	res := s.Verify(sctx)
	if res.Status == "ok" {
		t.Fatal("Verify should fail when BIG-IP is not onboarded")
	}
	if called {
		t.Error("Verify reached deployment wait despite not-onboarded gate")
	}
	if !assertionMentions(res, "onboarded") {
		t.Errorf("expected an assertion mentioning 'onboarded', got: %v", res.Assertions)
	}
}

// TestVerify_GateOnBigIPVEDisabled asserts the gate also trips when bigipVE is
// not enabled in the cluster intent.
func TestVerify_GateOnBigIPVEDisabled(t *testing.T) {
	deps := dembigipcis.VerifyDeps{
		WaitDeploymentAvailableFn: func(_ context.Context, _ *scenarios.Context, _, _ string, _ time.Duration) error { return nil },
		CheckVSProgrammedFn:       func(_ context.Context, _ *scenarios.Context, _, _ string) (bool, string) { return true, "" },
		RunBNKProbeFn: func(_ context.Context, _ *scenarios.Context, _ string, _ time.Duration) (bool, string) {
			return true, ""
		},
	}
	s := newTestScenario(deps)

	cl := makeCluster()
	cl.BigIPVE = &intent.BigIPVESpec{Enabled: false}
	sctx := &scenarios.Context{
		Ctx:     context.Background(),
		Cluster: cl,
		State:   onboardedState(t),
		Out:     io.Discard,
		Options: map[string]string{},
	}

	res := s.Verify(sctx)
	if res.Status == "ok" {
		t.Fatal("Verify should fail when bigipVE is disabled")
	}
	if !assertionMentions(res, "bigipVE") {
		t.Errorf("expected an assertion mentioning 'bigipVE', got: %v", res.Assertions)
	}
}

// TestApply_PasswordRequired asserts Apply errors when the env password is unset,
// and that the error names the env var.
func TestApply_PasswordRequired(t *testing.T) {
	restore := dembigipcis.SetGetBigIPPassword(func() string { return "" })
	defer restore()

	var secretCalled bool
	cfg := dembigipcis.ScenarioTestConfig{
		ApplyRoutesFn:  func(_ context.Context, _ *scenarios.Context, _ []string) error { return nil },
		CreateSecretFn: func(_ context.Context, _ *scenarios.Context, _ string) error { secretCalled = true; return nil },
	}
	s := dembigipcis.NewScenarioForApplyTest(cfg)

	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("BIGIP_MGMT_IP", "10.0.1.50")
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      makeCluster(),
		State:        st,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	err := s.Apply(sctx)
	if err == nil {
		t.Fatal("Apply should error when AWSBNKCTL_BIGIP_PASSWORD is unset")
	}
	if !strings.Contains(err.Error(), "AWSBNKCTL_BIGIP_PASSWORD") {
		t.Errorf("error should name the env var, got: %v", err)
	}
	if secretCalled {
		t.Error("createSecret should NOT be called when the password is empty")
	}
}

// TestApply_SecretGetsEnvPassword asserts the password the createSecret seam
// receives comes from the env reader — and crucially that the password is never
// written into any rendered manifest file on disk.
func TestApply_SecretGetsEnvPassword(t *testing.T) {
	const secretPW = "s3cr3t-PW-not-in-manifests"
	restore := dembigipcis.SetGetBigIPPassword(func() string { return secretPW })
	defer restore()

	var gotPW string
	cfg := dembigipcis.ScenarioTestConfig{
		ApplyRoutesFn:  func(_ context.Context, _ *scenarios.Context, _ []string) error { return nil },
		CreateSecretFn: func(_ context.Context, _ *scenarios.Context, password string) error { gotPW = password; return nil },
	}
	s := dembigipcis.NewScenarioForApplyTest(cfg)

	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("BIGIP_MGMT_IP", "10.0.1.50")
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      makeCluster(),
		State:        st,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	// Render manifests first (Apply renders nothing; demo run calls Manifests).
	if _, err := s.Manifests(sctx); err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	if err := s.Apply(sctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if gotPW != secretPW {
		t.Errorf("createSecret password = %q, want %q", gotPW, secretPW)
	}

	// The password must NEVER appear in any rendered manifest on disk.
	assertNoPasswordOnDisk(t, dir, secretPW)
}

// TestApply_SecretBeforeHelm guards the load-bearing Apply ordering invariant:
// the bigip-login Secret MUST be created BEFORE the Helm EnsureRelease call. The
// CIS pod mounts that secret and stays ContainerCreating until it exists, so a
// Helm --wait that runs first would block the full timeout (the live deadlock).
func TestApply_SecretBeforeHelm(t *testing.T) {
	restore := dembigipcis.SetGetBigIPPassword(func() string { return "pw" })
	defer restore()

	var calls []string
	cfg := dembigipcis.ScenarioTestConfig{
		ApplyRoutesFn: func(_ context.Context, _ *scenarios.Context, _ []string) error { return nil },
		CreateSecretFn: func(_ context.Context, _ *scenarios.Context, _ string) error {
			calls = append(calls, "createSecret")
			return nil
		},
		EnsureReleaseFn: func() error { calls = append(calls, "ensureRelease"); return nil },
	}
	s := dembigipcis.NewScenarioForApplyTest(cfg)

	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("BIGIP_MGMT_IP", "10.0.1.50")
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      makeCluster(),
		State:        st,
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	if err := s.Apply(sctx); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	want := []string{"createSecret", "ensureRelease"}
	if len(calls) != len(want) {
		t.Fatalf("call sequence = %v\nwant            = %v", calls, want)
	}
	for i, got := range calls {
		if got != want[i] {
			t.Errorf("calls[%d] = %q, want %q (secret must precede Helm install)", i, got, want[i])
		}
	}
}

// assertNoPasswordOnDisk walks the workspace dir and fails if the secret value
// appears in any file (proving the pw stays out of rendered manifests).
func assertNoPasswordOnDisk(t *testing.T, dir, secret string) {
	t.Helper()
	err := dembigipcis.WalkFiles(dir, func(path string, content []byte) error {
		if strings.Contains(string(content), secret) {
			t.Errorf("password leaked into on-disk file %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking workspace: %v", err)
	}
}

func TestBigIPCIS_Registered(t *testing.T) {
	s := demo.Find("bigip-cis")
	if s == nil {
		t.Fatal("bigip-cis not registered — init() not called?")
	}
	if s.Rating() != scenarios.Green {
		t.Errorf("rating = %q, want green", s.Rating())
	}
	if len(s.Dependencies()) != 0 {
		t.Errorf("dependencies = %v, want empty", s.Dependencies())
	}
}

func TestBigIPCIS_Title(t *testing.T) {
	s := demo.Find("bigip-cis")
	if s == nil {
		t.Fatal("scenario not registered")
	}
	if !strings.Contains(strings.ToLower(s.Title()), "big-ip") {
		t.Errorf("Title() = %q, want it to mention BIG-IP", s.Title())
	}
}

func TestBigIPCIS_DescriptionMigrationNarrative(t *testing.T) {
	s := demo.Find("bigip-cis")
	if s == nil {
		t.Fatal("scenario not registered")
	}
	d := s.Description()
	for _, want := range []string{"cne-controller", "TMM", "HTTPRoute", "VirtualServer"} {
		if !strings.Contains(d, want) {
			t.Errorf("Description() missing migration term %q", want)
		}
	}
}

func TestBigIPCIS_ManifestsRendered(t *testing.T) {
	dir := t.TempDir()
	sctx := &scenarios.Context{
		Ctx:          context.Background(),
		Cluster:      makeCluster(),
		State:        onboardedState(t),
		Out:          io.Discard,
		WorkspaceDir: dir,
		Options:      map[string]string{},
	}

	s := demo.Find("bigip-cis")
	if s == nil {
		t.Fatal("scenario not registered")
	}

	paths, err := s.Manifests(sctx)
	if err != nil {
		t.Fatalf("Manifests: %v", err)
	}
	if len(paths) != 3 {
		t.Errorf("expected 3 manifest paths, got %d: %v", len(paths), paths)
	}

	for _, p := range paths {
		content, readErr := os.ReadFile(p) // #nosec G304 — test path from WriteManifest
		if readErr != nil {
			t.Fatalf("reading %s: %v", p, readErr)
		}
		if strings.Contains(string(content), "{{") || strings.Contains(string(content), "}}") {
			t.Errorf("manifest %s still contains template directives:\n%s", p, content)
		}
		if strings.HasSuffix(p, "03-virtualserver.yaml") {
			if !strings.Contains(string(content), "10.0.10.120") {
				t.Errorf("03-virtualserver.yaml: expected VIP 10.0.10.120 from state, got:\n%s", content)
			}
			if !strings.Contains(string(content), "web.cis.migration.local") {
				t.Errorf("03-virtualserver.yaml: expected host web.cis.migration.local, got:\n%s", content)
			}
			if !strings.Contains(string(content), "cis.f5.com/v1") {
				t.Errorf("03-virtualserver.yaml: expected apiVersion cis.f5.com/v1, got:\n%s", content)
			}
		}
	}
}

func TestBigIPCIS_HelmValues(t *testing.T) {
	vals := dembigipcis.CISHelmValues("10.0.1.50")
	args, ok := vals["args"].(map[string]interface{})
	if !ok {
		t.Fatal("helm values missing args map")
	}
	checks := map[string]interface{}{
		"bigip_url":            "10.0.1.50",
		"bigip_partition":      "cis",
		"pool_member_type":     "cluster",
		"custom_resource_mode": true,
		"insecure":             true,
	}
	for k, want := range checks {
		if args[k] != want {
			t.Errorf("args[%q] = %v, want %v", k, args[k], want)
		}
	}
	if vals["bigip_login_secret"] != "bigip-login" {
		t.Errorf("bigip_login_secret = %v, want bigip-login", vals["bigip_login_secret"])
	}
	// The controller image tag is pinned via the chart's top-level `version` key.
	// The chart ignores image.tag, so it must NOT be relied on.
	if vals["version"] != "2.20.3" {
		t.Errorf("version = %v, want 2.20.3 (top-level controller image tag)", vals["version"])
	}
	img, ok := vals["image"].(map[string]interface{})
	if !ok {
		t.Fatalf("helm values missing image map: %v", vals["image"])
	}
	if _, present := img["tag"]; present {
		t.Errorf("image.tag is set (%v) but the chart ignores it — pin via top-level version instead", img["tag"])
	}
}

func TestBigIPCIS_GatewayFromCIDR(t *testing.T) {
	cases := map[string]string{
		"10.0.20.0/24":  "10.0.20.1",
		"172.16.5.0/24": "172.16.5.1",
		"bad":           "",
	}
	for in, want := range cases {
		if got := dembigipcis.GatewayFromCIDR(in); got != want {
			t.Errorf("GatewayFromCIDR(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestBigIPCIS_NamespaceFromOptions(t *testing.T) {
	s := demo.Find("bigip-cis")
	if s == nil {
		t.Fatal("scenario not registered")
	}
	sctx := &scenarios.Context{
		Ctx:     context.Background(),
		Cluster: makeCluster(),
		Out:     io.Discard,
		Options: map[string]string{"namespace": "custom-ns"},
	}
	if ns := s.Namespace(sctx); ns != "custom-ns" {
		t.Errorf("Namespace() = %q, want custom-ns", ns)
	}
}

// assertionMentions reports whether any assertion description or Got contains sub.
func assertionMentions(res scenarios.Result, sub string) bool {
	for _, a := range res.Assertions {
		if strings.Contains(a.Description, sub) || strings.Contains(a.Got, sub) {
			return true
		}
	}
	return false
}

// TestCleanup_DeletesSecretFromCISNamespace pins the Cleanup namespace bug:
// createBigIPLoginSecret creates bigip-login in the CIS namespace (kube-system),
// so Cleanup must delete it from kube-system — deleting from the demo namespace
// is a guaranteed NotFound no-op that leaves the admin-password Secret behind.
func TestCleanup_DeletesSecretFromCISNamespace(t *testing.T) {
	clientset := k8sfake.NewClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "bigip-login", Namespace: "kube-system"}},
		&corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: "demo-bigip-cis"}},
	)

	cfg := dembigipcis.ScenarioTestConfig{
		RemoveRoutesFn: func(_ context.Context, _ *scenarios.Context, _ []string) error { return nil },
	}
	s := dembigipcis.NewScenarioForTest(cfg)

	sctx := &scenarios.Context{
		Ctx:       context.Background(),
		Cluster:   makeCluster(),
		State:     onboardedState(t),
		Out:       io.Discard,
		Clientset: clientset,
		Options:   map[string]string{},
	}

	if err := s.Cleanup(sctx); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}

	_, err := clientset.CoreV1().Secrets("kube-system").Get(context.Background(), "bigip-login", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("bigip-login Secret should be deleted from kube-system, Get err = %v", err)
	}

	_, err = clientset.CoreV1().Namespaces().Get(context.Background(), "demo-bigip-cis", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Errorf("demo namespace should be deleted, Get err = %v", err)
	}
}

// TestCleanup_TolerantWhenSecretMissing asserts Cleanup stays idempotent: a
// second run (Secret + namespace already gone) must not error.
func TestCleanup_TolerantWhenSecretMissing(t *testing.T) {
	clientset := k8sfake.NewClientset()
	cfg := dembigipcis.ScenarioTestConfig{
		RemoveRoutesFn: func(_ context.Context, _ *scenarios.Context, _ []string) error { return nil },
	}
	s := dembigipcis.NewScenarioForTest(cfg)

	sctx := &scenarios.Context{
		Ctx:       context.Background(),
		Cluster:   makeCluster(),
		State:     onboardedState(t),
		Out:       io.Discard,
		Clientset: clientset,
		Options:   map[string]string{},
	}

	if err := s.Cleanup(sctx); err != nil {
		t.Errorf("Cleanup on empty cluster should be a tolerated no-op, got: %v", err)
	}
}
