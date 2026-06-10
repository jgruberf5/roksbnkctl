package phases

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

func TestPhase04IGW_CreatesWhenAbsent(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("VPC_ID", "vpc-0abc")

	igwID := "igw-0new"
	ec2m := &mockEC2{
		createIGWOut: &ec2.CreateInternetGatewayOutput{
			InternetGateway: &ec2types.InternetGateway{InternetGatewayId: &igwID},
		},
	}

	if err := Phase04IGW(context.Background(), testCluster(), st, testClients(ec2m), false); err != nil {
		t.Fatalf("Phase04IGW: %v", err)
	}
	if ec2m.createIGWCalls != 1 {
		t.Errorf("expected 1 CreateInternetGateway call, got %d", ec2m.createIGWCalls)
	}
	if ec2m.attachIGWCalls != 1 {
		t.Errorf("expected 1 AttachInternetGateway call, got %d", ec2m.attachIGWCalls)
	}
	if st.Get("IGW_ID") != igwID {
		t.Errorf("IGW_ID in state: got %q, want %q", st.Get("IGW_ID"), igwID)
	}
}

func TestPhase04IGW_SkipsWhenPresent(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("VPC_ID", "vpc-0abc")

	existingID := "igw-0existing"
	ec2m := &mockEC2{
		describeIGWsOut: &ec2.DescribeInternetGatewaysOutput{
			InternetGateways: []ec2types.InternetGateway{
				{InternetGatewayId: &existingID},
			},
		},
	}

	if err := Phase04IGW(context.Background(), testCluster(), st, testClients(ec2m), false); err != nil {
		t.Fatalf("Phase04IGW: %v", err)
	}
	if ec2m.createIGWCalls != 0 {
		t.Errorf("expected 0 CreateInternetGateway calls when IGW exists, got %d", ec2m.createIGWCalls)
	}
	if st.Get("IGW_ID") != existingID {
		t.Errorf("IGW_ID in state: got %q, want %q", st.Get("IGW_ID"), existingID)
	}
}

func TestPhase04IGW_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("VPC_ID", "vpc-0abc")
	ec2m := &mockEC2{}

	if err := Phase04IGW(context.Background(), testCluster(), st, testClients(ec2m), true); err != nil {
		t.Fatalf("Phase04IGW dry-run: %v", err)
	}
	if ec2m.createIGWCalls != 0 {
		t.Errorf("expected 0 CreateInternetGateway calls in dry-run, got %d", ec2m.createIGWCalls)
	}
}

func TestPhase04IGW_ErrorsWithoutVPCID(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	if err := Phase04IGW(context.Background(), testCluster(), st, testClients(&mockEC2{}), false); err == nil {
		t.Fatal("expected error when VPC_ID missing")
	}
}

// TestPhase04IGWDown_DiscoversVPCForDetach pins the tag-discovery regression:
// when state carries no VPC_ID (down running from tag-discovery), the detach
// must read the attached VPC from the IGW itself instead of being silently
// skipped — a skipped detach makes DeleteInternetGateway fail with
// DependencyViolation (hit live on bnk-demo teardown, 2026-06-10).
func TestPhase04IGWDown_DiscoversVPCForDetach(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("IGW_ID", "igw-0attached")
	// VPC_ID deliberately absent.

	igwID := "igw-0attached"
	vpcID := "vpc-0discovered"
	ec2m := &mockEC2{
		describeIGWsOut: &ec2.DescribeInternetGatewaysOutput{
			InternetGateways: []ec2types.InternetGateway{{
				InternetGatewayId: &igwID,
				Attachments:       []ec2types.InternetGatewayAttachment{{VpcId: &vpcID}},
			}},
		},
	}
	if err := Phase04IGWDown(context.Background(), testCluster(), st, testClients(ec2m)); err != nil {
		t.Fatalf("Phase04IGWDown: %v", err)
	}
	if len(ec2m.detachIGWInputs) != 1 {
		t.Fatalf("expected exactly 1 DetachInternetGateway call, got %d", len(ec2m.detachIGWInputs))
	}
	if got := *ec2m.detachIGWInputs[0].VpcId; got != vpcID {
		t.Fatalf("detach used VpcId %q, want discovered %q", got, vpcID)
	}
}

func TestPhase04IGWDown_ToleratesAlreadyGone(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("IGW_ID", "igw-0gone")
	st.Set("VPC_ID", "vpc-0abc")

	ec2m := &mockEC2{}
	if err := Phase04IGWDown(context.Background(), testCluster(), st, testClients(ec2m)); err != nil {
		t.Fatalf("Phase04IGWDown: %v", err)
	}
}
