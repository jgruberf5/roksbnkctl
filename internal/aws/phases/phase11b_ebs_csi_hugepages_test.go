//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig codegen
package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

func phase11bCluster(t *testing.T) (*intent.Cluster, *state.State, string) {
	t.Helper()
	cl := hostDeviceCluster()
	// Phase 11b reads cl.Bnk.StorageClassName + cl.Bnk.TmmHugepages — match the
	// post-defaults values from intent.applyDefaults so tests see the same
	// values production would.
	cl.Bnk = &intent.BnkSpec{
		FARArchive:       "/dev/null",
		JWT:              "/dev/null",
		StorageClassName: "gp2",
		TmmHugepages:     "4Gi",
	}
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	return cl, st, dir
}

func TestPhase11bEBSCSIHugepages_DryRun(t *testing.T) {
	cl, st, _ := phase11bCluster(t)
	clients := &Clients{
		Profile: "test",
		EKS:     newMockEKS(),
	}

	if err := Phase11bEBSCSIHugepages(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("dry-run: %v", err)
	}
	if got := st.Get("EBS_CSI_ADDON_STATUS"); !strings.HasPrefix(got, "dry-run") {
		t.Errorf("EBS_CSI_ADDON_STATUS: got %q, want dry-run prefix", got)
	}
	// GP3_STORAGE_CLASS now records the SC BNK will request — defaults to gp2
	// (EKS-default, CSI-migrated). No SC apply happens in Phase 11b anymore.
	if got := st.Get("GP3_STORAGE_CLASS"); got != "gp2" {
		t.Errorf("GP3_STORAGE_CLASS: got %q, want %q", got, "gp2")
	}
	if got := st.Get("HUGEPAGES_DS_INSTALLED_AT"); got != "dry-run" {
		t.Errorf("HUGEPAGES_DS_INSTALLED_AT: got %q, want dry-run", got)
	}
}

// TestPhase11bEBSCSIHugepages_DryRun_NilBnk confirms that dry-run with nil Bnk
// (as in forge's minimal external-only config) does not panic and uses defaults
// for SC and hugepages values in logging/state.
func TestPhase11bEBSCSIHugepages_DryRun_NilBnk(t *testing.T) {
	cl := hostDeviceCluster()
	cl.Bnk = nil // Simulate forge's dry-run with no BnkSpec
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	clients := &Clients{
		Profile: "test",
		EKS:     newMockEKS(),
	}

	// Must not panic.
	if err := Phase11bEBSCSIHugepages(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("dry-run with nil Bnk: %v", err)
	}

	// Should use defaults for logging/state.
	if got := st.Get("EBS_CSI_ADDON_STATUS"); !strings.HasPrefix(got, "dry-run") {
		t.Errorf("EBS_CSI_ADDON_STATUS: got %q, want dry-run prefix", got)
	}
	if got := st.Get("GP3_STORAGE_CLASS"); got != "gp2" {
		t.Errorf("GP3_STORAGE_CLASS (nil Bnk default): got %q, want %q", got, "gp2")
	}
}

// TestPhase11bEBSCSIHugepages_DryRun_HugepagesOverride confirms that an
// operator-overridden TmmHugepages (non-default 8Gi) flows through to the
// dry-run log path without breaking the BnkSpec read.
func TestPhase11bEBSCSIHugepages_DryRun_HugepagesOverride(t *testing.T) {
	cl, st, _ := phase11bCluster(t)
	cl.Bnk.TmmHugepages = "8Gi"
	cl.Bnk.StorageClassName = "gp3" // also exercise a non-default SC
	clients := &Clients{
		Profile: "test",
		EKS:     newMockEKS(),
	}

	if err := Phase11bEBSCSIHugepages(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("dry-run with overrides: %v", err)
	}
	if got := st.Get("GP3_STORAGE_CLASS"); got != "gp3" {
		t.Errorf("GP3_STORAGE_CLASS: got %q, want %q (operator override)", got, "gp3")
	}
}

func TestPhase11bEBSCSIHugepages_NoK8sClient(t *testing.T) {
	cl, st, _ := phase11bCluster(t)
	clients := &Clients{
		Profile: "test",
		EKS:     newMockEKS(),
		// K8s + Dynamic left nil intentionally
	}

	err := Phase11bEBSCSIHugepages(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error when k8s clients are nil, got nil")
	}
	if !strings.Contains(err.Error(), "k8s clients nil") {
		t.Errorf("error message: got %q, want substring 'k8s clients nil'", err.Error())
	}
}

func TestPhase11bEBSCSIHugepages_AddonAlreadyActive_NoCreateCall(t *testing.T) {
	// Seed the mock with an existing ACTIVE addon — Phase 11b should not
	// call CreateAddon, only DescribeAddon for the readiness poll.
	cl, _, _ := phase11bCluster(t)
	mockEKS := newMockEKS()
	// Simulate addon already present.
	if err := ensureEBSCSIAddon(context.Background(), mockEKS, cl.Metadata.Name); err != nil {
		t.Fatalf("seed addon: %v", err)
	}
	if mockEKS.createAddonCalls != 1 {
		t.Errorf("seed: createAddonCalls got %d, want 1", mockEKS.createAddonCalls)
	}
	// Now call ensureEBSCSIAddon a second time — should detect existing ACTIVE addon.
	if err := ensureEBSCSIAddon(context.Background(), mockEKS, cl.Metadata.Name); err != nil {
		t.Fatalf("ensure-idempotent: %v", err)
	}
	if mockEKS.createAddonCalls != 1 {
		t.Errorf("idempotent: createAddonCalls got %d, want 1 (no second create)", mockEKS.createAddonCalls)
	}
	if mockEKS.describeAddonCalls < 2 {
		t.Errorf("idempotent: describeAddonCalls got %d, want >= 2", mockEKS.describeAddonCalls)
	}
}

func TestPhase11bEBSCSIHugepagesDown_NoK8sClient_Tolerates(t *testing.T) {
	cl, st, _ := phase11bCluster(t)
	clients := &Clients{
		Profile: "test",
		EKS:     newMockEKS(),
	}
	if err := Phase11bEBSCSIHugepagesDown(context.Background(), cl, st, clients); err != nil {
		t.Fatalf("down with nil K8s should not error: %v", err)
	}
	if got := st.Get("EBS_CSI_ADDON_STATUS"); got != "" {
		t.Errorf("state should be cleared: EBS_CSI_ADDON_STATUS=%q", got)
	}
}
