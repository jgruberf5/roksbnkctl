//lint:file-ignore SA1019 k8sfake.NewSimpleClientset is still functional — NewClientset requires --with-applyconfig codegen

package phases

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// testClientsAllMock returns a Clients with all mocks wired, K8s nil.
func testClientsAllMock() *Clients {
	return &Clients{
		EC2:     &mockEC2{},
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     newMockIAM(),
		EKS:     newMockEKS(),
		Profile: "test",
	}
}

// makeNode returns a corev1.Node with the given name and labels.
func makeNode(name string, labels map[string]string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   name,
			Labels: labels,
		},
	}
}

// TestPhase16TMMNodeLabel_DryRun verifies zero k8s/EC2 mutations and
// placeholder state values.
func TestPhase16TMMNodeLabel_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()

	clients := testClientsAllMock()
	// K8s is nil in dry-run — phase must skip it.
	if err := Phase16TMMNodeLabel(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase16TMMNodeLabel dry-run: %v", err)
	}

	if got := st.Get("TMM_NODE_NAME"); got != "dry-run-tmm-node" {
		t.Errorf("TMM_NODE_NAME = %q, want dry-run-tmm-node", got)
	}
	if got := st.Get("TMM_INSTANCE_ID"); got != "dry-run-i-xxx" {
		t.Errorf("TMM_INSTANCE_ID = %q, want dry-run-i-xxx", got)
	}
}

// TestPhase16TMMNodeLabel_NoK8sClient verifies error when K8s is nil and
// not dry-run.
func TestPhase16TMMNodeLabel_NoK8sClient(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()

	clients := testClientsAllMock() // K8s is nil

	err := Phase16TMMNodeLabel(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error when K8s client is nil, got nil")
	}
	if !strings.Contains(err.Error(), "K8s client not attached") {
		t.Errorf("error does not mention K8s client: %v", err)
	}
}

// TestPhase16TMMNodeLabel_NoNodes verifies error when no role=bnk nodes exist.
func TestPhase16TMMNodeLabel_NoNodes(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()

	clients := testClientsAllMock()
	clients.K8s = k8sfake.NewSimpleClientset() // no nodes

	err := Phase16TMMNodeLabel(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error when no role=bnk nodes, got nil")
	}
	if !strings.Contains(err.Error(), "no nodes found") {
		t.Errorf("error does not mention no-nodes: %v", err)
	}
}

// TestPhase16TMMNodeLabel_PicksFirstNode verifies the phase picks the first
// role=bnk node, labels it app=f5-tmm, resolves the EC2 instance ID, and
// persists both state keys.
func TestPhase16TMMNodeLabel_PicksFirstNode(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()

	nodeName := "ip-10-0-1-100.ap-southeast-2.compute.internal"
	node1 := makeNode(nodeName, map[string]string{"role": "bnk"})
	node2 := makeNode("ip-10-0-1-200.ap-southeast-2.compute.internal",
		map[string]string{"role": "bnk"})

	fakeK8s := k8sfake.NewSimpleClientset(node1, node2)

	instanceID := "i-0123456789abcdef0"
	ec2m := &mockEC2{
		describeInstancesOut: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{
				{Instances: []ec2types.Instance{{InstanceId: &instanceID}}},
			},
		},
	}

	clients := &Clients{
		EC2:     ec2m,
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     newMockIAM(),
		EKS:     newMockEKS(),
		K8s:     fakeK8s,
		Profile: "test",
	}

	if err := Phase16TMMNodeLabel(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase16TMMNodeLabel: %v", err)
	}

	if got := st.Get("TMM_NODE_NAME"); got != nodeName {
		t.Errorf("TMM_NODE_NAME = %q, want %q", got, nodeName)
	}
	if got := st.Get("TMM_INSTANCE_ID"); got != instanceID {
		t.Errorf("TMM_INSTANCE_ID = %q, want %q", got, instanceID)
	}
}

// TestPhase16TMMNodeLabel_InstanceNotFound verifies error when no running
// EC2 instance matches the node's private-dns-name.
func TestPhase16TMMNodeLabel_InstanceNotFound(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()

	node := makeNode("ip-10-0-1-100.ap-southeast-2.compute.internal",
		map[string]string{"role": "bnk"})
	fakeK8s := k8sfake.NewSimpleClientset(node)

	// No instances returned.
	ec2m := &mockEC2{
		describeInstancesOut: &ec2.DescribeInstancesOutput{
			Reservations: []ec2types.Reservation{},
		},
	}

	clients := &Clients{
		EC2:     ec2m,
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     newMockIAM(),
		EKS:     newMockEKS(),
		K8s:     fakeK8s,
		Profile: "test",
	}

	err := Phase16TMMNodeLabel(context.Background(), cl, st, clients, false)
	if err == nil {
		t.Fatal("expected error when instance not found, got nil")
	}
	if !strings.Contains(err.Error(), "no running EC2 instance") {
		t.Errorf("error does not mention instance not found: %v", err)
	}
}

// TestPhase16TMMNodeLabelDown_ClearsState verifies down clears TMM state keys.
func TestPhase16TMMNodeLabelDown_ClearsState(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	st.Set("TMM_NODE_NAME", "ip-10-0-1-100.compute.internal")
	st.Set("TMM_INSTANCE_ID", "i-0123456789abcdef0")
	cl := testCluster()

	if err := Phase16TMMNodeLabelDown(context.Background(), cl, st, testClientsAllMock()); err != nil {
		t.Fatalf("Phase16TMMNodeLabelDown: %v", err)
	}

	if got := st.Get("TMM_NODE_NAME"); got != "" {
		t.Errorf("TMM_NODE_NAME = %q after down, want empty", got)
	}
	if got := st.Get("TMM_INSTANCE_ID"); got != "" {
		t.Errorf("TMM_INSTANCE_ID = %q after down, want empty", got)
	}
}
