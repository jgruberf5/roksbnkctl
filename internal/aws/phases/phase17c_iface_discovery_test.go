package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// ─── matchInterfaces tests ────────────────────────────────────────────────────

func TestMatchInterfaces_HappyPath(t *testing.T) {
	discovered := map[string]ifaceInfo{
		"0a:1b:2c:3d:4e:5f": {Ifname: "ens8", Pci: "0000:00:08.0"},
		"0a:1b:2c:3d:4e:60": {Ifname: "ens7", Pci: "0000:00:07.0"},
		"fe:80:00:00:00:01": {Ifname: "eth0", Pci: "0000:00:01.0"},
	}

	extIf, extPCI, intIf, intPCI, err := matchInterfaces(discovered,
		"0a:1b:2c:3d:4e:5f", // ext MAC
		"0a:1b:2c:3d:4e:60", // int MAC
	)
	if err != nil {
		t.Fatalf("matchInterfaces: unexpected error: %v", err)
	}
	if extIf != "ens8" {
		t.Errorf("extIf = %q, want ens8", extIf)
	}
	if extPCI != "0000:00:08.0" {
		t.Errorf("extPCI = %q, want 0000:00:08.0", extPCI)
	}
	if intIf != "ens7" {
		t.Errorf("intIf = %q, want ens7", intIf)
	}
	if intPCI != "0000:00:07.0" {
		t.Errorf("intPCI = %q, want 0000:00:07.0", intPCI)
	}
}

func TestMatchInterfaces_CaseInsensitiveMAC(t *testing.T) {
	// Probe emits lowercase; state may have mixed-case from EC2.
	discovered := map[string]ifaceInfo{
		"aa:bb:cc:dd:ee:ff": {Ifname: "ens8", Pci: "0000:00:08.0"},
		"11:22:33:44:55:66": {Ifname: "ens7", Pci: "0000:00:07.0"},
	}

	// Pass uppercase MACs — should still match.
	_, _, _, _, err := matchInterfaces(discovered,
		"AA:BB:CC:DD:EE:FF",
		"11:22:33:44:55:66",
	)
	if err != nil {
		t.Fatalf("matchInterfaces (case-insensitive): %v", err)
	}
}

func TestMatchInterfaces_ExtMACNotFound_HardError(t *testing.T) {
	discovered := map[string]ifaceInfo{
		"aa:bb:cc:dd:ee:ff": {Ifname: "ens8", Pci: "0000:00:08.0"},
		// int MAC missing.
	}

	_, _, _, _, err := matchInterfaces(discovered,
		"00:00:00:00:00:01", // ext MAC — not in map
		"aa:bb:cc:dd:ee:ff", // int MAC — found
	)
	if err == nil {
		t.Fatal("expected error for missing ext MAC, got nil")
	}
	if !strings.Contains(err.Error(), "external MAC") {
		t.Errorf("error should mention 'external MAC': %v", err)
	}
}

func TestMatchInterfaces_BothMACsNotFound_HardError(t *testing.T) {
	discovered := map[string]ifaceInfo{
		"00:00:00:00:00:01": {Ifname: "lo", Pci: ""},
	}

	_, _, _, _, err := matchInterfaces(discovered,
		"aa:bb:cc:dd:ee:ff",
		"11:22:33:44:55:66",
	)
	if err == nil {
		t.Fatal("expected error for both MACs missing, got nil")
	}
	// Both MACs named in error.
	if !strings.Contains(err.Error(), "external MAC") {
		t.Errorf("error should mention 'external MAC': %v", err)
	}
	if !strings.Contains(err.Error(), "internal MAC") {
		t.Errorf("error should mention 'internal MAC': %v", err)
	}
}

// ─── Phase17cIfaceDiscovery dry-run tests ────────────────────────────────────

func TestPhase17cIfaceDiscovery_DryRun_SetsConstants(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()

	// Dry-run must NOT require clients.K8s (it may be nil).
	clients := &Clients{Profile: "test"} // K8s is nil

	if err := Phase17cIfaceDiscovery(context.TODO(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase17cIfaceDiscovery dry-run: %v", err)
	}

	// All 6 keys must be set.
	checks := map[string]string{
		"EXTERNAL_IFNAME":        ExternalIFName,
		"INTERNAL_IFNAME":        InternalIFName,
		"EXTERNAL_PCI":           ExternalPCI,
		"INTERNAL_PCI":           InternalPCI,
		"CLOUD_HOST_DEVICE_NAME": ExternalIFName,
		"IFACE_DISCOVERY_AT":     "dry-run",
	}
	for key, want := range checks {
		if got := st.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

func TestPhase17cIfaceDiscovery_DryRun_NilK8sNoPanic(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()

	// Must not panic with nil K8s.
	clients := &Clients{Profile: "test", K8s: nil}

	defer func() {
		if r := recover(); r != nil {
			t.Errorf("Phase17cIfaceDiscovery dry-run panicked with nil K8s: %v", r)
		}
	}()

	if err := Phase17cIfaceDiscovery(context.TODO(), cl, st, clients, true); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestPhase17cIfaceDiscovery_MissingK8sNonDryRun_Errors(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("INTERNAL_ENI_MAC", "aa:bb:cc:dd:ee:ff")
	st.Set("EXTERNAL_ENI_MAC", "11:22:33:44:55:66")
	st.Set("TMM_NODE_NAME", "ip-10-0-1-10.ap-southeast-2.compute.internal")
	cl := testCluster()

	clients := &Clients{Profile: "test", K8s: nil}

	err := Phase17cIfaceDiscovery(context.TODO(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error when K8s is nil in non-dry-run, got nil")
	}
	if !strings.Contains(err.Error(), "Clients.K8s is nil") {
		t.Errorf("error should mention K8s nil: %v", err)
	}
}

// ─── Phase17cIfaceDiscoveryDown tests ─────────────────────────────────────────

func TestPhase17cIfaceDiscoveryDown_ClearsKeys(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	// Pre-populate keys as if phase17c ran.
	st.Set("EXTERNAL_IFNAME", "ens8")
	st.Set("INTERNAL_IFNAME", "ens7")
	st.Set("EXTERNAL_PCI", "0000:00:08.0")
	st.Set("INTERNAL_PCI", "0000:00:07.0")
	st.Set("CLOUD_HOST_DEVICE_NAME", "ens8")
	st.Set("IFACE_DISCOVERY_AT", "2026-05-26T00:00:00Z")

	clients := &Clients{Profile: "test", K8s: nil}
	cl := testCluster()

	if err := Phase17cIfaceDiscoveryDown(context.TODO(), cl, st, clients); err != nil {
		t.Fatalf("Phase17cIfaceDiscoveryDown: %v", err)
	}

	for _, key := range []string{
		"EXTERNAL_IFNAME", "INTERNAL_IFNAME",
		"EXTERNAL_PCI", "INTERNAL_PCI",
		"CLOUD_HOST_DEVICE_NAME", "IFACE_DISCOVERY_AT",
	} {
		if got := st.Get(key); got != "" {
			t.Errorf("%s = %q after down, want empty", key, got)
		}
	}
}

// ─── ifaceMappingResolved tests ──────────────────────────────────────────────

func TestIfaceMappingResolved_AllSet(t *testing.T) {
	st, _ := state.Load(t.TempDir())
	st.Set("EXTERNAL_IFNAME", "ens8")
	st.Set("INTERNAL_IFNAME", "ens7")
	st.Set("EXTERNAL_PCI", "0000:00:08.0")
	st.Set("INTERNAL_PCI", "0000:00:07.0")
	if !ifaceMappingResolved(st) {
		t.Error("expected resolved when all 4 keys set, got false")
	}
}

func TestIfaceMappingResolved_EachKeyMissingMeansNotResolved(t *testing.T) {
	allKeys := []string{"EXTERNAL_IFNAME", "INTERNAL_IFNAME", "EXTERNAL_PCI", "INTERNAL_PCI"}
	for _, missing := range allKeys {
		t.Run("missing_"+missing, func(t *testing.T) {
			st, _ := state.Load(t.TempDir())
			for _, k := range allKeys {
				if k != missing {
					st.Set(k, "somevalue")
				}
			}
			if ifaceMappingResolved(st) {
				t.Errorf("expected not resolved when %s is missing, got true", missing)
			}
		})
	}
}

// ─── shouldSkipIfaceDiscovery tests ──────────────────────────────────────────

func TestShouldSkipIfaceDiscovery(t *testing.T) {
	tests := []struct {
		resolved   bool
		tmmRunning bool
		want       bool
	}{
		{true, true, true},
		{true, false, false},
		{false, true, false},
		{false, false, false},
	}
	for _, tc := range tests {
		got := shouldSkipIfaceDiscovery(tc.resolved, tc.tmmRunning)
		if got != tc.want {
			t.Errorf("shouldSkipIfaceDiscovery(resolved=%v, tmmRunning=%v) = %v, want %v",
				tc.resolved, tc.tmmRunning, got, tc.want)
		}
	}
}

// ─── Cross-phase ordering test ────────────────────────────────────────────────

// TestCrossPhase_17cThenPhase19_DiscoveredKeysSurvive verifies the critical
// ordering property: phase17c writes EXTERNAL_PCI/INTERNAL_PCI/EXTERNAL_IFNAME/
// INTERNAL_IFNAME, and a subsequent phase19 dry-run must NOT overwrite them.
// This proves the D4 removal (delete of the five st.Set lines in phase19).
func TestCrossPhase_17cThenPhase19_DiscoveredKeysSurvive(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := hostDeviceCluster()
	// Seed public subnets so phase19 ensureMGMTSubnetAlias works.
	st.Set("PUBLIC_SUBNETS", "subnet-pub-001")
	clients := &Clients{Profile: "test"}

	// Step 1: phase17c dry-run sets discovered interface values.
	if err := Phase17cIfaceDiscovery(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase17cIfaceDiscovery dry-run: %v", err)
	}
	// Verify values are set (constants since dry-run).
	if got := st.Get("EXTERNAL_PCI"); got != ExternalPCI {
		t.Fatalf("after phase17c: EXTERNAL_PCI = %q, want %q", got, ExternalPCI)
	}

	// Simulate a non-constant discovery value (as if real node discovered a different PCI).
	st.Set("EXTERNAL_PCI", "0000:00:0a.0")
	st.Set("INTERNAL_PCI", "0000:00:09.0")
	st.Set("EXTERNAL_IFNAME", "ens9")
	st.Set("INTERNAL_IFNAME", "ens6")

	// Step 2: phase19 dry-run must NOT clobber the discovered values.
	if err := Phase19CloudNetworkMapping(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase19 dry-run: %v", err)
	}

	// Discovered keys must survive phase19.
	if got := st.Get("EXTERNAL_PCI"); got != "0000:00:0a.0" {
		t.Errorf("EXTERNAL_PCI clobbered by phase19: got %q, want 0000:00:0a.0", got)
	}
	if got := st.Get("INTERNAL_PCI"); got != "0000:00:09.0" {
		t.Errorf("INTERNAL_PCI clobbered by phase19: got %q, want 0000:00:09.0", got)
	}
	if got := st.Get("EXTERNAL_IFNAME"); got != "ens9" {
		t.Errorf("EXTERNAL_IFNAME clobbered by phase19: got %q, want ens9", got)
	}
	if got := st.Get("INTERNAL_IFNAME"); got != "ens6" {
		t.Errorf("INTERNAL_IFNAME clobbered by phase19: got %q, want ens6", got)
	}
}
