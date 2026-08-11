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
	mv := strings.TrimSpace(w.BNK.ManifestVersion)
	if mv == "" {
		return "", fmt.Errorf("bnk.manifest_version is empty, so the BNK release line cannot be determined — set it to a published f5-bigip-k8s-manifest version such as %s", DefaultManifestVersion)
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
