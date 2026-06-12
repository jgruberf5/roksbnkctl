package phases

import (
	"context"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// gpuClientsDryRun returns a minimal Clients for dry-run tests (no k8s).
func gpuClientsDryRun() *Clients {
	return &Clients{
		Profile: "test",
		EKS:     newMockEKS(),
	}
}

// noGPUCluster returns a cluster without any GPU node group (the clean-skip case).
func noGPUCluster(t *testing.T) (*intent.Cluster, *state.State) {
	t.Helper()
	cl := hostDeviceCluster()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	return cl, st
}

// gpuDryRunCluster returns a GPU rig cluster with state for dry-run tests.
func gpuDryRunCluster(t *testing.T) (*intent.Cluster, *state.State) {
	t.Helper()
	cl := gpuRigCluster()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	return cl, st
}

// TestPhase11cNvidiaDevicePlugin_DryRun_WithGPUNodeGroup verifies that:
// - Phase11c runs when a GPU node group is declared.
// - State key NVIDIA_DEVICE_PLUGIN_APPLIED_AT is set to "dry-run".
// - No k8s client calls are made (clients are nil in dry-run mode).
func TestPhase11cNvidiaDevicePlugin_DryRun_WithGPUNodeGroup(t *testing.T) {
	awsmw.ResetForTest()
	cl, st := gpuDryRunCluster(t)
	clients := gpuClientsDryRun()

	if err := Phase11cNvidiaDevicePlugin(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase11c dry-run with GPU ng: %v", err)
	}

	if got := st.Get("NVIDIA_DEVICE_PLUGIN_APPLIED_AT"); got != "dry-run" {
		t.Errorf("NVIDIA_DEVICE_PLUGIN_APPLIED_AT = %q, want %q", got, "dry-run")
	}
}

// TestPhase11cNvidiaDevicePlugin_DryRun_NoGPUNodeGroup verifies the clean-skip
// guard (R3): when no GPU node group is declared, Phase11c returns nil without
// setting any state key.
func TestPhase11cNvidiaDevicePlugin_DryRun_NoGPUNodeGroup(t *testing.T) {
	awsmw.ResetForTest()
	cl, st := noGPUCluster(t)
	clients := gpuClientsDryRun()

	if err := Phase11cNvidiaDevicePlugin(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase11c dry-run without GPU ng: %v", err)
	}

	// State key must NOT be set — clean skip.
	if got := st.Get("NVIDIA_DEVICE_PLUGIN_APPLIED_AT"); got != "" {
		t.Errorf("NVIDIA_DEVICE_PLUGIN_APPLIED_AT = %q, want empty (no GPU ng, clean skip)", got)
	}
}

// TestPhase11cNvidiaDevicePluginDown_DryRun_WithGPUNodeGroup verifies that Down
// self-gates on HasGPUNodeGroup() and tolerates nil k8s client gracefully.
func TestPhase11cNvidiaDevicePluginDown_WithGPUNodeGroup_NilK8s(t *testing.T) {
	awsmw.ResetForTest()
	cl, st := gpuDryRunCluster(t)
	// k8s nil — Down must tolerate and clear state.
	clients := &Clients{Profile: "test", EKS: newMockEKS()}

	if err := Phase11cNvidiaDevicePluginDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase11cDown with GPU ng + nil k8s: %v", err)
	}
}

// TestPhase11cNvidiaDevicePluginDown_NoGPUNodeGroup verifies the clean-skip
// guard (R3): Down returns nil immediately when no GPU node group.
func TestPhase11cNvidiaDevicePluginDown_NoGPUNodeGroup(t *testing.T) {
	awsmw.ResetForTest()
	cl, st := noGPUCluster(t)
	clients := gpuClientsDryRun()

	if err := Phase11cNvidiaDevicePluginDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("Phase11cDown without GPU ng: %v", err)
	}
}

// TestPhase11cNvidiaDevicePlugin_NoK8sClient_LiveFails verifies that the live
// path (dryRun=false) with nil k8s clients returns an appropriate error.
func TestPhase11cNvidiaDevicePlugin_NoK8sClient_LiveFails(t *testing.T) {
	awsmw.ResetForTest()
	cl, st := gpuDryRunCluster(t)
	clients := &Clients{Profile: "test", EKS: newMockEKS()}
	// K8s + Dynamic left nil — live path must fail with a clear message.

	err := Phase11cNvidiaDevicePlugin(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error when k8s clients are nil, got nil")
	}
	if msg := err.Error(); msg == "" {
		t.Error("error message should not be empty")
	}
}
