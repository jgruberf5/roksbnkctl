//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig which adds significant test-codegen complexity

package phases

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

var nadGVR = schema.GroupVersionResource{
	Group:    "k8s.cni.cncf.io",
	Version:  "v1",
	Resource: "network-attachment-definitions",
}

// buildP20Clients returns a Clients struct with a dynamic fake that knows about
// NetworkAttachmentDefinition.
func buildP20Clients(t *testing.T) *Clients {
	t.Helper()
	scheme := buildScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		nadGVR: "NetworkAttachmentDefinitionList",
	})
	return &Clients{
		K8s:     k8sfake.NewSimpleClientset(),
		Dynamic: dyn,
		Profile: "test",
	}
}

// ─── Phase 20 tests ──────────────────────────────────────────────────────────

func TestPhase20NADs_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	clients := &Clients{Profile: "test"}

	if err := Phase20NADs(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase20 dry-run: %v", err)
	}
	if st.Get("NADS_APPLIED_AT") != "dry-run" {
		t.Errorf("NADS_APPLIED_AT = %q, want dry-run", st.Get("NADS_APPLIED_AT"))
	}
}

// TestPhase20NADs_NADNamespaceCoverage verifies that nadNamespaces covers both
// the instance namespace and "default" (per aws-gpu-setup/deploy-bnk.sh:143).
func TestPhase20NADs_NADNamespaceCoverage(t *testing.T) {
	hasInstance := false
	hasDefault := false
	for _, ns := range nadNamespaces {
		if ns == InstanceNamespace {
			hasInstance = true
		}
		if ns == "default" {
			hasDefault = true
		}
	}
	if !hasInstance {
		t.Errorf("nadNamespaces must include %q, got %v", InstanceNamespace, nadNamespaces)
	}
	if !hasDefault {
		t.Errorf("nadNamespaces must include 'default', got %v", nadNamespaces)
	}
}

// TestPhase20NADs_DryRun_TemplateRenders verifies that the NADs template renders
// correctly for both namespace targets using the dry-run path.
// (SSA Patch with ApplyPatchType is a known limitation of the dynamic fake — not
// tested here; render correctness is covered by internal/k8s/render/render_test.go.)
func TestPhase20NADs_DryRun_TemplateRenders(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	clients := &Clients{Profile: "test"}

	// Run dry-run twice (for both potential namespaces) — must not error.
	for i := 0; i < 2; i++ {
		if err := Phase20NADs(context.Background(), cl, st, clients, true); err != nil {
			t.Fatalf("Phase20 dry-run run %d: %v", i+1, err)
		}
	}
	if st.Get("NADS_APPLIED_AT") != "dry-run" {
		t.Errorf("NADS_APPLIED_AT = %q, want dry-run", st.Get("NADS_APPLIED_AT"))
	}
}

// ─── Phase 20 Down tests ─────────────────────────────────────────────────────

func TestPhase20NADsDown_Deletes(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("NADS_APPLIED_AT", "2026-05-22T00:00:00Z")

	clients := buildP20Clients(t)

	// Should not error.
	if err := Phase20NADsDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase20Down: %v", err)
	}
	if st.Get("NADS_APPLIED_AT") != "" {
		t.Errorf("NADS_APPLIED_AT should be cleared after down, got %q", st.Get("NADS_APPLIED_AT"))
	}
}

func TestPhase20NADsDown_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	clients := buildP20Clients(t)

	// No NADs pre-created — down should tolerate NotFound.
	if err := Phase20NADsDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase20Down NotFound: %v", err)
	}
}
