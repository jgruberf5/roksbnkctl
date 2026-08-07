package cli

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
	"github.com/jgruberf5/roksbnkctl/internal/cred"
	"github.com/jgruberf5/roksbnkctl/internal/ibm"
	"github.com/jgruberf5/roksbnkctl/internal/naming"
)

// guardVPCPrefixOverlap refuses a `cluster up` that would create a VPC whose
// address prefixes overlap a VPC already attached to the transit gateway it joins.
//
// WHY THIS EXISTS. A transit gateway routes on VPC address prefixes. Two attached
// VPCs claiming the same block make routing ambiguous and the gateway drops traffic
// for one of them — with no error anywhere. It surfaces much later as INTERMITTENT
// image-pull timeouts against a mirror, while every security group and network ACL
// in the path allows the traffic, which sends people to firewalls for hours. Issue
// #46 has the full write-up.
//
// It is easy to hit because IBM's default ("auto") address prefix management gives
// EVERY VPC in a region the same prefixes, and a disconnected cluster MUST share a
// gateway with its private registry. So the second disconnected cluster on a gateway
// collides by construction, not by bad luck.
//
// Best-effort by design: only a definite, explainable overlap fails. An unreachable
// API, an unresolvable gateway or a VPC whose prefixes cannot be read all warn and
// continue — this is a guard against a specific silent failure, not a new
// precondition on being able to build a cluster at all.
func guardVPCPrefixOverlap(ctx context.Context, cctx *config.Context) error {
	ws := cctx.Workspace
	if ws == nil || !ws.Cluster.Create {
		return nil // adopting a cluster: its VPC already exists with its own prefixes
	}
	// Only a SHARED gateway can collide. Creating our own means we are its only VPC.
	tgwName := ""
	if ws.Resources != nil && !ws.Resources.TransitGateway.Create {
		tgwName = strings.TrimSpace(ws.Resources.TransitGateway.Existing)
	}
	if tgwName == "" {
		return nil
	}

	intended, err := ibm.IntendedPrefixes(ws.Cluster.VPCCIDR)
	if err != nil {
		return err // a malformed cluster.vpc_cidr is the operator's own input — fail
	}

	region := ws.IBMCloud.Region
	resolver := &cred.Resolver{
		Workspace: cctx.WorkspaceName,
		Source:    ws.IBMCloud.APIKeySource,
	}
	apiKey, err := resolver.IBMCloudAPIKey(ctx)
	if err != nil {
		warnPrefixGuardSkipped(tgwName, err)
		return nil
	}
	cl, err := ibm.New(apiKey, region)
	if err != nil {
		warnPrefixGuardSkipped(tgwName, err)
		return nil
	}
	gw, err := cl.ResolveTransitGateway(ctx, tgwName)
	if err != nil || gw == nil {
		warnPrefixGuardSkipped(tgwName, err)
		return nil
	}
	conns, err := cl.ListTGWConnections(ctx, gw.ID)
	if err != nil {
		warnPrefixGuardSkipped(tgwName, err)
		return nil
	}

	vpcs, err := cl.ListVPCs(ctx, region)
	if err != nil {
		warnPrefixGuardSkipped(tgwName, err)
		return nil
	}
	// The gateway reports attachments by CRN; map those to the region's VPCs so a
	// conflict can name something an operator recognises.
	byCRN := make(map[string]ibm.VPC, len(vpcs))
	for _, v := range vpcs {
		byCRN[strings.ToLower(v.CRN)] = v
	}

	// EXCLUDE OUR OWN VPC. `cluster up` is idempotent and gets re-run constantly —
	// after a partial failure, or just to converge. On any run after the first, the
	// VPC this workspace created is itself attached to the gateway, carrying exactly
	// the prefixes we intend to use. Comparing against it makes the guard report a
	// VPC overlapping ITSELF and refuse a re-run that is a no-op. Identify it two
	// ways because either can be the only one available: by the id recorded in
	// cluster-outputs.json (absent until a run completes) and by the name terraform
	// derives from the prefix (valid before anything is recorded).
	ownVPCID := ""
	if co, cerr := config.ReadClusterOutputs(cctx.WorkspaceName); cerr == nil && co != nil {
		ownVPCID = co.VPCID
	}
	ownVPCName := naming.Derive(ws.Prefix).ClusterVPCName

	attached := map[string][]string{}
	for _, c := range conns {
		// Same filter tgwConnectionForVPC uses: empty network_type means vpc, and
		// only attached/pending connections actually route. A failed or deleting one
		// is on its way out — refusing the build over it would be wrong.
		if c.NetworkType != "" && !strings.EqualFold(c.NetworkType, "vpc") {
			continue
		}
		if c.Status != "attached" && c.Status != "pending" {
			continue
		}
		v, ok := byCRN[strings.ToLower(c.NetworkID)]
		if !ok {
			continue // attached from another region/account — not comparable here
		}
		if (ownVPCID != "" && v.ID == ownVPCID) || v.Name == ownVPCName {
			continue // ourselves: a VPC cannot overlap itself
		}
		prefixes, perr := cl.ListVPCAddressPrefixes(ctx, region, v.ID)
		if perr != nil {
			continue // best-effort: one unreadable VPC must not block the build
		}
		for _, p := range prefixes {
			attached[v.Name] = append(attached[v.Name], p.CIDR)
		}
	}
	if len(attached) == 0 {
		return nil
	}

	conflicts, err := ibm.FindPrefixConflicts(intended, attached)
	if err != nil || len(conflicts) == 0 {
		return nil
	}

	// Deterministic output: the same collision must read the same way every run.
	sort.Slice(conflicts, func(i, j int) bool {
		if conflicts[i].VPCName != conflicts[j].VPCName {
			return conflicts[i].VPCName < conflicts[j].VPCName
		}
		return conflicts[i].Intended < conflicts[j].Intended
	})
	var b strings.Builder
	fmt.Fprintf(&b, "the cluster VPC's address prefixes overlap a VPC already attached to transit gateway %q:\n", tgwName)
	for _, c := range conflicts {
		fmt.Fprintf(&b, "  %s\n", c)
	}
	b.WriteString("\nA transit gateway cannot route to two VPCs with overlapping prefixes: it silently\n")
	b.WriteString("blackholes one, which shows up as INTERMITTENT image-pull timeouts against the\n")
	b.WriteString("mirror while every security group and ACL in the path allows the traffic.\n\n")
	if strings.TrimSpace(ws.Cluster.VPCCIDR) == "" {
		b.WriteString("This cluster has no cluster.vpc_cidr, so it takes IBM's default prefixes — the\n")
		b.WriteString("same ones every other roksbnkctl-created VPC takes. Give it its own block:\n")
		b.WriteString("  config.yaml:  cluster.vpc_cidr: 10.242.0.0/16\n")
		b.WriteString("  env:          ROKSBNKCTL_CLUSTER_VPC_CIDR=10.242.0.0/16\n")
	} else {
		fmt.Fprintf(&b, "Choose a block that does not overlap the above (current: %s).\n", ws.Cluster.VPCCIDR)
	}
	b.WriteString("\nOr free the gateway first — detaching is enough, the other cluster need not be\n")
	b.WriteString("destroyed:  roksbnkctl -w <workspace> tgw disconnect --auto")
	return fmt.Errorf("%s", b.String())
}

func warnPrefixGuardSkipped(tgwName string, err error) {
	msg := "could not read its attachments"
	if err != nil {
		msg = err.Error()
	}
	fmt.Fprintf(os.Stderr,
		"→ note: could not check address-prefix overlap on transit gateway %q (%s).\n"+
			"  Confirm no other roksbnkctl-created cluster VPC is attached — overlapping prefixes\n"+
			"  are silently blackholed by the gateway rather than reported.\n", tgwName, msg)
}
