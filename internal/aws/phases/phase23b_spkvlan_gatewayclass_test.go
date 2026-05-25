package phases

import (
	"context"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// TestPhase23b_DryRun_HostDevice verifies the dry-run path sets the expected
// state keys for the host-device pattern and skips actual k8s apply.
func TestPhase23b_DryRun_HostDevice(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	cl.Network.DataPath.SelfIPs = &intent.SelfIPsSpec{
		External:  "10.0.10.240",
		Internal:  "10.0.20.240",
		PrefixLen: 24,
	}
	dir := t.TempDir()
	st, _ := state.Load(dir)
	clients := &Clients{Profile: "test"}

	if err := Phase23bSPKVlanGatewayClass(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase23b dry-run: %v", err)
	}
	if got := st.Get("F5SPKVLAN_APPLIED_AT"); got != "dry-run" {
		t.Errorf("F5SPKVLAN_APPLIED_AT = %q, want dry-run", got)
	}
	wantGwc := cl.Metadata.Name + "-gatewayclass"
	if got := st.Get("GATEWAYCLASS_NAME"); got != wantGwc {
		t.Errorf("GATEWAYCLASS_NAME = %q, want %q", got, wantGwc)
	}
}

// TestPhase23b_SkipsWhenNotHostDevice verifies the phase is a silent no-op
// when pattern is anything other than host-device.
func TestPhase23b_SkipsWhenNotHostDevice(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	cl.Pattern = "" // not host-device
	dir := t.TempDir()
	st, _ := state.Load(dir)
	clients := &Clients{Profile: "test"}

	if err := Phase23bSPKVlanGatewayClass(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase23b non-host-device should silently skip: %v", err)
	}
	if got := st.Get("F5SPKVLAN_APPLIED_AT"); got != "" {
		t.Errorf("F5SPKVLAN_APPLIED_AT = %q, want empty (skipped)", got)
	}
}

// TestPhase23b_LivePath_MissingSelfIPs verifies a clear error when SelfIPs
// aren't defaulted (regression guard for intent.applyDefaults).
func TestPhase23b_LivePath_MissingSelfIPs(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	// Note: NOT setting SelfIPs — simulates applyDefaults not running.
	dir := t.TempDir()
	st, _ := state.Load(dir)
	// Live path needs Clients.Dynamic non-nil — but we expect to fail BEFORE
	// touching the dynamic client (SelfIPs check happens first). Use a stub.
	clients := &Clients{Profile: "test"}

	err := Phase23bSPKVlanGatewayClass(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error when SelfIPs missing, got nil")
	}
}

// TestPhase23bDown_SkipsWhenNotHostDevice mirrors the up-side guard.
func TestPhase23bDown_SkipsWhenNotHostDevice(t *testing.T) {
	awsmw.ResetForTest()
	cl := hostDeviceCluster()
	cl.Pattern = "sriov" // any non-host-device
	dir := t.TempDir()
	st, _ := state.Load(dir)
	clients := &Clients{Profile: "test"}

	if err := Phase23bSPKVlanGatewayClassDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase23bDown non-host-device should silently skip: %v", err)
	}
}

// TestPhase23b_GatewayClassCRDAbsent_BlocksBeforeApply verifies that when only
// the F5SPKVlan CRD is present (not the GatewayClass CRD), the phase blocks at
// the new GatewayClass CRD wait and never proceeds to any apply.
func TestPhase23b_GatewayClassCRDAbsent_BlocksBeforeApply(t *testing.T) {
	awsmw.ResetForTest()

	cl := hostDeviceCluster()
	cl.Network.DataPath.SelfIPs = &intent.SelfIPsSpec{
		External:  "10.0.10.240",
		Internal:  "10.0.20.240",
		PrefixLen: 24,
	}
	dir := t.TempDir()
	st, _ := state.Load(dir)

	// Seed the dynamic fake with ONLY the F5SPKVlan CRD — GatewayClass CRD absent.
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	spkvlanCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": f5spkvlanCRDName},
		},
	}
	scheme := buildScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
		[]runtime.Object{spkvlanCRD}...,
	)
	clients := &Clients{
		Profile:    "test",
		Dynamic:    dyn,
		RESTMapper: p12FakeRESTMapper(),
	}

	// Short timeout so the GatewayClass CRD wait expires quickly.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := Phase23bSPKVlanGatewayClass(ctx, cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error when GatewayClass CRD is absent, got nil")
	}
	if !strings.Contains(err.Error(), "GatewayClass CRD") {
		t.Errorf("error should mention 'GatewayClass CRD': %v", err)
	}
	// Neither apply should have been reached — up-front waits block both.
	if got := st.Get("F5SPKVLAN_APPLIED_AT"); got != "" {
		t.Errorf("F5SPKVLAN_APPLIED_AT = %q, want empty (blocked before apply)", got)
	}
	if got := st.Get("GATEWAYCLASS_NAME"); got != "" {
		t.Errorf("GATEWAYCLASS_NAME = %q, want empty (blocked before apply)", got)
	}
}

// TestPhase23b_BothCRDsPresent_ProceedsPastWaits verifies that when both the
// F5SPKVlan and GatewayClass CRDs are present, the phase clears both CRD waits
// and proceeds to the apply step. The dynamic fake cannot execute SSA, so the
// apply will fail — but the error must NOT mention "CRD", proving we got past
// the waits rather than failing there.
func TestPhase23b_BothCRDsPresent_ProceedsPastWaits(t *testing.T) {
	awsmw.ResetForTest()

	cl := hostDeviceCluster()
	cl.Network.DataPath.SelfIPs = &intent.SelfIPsSpec{
		External:  "10.0.10.240",
		Internal:  "10.0.20.240",
		PrefixLen: 24,
	}
	dir := t.TempDir()
	st, _ := state.Load(dir)

	// Seed BOTH CRD objects.
	crdGVR := schema.GroupVersionResource{
		Group:    "apiextensions.k8s.io",
		Version:  "v1",
		Resource: "customresourcedefinitions",
	}
	spkvlanCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": f5spkvlanCRDName},
		},
	}
	gwclassCRD := &unstructured.Unstructured{
		Object: map[string]interface{}{
			"apiVersion": "apiextensions.k8s.io/v1",
			"kind":       "CustomResourceDefinition",
			"metadata":   map[string]interface{}{"name": gatewayClassCRDName},
		},
	}
	scheme := buildScheme()
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme,
		map[schema.GroupVersionResource]string{crdGVR: "CustomResourceDefinitionList"},
		[]runtime.Object{spkvlanCRD, gwclassCRD}...,
	)
	clients := &Clients{
		Profile:    "test",
		Dynamic:    dyn,
		RESTMapper: p12FakeRESTMapper(),
	}

	// No artificial timeout — the CRD waits should resolve immediately.
	err := Phase23bSPKVlanGatewayClass(context.Background(), cl, st, clients, false)

	// The dynamic fake cannot execute SSA, so we expect an error — but it must
	// be an apply-step error, not a CRD-wait error. If "CRD" appears in the
	// error, both waits did NOT succeed and the test has found a real problem.
	if err == nil {
		t.Fatal("expected error from SSA apply on dynamic fake, got nil")
	}
	if strings.Contains(err.Error(), "CRD") {
		t.Errorf("error mentions 'CRD', meaning the phase did NOT clear both CRD waits — investigate: %v", err)
	}
}
