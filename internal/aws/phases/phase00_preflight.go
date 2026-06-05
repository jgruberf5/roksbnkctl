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

// bnkMinENIs returns the minimum number of network interfaces an EC2 instance
// type must support for a cluster's BNK interface pattern: the primary ENI plus
// one TMM data-plane secondary per interface.
//
//	external-only / sriov-external: 2 (primary + external secondary)
//	dual-interface:                 3 (primary + external + internal secondaries)
//
// No dedicated EKS-CNI secondary is counted: VPC CNI prefix delegation
// (Phase 08b) serves pod IPs from /28 prefixes on the primary ENI, so the CNI
// stays on the primary and never claims a secondary. An instance type below the
// floor fails phase 17 with AttachmentLimitExceeded when the last TMM secondary
// tries to attach; m5.xlarge is the minimum viable instance for either floor.
func bnkMinENIs(cl *intent.Cluster) int {
	if cl.HasInternalInterface() {
		return 3
	}
	return 2
}

// HostDeviceMinVCPUs is the minimum vCPU count required for a BNK pattern.
// BNK 2.3 Small profile requires at least 16 vCPUs on the TMM node.
const HostDeviceMinVCPUs = 16

// HostDeviceMinMemoryMiB is the minimum memory (MiB) required for a BNK pattern.
// BNK 2.3 Small profile requires at least 64 GiB.
const HostDeviceMinMemoryMiB = 65536

// HostDeviceMinDesiredSize is the minimum node group desired size for a BNK
// pattern. dSSM requires a quorum of ≥3 nodes.
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

	// Validate instance type + node group capacity for BNK interface patterns.
	if !dryRun && cl.IsBNKPattern() {
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

// checkHostDeviceCapacity validates that the first node group meets all BNK
// pattern minimums (ENIs, vCPUs, memory, desiredSize) before any AWS resource
// creation. The ENI floor is pattern-dependent (see bnkMinENIs); vCPU/memory/
// desiredSize are shared across every BNK pattern. All failures are aggregated
// so the operator sees the full picture in one shot rather than discovering
// them sequentially.
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

	// ENI check (pattern-dependent floor).
	minENIs := bnkMinENIs(cl)
	if info.NetworkInfo == nil || info.NetworkInfo.MaximumNetworkInterfaces == nil {
		errs = append(errs, fmt.Errorf("ec2:DescribeInstanceTypes %q: missing network info", instanceType))
	} else {
		maxENIs := int(aws.ToInt32(info.NetworkInfo.MaximumNetworkInterfaces))
		if maxENIs < minENIs {
			errs = append(errs, fmt.Errorf(
				"pattern %s requires an EC2 instance type with at least %d network interfaces (primary + %d TMM data-plane secondary); "+
					"%q supports only %d. Bump cluster.nodeGroups[0].instanceType to m5.xlarge or larger",
				cl.Pattern, minENIs, minENIs-1, instanceType, maxENIs,
			))
		}
	}

	// vCPU check (BNK 2.3 Small minimum).
	if info.VCpuInfo != nil && info.VCpuInfo.DefaultVCpus != nil {
		vcpus := int(*info.VCpuInfo.DefaultVCpus)
		if vcpus < HostDeviceMinVCPUs {
			errs = append(errs, fmt.Errorf(
				"pattern %s requires at least %d vCPUs (BNK 2.3 Small minimum); %q has %d vCPUs",
				cl.Pattern, HostDeviceMinVCPUs, instanceType, vcpus,
			))
		}
	}

	// Memory check (BNK 2.3 Small minimum: 64 GiB).
	if info.MemoryInfo != nil && info.MemoryInfo.SizeInMiB != nil {
		memMiB := *info.MemoryInfo.SizeInMiB
		if memMiB < HostDeviceMinMemoryMiB {
			errs = append(errs, fmt.Errorf(
				"pattern %s requires at least %d MiB memory (64 GiB, BNK 2.3 Small minimum); %q has %d MiB",
				cl.Pattern, HostDeviceMinMemoryMiB, instanceType, memMiB,
			))
		}
	}

	// DesiredSize check (dSSM quorum requires ≥3).
	if ng.DesiredSize > 0 && ng.DesiredSize < HostDeviceMinDesiredSize {
		errs = append(errs, fmt.Errorf(
			"pattern %s requires cluster.nodeGroups[0].desiredSize >= %d (dSSM quorum requires ≥3); got %d",
			cl.Pattern, HostDeviceMinDesiredSize, ng.DesiredSize,
		))
	}

	if len(errs) > 0 {
		return errors.Join(errs...)
	}

	fmt.Fprintf(os.Stderr, "[phase 00] instance-type %s OK for pattern %s: %d ENIs (min %d), desiredSize=%d\n",
		instanceType, cl.Pattern, int(aws.ToInt32(info.NetworkInfo.MaximumNetworkInterfaces)), minENIs, ng.DesiredSize)
	return nil
}
