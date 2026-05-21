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
// Slice 7 introduces a Launch Template (LT) with MIME-multipart UserData
// containing sysctl + udev rules required for BNK host-device ENI bring-up.
// The LT is created once and the node group is bound via LaunchTemplate{Id, Version=$Latest}.
// EKS UpdateNodegroupConfig does NOT accept retroactive LT addition — slice 6→7
// upgrades require down + re-up per D-007.
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

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 10] dry-run: would create launch template %s\n", ltName)
		st.Set("LT_ID", "lt-dry-run")
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

	// Ensure the Launch Template exists.
	ltID, err := ensureLaunchTemplate(ctx, clients.EC2, name, ltName, cl.Tags, cl.Metadata.Labels)
	if err != nil {
		return fmt.Errorf("phase10: launch template: %w", err)
	}
	st.Set("LT_ID", ltID)

	for _, ng := range cl.ClusterSpec.NodeGroups {
		if err := ensureNodeGroup(ctx, clients.EKS, name, clusterName, nodeRoleARN, publicSubnets, ltID, ng, cl.Tags, cl.Metadata.Labels, st); err != nil {
			return fmt.Errorf("phase10: node group %s: %w", ng.Name, err)
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

	// Delete the Launch Template.
	ltID := st.Get("LT_ID")
	ltName := name + "-bnk-lt"
	if ltID == "" {
		// Name-based fallback.
		ltID = lookupLTByName(ctx, clients.EC2, ltName)
	}
	if ltID != "" {
		fmt.Fprintf(os.Stderr, "[phase 10 down] deleting launch template %s\n", ltID)
		_, err := clients.EC2.DeleteLaunchTemplate(ctx, &ec2.DeleteLaunchTemplateInput{
			LaunchTemplateId: ptr(ltID),
		})
		if err := ignoreNotFound(err); err != nil {
			return fmt.Errorf("phase10 down: DeleteLaunchTemplate %s: %w", ltID, err)
		}
	}
	st.Set("LT_ID", "")

	return st.Save()
}

// --- helpers ---

func ensureNodeGroup(
	ctx context.Context,
	eksc EKSAPI,
	clusterDisplayName, clusterName, nodeRoleARN string,
	publicSubnets []string,
	ltID string,
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
	// docs/POST_TERRAFORM_DIRECTION.md §3 / D-006). The `:` form used for
	// AWS resource tags would cause an EKS InvalidParameterException
	// (regex '([A-Za-z0-9][-A-Za-z0-9_.]*)?[A-Za-z0-9]').
	k8sLabels := map[string]string{
		"awsbnkctl.io/cluster": clusterDisplayName,
	}
	for k, v := range ng.Labels {
		k8sLabels[k] = v
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

	// Bind the node group to the Launch Template via id=$LT_ID,version=$Latest.
	// DiskSize is NOT set when using a launch template (EKS rejects the combination).
	ltVersion := "$Latest"
	_, err = eksc.CreateNodegroup(ctx, &eks.CreateNodegroupInput{
		ClusterName:   ptr(clusterName),
		NodegroupName: ptr(ngName),
		NodeRole:      ptr(nodeRoleARN),
		Subnets:       publicSubnets,
		AmiType:       ekstypes.AMITypesAl2X8664,
		InstanceTypes: []string{ng.InstanceType},
		ScalingConfig: &ekstypes.NodegroupScalingConfig{
			DesiredSize: int32Ptr(desiredSize),
			MinSize:     int32Ptr(minSize),
			MaxSize:     int32Ptr(maxSize),
		},
		LaunchTemplate: &ekstypes.LaunchTemplateSpecification{
			Id:      ptr(ltID),
			Version: ptr(ltVersion),
		},
		Labels: k8sLabels,
		Tags:   ngTags,
	})
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
