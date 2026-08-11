package config

import (
	"fmt"
	"regexp"
	"strings"
)

// The BNK release a workspace targets, and the network mode its cluster uses.
//
// TWO AXES, VARYING INDEPENDENTLY. The BNK release decides which terraform layer
// and which F5 CRDs apply; the IBM platform capability (single- vs multi-NIC
// ROKS) decides how the cluster is built and what the BNK layer has to attach to.
// They interact — multi-NIC changes the IBM module AND the F5 network attachments
// AND CNEInstance options — so both have to be known before either phase plans.
//
// NEITHER AXIS ADDS A REQUIRED SETTING. The BNK line is DERIVED from
// bnk.manifest_version, which every workspace already sets and which already pins
// the F5 chart that FLO and CIS versions are extracted from. Deriving it rather
// than adding a field means no existing config.yaml changes, and no workspace has
// to learn a second way to say the same thing.

// manifestVersionRe pulls the BNK line out of a manifest version.
//
//	2.3.0-3.2598.3-0.0.170  →  2.3
//	2.4.0-…                 →  2.4
//
// Deliberately anchored and narrow: a manifest string that does not start with
// <major>.<minor> is not something to guess at, and a wrong guess would silently
// select the wrong terraform layer.
var manifestVersionRe = regexp.MustCompile(`^(\d+)\.(\d+)(?:[.\-]|$)`)

// BNKLine returns the BNK release line this workspace targets, e.g. "2.3".
//
// Returns an error rather than a default when the manifest version is missing or
// unparseable. A default here would pick a terraform layer and a set of CRDs on a
// guess, and be discovered as an apply failure against a real cluster.
func (w *Workspace) BNKLine() (string, error) {
	if w == nil {
		return "", fmt.Errorf("no workspace")
	}
	// EMPTY IS NOT MISSING INFORMATION — it is the default, and the default is a
	// known version. bnk.manifest_version has always been optional: absent means
	// the HCL's own default is what gets installed, so the line is perfectly
	// derivable and refusing here would turn an optional field into a required
	// one. Every workspace that never set it — including the BNK Forge
	// bnk-install module, whose manifest_version input defaults to blank — would
	// stop working at `bnk up`.
	//
	// A non-empty value that cannot be parsed is different: something was meant
	// and cannot be honoured, and guessing would pick a terraform layer and a set
	// of CRDs on that guess.
	mv := strings.TrimSpace(w.BNK.ManifestVersion)
	if mv == "" {
		mv = DefaultManifestVersion
	}
	m := manifestVersionRe.FindStringSubmatch(mv)
	if m == nil {
		return "", fmt.Errorf("bnk.manifest_version %q does not start with <major>.<minor>, so the BNK release line cannot be derived from it", mv)
	}
	return m[1] + "." + m[2], nil
}

// ClusterNetworkMode returns the configured worker attachment mode.
//
// Empty means single-nic — the mode every cluster built so far uses — so an
// untouched config.yaml keeps its exact behaviour.
func (w *Workspace) ClusterNetworkMode() string {
	if w == nil || strings.TrimSpace(w.Cluster.NetworkMode) == "" {
		return NetworkModeSingleNIC
	}
	return strings.TrimSpace(w.Cluster.NetworkMode)
}

// ValidNetworkMode reports whether s names a mode the tool knows.
func ValidNetworkMode(s string) bool {
	switch s {
	case NetworkModeSingleNIC, NetworkModeMultiNIC:
		return true
	}
	return false
}

// BNKLineOrEmpty is BNKLine for callers that must not fail on a malformed
// manifest version.
//
// Selecting a terraform overlay is not the right place to reject a bad version
// string: the base tree is exactly today's behaviour, so falling back to it is
// safe, and guardSupportedCombination refuses the run with a proper message
// before anything is applied. Two refusals for one cause, with the worse-worded
// one arriving first, is not an improvement.
func (w *Workspace) BNKLineOrEmpty() string {
	line, err := w.BNKLine()
	if err != nil {
		return ""
	}
	return line
}

// CheckNetworkMode validates the configured mode and, when a cluster already
// exists, checks that the config still describes the cluster that was built.
//
// Lives here rather than in either caller because `cluster up` and `bnk up` must
// give the SAME answer: two copies of this rule would eventually disagree, and
// the disagreement would show up as one command refusing what the other allows.
//
// out == nil (or a record with no cluster) is not a failure — there is nothing to
// contradict yet, and this exists to catch a contradiction rather than to add a
// new way for a first run to fail.
func CheckNetworkMode(ws *Workspace, out *ClusterOutputs) error {
	mode := ws.ClusterNetworkMode()
	if !ValidNetworkMode(mode) {
		return fmt.Errorf("cluster.network_mode %q is not a mode this build knows (%s or %s)",
			mode, NetworkModeSingleNIC, NetworkModeMultiNIC)
	}
	if out == nil || out.ClusterID == "" {
		return nil
	}

	// SILENCE IS NOT AN ASSERTION. An unset network_mode means the config does
	// not have an opinion, so it cannot contradict the record — only an
	// EXPLICIT value can. The distinction is invisible through
	// ClusterNetworkMode(), which collapses unset to single-nic, so read the raw
	// field here.
	//
	// This matters because config.yaml is not always hand-written and durable.
	// The BNK Forge modules regenerate it per step from a curated env list
	// (`init --override-from-env --non-interactive` against a shared workspace),
	// so a setting the cluster-creating step passed is simply absent by the time
	// the installing step runs. Treating that absence as "the user asks for
	// single-nic" would refuse a correct multi-NIC deployment at its second
	// step, for a contradiction nobody expressed.
	if ws == nil || strings.TrimSpace(ws.Cluster.NetworkMode) == "" {
		return nil
	}

	if got := out.Network(); got != mode {
		return fmt.Errorf(
			"cluster %q was created as a %s cluster, but this workspace now asks for %s.\n\n"+
				"  A cluster's network mode is fixed when it is built — converting one in place is\n"+
				"  not supported, and continuing would plan a REPLACEMENT of the running cluster\n"+
				"  rather than a change to it.\n\n"+
				"  Either set cluster.network_mode back to %s, or create a new cluster in a new\n"+
				"  workspace for %s",
			out.ClusterName, got, mode, got, mode)
	}
	return nil
}
