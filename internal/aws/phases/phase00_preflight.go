package phases

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/sts"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// HostDeviceMinENIs is the minimum number of network interfaces an EC2
// instance type must support for the host-device data-plane pattern.
//
// The TMM-target node attaches: primary (device 0) + EKS CNI (device 1,
// auto-allocated by the aws-node DaemonSet for pod networking) + BNK
// INTERNAL (device 2, ens7) + BNK EXTERNAL (device 3, ens8) = 4 ENIs.
//
// Smaller instance types (t3.medium/large, m5.large) cap at 3 ENIs and
// will fail phase 17 with AttachmentLimitExceeded when the second TMM
// secondary tries to attach. m5.xlarge is the minimum viable instance.
const HostDeviceMinENIs = 4

// HostDeviceMinVCPUs is the minimum vCPU count required for the host-device
// pattern. BNK 2.3 Small profile requires at least 16 vCPUs on the TMM node.
const HostDeviceMinVCPUs = 16

// HostDeviceMinMemoryMiB is the minimum memory (MiB) required for the
// host-device pattern. BNK 2.3 Small profile requires at least 64 GiB.
const HostDeviceMinMemoryMiB = 65536

// HostDeviceMinDesiredSize is the minimum node group desired size for the
// host-device pattern. dSSM requires a quorum of ≥3 nodes.
const HostDeviceMinDesiredSize = 3

// Phase00Preflight runs the pre-flight checks before any provisioning begins:
//   - SSO sentinel check (hard-exits if auth failed during a previous API call)
//   - sts:GetCallerIdentity to verify credentials are live
//   - For pattern=host-device: validates that the node group instance type
//     supports at least HostDeviceMinENIs ENIs (queries ec2:DescribeInstanceTypes)
//   - Ensures .awsbnkctl/<cluster>/ exists and persists CLUSTER_NAME + AWS_REGION
//
// In dry-run mode the instance-type API call is skipped (no AWS mutations)
// and state values are set in memory only.
func Phase00Preflight(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintf(os.Stderr, "[phase 00] preflight: cluster=%s region=%s\n",
		cl.Metadata.Name, cl.Metadata.Region)

	// Verify credentials are live.
	out, err := clients.STS.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return fmt.Errorf("phase00: sts:GetCallerIdentity: %w", err)
	}
	account := ""
	if out.Account != nil {
		account = *out.Account
	}
	fmt.Fprintf(os.Stderr, "[phase 00] authenticated: account=%s\n", account)

	// Validate instance type + node group capacity for host-device pattern.
	if !dryRun && cl.Pattern == "host-device" {
		if err := checkHostDeviceCapacity(ctx, cl, clients.EC2); err != nil {
			return fmt.Errorf("phase00: %w", err)
		}
	}

	// Ensure the state directory exists and persist the cluster name + region.
	st.Set("CLUSTER_NAME", cl.Metadata.Name)
	st.Set("AWS_REGION", cl.Metadata.Region)
	if !dryRun {
		if err := st.Save(); err != nil {
			return fmt.Errorf("phase00: saving state: %w", err)
		}
	}

	fmt.Fprintf(os.Stderr, "[phase 00] preflight OK\n")
	return nil
}

// checkHostDeviceCapacity validates that the first node group meets all
// host-device pattern minimums (ENIs, vCPUs, memory, desiredSize) before any
// AWS resource creation. All failures are aggregated so the operator sees the
// full picture in one shot rather than discovering them sequentially.
//
// See docs/audits/slice-12-cold-start-audit.md §5 for rationale.
func checkHostDeviceCapacity(ctx context.Context, cl *intent.Cluster, ec2c EC2API) error {
	if cl.ClusterSpec == nil || len(cl.ClusterSpec.NodeGroups) == 0 {
		return nil
	}
	ng := cl.ClusterSpec.NodeGroups[0]
	instanceType := ng.InstanceType
	if instanceType == "" {
		return nil
	}

	out, err := ec2c.DescribeInstanceTypes(ctx, &ec2.DescribeInstanceTypesInput{
		InstanceTypes: []ec2types.InstanceType{ec2types.InstanceType(instanceType)},
	})
	if err != nil {
		return fmt.Errorf("ec2:DescribeInstanceTypes %s: %w", instanceType, err)
	}
	if len(out.InstanceTypes) == 0 {
		return fmt.Errorf("ec2:DescribeInstanceTypes returned no info for %q", instanceType)
	}
	info := out.InstanceTypes[0]

	var errs []error

	// ENI check.
	if info.NetworkInfo == nil || info.NetworkInfo.MaximumNetworkInterfaces == nil {
		errs = append(errs, fmt.Errorf("ec2:DescribeInstanceTypes %q: missing network info", instanceType))
	} else {
		maxENIs := int(aws.ToInt32(info.NetworkInfo.MaximumNetworkInterfaces))
		if maxENIs < HostDeviceMinENIs {
			errs = append(errs, fmt.Errorf(
				"pattern=host-device requires an EC2 instance type with at least %d network interfaces (primary + EKS CNI + 2 BNK secondaries); "+
					"%q supports only %d. Bump cluster.nodeGroups[0].instanceType to m5.xlarge or larger",
				HostDeviceMinENIs, instanceType, maxENIs,
			))
		}
	}

	// vCPU check (BNK 2.3 Small minimum).
	if info.VCpuInfo != nil && info.VCpuInfo.DefaultVCpus != nil {
		vcpus := int(*info.VCpuInfo.DefaultVCpus)
		if vcpus < HostDeviceMinVCPUs {
			errs = append(errs, fmt.Errorf(
				"pattern=host-device requires at least %d vCPUs (BNK 2.3 Small minimum); %q has %d vCPUs",
				HostDeviceMinVCPUs, instanceType, vcpus,
			))
		}
	}

	// Memory check (BNK 2.3 Small minimum: 64 GiB).
	if info.MemoryInfo != nil && info.MemoryInfo.SizeInMiB != nil {
		memMiB := *info.MemoryInfo.SizeInMiB
		if memMiB < HostDeviceMinMemoryMiB {
			errs = append(errs, fmt.Errorf(
				"pattern=host-device requires at least %d MiB memory (64 GiB, BNK 2.3 Small minimum); %q has %d MiB",
				HostDeviceMinMemoryMiB, instanceType, memMiB,
			))
		}
	}

	// DesiredSize check (dSSM quorum requires ≥3).
	if ng.DesiredSize > 0 && ng.DesiredSize < HostDeviceMinDesiredSize {
		errs = append(errs, fmt.Errorf(
			"pattern=host-device requires cluster.nodeGroups[0].desiredSize >= %d (dSSM quorum requires ≥3); got %d",
			HostDeviceMinDesiredSize, ng.DesiredSize,
		))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	fmt.Fprintf(os.Stderr, "[phase 00] instance-type %s OK for host-device: %d ENIs, desiredSize=%d\n",
		instanceType, int(aws.ToInt32(info.NetworkInfo.MaximumNetworkInterfaces)), ng.DesiredSize)
	return nil
}
