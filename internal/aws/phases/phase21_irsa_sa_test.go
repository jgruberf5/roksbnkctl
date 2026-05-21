//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig which adds significant test-codegen complexity

package phases

import (
	"context"
	"testing"

	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// buildP21Clients returns a Clients struct with a dynamic fake for phase21 tests.
func buildP21Clients(t *testing.T) *Clients {
	t.Helper()
	scheme := buildScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)
	return &Clients{
		K8s:     k8sfake.NewSimpleClientset(),
		Dynamic: dyn,
		Profile: "test",
	}
}

// ─── Phase 21 tests ──────────────────────────────────────────────────────────

func TestPhase21IRSASA_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster() // name is "tracer" (from testCluster())
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("CNE_IRSA_ROLE_ARN", "arn:aws:iam::111122223333:role/tracer-cne-controller-irsa")
	clients := &Clients{Profile: "test"}

	if err := Phase21IRSASA(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase21 dry-run: %v", err)
	}
	if st.Get("IRSA_SA_APPLIED_AT") != "dry-run" {
		t.Errorf("IRSA_SA_APPLIED_AT = %q, want dry-run", st.Get("IRSA_SA_APPLIED_AT"))
	}
	wantSA := cneSAName("tracer")
	if st.Get("CNE_SA_NAME") != wantSA {
		t.Errorf("CNE_SA_NAME = %q, want %q", st.Get("CNE_SA_NAME"), wantSA)
	}
}

func TestPhase21IRSASA_MissingRoleARN_Errors(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	// CNE_IRSA_ROLE_ARN deliberately missing.
	clients := &Clients{Profile: "test"}

	err := Phase21IRSASA(context.Background(), cl, st, clients, true)
	if err == nil {
		t.Fatal("expected error when CNE_IRSA_ROLE_ARN missing, got nil")
	}
}

// TestPhase21IRSASA_StateKeys verifies that the SA name is correctly derived
// and CNE_SA_NAME is set in dry-run mode (without calling applyRawYAML).
// SSA Patch is not tested with the dynamic fake — render + SSA correctness
// are exercised by render_test.go + integration tests.
func TestPhase21IRSASA_StateKeys(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster() // name is "tracer"
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("CNE_IRSA_ROLE_ARN", "arn:aws:iam::111122223333:role/tracer-cne-controller-irsa")
	clients := &Clients{Profile: "test"}

	if err := Phase21IRSASA(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase21 dry-run: %v", err)
	}
	wantSA := cneSAName("tracer")
	if st.Get("CNE_SA_NAME") != wantSA {
		t.Errorf("CNE_SA_NAME = %q, want %q", st.Get("CNE_SA_NAME"), wantSA)
	}
	if st.Get("IRSA_SA_APPLIED_AT") != "dry-run" {
		t.Errorf("IRSA_SA_APPLIED_AT = %q, want dry-run", st.Get("IRSA_SA_APPLIED_AT"))
	}
}

func TestPhase21IRSASA_SANameConvention(t *testing.T) {
	// Verify the SA name follows f5-cne-controller-<cluster>-bnk-serviceaccount.
	name := cneSAName("my-cluster")
	want := "f5-cne-controller-my-cluster-bnk-serviceaccount"
	if name != want {
		t.Errorf("cneSAName(%q) = %q, want %q", "my-cluster", name, want)
	}
}

// ─── Phase 21 Down tests ─────────────────────────────────────────────────────

func TestPhase21IRSASADown_Deletes(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("IRSA_SA_APPLIED_AT", "2026-05-22T00:00:00Z")
	st.Set("CNE_SA_NAME", cneSAName("tracer"))

	clients := buildP21Clients(t)

	if err := Phase21IRSASADown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase21Down: %v", err)
	}
	if st.Get("IRSA_SA_APPLIED_AT") != "" {
		t.Errorf("IRSA_SA_APPLIED_AT should be cleared after down, got %q", st.Get("IRSA_SA_APPLIED_AT"))
	}
	if st.Get("CNE_SA_NAME") != "" {
		t.Errorf("CNE_SA_NAME should be cleared after down, got %q", st.Get("CNE_SA_NAME"))
	}
}

func TestPhase21IRSASADown_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	// SA not pre-created — down should tolerate NotFound.
	clients := buildP21Clients(t)

	if err := Phase21IRSASADown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase21Down NotFound: %v", err)
	}
}
