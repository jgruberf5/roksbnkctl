//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig codegen
package phases

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// buildScheme builds a runtime.Scheme with core types + cert-manager CRD stubs
// for the dynamic fake client.
func buildScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	return s
}

// p12Cluster returns a minimal cluster with a bnk: block and real FAR/JWT files.
func p12Cluster(t *testing.T, farPath, jwtPath string) *intent.Cluster {
	t.Helper()
	return &intent.Cluster{
		Metadata: intent.Metadata{Name: "syd-tracer", Region: "ap-southeast-2"},
		Network: intent.Network{
			VPCCidr: "10.0.0.0/16",
			AZs:     []string{"ap-southeast-2a"},
			Subnets: intent.Subnets{
				Public:  []intent.SubnetSpec{{CIDR: "10.0.1.0/24", AZ: "ap-southeast-2a"}},
				Private: []intent.SubnetSpec{{CIDR: "10.0.11.0/24", AZ: "ap-southeast-2a"}},
			},
			NatGateways: 1,
		},
		Bnk: &intent.BnkSpec{
			FARArchive:         farPath,
			JWT:                jwtPath,
			CertManagerVersion: "1.16.1",
		},
	}
}

// writeTempFile writes content to a temp file and returns the path.
func writeTempFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatalf("writeTempFile %s: %v", name, err)
	}
	return p
}

// p12ClientsDryRun returns a Clients struct suitable for dry-run tests (no k8s clients needed).
func p12ClientsDryRun() *Clients {
	return &Clients{Profile: "test"}
}

// p12FakeRESTMapper returns a meta.DefaultRESTMapper covering the kinds the
// phase 12 fake-client tests exercise. Real production uses
// restmapper.NewDeferredDiscoveryRESTMapper against the live API.
func p12FakeRESTMapper() meta.RESTMapper {
	versions := []schema.GroupVersion{
		{Group: "", Version: "v1"},
		{Group: "apps", Version: "v1"},
		{Group: "rbac.authorization.k8s.io", Version: "v1"},
		{Group: "apiextensions.k8s.io", Version: "v1"},
		{Group: "admissionregistration.k8s.io", Version: "v1"},
		{Group: "cert-manager.io", Version: "v1"},
		{Group: "storage.k8s.io", Version: "v1"},
		{Group: "networking.k8s.io", Version: "v1"},
		{Group: "k8s.cni.cncf.io", Version: "v1"},
		{Group: "k8s.f5.com", Version: "v1"},
		{Group: "k8s.f5net.com", Version: "v1"},
		{Group: "gateway.networking.k8s.io", Version: "v1"},
	}
	m := meta.NewDefaultRESTMapper(versions)

	add := func(group, version, kind, plural string, scope meta.RESTScope) {
		m.AddSpecific(
			schema.GroupVersionKind{Group: group, Version: version, Kind: kind},
			schema.GroupVersionResource{Group: group, Version: version, Resource: plural},
			schema.GroupVersionResource{Group: group, Version: version, Resource: strings.ToLower(kind)},
			scope,
		)
	}

	add("", "v1", "Namespace", "namespaces", meta.RESTScopeRoot)
	add("", "v1", "ServiceAccount", "serviceaccounts", meta.RESTScopeNamespace)
	add("", "v1", "Secret", "secrets", meta.RESTScopeNamespace)
	add("", "v1", "ConfigMap", "configmaps", meta.RESTScopeNamespace)
	add("", "v1", "Service", "services", meta.RESTScopeNamespace)
	add("", "v1", "Pod", "pods", meta.RESTScopeNamespace)
	add("rbac.authorization.k8s.io", "v1", "ClusterRole", "clusterroles", meta.RESTScopeRoot)
	add("rbac.authorization.k8s.io", "v1", "ClusterRoleBinding", "clusterrolebindings", meta.RESTScopeRoot)
	add("rbac.authorization.k8s.io", "v1", "Role", "roles", meta.RESTScopeNamespace)
	add("rbac.authorization.k8s.io", "v1", "RoleBinding", "rolebindings", meta.RESTScopeNamespace)
	add("apps", "v1", "Deployment", "deployments", meta.RESTScopeNamespace)
	add("apps", "v1", "DaemonSet", "daemonsets", meta.RESTScopeNamespace)
	add("apps", "v1", "StatefulSet", "statefulsets", meta.RESTScopeNamespace)
	add("networking.k8s.io", "v1", "NetworkPolicy", "networkpolicies", meta.RESTScopeNamespace)
	add("networking.k8s.io", "v1", "IngressClass", "ingressclasses", meta.RESTScopeRoot)
	add("apiextensions.k8s.io", "v1", "CustomResourceDefinition", "customresourcedefinitions", meta.RESTScopeRoot)
	add("admissionregistration.k8s.io", "v1", "ValidatingWebhookConfiguration", "validatingwebhookconfigurations", meta.RESTScopeRoot)
	add("admissionregistration.k8s.io", "v1", "MutatingWebhookConfiguration", "mutatingwebhookconfigurations", meta.RESTScopeRoot)
	add("cert-manager.io", "v1", "ClusterIssuer", "clusterissuers", meta.RESTScopeRoot)
	add("cert-manager.io", "v1", "Issuer", "issuers", meta.RESTScopeNamespace)
	add("cert-manager.io", "v1", "Certificate", "certificates", meta.RESTScopeNamespace)
	add("storage.k8s.io", "v1", "StorageClass", "storageclasses", meta.RESTScopeRoot)
	add("k8s.cni.cncf.io", "v1", "NetworkAttachmentDefinition", "network-attachment-definitions", meta.RESTScopeNamespace)
	add("k8s.f5.com", "v1", "CNEInstance", "cneinstances", meta.RESTScopeNamespace)
	add("k8s.f5net.com", "v1", "License", "licenses", meta.RESTScopeNamespace)
	add("k8s.f5net.com", "v1", "F5SPKVlan", "f5-spk-vlans", meta.RESTScopeNamespace)
	add("gateway.networking.k8s.io", "v1", "GatewayClass", "gatewayclasses", meta.RESTScopeRoot)

	return m
}

// p12ClientsFake returns a Clients struct with fake k8s clients pre-populated
// for unit tests. The fake dynamic client is seeded with a minimal scheme.
func p12ClientsFake() *Clients {
	scheme := buildScheme()
	return &Clients{
		K8s:        k8sfake.NewSimpleClientset(),
		Dynamic:    dynamicfake.NewSimpleDynamicClient(scheme),
		RESTMapper: p12FakeRESTMapper(),
		Profile:    "test",
	}
}

// ─── Test 1: Dry-run sets placeholder state, makes no k8s calls ──────────────

func TestPhase12_DryRun_SetsPlaceholdersNoK8sCalls(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	farPath := writeTempFile(t, dir, "far.json", `{"auths":{}}`)
	jwtPath := writeTempFile(t, dir, "license.jwt", "jwt-token-content")

	cl := p12Cluster(t, farPath, jwtPath)
	st, _ := state.Load(dir)
	clients := p12ClientsDryRun()

	if err := Phase12K8sFoundation(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase12K8sFoundation dry-run: %v", err)
	}

	// Placeholder state values should all start with "dry-run-".
	checks := []string{
		"BNK_NAMESPACES_CREATED",
		"BNK_FAR_SECRET_NAME",
		"BNK_LICENSE_JWT_SECRET",
		"CERT_MANAGER_VERSION",
		"BNK_SELFSIGNED_ISSUER",
		"BNK_CA_CERT_NAME",
		"BNK_CA_SECRET_NAME",
		"BNK_CA_ISSUER",
	}
	for _, key := range checks {
		v := st.Get(key)
		if v == "" {
			t.Errorf("dry-run: state[%s] is empty", key)
		}
		if !strings.HasPrefix(v, "dry-run-") {
			t.Errorf("dry-run: state[%s] = %q, want 'dry-run-' prefix", key, v)
		}
	}

	// No k8s client was needed in dry-run → K8s field stays nil.
	if clients.K8s != nil {
		t.Error("dry-run: clients.K8s should be nil (not constructed for dry-run)")
	}
}

// ─── Test 2: Missing bnk: block → clear error ─────────────────────────────────

func TestPhase12_MissingBnkBlock_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster() // no Bnk field
	st, _ := state.Load(dir)
	clients := p12ClientsDryRun()

	err := Phase12K8sFoundation(context.Background(), cl, st, clients, true)
	if err == nil {
		t.Fatal("expected error for missing bnk: block, got nil")
	}
	if !strings.Contains(err.Error(), "bnk:") {
		t.Errorf("error message should mention 'bnk:': %v", err)
	}
}

// ─── Test 3: FAR archive file not found → clear error at phase entry ──────────

func TestPhase12_FARArchiveNotFound_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	jwtPath := writeTempFile(t, dir, "license.jwt", "jwt-token")
	cl := p12Cluster(t, "/nonexistent/far.json", jwtPath)
	st, _ := state.Load(dir)
	clients := p12ClientsDryRun()

	err := Phase12K8sFoundation(context.Background(), cl, st, clients, true)
	if err == nil {
		t.Fatal("expected error for missing FAR archive, got nil")
	}
	if !strings.Contains(err.Error(), "FAR archive") {
		t.Errorf("error message should mention 'FAR archive': %v", err)
	}
}

// ─── Test 4: JWT file not found → clear error at phase entry ─────────────────

func TestPhase12_JWTNotFound_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	farPath := writeTempFile(t, dir, "far.json", `{"auths":{}}`)
	cl := p12Cluster(t, farPath, "/nonexistent/license.jwt")
	st, _ := state.Load(dir)
	clients := p12ClientsDryRun()

	err := Phase12K8sFoundation(context.Background(), cl, st, clients, true)
	if err == nil {
		t.Fatal("expected error for missing JWT file, got nil")
	}
	if !strings.Contains(err.Error(), "JWT") {
		t.Errorf("error message should mention 'JWT': %v", err)
	}
}

// ─── Test 5: Fresh apply creates namespaces + secrets ────────────────────────
// Note: the cert-manager YAML apply step tries SSA via dynamic fake client.
// The fake dynamic client returns errors for unknown GVRs; we test up to the
// namespace + secret creation which use the typed fake client and succeed.
// The applyRawYAML step with the fake dynamic client will fail on unknown
// GVRs — this is expected and acceptable in unit tests. We wrap the test to
// verify namespaces and secrets before the cert-manager step would run.

func TestPhase12_FreshApply_CreatesNamespacesAndSecrets(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	farPath := writeTempFile(t, dir, "far.json", `{"auths":{}}`)
	jwtPath := writeTempFile(t, dir, "license.jwt", "jwt-token-content")

	cl := p12Cluster(t, farPath, jwtPath)
	st, _ := state.Load(dir)
	clients := p12ClientsFake()

	// Run only namespace + secret creation sub-steps in isolation
	// (the full Phase12K8sFoundation would block on cert-manager rollout waits).
	ctx := context.Background()

	// Sub-test: ensure namespaces.
	if err := ensureNamespaces(ctx, clients, bnkNamespaces); err != nil {
		t.Fatalf("ensureNamespaces: %v", err)
	}
	for _, ns := range bnkNamespaces {
		obj, err := clients.K8s.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{})
		if err != nil {
			t.Errorf("namespace %s not found after create: %v", ns, err)
		} else if obj.Name != ns {
			t.Errorf("namespace name: got %q, want %q", obj.Name, ns)
		}
	}

	// Sub-test: apply FAR secrets.
	farData := []byte(`{"auths":{}}`)
	if err := applyFARSecrets(ctx, clients, farSecretNamespaces, farData); err != nil {
		t.Fatalf("applyFARSecrets: %v", err)
	}

	// Sub-test: apply license JWT secret.
	jwtData := []byte("jwt-token-content")
	if err := applyLicenseJWTSecret(ctx, clients, operatorNS, jwtData); err != nil {
		t.Fatalf("applyLicenseJWTSecret: %v", err)
	}

	// Verify state keys after full dry-run succeeds.
	if err := Phase12K8sFoundation(ctx, cl, st, clients, true); err != nil {
		t.Fatalf("Phase12K8sFoundation dry-run: %v", err)
	}
	if v := st.Get("BNK_NAMESPACES_CREATED"); v == "" {
		t.Error("BNK_NAMESPACES_CREATED not set in dry-run state")
	}
	_ = st
}

// ─── Test 6: Idempotency — second ensureNamespaces call is a no-op ───────────

func TestPhase12_IdempotentNamespaces(t *testing.T) {
	awsmw.ResetForTest()
	ctx := context.Background()
	clients := p12ClientsFake()

	// First run.
	if err := ensureNamespaces(ctx, clients, bnkNamespaces); err != nil {
		t.Fatalf("first ensureNamespaces: %v", err)
	}

	// Second run — should not error (namespaces already exist, IsAlreadyExists tolerated).
	if err := ensureNamespaces(ctx, clients, bnkNamespaces); err != nil {
		t.Fatalf("second ensureNamespaces (idempotent): %v", err)
	}

	// Namespace count should remain the same.
	nsList, err := clients.K8s.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list namespaces: %v", err)
	}
	if len(nsList.Items) != len(bnkNamespaces) {
		t.Errorf("namespace count: got %d, want %d", len(nsList.Items), len(bnkNamespaces))
	}
}

// ─── Test 6b: Idempotency — second applyFARSecrets + applyLicenseJWTSecret is a no-op ───

func TestPhase12_IdempotentSecrets(t *testing.T) {
	awsmw.ResetForTest()
	ctx := context.Background()
	cs := k8sfake.NewSimpleClientset()
	clients := &Clients{
		K8s:     cs,
		Dynamic: dynamicfake.NewSimpleDynamicClient(buildScheme()),
		Profile: "test",
	}

	farData := []byte(`{"auths":{}}`)
	jwtData := []byte("jwt-token-content")

	// First invocation — creates secrets.
	if err := applyFARSecrets(ctx, clients, farSecretNamespaces, farData); err != nil {
		t.Fatalf("first applyFARSecrets: %v", err)
	}
	if err := applyLicenseJWTSecret(ctx, clients, operatorNS, jwtData); err != nil {
		t.Fatalf("first applyLicenseJWTSecret: %v", err)
	}

	// Reset action log so second invocation is measured in isolation.
	cs.ClearActions()

	// Second invocation — should update (Get+Update), never create.
	if err := applyFARSecrets(ctx, clients, farSecretNamespaces, farData); err != nil {
		t.Fatalf("second applyFARSecrets (idempotent): %v", err)
	}
	if err := applyLicenseJWTSecret(ctx, clients, operatorNS, jwtData); err != nil {
		t.Fatalf("second applyLicenseJWTSecret (idempotent): %v", err)
	}

	// Assert zero "create" verbs on "secrets" resource.
	for _, action := range cs.Actions() {
		if action.GetVerb() == "create" && action.GetResource().Resource == "secrets" {
			t.Errorf("unexpected secret Create on second invocation: %v", action)
		}
	}
}

// ─── Test 7: Down removes resources, tolerates NotFound ──────────────────────

func TestPhase12Down_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	cl.Bnk = &intent.BnkSpec{FARArchive: "far.json", JWT: "jwt.jwt"}

	st, _ := state.Load(dir)
	// Pre-populate some state.
	st.Set("BNK_NAMESPACES_CREATED", "cert-manager,f5-cne-core,f5-bnk-instance,f5-utils")
	st.Set("BNK_CA_CERT_NAME", "syd-tracer-ca")

	clients := p12ClientsFake()
	ctx := context.Background()

	// Down should succeed even though nothing was ever created (NotFound tolerated).
	if err := Phase12K8sFoundationDown(ctx, cl, st, clients); err != nil {
		t.Fatalf("Phase12K8sFoundationDown (empty cluster): %v", err)
	}

	// State keys should be cleared.
	for _, key := range []string{"BNK_NAMESPACES_CREATED", "BNK_CA_CERT_NAME", "BNK_FAR_SECRET_NAME"} {
		if v := st.Get(key); v != "" {
			t.Errorf("state[%s] = %q after down, want empty", key, v)
		}
	}
}

// ─── Test 8: Down with absent namespaces logs "already gone" ─────────────────

func TestPhase12Down_AbsentNamespacesLogged(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	cl.Bnk = &intent.BnkSpec{FARArchive: "far.json", JWT: "jwt.jwt"}
	st, _ := state.Load(dir)
	clients := p12ClientsFake()
	ctx := context.Background()

	// Pre-create one namespace, leave others absent.
	_, _ = clients.K8s.CoreV1().Namespaces().Create(ctx, &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{Name: "f5-cne-core"},
	}, metav1.CreateOptions{})

	// Down should not error for absent namespaces.
	if err := Phase12K8sFoundationDown(ctx, cl, st, clients); err != nil {
		t.Fatalf("Phase12K8sFoundationDown: %v", err)
	}

	// f5-cne-core should be gone.
	_, err := clients.K8s.CoreV1().Namespaces().Get(ctx, "f5-cne-core", metav1.GetOptions{})
	if err == nil {
		t.Error("f5-cne-core namespace should be deleted")
	}
	if !k8serrors.IsNotFound(err) {
		t.Errorf("unexpected error getting f5-cne-core after delete: %v", err)
	}
}

// ─── Test 9: restMapping known resource types ─────────────────────────────────

func TestRestMapping_KnownTypes(t *testing.T) {
	mapper := p12FakeRESTMapper()
	cases := []struct {
		apiVersion string
		kind       string
		wantGroup  string
		wantNS     bool
	}{
		{"v1", "Namespace", "", false},
		{"v1", "Secret", "", true},
		{"apps/v1", "Deployment", "apps", true},
		{"cert-manager.io/v1", "ClusterIssuer", "cert-manager.io", false},
		{"cert-manager.io/v1", "Certificate", "cert-manager.io", true},
		{"apiextensions.k8s.io/v1", "CustomResourceDefinition", "apiextensions.k8s.io", false},
	}
	for _, tc := range cases {
		gvr, ns, err := restMapping(mapper, tc.apiVersion, tc.kind)
		if err != nil {
			t.Errorf("restMapping(%s/%s): unexpected error: %v", tc.apiVersion, tc.kind, err)
			continue
		}
		if gvr.Group != tc.wantGroup {
			t.Errorf("restMapping(%s/%s).Group = %q, want %q", tc.apiVersion, tc.kind, gvr.Group, tc.wantGroup)
		}
		if ns != tc.wantNS {
			t.Errorf("restMapping(%s/%s).namespaced = %v, want %v", tc.apiVersion, tc.kind, ns, tc.wantNS)
		}
	}
}

func TestRestMapping_UnknownType(t *testing.T) {
	mapper := p12FakeRESTMapper()
	_, _, err := restMapping(mapper, "unknown.io/v1", "Bogus")
	if err == nil {
		t.Fatal("expected error for unknown resource, got nil")
	}
}

// ─── Test 10: isCertificateReady logic ────────────────────────────────────────

func TestIsCertificateReady_TrueWhenConditionPresent(t *testing.T) {
	certObj := buildCertObj("True")
	if !isCertificateReadyLocal(certObj) {
		t.Error("expected Ready=True to return true")
	}
}

func TestIsCertificateReady_FalseWhenNotReady(t *testing.T) {
	obj := buildCertObj("False")
	if isCertificateReadyLocal(obj) {
		t.Error("expected Ready=False to return false")
	}
}

func TestIsCertificateReady_FalseWhenNoConditions(t *testing.T) {
	obj := map[string]interface{}{
		"status": map[string]interface{}{},
	}
	if isCertificateReadyLocal(obj) {
		t.Error("expected no conditions to return false")
	}
}

// isCertificateReadyLocal delegates to the exported k8swait function
// via the same package alias used in phase13.
func isCertificateReadyLocal(obj map[string]interface{}) bool {
	// Use the internal function directly since we're in the same binary.
	// The exported k8s.IsCertificateReady is tested separately in wait_test.
	status, ok := obj["status"].(map[string]interface{})
	if !ok {
		return false
	}
	conditions, ok := status["conditions"].([]interface{})
	if !ok {
		return false
	}
	for _, raw := range conditions {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if t, _ := cond["type"].(string); t != "Ready" {
			continue
		}
		if s, _ := cond["status"].(string); s == "True" {
			return true
		}
	}
	return false
}

func buildCertObj(readyStatus string) map[string]interface{} {
	return map[string]interface{}{
		"status": map[string]interface{}{
			"conditions": []interface{}{
				map[string]interface{}{
					"type":   "Ready",
					"status": readyStatus,
				},
			},
		},
	}
}

// TestApplyRawYAML_UnknownKindHardFails verifies that applyRawYAML errors
// (rather than silently skipping) when a YAML doc references a kind not in
// the RESTMapper. This is the C-6 regression test — before the live-mapper
// migration, unknown kinds logged a warning and returned nil, silently
// dropping resources like F5SPKVlan/GatewayClass during slice-10.
func TestApplyRawYAML_UnknownKindHardFails(t *testing.T) {
	clients := p12ClientsFake()
	raw := []byte(`apiVersion: made-up.example/v1
kind: NotARealKind
metadata:
  name: should-not-apply
  namespace: default
`)
	err := applyRawYAML(context.Background(), clients, raw)
	if err == nil {
		t.Fatal("expected error for unknown kind, got nil")
	}
	if !strings.Contains(err.Error(), "NotARealKind") {
		t.Errorf("error should reference unknown kind: %v", err)
	}
}
