package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
)

// guardSharedVPCTeardown refuses to destroy a cluster that CREATED its VPC while
// that VPC still holds subnets from OTHER clusters (the shared-VPC / adopt-existing-
// VPC topology). The VPC owner must be torn down LAST: destroying it first fails
// with "VPC is in use" AND deletes the per-zone public gateways the other clusters'
// subnets are still attached to. Best-effort — a discovery error warns and proceeds,
// so a transient API hiccup never blocks a legitimate teardown.
func guardSharedVPCTeardown(ctx context.Context, ws *config.Workspace, workspace string) error {
	if ws == nil || !ws.Cluster.Create {
		return nil // we didn't create the cluster/VPC (registered, or nothing)
	}
	// Adopting an existing VPC ⇒ we are NOT the owner; someone else deletes it.
	if ws.Resources != nil && !ws.Resources.ClusterVPC.Create && ws.Resources.ClusterVPC.Existing != "" {
		return nil
	}
	co, err := config.ReadClusterOutputs(workspace)
	if err != nil || co == nil || co.VPCID == "" {
		return nil // no recorded VPC to check
	}
	region := co.Region
	if region == "" {
		region = ws.IBMCloud.Region
	}
	if region == "" {
		return nil
	}
	_, ic, err := openIBMClient(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (shared-VPC teardown check skipped: %v)\n", err)
		return nil
	}
	lctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	subnets, err := ic.ListSubnets(lctx, region)
	if err != nil {
		fmt.Fprintf(os.Stderr, "  (shared-VPC teardown check skipped: %v)\n", err)
		return nil
	}
	foreign := foreignSubnetsInVPC(subnets, co.VPCID, co.ClusterName, co.SubnetIDs)
	if len(foreign) == 0 {
		return nil
	}
	label := co.VPCName
	if label == "" {
		label = co.VPCID
	}
	//lint:ignore ST1005 multi-line actionable operator message; trailing period is intentional and matches the internal sentences
	return fmt.Errorf(
		"cluster VPC %q still holds %d subnet(s) from other clusters: %s\n"+
			"  This workspace CREATED the VPC (and its per-zone public gateways), so it must be torn\n"+
			"  down LAST — destroying it now fails (\"VPC is in use\") and deletes the gateways the other\n"+
			"  clusters still depend on. Tear down the workspace(s) sharing this VPC first, then retry.",
		label, len(foreign), strings.Join(foreign, ", "))
}

// foreignSubnetsInVPC returns the names of subnets in vpcID that do NOT belong to
// this cluster. A subnet is OURS if its id is recorded in cluster-outputs.json OR
// its name carries this cluster's subnet prefix ("<cluster>-subnet") — the OR holds
// even when the id list wasn't recorded, and never mis-flags our own subnets. The
// "-subnet" suffix on the prefix keeps "us-south-test-1" from matching
// "us-south-test-10"'s subnets.
func foreignSubnetsInVPC(subnets []ibm.Subnet, vpcID, clusterName string, ownIDs []string) []string {
	own := make(map[string]bool, len(ownIDs))
	for _, id := range ownIDs {
		own[id] = true
	}
	prefix := clusterName + "-subnet"
	var foreign []string
	for _, s := range subnets {
		if s.VPC.ID != vpcID {
			continue
		}
		if own[s.ID] {
			continue
		}
		if clusterName != "" && strings.HasPrefix(s.Name, prefix) {
			continue
		}
		foreign = append(foreign, s.Name)
	}
	return foreign
}
