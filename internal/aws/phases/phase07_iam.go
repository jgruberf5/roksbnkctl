package phases

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/tags"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// trustDoc is the typed Go struct for AssumeRole trust policy documents.
// Using a struct + json.Marshal avoids string templates and ensures valid JSON.
type trustDoc struct {
	Version   string `json:"Version"`
	Statement []struct {
		Effect    string            `json:"Effect"`
		Principal map[string]string `json:"Principal"`
		Action    string            `json:"Action"`
	} `json:"Statement"`
}

// tmmVpcRoutePolicy is the inline policy attached to the node role.
// Allows VPC route table management for self-managed node groups.
const tmmVpcRoutePolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":["ec2:CreateRoute","ec2:DeleteRoute","ec2:ReplaceRoute","ec2:DescribeRouteTables","ec2:DescribeVpcs","ec2:DescribeSubnets","ec2:DescribeNetworkInterfaces","ec2:ModifyNetworkInterfaceAttribute"],"Resource":"*"}]}`

// Phase07IAM creates the EKS cluster service role, node instance role, and
// node instance profile. All resources are tagged and state ARNs are persisted.
//
// Idempotent: GetRole / GetInstanceProfile by name before creating.
// Dry-run: writes placeholder state values, makes zero IAM API mutations.
func Phase07IAM(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 07] iam: cluster=%s\n", name)

	clusterRoleName := name + "-eks-cluster-role"
	nodeRoleName := name + "-eks-node-role"
	profileName := name + "-node-instance-profile"

	sgName := name + "-bnk-data"

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 07] dry-run: would create cluster role %s\n", clusterRoleName)
		fmt.Fprintf(os.Stderr, "[phase 07] dry-run: would create node role %s\n", nodeRoleName)
		fmt.Fprintf(os.Stderr, "[phase 07] dry-run: would create instance profile %s\n", profileName)
		fmt.Fprintf(os.Stderr, "[phase 07] dry-run: would create security group %s (bnk-data-plane)\n", sgName)
		st.Set("EKS_CLUSTER_ROLE_ARN", "arn:aws:iam::dry-run:role/"+clusterRoleName)
		st.Set("EKS_NODE_ROLE_ARN", "arn:aws:iam::dry-run:role/"+nodeRoleName)
		st.Set("NODE_INSTANCE_PROFILE_NAME", profileName)
		st.Set("NODE_INSTANCE_PROFILE_ARN", "arn:aws:iam::dry-run:instance-profile/"+profileName)
		st.Set("SG_BNK_DATA", "sg-dry-run-bnk-data")
		return nil
	}

	// --- EKS cluster service role ---
	clusterRoleARN, err := ensureRole(ctx, clients.IAM, clusterRoleName, "eks.amazonaws.com",
		[]string{
			"arn:aws:iam::aws:policy/AmazonEKSClusterPolicy",
			"arn:aws:iam::aws:policy/AmazonEKSVPCResourceController",
		},
		"",
		tags.IAMTags(
			tags.Required(name, tags.CompIAMClusterRole),
			cl.Tags,
			cl.Metadata.Labels,
		),
	)
	if err != nil {
		return fmt.Errorf("phase07: cluster role: %w", err)
	}
	st.Set("EKS_CLUSTER_ROLE_ARN", clusterRoleARN)

	// --- EKS node instance role ---
	nodeRoleARN, err := ensureRole(ctx, clients.IAM, nodeRoleName, "ec2.amazonaws.com",
		[]string{
			"arn:aws:iam::aws:policy/AmazonEKSWorkerNodePolicy",
			"arn:aws:iam::aws:policy/AmazonEC2ContainerRegistryReadOnly",
			"arn:aws:iam::aws:policy/AmazonEKS_CNI_Policy",
			// NOTE: service-role/ path prefix is required — the policy lives under
			// arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy, NOT
			// arn:aws:iam::aws:policy/AmazonEBSCSIDriverPolicy. Wrong path = not found.
			"arn:aws:iam::aws:policy/service-role/AmazonEBSCSIDriverPolicy",
		},
		tmmVpcRoutePolicy,
		tags.IAMTags(
			tags.Required(name, tags.CompIAMNodeRole),
			cl.Tags,
			cl.Metadata.Labels,
		),
	)
	if err != nil {
		return fmt.Errorf("phase07: node role: %w", err)
	}
	st.Set("EKS_NODE_ROLE_ARN", nodeRoleARN)

	// --- Node instance profile ---
	profileARN, err := ensureInstanceProfile(ctx, clients.IAM, profileName, nodeRoleName,
		tags.IAMTags(
			tags.Required(name, tags.CompIAMNodeProfile),
			cl.Tags,
			cl.Metadata.Labels,
		),
	)
	if err != nil {
		return fmt.Errorf("phase07: instance profile: %w", err)
	}
	st.Set("NODE_INSTANCE_PROFILE_NAME", profileName)
	st.Set("NODE_INSTANCE_PROFILE_ARN", profileARN)

	// --- SG_BNK_DATA security group (BNK data-plane) ---
	vpcID := st.Get("VPC_ID")
	if vpcID == "" {
		return fmt.Errorf("phase07: VPC_ID not in state (run phase02 first)")
	}
	sgID, err := ensureBNKDataSG(ctx, clients.EC2, name, vpcID, cl.Tags, cl.Metadata.Labels)
	if err != nil {
		return fmt.Errorf("phase07: SG_BNK_DATA: %w", err)
	}
	st.Set("SG_BNK_DATA", sgID)

	return st.Save()
}

// Phase07IAMDown destroys IAM resources in reverse-create order.
// Each step tolerates NoSuchEntity (already gone).
// Destroy order: remove role from profile → delete profile → detach + delete
// inline policies on node role → delete node role → same for cluster role.
func Phase07IAMDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 07 down] iam: cluster=%s\n", name)

	// Primary: well-known name convention. Fallback: tag scan via findRoleByTag
	// when GetRole returns NoSuchEntity (handles manual rename / cross-account copy).
	clusterRoleName := name + "-eks-cluster-role"
	if _, err := clients.IAM.GetRole(ctx, &iam.GetRoleInput{RoleName: ptr(clusterRoleName)}); isNoSuchEntity(err) {
		if found, scanErr := findRoleByTag(ctx, clients.IAM, name, tags.CompIAMClusterRole); scanErr == nil && found != "" {
			fmt.Fprintf(os.Stderr, "[phase 07 down] cluster role name diverged from convention; found by tag: %s\n", found)
			clusterRoleName = found
		}
	}
	nodeRoleName := name + "-eks-node-role"
	if _, err := clients.IAM.GetRole(ctx, &iam.GetRoleInput{RoleName: ptr(nodeRoleName)}); isNoSuchEntity(err) {
		if found, scanErr := findRoleByTag(ctx, clients.IAM, name, tags.CompIAMNodeRole); scanErr == nil && found != "" {
			fmt.Fprintf(os.Stderr, "[phase 07 down] node role name diverged from convention; found by tag: %s\n", found)
			nodeRoleName = found
		}
	}
	profileName := st.Get("NODE_INSTANCE_PROFILE_NAME")
	if profileName == "" {
		profileName = name + "-node-instance-profile"
	}

	iamClient := clients.IAM

	// 1. Remove node role from instance profile.
	fmt.Fprintf(os.Stderr, "[phase 07 down] removing role from instance profile %s\n", profileName)
	_, err := iamClient.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
		InstanceProfileName: ptr(profileName),
		RoleName:            ptr(nodeRoleName),
	})
	if err != nil && !isNoSuchEntity(err) {
		return fmt.Errorf("phase07 down: RemoveRoleFromInstanceProfile: %w", err)
	}

	// 2. Delete instance profile.
	fmt.Fprintf(os.Stderr, "[phase 07 down] deleting instance profile %s\n", profileName)
	_, err = iamClient.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{
		InstanceProfileName: ptr(profileName),
	})
	if err != nil && !isNoSuchEntity(err) {
		return fmt.Errorf("phase07 down: DeleteInstanceProfile: %w", err)
	}
	st.Set("NODE_INSTANCE_PROFILE_NAME", "")
	st.Set("NODE_INSTANCE_PROFILE_ARN", "")

	// 3–5. Tear down node role.
	if err := deleteRole(ctx, iamClient, nodeRoleName); err != nil {
		return fmt.Errorf("phase07 down: node role: %w", err)
	}
	st.Set("EKS_NODE_ROLE_ARN", "")

	// 6–8. Tear down cluster role.
	if err := deleteRole(ctx, iamClient, clusterRoleName); err != nil {
		return fmt.Errorf("phase07 down: cluster role: %w", err)
	}
	st.Set("EKS_CLUSTER_ROLE_ARN", "")

	// 9. Delete SG_BNK_DATA security group.
	sgID := st.Get("SG_BNK_DATA")
	if sgID == "" {
		// Name-based lookup fallback.
		sgID = lookupSGByName(ctx, clients.EC2, name+"-bnk-data")
	}
	if sgID != "" {
		// EKS leaves its managed cluster SG (eks-cluster-sg-<cluster>-*) behind
		// when cross-SG rules reference SG_BNK_DATA (phase 18 wires cluster-SG ↔
		// bnk-data both ways, and its state-driven revoke is skipped when down
		// runs from tag-discovery with no CLUSTER_SG_ID). Those references make
		// DeleteSecurityGroup fail with DependencyViolation, so clear them by
		// discovery first and remove the orphaned EKS SG shell.
		if err := clearSGReferences(ctx, clients.EC2, sgID, name); err != nil {
			fmt.Fprintf(os.Stderr, "[phase 07 down] warning: clearing SG references: %v\n", err)
		}
		fmt.Fprintf(os.Stderr, "[phase 07 down] deleting security group %s (SG_BNK_DATA)\n", sgID)
		_, err := clients.EC2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: ptr(sgID)})
		if err := ignoreNotFound(err); err != nil {
			return fmt.Errorf("phase07 down: DeleteSecurityGroup SG_BNK_DATA: %w", err)
		}
	}
	st.Set("SG_BNK_DATA", "")

	return st.Save()
}

// ensureBNKDataSG creates the SG_BNK_DATA security group (bnk-data-plane) in
// the VPC with an intra-VPC ingress rule (allow all from VPC CIDR). Idempotent.
func ensureBNKDataSG(ctx context.Context, ec2c EC2API, clusterName, vpcID string,
	extraTags, labels map[string]string) (string, error) {

	sgName := clusterName + "-bnk-data"

	// Idempotency: look up by name+VPC.
	existing, err := findSGByName(ctx, ec2c, clusterName, vpcID, sgName)
	if err != nil {
		return "", fmt.Errorf("looking up SG_BNK_DATA: %w", err)
	}
	if existing != "" {
		fmt.Fprintf(os.Stderr, "[phase 07] SG_BNK_DATA %s already exists, skipping\n", existing)
		return existing, nil
	}

	sgTags := tags.Merge(
		tags.Required(clusterName, tags.CompSGBNKData),
		extraTags,
		labels,
	)
	out, err := ec2c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   ptr(sgName),
		Description: ptr("bnk-data-plane"),
		VpcId:       ptr(vpcID),
		TagSpecifications: []ec2types.TagSpecification{
			tagSpecification(ec2types.ResourceTypeSecurityGroup, sgTags),
		},
	})
	if err != nil {
		return "", fmt.Errorf("ec2:CreateSecurityGroup %s: %w", sgName, err)
	}
	sgID := *out.GroupId
	fmt.Fprintf(os.Stderr, "[phase 07] created SG_BNK_DATA %s (%s)\n", sgID, sgName)

	// Add intra-VPC ingress: allow all traffic from VPC CIDR.
	// This enables TMM secondary ENIs to communicate with the EKS control plane
	// and other in-VPC resources. The cluster-SG ingress from SG_BNK_DATA is
	// added in Phase 18 (depends on EKS cluster SG).
	_, err = ec2c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: ptr(sgID),
		IpPermissions: []ec2types.IpPermission{
			{
				IpProtocol: ptr("-1"),
				UserIdGroupPairs: []ec2types.UserIdGroupPair{
					{GroupId: ptr(sgID), Description: ptr("intra-sg-self")},
				},
			},
		},
	})
	if err != nil {
		// Tolerate duplicate-rule errors (DuplicatePermission).
		if !isEC2DuplicatePermission(err) {
			return "", fmt.Errorf("ec2:AuthorizeSecurityGroupIngress SG_BNK_DATA self: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "[phase 07] added intra-SG ingress rule to %s\n", sgID)
	return sgID, nil
}

// findSGByName looks up a security group by name within the cluster's VPC using
// the awsbnkctl:cluster tag as the primary filter.
func findSGByName(ctx context.Context, ec2c EC2API, clusterName, vpcID, sgName string) (string, error) {
	out, err := ec2c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			tags.ClusterFilter(clusterName),
			{Name: ptr("vpc-id"), Values: []string{vpcID}},
			{Name: ptr("group-name"), Values: []string{sgName}},
		},
	})
	if err != nil {
		return "", err
	}
	if len(out.SecurityGroups) == 0 {
		return "", nil
	}
	return *out.SecurityGroups[0].GroupId, nil
}

// clearSGReferences removes everything that would make DeleteSecurityGroup on
// sgID fail with DependencyViolation: it finds every SG whose ingress rules
// reference sgID, revokes exactly those referencing rules, and best-effort
// deletes the orphaned EKS-managed cluster SG shell
// (eks-cluster-sg-<cluster>-*) — EKS leaves it behind when the cluster is
// deleted while cross-SG references exist. Errors on individual revokes are
// returned; the caller treats the whole step as best-effort.
func clearSGReferences(ctx context.Context, ec2c EC2API, sgID, clusterName string) error {
	out, err := ec2c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{Name: ptr("ip-permission.group-id"), Values: []string{sgID}},
		},
	})
	if err != nil {
		return fmt.Errorf("ec2:DescribeSecurityGroups referencing %s: %w", sgID, err)
	}
	for _, sg := range out.SecurityGroups {
		// Build the subset of this SG's ingress permissions that reference sgID.
		var perms []ec2types.IpPermission
		for _, p := range sg.IpPermissions {
			var pairs []ec2types.UserIdGroupPair
			for _, g := range p.UserIdGroupPairs {
				if g.GroupId != nil && *g.GroupId == sgID {
					pairs = append(pairs, ec2types.UserIdGroupPair{GroupId: g.GroupId})
				}
			}
			if len(pairs) > 0 {
				perms = append(perms, ec2types.IpPermission{
					IpProtocol:       p.IpProtocol,
					FromPort:         p.FromPort,
					ToPort:           p.ToPort,
					UserIdGroupPairs: pairs,
				})
			}
		}
		if len(perms) == 0 || sg.GroupId == nil {
			continue
		}
		fmt.Fprintf(os.Stderr, "[phase 07 down] revoking %d rule(s) referencing %s from %s\n",
			len(perms), sgID, *sg.GroupId)
		_, err := ec2c.RevokeSecurityGroupIngress(ctx, &ec2.RevokeSecurityGroupIngressInput{
			GroupId:       sg.GroupId,
			IpPermissions: perms,
		})
		if err != nil && !isInvalidPermissionNotFound(err) {
			return fmt.Errorf("ec2:RevokeSecurityGroupIngress %s: %w", *sg.GroupId, err)
		}
		// Orphaned EKS-managed shell: the cluster is already gone by the time
		// phase 07 down runs, so the SG serves nothing — delete it. Tolerate
		// failures (it may still be referenced elsewhere); the primary goal,
		// unblocking sgID, is already achieved by the revoke above.
		if *sg.GroupId != sgID && sg.GroupName != nil &&
			strings.HasPrefix(*sg.GroupName, "eks-cluster-sg-"+clusterName+"-") {
			fmt.Fprintf(os.Stderr, "[phase 07 down] deleting orphaned EKS cluster SG %s (%s)\n",
				*sg.GroupId, *sg.GroupName)
			if _, err := ec2c.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{GroupId: sg.GroupId}); err != nil {
				if e := ignoreNotFound(err); e != nil {
					fmt.Fprintf(os.Stderr, "[phase 07 down] warning: orphaned EKS SG %s not deleted: %v\n", *sg.GroupId, e)
				}
			}
		}
	}
	return nil
}

// lookupSGByName does a best-effort name-based lookup for the down-path
// fallback. Returns "" on any error (down continues).
func lookupSGByName(ctx context.Context, ec2c EC2API, sgName string) string {
	out, err := ec2c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			{Name: ptr("group-name"), Values: []string{sgName}},
		},
	})
	if err != nil || len(out.SecurityGroups) == 0 {
		return ""
	}
	return *out.SecurityGroups[0].GroupId
}

// isEC2DuplicatePermission returns true for the EC2 DuplicatePermission error
// code (rule already exists).
func isEC2DuplicatePermission(err error) bool {
	if err == nil {
		return false
	}
	type coder interface{ ErrorCode() string }
	e := err
	for e != nil {
		if ce, ok := e.(coder); ok {
			return ce.ErrorCode() == "InvalidPermission.Duplicate"
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

// --- helpers ---

// buildTrustPolicy serialises an AssumeRole trust policy for the given service
// principal (e.g. "eks.amazonaws.com").
func buildTrustPolicy(servicePrincipal string) (string, error) {
	doc := trustDoc{
		Version: "2012-10-17",
		Statement: []struct {
			Effect    string            `json:"Effect"`
			Principal map[string]string `json:"Principal"`
			Action    string            `json:"Action"`
		}{
			{
				Effect:    "Allow",
				Principal: map[string]string{"Service": servicePrincipal},
				Action:    "sts:AssumeRole",
			},
		},
	}
	b, err := json.Marshal(doc)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// ensureRole creates an IAM role if it does not exist; returns the ARN.
// managedPolicies are attached (idempotent). inlinePolicy (if non-empty) is
// written as "TmmVpcRoute" via PutRolePolicy (always idempotent).
func ensureRole(
	ctx context.Context,
	iamClient IAMAPI,
	roleName, servicePrincipal string,
	managedPolicies []string,
	inlinePolicy string,
	iamTags []iamtypes.Tag,
) (string, error) {
	// Check existence by name.
	getOut, err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: ptr(roleName)})
	if err != nil && !isNoSuchEntity(err) {
		return "", fmt.Errorf("GetRole %s: %w", roleName, err)
	}

	var roleARN string
	if err == nil {
		// Role already exists.
		roleARN = *getOut.Role.Arn
		fmt.Fprintf(os.Stderr, "[phase 07] role %s already exists, skipping create\n", roleName)
	} else {
		// Create the role.
		trustPolicy, err := buildTrustPolicy(servicePrincipal)
		if err != nil {
			return "", fmt.Errorf("buildTrustPolicy: %w", err)
		}
		createOut, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 ptr(roleName),
			AssumeRolePolicyDocument: ptr(trustPolicy),
			Tags:                     iamTags,
		})
		if err != nil {
			return "", fmt.Errorf("CreateRole %s: %w", roleName, err)
		}
		roleARN = *createOut.Role.Arn
		fmt.Fprintf(os.Stderr, "[phase 07] created role %s (%s)\n", roleName, roleARN)
	}

	// Attach managed policies (skip already-attached).
	attached, err := listAttachedPolicies(ctx, iamClient, roleName)
	if err != nil {
		return "", fmt.Errorf("ListAttachedRolePolicies %s: %w", roleName, err)
	}
	for _, policyARN := range managedPolicies {
		if attached[policyARN] {
			fmt.Fprintf(os.Stderr, "[phase 07] policy %s already attached to %s, skipping\n", policyARN, roleName)
			continue
		}
		if _, err := iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
			RoleName:  ptr(roleName),
			PolicyArn: ptr(policyARN),
		}); err != nil {
			return "", fmt.Errorf("AttachRolePolicy %s → %s: %w", policyARN, roleName, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 07] attached policy %s to %s\n", policyARN, roleName)
	}

	// Inline policy (PutRolePolicy is always idempotent — overwrites).
	if inlinePolicy != "" {
		if _, err := iamClient.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
			RoleName:       ptr(roleName),
			PolicyName:     ptr("TmmVpcRoute"),
			PolicyDocument: ptr(inlinePolicy),
		}); err != nil {
			return "", fmt.Errorf("PutRolePolicy TmmVpcRoute → %s: %w", roleName, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 07] put inline policy TmmVpcRoute on %s\n", roleName)
	}

	return roleARN, nil
}

// ensureInstanceProfile creates a node instance profile if absent, adds the
// node role to it (idempotent via ListInstanceProfilesForRole check).
func ensureInstanceProfile(
	ctx context.Context,
	iamClient IAMAPI,
	profileName, nodeRoleName string,
	iamTags []iamtypes.Tag,
) (string, error) {
	getOut, err := iamClient.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{
		InstanceProfileName: ptr(profileName),
	})
	if err != nil && !isNoSuchEntity(err) {
		return "", fmt.Errorf("GetInstanceProfile %s: %w", profileName, err)
	}

	var profileARN string
	if err == nil {
		profileARN = *getOut.InstanceProfile.Arn
		fmt.Fprintf(os.Stderr, "[phase 07] instance profile %s already exists, skipping create\n", profileName)
	} else {
		createOut, err := iamClient.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
			InstanceProfileName: ptr(profileName),
			Tags:                iamTags,
		})
		if err != nil {
			return "", fmt.Errorf("CreateInstanceProfile %s: %w", profileName, err)
		}
		profileARN = *createOut.InstanceProfile.Arn
		fmt.Fprintf(os.Stderr, "[phase 07] created instance profile %s (%s)\n", profileName, profileARN)
	}

	// Check whether the node role is already in this profile before adding.
	// AddRoleToInstanceProfile errors with LimitExceededException when a role is
	// already present (max 1 role per profile). Only add if absent.
	alreadyAttached, err := roleInProfile(ctx, iamClient, nodeRoleName)
	if err != nil {
		return "", fmt.Errorf("ListInstanceProfilesForRole %s: %w", nodeRoleName, err)
	}
	if !alreadyAttached {
		if _, err := iamClient.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
			InstanceProfileName: ptr(profileName),
			RoleName:            ptr(nodeRoleName),
		}); err != nil {
			return "", fmt.Errorf("AddRoleToInstanceProfile %s → %s: %w", nodeRoleName, profileName, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 07] added role %s to instance profile %s\n", nodeRoleName, profileName)
	} else {
		fmt.Fprintf(os.Stderr, "[phase 07] role %s already in instance profile %s, skipping\n", nodeRoleName, profileName)
	}

	return profileARN, nil
}

// deleteRole detaches all managed policies, deletes all inline policies, then
// deletes the role. Tolerates NoSuchEntity at every step.
func deleteRole(ctx context.Context, iamClient IAMAPI, roleName string) error {
	// Detach managed policies.
	attached, err := listAttachedPolicies(ctx, iamClient, roleName)
	if err != nil && !isNoSuchEntity(err) {
		return fmt.Errorf("ListAttachedRolePolicies %s: %w", roleName, err)
	}
	for policyARN := range attached {
		policyARN := policyARN
		fmt.Fprintf(os.Stderr, "[phase 07 down] detaching policy %s from %s\n", policyARN, roleName)
		if _, err := iamClient.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
			RoleName:  ptr(roleName),
			PolicyArn: ptr(policyARN),
		}); err != nil && !isNoSuchEntity(err) {
			return fmt.Errorf("DetachRolePolicy %s: %w", policyARN, err)
		}
	}

	// Delete inline policies.
	inlineOut, err := iamClient.ListRolePolicies(ctx, &iam.ListRolePoliciesInput{RoleName: ptr(roleName)})
	if err != nil && !isNoSuchEntity(err) {
		return fmt.Errorf("ListRolePolicies %s: %w", roleName, err)
	}
	if err == nil {
		for _, policyName := range inlineOut.PolicyNames {
			policyName := policyName
			fmt.Fprintf(os.Stderr, "[phase 07 down] deleting inline policy %s from %s\n", policyName, roleName)
			if _, err := iamClient.DeleteRolePolicy(ctx, &iam.DeleteRolePolicyInput{
				RoleName:   ptr(roleName),
				PolicyName: ptr(policyName),
			}); err != nil && !isNoSuchEntity(err) {
				return fmt.Errorf("DeleteRolePolicy %s: %w", policyName, err)
			}
		}
	}

	// Delete the role itself.
	fmt.Fprintf(os.Stderr, "[phase 07 down] deleting role %s\n", roleName)
	if _, err := iamClient.DeleteRole(ctx, &iam.DeleteRoleInput{RoleName: ptr(roleName)}); err != nil {
		if isNoSuchEntity(err) {
			fmt.Fprintf(os.Stderr, "[phase 07 down] role %s already gone\n", roleName)
			return nil
		}
		return fmt.Errorf("DeleteRole %s: %w", roleName, err)
	}
	return nil
}

// listAttachedPolicies returns the set of policy ARNs currently attached to a
// role, keyed by ARN for O(1) lookup.
func listAttachedPolicies(ctx context.Context, iamClient IAMAPI, roleName string) (map[string]bool, error) {
	out, err := iamClient.ListAttachedRolePolicies(ctx, &iam.ListAttachedRolePoliciesInput{
		RoleName: ptr(roleName),
	})
	if err != nil {
		return nil, err
	}
	result := make(map[string]bool, len(out.AttachedPolicies))
	for _, p := range out.AttachedPolicies {
		result[*p.PolicyArn] = true
	}
	return result, nil
}

// roleInProfile returns true if the given role name appears in any instance
// profile listed by ListInstanceProfilesForRole.
func roleInProfile(ctx context.Context, iamClient IAMAPI, roleName string) (bool, error) {
	out, err := iamClient.ListInstanceProfilesForRole(ctx, &iam.ListInstanceProfilesForRoleInput{
		RoleName: ptr(roleName),
	})
	if err != nil {
		if isNoSuchEntity(err) {
			return false, nil
		}
		return false, err
	}
	return len(out.InstanceProfiles) > 0, nil
}

// isNoSuchEntity returns true when err is an IAM NoSuchEntityException.
// Uses errors.As for robustness across SDK versions.
func isNoSuchEntity(err error) bool {
	if err == nil {
		return false
	}
	var nse *iamtypes.NoSuchEntityException
	return errors.As(err, &nse)
}

// findRoleByTag scans all IAM roles (paginating via Marker) and returns the name
// of the first role that carries awsbnkctl:cluster=<clusterName> AND
// awsbnkctl:component=<component>. Returns "" when no match is found.
//
// This is the fallback for Phase07IAMDown when the well-known naming convention
// has diverged (e.g. after a manual rename or cross-account copy).
func findRoleByTag(ctx context.Context, iamClient IAMAPI, clusterName, component string) (string, error) {
	var marker *string
	for {
		out, err := iamClient.ListRoles(ctx, &iam.ListRolesInput{Marker: marker})
		if err != nil {
			return "", fmt.Errorf("ListRoles: %w", err)
		}
		for _, r := range out.Roles {
			if r.RoleName == nil {
				continue
			}
			tagsOut, err := iamClient.ListRoleTags(ctx, &iam.ListRoleTagsInput{RoleName: r.RoleName})
			if err != nil {
				// Skip inaccessible roles rather than aborting the scan.
				continue
			}
			var hasCluster, hasComponent bool
			for _, t := range tagsOut.Tags {
				if t.Key == nil || t.Value == nil {
					continue
				}
				if *t.Key == tags.KeyCluster && *t.Value == clusterName {
					hasCluster = true
				}
				if *t.Key == tags.KeyComponent && *t.Value == component {
					hasComponent = true
				}
			}
			if hasCluster && hasComponent {
				return *r.RoleName, nil
			}
		}
		if !out.IsTruncated {
			break
		}
		marker = out.Marker
	}
	return "", nil
}
