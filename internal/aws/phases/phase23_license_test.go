//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig codegen
package phases

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// licScheme returns a scheme for License + CRD kinds.
func licScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "k8s.f5net.com", Version: "v1", Kind: "License",
	}, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "k8s.f5net.com", Version: "v1", Kind: "LicenseList",
	}, &unstructured.UnstructuredList{})
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinition",
	}, &unstructured.Unstructured{})
	s.AddKnownTypeWithName(schema.GroupVersionKind{
		Group: "apiextensions.k8s.io", Version: "v1", Kind: "CustomResourceDefinitionList",
	}, &unstructured.UnstructuredList{})
	return s
}

// p23Cluster returns a cluster with a real JWT file for phase 23 tests.
func p23Cluster(t *testing.T) (*intent.Cluster, string) {
	t.Helper()
	dir := t.TempDir()
	jwtPath := filepath.Join(dir, "jwt.txt")
	if err := os.WriteFile(jwtPath, []byte("test.jwt.token\n"), 0o600); err != nil {
		t.Fatalf("write JWT: %v", err)
	}
	cl := p22Cluster()
	cl.Bnk.JWT = jwtPath
	return cl, dir
}

// buildLicenseCRD builds an unstructured CRD for licenses.k8s.f5net.com.
func buildLicenseCRD() *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata": map[string]interface{}{
				"name": licenseCRDName,
			},
		},
	}
}

var crdGVRForLicense = schema.GroupVersionResource{
	Group: "apiextensions.k8s.io", Version: "v1", Resource: "customresourcedefinitions",
}

// ─── Test 1: Dry-run ─────────────────────────────────────────────────────────

func TestPhase23_DryRun_SetsPlaceholders(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := p22Cluster()
	clients := &Clients{Profile: "test"}

	if err := Phase23License(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase23License dry-run: %v", err)
	}
	if st.Get("LICENSE_CRD_READY_AT") != "dry-run" {
		t.Errorf("LICENSE_CRD_READY_AT = %q, want dry-run", st.Get("LICENSE_CRD_READY_AT"))
	}
	if st.Get("LICENSE_NAME") != licenseCRName {
		t.Errorf("LICENSE_NAME = %q, want %q", st.Get("LICENSE_NAME"), licenseCRName)
	}
}

// ─── Test 2: CRD is findable in fake client ───────────────────────────────────

// TestPhase23_CRDFindable verifies that our licScheme + fake dynamic client
// correctly returns the license CRD. This simulates the WaitForCRDExists happy path.
func TestPhase23_CRDFindable(t *testing.T) {
	awsmw.ResetForTest()
	_, stateDir := p23Cluster(t)
	_ = stateDir

	scheme := licScheme()
	crd := buildLicenseCRD()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		crdGVRForLicense: "CustomResourceDefinitionList",
		licenseGVR:       "LicenseList",
	}, crd)

	// Verify CRD is findable — simulates WaitForCRDExists happy path.
	_, err := dyn.Resource(crdGVRForLicense).Get(context.Background(), licenseCRDName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("CRD not found in fake client: %v", err)
	}
}

// ─── Test 3: nil Dynamic client error ─────────────────────────────────────────

// TestPhase23_NilDynamic_ReturnsError verifies that missing Dynamic client is caught.
func TestPhase23_NilDynamic_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := p22Cluster()
	clients := &Clients{Profile: "test", Dynamic: nil}

	err := Phase23License(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error for nil Dynamic, got nil")
	}
}

// ─── Test 4: Idempotent re-apply (dry-run path) ───────────────────────────────

func TestPhase23_Idempotent_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := p22Cluster()
	clients := &Clients{Profile: "test"}

	for i := 0; i < 2; i++ {
		if err := Phase23License(context.Background(), cl, st, clients, true); err != nil {
			t.Fatalf("Phase23 run %d: %v", i+1, err)
		}
	}
}

// ─── Test 5: Down deletes + tolerates NotFound ───────────────────────────────

func TestPhase23_Down_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := p22Cluster()

	scheme := licScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		licenseGVR: "LicenseList",
	})
	clients := &Clients{Dynamic: dyn, Profile: "test"}

	if err := Phase23LicenseDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase23LicenseDown: %v", err)
	}
	if st.Get("LICENSE_NAME") != "" {
		t.Errorf("LICENSE_NAME should be cleared, got %q", st.Get("LICENSE_NAME"))
	}
}

func TestPhase23_Down_NilDynamic_Succeeds(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("LICENSE_NAME", "bnk-license")
	cl := p22Cluster()
	clients := &Clients{Profile: "test", Dynamic: nil}

	if err := Phase23LicenseDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase23LicenseDown nil dynamic: %v", err)
	}
}
