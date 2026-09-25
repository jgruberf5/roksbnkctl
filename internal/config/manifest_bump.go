package config

import "fmt"

// A MANIFEST VERSION BUMP CANNOT BE APPLIED IN PLACE.
//
// bnk.manifest_version names the CNEManifest object:
// cnemanifest_name = lower("BNK-${var.f5_bigip_k8s_manifest_version}"). Bumping
// the version therefore RENAMES that object, and a rename is not an update —
// the provider has to create a new object — but terraform plans an update and
// then aborts:
//
//	module.flo.module.flo.kubectl_manifest.cnemanifest[0]
//	.name: was cty.StringVal("bnk-2.4.0-ea"), but now cty.StringVal("bnk-2.4.0")
//	.id / .uid / .live_uid: was known, but now unknown
//
// THE HARM THIS PREVENTS is not the failed plan. It is that the apply fails
// PARTWAY. Three attempts on a live cluster (#309) rolled every pod three
// times, exhausted FAR's pull quota —
//
//	Failed to pull image ".../crdupdater:v0.90.14-0.0.2": pull QPS exceeded
//
// — and left f5-cne-controller, f5-tmm and f5-dssm-sentinel-2 in
// ImagePullBackOff with NO CNEManifest on the cluster at all. BNK was down. The
// retry is what burned the quota, and nothing about the first failure suggested
// that retrying would make it worse.
//
// WHY THIS REFUSES RATHER THAN WARNS. vpc_cidr warns because it is an existing
// contract somebody may be relying on. This is the opposite: an in-place bump
// has NEVER worked — the apply cannot complete — so refusing takes away nothing
// anyone has today. It replaces a broken apply with a message.
//
// WHY NOT FIX THE RENAME INSTEAD. Three mechanisms were considered and rejected
// in #309:
//
//   - replace_triggered_by on a terraform_data holding the version. Tried, and
//     recorded on the issue so nobody repeats it: the trigger fires when the
//     referenced resource CHANGES, and a first-time CREATE is not a change. It
//     would work on the next bump and does nothing for the one in front of you.
//     Error count went 7 -> 15.
//   - a `moved` block. Requires static addresses; the target key depends on the
//     workspace's current manifest version, so it cannot be written generically.
//   - keying the resource on the manifest name via for_each, so a bump changes
//     the key and forces create/destroy. This WOULD work on the first bump, but
//     it moves the address from cnemanifest[0] to cnemanifest["bnk-2.4.0-ea"],
//     so every EXISTING workspace would destroy and recreate its CNEManifest on
//     the next ordinary apply. Deleting the CNEManifest is precisely what left
//     the cluster broken above. The cure is worse than the disease.
//
// The supported path is `bnk down` then `bnk up`, which is how the verified
// 2.4.0 GA upgrade was actually performed (37 added, 0 changed, 0 destroyed).

// CheckManifestVersionChange refuses a bnk.manifest_version that differs from
// the one this workspace last applied.
//
// applied is the BNK phase's applied-tfvars snapshot. An empty or missing
// snapshot means nothing is known to be installed, so there is nothing to
// contradict — the same silence CheckLineChange keeps, and for the same reason.
//
// A CROSS-LINE change is CheckLineChange's to report, not this one's: it runs
// first and its message is the more specific one. This catches the within-line
// bump (2.4.0-EA -> 2.4.0) that a line comparison cannot see.
func CheckManifestVersionChange(w *Workspace, applied map[string]string) error {
	if w == nil || len(applied) == 0 {
		return nil
	}
	prior := tfvarString(applied["f5_bigip_k8s_manifest_version"])
	if prior == "" {
		// Installed before the manifest version was recorded. Guessing from a
		// value that may itself have changed is how a guard produces a false
		// accusation.
		return nil
	}
	current := w.BNK.ManifestVersion
	if current == "" || current == prior {
		return nil
	}
	// Leave a line flip to CheckLineChange — two refusals for one edit is noise,
	// and its explanation is the one that matters.
	priorWS := &Workspace{BNK: BNKCfg{ManifestVersion: prior}}
	if priorLine := priorWS.BNKLineOrEmpty(); priorLine != "" {
		if currentLine := w.BNKLineOrEmpty(); currentLine != "" && currentLine != priorLine {
			return nil
		}
	}
	//lint:ignore ST1005 multi-line actionable operator message; trailing period is intentional and matches the internal sentences
	return fmt.Errorf(`this workspace installed BNK manifest %[1]s and the config now selects %[2]s.

  A manifest bump cannot be applied in place. The manifest version names the
  CNEManifest object, so changing it RENAMES that object — and terraform plans a
  rename as an in-place update, then aborts with "Provider produced inconsistent
  final plan". Re-running does not converge: the stale value is in terraform
  state, not on disk.

  The apply does not stop cleanly. It stops PARTWAY, having already rolled every
  pod, and a retry rolls them again — which is how a previous attempt exhausted
  the registry's pull quota and left BNK down with no CNEManifest on the cluster
  at all (#309).

  To move to %[2]s:

      roksbnkctl bnk down
      roksbnkctl bnk up

  That is the supported path and the one the 2.4.0 GA upgrade was verified with.
  To keep the current install, set bnk.manifest_version back to %[1]s.`, prior, current)
}
