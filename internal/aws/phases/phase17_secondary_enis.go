package phases

import (
	"context"
	"fmt"
	"os"
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
	// eniDetachPollInterval is the interval between detach status polls.
	eniDetachPollInterval = 5 * time.Second
	// eniDetachTimeout is the maximum time to wait for an ENI to detach.
	eniDetachTimeout = 3 * time.Minute
)

// Phase17SecondaryENIs creates, tags, and attaches the two TMM data-plane ENIs
// to the TMM-target instance.
//
// ENI assignment:
//   - INTERNAL_ENI: BNK_INT_SUBNET, device-index 2 → ens7, tag f5-cne-device=ens7
//   - EXTERNAL_ENI: BNK_EXT_SUBNET, device-index 3 → ens8, tag f5-cne-device=ens8
//
// Tags: standard awsbnkctl:* scheme + f5-cne-device=<ifname> + node.k8s.amazonaws.com/no_manage=true.
// Source/dest check disabled (required for TMM routing).
// Idempotent: reads INTERNAL_ENI/EXTERNAL_ENI from state first; falls back to
// tag-lookup by awsbnkctl:cluster + awsbnkctl:component.
//
// Dry-run: sets placeholder state values, no AWS mutations.
// SSO sentinel: CheckAuthOrDie at entry.
func Phase17SecondaryENIs(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 17] secondary ENIs: cluster=%s\n", name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 17] dry-run: would create + attach INTERNAL_ENI (ens7, idx=2) and EXTERNAL_ENI (ens8, idx=3)")
		st.Set("INTERNAL_ENI", "eni-dry-run-int")
		st.Set("EXTERNAL_ENI", "eni-dry-run-ext")
		st.Set("INTERNAL_ENI_MAC", "02:00:00:00:00:02")
		st.Set("EXTERNAL_ENI_MAC", "02:00:00:00:00:03")
		return nil
	}

	instanceID := st.Get("TMM_INSTANCE_ID")
	if instanceID == "" {
		return fmt.Errorf("phase17: TMM_INSTANCE_ID not in state (run phase16 first)")
	}
	intSubnet := st.Get("BNK_INT_SUBNET")
	if intSubnet == "" {
		return fmt.Errorf("phase17: BNK_INT_SUBNET not in state (run phase03 first)")
	}
	extSubnet := st.Get("BNK_EXT_SUBNET")
	if extSubnet == "" {
		return fmt.Errorf("phase17: BNK_EXT_SUBNET not in state (run phase03 first)")
	}
	sgID := st.Get("SG_BNK_DATA")
	if sgID == "" {
		return fmt.Errorf("phase17: SG_BNK_DATA not in state (run phase07 first)")
	}

	// Internal ENI: device-index 2 → ens7.
	intENI, err := ensureSecondaryENI(ctx, clients.EC2, name, intSubnet, sgID,
		tags.CompENIInternal, "ens7", cl.Tags, cl.Metadata.Labels, st, "INTERNAL_ENI")
	if err != nil {
		return fmt.Errorf("phase17: internal ENI: %w", err)
	}
	st.Set("INTERNAL_ENI", intENI)

	// Capture MAC for internal ENI (always — covers state-hit and tag-hit paths).
	intMAC, err := eniMAC(ctx, clients.EC2, intENI)
	if err != nil {
		return fmt.Errorf("phase17: capturing internal ENI MAC: %w", err)
	}
	st.Set("INTERNAL_ENI_MAC", intMAC)
	fmt.Fprintf(os.Stderr, "[phase 17] INTERNAL_ENI_MAC=%s\n", intMAC)

	// External ENI: device-index 3 → ens8.
	extENI, err := ensureSecondaryENI(ctx, clients.EC2, name, extSubnet, sgID,
		tags.CompENIExternal, "ens8", cl.Tags, cl.Metadata.Labels, st, "EXTERNAL_ENI")
	if err != nil {
		return fmt.Errorf("phase17: external ENI: %w", err)
	}
	st.Set("EXTERNAL_ENI", extENI)

	// Capture MAC for external ENI (always — covers state-hit and tag-hit paths).
	extMAC, err := eniMAC(ctx, clients.EC2, extENI)
	if err != nil {
		return fmt.Errorf("phase17: capturing external ENI MAC: %w", err)
	}
	st.Set("EXTERNAL_ENI_MAC", extMAC)
	fmt.Fprintf(os.Stderr, "[phase 17] EXTERNAL_ENI_MAC=%s\n", extMAC)

	// Attach internal at device-index 2.
	if err := attachENIIfNeeded(ctx, clients.EC2, intENI, instanceID, 2); err != nil {
		return fmt.Errorf("phase17: attaching internal ENI %s: %w", intENI, err)
	}
	// Attach external at device-index 3.
	if err := attachENIIfNeeded(ctx, clients.EC2, extENI, instanceID, 3); err != nil {
		return fmt.Errorf("phase17: attaching external ENI %s: %w", extENI, err)
	}
	// Phase 17c (iface-discovery) runs after this phase and resolves the actual
	// Linux ifname + PCI bus address for each ENI by MAC matching on the node.
	// The hardcoded ens7/ens8 names are now FALLBACK constants only — phase 17c
	// provides the authoritative values. See constants_hostdevice.go.
	fmt.Fprintf(os.Stderr, "[phase 17] MACs captured; phase 17c will resolve ifname+PCI on node\n")

	// Assign TMM SelfIPs as secondary private IPs on each ENI.
	// Per F5 Multi-AZ PDF p.9: AWS won't route SelfIPs to the ENI unless they
	// are also listed as secondary IPs on the ENI. Without this, F5SPKVlan
	// SelfIP plumbing silently fails (Phase 23b applies the F5SPKVlan CR but
	// the cne-controller can't program the data path until the IPs are on the
	// ENI). aws-gpu-setup mirrors this in up.sh assign_selfip after attach.
	if c := cl.Network.DataPath; c != nil && c.SelfIPs != nil {
		if c.SelfIPs.External != "" {
			if err := assignSelfIPIfNeeded(ctx, clients.EC2, extENI, c.SelfIPs.External); err != nil {
				return fmt.Errorf("phase17: assigning external SelfIP %s to %s: %w", c.SelfIPs.External, extENI, err)
			}
			st.Set("TMM_EXT_SELFIP", c.SelfIPs.External)
		}
		if c.SelfIPs.Internal != "" {
			if err := assignSelfIPIfNeeded(ctx, clients.EC2, intENI, c.SelfIPs.Internal); err != nil {
				return fmt.Errorf("phase17: assigning internal SelfIP %s to %s: %w", c.SelfIPs.Internal, intENI, err)
			}
			st.Set("TMM_INT_SELFIP", c.SelfIPs.Internal)
		}
		if c.SelfIPs.PrefixLen > 0 {
			st.Set("TMM_SELFIP_PREFIXLEN", fmt.Sprintf("%d", c.SelfIPs.PrefixLen))
		}
	}

	return st.Save()
}

// assignSelfIPIfNeeded assigns a secondary private IP to an ENI. Idempotent:
// describes first and skips if the IP is already in the assigned list.
// Uses AllowReassignment=true to be safe if the IP was previously assigned
// to a different ENI (e.g. orphaned by a partial down).
func assignSelfIPIfNeeded(ctx context.Context, ec2c EC2API, eniID, selfIP string) error {
	out, err := ec2c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eniID},
	})
	if err != nil {
		return fmt.Errorf("DescribeNetworkInterfaces %s: %w", eniID, err)
	}
	if len(out.NetworkInterfaces) == 0 {
		return fmt.Errorf("ENI %s not found", eniID)
	}
	for _, ip := range out.NetworkInterfaces[0].PrivateIpAddresses {
		if ip.PrivateIpAddress != nil && *ip.PrivateIpAddress == selfIP {
			fmt.Fprintf(os.Stderr, "[phase 17] SelfIP %s already assigned to %s\n", selfIP, eniID)
			return nil
		}
	}
	allowReassignment := true
	_, err = ec2c.AssignPrivateIpAddresses(ctx, &ec2.AssignPrivateIpAddressesInput{
		NetworkInterfaceId: ptr(eniID),
		PrivateIpAddresses: []string{selfIP},
		AllowReassignment:  &allowReassignment,
	})
	if err != nil {
		return fmt.Errorf("ec2:AssignPrivateIpAddresses %s: %w", eniID, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17] assigned SelfIP %s to %s (per F5 Multi-AZ PDF p.9)\n", selfIP, eniID)
	return nil
}

// Phase17SecondaryENIsDown detaches and deletes the TMM secondary ENIs.
// Tolerates NotFound and AlreadyDetached.
func Phase17SecondaryENIsDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 17 down] secondary ENIs: cluster=%s\n", name)

	for _, key := range []string{"EXTERNAL_ENI", "INTERNAL_ENI"} {
		eniID := st.Get(key)
		if eniID == "" {
			// Tag-discovery fallback.
			comp := tags.CompENIInternal
			if key == "EXTERNAL_ENI" {
				comp = tags.CompENIExternal
			}
			eniID = lookupENIByTag(ctx, clients.EC2, name, comp)
		}
		if eniID == "" {
			fmt.Fprintf(os.Stderr, "[phase 17 down] %s not found, skipping\n", key)
			continue
		}
		fmt.Fprintf(os.Stderr, "[phase 17 down] detaching + deleting %s (%s)\n", key, eniID)
		if err := detachAndDeleteENI(ctx, clients.EC2, eniID); err != nil {
			return fmt.Errorf("phase17 down: %s: %w", key, err)
		}
		st.Set(key, "")
	}
	st.Set("INTERNAL_ENI_MAC", "")
	st.Set("EXTERNAL_ENI_MAC", "")
	return st.Save()
}

// ensureSecondaryENI looks up an ENI by state key first, then by tag, then
// creates it. Also disables source/dest check and applies extra tags. Returns the ENI ID.
func ensureSecondaryENI(ctx context.Context, ec2c EC2API, clusterName, subnetID, sgID,
	component, ifname string, extraTags, labels map[string]string,
	st *state.State, stateKey string) (string, error) {

	// Check state.
	if eniID := st.Get(stateKey); eniID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17] %s found in state: %s\n", stateKey, eniID)
		return eniID, nil
	}

	// Tag-discovery fallback.
	if eniID := lookupENIByTag(ctx, ec2c, clusterName, component); eniID != "" {
		fmt.Fprintf(os.Stderr, "[phase 17] %s found via tags: %s\n", stateKey, eniID)
		return eniID, nil
	}

	// Create.
	eniName := clusterName + "-tmm-" + ifname
	eniTags := tags.Merge(
		tags.Required(clusterName, component),
		map[string]string{
			tags.KeyName:                       eniName,
			"f5-cne-device":                    ifname,
			"node.k8s.amazonaws.com/no_manage": "true",
		},
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
		return "", fmt.Errorf("ec2:CreateNetworkInterface %s: %w", component, err)
	}
	eniID := *out.NetworkInterface.NetworkInterfaceId
	fmt.Fprintf(os.Stderr, "[phase 17] created ENI %s (%s, ifname=%s)\n", eniID, component, ifname)

	// Disable source/dest check.
	if _, err := ec2c.ModifyNetworkInterfaceAttribute(ctx, &ec2.ModifyNetworkInterfaceAttributeInput{
		NetworkInterfaceId: ptr(eniID),
		SourceDestCheck:    &ec2types.AttributeBooleanValue{Value: boolPtr(false)},
	}); err != nil {
		return "", fmt.Errorf("ec2:ModifyNetworkInterfaceAttribute --no-source-dest-check %s: %w", eniID, err)
	}

	return eniID, nil
}

// attachENIIfNeeded checks the current attachment state and attaches if not
// already attached to the target instance at the given device index.
func attachENIIfNeeded(ctx context.Context, ec2c EC2API, eniID, instanceID string, deviceIndex int) error {
	out, err := ec2c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eniID},
	})
	if err != nil {
		return fmt.Errorf("ec2:DescribeNetworkInterfaces %s: %w", eniID, err)
	}
	if len(out.NetworkInterfaces) == 0 {
		return fmt.Errorf("ENI %s not found", eniID)
	}
	att := out.NetworkInterfaces[0].Attachment
	if att != nil && att.InstanceId != nil && *att.InstanceId == instanceID {
		fmt.Fprintf(os.Stderr, "[phase 17] ENI %s already attached to %s (device-index=%d)\n",
			eniID, instanceID, deviceIndex)
		return nil
	}

	devIdx := int32(deviceIndex) // #nosec G115 -- device index is always 2 or 3
	if _, err := ec2c.AttachNetworkInterface(ctx, &ec2.AttachNetworkInterfaceInput{
		NetworkInterfaceId: ptr(eniID),
		InstanceId:         ptr(instanceID),
		DeviceIndex:        &devIdx,
	}); err != nil {
		return fmt.Errorf("ec2:AttachNetworkInterface %s device-index=%d: %w", eniID, deviceIndex, err)
	}
	fmt.Fprintf(os.Stderr, "[phase 17] attached ENI %s to %s at device-index=%d\n", eniID, instanceID, deviceIndex)
	return nil
}

// detachAndDeleteENI detaches (if attached) and then deletes an ENI.
// Tolerates NotFound and AlreadyDetached.
func detachAndDeleteENI(ctx context.Context, ec2c EC2API, eniID string) error {
	// Describe to find attachment.
	descOut, err := ec2c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eniID},
	})
	if err != nil {
		if err := ignoreNotFound(err); err == nil {
			return nil // already gone
		}
		return fmt.Errorf("ec2:DescribeNetworkInterfaces %s: %w", eniID, err)
	}
	if len(descOut.NetworkInterfaces) == 0 {
		return nil // already gone
	}

	att := descOut.NetworkInterfaces[0].Attachment
	if att != nil && att.AttachmentId != nil {
		fmt.Fprintf(os.Stderr, "[phase 17 down] detaching ENI %s (attachment %s)\n", eniID, *att.AttachmentId)
		force := true
		_, err := ec2c.DetachNetworkInterface(ctx, &ec2.DetachNetworkInterfaceInput{
			AttachmentId: att.AttachmentId,
			Force:        &force,
		})
		if err != nil {
			if err := ignoreNotFound(err); err == nil {
				// attachment already gone
			} else {
				return fmt.Errorf("ec2:DetachNetworkInterface %s: %w", eniID, err)
			}
		}
		// Wait for detached status.
		if err := waitENIDetached(ctx, ec2c, eniID); err != nil {
			return err
		}
	}

	fmt.Fprintf(os.Stderr, "[phase 17 down] deleting ENI %s\n", eniID)
	_, err = ec2c.DeleteNetworkInterface(ctx, &ec2.DeleteNetworkInterfaceInput{
		NetworkInterfaceId: ptr(eniID),
	})
	if err := ignoreNotFound(err); err != nil {
		return fmt.Errorf("ec2:DeleteNetworkInterface %s: %w", eniID, err)
	}
	return nil
}

// waitENIDetached polls until the ENI status is "available" (detached).
func waitENIDetached(ctx context.Context, ec2c EC2API, eniID string) error {
	deadline := time.Now().Add(eniDetachTimeout)
	for time.Now().Before(deadline) {
		out, err := ec2c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
			NetworkInterfaceIds: []string{eniID},
		})
		if err != nil {
			if err := ignoreNotFound(err); err == nil {
				return nil // gone
			}
			return err
		}
		if len(out.NetworkInterfaces) == 0 {
			return nil
		}
		if out.NetworkInterfaces[0].Status == ec2types.NetworkInterfaceStatusAvailable {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(eniDetachPollInterval):
		}
	}
	return fmt.Errorf("timeout waiting for ENI %s to detach", eniID)
}

// eniMAC describes a single ENI and returns its MAC address in lower-case
// (e.g. "0a:1b:2c:3d:4e:5f"). Called after ensureSecondaryENI on ALL paths
// (state-hit, tag-hit, freshly-created) so the MAC is always captured.
func eniMAC(ctx context.Context, ec2c EC2API, eniID string) (string, error) {
	out, err := ec2c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		NetworkInterfaceIds: []string{eniID},
	})
	if err != nil {
		return "", fmt.Errorf("DescribeNetworkInterfaces %s: %w", eniID, err)
	}
	if len(out.NetworkInterfaces) == 0 || out.NetworkInterfaces[0].MacAddress == nil ||
		*out.NetworkInterfaces[0].MacAddress == "" {
		return "", fmt.Errorf("ENI %s: MAC address not available", eniID)
	}
	return strings.ToLower(*out.NetworkInterfaces[0].MacAddress), nil
}

// lookupENIByTag looks up a network interface by cluster + component tags.
// Returns "" if not found or on error.
func lookupENIByTag(ctx context.Context, ec2c EC2API, clusterName, component string) string {
	out, err := ec2c.DescribeNetworkInterfaces(ctx, &ec2.DescribeNetworkInterfacesInput{
		Filters: []ec2types.Filter{
			tags.ClusterFilter(clusterName),
			tags.ComponentFilter(component),
		},
	})
	if err != nil || len(out.NetworkInterfaces) == 0 {
		return ""
	}
	if out.NetworkInterfaces[0].NetworkInterfaceId == nil {
		return ""
	}
	return *out.NetworkInterfaces[0].NetworkInterfaceId
}
