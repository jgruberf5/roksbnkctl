//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig codegen
package phases

import (
	"context"
	"strings"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
	k8swait "github.com/JLCode-tech/awsbnkctl/internal/k8s"
	"github.com/JLCode-tech/awsbnkctl/internal/k8s/render"
)

// buildReadyCertificate builds an unstructured Certificate CR with Ready=True.
func buildReadyCertificate(name, ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": ns,
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "True",
					},
				},
			},
		},
	}
}

// buildNotReadyCertificate builds an unstructured Certificate CR with Ready=False.
func buildNotReadyCertificate(name, ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "cert-manager.io/v1",
			"kind":       "Certificate",
			"metadata": map[string]interface{}{
				"name":      name,
				"namespace": ns,
			},
			"status": map[string]interface{}{
				"conditions": []interface{}{
					map[string]interface{}{
						"type":   "Ready",
						"status": "False",
					},
				},
			},
		},
	}
}

// buildReadyDeployment builds a fake appsv1.Deployment in ready state.
func buildReadyDeployment(name, ns string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{
			Replicas:          1,
			AvailableReplicas: 1,
		},
	}
}

// buildNotReadyDeployment builds a fake appsv1.Deployment in not-ready state.
func buildNotReadyDeployment(name, ns string) *appsv1.Deployment {
	replicas := int32(1)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       appsv1.DeploymentSpec{Replicas: &replicas},
		Status: appsv1.DeploymentStatus{
			Replicas:          1,
			AvailableReplicas: 0,
		},
	}
}

// p13FullHappyClients returns a Clients struct fully seeded for a happy-path
// Phase13 test: namespaces exist, deployments ready, CA cert ready, FAR secrets
// present, FLO deployment ready, CNE CRD present, OTEL certs ready.
func p13FullHappyClients(t *testing.T) *Clients {
	t.Helper()
	cl := sydTracerCluster()
	vars := render.CertChainVarsFromCluster(cl)

	// Seed fake k8s clientset.
	k8sObjects := []runtime.Object{}

	// Namespaces.
	for _, ns := range bnkNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})
	}

	// cert-manager Deployments (ready).
	for _, dep := range []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"} {
		k8sObjects = append(k8sObjects, buildReadyDeployment(dep, certManagerNS))
	}

	// FLO Deployment (ready) — phase 13 checks this when FLO is enabled (default).
	k8sObjects = append(k8sObjects, buildReadyDeployment(floDeployName, operatorNS))

	// FAR secrets in all four namespaces.
	for _, ns := range farSecretNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: ns},
			Type:       corev1.SecretTypeDockerConfigJson,
		})
	}

	cs := k8sfake.NewSimpleClientset(k8sObjects...)

	// Seed dynamic fake client: CA cert + CNE CRD + OTEL certs.
	scheme := buildScheme()
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	cneCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": cneCRDName},
		},
	}
	dynObjects := []runtime.Object{
		buildReadyCertificate(vars.CACertName, certManagerNS),
		cneCRD,
		buildReadyCertificate(otelSvrCertName, operatorNS),
		buildReadyCertificate(otelF5IngCertName, operatorNS),
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
	}, dynObjects...)

	return &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}
}

// ─── Test 1: All-ready happy path ────────────────────────────────────────────

func TestPhase13_AllReady_HappyPath(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	st, _ := state.Load(dir)
	clients := p13FullHappyClients(t)

	if err := Phase13Postflight(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase13Postflight happy path: %v", err)
	}
}

// ─── Test 2: Dry-run logs and returns nil ────────────────────────────────────

func TestPhase13_DryRun_ReturnsNil(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	st, _ := state.Load(dir)
	clients := &Clients{Profile: "test"} // no k8s clients needed

	if err := Phase13Postflight(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase13Postflight dry-run: %v", err)
	}
}

// ─── Test 3: Namespace missing → error naming the namespace ──────────────────

func TestPhase13_NamespaceMissing_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	st, _ := state.Load(dir)

	// Create all namespaces except f5-cne-core.
	k8sObjects := []runtime.Object{}
	for _, ns := range bnkNamespaces {
		if ns == "f5-cne-core" {
			continue
		}
		k8sObjects = append(k8sObjects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	cs := k8sfake.NewSimpleClientset(k8sObjects...)
	scheme := buildScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)
	clients := &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}

	err := Phase13Postflight(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error for missing namespace, got nil")
	}
	if !strings.Contains(err.Error(), "f5-cne-core") {
		t.Errorf("error should name the missing namespace 'f5-cne-core': %v", err)
	}
}

// ─── Test 4: cert-manager Deployment not ready → error naming deployment ─────

func TestPhase13_CertManagerDeploymentNotReady_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	st, _ := state.Load(dir)

	k8sObjects := []runtime.Object{}
	for _, ns := range bnkNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	// cert-manager-cainjector is not ready.
	k8sObjects = append(k8sObjects,
		buildReadyDeployment("cert-manager", certManagerNS),
		buildNotReadyDeployment("cert-manager-cainjector", certManagerNS),
		buildReadyDeployment("cert-manager-webhook", certManagerNS),
	)
	cs := k8sfake.NewSimpleClientset(k8sObjects...)
	scheme := buildScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)
	clients := &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}

	err := Phase13Postflight(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error for not-ready deployment, got nil")
	}
	if !strings.Contains(err.Error(), "cert-manager-cainjector") {
		t.Errorf("error should name the not-ready deployment: %v", err)
	}
}

// ─── Test 5: CA Certificate not ready → error ────────────────────────────────

func TestPhase13_CACertNotReady_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	vars := render.CertChainVarsFromCluster(cl)
	st, _ := state.Load(dir)

	k8sObjects := []runtime.Object{}
	for _, ns := range bnkNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	for _, dep := range []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"} {
		k8sObjects = append(k8sObjects, buildReadyDeployment(dep, certManagerNS))
	}
	cs := k8sfake.NewSimpleClientset(k8sObjects...)

	scheme := buildScheme()
	// Seed with a NOT-ready certificate.
	dynObjects := []runtime.Object{buildNotReadyCertificate(vars.CACertName, certManagerNS)}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
	}, dynObjects...)
	clients := &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}

	err := Phase13Postflight(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error for not-ready CA cert, got nil")
	}
	if !strings.Contains(err.Error(), "Ready condition is not True") {
		t.Errorf("error should mention 'Ready condition is not True': %v", err)
	}
}

// ─── Test 6: FAR secret missing → error naming namespace ─────────────────────

func TestPhase13_FARSecretMissing_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	vars := render.CertChainVarsFromCluster(cl)
	st, _ := state.Load(dir)

	k8sObjects := []runtime.Object{}
	for _, ns := range bnkNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	for _, dep := range []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"} {
		k8sObjects = append(k8sObjects, buildReadyDeployment(dep, certManagerNS))
	}
	// FAR secret in only f5-cne-core, missing from others.
	k8sObjects = append(k8sObjects, &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: "f5-cne-core"},
	})
	cs := k8sfake.NewSimpleClientset(k8sObjects...)

	scheme := buildScheme()
	dynObjects := []runtime.Object{buildReadyCertificate(vars.CACertName, certManagerNS)}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
	}, dynObjects...)
	clients := &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}

	err := Phase13Postflight(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error for missing FAR secret, got nil")
	}
	// Should name a namespace where secret is missing.
	if !strings.Contains(err.Error(), "FAR secret") {
		t.Errorf("error should mention 'FAR secret': %v", err)
	}
}

// ─── Test 7: No state written in postflight ───────────────────────────────────

func TestPhase13_WritesNoState(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	st, _ := state.Load(dir)
	clients := p13FullHappyClients(t)

	// Mark original state size.
	stBefore := map[string]string{
		"SOME_KEY": "some_val",
	}
	for k, v := range stBefore {
		st.Set(k, v)
	}

	if err := Phase13Postflight(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase13Postflight: %v", err)
	}

	// Check existing keys are unchanged.
	if st.Get("SOME_KEY") != "some_val" {
		t.Errorf("Phase13 should not modify existing state keys")
	}
}

// ─── Test 8: Phase13 with FLO checks enabled — happy path ──────────────────

// p13FloFullHappyClients returns a Clients struct seeded for Phase13 with FLO
// checks enabled: all slice-5 objects plus FLO Deployment, CNE CRD, and OTEL
// certs.
func p13FloFullHappyClients(t *testing.T) *Clients {
	t.Helper()
	cl := sydTracerCluster()
	vars := render.CertChainVarsFromCluster(cl)

	k8sObjects := []runtime.Object{}
	for _, ns := range bnkNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{Name: ns},
		})
	}
	for _, dep := range []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"} {
		k8sObjects = append(k8sObjects, buildReadyDeployment(dep, certManagerNS))
	}
	// FLO deployment ready.
	k8sObjects = append(k8sObjects, buildReadyDeployment(floDeployName, operatorNS))
	for _, ns := range farSecretNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: ns},
			Type:       corev1.SecretTypeDockerConfigJson,
		})
	}
	cs := k8sfake.NewSimpleClientset(k8sObjects...)

	// Dynamic: CA cert + CNE CRD + OTEL certs.
	scheme := buildScheme()
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	cneCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": cneCRDName},
		},
	}
	otelSvr := buildReadyCertificate(otelSvrCertName, operatorNS)
	otelF5Ing := buildReadyCertificate(otelF5IngCertName, operatorNS)

	dynObjects := []runtime.Object{
		buildReadyCertificate(vars.CACertName, certManagerNS),
		cneCRD,
		otelSvr,
		otelF5Ing,
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
	}, dynObjects...)

	return &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}
}

func TestPhase13_WithFLO_HappyPath(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	// addons block absent → FLO defaults to enabled
	st, _ := state.Load(dir)
	clients := p13FloFullHappyClients(t)

	if err := Phase13Postflight(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase13Postflight FLO happy path: %v", err)
	}
}

// ─── Test 9: Phase13 FLO deployment not ready → error ──────────────────────

func TestPhase13_FLODeploymentNotReady_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	vars := render.CertChainVarsFromCluster(cl)
	st, _ := state.Load(dir)

	k8sObjects := []runtime.Object{}
	for _, ns := range bnkNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	for _, dep := range []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"} {
		k8sObjects = append(k8sObjects, buildReadyDeployment(dep, certManagerNS))
	}
	// FLO deployment NOT ready.
	k8sObjects = append(k8sObjects, buildNotReadyDeployment(floDeployName, operatorNS))
	for _, ns := range farSecretNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: ns},
			Type:       corev1.SecretTypeDockerConfigJson,
		})
	}
	cs := k8sfake.NewSimpleClientset(k8sObjects...)

	scheme := buildScheme()
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	cneCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": cneCRDName},
		},
	}
	dynObjects := []runtime.Object{buildReadyCertificate(vars.CACertName, certManagerNS), cneCRD}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
	}, dynObjects...)
	clients := &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}

	err := Phase13Postflight(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error for not-ready FLO deployment, got nil")
	}
	if !strings.Contains(err.Error(), floDeployName) {
		t.Errorf("error should mention FLO deploy name %q: %v", floDeployName, err)
	}
}

// ─── Test 10: Phase13 CNE CRD missing → error ───────────────────────────────

func TestPhase13_CNECRDMissing_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	vars := render.CertChainVarsFromCluster(cl)
	st, _ := state.Load(dir)

	k8sObjects := []runtime.Object{}
	for _, ns := range bnkNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	for _, dep := range []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"} {
		k8sObjects = append(k8sObjects, buildReadyDeployment(dep, certManagerNS))
	}
	k8sObjects = append(k8sObjects, buildReadyDeployment(floDeployName, operatorNS))
	for _, ns := range farSecretNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: ns},
			Type:       corev1.SecretTypeDockerConfigJson,
		})
	}
	cs := k8sfake.NewSimpleClientset(k8sObjects...)

	scheme := buildScheme()
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	// No CNE CRD seeded.
	dynObjects := []runtime.Object{buildReadyCertificate(vars.CACertName, certManagerNS)}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
	}, dynObjects...)
	clients := &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}

	err := Phase13Postflight(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error for missing CNE CRD, got nil")
	}
	if !strings.Contains(err.Error(), cneCRDName) {
		t.Errorf("error should mention CRD name %q: %v", cneCRDName, err)
	}
}

// ─── Test 11: Phase13 OTEL cert not ready → error ───────────────────────────

func TestPhase13_OTELCertNotReady_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	vars := render.CertChainVarsFromCluster(cl)
	st, _ := state.Load(dir)

	k8sObjects := []runtime.Object{}
	for _, ns := range bnkNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: ns}})
	}
	for _, dep := range []string{"cert-manager", "cert-manager-cainjector", "cert-manager-webhook"} {
		k8sObjects = append(k8sObjects, buildReadyDeployment(dep, certManagerNS))
	}
	k8sObjects = append(k8sObjects, buildReadyDeployment(floDeployName, operatorNS))
	for _, ns := range farSecretNamespaces {
		k8sObjects = append(k8sObjects, &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Name: farSecretName, Namespace: ns},
			Type:       corev1.SecretTypeDockerConfigJson,
		})
	}
	cs := k8sfake.NewSimpleClientset(k8sObjects...)

	scheme := buildScheme()
	crdGVR := schema.GroupVersionResource{Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions"}
	cneCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": cneCRDName},
		},
	}
	// otelSvr NOT ready.
	dynObjects := []runtime.Object{
		buildReadyCertificate(vars.CACertName, certManagerNS),
		cneCRD,
		buildNotReadyCertificate(otelSvrCertName, operatorNS),
		buildReadyCertificate(otelF5IngCertName, operatorNS),
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
	}, dynObjects...)
	clients := &Clients{K8s: cs, Dynamic: dyn, Profile: "test"}

	err := Phase13Postflight(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error for not-ready OTEL cert, got nil")
	}
	if !strings.Contains(err.Error(), otelSvrCertName) {
		t.Errorf("error should mention OTEL cert name %q: %v", otelSvrCertName, err)
	}
}

// ─── Test 12: Phase13 FLO disabled → skips FLO/CRD/OTEL checks ─────────────

func TestPhase13_FLODisabled_SkipsFLOChecks(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	disabled := false
	cl.Addons = &intent.AddonsSpec{
		Flo: &intent.FloSpec{Enabled: &disabled},
	}
	st, _ := state.Load(dir)
	// Use a clients with only slice-5 objects (no FLO/CRD/OTEL) — should pass
	// since FLO checks are skipped.
	clients := p13FullHappyClients(t)

	if err := Phase13Postflight(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase13Postflight with FLO disabled: %v", err)
	}
}

// ─── Test 13: CNEInstance + License checks pass when CNEINSTANCE_READY_AT set ─

// p13FullHappyClientsWithActivation returns a Clients struct seeded with
// CNEInstance (Ready) + License (Active) in addition to the full base set.
// Used by the slice-7c postflight tests.
func p13FullHappyClientsWithActivation(t *testing.T) *Clients {
	t.Helper()
	base := p13FullHappyClients(t)

	// Build an additional dynamic client that also includes CNEInstance + License.
	// We need to include the base dynamic client's objects plus the new ones.
	cl := sydTracerCluster()
	vars := render.CertChainVarsFromCluster(cl)

	crdGVR := schema.GroupVersionResource{
		Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
	}
	cneCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": cneCRDName},
		},
	}
	cneInst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.f5.com/v1",
			"kind":       "CNEInstance",
			"metadata":   map[string]interface{}{"name": cl.Metadata.Name + "-bnk", "namespace": InstanceNamespace},
			"status":     map[string]interface{}{"state": "Ready"},
		},
	}
	licInst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.f5net.com/v1",
			"kind":       "License",
			"metadata":   map[string]interface{}{"name": licenseCRName, "namespace": OperatorNamespace},
			"status":     map[string]interface{}{"state": "Active"},
		},
	}

	scheme := buildScheme()
	// Register CNEInstance + License kinds in scheme.
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "k8s.f5.com", Version: "v1", Kind: "CNEInstance",
	}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "k8s.f5.com", Version: "v1", Kind: "CNEInstanceList",
	}, &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "k8s.f5net.com", Version: "v1", Kind: "License",
	}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "k8s.f5net.com", Version: "v1", Kind: "LicenseList",
	}, &unstructured.UnstructuredList{})

	dynObjects := []runtime.Object{
		buildReadyCertificate(vars.CACertName, certManagerNS),
		cneCRD,
		buildReadyCertificate(otelSvrCertName, operatorNS),
		buildReadyCertificate(otelF5IngCertName, operatorNS),
		cneInst,
		licInst,
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
		cneinstanceGVR:         "CNEInstanceList",
		licenseGVR:             "LicenseList",
	}, dynObjects...)

	return &Clients{K8s: base.K8s, Dynamic: dyn, Profile: "test"}
}

func TestPhase13_CNEInstanceAndLicense_HappyPath(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	st, _ := state.Load(dir)
	// Simulate Phase 25 success.
	st.Set("CNEINSTANCE_READY_AT", "2026-05-22T10:00:00Z")

	clients := p13FullHappyClientsWithActivation(t)

	if err := Phase13Postflight(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase13Postflight with activation: %v", err)
	}
}

// TestPhase13_CNEInstanceNotReady verifies that postflight fails when
// CNEInstance.status.state is not Ready/Running.
func TestPhase13_CNEInstanceNotReady(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	st, _ := state.Load(dir)
	st.Set("CNEINSTANCE_READY_AT", "2026-05-22T10:00:00Z")

	// Override CNEInstance with Pending state.
	base := p13FullHappyClientsWithActivation(t)
	// Patch the CNEInstance in the dynamic client with Pending state.
	// Re-build with a not-ready CNEInstance.
	vars := render.CertChainVarsFromCluster(cl)
	crdGVR := schema.GroupVersionResource{
		Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
	}
	cneCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": cneCRDName},
		},
	}
	cneInst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.f5.com/v1",
			"kind":       "CNEInstance",
			"metadata":   map[string]interface{}{"name": cl.Metadata.Name + "-bnk", "namespace": InstanceNamespace},
			"status":     map[string]interface{}{"state": "Pending"}, // NOT ready
		},
	}
	licInst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.f5net.com/v1",
			"kind":       "License",
			"metadata":   map[string]interface{}{"name": licenseCRName, "namespace": OperatorNamespace},
			"status":     map[string]interface{}{"state": "Active"},
		},
	}
	scheme := buildScheme()
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "k8s.f5.com", Version: "v1", Kind: "CNEInstance"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "k8s.f5.com", Version: "v1", Kind: "CNEInstanceList"}, &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "k8s.f5net.com", Version: "v1", Kind: "License"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "k8s.f5net.com", Version: "v1", Kind: "LicenseList"}, &unstructured.UnstructuredList{})
	dynObjects := []runtime.Object{
		buildReadyCertificate(vars.CACertName, certManagerNS),
		cneCRD,
		buildReadyCertificate(otelSvrCertName, operatorNS),
		buildReadyCertificate(otelF5IngCertName, operatorNS),
		cneInst,
		licInst,
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
		cneinstanceGVR:         "CNEInstanceList",
		licenseGVR:             "LicenseList",
	}, dynObjects...)
	clients := &Clients{K8s: base.K8s, Dynamic: dyn, Profile: "test"}

	err := Phase13Postflight(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error for CNEInstance not Ready, got nil")
	}
	if !strings.Contains(err.Error(), "Pending") {
		t.Errorf("error should mention Pending state: %v", err)
	}
}

// TestPhase13_CNEInstanceEmptyStateSubConditionsTrue_Passes verifies the H1 fix:
// when status.state is empty but F5TmmAvailable + CNEControllerAvailable are both
// True (Sydney gold-reference shape), postflight must pass.
func TestPhase13_CNEInstanceEmptyStateSubConditionsTrue_Passes(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	st, _ := state.Load(dir)
	st.Set("CNEINSTANCE_READY_AT", "2026-05-22T10:00:00Z")

	base := p13FullHappyClientsWithActivation(t)

	// Re-build dynamic client with Sydney-shape CNEInstance: state="" + sub-conditions True.
	vars := render.CertChainVarsFromCluster(cl)
	crdGVR := schema.GroupVersionResource{
		Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
	}
	cneCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": cneCRDName},
		},
	}
	cneInst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.f5.com/v1",
			"kind":       "CNEInstance",
			"metadata":   map[string]interface{}{"name": cl.Metadata.Name + "-bnk", "namespace": InstanceNamespace},
			"status": map[string]interface{}{
				"state": "",
				"conditions": []interface{}{
					map[string]interface{}{"type": "F5TmmAvailable", "status": "True"},
					map[string]interface{}{"type": "CNEControllerAvailable", "status": "True"},
				},
			},
		},
	}
	licInst := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.f5net.com/v1",
			"kind":       "License",
			"metadata":   map[string]interface{}{"name": licenseCRName, "namespace": OperatorNamespace},
			"status":     map[string]interface{}{"state": "Active"},
		},
	}
	scheme := buildScheme()
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "k8s.f5.com", Version: "v1", Kind: "CNEInstance"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "k8s.f5.com", Version: "v1", Kind: "CNEInstanceList"}, &unstructured.UnstructuredList{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "k8s.f5net.com", Version: "v1", Kind: "License"}, &unstructured.Unstructured{})
	scheme.AddKnownTypeWithName(schema.GroupVersionKind{Group: "k8s.f5net.com", Version: "v1", Kind: "LicenseList"}, &unstructured.UnstructuredList{})
	dynObjects := []runtime.Object{
		buildReadyCertificate(vars.CACertName, certManagerNS),
		cneCRD,
		buildReadyCertificate(otelSvrCertName, operatorNS),
		buildReadyCertificate(otelF5IngCertName, operatorNS),
		cneInst,
		licInst,
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		k8swait.CertificateGVR: "CertificateList",
		crdGVR:                 "CustomResourceDefinitionList",
		cneinstanceGVR:         "CNEInstanceList",
		licenseGVR:             "LicenseList",
	}, dynObjects...)
	clients := &Clients{K8s: base.K8s, Dynamic: dyn, Profile: "test"}

	if err := Phase13Postflight(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase13Postflight Sydney-shape (empty state + sub-conditions True): %v", err)
	}
}

// TestPhase13_SkipActivationPoll_Warning verifies that when CNEINSTANCE_READY_AT
// is absent (--skip-activation-poll), postflight logs a warning but succeeds.
func TestPhase13_SkipActivationPoll_Warning(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	cl := sydTracerCluster()
	st, _ := state.Load(dir)
	// CNEINSTANCE_READY_AT not set — simulates --skip-activation-poll.

	clients := p13FullHappyClients(t)
	if err := Phase13Postflight(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase13Postflight (skip-poll): %v", err)
	}
}

// ─── Helpers for import of k8swait in test (indirect use) ──────────────────

// Ensure the CertificateGVR is importable via k8swait alias.
func init() {
	_ = k8swait.CertificateGVR
}
