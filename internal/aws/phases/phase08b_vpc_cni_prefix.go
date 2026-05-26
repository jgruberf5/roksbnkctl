package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"
	"github.com/aws/smithy-go"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

const (
	vpcCNIAddonName        = "vpc-cni"
	vpcCNIPrefixConfigJSON = `{"env":{"ENABLE_PREFIX_DELEGATION":"true","WARM_PREFIX_TARGET":"1","WARM_ENI_TARGET":"0"}}`
	// vpcCNIAddonActiveTimeout is intentionally short — the addon is configured
	// before the node group exists, so it may never reach ACTIVE during phase08b.
	// The poll is best-effort: non-ACTIVE at deadline is a warning, not an error
	// (see pollVPCCNIAddon comment).
	vpcCNIAddonActiveTimeout = 2 * time.Minute
	vpcCNIAddonPollInterval  = 10 * time.Second
)

// Phase08bVPCCNIPrefix adopts the vpc-cni EKS managed addon and enables prefix
// delegation (ENABLE_PREFIX_DELEGATION=true, WARM_PREFIX_TARGET=1,
// WARM_ENI_TARGET=0).
//
// Ordering: runs after Phase08 (cluster ACTIVE) and BEFORE Phase10 (node group)
// so nodes boot already in prefix mode. Prefix delegation keeps the CNI on the
// primary ENI (device-index 0), which:
//   - avoids attaching a secondary ENI for pods (no cross-node asymmetric-drop
//     on a secondary ENI — the root cause that previously hung BNK licensing), and
//   - leaves device-index 1 free so Phase17 can claim it for the TMM data ENIs
//     without contention.
//
// D-005: CheckAuthOrDie at entry.
func Phase08bVPCCNIPrefix(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintf(os.Stderr, "[phase 08b] vpc-cni prefix delegation: cluster=%s\n", cl.Metadata.Name)

	if dryRun {
		fmt.Fprintln(os.Stderr, "[phase 08b] dry-run: would adopt vpc-cni addon with ENABLE_PREFIX_DELEGATION=true (prefix delegation → CNI stays on primary ENI, no secondary ENI)")
		st.Set("VPC_CNI_PREFIX_DELEGATION", "dry-run-true")
		return nil
	}

	if err := ensureVPCCNIPrefixAddon(ctx, clients.EKS, cl.Metadata.Name); err != nil {
		return fmt.Errorf("phase08b: vpc-cni addon: %w", err)
	}
	st.Set("VPC_CNI_PREFIX_DELEGATION", "true")
	return st.Save()
}

// Phase08bVPCCNIPrefixDown leaves the vpc-cni addon in place — it is
// EKS-owned core networking and will be removed with the cluster by Phase08
// down. This function only clears state.
func Phase08bVPCCNIPrefixDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	fmt.Fprintf(os.Stderr, "[phase 08b down] vpc-cni addon left in place (EKS-owned core networking; removed with the cluster by phase08 down): cluster=%s\n", cl.Metadata.Name)
	clearPhase08bState(st)
	return st.Save()
}

// ensureVPCCNIPrefixAddon creates or updates the vpc-cni managed addon with
// prefix-delegation ConfigurationValues, then polls for ACTIVE.
//
// vpc-cni is configured before the node group exists; with 0 DaemonSet pods
// the managed addon may report CREATING or DEGRADED. The ConfigurationValues
// is applied server-side regardless and reconciles when Phase10 nodes join.
// Accept non-ACTIVE here to avoid hanging up; Phase10's node-Ready gate is
// the real convergence point.
func ensureVPCCNIPrefixAddon(ctx context.Context, eksClient EKSAPI, clusterName string) error {
	desc, err := eksClient.DescribeAddon(ctx, &eks.DescribeAddonInput{
		ClusterName: &clusterName,
		AddonName:   aws.String(vpcCNIAddonName),
	})
	if err != nil {
		var apiErr smithy.APIError
		if !errors.As(err, &apiErr) || apiErr.ErrorCode() != "ResourceNotFoundException" {
			return fmt.Errorf("DescribeAddon: %w", err)
		}
		// Not found → create.
		fmt.Fprintf(os.Stderr, "[phase 08b] creating vpc-cni addon with prefix delegation config\n")
		_, cErr := eksClient.CreateAddon(ctx, &eks.CreateAddonInput{
			ClusterName:         &clusterName,
			AddonName:           aws.String(vpcCNIAddonName),
			ConfigurationValues: aws.String(vpcCNIPrefixConfigJSON),
			ResolveConflicts:    ekstypes.ResolveConflictsOverwrite,
		})
		if cErr != nil {
			return fmt.Errorf("CreateAddon %s: %w", vpcCNIAddonName, cErr)
		}
	} else {
		// Found → update to apply/enforce prefix delegation config idempotently.
		fmt.Fprintf(os.Stderr, "[phase 08b] vpc-cni addon already present (status=%s); updating with prefix delegation config\n",
			desc.Addon.Status)
		_, uErr := eksClient.UpdateAddon(ctx, &eks.UpdateAddonInput{
			ClusterName:         &clusterName,
			AddonName:           aws.String(vpcCNIAddonName),
			ConfigurationValues: aws.String(vpcCNIPrefixConfigJSON),
			ResolveConflicts:    ekstypes.ResolveConflictsOverwrite,
		})
		if uErr != nil {
			return fmt.Errorf("UpdateAddon %s: %w", vpcCNIAddonName, uErr)
		}
	}

	return pollVPCCNIAddon(ctx, eksClient, clusterName)
}

// pollVPCCNIAddon waits for the vpc-cni addon to reach ACTIVE. If the deadline
// passes while the addon is still in a non-terminal state (CREATING, DEGRADED,
// etc.) it logs a warning and returns nil — the configuration is applied
// server-side regardless, and Phase10's node-Ready gate is the real convergence
// point.
func pollVPCCNIAddon(ctx context.Context, eksClient EKSAPI, clusterName string) error {
	deadline := time.Now().Add(vpcCNIAddonActiveTimeout)
	for time.Now().Before(deadline) {
		if err := ctx.Err(); err != nil {
			return err
		}
		out, dErr := eksClient.DescribeAddon(ctx, &eks.DescribeAddonInput{
			ClusterName: &clusterName,
			AddonName:   aws.String(vpcCNIAddonName),
		})
		if dErr != nil {
			return fmt.Errorf("DescribeAddon during poll: %w", dErr)
		}
		switch out.Addon.Status {
		case ekstypes.AddonStatusActive:
			fmt.Fprintf(os.Stderr, "[phase 08b] vpc-cni addon ACTIVE\n")
			return nil
		case ekstypes.AddonStatusCreateFailed:
			return fmt.Errorf("vpc-cni addon entered terminal state %s", out.Addon.Status)
		}
		// AddonStatusDegraded, AddonStatusCreating, AddonStatusUpdating, and any
		// other non-terminal statuses are tolerated — keep polling.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(vpcCNIAddonPollInterval):
		}
	}
	// Deadline passed but addon not yet ACTIVE — warn and continue. The
	// ConfigurationValues is committed server-side; convergence happens when
	// Phase10 nodes join and the DaemonSet rolls out.
	fmt.Fprintf(os.Stderr, "[phase 08b] warning: vpc-cni addon did not reach ACTIVE within %s (node group not yet created — this is expected); continuing\n",
		vpcCNIAddonActiveTimeout)
	return nil
}

func clearPhase08bState(st *state.State) {
	st.Set("VPC_CNI_PREFIX_DELEGATION", "")
}
