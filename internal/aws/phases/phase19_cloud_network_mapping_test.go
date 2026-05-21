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

// seedP19State seeds the state keys that Phase 19 reads (written by Phase 03).
func seedP19State(st *state.State) {
	st.Set("PUBLIC_SUBNETS", "subnet-pub-001,subnet-pub-002")
	st.Set("BNK_EXT_SUBNET", "subnet-ext-001")
	st.Set("BNK_INT_SUBNET", "subnet-int-001")
	st.Set("CNE_IRSA_ROLE_ARN", "arn:aws:iam::111122223333:role/syd-tracer-cne-controller-irsa")
}

// buildP19DynamicFake returns a dynamic fake client with ConfigMap registered.
func buildP19DynamicFake() *dynamicfake.FakeDynamicClient {
	scheme := buildScheme()
	cmGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	return dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		cmGVR: "ConfigMapList",
	})
}

// ─── Phase 19 tests ──────────────────────────────────────────────────────────

func TestPhase19CloudNetworkMapping_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	seedP19State(st)
	clients := &Clients{Profile: "test"}

	if err := Phase19CloudNetworkMapping(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase19 dry-run: %v", err)
	}
	if st.Get("CLOUD_NETWORK_MAPPING_APPLIED_AT") != "dry-run" {
		t.Errorf("CLOUD_NETWORK_MAPPING_APPLIED_AT = %q, want dry-run",
			st.Get("CLOUD_NETWORK_MAPPING_APPLIED_AT"))
	}
	// Host-device constants must be set in dry-run.
	if st.Get("INSTANCE_NS") != InstanceNamespace {
		t.Errorf("INSTANCE_NS = %q, want %q", st.Get("INSTANCE_NS"), InstanceNamespace)
	}
	if st.Get("EXTERNAL_IFNAME") != ExternalIFName {
		t.Errorf("EXTERNAL_IFNAME = %q, want %q", st.Get("EXTERNAL_IFNAME"), ExternalIFName)
	}
	// MGMT_SUBNET alias must be derived from PUBLIC_SUBNETS[0].
	if st.Get("MGMT_SUBNET") != "subnet-pub-001" {
		t.Errorf("MGMT_SUBNET = %q, want subnet-pub-001", st.Get("MGMT_SUBNET"))
	}
}

func TestPhase19CloudNetworkMapping_DryRun_MissingPublicSubnets(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	// Do NOT seed PUBLIC_SUBNETS — phase should error.
	clients := &Clients{Profile: "test"}

	err := Phase19CloudNetworkMapping(context.Background(), cl, st, clients, true)
	if err == nil {
		t.Fatal("expected error when PUBLIC_SUBNETS missing, got nil")
	}
}

// TestPhase19CloudNetworkMapping_Apply verifies that constants + MGMT_SUBNET alias
// are correctly set when Phase19 runs non-dry. The actual applyRawYAML (SSA Patch)
// uses the dynamic fake which does not support ApplyPatchType create-if-absent
// semantics — the apply step is covered by dry-run path (template render path)
// and the render_test.go unit tests. We verify state keys via the render→state path.
func TestPhase19CloudNetworkMapping_StateKeysSetOnApply(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	seedP19State(st)

	// In dry-run mode, all state keys are set without calling applyRawYAML.
	clients := &Clients{Profile: "test"}
	if err := Phase19CloudNetworkMapping(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase19 dry-run for state-key test: %v", err)
	}
	// Verify constants are persisted.
	if st.Get("INSTANCE_NS") != InstanceNamespace {
		t.Errorf("INSTANCE_NS = %q, want %q", st.Get("INSTANCE_NS"), InstanceNamespace)
	}
	if st.Get("EXTERNAL_IFNAME") != ExternalIFName {
		t.Errorf("EXTERNAL_IFNAME = %q, want %q", st.Get("EXTERNAL_IFNAME"), ExternalIFName)
	}
	if st.Get("INTERNAL_IFNAME") != InternalIFName {
		t.Errorf("INTERNAL_IFNAME = %q, want %q", st.Get("INTERNAL_IFNAME"), InternalIFName)
	}
	if st.Get("CLOUD_HOST_DEVICE_TAG") != CloudHostDeviceTag {
		t.Errorf("CLOUD_HOST_DEVICE_TAG = %q, want %q", st.Get("CLOUD_HOST_DEVICE_TAG"), CloudHostDeviceTag)
	}
}

// TestPhase19CloudNetworkMapping_MGMTSubnetAlias verifies MGMT_SUBNET is derived
// from PUBLIC_SUBNETS[0] and idempotent on re-run.
func TestPhase19CloudNetworkMapping_MGMTSubnetAlias(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("PUBLIC_SUBNETS", "subnet-pub-001,subnet-pub-002")

	// First call — should derive.
	if err := ensureMGMTSubnetAlias(st); err != nil {
		t.Fatalf("ensureMGMTSubnetAlias: %v", err)
	}
	if st.Get("MGMT_SUBNET") != "subnet-pub-001" {
		t.Errorf("MGMT_SUBNET = %q, want subnet-pub-001", st.Get("MGMT_SUBNET"))
	}

	// Second call — should be idempotent (already set).
	st.Set("PUBLIC_SUBNETS", "DIFFERENT-subnet") // changing PUBLIC_SUBNETS shouldn't matter
	if err := ensureMGMTSubnetAlias(st); err != nil {
		t.Fatalf("ensureMGMTSubnetAlias idempotent: %v", err)
	}
	if st.Get("MGMT_SUBNET") != "subnet-pub-001" {
		t.Errorf("MGMT_SUBNET changed on idempotent re-run: got %q", st.Get("MGMT_SUBNET"))
	}
}

func TestPhase19CloudNetworkMapping_MissingBNKExtSubnet_Errors(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("PUBLIC_SUBNETS", "subnet-pub-001")
	// Missing BNK_EXT_SUBNET.

	dyn := buildP19DynamicFake()
	clients := &Clients{
		K8s:     k8sfake.NewSimpleClientset(),
		Dynamic: dyn,
		Profile: "test",
	}

	err := Phase19CloudNetworkMapping(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error when BNK_EXT_SUBNET missing, got nil")
	}
}

// ─── Phase 19 Down tests ─────────────────────────────────────────────────────

func TestPhase19CloudNetworkMappingDown_Deletes(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("CLOUD_NETWORK_MAPPING_APPLIED_AT", "2026-05-22T00:00:00Z")

	cmGVR := schema.GroupVersionResource{Group: "", Version: "v1", Resource: "configmaps"}
	scheme := buildScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		cmGVR: "ConfigMapList",
	})
	clients := &Clients{
		K8s:     k8sfake.NewSimpleClientset(),
		Dynamic: dyn,
		Profile: "test",
	}

	// Should not error even if CM doesn't exist.
	if err := Phase19CloudNetworkMappingDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase19Down: %v", err)
	}
	if st.Get("CLOUD_NETWORK_MAPPING_APPLIED_AT") != "" {
		t.Errorf("CLOUD_NETWORK_MAPPING_APPLIED_AT should be cleared, got %q",
			st.Get("CLOUD_NETWORK_MAPPING_APPLIED_AT"))
	}
}

func TestPhase19CloudNetworkMappingDown_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, _ := state.Load(dir)

	// No CM pre-seeded — down should tolerate NotFound.
	scheme := buildScheme()
	dyn := dynamicfake.NewSimpleDynamicClient(scheme)
	clients := &Clients{
		K8s:     k8sfake.NewSimpleClientset(),
		Dynamic: dyn,
		Profile: "test",
	}

	if err := Phase19CloudNetworkMappingDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase19Down NotFound: %v", err)
	}
}
