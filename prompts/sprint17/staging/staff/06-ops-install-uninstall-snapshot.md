---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: `ops install` / `ops uninstall` snapshot — record the K8s-side install footprint the way `terraform.applied.tfvars` records the terraform-side one (closes PRD 07 §"Open questions" #1)'
labels: []
assignees: ''
---

## Motivation

PRD 07 (Sprint 11 `terraform.applied.tfvars`) gave the terraform-side
lifecycle a canonical, on-disk record of what was deployed: a
`~/.roksbnkctl/<workspace>/state[-cluster]/terraform.applied.tfvars`
snapshot written after every successful `terraform apply`, audit-grade,
secret-redacted. PRD 07 §"Open questions" #1 named the matching gap for
the K8s side: `ops install` / `ops uninstall` mutate cluster-side state
(Namespace, ServiceAccount, ClusterRole, ClusterRoleBinding, Secret,
Pod, plus — under `--trusted-profile=auto/on` — the IBM IAM trusted
profile + claim rule + policies) but leave NO local record of what they
did. The same audit / re-deploy / hand-off workflows that motivated PRD
07 are blind on the ops-pod side today.

This recurs in real operator workflows:

- "Which trusted-profile is bound to this workspace's ops pod, and
  when?" — there's no on-disk answer; the operator runs `ops show` or
  `kubectl describe sa` to recover state from the live cluster.
- "Were the v1.0.x static-key Secret OR the v1.2+ trusted-profile path
  used here?" — knowable only from a live cluster query (`ops show`),
  which fails if the cluster is down or unreachable.
- "What `--trusted-profile=…` value did we pick on the last
  `ops install`?" — no record; the integrator decision is in stderr
  log scrollback only.

CHANGELOG `### Deferred (v1.x roadmap)` from `v1.4.0` onward has named
this item every release; it carries forward unchanged in v1.5.0,
v1.6.0, v1.6.1, v1.6.2.

## Proposed surface

No new top-level verb. A new on-disk snapshot file written by the
existing `ops install` / `ops uninstall` paths, named symmetrically with
PRD 07:

```
~/.roksbnkctl/<workspace>/ops.applied.json
```

JSON (not HCL — there's no `terraform`-shaped surface here to mirror;
JSON parses everywhere and the existing tooling already consumes
`cluster-outputs.json` JSON):

```json
{
  "schema": "roksbnkctl.ops.v1",
  "recorded_at": "2026-05-20T11:04:52Z",
  "roksbnkctl_version": "v1.7.0",
  "verb": "install" | "uninstall",
  "namespace": "roksbnkctl",
  "trusted_profile": {
    "flag": "auto" | "on" | "off",
    "resolved": "auto-success" | "auto-fallback" | "on" | "off",
    "profile_id": "<iam profile id, empty when off/auto-fallback>",
    "profile_name": "roksbnkctl-ops-<workspace>"
  },
  "service_account": {
    "name": "roksbnkctl-ops",
    "annotations": {
      "iam.cloud.ibm.com/trusted-profile": "<name, empty when off>",
      "roksbnkctl.io/trusted-profile-managed": "true|false"
    }
  },
  "secret": {
    "name": "roksbnkctl-ops-creds",
    "rotated_at": "<value of the existing roksbnkctl.io/rotated-at annotation>",
    "has_api_key": true | false
  },
  "pod": {
    "name": "roksbnkctl-ops",
    "image": "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v1.7.0"
  }
}
```

`ibmcloud_api_key` value is NEVER persisted (mirrors PRD 07
§"Redaction list"); only `has_api_key: true|false` records its
presence in the Secret.

## Behavior

- Successful `ops install`: after the manifest apply completes, write
  `ops.applied.json` capturing the resolved values. Idempotent on
  re-`ops install` — file is rewritten each time. Atomic-rename
  pattern (mirroring `internal/config/applied_tfvars.go`'s tempfile
  + rename) so a crashed mid-write doesn't leave a half-file. Mode
  `0600` to match.
- Successful `ops uninstall --confirm`: after the cluster-side
  uninstall completes, write a final `ops.applied.json` with
  `verb: "uninstall"` and the cluster-side fields that the uninstall
  resolved (e.g. trusted-profile deletion best-effort outcome). The
  PRD 07 §"Resolved design decisions" #2 ("Destroy leaves the prior
  snapshot intact") DOES NOT apply here — the `ops uninstall` case
  IS the user-visible audit record, where terraform's destroy was a
  side-effect-only verb. Rationale: `ops uninstall` is rare and the
  "what state are we in now" answer must reflect the most recent
  verb.
- Failed `ops install` partway through: best-effort write capturing
  the state at the failure point. Failure mode: log-and-continue
  per PRD 07 §"Anti-patterns to avoid" #4 (the apply already
  succeeded or failed on its own merit; the snapshot is an audit
  output, never the primary signal).
- `ops show` adds a final line `Snapshot: ~/.roksbnkctl/<ws>/ops.applied.json`
  pointing at the file. No data exchange between the two — they are
  independent.
- The file is `ops.applied.json` regardless of phase split — the ops
  pod is workspace-scoped (one per workspace), not phase-scoped.

## Acceptance criteria

1. New `internal/config/ops_applied.go` (mirrors
   `applied_tfvars.go`): `WriteOpsApplied(workspace string, snap
   OpsApplied) error` + `OpsAppliedPath(workspace) (string, error)`
   + the `OpsApplied` struct matching the JSON schema in §"Proposed
   surface". Mode `0600`, atomic rename, ASCII-clean (no system-
   specific characters).
2. `internal/cli/ops.go` calls `WriteOpsApplied` after every
   successful `ops install` and every successful `ops uninstall
   --confirm` with the resolved fields. Failure is logged-not-failed.
3. `ops show` adds the `Snapshot: <path>` line when the file exists
   (silently absent when not). Hermetic test pins both branches.
4. New `internal/config/ops_applied_test.go` (additive — never
   editing a pre-existing test): table-driven test asserts
   round-trip JSON shape, atomic-rename behaviour, mode `0600`,
   absence of any `ibmcloud_api_key` value bytes anywhere in the
   written file even when `has_api_key: true`.
5. `internal/cli/ops_test.go` (additive) covers: (a) `ops install
   --trusted-profile=auto` success writes a snapshot with
   `resolved: "auto-success"` + a non-empty `profile_id`; (b) `auto`
   IAM-403 fallback writes `resolved: "auto-fallback"` + empty
   `profile_id`; (c) `--trusted-profile=off` writes `resolved:
   "off"`; (d) `ops uninstall --confirm` writes a snapshot with
   `verb: "uninstall"`.
6. PRD 07 §"Open questions" item 1 is flipped to "Resolved in
   <release>" and CHANGELOG's recurring `### Deferred` bullet "`ops
   install` / `ops uninstall` snapshot" is dropped.
7. Book chapter 6 (workspaces) gains a §"`ops.applied.json` — what's
   installed on cluster right now" subsection mirroring the existing
   §"`terraform.applied.tfvars` — what's deployed right now" — same
   shape, same redaction language.

## Out of scope (deliberately)

- Reading the snapshot back into roksbnkctl on a later `ops install`
  / `ops uninstall` to validate against the live cluster — the file
  is an OUTPUT, not an INPUT (mirrors PRD 07 §"Anti-patterns to
  avoid" #3).
- Surfacing the snapshot from the in-cluster `ops` pod (the file
  lives on the operator's workstation; the pod is unaware).
- An `ops diff` verb that compares the snapshot against the live
  cluster state — a useful follow-up, separate issue.
- A snapshot of the cluster-side trusted-profile's IAM policies — the
  policies are determined from the IBM Cloud account, not local
  config; recording them inflates the file and stales fast.
- Backfilling existing `ops install`-ed workspaces with a synthesised
  snapshot — the file is a record of FUTURE installs only; a
  first-run `ops show` after upgrade shows no `Snapshot:` line until
  the next `ops install`.

## Files likely touched

- `internal/config/ops_applied.go` (new file).
- `internal/config/ops_applied_test.go` (new file, additive).
- `internal/cli/ops.go` — write the snapshot at the end of every
  successful `ops install` / `ops uninstall --confirm`; add the
  `Snapshot: <path>` line to `ops show`.
- `internal/cli/ops_test.go` (additive — new test functions only).
- `docs/prd/07-DEPLOYED-TFVARS.md` §"Open questions" item 1 — flip
  to "Resolved in <release>".
- `CHANGELOG.md` — new `### Added` entry naming this issue; drop the
  recurring `### Deferred` bullet.
- `book/src/06-workspaces.md` — new §"`ops.applied.json`" subsection.

## Notes

This is the smallest follow-up that closes PRD 07 §"Open questions"
item 1 — single new file path on disk, no surface change to existing
verbs except `ops show` gaining one line. The bigger ask ("an `ops
diff` that compares snapshot vs live cluster") is deliberately a
separate follow-up.

The schema string `roksbnkctl.ops.v1` mirrors the
`roksbnkctl.dns.v1.vantage` / `roksbnkctl.dns.v1` convention from
Sprint 5 DNS-probe JSON output and the `cluster-outputs.json` shape
already in `internal/config/`.
