package cli

import (
	"context"
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
	cctx, ic, err := openIBMClient(cmdContext(cmd))
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

	scope := ibm.SweepScope{Prefix: ws.Prefix, ClusterID: clusterID, Regions: regions, Adopted: adoptedRefs(ws)}

	ctx := cmdContext(cmd)
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

	// Split before anything is shown or deleted. An adopted resource matches the
	// workspace prefix exactly like one the tool created — `sm-cli` adopting
	// `sm-cli-registry-cos` is the motivating case (#302) — and the account that
	// adopts is usually at its COS cap, so it cannot simply recreate one.
	deletable, protected := partitionProtected(orphans)

	if len(protected) > 0 {
		fmt.Fprintf(os.Stderr, "\n%d resource(s) match the prefix but are ADOPTED by this workspace and will NOT be deleted:\n", len(protected))
		printProtected(os.Stderr, protected)
		fmt.Fprintln(os.Stderr, "  (roksbnkctl never created these; remove the config key if you want cleanup to own them)")
	}

	if len(deletable) == 0 {
		fmt.Fprintln(os.Stderr, "\n✓ Nothing to delete — every match is adopted.")
		return nil
	}

	printOrphans(os.Stderr, deletable)

	if flagCleanupDryRun {
		fmt.Fprintf(os.Stderr, "\n(dry-run — %d resource(s) would be deleted)\n", len(deletable))
		return nil
	}
	if !flagCleanupAuto {
		if !promptYesNo(fmt.Sprintf("Delete these %d resource(s)?", len(deletable)), false) {
			return errors.New("aborted")
		}
	}

	failures, refusals := deleteOrphans(ctx, ic, deletable, os.Stderr)

	if failures > 0 {
		// "Re-run" is the right advice ONLY for a delete that can still
		// converge on its own. A refusal cannot: nothing in the loop will ever
		// remove a connection to a network outside the sweep, so an unqualified
		// "re-run" would send the operator round a loop that fails identically
		// every time (#85).
		//
		// "as-is" carries weight. An identical re-run cannot help, but a WIDER
		// one can: a VPC that looks foreign is often one this sweep simply did
		// not scan, and --all-regions brings it into scope. Saying re-running
		// never helps would contradict the per-resource message, which offers
		// exactly that.
		if refusals > 0 {
			return fmt.Errorf("%d of %d resource(s) failed to delete; %d refused because a Transit Gateway is still attached to networks outside this sweep — re-running as-is will NOT clear those. If those networks are yours, widen the sweep (--all-regions); otherwise detach them, or delete them, first", failures, len(orphans), refusals)
		}
		return fmt.Errorf("%d of %d resource(s) failed to delete — re-run `roksbnkctl cleanup` (some may be waiting on an async delete)", failures, len(orphans))
	}
	fmt.Fprintf(os.Stderr, "\n✓ Deleted %d orphaned resource(s).\n", len(orphans))
	return nil
}

// printOrphans renders the discovered resources as an aligned table.
// adoptedRefs translates the workspace's adopt decisions into the sweep's
// protection list.
//
// Every entry here is a resource roksbnkctl READS and never creates. The
// terraform side reaches them through `data` sources, which are never
// destroyed, so `bnk down` was already safe — it is only cleanup's independent
// resource-controller sweep that can reach them, precisely because it does not
// consult terraform state at all.
func adoptedRefs(ws *config.Workspace) []ibm.AdoptedRef {
	var refs []ibm.AdoptedRef
	add := func(kind, value, source string) {
		if value != "" {
			refs = append(refs, ibm.AdoptedRef{Kind: kind, Value: value, Source: source})
		}
	}

	// The cluster is adopted through ClusterCfg, not a ResourceToggle, and is
	// named the bare prefix — the one case matchesPrefix matches exactly.
	if !ws.Cluster.Create {
		add("cluster", ws.Cluster.Name, "cluster.name (create: false)")
	}

	if ws.Resources == nil {
		return refs
	}
	r := ws.Resources
	if !r.TransitGateway.Create {
		add("transit_gateway", r.TransitGateway.Existing, "resources.transit_gateway.existing")
	}
	if !r.RegistryCOS.Create {
		add("cos_instance", r.RegistryCOS.Existing, "resources.registry_cos.existing")
	}
	if !r.ClientVPC.Create {
		add("vpc", r.ClientVPC.Existing, "resources.client_vpc.existing")
	}
	if !r.ClusterVPC.Create {
		// ClusterVPC.Existing is the VPC *ID*, unlike the adopt-by-name toggles.
		add("vpc", r.ClusterVPC.Existing, "resources.cluster_vpc.existing")
	}
	// The testing SSH key has no create toggle because it is NEVER created:
	// terraform/modules/testing reaches it only through
	// data.ibm_is_ssh_key.{tgw,cluster}_ssh_key. So whenever it is named, it is
	// adopted. #302 listed this path as unverified; it is verified now.
	add("ssh_key", r.TestingSSHKeyName, "resources.testing_ssh_key_name")

	return refs
}

// partitionProtected splits a swept set into what may be deleted and what may
// not. Returning two slices, rather than filtering at the delete call, is what
// keeps --auto away from a protected resource: the delete loop never sees one.
// DeleteOrphan refuses them as well — see the note there on why one layer was
// not enough.
func partitionProtected(orphans []ibm.OrphanResource) (deletable, protected []ibm.OrphanResource) {
	for _, o := range orphans {
		if o.Protected {
			protected = append(protected, o)
			continue
		}
		deletable = append(deletable, o)
	}
	return deletable, protected
}

// orphanDeleter is the one method deleteOrphans needs, so a test can supply a
// recorder instead of a live IBM client.
type orphanDeleter interface {
	DeleteOrphan(ctx context.Context, o ibm.OrphanResource, sweep []ibm.OrphanResource) error
}

// deleteOrphans deletes each resource in list, reporting per-resource outcomes
// to w, and returns how many failed and how many were refusals.
//
// It SKIPS protected resources even though its caller already filtered them
// out. That is deliberate belt-and-braces: a mutation that passed the
// unfiltered set here compiled and passed every test (#302), and the cost of
// that mistake is deleting a customer's adopted COS. With this skip the slice
// passed in is no longer load-bearing, and ibm.DeleteOrphan refuses protected
// resources as a third layer.
func deleteOrphans(ctx context.Context, d orphanDeleter, list []ibm.OrphanResource, w io.Writer) (failures, refusals int) {
	// Filter ONCE, and use the filtered slice for both the loop and the sweep
	// argument. The sweep set is not just bookkeeping: ibm.sweptVPCCRNs reads it
	// as "the VPCs this run is deleting", and the Transit Gateway path detaches
	// connections to exactly those. A protected VPC left in it would keep its
	// own delete refused while its transit-gateway connection was detached
	// anyway — cutting a network the workspace adopted, which is the harm #302
	// is about arriving by a different door.
	active := make([]ibm.OrphanResource, 0, len(list))
	for _, o := range list {
		if !o.Protected {
			active = append(active, o)
		}
	}

	for _, o := range active {
		label := fmt.Sprintf("%s %s", o.Kind, o.Name)
		if o.Region != "" {
			label += " (" + o.Region + ")"
		}
		// The whole ACTIVE set goes with each delete: the Transit Gateway path
		// has to know which VPCs this run is removing before it decides which
		// connections it may detach.
		if derr := d.DeleteOrphan(ctx, o, active); derr != nil {
			fmt.Fprintf(w, "  ✗ %s: %v\n", label, derr)
			failures++
			if errors.Is(derr, ibm.ErrForeignTGWConnection) {
				refusals++
			}
			continue
		}
		fmt.Fprintf(w, "  ✓ %s\n", label)
	}
	return failures, refusals
}

func printProtected(w io.Writer, orphans []ibm.OrphanResource) {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "\nKIND\tNAME\tREGION\tADOPTED VIA")
	for _, o := range orphans {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", o.Kind, o.Name, o.Region, o.ProtectedBy)
	}
	tw.Flush()
}

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
