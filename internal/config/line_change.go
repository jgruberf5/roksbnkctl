package config

import "fmt"

// THE BNK RELEASE LINE IS A CREATE-TIME SETTING.
//
// bnk_line is derived from bnk.manifest_version, so moving a workspace from a
// 2.3.* manifest to a 2.4.* one flips the line — and that is not an upgrade the
// tool can perform in place.
//
// THE HARM THIS PREVENTS. Three things make a 2.3 -> 2.4 flip destructive in
// ways terraform cannot see:
//
//   - GTM object naming changed between the lines. 2.3 writes
//     server_<tmm_selfip>; 2.4 writes
//     server_<digitalassetid>_<cluster-name>_<ns>_<ip>. The old objects live on
//     an EXTERNAL BIG-IP — outside the cluster and outside terraform state — so
//     nothing here deletes them, and the guide is explicit that leaving both
//     formats in place causes device IP conflicts (#172).
//
//   - The network model is replaced wholesale. 2.4 runs with
//     USE_GATEWAY_SETTINGS=true, under which the controller ignores the
//     cloud-network-mapping ConfigMap and the whole F5SPK* family that a 2.3
//     install created. Those objects do not migrate; they are simply no longer
//     read, and they stay on the cluster (#168).
//
//   - ~30 CWC license secrets survive a teardown and must be removed before a
//     reinstall re-licenses cleanly (#172).
//
// None of that appears in a plan. A line flip reads as a version string
// changing.
//
// So the line is fixed at install time. This REFUSES rather than warns, for the
// same reason the namespace topology guard does: the damage is off-cluster and
// unrecoverable by re-running, so a warning the operator scrolls past is not a
// fair trade for the convenience of not blocking.
//
// The escape is a new workspace, which costs a new install and nothing else.

// CheckLineChange refuses a BNK release line that differs from the one this
// workspace last applied.
//
// applied is the BNK phase's applied-tfvars snapshot. An empty or missing
// snapshot means nothing is known to have been installed, so there is nothing to
// contradict — the same silence the namespace guard keeps, and for the same
// reason: this exists to catch a contradiction, not to invent a new way for a
// first run to fail.
func CheckLineChange(w *Workspace, applied map[string]string) error {
	if w == nil || len(applied) == 0 {
		return nil
	}
	prior := tfvarString(applied["bnk_line"])
	if prior == "" {
		// Installed before bnk_line was rendered. The snapshot cannot say which
		// line it was, and guessing from a manifest that may itself have changed
		// is how a guard produces a false accusation. Stay silent.
		return nil
	}
	// A malformed manifest is BNKLine's error to report, not this one's.
	current := w.BNKLineOrEmpty()
	if current == "" || current == prior {
		return nil
	}
	//lint:ignore ST1005 multi-line actionable operator message; trailing period is intentional and matches the internal sentences
	return fmt.Errorf(`this workspace installed BNK %[1]s and the config now selects BNK %[2]s.

  The release line is fixed at install time; moving between lines in place is not
  supported, and the reasons are all things terraform cannot see or undo:

    - GTM objects on the external BIG-IP use a different naming format per line.
      The %[1]s-format objects are outside the cluster and outside terraform state,
      so nothing here removes them, and leaving both formats causes device IP
      conflicts.
    - %[2]s replaces the network model rather than migrating it: the objects a
      %[1]s install created are no longer read, and they stay on the cluster.
    - The CWC license secrets from the %[1]s install survive and must be cleared
      before a reinstall licenses cleanly.

  To run BNK %[2]s, use a NEW workspace and a new install. To keep this one, set
  bnk.manifest_version back to a %[1]s release.`, prior, current)
}
