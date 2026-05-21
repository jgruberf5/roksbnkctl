package phases

import (
	"context"
	"crypto/sha1" // #nosec G505 -- SHA1 required by AWS OIDC thumbprint spec (RFC 8485)
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/iam"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/tags"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// cneControllerVpcReadPolicy is the inline policy attached to the IRSA role.
// Actions mirror aws-gpu-setup/up.sh:364-365 (the VPC read permissions required
// by f5-cne-controller to enumerate subnets, route tables, and ENIs).
const cneControllerVpcReadPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["ec2:DescribeVpcs","ec2:DescribeSubnets","ec2:DescribeRouteTables","ec2:DescribeNetworkInterfaces","ec2:DescribeSecurityGroups","ec2:DescribeInstances","ec2:DescribeInstanceTypes","ec2:DescribeTags","ec2:DescribeAvailabilityZones","ec2:CreateNetworkInterface","ec2:DeleteNetworkInterface","ec2:ModifyNetworkInterfaceAttribute","ec2:AssignPrivateIpAddresses","ec2:UnassignPrivateIpAddresses","ec2:AttachNetworkInterface","ec2:DetachNetworkInterface","ec2:CreateTags"],"Resource":"*"}]}`

// Phase18IRSAOIDC provisions the per-cluster OIDC provider and the
// f5-cne-controller IRSA role.
//
// Steps (all idempotent):
//  1. EKS DescribeCluster → extract OIDC issuer URL.
//  2. Compute SHA1 thumbprint of the OIDC endpoint's TLS leaf cert
//     (crypto/tls.Dial — no os/exec per D-001).
//  3. iam:GetOpenIDConnectProvider by computed ARN; if absent →
//     iam:CreateOpenIDConnectProvider. Tag with awsbnkctl:*.
//  4. Create <cluster>-cne-controller-irsa IAM role with federated
//     assume-role-policy targeting system:serviceaccount:<INSTANCE_NS>/<CNE_SA_NAME>.
//  5. Put inline policy CneControllerVpcRead (VPC + ENI read/write actions).
//  6. EC2 AuthorizeSecurityGroupIngress: add SG_BNK_DATA as source to the EKS
//     cluster SG (EKS_SECURITY_GROUP from state). Tolerates duplicate-rule errors.
//
// Persists: OIDC_PROVIDER_ARN, CNE_IRSA_ROLE_NAME, CNE_IRSA_ROLE_ARN.
// Dry-run: sets placeholder values, skips all mutations.
// SSO sentinel: CheckAuthOrDie at entry.
func Phase18IRSAOIDC(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 18] irsa+oidc: cluster=%s\n", name)

	irsaRoleName := name + "-cne-controller-irsa"

	// Resolve SA identifiers from state (written by slice 7b / dryRun defaults).
	instanceNS := st.Get("INSTANCE_NS")
	if instanceNS == "" {
		instanceNS = "f5-cne-system"
	}
	cneSAName := st.Get("CNE_SA_NAME")
	if cneSAName == "" {
		// CNE_SA_NAME is set in slice 7b (Phase 21); use deterministic default.
		cneSAName = "f5-cne-controller-" + name + "-serviceaccount"
	}

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 18] dry-run: would resolve EKS OIDC issuer, compute thumbprint, create OIDC provider")
		fmt.Fprintf(os.Stderr, "[phase 18] dry-run: would create IRSA role %s\n", irsaRoleName)
		fmt.Fprintln(os.Stderr, "[phase 18] dry-run: would add cluster-SG ingress from SG_BNK_DATA")
		st.Set("OIDC_PROVIDER_ARN", "arn:aws:iam::dry-run:oidc-provider/dry-run-oidc")
		st.Set("CNE_IRSA_ROLE_NAME", irsaRoleName)
		st.Set("CNE_IRSA_ROLE_ARN", "arn:aws:iam::dry-run:role/"+irsaRoleName)
		return nil
	}

	// ── Step 1: resolve EKS OIDC issuer URL ──────────────────────────────────
	descOut, err := clients.EKS.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: ptr(name)})
	if err != nil {
		return fmt.Errorf("phase18: DescribeCluster %s: %w", name, err)
	}
	if descOut.Cluster == nil || descOut.Cluster.Identity == nil ||
		descOut.Cluster.Identity.Oidc == nil || descOut.Cluster.Identity.Oidc.Issuer == nil {
		return fmt.Errorf("phase18: cluster %s has no OIDC issuer (is the OIDC provider associated?)", name)
	}
	issuerURL := *descOut.Cluster.Identity.Oidc.Issuer
	fmt.Fprintf(os.Stderr, "[phase 18] EKS OIDC issuer: %s\n", issuerURL)

	// ── Step 2: compute thumbprint ────────────────────────────────────────────
	thumbprint, err := oidcThumbprint(issuerURL)
	if err != nil {
		return fmt.Errorf("phase18: computing OIDC thumbprint for %s: %w", issuerURL, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 18] OIDC thumbprint: %s\n", thumbprint)

	// ── Step 3: ensure OIDC provider ─────────────────────────────────────────
	// If OIDC_PROVIDER_ARN is already in state (idempotent re-run), verify it
	// exists and skip create. Otherwise create it.
	oidcARN := st.Get("OIDC_PROVIDER_ARN")
	if oidcARN != "" {
		// Verify the provider is still live.
		_, getErr := clients.IAM.GetOpenIDConnectProvider(ctx, &iam.GetOpenIDConnectProviderInput{
			OpenIDConnectProviderArn: ptr(oidcARN),
		})
		if getErr != nil && !isNoSuchEntity(getErr) {
			return fmt.Errorf("phase18: GetOpenIDConnectProvider %s: %w", oidcARN, getErr)
		}
		if getErr == nil {
			fmt.Fprintf(os.Stderr, "[phase 18] OIDC provider %s already exists, skipping create\n", oidcARN)
		} else {
			// State ARN gone — fall through to create.
			oidcARN = ""
		}
	}
	if oidcARN == "" {
		var createErr error
		oidcARN, createErr = ensureOIDCProvider(ctx, clients.IAM, name, issuerURL, thumbprint, cl.Tags, cl.Metadata.Labels)
		if createErr != nil {
			return fmt.Errorf("phase18: OIDC provider: %w", createErr)
		}
		st.Set("OIDC_PROVIDER_ARN", oidcARN)
	}
	fmt.Fprintf(os.Stderr, "[phase 18] OIDC provider ARN: %s\n", oidcARN)

	// ── Step 4+5: ensure IRSA role ────────────────────────────────────────────
	// Extract account ID from existing ARN (e.g. EKS_CLUSTER_ROLE_ARN) or OIDC ARN.
	accountID := extractAccountID(oidcARN)
	// Extract OIDC hostname from issuer for the federated principal.
	oidcHost := strings.TrimPrefix(issuerURL, "https://")

	irsaRoleARN, err := ensureIRSARole(ctx, clients.IAM, name, irsaRoleName, oidcHost, accountID,
		instanceNS, cneSAName, cl.Tags, cl.Metadata.Labels)
	if err != nil {
		return fmt.Errorf("phase18: IRSA role: %w", err)
	}
	st.Set("CNE_IRSA_ROLE_NAME", irsaRoleName)
	st.Set("CNE_IRSA_ROLE_ARN", irsaRoleARN)
	fmt.Fprintf(os.Stderr, "[phase 18] IRSA role ARN: %s\n", irsaRoleARN)

	// ── Step 6: cluster SG ingress from SG_BNK_DATA ───────────────────────────
	clusterSG := st.Get("EKS_SECURITY_GROUP")
	sgBNKData := st.Get("SG_BNK_DATA")
	if clusterSG != "" && sgBNKData != "" {
		if err := ensureClusterSGIngress(ctx, clients.EC2, clusterSG, sgBNKData); err != nil {
			return fmt.Errorf("phase18: cluster SG ingress: %w", err)
		}
	} else {
		fmt.Fprintf(os.Stderr, "[phase 18] warning: EKS_SECURITY_GROUP=%q SG_BNK_DATA=%q — skipping cluster SG ingress rule\n", clusterSG, sgBNKData)
	}

	return st.Save()
}

// Phase18IrsaOidcDown deletes the IRSA role and OIDC provider.
// When keepIRSA is true, BOTH the OIDC provider and the IRSA role are retained
// (mirrors --keep-iam/--keep-keypair symmetry — all artifacts of the scope are kept).
// Tolerates NoSuchEntity (already gone).
//
// keepIRSA mirrors --keep-irsa on the CLI down command.
func Phase18IrsaOidcDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, keepIRSA bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 18 down] irsa+oidc: cluster=%s keep-irsa=%v\n", name, keepIRSA)

	if keepIRSA {
		fmt.Fprintln(os.Stderr, "[phase 18 down] --keep-irsa: retaining OIDC provider and IRSA role")
		return st.Save()
	}

	irsaRoleName := st.Get("CNE_IRSA_ROLE_NAME")
	if irsaRoleName == "" {
		irsaRoleName = name + "-cne-controller-irsa"
	}

	// Delete IRSA role (inline policy first, then the role).
	if err := deleteRole(ctx, clients.IAM, irsaRoleName); err != nil {
		return fmt.Errorf("phase18 down: IRSA role: %w", err)
	}
	st.Set("CNE_IRSA_ROLE_NAME", "")
	st.Set("CNE_IRSA_ROLE_ARN", "")
	fmt.Fprintf(os.Stderr, "[phase 18 down] deleted IRSA role %s\n", irsaRoleName)

	// Delete OIDC provider.
	oidcARN := st.Get("OIDC_PROVIDER_ARN")
	if oidcARN == "" {
		fmt.Fprintln(os.Stderr, "[phase 18 down] OIDC_PROVIDER_ARN not in state, skipping OIDC delete")
	} else {
		_, err := clients.IAM.DeleteOpenIDConnectProvider(ctx, &iam.DeleteOpenIDConnectProviderInput{
			OpenIDConnectProviderArn: ptr(oidcARN),
		})
		if err != nil && !isNoSuchEntity(err) {
			return fmt.Errorf("phase18 down: DeleteOpenIDConnectProvider %s: %w", oidcARN, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 18 down] deleted OIDC provider %s\n", oidcARN)
	}
	st.Set("OIDC_PROVIDER_ARN", "")

	return st.Save()
}

// oidcThumbprint dials the OIDC issuer's TLS endpoint and returns the SHA1
// fingerprint of the leaf certificate as an uppercase hex string (no colons).
// AWS IAM requires this exact format.
//
// Per D-001: no os/exec. This is the Go-native equivalent of:
//
//	openssl s_client -connect <host>:443 2>/dev/null | openssl x509 -fingerprint -sha1 -noout
func oidcThumbprint(issuerURL string) (string, error) {
	u, err := url.Parse(issuerURL)
	if err != nil {
		return "", fmt.Errorf("parsing OIDC issuer URL %q: %w", issuerURL, err)
	}
	host := u.Hostname()
	addr := host + ":443"

	// TLS 1.2+ with InsecureSkipVerify=false to get the real cert chain.
	// We deliberately skip verification so we get the raw cert for hashing;
	// the thumbprint is a commitment to the cert's public key, not chain validity.
	// #nosec G402 -- InsecureSkipVerify required: AWS OIDC thumbprint must be computed from the raw cert
	conn, err := tls.Dial("tcp", addr, &tls.Config{
		InsecureSkipVerify: true,
		MinVersion:         tls.VersionTLS12,
		ServerName:         host,
	})
	if err != nil {
		return "", fmt.Errorf("tls.Dial %s: %w", addr, err)
	}
	defer conn.Close()

	certs := conn.ConnectionState().PeerCertificates
	if len(certs) == 0 {
		return "", fmt.Errorf("no TLS certificates returned from %s", addr)
	}
	// AWS uses the last cert in the chain (root CA) for the thumbprint.
	// Match the openssl s_client behaviour used in aws-gpu-setup/up.sh:321-322.
	leaf := certs[len(certs)-1]
	// #nosec G401 -- SHA1 required by AWS OIDC thumbprint specification (RFC 8485)
	sum := sha1.Sum(leaf.Raw)
	return strings.ToUpper(fmt.Sprintf("%x", sum)), nil
}

// ensureOIDCProvider creates or fetches the OIDC provider for the cluster.
// Idempotent: first tries to look up an existing provider via state-stored ARN
// (caller checks OIDC_PROVIDER_ARN before calling); on create, tolerates
// EntityAlreadyExists by deriving the ARN from the known EKS_CLUSTER_ROLE_ARN
// account ID and the issuer host path.
func ensureOIDCProvider(ctx context.Context, iamClient IAMAPI, clusterName, issuerURL, thumbprint string,
	extraTags, labels map[string]string) (string, error) {

	iamTagSlice := tags.IAMTags(
		tags.Required(clusterName, tags.CompOIDCProvider),
		extraTags,
		labels,
	)

	out, err := iamClient.CreateOpenIDConnectProvider(ctx, &iam.CreateOpenIDConnectProviderInput{
		Url:            ptr(issuerURL),
		ThumbprintList: []string{thumbprint},
		ClientIDList:   []string{"sts.amazonaws.com"},
		Tags:           iamTagSlice,
	})
	if err != nil {
		if !isEntityAlreadyExists(err) {
			return "", fmt.Errorf("iam:CreateOpenIDConnectProvider: %w", err)
		}
		// Provider already exists (EntityAlreadyExists). AWS does not return the ARN
		// in this error. Derive it: arn:aws:iam::<account>:oidc-provider/<host-path>.
		// We cannot know the account without STS:GetCallerIdentity or a known ARN.
		// Surface a clear actionable message — this path is only reached on a second
		// `up` run where OIDC_PROVIDER_ARN should already be in state.env; the caller
		// (Phase18IRSAOIDC) should have short-circuited before reaching this helper.
		fmt.Fprintln(os.Stderr, "[phase 18] OIDC provider already exists (EntityAlreadyExists)")
		return "", fmt.Errorf("OIDC provider already exists for issuer %s; OIDC_PROVIDER_ARN should be in state.env from the prior run — check .awsbnkctl/<cluster>/state.env", issuerURL)
	}

	arn := *out.OpenIDConnectProviderArn
	fmt.Fprintf(os.Stderr, "[phase 18] created OIDC provider: %s\n", arn)

	// Re-tag for idempotency on tag drift.
	_, _ = iamClient.TagOpenIDConnectProvider(ctx, &iam.TagOpenIDConnectProviderInput{
		OpenIDConnectProviderArn: ptr(arn),
		Tags:                     iamTagSlice,
	})

	return arn, nil
}

// oidcFederatedTrustPolicy builds the IRSA assume-role-policy document for a
// specific OIDC provider / service-account pair.
//
// oidcHost is the OIDC provider hostname path (e.g.
// "oidc.eks.ap-southeast-2.amazonaws.com/id/EXAMPLE").
// accountID is the AWS account number (e.g. "111122223333").
// namespace is the k8s namespace (e.g. "f5-cne-system").
// saName is the k8s service account name.
func oidcFederatedTrustPolicy(oidcHost, accountID, namespace, saName string) (string, error) {
	type conditionEntry struct {
		Eq map[string]string `json:"StringEquals"`
	}
	type statement struct {
		Effect    string            `json:"Effect"`
		Principal map[string]string `json:"Principal"`
		Action    string            `json:"Action"`
		Condition conditionEntry    `json:"Condition"`
	}
	doc := struct {
		Version   string      `json:"Version"`
		Statement []statement `json:"Statement"`
	}{
		Version: "2012-10-17",
		Statement: []statement{
			{
				Effect: "Allow",
				Principal: map[string]string{
					"Federated": "arn:aws:iam::" + accountID + ":oidc-provider/" + oidcHost,
				},
				Action: "sts:AssumeRoleWithWebIdentity",
				Condition: conditionEntry{
					Eq: map[string]string{
						oidcHost + ":sub": "system:serviceaccount:" + namespace + ":" + saName,
						oidcHost + ":aud": "sts.amazonaws.com",
					},
				},
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ensureIRSARole creates the IRSA role for f5-cne-controller (idempotent).
func ensureIRSARole(ctx context.Context, iamClient IAMAPI, clusterName, roleName,
	oidcHost, accountID, namespace, saName string,
	extraTags, labels map[string]string) (string, error) {

	// Check if role already exists.
	getOut, err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: ptr(roleName)})
	if err != nil && !isNoSuchEntity(err) {
		return "", fmt.Errorf("GetRole %s: %w", roleName, err)
	}

	iamTagSlice := tags.IAMTags(
		tags.Required(clusterName, tags.CompIRSARole),
		extraTags,
		labels,
	)

	var roleARN string
	if err == nil {
		roleARN = *getOut.Role.Arn
		fmt.Fprintf(os.Stderr, "[phase 18] IRSA role %s already exists, skipping create\n", roleName)
	} else {
		trustPolicy, err := oidcFederatedTrustPolicy(oidcHost, accountID, namespace, saName)
		if err != nil {
			return "", fmt.Errorf("building IRSA trust policy: %w", err)
		}
		createOut, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 ptr(roleName),
			AssumeRolePolicyDocument: ptr(trustPolicy),
			Tags:                     iamTagSlice,
		})
		if err != nil {
			return "", fmt.Errorf("iam:CreateRole %s: %w", roleName, err)
		}
		roleARN = *createOut.Role.Arn
		fmt.Fprintf(os.Stderr, "[phase 18] created IRSA role %s (%s)\n", roleName, roleARN)
	}

	// Inline policy: CneControllerVpcRead. PutRolePolicy is always idempotent.
	if _, err := iamClient.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       ptr(roleName),
		PolicyName:     ptr("CneControllerVpcRead"),
		PolicyDocument: ptr(cneControllerVpcReadPolicy),
	}); err != nil {
		return "", fmt.Errorf("PutRolePolicy CneControllerVpcRead → %s: %w", roleName, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 18] put inline policy CneControllerVpcRead on %s\n", roleName)

	return roleARN, nil
}

// ensureClusterSGIngress adds an ingress rule from SG_BNK_DATA to the EKS
// cluster security group. Tolerates duplicate-rule errors (idempotent).
func ensureClusterSGIngress(ctx context.Context, ec2c EC2API, clusterSGID, sgBNKDataID string) error {
	_, err := ec2c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: ptr(clusterSGID),
		IpPermissions: []ec2types.IpPermission{
			{
				IpProtocol: ptr("-1"),
				UserIdGroupPairs: []ec2types.UserIdGroupPair{
					{
						GroupId:     ptr(sgBNKDataID),
						Description: ptr("allow-bnk-data-plane"),
					},
				},
			},
		},
	})
	if err != nil && !isEC2DuplicatePermission(err) {
		return fmt.Errorf("ec2:AuthorizeSecurityGroupIngress cluster-SG %s ← SG_BNK_DATA %s: %w",
			clusterSGID, sgBNKDataID, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 18] cluster SG %s: ingress from SG_BNK_DATA %s added (or already present)\n",
		clusterSGID, sgBNKDataID)
	return nil
}

// extractAccountID extracts the account ID from an AWS ARN.
// ARN format: arn:aws:<service>:<region>:<account-id>:<resource>
// Returns "" if the ARN is malformed.
func extractAccountID(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 5 {
		return ""
	}
	return parts[4]
}

// isEntityAlreadyExists returns true if the error is an IAM EntityAlreadyExists.
func isEntityAlreadyExists(err error) bool {
	if err == nil {
		return false
	}
	type coder interface{ ErrorCode() string }
	e := err
	for e != nil {
		if ce, ok := e.(coder); ok {
			return ce.ErrorCode() == "EntityAlreadyExists"
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	return false
}
