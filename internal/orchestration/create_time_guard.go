package orchestration

import (
	"fmt"
	"io"
	"strings"

	"github.com/jgruberf5/roksbnkctl/internal/config"
)

// Settings that are fixed when a cluster is created.
//
// THE HARM THEY PREVENT. Terraform is perfectly willing to plan a replacement for
// a running cluster when one of these changes — it is not an error, it is a
// destroy and a create, and it shows up as ~60 lines of plan output that reads
// like an update. Nothing converts a cluster in place, so a changed value is
// always a mistake or always a new cluster; either way it should not be planned.
//
// TWO CLASSES, DELIBERATELY TREATED DIFFERENTLY.
//
// network_mode is NEW. Nothing can be relying on changing it, so refusing is
// safe and immediate.
//
// vpc_cidr is NOT new. It has always been documented as create-time-only and has
// never been enforced, so somebody may be changing it today and getting a silent
// replacement. Turning that into a refusal would be a behaviour change on an
// existing contract — so it WARNS, loudly, and refuses in a later release. The
// warning is the deprecation; the refusal follows it, not the other way round.

// guardCreateTimeSettings compares create-time-only settings against what the
// cluster was actually built with. Returns an error only for the settings that
// are safe to enforce; the rest warn on w.
//
// Silent when there is no recorded cluster (nothing to contradict) or the record
// cannot be read — this exists to catch a contradiction, not to invent a new way
// for a first run to fail.
func guardCreateTimeSettings(cctx *config.Context, w io.Writer) error {
	if cctx == nil || cctx.Workspace == nil {
		return nil
	}
	out, err := config.ReadClusterOutputs(cctx.WorkspaceName)
	if err != nil || out == nil || out.ClusterID == "" {
		return nil
	}

	// ── enforced: network_mode ───────────────────────────────────────────────
	want := cctx.Workspace.ClusterNetworkMode()
	got := out.Network()
	if want != got {
		return fmt.Errorf(
			"cluster %q was created as a %s cluster, but this workspace now asks for %s.\n\n"+
				"  A cluster's network mode is fixed when it is built — converting one in place is\n"+
				"  not supported, and continuing would plan a REPLACEMENT of the running cluster\n"+
				"  rather than a change to it.\n\n"+
				"  Either set cluster.network_mode back to %s, or create a new cluster in a new\n"+
				"  workspace for %s",
			out.ClusterName, got, want, got, want)
	}

	// ── warn-only: vpc_cidr (see the note above) ─────────────────────────────
	if cidr := strings.TrimSpace(cctx.Workspace.Cluster.VPCCIDR); cidr != "" && out.VPCID != "" {
		fmt.Fprintf(w, "! cluster.vpc_cidr is a CREATE-time setting and cluster %q already exists.\n"+
			"  It is ignored for an existing VPC; changing it cannot re-address a live subnet.\n"+
			"  A future release will refuse this rather than ignore it.\n", out.ClusterName)
	}
	return nil
}

// guardSupportedCombination refuses a BNK release driving a cluster it cannot.
//
// Runs before the BNK phase plans, where both halves are finally known: the line
// derived from bnk.manifest_version, and what the cluster actually is from its
// own record. Neither half alone is enough to tell.
func guardSupportedCombination(cctx *config.Context) error {
	if cctx == nil || cctx.Workspace == nil {
		return nil
	}
	line, err := cctx.Workspace.BNKLine()
	if err != nil {
		return err
	}
	// No cluster recorded yet: check what we can — the line itself — and leave
	// the pairing to the run that actually has a cluster.
	out, rerr := config.ReadClusterOutputs(cctx.WorkspaceName)
	if rerr != nil || out == nil || out.ClusterID == "" {
		return config.CheckSupported(line, cctx.Workspace.ClusterNetworkMode(), 0)
	}
	return config.CheckSupported(line, out.Network(), out.Schema())
}
