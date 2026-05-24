package phases

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/ssm"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/tags"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

const (
	// al2023AMIParam is the SSM parameter path for the latest x86_64 AL2023 AMI.
	al2023AMIParam = "/aws/service/ami-amazon-linux-latest/al2023-ami-kernel-default-x86_64"

	// jumphostSSMPolicyARN is the AWS-managed policy that enables EC2 Instance
	// Connect Endpoint to initiate SSH sessions to the instance.
	jumphostSSMPolicyARN = "arn:aws:iam::aws:policy/AmazonSSMManagedInstanceCore"

	// jumphostInstancePollInterval is the interval between instance-state polls.
	jumphostInstancePollInterval = 5 * time.Second
	// jumphostInstanceRunningTimeout is the maximum time to wait for running state.
	jumphostInstanceRunningTimeout = 5 * time.Minute
	// jumphostInstanceTerminatedTimeout is the maximum time to wait for terminated state.
	jumphostInstanceTerminatedTimeout = 10 * time.Minute

	// eicePollInterval / eiceReadyTimeout covers the time EICE spends in "creating" state.
	eicePollInterval = 5 * time.Second
	eiceReadyTimeout = 5 * time.Minute
)

// Phase17bJumphost provisions a multi-ENI EC2 jumphost + EC2 Instance Connect
// Endpoint (EICE) in the cluster's MGMT subnet. Feature-gated by
// testing.jumphost.enabled in cluster.yaml (default off — zero AWS calls when
// disabled).
//
// Provisioned resources (when enabled):
//  1. IAM role <cluster>-jumphost-role + instance profile with AmazonSSMManagedInstanceCore.
//  2. Security group <cluster>-jumphost: ingress 22/tcp from EICE SG only.
//  3. EICE in MGMT subnet. Reused if one with our tag already exists.
//  4. On-demand t3.small AL2023 instance in MGMT subnet.
//  5. Secondary ENI in BNK_EXT subnet attached at device-index=1, SG_BNK_DATA.
//
// Dry-run: sets placeholder state values, makes zero AWS mutations.
// SSO sentinel: CheckAuthOrDie at entry.
func Phase17bJumphost(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 17b] jumphost: cluster=%s\n", name)

	// Feature gate — check before any state reads.
	if cl.Testing == nil || cl.Testing.Jumphost == nil || !cl.Testing.Jumphost.Enabled {
		fmt.Fprintln(os.Stderr, "[phase 17b] skipped: jumphost disabled")
		return nil
	}

	jh := cl.Testing.Jumphost
	instanceType := jh.InstanceType
	if instanceType == "" {
		instanceType = "t3.small"
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 17b] dry-run: would resolve AL2023 AMI via SSM %s\n", al2023AMIParam)
		fmt.Fprintf(os.Stderr, "[phase 17b] dry-run: would create jumphost SG %s-jumphost in VPC\n", name)
		fmt.Fprintf(os.Stderr, "[phase 17b] dry-run: would create EICE in MGMT subnet\n")
		fmt.Fprintf(os.Stderr, "[phase 17b] dry-run: would launch %s instance in MGMT subnet\n", instanceType)
		fmt.Fprintf(os.Stderr, "[phase 17b] dry-run: would create+attach secondary ENI in BNK_EXT subnet\n")
		fmt.Fprintf(os.Stderr, "[phase 17b] dry-run: would set 11 JUMPHOST_* state keys\n")
		st.Set("JUMPHOST_INSTANCE_ID", "i-dry-run")
		st.Set("JUMPHOST_MGMT_ENI_ID", "eni-dry-run-mgmt")
		st.Set("JUMPHOST_MGMT_ENI_IP", "10.0.1.200")
		st.Set("JUMPHOST_BNK_EXT_ENI_ID", "eni-dry-run-ext")
		st.Set("JUMPHOST_BNK_EXT_ENI_IP", "10.0.10.200")
		st.Set("JUMPHOST_EICE_ID", "eice-dry-run")
		st.Set("JUMPHOST_EICE_SG_ID", "sg-dry-run-eice")
		st.Set("JUMPHOST_SG_ID", "sg-dry-run-jumphost")
		st.Set("JUMPHOST_AMI_ID", "ami-dry-run")
		st.Set("JUMPHOST_INSTANCE_TYPE", instanceType)
		st.Set("JUMPHOST_INSTANCE_PROFILE_NAME", name+"-jumphost-profile")
		st.Set("JUMPHOST_ROLE_NAME", name+"-jumphost-role")
		return nil
	}

	// Prerequisite state checks.
	vpcID := st.Get("VPC_ID")
	if vpcID == "" {
		return fmt.Errorf("phase17b: VPC_ID not in state (run phase02 first)")
	}
	sgBNKData := st.Get("SG_BNK_DATA")
	if sgBNKData == "" {
		return fmt.Errorf("phase17b: SG_BNK_DATA not in state (run phase07 first)")
	}
	bnkExtSubnet := st.Get("BNK_EXT_SUBNET")
	if bnkExtSubnet == "" {
		return fmt.Errorf("phase17b: BNK_EXT_SUBNET not in state (run phase03 first)")
	}
	// Pick MGMT subnet by index.
	if len(cl.Network.Subnets.Public) == 0 {
		return fmt.Errorf("phase17b: no public subnets defined in cluster.yaml")
	}
	idx := jh.MgmtSubnetIndex
	if idx >= len(cl.Network.Subnets.Public) {
		idx = 0
	}
	mgmtSubnetCIDR := cl.Network.Subnets.Public[idx].CIDR
	// Resolve MGMT subnet ID by parsing PUBLIC_SUBNETS (csv) from state.
	// State is flat key=value — no array indexing — so the original
	// `PUBLIC_SUBNETS[0]` lookup never resolved and the MGMT_SUBNET
	// fallback only works if phase 19 has already run (it hasn't at 17b time).
	publicCSV := st.Get("PUBLIC_SUBNETS")
	if publicCSV == "" {
		return fmt.Errorf("phase17b: PUBLIC_SUBNETS not in state (run phase03 first)")
	}
	publicIDs := strings.Split(publicCSV, ",")
	if idx >= len(publicIDs) {
		return fmt.Errorf("phase17b: mgmtSubnetIndex=%d but PUBLIC_SUBNETS has only %d entries", idx, len(publicIDs))
	}
	mgmtSubnetID := strings.TrimSpace(publicIDs[idx])
	if mgmtSubnetID == "" {
		return fmt.Errorf("phase17b: PUBLIC_SUBNETS[%d] is empty (corrupted state.env?)", idx)
	}
	_ = mgmtSubnetCIDR // available for logging if needed

	// Step 1: IAM role + instance profile.
	profileName, roleName, err := ensureJumphostInstanceProfile(ctx, clients.IAM, name, cl.Tags, cl.Metadata.Labels)
	if err != nil {
		return fmt.Errorf("phase17b: instance profile: %w", err)
	}
	st.Set("JUMPHOST_INSTANCE_PROFILE_NAME", profileName)
	st.Set("JUMPHOST_ROLE_NAME", roleName)

	// Step 2: AMI resolution.
	amiID := st.Get("JUMPHOST_AMI_ID")
	if amiID == "" {
		amiID, err = resolveAL2023AMI(ctx, clients.SSM)
		if err != nil {
			return fmt.Errorf("phase17b: AMI resolution: %w", err)
		}
	}
	st.Set("JUMPHOST_AMI_ID", amiID)
	fmt.Fprintf(os.Stderr, "[phase 17b] AMI: %s\n", amiID)

	// Step 3: jumphost security group.
	jumphostSGID, err := ensureJumphostSG(ctx, clients.EC2, name, vpcID, cl.Tags, cl.Metadata.Labels, st)
	if err != nil {
		return fmt.Errorf("phase17b: jumphost SG: %w", err)
	}
	st.Set("JUMPHOST_SG_ID", jumphostSGID)

	// Step 4: EICE.
	eiceID, eiceSGID, err := ensureJumphostEICE(ctx, clients.EC2, name, mgmtSubnetID, vpcID, cl.Tags, cl.Metadata.Labels, st)
	if err != nil {
		return fmt.Errorf("phase17b: EICE: %w", err)
	}
	st.Set("JUMPHOST_EICE_ID", eiceID)
	if eiceSGID != "" {
		st.Set("JUMPHOST_EICE_SG_ID", eiceSGID)
	}

	// Now that we have EICE SG, add ingress rule 22/tcp from EICE SG to jumphost SG.
	eiceSGIDFinal := st.Get("JUMPHOST_EICE_SG_ID")
	if eiceSGIDFinal != "" {
		if err := ensureJumphostSGIngress(ctx, clients.EC2, jumphostSGID, eiceSGIDFinal); err != nil {
			return fmt.Errorf("phase17b: jumphost SG ingress: %w", err)
		}
	}

	// Step 5: instance.
	instanceID, mgmtENIID, mgmtENIIP, err := ensureJumphostInstance(ctx, clients.EC2, name,
		mgmtSubnetID, jumphostSGID, amiID, instanceType, profileName, cl.Tags, cl.Metadata.Labels, st)
	if err != nil {
		return fmt.Errorf("phase17b: instance: %w", err)
	}
	st.Set("JUMPHOST_INSTANCE_ID", instanceID)
	st.Set("JUMPHOST_MGMT_ENI_ID", mgmtENIID)
	st.Set("JUMPHOST_MGMT_ENI_IP", mgmtENIIP)
	st.Set("JUMPHOST_INSTANCE_TYPE", instanceType)

	// Step 6: secondary ENI in BNK_EXT.
	extENIID, extENIIP, err := ensureJumphostSecondaryENI(ctx, clients.EC2, name, bnkExtSubnet, sgBNKData, instanceID, cl.Tags, cl.Metadata.Labels, st)
	if err != nil {
		return fmt.Errorf("phase17b: secondary ENI: %w", err)
	}
	st.Set("JUMPHOST_BNK_EXT_ENI_ID", extENIID)
	st.Set("JUMPHOST_BNK_EXT_ENI_IP", extENIIP)

	fmt.Fprintf(os.Stderr, "[phase 17b] jumphost ready: instance=%s mgmt-ip=%s ext-ip=%s eice=%s\n",
		instanceID, mgmtENIIP, extENIIP, eiceID)

	return st.Save()
}

// Phase17bJumphostDown tears down the jumphost resources in reverse order:
// terminate instance → delete secondary ENI → delete EICE → delete jumphost SG
// → delete IAM role+profile → clear state.
//
// Tolerates NotFound on every resource (idempotent down).
// Falls back to tag-discovery when state keys are absent.
func Phase17bJumphostDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 17b down] jumphost: cluster=%s\n", name)

	// Terminate instance.
	instanceID := st.Get("JUMPHOST_INSTANCE_ID")
	if instanceID == "" {
		instanceID = lookupInstanceByTag(ctx, clients.EC2, name, tags.CompJumphostInstance)
	}
	if instanceID != "" && instanceID != "i-dry-run" {
		fmt.Fprintf(os.Stderr, "[phase 17b down] terminating instance %s\n", instanceID)
		if _, err := clients.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds: []string{instanceID},
		}); err != nil {
			if e := ignoreNotFound(err); e != nil {
				return fmt.Errorf("phase17b down: TerminateInstances %s: %w", instanceID, err)
			}
		}
		if err := waitInstanceTerminated(ctx, clients.EC2, instanceID); err != nil {
			return fmt.Errorf("phase17b down: waiting for instance %s terminated: %w", instanceID, err)
		}
	}

	// Delete secondary ENI in BNK_EXT (now safe — instance is terminated).
	extENIID := st.Get("JUMPHOST_BNK_EXT_ENI_ID")
	if extENIID == "" {
		extENIID = lookupENIByTag(ctx, clients.EC2, name, tags.CompJumphostENIExt)
	}
	if extENIID != "" && extENIID != "eni-dry-run-ext" {
		fmt.Fprintf(os.Stderr, "[phase 17b down] deleting BNK_EXT ENI %s\n", extENIID)
		if err := detachAndDeleteENI(ctx, clients.EC2, extENIID); err != nil {
			return fmt.Errorf("phase17b down: BNK_EXT ENI: %w", err)
		}
	}

	// Delete EICE.
	eiceID := st.Get("JUMPHOST_EICE_ID")
	if eiceID == "" {
		eiceID = lookupEICEByTag(ctx, clients.EC2, name, tags.CompJumphostEICE)
	}
	if eiceID != "" && eiceID != "eice-dry-run" {
		fmt.Fprintf(os.Stderr, "[phase 17b down] deleting EICE %s\n", eiceID)
		if _, err := clients.EC2.DeleteInstanceConnectEndpoint(ctx, &ec2.DeleteInstanceConnectEndpointInput{
			InstanceConnectEndpointId: ptr(eiceID),
		}); err != nil {
			if e := ignoreNotFound(err); e != nil {
				return fmt.Errorf("phase17b down: DeleteInstanceConnectEndpoint %s: %w", eiceID, err)
			}
		}
	}

	// Delete jumphost SG.
	jumphostSGID := st.Get("JUMPHOST_SG_ID")
	if jumphostSGID == "" {
		jumphostSGID = lookupSGByTag(ctx, clients.EC2, name, tags.CompJumphostSG)
	}
	if jumphostSGID != "" && jumphostSGID != "sg-dry-run-jumphost" {
		fmt.Fprintf(os.Stderr, "[phase 17b down] deleting jumphost SG %s\n", jumphostSGID)
		if _, err := clients.EC2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
			GroupId: ptr(jumphostSGID),
		}); err != nil {
			if e := ignoreNotFound(err); e != nil {
				return fmt.Errorf("phase17b down: DeleteSecurityGroup jumphost %s: %w", jumphostSGID, err)
			}
		}
	}

	// Delete IAM instance profile + role.
	profileName := st.Get("JUMPHOST_INSTANCE_PROFILE_NAME")
	if profileName == "" {
		profileName = name + "-jumphost-profile"
	}
	roleName := st.Get("JUMPHOST_ROLE_NAME")
	if roleName == "" {
		roleName = name + "-jumphost-role"
	}
	if err := deleteJumphostIAM(ctx, clients.IAM, profileName, roleName); err != nil {
		return fmt.Errorf("phase17b down: IAM: %w", err)
	}

	clearJumphostState(st)
	return st.Save()
}

// ensureJumphostInstanceProfile creates (or finds) the IAM role and instance
// profile for the jumphost. Returns (profileName, roleName, error).
func ensureJumphostInstanceProfile(ctx context.Context, iamClient IAMAPI, clusterName string,
	extraTags, labels map[string]string) (string, string, error) {

	roleName := clusterName + "-jumphost-role"
	profileName := clusterName + "-jumphost-profile"

	iamTagSlice := tags.IAMTags(
		tags.Required(clusterName, tags.CompJumphostRole),
		extraTags,
		labels,
	)

	// Ensure role.
	_, err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: ptr(roleName)})
	if err != nil {
		if !isNoSuchEntity(err) {
			return "", "", fmt.Errorf("GetRole %s: %w", roleName, err)
		}
		// Create role with EC2 trust policy.
		ec2TrustPolicy := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`
		if _, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
			RoleName:                 ptr(roleName),
			AssumeRolePolicyDocument: ptr(ec2TrustPolicy),
			Tags:                     iamTagSlice,
		}); err != nil {
			return "", "", fmt.Errorf("iam:CreateRole %s: %w", roleName, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 17b] created IAM role %s\n", roleName)
	} else {
		fmt.Fprintf(os.Stderr, "[phase 17b] IAM role %s already exists\n", roleName)
	}

	// Attach AmazonSSMManagedInstanceCore (idempotent).
	if _, err := iamClient.AttachRolePolicy(ctx, &iam.AttachRolePolicyInput{
		RoleName:  ptr(roleName),
		PolicyArn: ptr(jumphostSSMPolicyARN),
	}); err != nil && !isDuplicatePolicy(err) {
		return "", "", fmt.Errorf("iam:AttachRolePolicy %s: %w", roleName, err)
	}

	// Ensure instance profile.
	profileTagSlice := tags.IAMTags(
		tags.Required(clusterName, tags.CompJumphostProfile),
		extraTags,
		labels,
	)
	_, err = iamClient.GetInstanceProfile(ctx, &iam.GetInstanceProfileInput{InstanceProfileName: ptr(profileName)})
	if err != nil {
		if !isNoSuchEntity(err) {
			return "", "", fmt.Errorf("GetInstanceProfile %s: %w", profileName, err)
		}
		if _, err := iamClient.CreateInstanceProfile(ctx, &iam.CreateInstanceProfileInput{
			InstanceProfileName: ptr(profileName),
			Tags:                profileTagSlice,
		}); err != nil {
			return "", "", fmt.Errorf("iam:CreateInstanceProfile %s: %w", profileName, err)
		}
		if _, err := iamClient.AddRoleToInstanceProfile(ctx, &iam.AddRoleToInstanceProfileInput{
			InstanceProfileName: ptr(profileName),
			RoleName:            ptr(roleName),
		}); err != nil {
			return "", "", fmt.Errorf("iam:AddRoleToInstanceProfile %s→%s: %w", roleName, profileName, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 17b] created instance profile %s\n", profileName)
	} else {
		fmt.Fprintf(os.Stderr, "[phase 17b] instance profile %s already exists\n", profileName)
	}

	return profileName, roleName, nil
}

// resolveAL2023AMI fetches the latest AL2023 x86_64 AMI ID from SSM Parameter Store.
func resolveAL2023AMI(ctx context.Context, ssmClient SSMAPI) (string, error) {
	out, err := ssmClient.GetParameter(ctx, &ssm.GetParameterInput{
		Name: ptr(al2023AMIParam),
	})
	if err != nil {
		return "", fmt.Errorf("SSM GetParameter %s: %w (is this parameter available in your region?)", al2023AMIParam, err)
	}
	if out.Parameter == nil || out.Parameter.Value == nil {
		return "", fmt.Errorf("SSM parameter %s returned nil value", al2023AMIParam)
	}
	return *out.Parameter.Value, nil
}

// ensureJumphostSG creates (or finds) the jumphost security group.
// Ingress: 22/tcp from EICE SG only (added separately after EICE is created).
// Returns the SG ID.
func ensureJumphostSG(ctx context.Context, ec2c EC2API, clusterName, vpcID string,
	extraTags, labels map[string]string, st *state.State) (string, error) {

	// Check state first.
	if sgID := st.Get("JUMPHOST_SG_ID"); sgID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17b] jumphost SG found in state: %s\n", sgID)
		return sgID, nil
	}

	// Tag-discovery fallback.
	if sgID := lookupSGByTag(ctx, ec2c, clusterName, tags.CompJumphostSG); sgID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17b] jumphost SG found via tags: %s\n", sgID)
		return sgID, nil
	}

	sgName := clusterName + "-jumphost"
	sgTags := tags.Merge(
		tags.Required(clusterName, tags.CompJumphostSG),
		map[string]string{tags.KeyName: sgName},
		extraTags,
		labels,
	)

	out, err := ec2c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   ptr(sgName),
		Description: ptr("Jumphost SG - ingress 22/tcp from EICE SG only"),
		VpcId:       ptr(vpcID),
		TagSpecifications: []ec2types.TagSpecification{
			tagSpecification(ec2types.ResourceTypeSecurityGroup, sgTags),
		},
	})
	if err != nil {
		return "", fmt.Errorf("ec2:CreateSecurityGroup jumphost: %w", err)
	}
	sgID := *out.GroupId
	fmt.Fprintf(os.Stderr, "[phase 17b] created jumphost SG %s\n", sgID)
	return sgID, nil
}

// ensureJumphostSGIngress adds ingress 22/tcp from EICE SG to jumphost SG.
// Idempotent: tolerates DuplicatePermission.
func ensureJumphostSGIngress(ctx context.Context, ec2c EC2API, jumphostSGID, eiceSGID string) error {
	proto := "tcp"
	port := int32(22)
	_, err := ec2c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId: ptr(jumphostSGID),
		IpPermissions: []ec2types.IpPermission{
			{
				IpProtocol: &proto,
				FromPort:   &port,
				ToPort:     &port,
				UserIdGroupPairs: []ec2types.UserIdGroupPair{
					{
						GroupId:     ptr(eiceSGID),
						Description: ptr("allow-ssh-from-eice"),
					},
				},
			},
		},
	})
	if err != nil && !isEC2DuplicatePermission(err) {
		return fmt.Errorf("ec2:AuthorizeSecurityGroupIngress jumphost←eice: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17b] jumphost SG %s: ingress 22/tcp from EICE SG %s set\n", jumphostSGID, eiceSGID)
	return nil
}

// ensureJumphostEICE creates (or finds) the EC2 Instance Connect Endpoint.
// Handles ResourceLimitExceeded (1/subnet quota) by falling back to describe.
// Returns (eiceID, eiceSGID, error). eiceSGID may be "" if not returned by AWS.
func ensureJumphostEICE(ctx context.Context, ec2c EC2API, clusterName, subnetID, vpcID string,
	extraTags, labels map[string]string, st *state.State) (string, string, error) {

	// Check state first.
	if eiceID := st.Get("JUMPHOST_EICE_ID"); eiceID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17b] EICE found in state: %s\n", eiceID)
		return eiceID, st.Get("JUMPHOST_EICE_SG_ID"), nil
	}

	// Tag-discovery fallback.
	if eiceID := lookupEICEByTag(ctx, ec2c, clusterName, tags.CompJumphostEICE); eiceID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17b] EICE found via tags: %s\n", eiceID)
		return eiceID, "", nil
	}

	eiceTags := tags.Merge(
		tags.Required(clusterName, tags.CompJumphostEICE),
		map[string]string{tags.KeyName: clusterName + "-jumphost-eice"},
		extraTags,
		labels,
	)

	out, err := ec2c.CreateInstanceConnectEndpoint(ctx, &ec2.CreateInstanceConnectEndpointInput{
		SubnetId: ptr(subnetID),
		TagSpecifications: []ec2types.TagSpecification{
			tagSpecification(ec2types.ResourceTypeInstanceConnectEndpoint, eiceTags),
		},
	})
	if err != nil {
		// ResourceLimitExceeded = 1 EICE per subnet. Fall back to describe.
		if isResourceLimitExceeded(err) {
			fmt.Fprintln(os.Stderr, "[phase 17b] EICE quota hit — looking up existing EICE by subnet")
			eiceID, eiceSGID := lookupEICEBySubnet(ctx, ec2c, subnetID)
			if eiceID == "" {
				return "", "", fmt.Errorf("ec2:CreateInstanceConnectEndpoint quota hit but no existing EICE found in subnet %s", subnetID)
			}
			return eiceID, eiceSGID, nil
		}
		return "", "", fmt.Errorf("ec2:CreateInstanceConnectEndpoint: %w", err)
	}

	eiceID := ""
	eiceSGID := ""
	if out.InstanceConnectEndpoint != nil {
		if out.InstanceConnectEndpoint.InstanceConnectEndpointId != nil {
			eiceID = *out.InstanceConnectEndpoint.InstanceConnectEndpointId
		}
		// EICE has its own SG that AWS auto-creates; capture it for audit.
		for _, sg := range out.InstanceConnectEndpoint.SecurityGroupIds {
			eiceSGID = sg
			break
		}
	}
	if eiceID == "" {
		return "", "", fmt.Errorf("ec2:CreateInstanceConnectEndpoint returned nil endpoint")
	}

	// Wait for EICE to reach "create-complete" state (only if not already there).
	if out.InstanceConnectEndpoint == nil || out.InstanceConnectEndpoint.State != ec2types.Ec2InstanceConnectEndpointStateCreateComplete {
		if err := waitEICEReady(ctx, ec2c, eiceID); err != nil {
			return "", "", fmt.Errorf("waiting for EICE %s ready: %w", eiceID, err)
		}
	}

	fmt.Fprintf(os.Stderr, "[phase 17b] created EICE %s (SG=%s)\n", eiceID, eiceSGID)
	return eiceID, eiceSGID, nil
}

// ensureJumphostInstance launches (or finds) the jumphost EC2 instance.
// Returns (instanceID, mgmtENIID, mgmtENIIP, error).
func ensureJumphostInstance(ctx context.Context, ec2c EC2API, clusterName, subnetID, sgID,
	amiID, instanceType, profileName string, extraTags, labels map[string]string, st *state.State) (string, string, string, error) {

	// Check state first.
	if instanceID := st.Get("JUMPHOST_INSTANCE_ID"); instanceID != "" {
		// Verify it's still running.
		descOut, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err == nil && len(descOut.Reservations) > 0 && len(descOut.Reservations[0].Instances) > 0 {
			inst := descOut.Reservations[0].Instances[0]
			if inst.State != nil && inst.State.Name == ec2types.InstanceStateNameRunning {
				fmt.Fprintf(os.Stderr, "[phase 17b] instance %s already running — skipping launch\n", instanceID)
				mgmtENIID := st.Get("JUMPHOST_MGMT_ENI_ID")
				mgmtENIIP := st.Get("JUMPHOST_MGMT_ENI_IP")
				return instanceID, mgmtENIID, mgmtENIIP, nil
			}
		}
	}

	// Tag-discovery fallback: look for a running (non-terminated) instance.
	if instanceID := lookupInstanceByTag(ctx, ec2c, clusterName, tags.CompJumphostInstance); instanceID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17b] instance found via tags: %s — skipping launch\n", instanceID)
		return instanceID, "", "", nil
	}

	userData := jumphostUserData()
	userDataB64 := base64.StdEncoding.EncodeToString([]byte(userData))

	instTags := tags.Merge(
		tags.Required(clusterName, tags.CompJumphostInstance),
		map[string]string{tags.KeyName: clusterName + "-jumphost"},
		extraTags,
		labels,
	)

	runOut, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:          ptr(amiID),
		InstanceType:     ec2types.InstanceType(instanceType),
		MinCount:         int32Ptr(1),
		MaxCount:         int32Ptr(1),
		SubnetId:         ptr(subnetID),
		SecurityGroupIds: []string{sgID},
		UserData:         ptr(userDataB64),
		IamInstanceProfile: &ec2types.IamInstanceProfileSpecification{
			Name: ptr(profileName),
		},
		TagSpecifications: []ec2types.TagSpecification{
			tagSpecification(ec2types.ResourceTypeInstance, instTags),
		},
	})
	if err != nil {
		return "", "", "", fmt.Errorf("ec2:RunInstances: %w", err)
	}
	if len(runOut.Instances) == 0 {
		return "", "", "", fmt.Errorf("ec2:RunInstances returned no instances")
	}
	inst := runOut.Instances[0]
	instanceID := ""
	if inst.InstanceId != nil {
		instanceID = *inst.InstanceId
	}
	fmt.Fprintf(os.Stderr, "[phase 17b] launched instance %s (ami=%s type=%s)\n", instanceID, amiID, instanceType)

	// Wait for running.
	if err := waitInstanceRunning(ctx, ec2c, instanceID); err != nil {
		return "", "", "", fmt.Errorf("waiting for instance %s running: %w", instanceID, err)
	}

	// Re-describe to get the primary ENI details.
	descOut, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		InstanceIds: []string{instanceID},
	})
	if err != nil || len(descOut.Reservations) == 0 || len(descOut.Reservations[0].Instances) == 0 {
		return instanceID, "", "", nil
	}
	latest := descOut.Reservations[0].Instances[0]
	mgmtENIID := ""
	mgmtENIIP := ""
	if latest.NetworkInterfaces != nil {
		for _, ni := range latest.NetworkInterfaces {
			if ni.Attachment != nil && ni.Attachment.DeviceIndex != nil && *ni.Attachment.DeviceIndex == 0 {
				if ni.NetworkInterfaceId != nil {
					mgmtENIID = *ni.NetworkInterfaceId
				}
				if ni.PrivateIpAddress != nil {
					mgmtENIIP = *ni.PrivateIpAddress
				}
				break
			}
		}
	}

	return instanceID, mgmtENIID, mgmtENIIP, nil
}

// ensureJumphostSecondaryENI creates (or finds) the BNK_EXT secondary ENI and
// attaches it to the jumphost instance at device-index=1.
// Returns (eniID, eniPrivateIP, error).
func ensureJumphostSecondaryENI(ctx context.Context, ec2c EC2API, clusterName, subnetID, sgID,
	instanceID string, extraTags, labels map[string]string, st *state.State) (string, string, error) {

	// Check state first.
	if eniID := st.Get("JUMPHOST_BNK_EXT_ENI_ID"); eniID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17b] BNK_EXT ENI found in state: %s\n", eniID)
		return eniID, st.Get("JUMPHOST_BNK_EXT_ENI_IP"), nil
	}

	// Tag-discovery fallback.
	if eniID := lookupENIByTag(ctx, ec2c, clusterName, tags.CompJumphostENIExt); eniID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17b] BNK_EXT ENI found via tags: %s\n", eniID)
		return eniID, "", nil
	}

	eniName := clusterName + "-jumphost-bnk-ext"
	eniTags := tags.Merge(
		tags.Required(clusterName, tags.CompJumphostENIExt),
		map[string]string{tags.KeyName: eniName},
		extraTags,
		labels,
	)

	out, err := ec2c.CreateNetworkInterface(ctx, &ec2.CreateNetworkInterfaceInput{
		SubnetId:    ptr(subnetID),
		Groups:      []string{sgID},
		Description: ptr(eniName),
		TagSpecifications: []ec2types.TagSpecification{
			tagSpecification(ec2types.ResourceTypeNetworkInterface, eniTags),
		},
	})
	if err != nil {
		return "", "", fmt.Errorf("ec2:CreateNetworkInterface jumphost BNK_EXT: %w", err)
	}
	eniID := *out.NetworkInterface.NetworkInterfaceId
	eniIP := ""
	if out.NetworkInterface.PrivateIpAddress != nil {
		eniIP = *out.NetworkInterface.PrivateIpAddress
	}

	// Disable source/dest check so the jumphost can route traffic.
	if _, err := ec2c.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: ptr(eniID),
		SourceDestCheck:    &ec2types.AttributeBooleanValue{Value: boolPtr(false)},
	}); err != nil {
		return "", "", fmt.Errorf("ec2:ModifyNetworkInterfaceAttribute --no-source-dest-check %s: %w", eniID, err)
	}

	// Attach at device-index=1.
	if err := attachENIIfNeeded(ctx, ec2c, eniID, instanceID, 1); err != nil {
		return "", "", fmt.Errorf("attach BNK_EXT ENI %s to instance %s: %w", eniID, instanceID, err)
	}

	fmt.Fprintf(os.Stderr, "[phase 17b] created+attached BNK_EXT ENI %s (ip=%s)\n", eniID, eniIP)
	return eniID, eniIP, nil
}

// jumphostUserData returns the cloud-init shell script that activates the
// secondary NIC at boot. Adapts the ENA bring-up pattern from phase10.
func jumphostUserData() string {
	return `#!/bin/bash
set -euo pipefail
# Bring up secondary NIC (device-index=1, typically ens6 on AL2023/ENA).
# Adapted from aws-gpu-setup ENA bring-up pattern (phase10).
for attempt in $(seq 1 10); do
  # Find the secondary interface (not lo, not eth0/ens5 primary).
  for ifname in $(ls /sys/class/net/); do
    case "$ifname" in lo|eth0|ens5) continue ;; esac
    ip link set "$ifname" up 2>/dev/null || true
    ip link set "$ifname" mtu 1500 2>/dev/null || true
    dhclient "$ifname" 2>/dev/null && break 2 || true
  done
  sleep 2
done
`
}

// waitInstanceRunning polls until the instance is in "running" state.
func waitInstanceRunning(ctx context.Context, ec2c EC2API, instanceID string) error {
	deadline := time.Now().Add(jumphostInstanceRunningTimeout)
	for time.Now().Before(deadline) {
		out, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err == nil && len(out.Reservations) > 0 && len(out.Reservations[0].Instances) > 0 {
			state := out.Reservations[0].Instances[0].State
			if state != nil && state.Name == ec2types.InstanceStateNameRunning {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jumphostInstancePollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for instance %s to reach running state", instanceID)
}

// waitInstanceTerminated polls until the instance is in "terminated" state.
func waitInstanceTerminated(ctx context.Context, ec2c EC2API, instanceID string) error {
	deadline := time.Now().Add(jumphostInstanceTerminatedTimeout)
	for time.Now().Before(deadline) {
		out, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err != nil {
			if ignoreNotFound(err) == nil {
				return nil // already gone
			}
		}
		if err == nil && len(out.Reservations) > 0 && len(out.Reservations[0].Instances) > 0 {
			s := out.Reservations[0].Instances[0].State
			if s != nil && s.Name == ec2types.InstanceStateNameTerminated {
				return nil
			}
		} else if err == nil && len(out.Reservations) == 0 {
			return nil // terminated instances disappear from describe
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(jumphostInstancePollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for instance %s to terminate", instanceID)
}

// waitEICEReady polls until the EICE reaches "create-complete" state.
func waitEICEReady(ctx context.Context, ec2c EC2API, eiceID string) error {
	deadline := time.Now().Add(eiceReadyTimeout)
	for time.Now().Before(deadline) {
		out, err := ec2c.DescribeInstanceConnectEndpoints(ctx, &ec2.DescribeInstanceConnectEndpointsInput{
			InstanceConnectEndpointIds: []string{eiceID},
		})
		if err == nil && len(out.InstanceConnectEndpoints) > 0 {
			s := out.InstanceConnectEndpoints[0].State
			if s == ec2types.Ec2InstanceConnectEndpointStateCreateComplete {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(eicePollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for EICE %s to reach create-complete", eiceID)
}

// lookupInstanceByTag looks up a non-terminated EC2 instance by cluster + component tags.
// Returns "" if not found.
func lookupInstanceByTag(ctx context.Context, ec2c EC2API, clusterName, component string) string {
	out, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
		Filters: []ec2types.Filter{
			tags.ClusterFilter(clusterName),
			tags.ComponentFilter(component),
			{Name: ptr("instance-state-name"), Values: []string{"running", "pending", "stopped", "stopping"}},
		},
	})
	if err != nil || len(out.Reservations) == 0 {
		return ""
	}
	for _, r := range out.Reservations {
		for _, inst := range r.Instances {
			if inst.InstanceId != nil {
				return *inst.InstanceId
			}
		}
	}
	return ""
}

// lookupEICEByTag looks up an EICE by cluster + component tags.
func lookupEICEByTag(ctx context.Context, ec2c EC2API, clusterName, component string) string {
	out, err := ec2c.DescribeInstanceConnectEndpoints(ctx, &ec2.DescribeInstanceConnectEndpointsInput{
		Filters: []ec2types.Filter{
			tags.ClusterFilter(clusterName),
			tags.ComponentFilter(component),
		},
	})
	if err != nil || len(out.InstanceConnectEndpoints) == 0 {
		return ""
	}
	if out.InstanceConnectEndpoints[0].InstanceConnectEndpointId == nil {
		return ""
	}
	return *out.InstanceConnectEndpoints[0].InstanceConnectEndpointId
}

// lookupEICEBySubnet finds the first EICE in a given subnet (for quota-hit recovery).
func lookupEICEBySubnet(ctx context.Context, ec2c EC2API, subnetID string) (string, string) {
	out, err := ec2c.DescribeInstanceConnectEndpoints(ctx, &ec2.DescribeInstanceConnectEndpointsInput{
		Filters: []ec2types.Filter{
			{Name: ptr("subnet-id"), Values: []string{subnetID}},
		},
	})
	if err != nil || len(out.InstanceConnectEndpoints) == 0 {
		return "", ""
	}
	ep := out.InstanceConnectEndpoints[0]
	eiceID := ""
	if ep.InstanceConnectEndpointId != nil {
		eiceID = *ep.InstanceConnectEndpointId
	}
	eiceSGID := ""
	for _, sg := range ep.SecurityGroupIds {
		eiceSGID = sg
		break
	}
	return eiceID, eiceSGID
}

// lookupSGByTag looks up a security group by cluster + component tags.
func lookupSGByTag(ctx context.Context, ec2c EC2API, clusterName, component string) string {
	out, err := ec2c.DescribeSecurityGroups(ctx, &ec2.DescribeSecurityGroupsInput{
		Filters: []ec2types.Filter{
			tags.ClusterFilter(clusterName),
			tags.ComponentFilter(component),
		},
	})
	if err != nil || len(out.SecurityGroups) == 0 {
		return ""
	}
	if out.SecurityGroups[0].GroupId == nil {
		return ""
	}
	return *out.SecurityGroups[0].GroupId
}

// deleteJumphostIAM removes the instance profile (role detached first) and the role.
// Tolerates NoSuchEntity.
func deleteJumphostIAM(ctx context.Context, iamClient IAMAPI, profileName, roleName string) error {
	// Remove role from profile.
	if _, err := iamClient.RemoveRoleFromInstanceProfile(ctx, &iam.RemoveRoleFromInstanceProfileInput{
		InstanceProfileName: ptr(profileName),
		RoleName:            ptr(roleName),
	}); err != nil && !isNoSuchEntity(err) {
		return fmt.Errorf("iam:RemoveRoleFromInstanceProfile %s→%s: %w", roleName, profileName, err)
	}

	// Delete instance profile.
	if _, err := iamClient.DeleteInstanceProfile(ctx, &iam.DeleteInstanceProfileInput{
		InstanceProfileName: ptr(profileName),
	}); err != nil && !isNoSuchEntity(err) {
		return fmt.Errorf("iam:DeleteInstanceProfile %s: %w", profileName, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17b down] deleted instance profile %s\n", profileName)

	// Detach managed policy then delete role.
	if _, err := iamClient.DetachRolePolicy(ctx, &iam.DetachRolePolicyInput{
		RoleName:  ptr(roleName),
		PolicyArn: ptr(jumphostSSMPolicyARN),
	}); err != nil && !isNoSuchEntity(err) {
		return fmt.Errorf("iam:DetachRolePolicy %s: %w", roleName, err)
	}
	if _, err := iamClient.DeleteRole(ctx, &iam.DeleteRoleInput{
		RoleName: ptr(roleName),
	}); err != nil && !isNoSuchEntity(err) {
		return fmt.Errorf("iam:DeleteRole %s: %w", roleName, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17b down] deleted IAM role %s\n", roleName)
	return nil
}

// clearJumphostState removes all JUMPHOST_* keys from state.
func clearJumphostState(st *state.State) {
	for _, key := range []string{
		"JUMPHOST_INSTANCE_ID",
		"JUMPHOST_MGMT_ENI_ID",
		"JUMPHOST_MGMT_ENI_IP",
		"JUMPHOST_BNK_EXT_ENI_ID",
		"JUMPHOST_BNK_EXT_ENI_IP",
		"JUMPHOST_EICE_ID",
		"JUMPHOST_EICE_SG_ID",
		"JUMPHOST_SG_ID",
		"JUMPHOST_AMI_ID",
		"JUMPHOST_INSTANCE_TYPE",
		"JUMPHOST_INSTANCE_PROFILE_NAME",
		"JUMPHOST_ROLE_NAME",
	} {
		st.Set(key, "")
	}
}

// isResourceLimitExceeded returns true for EC2 ResourceLimitExceeded errors
// (e.g. 1 EICE per subnet quota).
func isResourceLimitExceeded(err error) bool {
	if err == nil {
		return false
	}
	type coder interface{ ErrorCode() string }
	e := err
	for e != nil {
		if ce, ok := e.(coder); ok {
			return ce.ErrorCode() == "ResourceLimitExceeded"
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

// isDuplicatePolicy returns true for IAM DuplicatePolicyAttachmentException.
func isDuplicatePolicy(err error) bool {
	if err == nil {
		return false
	}
	type coder interface{ ErrorCode() string }
	e := err
	for e != nil {
		if ce, ok := e.(coder); ok {
			return ce.ErrorCode() == "DuplicatePolicyAttachmentException"
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
