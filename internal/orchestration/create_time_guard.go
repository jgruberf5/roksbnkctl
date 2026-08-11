package orchestration

import (
	"errors"
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
	// A missing or unreadable record means "no cluster yet", not "fail": a first
	// run has nothing to contradict. CheckNetworkMode is given the nil and
	// handles it — it still VALIDATES the configured value, which has to happen
	// whether or not a cluster exists, since an unknown mode must never reach
	// terraform from either entry point.
	out, err := config.ReadClusterOutputs(cctx.WorkspaceName)
	if err != nil {
		out = nil
	}

	// ── enforced: network_mode ───────────────────────────────────────────────
	// The rule itself lives in config so `cluster up` and `bnk up` cannot drift
	// into giving different answers to the same question.
	if err := config.CheckNetworkMode(cctx.Workspace, out); err != nil {
		return err
	}
	if out == nil || out.ClusterID == "" {
		return nil // nothing recorded to compare the rest against
	}

	// ── warn-only: vpc_cidr (see the note above) ─────────────────────────────
	// Only on a real DISAGREEMENT. Warning whenever vpc_cidr is merely set would
	// fire on every run of every workspace that ever used it — including the ones
	// that set it, built the cluster it describes, and changed nothing since,
	// which is the normal steady state. A warning that fires when nothing is
	// wrong teaches people to skip warnings.
	//
	// A record with no vpc_cidr (adopted VPC, or written before schema 2) is
	// silent: nothing is known to disagree with, and guessing reintroduces
	// exactly the false positive this avoids.
	cidr := strings.TrimSpace(cctx.Workspace.Cluster.VPCCIDR)
	if cidr != "" && out.VPCCIDR != "" && cidr != out.VPCCIDR {
		fmt.Fprintf(w, "! cluster.vpc_cidr is a CREATE-time setting and it has changed.\n"+
			"  Cluster %q was created from %s; this workspace now says %s.\n"+
			"  The new value is ignored — an existing VPC's prefixes cannot be re-addressed\n"+
			"  without replacing its subnets. A future release will refuse this rather than\n"+
			"  ignore it; to use %s, build a new cluster in a new workspace.\n",
			out.ClusterName, out.VPCCIDR, cidr, cidr)
	}
	return nil
}

// guardSupportedCombination refuses a BNK release driving a cluster it cannot.
//
// Runs before the BNK phase plans, where both halves are finally known: the line
// derived from bnk.manifest_version, and what the cluster actually is from its
// own record. Neither half alone is enough to tell.
func guardSupportedCombination(cctx *config.Context, w io.Writer) error {
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
		err = config.CheckSupported(line, cctx.Workspace.ClusterNetworkMode(), 0)
	} else {
		err = config.CheckSupported(line, out.Network(), out.Schema())
	}

	// A line this build has never heard of is missing information, not a known
	// incompatibility — and a release newer than the binary is the ordinary way
	// to get here. Say so and continue; refusing would mean every build refuses
	// every BNK release that ships after it.
	if errors.Is(err, config.ErrUnknownLine) {
		fmt.Fprintf(w, "! %v\n", err)
		return nil
	}
	return err
}
