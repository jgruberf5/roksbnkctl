package phases

import (
	"context"
	"testing"

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
