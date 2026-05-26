package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// stateWithENIPrereqs returns a state pre-populated with Phase17 required keys.
func stateWithENIPrereqs(t *testing.T) (*state.State, string) {
	t.Helper()
	dir := t.TempDir()
	st, err := state.Load(dir)
	if err != nil {
		t.Fatalf("state.Load: %v", err)
	}
	st.Set("TMM_INSTANCE_ID", "i-0123456789abcdef0")
	st.Set("BNK_INT_SUBNET", "subnet-int-1")
	st.Set("BNK_EXT_SUBNET", "subnet-ext-1")
	st.Set("SG_BNK_DATA", "sg-bnk-data-1")
	return st, dir
}

// TestPhase17SecondaryENIs_DryRun verifies no EC2 mutations and placeholder state.
func TestPhase17SecondaryENIs_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()
	ec2m := &mockEC2{}

	if err := Phase17SecondaryENIs(context.Background(), cl, st, testClients(ec2m), true); err != nil {
		t.Fatalf("Phase17SecondaryENIs dry-run: %v", err)
	}

	if ec2m.createENICalls != 0 {
		t.Errorf("dry-run: createENICalls = %d, want 0", ec2m.createENICalls)
	}
	if got := st.Get("INTERNAL_ENI"); got != "eni-dry-run-int" {
		t.Errorf("INTERNAL_ENI = %q, want eni-dry-run-int", got)
	}
	if got := st.Get("EXTERNAL_ENI"); got != "eni-dry-run-ext" {
		t.Errorf("EXTERNAL_ENI = %q, want eni-dry-run-ext", got)
	}
	// Dry-run must set placeholder MACs.
	if got := st.Get("INTERNAL_ENI_MAC"); got != "02:00:00:00:00:02" {
		t.Errorf("INTERNAL_ENI_MAC = %q, want 02:00:00:00:00:02", got)
	}
	if got := st.Get("EXTERNAL_ENI_MAC"); got != "02:00:00:00:00:03" {
		t.Errorf("EXTERNAL_ENI_MAC = %q, want 02:00:00:00:00:03", got)
	}
}

// TestPhase17SecondaryENIs_MissingPrereqs verifies error when required state
// keys are absent.
func TestPhase17SecondaryENIs_MissingPrereqs(t *testing.T) {
	tests := []struct {
		name      string
		omitKey   string
		wantInErr string
	}{
		{"missing TMM_INSTANCE_ID", "TMM_INSTANCE_ID", "TMM_INSTANCE_ID"},
		{"missing BNK_INT_SUBNET", "BNK_INT_SUBNET", "BNK_INT_SUBNET"},
		{"missing BNK_EXT_SUBNET", "BNK_EXT_SUBNET", "BNK_EXT_SUBNET"},
		{"missing SG_BNK_DATA", "SG_BNK_DATA", "SG_BNK_DATA"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			awsmw.ResetForTest()
			st, _ := stateWithENIPrereqs(t)
			st.Set(tc.omitKey, "")
			cl := testCluster()

			err := Phase17SecondaryENIs(context.Background(), cl, st, testClients(&mockEC2{}), false)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.omitKey)
			}
			if !strings.Contains(err.Error(), tc.wantInErr) {
				t.Errorf("error %q should contain %q", err.Error(), tc.wantInErr)
			}
		})
	}
}

// TestPhase17SecondaryENIs_TagDiscoveryAndAttach verifies that when no ENI IDs
// are in state but tag-discovery finds existing ENIs with no active attachment,
// the phase attaches them and sets state keys (including MACs).
func TestPhase17SecondaryENIs_TagDiscoveryAndAttach(t *testing.T) {
	awsmw.ResetForTest()
	st, _ := stateWithENIPrereqs(t)
	cl := testCluster()

	mac := "0a:1b:2c:3d:4e:5f"
	// DescribeNetworkInterfaces returns ENI with no attachment — both tag-discovery
	// and the attach-check + MAC-capture use the same mock. MAC is included so the
	// new eniMAC() call succeeds.
	ec2m := &mockEC2{
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{
					NetworkInterfaceId: ptr("eni-found-by-tag"),
					MacAddress:         &mac,
					Attachment:         nil,
				},
			},
		},
	}

	if err := Phase17SecondaryENIs(context.Background(), cl, st, testClients(ec2m), false); err != nil {
		t.Fatalf("Phase17SecondaryENIs: %v", err)
	}

	// State keys populated (from tag-discovery).
	if st.Get("INTERNAL_ENI") == "" {
		t.Error("INTERNAL_ENI not set in state")
	}
	if st.Get("EXTERNAL_ENI") == "" {
		t.Error("EXTERNAL_ENI not set in state")
	}
	// MAC keys populated (from eniMAC describe — always runs).
	if got := st.Get("INTERNAL_ENI_MAC"); got != mac {
		t.Errorf("INTERNAL_ENI_MAC = %q, want %q", got, mac)
	}
	if got := st.Get("EXTERNAL_ENI_MAC"); got != mac {
		t.Errorf("EXTERNAL_ENI_MAC = %q, want %q", got, mac)
	}
	// Attach called twice (once per ENI that has nil attachment).
	if ec2m.attachENICalls != 2 {
		t.Errorf("attachENICalls = %d, want 2", ec2m.attachENICalls)
	}
}

// TestPhase17SecondaryENIs_Idempotent verifies no re-create when state keys
// already contain ENI IDs and ENIs are already attached to the correct instance.
// Also verifies that MACs are still captured on re-run (state-hit path).
func TestPhase17SecondaryENIs_Idempotent(t *testing.T) {
	awsmw.ResetForTest()
	st, _ := stateWithENIPrereqs(t)
	st.Set("INTERNAL_ENI", "eni-existing-int")
	st.Set("EXTERNAL_ENI", "eni-existing-ext")
	cl := testCluster()

	instanceID := "i-0123456789abcdef0"
	mac := "0a:1b:2c:3d:4e:5f"
	// DescribeNetworkInterfaces returns both ENIs already attached to the instance,
	// with a MAC so eniMAC() succeeds.
	ec2m := &mockEC2{
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{
					NetworkInterfaceId: ptr("eni-existing-int"),
					MacAddress:         &mac,
					Attachment: &ec2types.NetworkInterfaceAttachment{
						InstanceId: &instanceID,
					},
				},
			},
		},
	}

	if err := Phase17SecondaryENIs(context.Background(), cl, st, testClients(ec2m), false); err != nil {
		t.Fatalf("Phase17SecondaryENIs idempotent: %v", err)
	}

	// No new ENIs created.
	if ec2m.createENICalls != 0 {
		t.Errorf("idempotent: createENICalls = %d, want 0", ec2m.createENICalls)
	}
	// No attach calls (already attached).
	if ec2m.attachENICalls != 0 {
		t.Errorf("idempotent: attachENICalls = %d, want 0", ec2m.attachENICalls)
	}
	// MAC still captured on state-hit path.
	if got := st.Get("INTERNAL_ENI_MAC"); got != mac {
		t.Errorf("idempotent: INTERNAL_ENI_MAC = %q, want %q (MAC must be captured on re-run)", got, mac)
	}
	if got := st.Get("EXTERNAL_ENI_MAC"); got != mac {
		t.Errorf("idempotent: EXTERNAL_ENI_MAC = %q, want %q (MAC must be captured on re-run)", got, mac)
	}
}

// TestPhase17SecondaryENIs_MACLowercased verifies that eniMAC returns a
// lowercase MAC regardless of what EC2 returns (EC2 returns mixed-case).
func TestPhase17SecondaryENIs_MACLowercased(t *testing.T) {
	awsmw.ResetForTest()
	st, _ := stateWithENIPrereqs(t)
	cl := testCluster()

	// EC2 returns mixed-case MAC.
	mac := "0A:1B:2C:3D:4E:5F"
	ec2m := &mockEC2{
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{
					NetworkInterfaceId: ptr("eni-found-by-tag"),
					MacAddress:         &mac,
					Attachment:         nil,
				},
			},
		},
	}

	if err := Phase17SecondaryENIs(context.Background(), cl, st, testClients(ec2m), false); err != nil {
		t.Fatalf("Phase17SecondaryENIs: %v", err)
	}

	// MAC must be lower-cased.
	want := "0a:1b:2c:3d:4e:5f"
	if got := st.Get("INTERNAL_ENI_MAC"); got != want {
		t.Errorf("INTERNAL_ENI_MAC = %q, want %q (must be lowercase)", got, want)
	}
	if got := st.Get("EXTERNAL_ENI_MAC"); got != want {
		t.Errorf("EXTERNAL_ENI_MAC = %q, want %q (must be lowercase)", got, want)
	}
}

// TestPhase17SecondaryENIs_TagHitMACCaptured verifies that when ENIs are found
// via tags (not in state), the MAC is still captured (tag-hit path).
func TestPhase17SecondaryENIs_TagHitMACCaptured(t *testing.T) {
	awsmw.ResetForTest()
	st, _ := stateWithENIPrereqs(t)
	// No ENI IDs pre-set in state — forces tag-discovery.
	cl := testCluster()

	mac := "aa:bb:cc:dd:ee:ff"
	instanceID := "i-0123456789abcdef0"
	ec2m := &mockEC2{
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{
					NetworkInterfaceId: ptr("eni-tag-hit"),
					MacAddress:         &mac,
					Attachment: &ec2types.NetworkInterfaceAttachment{
						InstanceId: &instanceID,
					},
				},
			},
		},
	}

	if err := Phase17SecondaryENIs(context.Background(), cl, st, testClients(ec2m), false); err != nil {
		t.Fatalf("Phase17SecondaryENIs (tag-hit): %v", err)
	}

	if got := st.Get("INTERNAL_ENI_MAC"); got != mac {
		t.Errorf("INTERNAL_ENI_MAC = %q, want %q", got, mac)
	}
	if got := st.Get("EXTERNAL_ENI_MAC"); got != mac {
		t.Errorf("EXTERNAL_ENI_MAC = %q, want %q", got, mac)
	}
}

// TestPhase17SecondaryENIsDown_ToleratesNotFound verifies down succeeds even
// when no ENIs are in state or discoverable by tag.
func TestPhase17SecondaryENIsDown_ToleratesNotFound(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir) // empty state
	cl := testCluster()

	// DescribeNetworkInterfaces returns empty (tag lookup finds nothing).
	ec2m := &mockEC2{
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{},
		},
	}

	if err := Phase17SecondaryENIsDown(context.Background(), cl, st, testClients(ec2m)); err != nil {
		t.Fatalf("Phase17SecondaryENIsDown (not-found): %v", err)
	}
}

// TestPhase17SecondaryENIsDown_DetachesAndDeletes verifies the down path
// detaches and deletes ENIs present in state.
func TestPhase17SecondaryENIsDown_DetachesAndDeletes(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("INTERNAL_ENI", "eni-int-1")
	st.Set("EXTERNAL_ENI", "eni-ext-1")
	cl := testCluster()

	// DescribeNetworkInterfaces returns ENIs with status Available (already
	// detached) — simplifies the down path (no detach needed, direct delete).
	ec2m := &mockEC2{
		describeENIsOut: &ec2.DescribeNetworkInterfacesOutput{
			NetworkInterfaces: []ec2types.NetworkInterface{
				{
					NetworkInterfaceId: ptr("eni-int-1"),
					Status:             ec2types.NetworkInterfaceStatusAvailable,
				},
			},
		},
	}

	if err := Phase17SecondaryENIsDown(context.Background(), cl, st, testClients(ec2m)); err != nil {
		t.Fatalf("Phase17SecondaryENIsDown: %v", err)
	}

	// Both ENI ID state keys cleared.
	if got := st.Get("INTERNAL_ENI"); got != "" {
		t.Errorf("INTERNAL_ENI = %q after down, want empty", got)
	}
	if got := st.Get("EXTERNAL_ENI"); got != "" {
		t.Errorf("EXTERNAL_ENI = %q after down, want empty", got)
	}
	// MAC keys cleared.
	if got := st.Get("INTERNAL_ENI_MAC"); got != "" {
		t.Errorf("INTERNAL_ENI_MAC = %q after down, want empty", got)
	}
	if got := st.Get("EXTERNAL_ENI_MAC"); got != "" {
		t.Errorf("EXTERNAL_ENI_MAC = %q after down, want empty", got)
	}

	// Delete was attempted.
	if ec2m.deleteENICalls < 1 {
		t.Errorf("deleteENICalls = %d, want at least 1", ec2m.deleteENICalls)
	}
}
