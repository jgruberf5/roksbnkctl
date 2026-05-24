package phases

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/eks"
	ekstypes "github.com/aws/aws-sdk-go-v2/service/eks/types"

	"github.com/JLCode-tech/awsbnkctl/internal/aws/awsmw"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/state"
	"github.com/JLCode-tech/awsbnkctl/internal/aws/tags"
	"github.com/JLCode-tech/awsbnkctl/internal/intent"
)

// Phase08EKSCluster creates the EKS control plane and waits until ACTIVE.
// State keys written: EKS_CLUSTER_NAME, EKS_CLUSTER_ARN, EKS_ENDPOINT,
// EKS_CA, EKS_OIDC_URL, EKS_SECURITY_GROUP, EKS_VERSION.
//
// Idempotent: DescribeCluster before creating; if already CREATING/UPDATING
// waits until ACTIVE; if already ACTIVE populates state and skips create.
// Dry-run: writes placeholder state, no AWS mutations.
func Phase08EKSCluster(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients, dryRun bool) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name

	if cl.ClusterSpec == nil {
		return fmt.Errorf("phase08: cluster.yaml must include a 'cluster:' block (see slice-03 docs)")
	}

	k8sVersion := cl.ClusterSpec.KubernetesVersion
	fmt.Fprintf(os.Stderr, "[phase 08] eks cluster: creating %s (k8s v%s)\n", name, k8sVersion)

	if dryRun {
		fmt.Fprintf(os.Stderr, "[phase 08] dry-run: would create EKS cluster %s\n", name)
		st.Set("EKS_CLUSTER_NAME", name)
		st.Set("EKS_CLUSTER_ARN", "arn:aws:eks:dry-run:cluster/"+name)
		st.Set("EKS_ENDPOINT", "https://dry-run.eks")
		st.Set("EKS_CA", "dry-run-ca")
		st.Set("EKS_OIDC_URL", "https://oidc.eks.dry-run/id/dry-run")
		st.Set("EKS_SECURITY_GROUP", "sg-dry-run")
		st.Set("EKS_VERSION", k8sVersion)
		return nil
	}

	clusterRoleARN := st.Get("EKS_CLUSTER_ROLE_ARN")
	if clusterRoleARN == "" {
		return fmt.Errorf("phase08: EKS_CLUSTER_ROLE_ARN not in state (run phase07 / slice-02 first)")
	}

	allSubnets := splitCSV(st.Get("PUBLIC_SUBNETS"))
	allSubnets = append(allSubnets, splitCSV(st.Get("PRIVATE_SUBNETS"))...)
	if len(allSubnets) == 0 {
		return fmt.Errorf("phase08: no subnets in state (run phases 02-03 first)")
	}

	eksTags := tags.EKSTags(
		tags.Required(name, tags.CompEKSCluster),
		cl.Tags,
		cl.Metadata.Labels,
	)

	// Idempotency check.
	descOut, err := clients.EKS.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: ptr(name)})
	if err != nil && !isEKSNotFound(err) {
		return fmt.Errorf("phase08: DescribeCluster: %w", err)
	}

	if err == nil {
		// Cluster exists.
		existing := descOut.Cluster
		status := existing.Status
		switch status {
		case ekstypes.ClusterStatusActive:
			fmt.Fprintf(os.Stderr, "[phase 08] cluster %s already exists, status=ACTIVE, skipping create\n", name)
			return populateClusterState(st, existing)
		case ekstypes.ClusterStatusCreating, ekstypes.ClusterStatusUpdating:
			fmt.Fprintf(os.Stderr, "[phase 08] cluster %s already exists, status=%s, waiting for ACTIVE\n", name, status)
			return waitAndPopulateCluster(ctx, clients.EKS, name, st)
		case ekstypes.ClusterStatusFailed:
			return fmt.Errorf("phase08: cluster %s is in FAILED state", name)
		default:
			return fmt.Errorf("phase08: cluster %s in unexpected status %s", name, status)
		}
	}

	publicCIDRs := []string{"0.0.0.0/0"}
	if cl.ClusterSpec.EndpointAccess != nil && len(cl.ClusterSpec.EndpointAccess.PublicAccessCidrs) > 0 {
		publicCIDRs = cl.ClusterSpec.EndpointAccess.PublicAccessCidrs
	}

	// Create the cluster.
	_, err = clients.EKS.CreateCluster(ctx, &eks.CreateClusterInput{
		Name:    ptr(name),
		RoleArn: ptr(clusterRoleARN),
		Version: ptr(k8sVersion),
		ResourcesVpcConfig: &ekstypes.VpcConfigRequest{
			SubnetIds:             allSubnets,
			EndpointPublicAccess:  boolPtr(true),
			EndpointPrivateAccess: boolPtr(true),
			PublicAccessCidrs:     publicCIDRs,
		},
		Tags: eksTags,
	})
	if err != nil {
		return fmt.Errorf("phase08: CreateCluster: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 08] eks cluster %s: create request sent, waiting for ACTIVE (up to 30 min)\n", name)

	return waitAndPopulateCluster(ctx, clients.EKS, name, st)
}

// Phase08EKSClusterDown deletes the EKS cluster and waits until gone.
// Tolerates ResourceNotFoundException (already deleted).
func Phase08EKSClusterDown(ctx context.Context, cl *intent.Cluster, st *state.State, clients *Clients) error {
	awsmw.CheckAuthOrDie(clients.Profile)
	name := cl.Metadata.Name
	fmt.Fprintf(os.Stderr, "[phase 08 down] eks cluster: cluster=%s\n", name)

	clusterName := st.Get("EKS_CLUSTER_NAME")
	if clusterName == "" {
		clusterName = name
	}

	_, err := clients.EKS.DeleteCluster(ctx, &eks.DeleteClusterInput{Name: ptr(clusterName)})
	if err != nil {
		if isEKSNotFound(err) {
			fmt.Fprintf(os.Stderr, "[phase 08 down] cluster %s already gone\n", clusterName)
			clearClusterState(st)
			return st.Save()
		}
		return fmt.Errorf("phase08 down: DeleteCluster: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 08 down] cluster %s: delete requested, waiting until gone (up to 15 min)\n", clusterName)

	if err := waitClusterDeleted(ctx, clients.EKS, clusterName); err != nil {
		return fmt.Errorf("phase08 down: waiting for cluster deletion: %w", err)
	}
	fmt.Fprintf(os.Stderr, "[phase 08 down] cluster %s deleted\n", clusterName)
	clearClusterState(st)
	return st.Save()
}

// --- helpers ---

func waitAndPopulateCluster(ctx context.Context, eksc EKSAPI, name string, st *state.State) error {
	const (
		clusterTimeout = 30 * time.Minute
		clusterPoll    = 30 * time.Second
	)
	deadline := time.Now().Add(clusterTimeout)
	for time.Now().Before(deadline) {
		out, err := eksc.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: ptr(name)})
		if err != nil {
			return fmt.Errorf("waitAndPopulateCluster: DescribeCluster: %w", err)
		}
		c := out.Cluster
		switch c.Status {
		case ekstypes.ClusterStatusActive:
			fmt.Fprintf(os.Stderr, "[phase 08] EKS cluster %s reached state ACTIVE\n", name)
			if err := populateClusterState(st, c); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "[phase 08] endpoint=%s  oidc=%s\n", *c.Endpoint, oidcURL(c))
			return nil
		case ekstypes.ClusterStatusFailed:
			return fmt.Errorf("phase08: cluster %s entered FAILED state", name)
		case ekstypes.ClusterStatusCreating, ekstypes.ClusterStatusUpdating:
			// still in progress — keep polling
		default:
			return fmt.Errorf("phase08: cluster %s in unexpected state %s", name, c.Status)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(clusterPoll):
		}
	}
	return fmt.Errorf("phase08: timeout waiting for cluster %s to become ACTIVE", name)
}

func waitClusterDeleted(ctx context.Context, eksc EKSAPI, name string) error {
	const (
		deleteTimeout = 15 * time.Minute
		deletePoll    = 30 * time.Second
	)
	deadline := time.Now().Add(deleteTimeout)
	for time.Now().Before(deadline) {
		out, err := eksc.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: ptr(name)})
		if err != nil {
			if isEKSNotFound(err) {
				return nil
			}
			return fmt.Errorf("waitClusterDeleted: DescribeCluster: %w", err)
		}
		// Inspect status: a FAILED cluster during deletion will never reach
		// ResourceNotFoundException — fail fast instead of spinning to timeout.
		if c := out.Cluster; c != nil && c.Status == ekstypes.ClusterStatusFailed {
			return fmt.Errorf("EKS cluster %s entered FAILED state during deletion", name)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(deletePoll):
		}
	}
	return fmt.Errorf("phase08 down: timeout waiting for cluster %s to be deleted", name)
}

func populateClusterState(st *state.State, c *ekstypes.Cluster) error {
	if c.Arn == nil || c.Endpoint == nil || c.CertificateAuthority == nil {
		return fmt.Errorf("phase08: cluster describe response missing required fields (ARN/Endpoint/CA)")
	}
	st.Set("EKS_CLUSTER_NAME", *c.Name)
	st.Set("EKS_CLUSTER_ARN", *c.Arn)
	st.Set("EKS_ENDPOINT", *c.Endpoint)
	if c.CertificateAuthority.Data != nil {
		st.Set("EKS_CA", *c.CertificateAuthority.Data)
	}
	st.Set("EKS_OIDC_URL", oidcURL(c))
	if c.ResourcesVpcConfig != nil && c.ResourcesVpcConfig.ClusterSecurityGroupId != nil {
		st.Set("EKS_SECURITY_GROUP", *c.ResourcesVpcConfig.ClusterSecurityGroupId)
	}
	if c.Version != nil {
		st.Set("EKS_VERSION", *c.Version)
	}
	return st.Save()
}

func clearClusterState(st *state.State) {
	st.Set("EKS_CLUSTER_NAME", "")
	st.Set("EKS_CLUSTER_ARN", "")
	st.Set("EKS_ENDPOINT", "")
	st.Set("EKS_CA", "")
	st.Set("EKS_OIDC_URL", "")
	st.Set("EKS_SECURITY_GROUP", "")
	st.Set("EKS_VERSION", "")
}

func oidcURL(c *ekstypes.Cluster) string {
	if c.Identity != nil && c.Identity.Oidc != nil && c.Identity.Oidc.Issuer != nil {
		return *c.Identity.Oidc.Issuer
	}
	return ""
}

// isEKSNotFound returns true when err is an EKS ResourceNotFoundException.
func isEKSNotFound(err error) bool {
	if err == nil {
		return false
	}
	var nfe *ekstypes.ResourceNotFoundException
	return errors.As(err, &nfe)
}
