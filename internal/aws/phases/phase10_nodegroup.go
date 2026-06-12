package phases

import (
	"context"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/autoscaling"
	astypes "github.com/aws/aws-sdk-go-v2/service/autoscaling/types"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/tags"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// bnkLaunchTemplateUserData is the MIME multipart UserData for BNK worker nodes.
// Ported from aws-gpu-setup/up.sh:413-433.
//   - sysctl: raises inotify watch limit required by BNK workloads.
//   - udev rule: auto-brings-up ENA netdevs when they appear (secondary ENIs
//     attached in Phase 17 arrive DOWN on AL2023; TMM host-device CNI needs
//     carrier-UP interfaces in the pod netns at pod start).
//   - Boot-time loop: brings up ENA netdevs already present at boot.
const bnkLaunchTemplateUserData = `MIME-Version: 1.0
Content-Type: multipart/mixed; boundary="==BOUNDARY=="

--==BOUNDARY==
Content-Type: text/x-shellscript; charset="us-ascii"

#!/bin/bash
echo "fs.inotify.max_user_watches=1048576" >> /etc/sysctl.d/99-bnk.conf
sysctl -p /etc/sysctl.d/99-bnk.conf
cat > /etc/udev/rules.d/70-bnk-ena-up.rules <<EOF
ACTION=="add", SUBSYSTEM=="net", DRIVERS=="ena", RUN+="/sbin/ip link set %k up"
EOF
udevadm control --reload-rules
for ifname in $(ls /sys/class/net/); do
  drv=$(basename "$(readlink -f /sys/class/net/$ifname/device/driver 2>/dev/null)" 2>/dev/null)
  [ "$drv" = "ena" ] && ip link set "$ifname" up || true
done
--==BOUNDARY==--`

// Phase10NodeGroup creates managed node groups defined in cluster.yaml.
// One node group per NodeGroupSpec entry. Subnets used are public only.
//
// The node group uses a Launch Template (LT) with MIME-multipart UserData
// containing sysctl + udev rules required for BNK host-device ENI bring-up.
// The LT is created once and the node group is bound via LaunchTemplate{Id, Version=$Latest}.
// EKS UpdateNodegroupConfig does NOT accept retroactive LT addition — changing
// the LT requires down + re-up per D-007.
//
// State keys written per node group: NODEGROUP_<UPPER>_NAME, NODEGROUP_<UPPER>_ARN.
// State key LT_ID: the launch template ID.
//
// Idempotent: DescribeNodegroup + DescribeLaunchTemplates before creating.
// Dry-run: writes placeholder state, no AWS mutations.
func Phase10NodeGroup(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name

	if cl.ClusterSpec == nil {
		return fmt.Errorf("phase10: cluster.yaml must include a 'cluster:' block (see slice-03 docs)")
	}

	ltName := name + "-bnk-lt"
	gpuLTName := name + "-gpu-lt"

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 10] dry-run: would create launch template %s\n", ltName)
		st.Set("LT_ID", "lt-dry-run")
		if cl.HasGPUNodeGroup() {
			fmt.Fprintf(os.Stderr, "[phase 10] dry-run: would create GPU launch template %s\n", gpuLTName)
			st.Set("GPU_LT_ID", "lt-gpu-dry-run")
		}
		for _, ng := range cl.ClusterSpec.NodeGroups {
			upper := strings.ToUpper(ng.Name)
			ngName := name + "-ng-" + ng.Name
			fmt.Fprintf(os.Stderr, "[phase 10] dry-run: would create node group %s (lt=%s)\n", ngName, ltName)
			st.Set("NODEGROUP_"+upper+"_NAME", "dry-run-ng-"+ng.Name)
			st.Set("NODEGROUP_"+upper+"_ARN", "arn:aws:eks:dry-run:nodegroup/"+name+"/"+ngName+"/dry-run")
		}
		return nil
	}

	clusterName := st.Get("EKS_CLUSTER_NAME")
	if clusterName == "" {
		return fmt.Errorf("phase10: EKS_CLUSTER_NAME not in state (run phase08 first)")
	}
	nodeRoleARN := st.Get("EKS_NODE_ROLE_ARN")
	if nodeRoleARN == "" {
		return fmt.Errorf("phase10: EKS_NODE_ROLE_ARN not in state (run phase07 first)")
	}
	publicSubnets := splitCSV(st.Get("PUBLIC_SUBNETS"))
	if len(publicSubnets) == 0 {
		return fmt.Errorf("phase10: PUBLIC_SUBNETS not in state (run phase03 first)")
	}

	// For BNK patterns: pin the BNK node group to the public subnet that shares
	// an AZ with the external data-path subnet. EKS picks an AZ from the node
	// group's subnet set when launching the node; if we pass both AZs, EKS may
	// land the node in the wrong AZ and Phase 17 ENI attach fails with
	// "not in the same availability zone".
	// GPU node groups are NOT subject to this AZ pin — they have their own
	// per-nodegroup AZ filter applied in ensureNodeGroup.
	bnkSubnets := publicSubnets
	if cl.IsBNKPattern() && cl.Network.DataPath != nil {
		targetAZ := cl.Network.DataPath.External.AZ
		filtered := filterSubnetsByAZ(publicSubnets, cl.Network.Subnets.Public, targetAZ)
		if len(filtered) == 0 {
			return fmt.Errorf("phase10: no public subnet matches data-path AZ %q", targetAZ)
		}
		fmt.Fprintf(os.Stderr, "[phase 10] BNK pattern: pinning BNK node group to AZ=%s (subnets=%v)\n", targetAZ, filtered)
		bnkSubnets = filtered
	}

	// Ensure the BNK Launch Template exists. GPU node groups do NOT use this
	// LT — it carries TMM host-device ENA udev rules incompatible with NVIDIA AMI.
	ltID, err := ensureLaunchTemplate(ctx, clients.EC2, name, ltName, cl.Tags, cl.Metadata.Labels)
	if err != nil {
		return fmt.Errorf("phase10: launch template: %w", err)
	}
	st.Set("LT_ID", ltID)

	// Ensure the GPU Launch Template exists when the cluster has a GPU nodegroup.
	// This LT carries MetadataOptions{HttpPutResponseHopLimit:2} and a root volume
	// BlockDeviceMapping sized to the largest GPU nodegroup's DiskSize — so vLLM
	// pods can reach IMDS (F4 fix) and GPU nodes get a large-enough root volume for
	// the vLLM image (~10 GB) + Llama-3-8B weights (~16 GB).
	// Note: a single GPU LT is shared across all GPU nodegroups (uses the largest
	// DiskSize among them). Realistically there is one GPU nodegroup per cluster.
	// The EKS SDK v1.83.0 does not expose MetadataOptions on CreateNodegroupInput;
	// a launch template is the only supported path.
	gpuLTID := ""
	if cl.HasGPUNodeGroup() {
		var maxGPUDiskSize int32
		for _, ng := range cl.ClusterSpec.NodeGroups {
			if ng.IsGPU() && int32(ng.DiskSize) > maxGPUDiskSize { //nolint:gosec // bounded by earlier check
				maxGPUDiskSize = int32(ng.DiskSize) //nolint:gosec // bounded by earlier check
			}
		}
		gpuLTID, err = ensureGPULaunchTemplate(ctx, clients.EC2, name, gpuLTName, maxGPUDiskSize, cl.Tags, cl.Metadata.Labels)
		if err != nil {
			return fmt.Errorf("phase10: GPU launch template: %w", err)
		}
		st.Set("GPU_LT_ID", gpuLTID)
	}

	for _, ng := range cl.ClusterSpec.NodeGroups {
		if ng.IsGPU() {
			// GPU nodegroups use a per-AZ sweep: create in one AZ at a time, detect
			// capacity errors quickly via ASG scaling activities, delete and move to
			// the next AZ on exhaustion. Non-GPU nodegroups use the original path.
			//
			// Candidate AZ list: ng.AZs filtered through the GPU deny table (same
			// logic as intent validation). If ng.AZs is empty we use all public AZs.
			candidateAZs := buildGPUCandidateAZs(ng, cl)
			if len(candidateAZs) == 0 {
				return fmt.Errorf("phase10: GPU node group %s: no eligible AZs after deny-table filter (azs=%v)", ng.Name, ng.AZs)
			}
			if err := ensureGPUNodeGroupWithAZSweep(ctx, clients.EKS, clients.AutoScaling, name, clusterName, nodeRoleARN, publicSubnets, cl.Network.Subnets.Public, gpuLTID, ng, candidateAZs, cl.Tags, cl.Metadata.Labels, st); err != nil {
				return fmt.Errorf("phase10: GPU node group %s: %w", ng.Name, err)
			}
		} else {
			// Non-GPU nodegroup: original path, unchanged.
			if err := ensureNodeGroup(ctx, clients.EKS, name, clusterName, nodeRoleARN, bnkSubnets, ltID, gpuLTID, ng, cl.Tags, cl.Metadata.Labels, st); err != nil {
				return fmt.Errorf("phase10: node group %s: %w", ng.Name, err)
			}
		}
	}
	return st.Save()
}

// Phase10NodeGroupDown deletes all managed node groups for the cluster, then
// deletes the Launch Template. Tolerates ResourceNotFoundException (already deleted).
func Phase10NodeGroupDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 10 down] node group: cluster=%s\n", name)

	clusterName := st.Get("EKS_CLUSTER_NAME")
	if clusterName == "" {
		clusterName = name
	}

	if cl.ClusterSpec == nil {
		fmt.Fprintf(os.Stderr, "[phase 10 down] no cluster spec, skipping node group deletion\n")
		return nil
	}

	for _, ng := range cl.ClusterSpec.NodeGroups {
		upper := strings.ToUpper(ng.Name)
		ngName := st.Get("NODEGROUP_" + upper + "_NAME")
		if ngName == "" {
			ngName = clusterName + "-ng-" + ng.Name
		}

		if err := deleteNodeGroup(ctx, clients.EKS, clusterName, ngName); err != nil {
			return fmt.Errorf("phase10 down: node group %s: %w", ngName, err)
		}
		st.Set("NODEGROUP_"+upper+"_NAME", "")
		st.Set("NODEGROUP_"+upper+"_ARN", "")
	}

	// Delete the BNK Launch Template.
	ltID := st.Get("LT_ID")
	ltName := name + "-bnk-lt"
	if ltID == "" {
		// Name-based fallback.
		ltID = lookupLTByName(ctx, clients.EC2, ltName)
	}
	if ltID != "" {
		fmt.Fprintf(os.Stderr, "[phase 10 down] deleting BNK launch template %s\n", ltID)
		_, err := clients.EC2.DeleteLaunchTemplate(ctx, &ec2.DeleteLaunchTemplateInput{
			LaunchTemplateId: ptr(ltID),
		})
		if err := ignoreNotFound(err); err != nil {
			return fmt.Errorf("phase10 down: DeleteLaunchTemplate %s: %w", ltID, err)
		}
	}
	st.Set("LT_ID", "")

	// Delete the GPU Launch Template (if it was created).
	gpuLTID := st.Get("GPU_LT_ID")
	gpuLTName := name + "-gpu-lt"
	if gpuLTID == "" {
		// Name-based fallback.
		gpuLTID = lookupLTByName(ctx, clients.EC2, gpuLTName)
	}
	if gpuLTID != "" {
		fmt.Fprintf(os.Stderr, "[phase 10 down] deleting GPU launch template %s\n", gpuLTID)
		_, err := clients.EC2.DeleteLaunchTemplate(ctx, &ec2.DeleteLaunchTemplateInput{
			LaunchTemplateId: ptr(gpuLTID),
		})
		if err := ignoreNotFound(err); err != nil {
			return fmt.Errorf("phase10 down: DeleteLaunchTemplate (GPU) %s: %w", gpuLTID, err)
		}
	}
	st.Set("GPU_LT_ID", "")

	return st.Save()
}

// --- helpers ---

func ensureNodeGroup(
	ctx context.Context,
	eksc EKSAPI,
	clusterDisplayName, clusterName, nodeRoleARN string,
	ngSubnets []string,
	ltID string,
	gpuLTID string,
	ng intent.NodeGroupSpec,
	extraTags map[string]string,
	labels map[string]string,
	st *state.State,
) error {
	upper := strings.ToUpper(ng.Name)
	ngName := clusterName + "-ng-" + ng.Name

	fmt.Fprintf(os.Stderr, "[phase 10] node group: creating %s (%s × %d, lt=%s)\n", ngName, ng.InstanceType, ng.DesiredSize, ltID)

	// Idempotency check.
	descOut, err := eksc.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   ptr(clusterName),
		NodegroupName: ptr(ngName),
	})
	if err != nil && !isEKSNotFound(err) {
		return fmt.Errorf("DescribeNodegroup: %w", err)
	}

	if err == nil {
		existing := descOut.Nodegroup
		switch existing.Status {
		case ekstypes.NodegroupStatusActive:
			fmt.Fprintf(os.Stderr, "[phase 10] node group %s already exists, status=ACTIVE, skipping create\n", ngName)
			return populateNodeGroupState(st, existing, upper)
		case ekstypes.NodegroupStatusCreating, ekstypes.NodegroupStatusUpdating:
			fmt.Fprintf(os.Stderr, "[phase 10] node group %s already exists, status=%s, waiting for ACTIVE\n", ngName, existing.Status)
			return waitAndPopulateNodeGroup(ctx, eksc, clusterName, ngName, upper, st)
		case ekstypes.NodegroupStatusCreateFailed, ekstypes.NodegroupStatusDeleteFailed:
			return fmt.Errorf("node group %s in terminal failure status %s", ngName, existing.Status)
		default:
			return fmt.Errorf("node group %s in unexpected status %s", ngName, existing.Status)
		}
	}

	ngTags := tags.EKSTags(
		tags.Required(clusterDisplayName, tags.CompEKSNodeGroup),
		extraTags,
		labels,
	)

	// Kubernetes node labels. K8s label keys can't contain ':' so use the
	// awsbnkctl.io/ prefix (matching the namespace-label convention from
	// docs/ARCHITECTURE.md). The `:` form used for
	// AWS resource tags would cause an EKS InvalidParameterException
	// (regex '([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]').
	k8sLabels := map[string]string{
		"awsbnkctl.io/cluster": clusterDisplayName,
	}
	for k, v := range ng.Labels {
		k8sLabels[k] = v
	}
	// GPU node groups get the awsbnkctl.io/gpu label so the NVIDIA device-plugin
	// DaemonSet's nodeSelector targets them specifically.
	if ng.IsGPU() {
		k8sLabels["awsbnkctl.io/gpu"] = "true"
	}

	// Bounds-check before int32 cast (DiskSize/DesiredSize/MinSize/MaxSize
	// come from validated cluster.yaml; defaults are 50/1/1/2). EKS API
	// requires int32. The #nosec annotations satisfy gosec G115 — the
	// bounds check above makes the cast genuinely safe.
	if ng.DiskSize > 1<<30 || ng.DesiredSize > 1<<30 || ng.MinSize > 1<<30 || ng.MaxSize > 1<<30 {
		return fmt.Errorf("nodegroup %s: scaling/disk value too large", ngName)
	}
	desiredSize := int32(ng.DesiredSize) // #nosec G115 -- bounded above
	minSize := int32(ng.MinSize)         // #nosec G115 -- bounded above
	maxSize := int32(ng.MaxSize)         // #nosec G115 -- bounded above

	// Select AmiType per node group:
	// - GPU node groups: AL2023_x86_64_NVIDIA (includes NVIDIA drivers).
	// - BNK node groups: AL2023_x86_64_STANDARD (predictable ens5..ens8 naming).
	//   The downstream stack assumes AL2023 Standard for BNK nodes:
	//   Phase 17 attaches BNK_INT at device-index 2 (→ ens7) and BNK_EXT at
	//   device-index 3 (→ ens8); Phase 19 hard-codes those names in the
	//   cloud-network-mapping ConfigMap; Phase 20 NADs reference them too.
	//   AL2 names secondary ENIs eth1..ethN, which breaks Multus link-lookup.
	//   See docs/audits/slice-09-aws-gpu-setup-audit.md.3 and
	//   aws-gpu-setup/vars.env:92-95 for the source of the naming contract.
	amiType := ekstypes.AMITypesAl2023X8664Standard
	if ng.IsGPU() {
		amiType = ekstypes.AMITypesAl2023X8664Nvidia
	}

	// Select CapacityType (on-demand or spot).
	capacityType := ekstypes.CapacityTypesOnDemand
	if ng.CapacityType == "spot" {
		capacityType = ekstypes.CapacityTypesSpot
	}

	// Convert intent taints to EKS taint format.
	var eksTaints []ekstypes.Taint
	for _, t := range ng.Taints {
		taint := ekstypes.Taint{
			Key:   ptr(t.Key),
			Value: ptr(t.Value),
		}
		switch t.Effect {
		case "NoSchedule":
			taint.Effect = ekstypes.TaintEffectNoSchedule
		case "NoExecute":
			taint.Effect = ekstypes.TaintEffectNoExecute
		case "PreferNoSchedule":
			taint.Effect = ekstypes.TaintEffectPreferNoSchedule
		}
		eksTaints = append(eksTaints, taint)
	}

	// Build the CreateNodegroup input.
	// MUST-CARRY GUARD (R6): GPU node groups do NOT receive the BNK launch template.
	// That LT carries TMM host-device ENA-up udev rules + assumes the BNK ens5..ens8
	// naming contract. GPU nodes use the NVIDIA AMI's default user-data instead.
	// BNK node groups continue to use the launch template for ENA bring-up.
	input := &eks.CreateNodegroupInput{
		ClusterName:   ptr(clusterName),
		NodegroupName: ptr(ngName),
		NodeRole:      ptr(nodeRoleARN),
		Subnets:       ngSubnets,
		AmiType:       amiType,
		CapacityType:  capacityType,
		InstanceTypes: []string{ng.InstanceType},
		ScalingConfig: &ekstypes.NodegroupScalingConfig{
			DesiredSize: int32Ptr(desiredSize),
			MinSize:     int32Ptr(minSize),
			MaxSize:     int32Ptr(maxSize),
		},
		Taints: eksTaints,
		Labels: k8sLabels,
		Tags:   ngTags,
	}
	if !ng.IsGPU() {
		// BNK node groups: bind to the BNK launch template (ENA udev rules + IMDSv2 hop=2).
		// DiskSize is NOT set when using a launch template (EKS rejects the combination).
		ltVersion := "$Latest"
		input.LaunchTemplate = &ekstypes.LaunchTemplateSpecification{
			Id:      ptr(ltID),
			Version: ptr(ltVersion),
		}
	} else {
		// GPU node groups: use the minimal GPU launch template (gpuLTID) that sets
		// MetadataOptions{HttpPutResponseHopLimit:2} so vLLM pods can reach IMDS
		// for region/STS/S3 (F4 fix). The EKS SDK v1.83.0 does not expose
		// MetadataOptions on CreateNodegroupInput; a launch template is the only
		// supported path. This LT carries no UserData — it does NOT include the
		// BNK ENA udev rules which are GPU-incompatible (R6 guard preserved).
		// DiskSize must NOT be set when using a launch template (EKS rejects the
		// combination); the GPU LT data does not set disk size so EKS uses the
		// per-instance-type default.
		if gpuLTID == "" {
			return fmt.Errorf("ensureNodeGroup: GPU node group %s requires gpuLTID (nil passed)", ngName)
		}
		ltVersion := "$Latest"
		input.LaunchTemplate = &ekstypes.LaunchTemplateSpecification{
			Id:      ptr(gpuLTID),
			Version: ptr(ltVersion),
		}
	}
	_, err = eksc.CreateNodegroup(ctx, input)
	if err != nil {
		return fmt.Errorf("CreateNodegroup %s: %w", ngName, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 10] node group %s: create request sent, waiting for ACTIVE (up to 20 min)\n", ngName)

	return waitAndPopulateNodeGroup(ctx, eksc, clusterName, ngName, upper, st)
}

// ensureLaunchTemplate creates the BNK Launch Template with MIME-multipart
// UserData if not already present. Idempotent: lookup-by-name first.
// The LT sets MetadataOptions for IMDSv2 with hop=2 so pods can reach
// 169.254.169.254 (EKS-optimized AMIs default hop=1 which blocks pod IMDS).
func ensureLaunchTemplate(ctx context.Context, ec2c EC2API, clusterName, ltName string,
	extraTags, labels map[string]string) (string, error) {

	// Idempotency: look up by name.
	existing, err := ec2c.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
		Filters: []ec2types.Filter{
			{Name: ptr("launch-template-name"), Values: []string{ltName}},
		},
	})
	if err == nil && len(existing.LaunchTemplates) > 0 {
		ltID := *existing.LaunchTemplates[0].LaunchTemplateId
		fmt.Fprintf(os.Stderr, "[phase 10] launch template %s already exists (%s), skipping\n", ltName, ltID)
		return ltID, nil
	}

	// Encode UserData as base64.
	udB64 := base64.StdEncoding.EncodeToString([]byte(bnkLaunchTemplateUserData))

	ltTags := tags.Merge(
		tags.Required(clusterName, tags.CompLaunchTemplate),
		extraTags,
		labels,
	)
	ltTagSpec := ec2types.TagSpecification{
		ResourceType: ec2types.ResourceTypeLaunchTemplate,
		Tags:         ltTags,
	}

	out, err := ec2c.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: ptr(ltName),
		TagSpecifications:  []ec2types.TagSpecification{ltTagSpec},
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			UserData: ptr(udB64),
			MetadataOptions: &ec2types.LaunchTemplateInstanceMetadataOptionsRequest{
				HttpTokens:              ec2types.LaunchTemplateHttpTokensStateRequired,
				HttpPutResponseHopLimit: int32Ptr(2),
				HttpEndpoint:            ec2types.LaunchTemplateInstanceMetadataEndpointStateEnabled,
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("ec2:CreateLaunchTemplate %s: %w", ltName, err)
	}
	ltID := *out.LaunchTemplate.LaunchTemplateId
	fmt.Fprintf(os.Stderr, "[phase 10] created launch template %s (%s)\n", ltName, ltID)
	return ltID, nil
}

// ensureGPULaunchTemplate creates a minimal Launch Template for GPU node groups.
// It carries MetadataOptions{HttpPutResponseHopLimit:2} (IMDS hop-limit F4 fix)
// and a BlockDeviceMapping for the AL2023 root volume (/dev/xvda, gp3) sized to
// diskSize GiB — so vLLM can pull its ~10 GB image + ~16 GB Llama-3-8B weights.
// No UserData is set: GPU nodes use the NVIDIA AMI's default bootstrap.
// The BNK LT is intentionally NOT used for GPU nodes (R6 guard: the BNK LT
// embeds ENA udev rules that are incompatible with the NVIDIA AMI path).
// Idempotent: lookup-by-name first.
func ensureGPULaunchTemplate(ctx context.Context, ec2c EC2API, clusterName, ltName string,
	diskSize int32, extraTags, labels map[string]string) (string, error) {

	// Idempotency: look up by name.
	existing, err := ec2c.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
		Filters: []ec2types.Filter{
			{Name: ptr("launch-template-name"), Values: []string{ltName}},
		},
	})
	if err == nil && len(existing.LaunchTemplates) > 0 {
		ltID := *existing.LaunchTemplates[0].LaunchTemplateId
		fmt.Fprintf(os.Stderr, "[phase 10] GPU launch template %s already exists (%s), skipping\n", ltName, ltID)
		return ltID, nil
	}

	ltTags := tags.Merge(
		tags.Required(clusterName, tags.CompLaunchTemplate),
		extraTags,
		labels,
	)
	ltTagSpec := ec2types.TagSpecification{
		ResourceType: ec2types.ResourceTypeLaunchTemplate,
		Tags:         ltTags,
	}

	out, err := ec2c.CreateLaunchTemplate(ctx, &ec2.CreateLaunchTemplateInput{
		LaunchTemplateName: ptr(ltName),
		TagSpecifications:  []ec2types.TagSpecification{ltTagSpec},
		LaunchTemplateData: &ec2types.RequestLaunchTemplateData{
			// No UserData: GPU nodes use the NVIDIA AMI's default bootstrap.
			// MetadataOptions raises the IMDS hop limit from 1→2 so pods on
			// GPU nodes can reach 169.254.169.254 (F4 fix).
			MetadataOptions: &ec2types.LaunchTemplateInstanceMetadataOptionsRequest{
				HttpTokens:              ec2types.LaunchTemplateHttpTokensStateRequired,
				HttpPutResponseHopLimit: int32Ptr(2),
				HttpEndpoint:            ec2types.LaunchTemplateInstanceMetadataEndpointStateEnabled,
			},
			// BlockDeviceMappings carries the root-volume size because EKS rejects
			// LaunchTemplate + DiskSize together on CreateNodegroup (SDK v1.83.0).
			// /dev/xvda is the AL2023 EKS AMI root device name.
			BlockDeviceMappings: []ec2types.LaunchTemplateBlockDeviceMappingRequest{
				{
					DeviceName: ptr("/dev/xvda"),
					Ebs: &ec2types.LaunchTemplateEbsBlockDeviceRequest{
						VolumeType:          ec2types.VolumeTypeGp3,
						VolumeSize:          int32Ptr(diskSize),
						DeleteOnTermination: boolPtr(true),
					},
				},
			},
		},
	})
	if err != nil {
		return "", fmt.Errorf("ec2:CreateLaunchTemplate (GPU) %s: %w", ltName, err)
	}
	ltID := *out.LaunchTemplate.LaunchTemplateId
	fmt.Fprintf(os.Stderr, "[phase 10] created GPU launch template %s (%s)\n", ltName, ltID)
	return ltID, nil
}

// lookupLTByName does a best-effort name-based lookup for the down-path.
// Returns "" on any error.
func lookupLTByName(ctx context.Context, ec2c EC2API, ltName string) string {
	out, err := ec2c.DescribeLaunchTemplates(ctx, &ec2.DescribeLaunchTemplatesInput{
		Filters: []ec2types.Filter{
			{Name: ptr("launch-template-name"), Values: []string{ltName}},
		},
	})
	if err != nil || len(out.LaunchTemplates) == 0 {
		return ""
	}
	return *out.LaunchTemplates[0].LaunchTemplateId
}

func waitAndPopulateNodeGroup(ctx context.Context, eksc EKSAPI, clusterName, ngName, upper string, st *state.State) error {
	const (
		ngTimeout = 20 * time.Minute
		ngPoll    = 30 * time.Second
	)
	deadline := time.Now().Add(ngTimeout)
	for time.Now().Before(deadline) {
		out, err := eksc.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
			ClusterName:   ptr(clusterName),
			NodegroupName: ptr(ngName),
		})
		if err != nil {
			return fmt.Errorf("waitAndPopulateNodeGroup: DescribeNodegroup: %w", err)
		}
		ng := out.Nodegroup
		switch ng.Status {
		case ekstypes.NodegroupStatusActive:
			fmt.Fprintf(os.Stderr, "[phase 10] node group %s reached state ACTIVE\n", ngName)
			return populateNodeGroupState(st, ng, upper)
		case ekstypes.NodegroupStatusCreateFailed, ekstypes.NodegroupStatusDeleteFailed:
			return fmt.Errorf("node group %s entered failure status %s", ngName, ng.Status)
		case ekstypes.NodegroupStatusCreating, ekstypes.NodegroupStatusUpdating:
			// still in progress
		default:
			return fmt.Errorf("node group %s in unexpected state %s", ngName, ng.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ngPoll):
		}
	}
	return fmt.Errorf("phase10: timeout waiting for node group %s to become ACTIVE", ngName)
}

func populateNodeGroupState(st *state.State, ng *ekstypes.Nodegroup, upper string) error {
	if ng.NodegroupArn == nil {
		return fmt.Errorf("phase10: nodegroup describe response missing ARN")
	}
	st.Set("NODEGROUP_"+upper+"_NAME", *ng.NodegroupName)
	st.Set("NODEGROUP_"+upper+"_ARN", *ng.NodegroupArn)
	return st.Save()
}

func deleteNodeGroup(ctx context.Context, eksc EKSAPI, clusterName, ngName string) error {
	fmt.Fprintf(os.Stderr, "[phase 10 down] deleting node group %s\n", ngName)
	_, err := eksc.DeleteNodegroup(ctx, &eks.DeleteNodegroupInput{
		ClusterName:   ptr(clusterName),
		NodegroupName: ptr(ngName),
	})
	if err != nil {
		if isEKSNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 10 down] node group %s already gone\n", ngName)
			return nil
		}
		return fmt.Errorf("DeleteNodegroup %s: %w", ngName, err)
	}
	return waitNodeGroupDeleted(ctx, eksc, clusterName, ngName)
}

func waitNodeGroupDeleted(ctx context.Context, eksc EKSAPI, clusterName, ngName string) error {
	const (
		deleteTimeout = 15 * time.Minute
		deletePoll    = 30 * time.Second
	)
	fmt.Fprintf(os.Stderr, "[phase 10 down] waiting for node group %s to be deleted\n", ngName)
	deadline := time.Now().Add(deleteTimeout)
	for time.Now().Before(deadline) {
		_, err := eksc.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
			ClusterName:   ptr(clusterName),
			NodegroupName: ptr(ngName),
		})
		if err != nil {
			if isEKSNotFound(err) {
				fmt.Fprintf(os.Stderr, "[phase 10 down] node group %s deleted\n", ngName)
				return nil
			}
			return fmt.Errorf("waitNodeGroupDeleted: DescribeNodegroup: %w", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(deletePoll):
		}
	}
	return fmt.Errorf("phase10 down: timeout waiting for node group %s to be deleted", ngName)
}

// int32Ptr returns a pointer to an int32.
func int32Ptr(v int32) *int32 { return &v }

// filterSubnetsByAZ returns only the entries of stateSubnets whose
// cluster.yaml SubnetSpec position has the target AZ. Phase 03 writes
// PUBLIC_SUBNETS in the same order as cl.Network.Subnets.Public[], so we can
// correlate by index without re-querying AWS.
func filterSubnetsByAZ(stateSubnets []string, specSubnets []intent.SubnetSpec, az string) []string {
	if len(stateSubnets) != len(specSubnets) {
		// Mismatch (shouldn't happen in normal flow); return all to fail loudly later.
		return stateSubnets
	}
	var out []string
	for i, spec := range specSubnets {
		if spec.AZ == az {
			out = append(out, stateSubnets[i])
		}
	}
	return out
}

// --- GPU AZ-sweep helpers ---

// capacityErrorSubstrings are the substrings that appear in ASG scaling-activity
// StatusMessage when the AZ has no capacity for the requested instance type.
// Source: live ASG activities observed 2026-06-12 for g5.2xlarge in ap-southeast-2.
var capacityErrorSubstrings = []string{
	"InsufficientInstanceCapacity",
	"UnfulfillableCapacity",
	"Could not launch",
}

// buildGPUCandidateAZs derives the ordered list of AZs to try for a GPU
// nodegroup. It starts from ng.AZs (or all network AZs if ng.AZs is empty)
// and removes entries that appear in the static + env GPU AZ-deny table.
// Order is preserved so the caller controls priority.
func buildGPUCandidateAZs(ng intent.NodeGroupSpec, cl *intent.Cluster) []string {
	// Build deny table (same logic as intent.validateNodeGroups, keeps in sync).
	deny := make(map[string]bool)
	region := cl.Metadata.Region
	// Import-free re-implementation of the deny table to avoid a cross-package
	// import cycle; the deny table is small and stable.
	staticDeny := map[string][]string{
		"ap-southeast-2": {"ap-southeast-2b"},
	}
	for _, az := range staticDeny[region] {
		deny[az] = true
	}
	if envVal := os.Getenv("AWSBNKCTL_GPU_AZ_DENY"); envVal != "" {
		// Parse "region:az1,az2;region2:az3" format.
		for _, entry := range strings.Split(envVal, ";") {
			parts := strings.SplitN(strings.TrimSpace(entry), ":", 2)
			if len(parts) != 2 || strings.TrimSpace(parts[0]) != region {
				continue
			}
			for _, az := range strings.Split(parts[1], ",") {
				az = strings.TrimSpace(az)
				if az != "" {
					deny[az] = true
				}
			}
		}
	}

	// Source AZ list: declared AZs or all network AZs.
	src := ng.AZs
	if len(src) == 0 {
		src = cl.Network.AZs
	}

	var candidates []string
	for _, az := range src {
		if !deny[az] {
			candidates = append(candidates, az)
		}
	}
	return candidates
}

// azSweepResult is the outcome of a single AZ attempt.
type azSweepResult int

const (
	azResultSuccess azSweepResult = iota // nodegroup reached ACTIVE
	azResultExhaust                      // capacity error, no instance launched — try next AZ
	azResultError                        // hard error (AWS API failure, unexpected state)
)

// ensureGPUNodeGroupWithAZSweep creates the GPU nodegroup in each candidate AZ
// in sequence. On capacity exhaustion it deletes the failed nodegroup and moves
// to the next AZ. On success it stores state and returns nil. If capacityType is
// "spot" and all AZs fail, and ng.OnDemandFallback is true, the sweep is retried
// with on-demand. Returns an aggregated error if all options are exhausted.
func ensureGPUNodeGroupWithAZSweep(
	ctx context.Context,
	eksc EKSAPI,
	asc AutoScalingAPI,
	clusterDisplayName, clusterName, nodeRoleARN string,
	publicSubnets []string,
	specSubnets []intent.SubnetSpec,
	gpuLTID string,
	ng intent.NodeGroupSpec,
	candidateAZs []string,
	extraTags map[string]string,
	labels map[string]string,
	st *state.State,
) error {
	// sweepOnce tries every candidate AZ for a given capacityType and returns
	// nil on first success, or a slice of (az, msg) failure entries on exhaustion.
	type attempt struct{ az, msg string }
	sweepOnce := func(capType string) ([]attempt, error) {
		var tried []attempt
		for _, az := range candidateAZs {
			azSubnets := filterSubnetsByAZ(publicSubnets, specSubnets, az)
			if len(azSubnets) == 0 {
				return nil, fmt.Errorf("GPU node group %s: no public subnets match AZ %s", ng.Name, az)
			}

			// Build a per-AZ NodeGroupSpec with the effective capacityType.
			ngAZ := ng
			ngAZ.CapacityType = capType

			fmt.Fprintf(os.Stderr, "[phase 10] GPU nodegroup: trying AZ %s (%s)...\n", az, capType)
			result, capacityMsg, err := tryGPUNodeGroupInAZ(ctx, eksc, asc, clusterDisplayName, clusterName, nodeRoleARN, azSubnets, gpuLTID, ngAZ, extraTags, labels, st)
			switch {
			case err != nil:
				return nil, err
			case result == azResultSuccess:
				return nil, nil // done
			case result == azResultExhaust:
				tried = append(tried, attempt{az, capacityMsg})
				fmt.Fprintf(os.Stderr,
					"[phase 10] no %s capacity in %s (%s) — deleting + trying next AZ\n",
					ng.InstanceType, az, capacityMsg)
			}
		}
		return tried, nil
	}

	// First sweep: use the declared capacityType.
	firstCapType := ng.CapacityType
	tried, hardErr := sweepOnce(firstCapType)
	if hardErr != nil {
		return hardErr
	}
	if tried == nil {
		// nil tried AND nil err = success on first sweep.
		return nil
	}

	// All first-sweep AZs exhausted. Try on-demand fallback if enabled.
	if firstCapType == "spot" && ng.OnDemandFallback {
		fmt.Fprintf(os.Stderr,
			"[phase 10] GPU nodegroup: all %d AZ(s) exhausted on spot; "+
				"onDemandFallback=true — retrying with on-demand\n", len(tried))
		triedOD, hardErr2 := sweepOnce("on-demand")
		if hardErr2 != nil {
			return hardErr2
		}
		if triedOD == nil {
			return nil // succeeded with on-demand
		}
		tried = append(tried, triedOD...)
	}

	// Everything exhausted — build aggregated error.
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("GPU nodegroup %s: no capacity found in any candidate AZ:\n", ng.Name))
	for _, a := range tried {
		sb.WriteString(fmt.Sprintf("  AZ %s: %s\n", a.az, a.msg))
	}
	return fmt.Errorf("%s", sb.String())
}

// tryGPUNodeGroupInAZ creates the nodegroup pinned to a single AZ's subnet,
// then polls both DescribeNodegroup and the backing ASG's scaling activities.
// Returns:
//   - azResultSuccess: nodegroup reached ACTIVE.
//   - azResultExhaust + capacityMsg: a capacity error was detected with no instance
//     launched; the nodegroup has been deleted and caller should try the next AZ.
//   - azResultError + err: a hard AWS API or unexpected-state error.
func tryGPUNodeGroupInAZ(
	ctx context.Context,
	eksc EKSAPI,
	asc AutoScalingAPI,
	clusterDisplayName, clusterName, nodeRoleARN string,
	azSubnets []string,
	gpuLTID string,
	ng intent.NodeGroupSpec,
	extraTags map[string]string,
	labels map[string]string,
	st *state.State,
) (azSweepResult, string, error) {
	const (
		// perAZTimeout is the backstop per-AZ wait before treating the attempt as
		// a slow failure (real capacity shortfalls are detected within 2-3 minutes
		// via ASG activities long before this fires).
		perAZTimeout = 20 * time.Minute
		// fastPoll is the poll interval during the fast-fail ASG-check phase.
		fastPoll = 15 * time.Second
	)
	upper := strings.ToUpper(ng.Name)
	ngName := clusterName + "-ng-" + ng.Name

	// Idempotency: check whether the nodegroup already exists in a terminal state.
	descOut, err := eksc.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
		ClusterName:   ptr(clusterName),
		NodegroupName: ptr(ngName),
	})
	if err != nil && !isEKSNotFound(err) {
		return azResultError, "", fmt.Errorf("DescribeNodegroup: %w", err)
	}
	if err == nil {
		// Nodegroup already exists — handle current status.
		switch descOut.Nodegroup.Status {
		case ekstypes.NodegroupStatusActive:
			fmt.Fprintf(os.Stderr, "[phase 10] GPU nodegroup %s already ACTIVE, skipping create\n", ngName)
			return azResultSuccess, "", populateNodeGroupState(st, descOut.Nodegroup, upper)
		case ekstypes.NodegroupStatusCreating, ekstypes.NodegroupStatusUpdating:
			// Fall through to the poll loop below.
		case ekstypes.NodegroupStatusCreateFailed, ekstypes.NodegroupStatusDeleteFailed:
			return azResultError, "", fmt.Errorf("nodegroup %s in terminal failure status %s", ngName, descOut.Nodegroup.Status)
		default:
			return azResultError, "", fmt.Errorf("nodegroup %s in unexpected status %s", ngName, descOut.Nodegroup.Status)
		}
	}

	if err != nil {
		// Nodegroup does not yet exist — create it pinned to the single AZ subnet.
		if err2 := createGPUNodegroup(ctx, eksc, clusterDisplayName, clusterName, nodeRoleARN, azSubnets, gpuLTID, ng, extraTags, labels); err2 != nil {
			return azResultError, "", err2
		}
		fmt.Fprintf(os.Stderr, "[phase 10] GPU nodegroup %s: create request sent, watching for ACTIVE or capacity error\n", ngName)
	}

	// Poll loop: poll immediately, then sleep between polls.
	// This means a mock that returns ACTIVE on first call completes instantly
	// without waiting a full poll interval — important for test speed.
	deadline := time.Now().Add(perAZTimeout)
	var asgName string // discovered lazily after first DescribeNodegroup returns Resources
	for time.Now().Before(deadline) {
		out, pollErr := eksc.DescribeNodegroup(ctx, &eks.DescribeNodegroupInput{
			ClusterName:   ptr(clusterName),
			NodegroupName: ptr(ngName),
		})
		if pollErr != nil {
			return azResultError, "", fmt.Errorf("DescribeNodegroup poll: %w", pollErr)
		}
		ngDesc := out.Nodegroup

		switch ngDesc.Status {
		case ekstypes.NodegroupStatusActive:
			fmt.Fprintf(os.Stderr, "[phase 10] GPU nodegroup %s ACTIVE in AZ %s (%s)\n",
				ngName, azSubnets[0], ng.CapacityType)
			return azResultSuccess, "", populateNodeGroupState(st, ngDesc, upper)

		case ekstypes.NodegroupStatusCreateFailed, ekstypes.NodegroupStatusDeleteFailed:
			return azResultError, "", fmt.Errorf("nodegroup %s entered terminal failure status %s", ngName, ngDesc.Status)

		case ekstypes.NodegroupStatusCreating, ekstypes.NodegroupStatusUpdating:
			// Discover the backing ASG (available a short time after create).
			if asgName == "" && asc != nil && ngDesc.Resources != nil && len(ngDesc.Resources.AutoScalingGroups) > 0 {
				if ngDesc.Resources.AutoScalingGroups[0].Name != nil {
					asgName = *ngDesc.Resources.AutoScalingGroups[0].Name
				}
			}
			// Check ASG activities for a capacity error.
			if asgName != "" && asc != nil {
				capacityMsg, noInstance, checkErr := checkASGCapacityError(ctx, asc, asgName)
				if checkErr != nil {
					// Non-fatal: ASG check failure shouldn't abort the whole sweep.
					fmt.Fprintf(os.Stderr, "[phase 10] GPU nodegroup %s: ASG activity check warning: %v\n", ngName, checkErr)
				} else if capacityMsg != "" && noInstance {
					// Capacity exhausted and no instance launched — fast-fail.
					_ = deleteNodeGroupFast(ctx, eksc, clusterName, ngName)
					return azResultExhaust, capacityMsg, nil
				}
			}

		default:
			return azResultError, "", fmt.Errorf("nodegroup %s in unexpected state %s during poll", ngName, ngDesc.Status)
		}

		// Sleep between polls (after the check, not before).
		select {
		case <-ctx.Done():
			return azResultError, "", ctx.Err()
		case <-time.After(fastPoll):
		}
	}
	return azResultError, "", fmt.Errorf("phase10: timeout waiting for GPU nodegroup %s to become ACTIVE", ngName)
}

// createGPUNodegroup issues the EKS CreateNodegroup API call for a GPU nodegroup.
// It is extracted from ensureNodeGroup so the AZ-sweep can call it without the
// idempotency/state-population wrappers that belong to the outer sweep loop.
func createGPUNodegroup(
	ctx context.Context,
	eksc EKSAPI,
	clusterDisplayName, clusterName, nodeRoleARN string,
	azSubnets []string,
	gpuLTID string,
	ng intent.NodeGroupSpec,
	extraTags map[string]string,
	labels map[string]string,
) error {
	if gpuLTID == "" {
		return fmt.Errorf("createGPUNodegroup: gpuLTID is required")
	}
	ngName := clusterName + "-ng-" + ng.Name

	if ng.DiskSize > 1<<30 || ng.DesiredSize > 1<<30 || ng.MinSize > 1<<30 || ng.MaxSize > 1<<30 {
		return fmt.Errorf("nodegroup %s: scaling/disk value too large", ngName)
	}
	desiredSize := int32(ng.DesiredSize) // #nosec G115 -- bounded above
	minSize := int32(ng.MinSize)         // #nosec G115 -- bounded above
	maxSize := int32(ng.MaxSize)         // #nosec G115 -- bounded above

	capacityType := ekstypes.CapacityTypesOnDemand
	if ng.CapacityType == "spot" {
		capacityType = ekstypes.CapacityTypesSpot
	}

	k8sLabels := map[string]string{
		"awsbnkctl.io/cluster": clusterDisplayName,
		"awsbnkctl.io/gpu":     "true",
	}
	for k, v := range ng.Labels {
		k8sLabels[k] = v
	}

	var eksTaints []ekstypes.Taint
	for _, t := range ng.Taints {
		taint := ekstypes.Taint{Key: ptr(t.Key), Value: ptr(t.Value)}
		switch t.Effect {
		case "NoSchedule":
			taint.Effect = ekstypes.TaintEffectNoSchedule
		case "NoExecute":
			taint.Effect = ekstypes.TaintEffectNoExecute
		case "PreferNoSchedule":
			taint.Effect = ekstypes.TaintEffectPreferNoSchedule
		}
		eksTaints = append(eksTaints, taint)
	}

	ngTags := tags.EKSTags(
		tags.Required(clusterDisplayName, tags.CompEKSNodeGroup),
		extraTags,
		labels,
	)

	ltVersion := "$Latest"
	input := &eks.CreateNodegroupInput{
		ClusterName:   ptr(clusterName),
		NodegroupName: ptr(ngName),
		NodeRole:      ptr(nodeRoleARN),
		Subnets:       azSubnets,
		AmiType:       ekstypes.AMITypesAl2023X8664Nvidia,
		CapacityType:  capacityType,
		InstanceTypes: []string{ng.InstanceType},
		ScalingConfig: &ekstypes.NodegroupScalingConfig{
			DesiredSize: int32Ptr(desiredSize),
			MinSize:     int32Ptr(minSize),
			MaxSize:     int32Ptr(maxSize),
		},
		Taints: eksTaints,
		Labels: k8sLabels,
		Tags:   ngTags,
		LaunchTemplate: &ekstypes.LaunchTemplateSpecification{
			Id:      ptr(gpuLTID),
			Version: ptr(ltVersion),
		},
	}
	_, err := eksc.CreateNodegroup(ctx, input)
	if err != nil {
		return fmt.Errorf("CreateNodegroup %s: %w", ngName, err)
	}
	return nil
}

// checkASGCapacityError inspects the most recent scaling activities for the given
// ASG and reports whether a capacity error is present and no instance has launched.
// Returns (capacityMsg, noInstance, error).
func checkASGCapacityError(ctx context.Context, asc AutoScalingAPI, asgName string) (string, bool, error) {
	// Check ASG activities for capacity error messages.
	actOut, err := asc.DescribeScalingActivities(ctx, &autoscaling.DescribeScalingActivitiesInput{
		AutoScalingGroupName: ptr(asgName),
	})
	if err != nil {
		return "", false, fmt.Errorf("DescribeScalingActivities %s: %w", asgName, err)
	}

	var capacityMsg string
	for _, act := range actOut.Activities {
		if act.StatusCode != astypes.ScalingActivityStatusCodeFailed {
			continue
		}
		msg := ""
		if act.StatusMessage != nil {
			msg = *act.StatusMessage
		}
		for _, substr := range capacityErrorSubstrings {
			if strings.Contains(msg, substr) {
				capacityMsg = msg
				break
			}
		}
		if capacityMsg != "" {
			break
		}
	}
	if capacityMsg == "" {
		return "", false, nil
	}

	// Confirm no instance has actually launched (ASG instance count == 0).
	groupOut, err := asc.DescribeAutoScalingGroups(ctx, &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []string{asgName},
	})
	if err != nil {
		return "", false, fmt.Errorf("DescribeAutoScalingGroups %s: %w", asgName, err)
	}
	noInstance := len(groupOut.AutoScalingGroups) == 0 || len(groupOut.AutoScalingGroups[0].Instances) == 0
	return capacityMsg, noInstance, nil
}

// deleteNodeGroupFast issues a DeleteNodegroup and waits for deletion as part
// of the AZ-sweep fail-fast path. Errors are logged but not returned — the
// sweep moves on regardless.
func deleteNodeGroupFast(ctx context.Context, eksc EKSAPI, clusterName, ngName string) error {
	fmt.Fprintf(os.Stderr, "[phase 10] GPU nodegroup: deleting %s (capacity exhausted)\n", ngName)
	_, err := eksc.DeleteNodegroup(ctx, &eks.DeleteNodegroupInput{
		ClusterName:   ptr(clusterName),
		NodegroupName: ptr(ngName),
	})
	if err != nil {
		if isEKSNotFound(err) {
			return nil
		}
		return fmt.Errorf("DeleteNodegroup %s: %w", ngName, err)
	}
	return waitNodeGroupDeleted(ctx, eksc, clusterName, ngName)
}
