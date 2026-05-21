//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig codegen
package phases

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// p25Scheme returns a scheme with both CNEInstance and License kinds registered.
func p25Scheme() *runtime.Scheme {
	s := runtime.NewScheme()
	for _, entry := range []struct{ group, version, kind string }{
		{"k8s.f5.com", "v1", "CNEInstance"},
		{"k8s.f5.com", "v1", "CNEInstanceList"},
		{"k8s.f5net.com", "v1", "License"},
		{"k8s.f5net.com", "v1", "LicenseList"},
	} {
		gvk := schema.GroupVersionKind{Group: entry.group, Version: entry.version, Kind: entry.kind}
		if entry.kind == "CNEInstanceList" || entry.kind == "LicenseList" {
			s.AddKnownTypeWithName(gvk, &unstructured.UnstructuredList{})
		} else {
			s.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
		}
	}
	return s
}

// buildActiveLicense returns a License with status.state=Active.
func buildActiveLicense(name, ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.f5net.com/v1",
			"kind":       "License",
			"metadata":   map[string]interface{}{"name": name, "namespace": ns},
			"status":     map[string]interface{}{"state": "Active"},
		},
	}
}

// buildPendingCNEInstance returns a CNEInstance with status.state=Pending.
func buildPendingCNEInstance(name, ns string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "k8s.f5.com/v1",
			"kind":       "CNEInstance",
			"metadata":   map[string]interface{}{"name": name, "namespace": ns},
			"status":     map[string]interface{}{"state": "Pending"},
		},
	}
}

// p25Clients builds a Clients struct pre-seeded with a CNEInstance + License.
func p25Clients(cne, lic *unstructured.Unstructured) *Clients {
	scheme := p25Scheme()
	gvrMap := map[schema.GroupVersionResource]string{
		cneinstanceGVR: "CNEInstanceList",
		licenseGVR:     "LicenseList",
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, gvrMap, cne, lic)
	cs := k8sfake.NewSimpleClientset()
	return &Clients{
		Dynamic: dyn,
		K8s:     cs,
		Profile: "test",
	}
}

// ─── Test 1: Skip-flag returns nil immediately ────────────────────────────────

func TestPhase25_SkipFlag_ReturnsNil(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := p22Cluster()
	clients := &Clients{Profile: "test"}

	if err := Phase25ActivationPoll(context.Background(), cl, st, clients, false, true); err != nil {
		t.Fatalf("Phase25 skipPoll: %v", err)
	}
	// CNEINSTANCE_READY_AT must NOT be set when skipPoll=true.
	if st.Get("CNEINSTANCE_READY_AT") != "" {
		t.Errorf("CNEINSTANCE_READY_AT should not be set when skipPoll=true, got %q",
			st.Get("CNEINSTANCE_READY_AT"))
	}
}

// ─── Test 2: Dry-run ─────────────────────────────────────────────────────────

func TestPhase25_DryRun_ReturnsNil(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := p22Cluster()
	clients := &Clients{Profile: "test"}

	if err := Phase25ActivationPoll(context.Background(), cl, st, clients, true, false); err != nil {
		t.Fatalf("Phase25 dryRun: %v", err)
	}
}

// ─── Test 3: Nil Dynamic client error ────────────────────────────────────────

func TestPhase25_NilDynamic_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := p22Cluster()
	clients := &Clients{Profile: "test", Dynamic: nil, K8s: k8sfake.NewSimpleClientset()}

	err := Phase25ActivationPoll(context.Background(), cl, st, clients, false, false)
	if err == nil {
		t.Fatal("expected error for nil Dynamic, got nil")
	}
}

// ─── Test 4: Nil K8s client error ────────────────────────────────────────────

func TestPhase25_NilK8s_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := p22Cluster()

	scheme := p25Scheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		cneinstanceGVR: "CNEInstanceList",
		licenseGVR:     "LicenseList",
	})
	clients := &Clients{Profile: "test", Dynamic: dyn, K8s: nil}

	err := Phase25ActivationPoll(context.Background(), cl, st, clients, false, false)
	if err == nil {
		t.Fatal("expected error for nil K8s, got nil")
	}
}

// ─── Test 5: isCNEReady helper ────────────────────────────────────────────────

func TestIsCNEReady(t *testing.T) {
	cases := []struct {
		state string
		want  bool
	}{
		{"Ready", true},
		{"Running", true},
		{"Pending", false},
		{"", false},
		{"Active", false},
	}
	for _, tc := range cases {
		if got := isCNEReady(tc.state); got != tc.want {
			t.Errorf("isCNEReady(%q) = %v, want %v", tc.state, got, tc.want)
		}
	}
}

// ─── Test 6: podCounts returns zeroes on empty namespace ─────────────────────

func TestPodCounts_EmptyNamespace(t *testing.T) {
	cs := k8sfake.NewSimpleClientset()
	clients := &Clients{K8s: cs, Profile: "test"}
	running, pending, failed, total := podCounts(context.Background(), clients, InstanceNamespace)
	if running != 0 || pending != 0 || failed != 0 || total != 0 {
		t.Errorf("expected all zeros, got running=%d pending=%d failed=%d total=%d",
			running, pending, failed, total)
	}
}

// ─── Test 7: Context cancellation returns error ───────────────────────────────

func TestPhase25_CtxCancel_ReturnsError(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := p22Cluster()
	crName := cl.Metadata.Name + "-bnk"

	// Seed pending state — won't reach Ready.
	cne := buildPendingCNEInstance(crName, InstanceNamespace)
	lic := buildActiveLicense(licenseCRName, OperatorNamespace)
	clients := p25Clients(cne, lic)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel before the initial sleep

	err := Phase25ActivationPoll(ctx, cl, st, clients, false, false)
	if err == nil {
		t.Fatal("expected error from ctx cancellation, got nil")
	}
}
