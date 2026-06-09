package phases

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/tags"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

const (
	// bigipVEInstancePollInterval is the interval between EC2 state polls.
	bigipVEInstancePollInterval = 10 * time.Second
	// bigipVEInstanceRunningTimeout is the maximum time to wait for running state.
	bigipVEInstanceRunningTimeout = 10 * time.Minute
	// bigipVEInstanceTerminatedTimeout is the maximum time to wait for terminated state.
	bigipVEInstanceTerminatedTimeout = 15 * time.Minute

	// bigipVEKeyName is the EC2 key pair name created for BIG-IP SSH access.
	// The corresponding PEM private key is written to <StateDir>/bigip-ssh.pem
	// (mode 0600). It is used by the onboarding slice (F2-B2) to SSH from the
	// jumphost into the BIG-IP management interface.
	//
	// IMPORTANT: The BIG-IP admin PASSWORD is NOT handled here — it is read from
	// AWSBNKCTL_BIGIP_PASSWORD at F2-B2 onboarding time. Never store credentials
	// in cluster.yaml or state.env.
	bigipVEKeyName = "bnk-demo-bigip"

	// bigipVEPEMFile is the filename (relative to StateDir) for the SSH private key.
	bigipVEPEMFile = "bigip-ssh.pem"

	// bigipVEOptInRequiredCode is the EC2 API error code returned when the
	// operator has not subscribed to the BIG-IP VE PAYG AMI in AWS Marketplace.
	bigipVEOptInRequiredCode = "OptInRequired"

	// bigipVEAMIOwner is the AWS Marketplace owner ID for official F5 BIG-IP VE
	// PAYG AMIs. All dynamically resolved AMIs are filtered to this owner.
	bigipVEAMIOwner = "aws-marketplace"
)

// Phase17eBigIPVE provisions a 3-NIC F5 BIG-IP VE EC2 instance when
// bigipVE.enabled is true in cluster.yaml. This phase handles LAUNCH ONLY —
// BIG-IP onboarding (setting the admin password, provisioning modules, AS3)
// is deferred to F2-B2. The phase is a no-op when !cl.BigIPVEEnabled().
//
// Provisioned resources:
//  1. EC2 key pair "bnk-demo-bigip" (PEM saved to <StateDir>/bigip-ssh.pem).
//  2. BIG-IP mgmt SG (ingress 443+22 from jumphost SG; 443 from EKS node SG).
//  3. eth0 mgmt ENI  — public subnet [MgmtSubnetIndex], IP .50, mgmt SG.
//  4. eth1 external ENI — BNK_EXT_SUBNET, IP .50 + VIP as secondary, data SG, src/dst-check off.
//  5. eth2 internal ENI — BNK_INT_SUBNET, IP .50, data SG, src/dst-check off.
//  6. c5n.2xlarge PAYG BIG-IP VE instance with all 3 ENIs pre-attached.
//
// AMI resolution: DescribeImages (NOT SSM), owner aws-marketplace,
// Name filter "*BIGIP-{ver}*PAYG-{tier} 25Mbps*". Newest by CreationDate wins.
// If RunInstances returns OptInRequired, a helpful subscription URL error is returned.
//
// Dry-run: placeholder state keys, zero AWS calls.
// SSO sentinel: CheckAuthOrDie at entry.
func Phase17eBigIPVE(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 17e] bigip-ve: cluster=%s\n", name)

	if !cl.BigIPVEEnabled() {
		fmt.Fprintln(os.Stderr, "[phase 17e] skipped: bigipVE disabled")
		return nil
	}

	ve := cl.BigIPVE
	instanceType := ve.InstanceType
	if instanceType == "" {
		instanceType = "c5n.2xlarge"
	}

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 17e] dry-run: would resolve BIG-IP PAYG AMI (owner=%s tier=%s)\n",
			bigipVEAMIOwner, ve.LicenseTier)
		fmt.Fprintf(os.Stderr, "[phase 17e] dry-run: would create key pair %s → %s/bigip-ssh.pem\n",
			bigipVEKeyName, name)
		fmt.Fprintf(os.Stderr, "[phase 17e] dry-run: would create BIG-IP mgmt SG (443+22 jumphost; 443 EKS node SG)\n")
		fmt.Fprintf(os.Stderr, "[phase 17e] dry-run: would create 3 ENIs (mgmt/ext/int) and launch %s VE\n", instanceType)
		fmt.Fprintf(os.Stderr, "[phase 17e] dry-run: would write 11 BIGIP_* state keys\n")
		st.Set("BIGIP_INSTANCE_ID", "i-dry-run-bigip")
		st.Set("BIGIP_MGMT_ENI_ID", "eni-dry-run-bigip-mgmt")
		st.Set("BIGIP_MGMT_IP", "10.0.1.50")
		st.Set("BIGIP_EXT_ENI_ID", "eni-dry-run-bigip-ext")
		st.Set("BIGIP_EXT_IP", "10.0.10.50")
		st.Set("BIGIP_VIP", ve.VIP)
		st.Set("BIGIP_INT_ENI_ID", "eni-dry-run-bigip-int")
		st.Set("BIGIP_INT_IP", "10.0.20.50")
		st.Set("BIGIP_MGMT_SG_ID", "sg-dry-run-bigip-mgmt")
		st.Set("BIGIP_KEY_NAME", bigipVEKeyName)
		st.Set("BIGIP_AMI_ID", "ami-dry-run-bigip")
		st.Set("BIGIP_SSH_KEY_PATH", ".awsbnkctl/"+name+"/"+bigipVEPEMFile)
		return nil
	}

	// Prerequisite state checks.
	vpcID := st.Get("VPC_ID")
	if vpcID == "" {
		return fmt.Errorf("phase17e: VPC_ID not in state (run phase02 first)")
	}
	sgBNKData := st.Get("SG_BNK_DATA")
	if sgBNKData == "" {
		return fmt.Errorf("phase17e: SG_BNK_DATA not in state (run phase07 first)")
	}
	bnkExtSubnet := st.Get("BNK_EXT_SUBNET")
	if bnkExtSubnet == "" {
		return fmt.Errorf("phase17e: BNK_EXT_SUBNET not in state (run phase03 first)")
	}
	bnkIntSubnet := st.Get("BNK_INT_SUBNET")
	if bnkIntSubnet == "" {
		return fmt.Errorf("phase17e: BNK_INT_SUBNET not in state (run phase03 first)")
	}
	jumphostSGID := st.Get("JUMPHOST_SG_ID")
	if jumphostSGID == "" {
		return fmt.Errorf("phase17e: JUMPHOST_SG_ID not in state (run phase17b first)")
	}
	eksSGID := st.Get("EKS_SECURITY_GROUP")
	if eksSGID == "" {
		return fmt.Errorf("phase17e: EKS_SECURITY_GROUP not in state (run phase08/eks cluster creation first)")
	}

	// Resolve public subnet by MgmtSubnetIndex.
	if len(cl.Network.Subnets.Public) == 0 {
		return fmt.Errorf("phase17e: no public subnets defined in cluster.yaml")
	}
	idx := ve.MgmtSubnetIndex
	if idx >= len(cl.Network.Subnets.Public) {
		idx = 0
	}
	publicCSV := st.Get("PUBLIC_SUBNETS")
	if publicCSV == "" {
		return fmt.Errorf("phase17e: PUBLIC_SUBNETS not in state (run phase03 first)")
	}
	publicIDs := strings.Split(publicCSV, ",")
	if idx >= len(publicIDs) {
		return fmt.Errorf("phase17e: mgmtSubnetIndex=%d but PUBLIC_SUBNETS has only %d entries", idx, len(publicIDs))
	}
	mgmtSubnetID := strings.TrimSpace(publicIDs[idx])
	if mgmtSubnetID == "" {
		return fmt.Errorf("phase17e: PUBLIC_SUBNETS[%d] is empty (corrupted state.env?)", idx)
	}

	// Step 1: EC2 key pair — create or reuse.
	// ensureBigIPKeyPair writes the PEM on fresh creation and is a no-op on reuse.
	if _, err := ensureBigIPKeyPair(ctx, clients.EC2, bigipVEKeyName, st); err != nil {
		return fmt.Errorf("phase17e: key pair: %w", err)
	}
	st.Set("BIGIP_KEY_NAME", bigipVEKeyName)
	pemFilePath := st.Dir() + "/" + bigipVEPEMFile
	st.Set("BIGIP_SSH_KEY_PATH", pemFilePath)

	// Step 2: AMI resolution.
	amiID := st.Get("BIGIP_AMI_ID")
	if amiID == "" {
		var amiErr error
		amiID, amiErr = resolveBigIPAMI(ctx, clients.EC2, ve.LicenseTier, ve.Version)
		if amiErr != nil {
			return fmt.Errorf("phase17e: AMI resolution: %w", amiErr)
		}
	}
	st.Set("BIGIP_AMI_ID", amiID)
	fmt.Fprintf(os.Stderr, "[phase 17e] BIG-IP AMI: %s\n", amiID)

	// Step 3: BIG-IP mgmt security group.
	mgmtSGID, err := ensureBigIPMgmtSG(ctx, clients.EC2, name, vpcID, jumphostSGID,
		eksSGID, cl.Tags, cl.Metadata.Labels, st)
	if err != nil {
		return fmt.Errorf("phase17e: mgmt SG: %w", err)
	}
	st.Set("BIGIP_MGMT_SG_ID", mgmtSGID)

	// Derive .50 IPs for each subnet.
	mgmtSubnetCIDR := cl.Network.Subnets.Public[idx].CIDR
	extSubnetCIDR := cl.Network.DataPath.External.CIDR
	intSubnetCIDR := cl.Network.DataPath.Internal.CIDR

	mgmtPrimaryIP, err := cidrDotN(mgmtSubnetCIDR, 50)
	if err != nil {
		return fmt.Errorf("phase17e: derive mgmt IP: %w", err)
	}
	extPrimaryIP, err := cidrDotN(extSubnetCIDR, 50)
	if err != nil {
		return fmt.Errorf("phase17e: derive ext IP: %w", err)
	}
	intPrimaryIP, err := cidrDotN(intSubnetCIDR, 50)
	if err != nil {
		return fmt.Errorf("phase17e: derive int IP: %w", err)
	}

	// Step 4: eth0 mgmt ENI.
	mgmtENIID, err := ensureBigIPENI(ctx, clients.EC2, name, mgmtSubnetID, mgmtSGID, mgmtPrimaryIP,
		"", false, tags.CompBigIPMgmtENI, "bigip-mgmt", cl.Tags, cl.Metadata.Labels, st, "BIGIP_MGMT_ENI_ID")
	if err != nil {
		return fmt.Errorf("phase17e: mgmt ENI: %w", err)
	}
	st.Set("BIGIP_MGMT_ENI_ID", mgmtENIID)
	st.Set("BIGIP_MGMT_IP", mgmtPrimaryIP)

	// Step 5: eth1 external ENI (with VIP as secondary IP, src/dst-check off).
	extENIID, err := ensureBigIPENI(ctx, clients.EC2, name, bnkExtSubnet, sgBNKData, extPrimaryIP,
		ve.VIP, true, tags.CompBigIPExtENI, "bigip-ext", cl.Tags, cl.Metadata.Labels, st, "BIGIP_EXT_ENI_ID")
	if err != nil {
		return fmt.Errorf("phase17e: external ENI: %w", err)
	}
	st.Set("BIGIP_EXT_ENI_ID", extENIID)
	st.Set("BIGIP_EXT_IP", extPrimaryIP)
	st.Set("BIGIP_VIP", ve.VIP)

	// Step 6: eth2 internal ENI (src/dst-check off).
	intENIID, err := ensureBigIPENI(ctx, clients.EC2, name, bnkIntSubnet, sgBNKData, intPrimaryIP,
		"", true, tags.CompBigIPIntENI, "bigip-int", cl.Tags, cl.Metadata.Labels, st, "BIGIP_INT_ENI_ID")
	if err != nil {
		return fmt.Errorf("phase17e: internal ENI: %w", err)
	}
	st.Set("BIGIP_INT_ENI_ID", intENIID)
	st.Set("BIGIP_INT_IP", intPrimaryIP)

	// Step 7: Launch BIG-IP VE with all 3 ENIs pre-attached.
	instanceID, err := ensureBigIPInstance(ctx, clients.EC2, name, amiID, instanceType,
		bigipVEKeyName, mgmtENIID, extENIID, intENIID, cl.Tags, cl.Metadata.Labels, st)
	if err != nil {
		// Surface a helpful error if the operator hasn't subscribed to the AMI.
		if isBigIPOptInRequired(err) {
			return fmt.Errorf("phase17e: BIG-IP VE AMI requires an AWS Marketplace subscription: "+
				"visit https://aws.amazon.com/marketplace/search/results?searchTerms=F5+BIG-IP+VE+PAYG "+
				"and subscribe before re-running (original error: %w)", err)
		}
		return fmt.Errorf("phase17e: launch instance: %w", err)
	}
	st.Set("BIGIP_INSTANCE_ID", instanceID)

	fmt.Fprintf(os.Stderr, "[phase 17e] BIG-IP VE ready: instance=%s ami=%s mgmt-ip=%s ext-ip=%s vip=%s int-ip=%s\n",
		instanceID, amiID, mgmtPrimaryIP, extPrimaryIP, ve.VIP, intPrimaryIP)
	fmt.Fprintf(os.Stderr, "[phase 17e] SSH key: %s (use from jumphost over EICE — no public IP on VE)\n", pemFilePath)

	return st.Save()
}

// Phase17eBigIPVEDown tears down all BIG-IP VE resources in reverse order:
// terminate instance → wait terminated → delete 3 ENIs → delete mgmt SG
// → delete key pair → clear state.
//
// Tolerates NotFound / missing resources (idempotent down). Falls back to
// tag-discovery when state keys are absent.
func Phase17eBigIPVEDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 17e down] bigip-ve: cluster=%s\n", name)

	// Step 1: terminate instance.
	instanceID := st.Get("BIGIP_INSTANCE_ID")
	if instanceID == "" {
		instanceID = lookupInstanceByTag(ctx, clients.EC2, name, tags.CompBigIPInstance)
	}
	if instanceID != "" && instanceID != "i-dry-run-bigip" {
		fmt.Fprintf(os.Stderr, "[phase 17e down] terminating BIG-IP instance %s\n", instanceID)
		if _, err := clients.EC2.TerminateInstances(ctx, &ec2.TerminateInstancesInput{
			InstanceIds: []string{instanceID},
		}); err != nil {
			if e := ignoreNotFound(err); e != nil {
				return fmt.Errorf("phase17e down: TerminateInstances %s: %w", instanceID, err)
			}
		}
		if err := waitBigIPInstanceTerminated(ctx, clients.EC2, instanceID); err != nil {
			return fmt.Errorf("phase17e down: waiting for BIG-IP instance %s terminated: %w", instanceID, err)
		}
		fmt.Fprintf(os.Stderr, "[phase 17e down] BIG-IP instance %s terminated\n", instanceID)
	}

	// Step 2: delete the 3 ENIs (they survive instance termination).
	for _, stateKey := range []struct {
		key       string
		component string
		label     string
	}{
		{"BIGIP_MGMT_ENI_ID", tags.CompBigIPMgmtENI, "mgmt"},
		{"BIGIP_EXT_ENI_ID", tags.CompBigIPExtENI, "ext"},
		{"BIGIP_INT_ENI_ID", tags.CompBigIPIntENI, "int"},
	} {
		eniID := st.Get(stateKey.key)
		if eniID == "" {
			eniID = lookupENIByTag(ctx, clients.EC2, name, stateKey.component)
		}
		if eniID != "" && !strings.HasPrefix(eniID, "eni-dry-run") {
			fmt.Fprintf(os.Stderr, "[phase 17e down] deleting BIG-IP %s ENI %s\n", stateKey.label, eniID)
			if err := detachAndDeleteENI(ctx, clients.EC2, eniID); err != nil {
				return fmt.Errorf("phase17e down: %s ENI: %w", stateKey.label, err)
			}
		}
	}

	// Step 3: delete mgmt SG.
	mgmtSGID := st.Get("BIGIP_MGMT_SG_ID")
	if mgmtSGID == "" {
		mgmtSGID = lookupSGByTag(ctx, clients.EC2, name, tags.CompBigIPMgmtSG)
	}
	if mgmtSGID != "" && mgmtSGID != "sg-dry-run-bigip-mgmt" {
		fmt.Fprintf(os.Stderr, "[phase 17e down] deleting BIG-IP mgmt SG %s\n", mgmtSGID)
		if _, err := clients.EC2.DeleteSecurityGroup(ctx, &ec2.DeleteSecurityGroupInput{
			GroupId: ptr(mgmtSGID),
		}); err != nil {
			if e := ignoreNotFound(err); e != nil {
				return fmt.Errorf("phase17e down: DeleteSecurityGroup %s: %w", mgmtSGID, err)
			}
		}
	}

	// Step 4: delete key pair.
	keyName := st.Get("BIGIP_KEY_NAME")
	if keyName == "" {
		keyName = bigipVEKeyName
	}
	fmt.Fprintf(os.Stderr, "[phase 17e down] deleting key pair %s\n", keyName)
	if _, err := clients.EC2.DeleteKeyPair(ctx, &ec2.DeleteKeyPairInput{
		KeyName: ptr(keyName),
	}); err != nil {
		if e := ignoreNotFound(err); e != nil {
			return fmt.Errorf("phase17e down: DeleteKeyPair %s: %w", keyName, err)
		}
	}

	clearBigIPVEState(st)
	return st.Save()
}

// ensureBigIPKeyPair creates the EC2 key pair, saves the PEM to StateDir, and
// returns the path. If the key pair already exists (DescribeKeyPairs finds it),
// it is reused and "" is returned as the PEM path (existing PEM is preserved on
// disk). This is idempotent: a second run with the PEM already written is a no-op.
func ensureBigIPKeyPair(ctx context.Context, ec2c EC2API, keyName string, st *state.State) (string, error) {
	pemFilePath := st.Dir() + "/" + bigipVEPEMFile

	// Check if key pair already exists in AWS.
	descOut, err := ec2c.DescribeKeyPairs(ctx, &ec2.DescribeKeyPairsInput{
		KeyNames: []string{keyName},
	})
	if err == nil && len(descOut.KeyPairs) > 0 {
		fmt.Fprintf(os.Stderr, "[phase 17e] key pair %s already exists — reusing\n", keyName)
		// If PEM is already on disk, nothing to do.
		return "", nil
	}

	// Key pair not found — create it and save the PEM.
	out, err := ec2c.CreateKeyPair(ctx, &ec2.CreateKeyPairInput{
		KeyName: ptr(keyName),
		KeyType: ec2types.KeyTypeRsa,
		TagSpecifications: []ec2types.TagSpecification{
			tagSpecification(ec2types.ResourceTypeKeyPair, tags.Merge(
				map[string]string{
					"awsbnkctl:managed": "true",
					"Name":              keyName,
				},
			)),
		},
	})
	if err != nil {
		return "", fmt.Errorf("ec2:CreateKeyPair %s: %w", keyName, err)
	}

	if out.KeyMaterial == nil || *out.KeyMaterial == "" {
		return "", fmt.Errorf("CreateKeyPair %s returned empty key material", keyName)
	}

	// Write PEM with mode 0600 — this is a local secret file.
	// The admin PASSWORD is NOT handled here (F2-B2 via AWSBNKCTL_BIGIP_PASSWORD).
	// #nosec G306 -- 0600 is intentional for a private key
	if err := os.WriteFile(pemFilePath, []byte(*out.KeyMaterial), 0o600); err != nil {
		return "", fmt.Errorf("writing BIG-IP SSH private key to %s: %w", pemFilePath, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17e] created key pair %s → %s\n", keyName, pemFilePath)
	return pemFilePath, nil
}

// resolveBigIPAMI resolves the newest BIG-IP VE PAYG AMI from AWS Marketplace.
// Owner: aws-marketplace, filter Name like *BIGIP-{ver}*PAYG-{tier} 25Mbps*.
// Sorts by CreationDate DESC, returns the newest. Returns an error that wraps
// isBigIPOptInRequired if the operator hasn't subscribed.
func resolveBigIPAMI(ctx context.Context, ec2c EC2API, licenseTier, versionGlob string) (string, error) {
	if versionGlob == "" {
		versionGlob = "17.*"
	}
	// Filter: name must match *BIGIP-<ver>*PAYG-<tier> 25Mbps*
	// e.g. "*BIGIP-17.*PAYG-Good 25Mbps*"
	nameFilter := fmt.Sprintf("*BIGIP-%s*PAYG-%s 25Mbps*", versionGlob, licenseTier)

	out, err := ec2c.DescribeImages(ctx, &ec2.DescribeImagesInput{
		Owners: []string{bigipVEAMIOwner},
		Filters: []ec2types.Filter{
			{Name: ptr("name"), Values: []string{nameFilter}},
			{Name: ptr("state"), Values: []string{"available"}},
		},
	})
	if err != nil {
		return "", fmt.Errorf("ec2:DescribeImages BIGIP PAYG-%s: %w", licenseTier, err)
	}
	if len(out.Images) == 0 {
		return "", fmt.Errorf("no BIG-IP VE PAYG-%s 25Mbps AMI found (filter: %q, owner: %s) — "+
			"is the AWS Marketplace subscription active?", licenseTier, nameFilter, bigipVEAMIOwner)
	}

	// Sort by CreationDate DESC — newest first.
	images := out.Images
	sort.Slice(images, func(i, j int) bool {
		di, dj := "", ""
		if images[i].CreationDate != nil {
			di = *images[i].CreationDate
		}
		if images[j].CreationDate != nil {
			dj = *images[j].CreationDate
		}
		return di > dj // lexicographic DESC works for ISO-8601
	})

	if images[0].ImageId == nil {
		return "", fmt.Errorf("DescribeImages returned image with nil ImageId")
	}
	amiID := *images[0].ImageId
	name := ""
	if images[0].Name != nil {
		name = *images[0].Name
	}
	fmt.Fprintf(os.Stderr, "[phase 17e] resolved BIG-IP AMI: %s (%s)\n", amiID, name)
	return amiID, nil
}

// ensureBigIPMgmtSG creates (or finds) the BIG-IP management security group.
// Ingress rules:
//   - tcp/443 + tcp/22 from jumphostSGID (onboarding + readiness poll from jumphost).
//   - tcp/443 from eksSGID (CIS pods egress on the EKS node SG → iControl REST on 443).
//
// The former "tcp/443 from VPC CIDR" rule is intentionally replaced with the
// narrower EKS node SG source to limit iControl REST exposure. The admin
// login-failure lockout is disabled during onboarding, so the attack surface
// on port 443 must be minimised.
//
// Egress: all traffic allowed (default AWS rule).
func ensureBigIPMgmtSG(ctx context.Context, ec2c EC2API, clusterName, vpcID, jumphostSGID,
	eksSGID string, extraTags, labels map[string]string, st *state.State) (string, error) {

	// Check state first.
	if sgID := st.Get("BIGIP_MGMT_SG_ID"); sgID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17e] BIG-IP mgmt SG found in state: %s\n", sgID)
		return sgID, nil
	}
	// Tag-discovery fallback.
	if sgID := lookupSGByTag(ctx, ec2c, clusterName, tags.CompBigIPMgmtSG); sgID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17e] BIG-IP mgmt SG found via tags: %s\n", sgID)
		return sgID, nil
	}

	sgName := clusterName + "-bigip-mgmt"
	sgTags := tags.Merge(
		tags.Required(clusterName, tags.CompBigIPMgmtSG),
		map[string]string{tags.KeyName: sgName},
		extraTags,
		labels,
	)
	out, err := ec2c.CreateSecurityGroup(ctx, &ec2.CreateSecurityGroupInput{
		GroupName:   ptr(sgName),
		Description: ptr("BIG-IP VE mgmt SG - ingress 443+22 from jumphost SG; 443 from EKS node SG for CIS"),
		VpcId:       ptr(vpcID),
		TagSpecifications: []ec2types.TagSpecification{
			tagSpecification(ec2types.ResourceTypeSecurityGroup, sgTags),
		},
	})
	if err != nil {
		return "", fmt.Errorf("ec2:CreateSecurityGroup bigip-mgmt: %w", err)
	}
	sgID := *out.GroupId
	fmt.Fprintf(os.Stderr, "[phase 17e] created BIG-IP mgmt SG %s\n", sgID)

	// Ingress: tcp/443 from jumphost SG AND EKS node SG (both in one permission).
	// Using two UserIdGroupPairs in a single IpPermission is equivalent to two
	// separate rules — AWS applies them atomically and idempotency (DuplicatePermission)
	// is triggered only when the exact permission already exists.
	//
	// tcp/22 stays jumphost-SG-only; CIS does not need SSH.
	proto := "tcp"
	port22 := int32(22)
	port443 := int32(443)
	ingressRules := []ec2types.IpPermission{
		{
			IpProtocol: &proto,
			FromPort:   &port443,
			ToPort:     &port443,
			UserIdGroupPairs: []ec2types.UserIdGroupPair{
				{GroupId: ptr(jumphostSGID), Description: ptr("allow-443-from-jumphost-for-onboarding")},
				{GroupId: ptr(eksSGID), Description: ptr("allow-443-from-eks-node-sg-for-cis-icontrol")},
			},
		},
		{
			IpProtocol: &proto,
			FromPort:   &port22,
			ToPort:     &port22,
			UserIdGroupPairs: []ec2types.UserIdGroupPair{
				{GroupId: ptr(jumphostSGID), Description: ptr("allow-ssh-from-jumphost-for-onboarding")},
			},
		},
	}

	if _, err := ec2c.AuthorizeSecurityGroupIngress(ctx, &ec2.AuthorizeSecurityGroupIngressInput{
		GroupId:       ptr(sgID),
		IpPermissions: ingressRules,
	}); err != nil && !isEC2DuplicatePermission(err) {
		return "", fmt.Errorf("ec2:AuthorizeSecurityGroupIngress bigip-mgmt: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17e] BIG-IP mgmt SG %s: ingress 443+22 from jumphost SG %s; 443 from EKS node SG %s\n",
		sgID, jumphostSGID, eksSGID)
	return sgID, nil
}

// ensureBigIPENI creates (or finds) a BIG-IP ENI with a specific primary IP.
// When secondaryIP is non-empty, it is assigned as a secondary private IP (the VIP).
// When disableSrcDstCheck is true, source/destination check is disabled (required
// for BIG-IP data-plane ENIs so the VE can route traffic).
// Returns the ENI ID.
func ensureBigIPENI(ctx context.Context, ec2c EC2API, clusterName, subnetID, sgID,
	primaryIP, secondaryIP string, disableSrcDstCheck bool,
	component, nameSuffix string,
	extraTags, labels map[string]string, st *state.State, stateKey string) (string, error) {

	// Check state first.
	if eniID := st.Get(stateKey); eniID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17e] %s ENI found in state: %s\n", component, eniID)
		return eniID, nil
	}
	// Tag-discovery fallback.
	if eniID := lookupENIByTag(ctx, ec2c, clusterName, component); eniID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17e] %s ENI found via tags: %s\n", component, eniID)
		return eniID, nil
	}

	eniName := clusterName + "-" + nameSuffix
	eniTags := tags.Merge(
		tags.Required(clusterName, component),
		map[string]string{tags.KeyName: eniName},
		extraTags,
		labels,
	)

	createIn := &ec2.CreateNetworkInterfaceInput{
		SubnetId:          ptr(subnetID),
		Groups:            []string{sgID},
		Description:       ptr(eniName),
		PrivateIpAddress:  ptr(primaryIP),
		TagSpecifications: []ec2types.TagSpecification{tagSpecification(ec2types.ResourceTypeNetworkInterface, eniTags)},
	}
	if secondaryIP != "" {
		createIn.PrivateIpAddresses = []ec2types.PrivateIpAddressSpecification{
			{PrivateIpAddress: ptr(primaryIP), Primary: boolPtr(true)},
			{PrivateIpAddress: ptr(secondaryIP), Primary: boolPtr(false)},
		}
		// When using PrivateIpAddresses list, PrivateIpAddress must not also be set.
		createIn.PrivateIpAddress = nil
	}

	out, err := ec2c.CreateNetworkInterface(ctx, createIn)
	if err != nil {
		return "", fmt.Errorf("ec2:CreateNetworkInterface %s: %w", eniName, err)
	}
	eniID := *out.NetworkInterface.NetworkInterfaceId

	if disableSrcDstCheck {
		if _, err := ec2c.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
			NetworkInterfaceId: ptr(eniID),
			SourceDestCheck:    &ec2types.AttributeBooleanValue{Value: boolPtr(false)},
		}); err != nil {
			return "", fmt.Errorf("ec2:ModifyNetworkInterfaceAttribute --no-source-dest-check %s: %w", eniID, err)
		}
	}

	fmt.Fprintf(os.Stderr, "[phase 17e] created BIG-IP %s ENI %s (ip=%s)\n", nameSuffix, eniID, primaryIP)
	return eniID, nil
}

// ensureBigIPInstance launches (or finds) the BIG-IP VE EC2 instance with 3
// pre-attached ENIs. The ENIs are passed as NetworkInterfaces — this is the
// BIG-IP-recommended launch pattern (avoids the ENI re-order that can occur
// when attaching post-launch). Returns the instance ID.
func ensureBigIPInstance(ctx context.Context, ec2c EC2API, clusterName, amiID, instanceType,
	keyName, mgmtENIID, extENIID, intENIID string,
	extraTags, labels map[string]string, st *state.State) (string, error) {

	// Check state first.
	if instanceID := st.Get("BIGIP_INSTANCE_ID"); instanceID != "" {
		descOut, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err == nil && len(descOut.Reservations) > 0 && len(descOut.Reservations[0].Instances) > 0 {
			inst := descOut.Reservations[0].Instances[0]
			if inst.State != nil && inst.State.Name == ec2types.InstanceStateNameRunning {
				fmt.Fprintf(os.Stderr, "[phase 17e] BIG-IP instance %s already running — skipping launch\n", instanceID)
				return instanceID, nil
			}
		}
	}

	// Tag-discovery fallback.
	if instanceID := lookupInstanceByTag(ctx, ec2c, clusterName, tags.CompBigIPInstance); instanceID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17e] BIG-IP instance found via tags: %s — skipping launch\n", instanceID)
		return instanceID, nil
	}

	instTags := tags.Merge(
		tags.Required(clusterName, tags.CompBigIPInstance),
		map[string]string{tags.KeyName: clusterName + "-bigip-ve"},
		extraTags,
		labels,
	)

	// DeviceIndex 0 = mgmt, 1 = ext, 2 = int.
	idx0, idx1, idx2 := int32(0), int32(1), int32(2) // #nosec G115 -- constant indices 0-2

	// No user-data in this slice — onboarding is handled by F2-B2.
	// An empty (base64-encoded empty string) is fine; RunInstances accepts nil
	// UserData but we send a commented placeholder for clarity.
	userDataComment := "# BIG-IP onboarding deferred to F2-B2 (AWSBNKCTL_BIGIP_PASSWORD env)"
	userDataB64 := base64.StdEncoding.EncodeToString([]byte(userDataComment))

	runOut, err := ec2c.RunInstances(ctx, &ec2.RunInstancesInput{
		ImageId:      ptr(amiID),
		InstanceType: ec2types.InstanceType(instanceType),
		MinCount:     int32Ptr(1),
		MaxCount:     int32Ptr(1),
		KeyName:      ptr(keyName),
		UserData:     ptr(userDataB64),
		// Attach all 3 ENIs at launch — BIG-IP requires stable ENI ordering.
		// DeviceIndex 0=mgmt 1=ext(VIP) 2=int. No primary subnet is needed
		// because the NetworkInterfaces list drives placement.
		NetworkInterfaces: []ec2types.InstanceNetworkInterfaceSpecification{
			{DeviceIndex: &idx0, NetworkInterfaceId: ptr(mgmtENIID)},
			{DeviceIndex: &idx1, NetworkInterfaceId: ptr(extENIID)},
			{DeviceIndex: &idx2, NetworkInterfaceId: ptr(intENIID)},
		},
		TagSpecifications: []ec2types.TagSpecification{
			tagSpecification(ec2types.ResourceTypeInstance, instTags),
		},
	})
	if err != nil {
		return "", fmt.Errorf("ec2:RunInstances bigip-ve: %w", err)
	}
	if len(runOut.Instances) == 0 {
		return "", fmt.Errorf("ec2:RunInstances returned no instances")
	}
	instanceID := ""
	if runOut.Instances[0].InstanceId != nil {
		instanceID = *runOut.Instances[0].InstanceId
	}
	fmt.Fprintf(os.Stderr, "[phase 17e] launched BIG-IP VE %s (ami=%s type=%s)\n", instanceID, amiID, instanceType)

	// Wait for running.
	if err := waitBigIPInstanceRunning(ctx, ec2c, instanceID); err != nil {
		return "", fmt.Errorf("waiting for BIG-IP instance %s running: %w", instanceID, err)
	}
	return instanceID, nil
}

// waitBigIPInstanceRunning polls until the BIG-IP VE EC2 instance is running.
func waitBigIPInstanceRunning(ctx context.Context, ec2c EC2API, instanceID string) error {
	deadline := time.Now().Add(bigipVEInstanceRunningTimeout)
	for time.Now().Before(deadline) {
		out, err := ec2c.DescribeInstances(ctx, &ec2.DescribeInstancesInput{
			InstanceIds: []string{instanceID},
		})
		if err == nil && len(out.Reservations) > 0 && len(out.Reservations[0].Instances) > 0 {
			s := out.Reservations[0].Instances[0].State
			if s != nil && s.Name == ec2types.InstanceStateNameRunning {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(bigipVEInstancePollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for BIG-IP instance %s to reach running state", instanceID)
}

// waitBigIPInstanceTerminated polls until the BIG-IP VE EC2 instance is terminated.
func waitBigIPInstanceTerminated(ctx context.Context, ec2c EC2API, instanceID string) error {
	deadline := time.Now().Add(bigipVEInstanceTerminatedTimeout)
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
		case <-time.After(bigipVEInstancePollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for BIG-IP instance %s to terminate", instanceID)
}

// clearBigIPVEState removes all BIGIP_* state keys.
func clearBigIPVEState(st *state.State) {
	for _, key := range []string{
		"BIGIP_INSTANCE_ID",
		"BIGIP_MGMT_ENI_ID",
		"BIGIP_MGMT_IP",
		"BIGIP_EXT_ENI_ID",
		"BIGIP_EXT_IP",
		"BIGIP_VIP",
		"BIGIP_INT_ENI_ID",
		"BIGIP_INT_IP",
		"BIGIP_MGMT_SG_ID",
		"BIGIP_KEY_NAME",
		"BIGIP_AMI_ID",
		"BIGIP_SSH_KEY_PATH",
	} {
		st.Set(key, "")
	}
}

// cidrDotN returns the host address at offset n within the given CIDR.
// For example, cidrDotN("10.0.10.0/24", 50) returns "10.0.10.50".
func cidrDotN(cidr string, n int) (string, error) {
	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return "", fmt.Errorf("parse CIDR %q: %w", cidr, err)
	}
	ip := ipNet.IP.To4()
	if ip == nil {
		return "", fmt.Errorf("CIDR %q is not IPv4", cidr)
	}
	// Copy to avoid modifying the shared slice.
	result := make(net.IP, 4)
	copy(result, ip)
	result[3] = byte(n)
	if !ipNet.Contains(result) {
		return "", fmt.Errorf("computed IP %s (offset %d) is outside CIDR %s", result, n, cidr)
	}
	return result.String(), nil
}

// isBigIPOptInRequired returns true when the EC2 error is OptInRequired —
// indicating the operator has not subscribed to the BIG-IP VE AMI in
// AWS Marketplace.
func isBigIPOptInRequired(err error) bool {
	if err == nil {
		return false
	}
	type coder interface{ ErrorCode() string }
	e := err
	for e != nil {
		if ce, ok := e.(coder); ok {
			return ce.ErrorCode() == bigipVEOptInRequiredCode
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := e.(unwrapper); ok {
			e = u.Unwrap()
		} else {
			break
		}
	}
	return strings.Contains(err.Error(), bigipVEOptInRequiredCode)
}
