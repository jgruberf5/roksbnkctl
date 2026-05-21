package phases

import (
	"context"
	"strings"
	"testing"

	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
)

// seedMockEKSCluster adds a cluster with OIDC identity to the mock EKS
// so that DescribeCluster returns a valid issuer URL.
func seedMockEKSCluster(eksMock *mockEKS, clusterName string) {
	issuer := "https://oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"
	sgID := "sg-" + clusterName
	arn := "arn:aws:eks:ap-southeast-2:111122223333:cluster/" + clusterName
	endpoint := "https://" + clusterName + ".eks.amazonaws.com"
	ca := "dGVzdC1jYQ=="
	version := "1.30"
	status := ekstypes.ClusterStatusActive
	eksMock.clusters[clusterName] = &ekstypes.Cluster{
		Name:                 ptr(clusterName),
		Arn:                  &arn,
		Status:               status,
		Endpoint:             &endpoint,
		CertificateAuthority: &ekstypes.Certificate{Data: &ca},
		Identity: &ekstypes.Identity{
			Oidc: &ekstypes.OIDC{Issuer: &issuer},
		},
		ResourcesVpcConfig: &ekstypes.VpcConfigResponse{
			ClusterSecurityGroupId: &sgID,
		},
		Version: &version,
	}
}

// TestPhase18IRSAOIDC_DryRun verifies no AWS mutations and placeholder state.
func TestPhase18IRSAOIDC_DryRun(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()

	iamMock := newMockIAM()
	clients := &Clients{
		EC2:     &mockEC2{},
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     iamMock,
		EKS:     newMockEKS(),
		Profile: "test",
	}

	if err := Phase18IRSAOIDC(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase18IRSAOIDC dry-run: %v", err)
	}

	// Zero IAM mutations.
	if iamMock.createOIDCCalls != 0 {
		t.Errorf("dry-run: createOIDCCalls = %d, want 0", iamMock.createOIDCCalls)
	}
	if iamMock.createRoleCalls != 0 {
		t.Errorf("dry-run: createRoleCalls = %d, want 0", iamMock.createRoleCalls)
	}

	// Placeholder state values.
	if got := st.Get("OIDC_PROVIDER_ARN"); !strings.HasPrefix(got, "arn:aws:iam::dry-run:oidc-provider") {
		t.Errorf("OIDC_PROVIDER_ARN = %q, want arn:aws:iam::dry-run:oidc-provider/...", got)
	}
	name := cl.Metadata.Name
	if got := st.Get("CNE_IRSA_ROLE_NAME"); got != name+"-cne-controller-irsa" {
		t.Errorf("CNE_IRSA_ROLE_NAME = %q, want %s-cne-controller-irsa", got, name)
	}
	if got := st.Get("CNE_IRSA_ROLE_ARN"); !strings.HasPrefix(got, "arn:aws:iam::dry-run:role/") {
		t.Errorf("CNE_IRSA_ROLE_ARN = %q, want arn:aws:iam::dry-run:role/...", got)
	}
}

// TestPhase18IRSAOIDC_IRSARoleCreate verifies that ensureIRSARole creates the
// role with the correct federated trust policy and CneControllerVpcRead inline
// policy. This exercises the create path without triggering TLS thumbprint.
func TestPhase18IRSAOIDC_IRSARoleCreate(t *testing.T) {
	awsmw.ResetForTest()
	cl := testCluster()
	iamMock := newMockIAM()

	oidcHost := "oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"
	accountID := "111122223333"
	roleName := cl.Metadata.Name + "-cne-controller-irsa"

	roleARN, err := ensureIRSARole(context.Background(), iamMock, cl.Metadata.Name, roleName,
		oidcHost, accountID, "f5-cne-system", "f5-cne-controller-tracer-serviceaccount",
		nil, nil)
	if err != nil {
		t.Fatalf("ensureIRSARole: %v", err)
	}
	if !strings.HasPrefix(roleARN, "arn:aws:iam::") {
		t.Errorf("roleARN = %q, want arn:aws:iam::...", roleARN)
	}
	if iamMock.createRoleCalls != 1 {
		t.Errorf("createRoleCalls = %d, want 1", iamMock.createRoleCalls)
	}
	if iamMock.putRolePolicyCalls != 1 {
		t.Errorf("putRolePolicyCalls = %d, want 1", iamMock.putRolePolicyCalls)
	}
	inlines := iamMock.inlinePolicies[roleName]
	found := false
	for _, n := range inlines {
		if n == "CneControllerVpcRead" {
			found = true
		}
	}
	if !found {
		t.Errorf("CneControllerVpcRead not in inline policies for %s: %v", roleName, inlines)
	}
}

// TestPhase18IRSAOIDC_IRSARoleIdempotent verifies a second ensureIRSARole call
// does not re-create the role.
func TestPhase18IRSAOIDC_IRSARoleIdempotent(t *testing.T) {
	awsmw.ResetForTest()
	cl := testCluster()
	iamMock := newMockIAM()

	oidcHost := "oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"
	accountID := "111122223333"
	roleName := cl.Metadata.Name + "-cne-controller-irsa"

	if _, err := ensureIRSARole(context.Background(), iamMock, cl.Metadata.Name, roleName,
		oidcHost, accountID, "f5-cne-system", "sa-name", nil, nil); err != nil {
		t.Fatalf("ensureIRSARole run1: %v", err)
	}
	createAfterRun1 := iamMock.createRoleCalls

	if _, err := ensureIRSARole(context.Background(), iamMock, cl.Metadata.Name, roleName,
		oidcHost, accountID, "f5-cne-system", "sa-name", nil, nil); err != nil {
		t.Fatalf("ensureIRSARole run2: %v", err)
	}
	if iamMock.createRoleCalls != createAfterRun1 {
		t.Errorf("run2: createRoleCalls increased from %d to %d", createAfterRun1, iamMock.createRoleCalls)
	}
}

// TestPhase18IrsaOidcDown_DeletesRoleAndProvider verifies the down path deletes
// the IRSA role and OIDC provider when keepIRSA=false.
func TestPhase18IrsaOidcDown_DeletesRoleAndProvider(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()
	iamMock := newMockIAM()

	roleName := cl.Metadata.Name + "-cne-controller-irsa"
	oidcARN := "arn:aws:iam::111122223333:oidc-provider/oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"
	roleARN := "arn:aws:iam::111122223333:role/" + roleName

	// Pre-populate the mock with the role and OIDC provider.
	iamMock.roles[roleName] = &iamtypes.Role{RoleName: &roleName, Arn: &roleARN}
	iamMock.attachedPolicies[roleName] = make(map[string]bool)
	iamMock.inlinePolicies[roleName] = []string{"CneControllerVpcRead"}
	iamURL := "https://oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"
	iamMock.oidcProviders[oidcARN] = iamURL

	st.Set("CNE_IRSA_ROLE_NAME", roleName)
	st.Set("OIDC_PROVIDER_ARN", oidcARN)

	clients := &Clients{
		EC2:     &mockEC2{},
		IAM:     iamMock,
		EKS:     newMockEKS(),
		Profile: "test",
	}

	if err := Phase18IrsaOidcDown(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase18IrsaOidcDown: %v", err)
	}

	// Role deleted.
	if _, ok := iamMock.roles[roleName]; ok {
		t.Error("IRSA role still in mock after down")
	}
	// OIDC provider deleted.
	if _, ok := iamMock.oidcProviders[oidcARN]; ok {
		t.Error("OIDC provider still in mock after down")
	}
	if iamMock.deleteOIDCCalls != 1 {
		t.Errorf("deleteOIDCCalls = %d, want 1", iamMock.deleteOIDCCalls)
	}

	// State cleared.
	if st.Get("CNE_IRSA_ROLE_NAME") != "" {
		t.Errorf("CNE_IRSA_ROLE_NAME not cleared after down")
	}
	if st.Get("OIDC_PROVIDER_ARN") != "" {
		t.Errorf("OIDC_PROVIDER_ARN not cleared after down")
	}
}

// TestPhase18IrsaOidcDown_KeepIRSA verifies --keep-irsa retains BOTH the OIDC
// provider AND the IRSA role (mirrors --keep-iam/--keep-keypair symmetry).
func TestPhase18IrsaOidcDown_KeepIRSA(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()
	iamMock := newMockIAM()

	roleName := cl.Metadata.Name + "-cne-controller-irsa"
	oidcARN := "arn:aws:iam::111122223333:oidc-provider/oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"
	roleARN := "arn:aws:iam::111122223333:role/" + roleName

	iamMock.roles[roleName] = &iamtypes.Role{RoleName: &roleName, Arn: &roleARN}
	iamMock.attachedPolicies[roleName] = make(map[string]bool)
	iamMock.inlinePolicies[roleName] = []string{"CneControllerVpcRead"}
	iamURL := "https://oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"
	iamMock.oidcProviders[oidcARN] = iamURL

	st.Set("CNE_IRSA_ROLE_NAME", roleName)
	st.Set("OIDC_PROVIDER_ARN", oidcARN)

	clients := &Clients{
		EC2:     &mockEC2{},
		IAM:     iamMock,
		EKS:     newMockEKS(),
		Profile: "test",
	}

	if err := Phase18IrsaOidcDown(context.Background(), cl, st, clients, true); err != nil {
		t.Fatalf("Phase18IrsaOidcDown (keep-irsa): %v", err)
	}

	// IRSA role retained — no DeleteRole or DeleteRolePolicy calls.
	if _, ok := iamMock.roles[roleName]; !ok {
		t.Error("IRSA role was deleted despite keep-irsa=true")
	}
	if iamMock.deleteRoleCalls != 0 {
		t.Errorf("deleteRoleCalls = %d, want 0 (keep-irsa retains role)", iamMock.deleteRoleCalls)
	}

	// OIDC provider retained — no DeleteOpenIDConnectProvider calls.
	if iamMock.deleteOIDCCalls != 0 {
		t.Errorf("deleteOIDCCalls = %d, want 0 (keep-irsa)", iamMock.deleteOIDCCalls)
	}
	if _, ok := iamMock.oidcProviders[oidcARN]; !ok {
		t.Error("OIDC provider was deleted despite keep-irsa=true")
	}
}

// TestPhase18IrsaOidcDown_ToleratesEmpty verifies down is a no-op when state
// and mock are empty.
func TestPhase18IrsaOidcDown_ToleratesEmpty(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir) // empty state
	cl := testCluster()

	clients := &Clients{
		EC2:     &mockEC2{},
		IAM:     newMockIAM(),
		EKS:     newMockEKS(),
		Profile: "test",
	}

	if err := Phase18IrsaOidcDown(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase18IrsaOidcDown empty state: %v", err)
	}
}

// TestPhase18IrsaOidc_AddsClusterSGIngress verifies that Phase18IRSAOIDC calls
// AuthorizeSecurityGroupIngress with GroupId=EKS_SECURITY_GROUP and a permission
// referencing SG_BNK_DATA. This is a regression test for the CLUSTER_SG →
// EKS_SECURITY_GROUP key-name fix.
func TestPhase18IrsaOidc_AddsClusterSGIngress(t *testing.T) {
	awsmw.ResetForTest()
	dir := t.TempDir()
	st, _ := state.Load(dir)
	cl := testCluster()

	// Seed prerequisites that Phase18IRSAOIDC reads from state.
	st.Set("SG_BNK_DATA", "sg-bnk-data-id")
	st.Set("EKS_SECURITY_GROUP", "sg-eks-cluster-id")
	// Pre-populate OIDC_PROVIDER_ARN to skip the real TLS thumbprint + IAM create path.
	oidcARN := "arn:aws:iam::111122223333:oidc-provider/oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"
	st.Set("OIDC_PROVIDER_ARN", oidcARN)

	iamMock := newMockIAM()
	// Pre-populate the OIDC provider so GetOpenIDConnectProvider succeeds (idempotent path).
	iamMock.oidcProviders[oidcARN] = "https://oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC"

	ec2Mock := &mockEC2{}
	eksMock := newMockEKS()
	seedMockEKSCluster(eksMock, cl.Metadata.Name)

	clients := &Clients{
		EC2:     ec2Mock,
		STS:     &mockSTSImpl{accountID: "111122223333"},
		IAM:     iamMock,
		EKS:     eksMock,
		Profile: "test",
	}

	if err := Phase18IRSAOIDC(context.Background(), cl, st, clients, false); err != nil {
		t.Fatalf("Phase18IRSAOIDC: %v", err)
	}

	// AuthorizeSecurityGroupIngress must have been called exactly once.
	if ec2Mock.authorizeIngressCalls != 1 {
		t.Fatalf("authorizeIngressCalls = %d, want 1", ec2Mock.authorizeIngressCalls)
	}

	// GroupId must be the EKS cluster SG, not SG_BNK_DATA.
	if ec2Mock.authorizeIngressInput == nil {
		t.Fatal("authorizeIngressInput is nil")
	}
	if ec2Mock.authorizeIngressInput.GroupId == nil || *ec2Mock.authorizeIngressInput.GroupId != "sg-eks-cluster-id" {
		got := "<nil>"
		if ec2Mock.authorizeIngressInput.GroupId != nil {
			got = *ec2Mock.authorizeIngressInput.GroupId
		}
		t.Errorf("GroupId = %q, want sg-eks-cluster-id", got)
	}

	// IpPermissions must reference SG_BNK_DATA as the source.
	if len(ec2Mock.authorizeIngressInput.IpPermissions) == 0 {
		t.Fatal("IpPermissions is empty")
	}
	perm := ec2Mock.authorizeIngressInput.IpPermissions[0]
	if len(perm.UserIdGroupPairs) == 0 {
		t.Fatal("UserIdGroupPairs is empty")
	}
	if perm.UserIdGroupPairs[0].GroupId == nil || *perm.UserIdGroupPairs[0].GroupId != "sg-bnk-data-id" {
		got := "<nil>"
		if perm.UserIdGroupPairs[0].GroupId != nil {
			got = *perm.UserIdGroupPairs[0].GroupId
		}
		t.Errorf("source GroupId = %q, want sg-bnk-data-id", got)
	}
}

// TestExtractAccountID verifies the account ID extraction helper.
func TestExtractAccountID(t *testing.T) {
	tests := []struct {
		arn  string
		want string
	}{
		{"arn:aws:iam::111122223333:role/my-role", "111122223333"},
		{"arn:aws:iam::111122223333:oidc-provider/oidc.eks.amazonaws.com/id/ABC", "111122223333"},
		{"invalid", ""},
	}
	for _, tc := range tests {
		if got := extractAccountID(tc.arn); got != tc.want {
			t.Errorf("extractAccountID(%q) = %q, want %q", tc.arn, got, tc.want)
		}
	}
}

// TestOIDCFederatedTrustPolicy verifies the trust policy is valid JSON with the
// expected sts:AssumeRoleWithWebIdentity action and condition.
func TestOIDCFederatedTrustPolicy(t *testing.T) {
	policy, err := oidcFederatedTrustPolicy(
		"oidc.eks.ap-southeast-2.amazonaws.com/id/TESTOIDC",
		"111122223333",
		"f5-cne-system",
		"f5-cne-controller-tracer-sa",
	)
	if err != nil {
		t.Fatalf("oidcFederatedTrustPolicy: %v", err)
	}
	for _, want := range []string{
		"sts:AssumeRoleWithWebIdentity",
		"f5-cne-system",
		"f5-cne-controller-tracer-sa",
		"111122223333",
		"Federated",
	} {
		if !strings.Contains(policy, want) {
			t.Errorf("trust policy missing %q: %s", want, policy)
		}
	}
}
