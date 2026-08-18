package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
)

var (
	flagCleanupDryRun     bool
	flagCleanupAuto       bool
	flagCleanupRegions    []string
	flagCleanupAllRegions bool
)

var cleanupCmd = &cobra.Command{
	Use:   "cleanup",
	Short: "Delete orphaned IBM Cloud resources left by a failed `down`",
	Long: `cleanup sweeps the current workspace's IBM Cloud account for resources named
after the workspace prefix (<prefix>-*) and deletes them in dependency order.

Use it to recover from a 'down' that errored partway and stranded resources
(jumphosts, security groups, floating IPs, subnets, VPCs, the Transit Gateway,
the registry COS instance, the ROKS cluster, and the BNK trusted profile).

This is a DESTRUCTIVE, best-effort sweep keyed purely on the <prefix>- name
convention. It always lists what it found and asks before deleting (unless
--auto); --dry-run lists without deleting. Re-run if some deletes fail (e.g. a
VPC waiting on an async cluster delete).

A Transit Gateway is deleted only once its connections are detached, and
cleanup detaches only connections to VPCs it is also deleting. A gateway still
attached to anything else — a VPC under another prefix, a Direct Link, a GRE
tunnel — is REFUSED rather than silently disconnected from its other tenants.
Re-running will not clear that: detach it yourself, or delete those networks.`,
	Args: cobra.NoArgs,
	RunE: runCleanup,
}

func init() {
	cleanupCmd.Flags().BoolVar(&flagCleanupDryRun, "dry-run", false, "list orphaned resources without deleting")
	cleanupCmd.Flags().BoolVarP(&flagCleanupAuto, "auto", "y", false, "skip the confirmation prompt")
	cleanupCmd.Flags().StringSliceVar(&flagCleanupRegions, "region", nil, "additional region(s) to sweep (repeatable); defaults to the cluster + client regions")
	cleanupCmd.Flags().BoolVar(&flagCleanupAllRegions, "all-regions", false, "sweep every IBM Cloud region (slower; catches resources in regions not recorded in config)")
	rootCmd.AddCommand(cleanupCmd)
}

func runCleanup(cmd *cobra.Command, _ []string) error {
	cctx, ic, err := openIBMClient()
	if err != nil {
		return err
	}
	ws := cctx.Workspace
	if ws.Prefix == "" {
		return errors.New("workspace has no resource prefix; cleanup matches resources by `<prefix>-*` name, so there is nothing to sweep")
	}

	// Sweep the cluster region, the testing-client region, the cluster-outputs
	// region, and any --region overrides. The cluster ID (from
	// cluster-outputs.json) lets the BNK trusted profile be found even after
	// the cluster itself is gone.
	regions := []string{ws.IBMCloud.Region}
	if ws.Resources != nil && ws.Resources.ClientRegion != "" {
		regions = append(regions, ws.Resources.ClientRegion)
	}
	clusterID := ""
	if out, oerr := config.ReadClusterOutputs(cctx.WorkspaceName); oerr == nil && out != nil {
		clusterID = out.ClusterID
		regions = append(regions, out.Region)
	}
	regions = append(regions, flagCleanupRegions...)
	if flagCleanupAllRegions {
		for _, r := range ibm.CuratedRegions() {
			regions = append(regions, r.Name)
		}
	}
	regions = dedupeStrings(regions)

	scope := ibm.SweepScope{Prefix: ws.Prefix, ClusterID: clusterID, Regions: regions}

	ctx := cmd.Context()
	fmt.Fprintf(os.Stderr, "→ Scanning for %s-* resources in regions: %s\n", ws.Prefix, strings.Join(regions, ", "))
	if !flagCleanupAllRegions {
		fmt.Fprintln(os.Stderr, "  (use --all-regions to sweep every region, or --region <name> to add one)")
	}
	orphans, err := ic.FindOrphans(ctx, scope)
	if err != nil {
		return fmt.Errorf("scanning for orphaned resources: %w", err)
	}
	if len(orphans) == 0 {
		fmt.Fprintln(os.Stderr, "✓ No orphaned resources found.")
		return nil
	}

	ibm.SortOrphans(orphans)
	printOrphans(os.Stderr, orphans)

	if flagCleanupDryRun {
		fmt.Fprintf(os.Stderr, "\n(dry-run — %d resource(s) would be deleted)\n", len(orphans))
		return nil
	}
	if !flagCleanupAuto {
		if !promptYesNo(fmt.Sprintf("Delete these %d resource(s)?", len(orphans)), false) {
			return errors.New("aborted")
		}
	}

	var failures, refusals int
	for _, o := range orphans {
		label := fmt.Sprintf("%s %s", o.Kind, o.Name)
		if o.Region != "" {
			label += " (" + o.Region + ")"
		}
		// The whole discovered set goes with each delete: the Transit Gateway
		// path has to know which VPCs this run is removing before it decides
		// which connections it may detach.
		if derr := ic.DeleteOrphan(ctx, o, orphans); derr != nil {
			fmt.Fprintf(os.Stderr, "  ✗ %s: %v\n", label, derr)
			failures++
			if errors.Is(derr, ibm.ErrForeignTGWConnection) {
				refusals++
			}
			continue
		}
		fmt.Fprintf(os.Stderr, "  ✓ %s\n", label)
	}

	if failures > 0 {
		// "Re-run" is the right advice ONLY for a delete that can still
		// converge on its own. A refusal cannot: nothing in the loop will ever
		// remove a connection to a network outside the sweep, so telling the
		// operator to re-run would send them round a loop that fails
		// identically every time (#85).
		if refusals > 0 {
			return fmt.Errorf("%d of %d resource(s) failed to delete; %d refused because a Transit Gateway is still attached to networks outside this sweep — re-running will NOT clear those, detach them (or delete those networks) first", failures, len(orphans), refusals)
		}
		return fmt.Errorf("%d of %d resource(s) failed to delete — re-run `roksbnkctl cleanup` (some may be waiting on an async delete)", failures, len(orphans))
	}
	fmt.Fprintf(os.Stderr, "\n✓ Deleted %d orphaned resource(s).\n", len(orphans))
	return nil
}

// printOrphans renders the discovered resources as an aligned table.
func printOrphans(w io.Writer, orphans []ibm.OrphanResource) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\nKIND\tNAME\tREGION\tID")
	for _, o := range orphans {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", o.Kind, o.Name, o.Region, o.ID)
	}
	tw.Flush()
}

// dedupeStrings drops empties + duplicates, preserving first-seen order.
func dedupeStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}
