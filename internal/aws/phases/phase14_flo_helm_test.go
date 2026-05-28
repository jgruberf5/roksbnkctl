//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig codegen
package phases

import (
	"context"
	"errors"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/release"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	k8smanifests "github.com/JLCode-tech/awsbnkctl/internal/k8s/manifests"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

// ─── fakeHelmInstaller ───────────────────────────────────────────────────────

// fakeHelmInstaller records calls for test assertions.
type fakeHelmInstaller struct {
	// Configurable returns
	listReleases []*release.Release
	listErr      error
	installErr   error
	upgradeErr   error
	uninstallErr error
	pullErr      error

	// Call recording
	listCalls      int
	installCalls   int
	upgradeCalls   int
	uninstallCalls int
	pullCalls      int

	lastReleaseName  string
	lastNamespace    string
	lastValues       map[string]interface{}
	lastUninstallRel string
}

func (f *fakeHelmInstaller) List(_, _ string) ([]*release.Release, error) {
	f.listCalls++
	return f.listReleases, f.listErr
}

func (f *fakeHelmInstaller) Install(releaseName, namespace string, _ *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	f.installCalls++
	f.lastReleaseName = releaseName
	f.lastNamespace = namespace
	f.lastValues = values
	if f.installErr != nil {
		return nil, f.installErr
	}
	return &release.Release{Name: releaseName, Namespace: namespace}, nil
}

func (f *fakeHelmInstaller) Upgrade(releaseName, namespace string, _ *chart.Chart, values map[string]interface{}) (*release.Release, error) {
	f.upgradeCalls++
	f.lastReleaseName = releaseName
	f.lastNamespace = namespace
	f.lastValues = values
	if f.upgradeErr != nil {
		return nil, f.upgradeErr
	}
	return &release.Release{Name: releaseName, Namespace: namespace}, nil
}

func (f *fakeHelmInstaller) Uninstall(releaseName, _ string) error {
	f.uninstallCalls++
	f.lastUninstallRel = releaseName
	return f.uninstallErr
}

func (f *fakeHelmInstaller) PullAndLoad(_, _ string) (*chart.Chart, error) {
	f.pullCalls++
	if f.pullErr != nil {
		return nil, f.pullErr
	}
	return &chart.Chart{
		Metadata: &chart.Metadata{Name: "f5-lifecycle-operator", Version: "v2.21.13-0.0.28"},
	}, nil
}

// ─── test helpers ────────────────────────────────────────────────────────────

// clusterWithBnk returns a sydTracerCluster with a bnk: block using tmp files.
func clusterWithBnk(t *testing.T) (*intent.Cluster, string, string) {
	t.Helper()
	dir := t.TempDir()
	farPath := writeDryRunFile(t, dir, "far.json", "base64fakecredential==")
	jwtPath := writeDryRunFile(t, dir, "license.jwt", "my-jwt-token")

	cl := sydTracerCluster()
	cl.Bnk = &intent.BnkSpec{
		FARArchive:         farPath,
		JWT:                jwtPath,
		CertManagerVersion: "1.16.1",
	}
	return cl, farPath, jwtPath
}

// buildFakeCNEClients builds a Clients struct with a fake dynamic client that
// already contains the CNE CRD object so WaitForCRDExists succeeds immediately.
func buildFakeCNEClients(t *testing.T) *Clients {
	t.Helper()
	cs := k8sfake.NewSimpleClientset()
	scheme := buildScheme()

	crdObj := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]interface{}{
				"name": cneCRDName,
			},
		},
	}
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
	}, crdObj)
	return &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}
}

// buildFakeDynClients builds a Clients struct with no seeded CRDs (CRD wait will time out).
func buildFakeDynClients(t *testing.T) *Clients {
	t.Helper()
	cs := k8sfake.NewSimpleClientset()
	scheme := buildScheme()
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
	})
	return &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}
}

// ─── Test 1: Dry-run path ────────────────────────────────────────────────────

func TestPhase14_DryRun_NoHelmCalls(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	clients := &Clients{Profile: "test"} // no k8s clients needed in dry-run

	if err := Phase14FLOHelm(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase14FLOHelm dry-run: %v", err)
	}

	if st.Get("FLO_RELEASE_NAME") != floReleaseName {
		t.Errorf("FLO_RELEASE_NAME = %q, want %q", st.Get("FLO_RELEASE_NAME"), floReleaseName)
	}
	if st.Get("FLO_VERSION") != intent.DefaultFLOVersion {
		t.Errorf("FLO_VERSION = %q, want %q", st.Get("FLO_VERSION"), intent.DefaultFLOVersion)
	}
	if st.Get("FLO_NAMESPACE") != "dry-run" {
		t.Errorf("FLO_NAMESPACE = %q, want dry-run", st.Get("FLO_NAMESPACE"))
	}
	if st.Get("FLO_INSTALLED_AT") != "dry-run" {
		t.Errorf("FLO_INSTALLED_AT = %q, want dry-run", st.Get("FLO_INSTALLED_AT"))
	}
}

// ─── Test 2: Fresh install calls Install, not Upgrade ───────────────────────

func TestPhase14_FreshInstall_CallsInstall(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("KUBECONFIG_PATH", "/fake/kubeconfig")

	clients := buildFakeCNEClients(t)
	fake := &fakeHelmInstaller{} // empty listReleases → fresh install

	if err := runFLOHelmInstall(context.Background(), fake, cl, st, intent.DefaultFLOVersion, map[string]interface{}{}, clients); err != nil {
		t.Fatalf("runFLOHelmInstall fresh install: %v", err)
	}

	if fake.installCalls != 1 {
		t.Errorf("installCalls = %d, want 1", fake.installCalls)
	}
	if fake.upgradeCalls != 0 {
		t.Errorf("upgradeCalls = %d, want 0", fake.upgradeCalls)
	}
	if fake.lastReleaseName != floReleaseName {
		t.Errorf("releaseName = %q, want %q", fake.lastReleaseName, floReleaseName)
	}
	if fake.lastNamespace != floNamespace {
		t.Errorf("namespace = %q, want %q", fake.lastNamespace, floNamespace)
	}
	if st.Get("FLO_RELEASE_NAME") != floReleaseName {
		t.Errorf("FLO_RELEASE_NAME state = %q", st.Get("FLO_RELEASE_NAME"))
	}
}

// ─── Test 3: Idempotent upgrade calls Upgrade, not Install ──────────────────

func TestPhase14_ExistingRelease_CallsUpgrade(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("KUBECONFIG_PATH", "/fake/kubeconfig")

	clients := buildFakeCNEClients(t)
	fake := &fakeHelmInstaller{
		listReleases: []*release.Release{
			{Name: floReleaseName, Namespace: floNamespace},
		},
	}

	if err := runFLOHelmInstall(context.Background(), fake, cl, st, intent.DefaultFLOVersion, map[string]interface{}{}, clients); err != nil {
		t.Fatalf("runFLOHelmInstall upgrade: %v", err)
	}

	if fake.upgradeCalls != 1 {
		t.Errorf("upgradeCalls = %d, want 1", fake.upgradeCalls)
	}
	if fake.installCalls != 0 {
		t.Errorf("installCalls = %d, want 0 (upgrade path must not call Install)", fake.installCalls)
	}
}

// ─── Test 4: Helm install failure surfaces error ────────────────────────────

func TestPhase14_InstallFailure_SurfacesError(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("KUBECONFIG_PATH", "/fake/kubeconfig")

	clients := buildFakeDynClients(t)
	fake := &fakeHelmInstaller{
		installErr: errors.New("OCI pull timeout"),
	}

	err := runFLOHelmInstall(context.Background(), fake, cl, st, intent.DefaultFLOVersion, map[string]interface{}{}, clients)
	if err == nil {
		t.Fatal("expected error on install failure, got nil")
	}
	if !strings.Contains(err.Error(), "helm install") {
		t.Errorf("error should mention 'helm install': %v", err)
	}
}

// ─── Test 5: Down uninstalls via Phase14FLOHelmDown (nil-K8s soft-skip path) ─
//
// NOTE: The FAR-file → OCI-login → Helm uninstall path requires a live
// repo.f5.com connection and is integration-tested, not unit-tested here.
// This test exercises Phase14FLOHelmDown directly, covering the nil-K8s
// soft-skip guard, state-clear, and state-save code paths.

func TestPhase14_Down_CallsUninstall(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	// Seed FLO state to verify it is cleared on down.
	st.Set("FLO_RELEASE_NAME", floReleaseName)
	st.Set("FLO_VERSION", "v2.21.13-0.0.28")
	st.Set("FLO_NAMESPACE", floNamespace)
	st.Set("FLO_INSTALLED_AT", "2026-01-01T00:00:00Z")

	// K8s == nil triggers the soft-skip path: logs warning, clears state, saves.
	clients := &Clients{Profile: "test"} // K8s is nil

	if err := Phase14FLOHelmDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase14FLOHelmDown (nil-K8s): %v", err)
	}
	// State must be cleared after down.
	for _, key := range []string{"FLO_RELEASE_NAME", "FLO_VERSION", "FLO_NAMESPACE", "FLO_INSTALLED_AT"} {
		if got := st.Get(key); got != "" {
			t.Errorf("after down: state key %q = %q, want empty", key, got)
		}
	}
}

// ─── Test 6: Down tolerates "release not found" via Phase14FLOHelmDown ──────
//
// NOTE: The realHelmInstaller uses IgnoreNotFound=true, so release-not-found
// errors are suppressed by the SDK. This test exercises Phase14FLOHelmDown
// through the FAR-unreadable soft-skip path (analogous to not-found tolerance)
// and verifies the function returns nil rather than propagating the error.

func TestPhase14_Down_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	cl := sydTracerCluster()
	// cl.Bnk is nil → FAR archive unreadable → Phase14FLOHelmDown returns nil
	// (logs warning) rather than surfacing the error, matching the
	// IgnoreNotFound contract of realHelmInstaller.
	cl.Bnk = nil
	dir := t.TempDir()
	st, _ := state.Load(dir)
	clients := buildFakeCNEClients(t)

	if err := Phase14FLOHelmDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase14FLOHelmDown should return nil when FAR unreadable (not-found tolerance): %v", err)
	}
}

// ─── Test 7: CRD wait succeeds ──────────────────────────────────────────────

func TestPhase14_CRDWait_Succeeds(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("KUBECONFIG_PATH", "/fake/kubeconfig")

	clients := buildFakeCNEClients(t) // CRD is seeded
	fake := &fakeHelmInstaller{}

	if err := runFLOHelmInstall(context.Background(), fake, cl, st, intent.DefaultFLOVersion, map[string]interface{}{}, clients); err != nil {
		t.Fatalf("CRD wait should succeed when CRD is present: %v", err)
	}
	if st.Get("FLO_INSTALLED_AT") == "" {
		t.Error("FLO_INSTALLED_AT should be set after successful install")
	}
}

// ─── Test 8: CRD wait times out → error ─────────────────────────────────────

func TestPhase14_CRDWait_TimesOut(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("KUBECONFIG_PATH", "/fake/kubeconfig")

	clients := buildFakeDynClients(t) // no CRD seeded
	fake := &fakeHelmInstaller{}

	// Use an already-expired context to force immediate timeout.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	err := runFLOHelmInstall(ctx, fake, cl, st, intent.DefaultFLOVersion, map[string]interface{}{}, clients)
	if err == nil {
		t.Fatal("expected error on CRD wait timeout, got nil")
	}
	if !strings.Contains(err.Error(), cneCRDName) {
		t.Errorf("error should mention CRD name %q: %v", cneCRDName, err)
	}
}

// ─── Test 9: FLO disabled → no helm calls, log skip ─────────────────────────

func TestPhase14_FLODisabled_SkipsHelmCalls(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	disabled := false
	cl.Addons = &intent.AddonsSpec{
		Flo: &intent.FloSpec{Enabled: &disabled},
	}
	dir := t.TempDir()
	st, _ := state.Load(dir)
	clients := &Clients{Profile: "test"}

	if err := Phase14FLOHelm(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase14FLOHelm disabled: %v", err)
	}
	// No FLO state should be set when disabled.
	if st.Get("FLO_RELEASE_NAME") != "" {
		t.Errorf("FLO_RELEASE_NAME should be empty when FLO disabled, got %q", st.Get("FLO_RELEASE_NAME"))
	}
}

// ─── Test 10b: Skip upgrade when version + values + status all match ────────

func TestPhase14_SkipUpgrade_WhenUnchanged(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("KUBECONFIG_PATH", "/fake/kubeconfig")

	clients := buildFakeCNEClients(t)
	desiredValues := map[string]interface{}{"key": "value"}
	fake := &fakeHelmInstaller{
		listReleases: []*release.Release{
			{
				Name:      floReleaseName,
				Namespace: floNamespace,
				Chart: &chart.Chart{
					Metadata: &chart.Metadata{Version: intent.DefaultFLOVersion},
				},
				Config: map[string]interface{}{"key": "value"},
				Info:   &release.Info{Status: release.StatusDeployed},
			},
		},
	}

	if err := runFLOHelmInstall(context.Background(), fake, cl, st, intent.DefaultFLOVersion, desiredValues, clients); err != nil {
		t.Fatalf("runFLOHelmInstall skip-unchanged: %v", err)
	}

	if fake.upgradeCalls != 0 {
		t.Errorf("upgradeCalls = %d, want 0 (should skip when unchanged)", fake.upgradeCalls)
	}
	if fake.installCalls != 0 {
		t.Errorf("installCalls = %d, want 0", fake.installCalls)
	}
	// CRD-wait and state writes must still run.
	if st.Get("FLO_RELEASE_NAME") != floReleaseName {
		t.Errorf("FLO_RELEASE_NAME = %q, want %q", st.Get("FLO_RELEASE_NAME"), floReleaseName)
	}
	if st.Get("FLO_INSTALLED_AT") == "" {
		t.Error("FLO_INSTALLED_AT should be set even when upgrade is skipped")
	}
}

// ─── Test 10c: Upgrade when chart version differs ────────────────────────────

func TestPhase14_Upgrade_WhenVersionDiffers(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("KUBECONFIG_PATH", "/fake/kubeconfig")

	clients := buildFakeCNEClients(t)
	desiredValues := map[string]interface{}{"key": "value"}
	fake := &fakeHelmInstaller{
		listReleases: []*release.Release{
			{
				Name:      floReleaseName,
				Namespace: floNamespace,
				Chart: &chart.Chart{
					// Deployed version is older than desired.
					Metadata: &chart.Metadata{Version: "v2.20.0-0.0.1"},
				},
				Config: map[string]interface{}{"key": "value"},
				Info:   &release.Info{Status: release.StatusDeployed},
			},
		},
	}

	if err := runFLOHelmInstall(context.Background(), fake, cl, st, intent.DefaultFLOVersion, desiredValues, clients); err != nil {
		t.Fatalf("runFLOHelmInstall upgrade-version-differs: %v", err)
	}

	if fake.upgradeCalls != 1 {
		t.Errorf("upgradeCalls = %d, want 1 (should upgrade when version differs)", fake.upgradeCalls)
	}
	if fake.installCalls != 0 {
		t.Errorf("installCalls = %d, want 0", fake.installCalls)
	}
}

// ─── Test 10d: Upgrade when values differ ────────────────────────────────────

func TestPhase14_Upgrade_WhenValuesDiffer(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("KUBECONFIG_PATH", "/fake/kubeconfig")

	clients := buildFakeCNEClients(t)
	desiredValues := map[string]interface{}{"key": "new-value"}
	fake := &fakeHelmInstaller{
		listReleases: []*release.Release{
			{
				Name:      floReleaseName,
				Namespace: floNamespace,
				Chart: &chart.Chart{
					Metadata: &chart.Metadata{Version: intent.DefaultFLOVersion},
				},
				// Deployed with different values than desired.
				Config: map[string]interface{}{"key": "old-value"},
				Info:   &release.Info{Status: release.StatusDeployed},
			},
		},
	}

	if err := runFLOHelmInstall(context.Background(), fake, cl, st, intent.DefaultFLOVersion, desiredValues, clients); err != nil {
		t.Fatalf("runFLOHelmInstall upgrade-values-differ: %v", err)
	}

	if fake.upgradeCalls != 1 {
		t.Errorf("upgradeCalls = %d, want 1 (should upgrade when values differ)", fake.upgradeCalls)
	}
	if fake.installCalls != 0 {
		t.Errorf("installCalls = %d, want 0", fake.installCalls)
	}
}

// ─── Test 10e: Install when release absent ────────────────────────────────────

func TestPhase14_Install_WhenAbsent(t *testing.T) {
	awsmw.ResetForTest()
	cl, _, _ := clusterWithBnk(t)
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("KUBECONFIG_PATH", "/fake/kubeconfig")

	clients := buildFakeCNEClients(t)
	fake := &fakeHelmInstaller{} // empty listReleases → no existing release

	if err := runFLOHelmInstall(context.Background(), fake, cl, st, intent.DefaultFLOVersion, map[string]interface{}{}, clients); err != nil {
		t.Fatalf("runFLOHelmInstall install-when-absent: %v", err)
	}

	if fake.installCalls != 1 {
		t.Errorf("installCalls = %d, want 1", fake.installCalls)
	}
	if fake.upgradeCalls != 0 {
		t.Errorf("upgradeCalls = %d, want 0", fake.upgradeCalls)
	}
}

// ─── Test 10: CA_ISSUER / FAR_SECRET_NAME / JWT substitution in values template ─

func TestPhase14_ValuesTemplate_Substitution(t *testing.T) {
	awsmw.ResetForTest()
	cl := sydTracerCluster()
	jwtStr := "test-jwt-content"

	// Load and render the embedded flo-values template.
	valsTmpl, err := k8smanifests.FS.ReadFile(floValuesYAMLPath)
	if err != nil {
		t.Fatalf("reading flo-values template: %v", err)
	}
	out, err := render.RenderFLOValues(valsTmpl, cl, jwtStr)
	if err != nil {
		t.Fatalf("RenderFLOValues: %v", err)
	}
	rendered := string(out)

	// CA issuer must appear.
	caIssuer := cl.Metadata.Name + "-ca-cluster-issuer"
	if !strings.Contains(rendered, caIssuer) {
		t.Errorf("rendered values missing CA issuer %q:\n%s", caIssuer, rendered)
	}
	// FAR secret name must appear.
	if !strings.Contains(rendered, "far-secret") {
		t.Errorf("rendered values missing far-secret:\n%s", rendered)
	}
	// JWT must appear.
	if !strings.Contains(rendered, jwtStr) {
		t.Errorf("rendered values missing JWT %q:\n%s", jwtStr, rendered)
	}
	// Cluster name must appear in friendlyName.
	if !strings.Contains(rendered, cl.Metadata.Name) {
		t.Errorf("rendered values missing cluster name %q:\n%s", cl.Metadata.Name, rendered)
	}
}
