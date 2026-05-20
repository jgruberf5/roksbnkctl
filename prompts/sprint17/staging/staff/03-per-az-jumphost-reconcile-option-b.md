---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: per-AZ jumphost stale-target reconcile (PRD 09 option (b)) with a `auto:`/`managed_by:` ownership marker so re-`up` after a zone change prunes orphaned `jumphost-<oldzone>` targets'
labels: []
assignees: ''
---

## Motivation

Today (`v1.6.2`), `roksbnkctl up` on a workspace with
`testing_create_cluster_jumphosts = true` upserts a target per
cluster-VPC availability zone (`jumphost-ca-tor-1`, `jumphost-ca-tor-2`,
…) from the `testing_cluster_jumphost_ips` terraform output (PRD 09;
`internal/orchestration/lifecycle.go::tryAutoClusterJumphosts`). The
landed behaviour is the integrator-chosen **option (a) upsert-only**:
when a zone is later removed (e.g. the deploy moves from a 3-AZ to a
2-AZ shape) or `testing_create_cluster_jumphosts` is flipped to `false`,
the now-orphaned `jumphost-<oldzone>` target lingers in
`config.TargetCfg` until the user runs `roksbnkctl targets remove
jumphost-<oldzone>` by hand. `--on jumphost-<oldzone>` then dials a
floating IP that may belong to someone else.

PRD 09 §"Open questions" + CHANGELOG `### Deferred (v1.x roadmap,
post-v1.6.0)` track option (b) reconcile as the post-`v1.5.0`
follow-up; the blocker named there is unambiguous ownership ("a user's
hand-named `jumphost-mybox` is never deleted"). With the cluster-VPC
auto-registration loop now battle-tested across `v1.5.0` and `v1.6.x`
and the post-Sprint 16 stable phase-handoff, this is the right cycle to
land the ownership marker + the reconcile sweep.

## Proposed surface

No new top-level verb. Two surface deltas:

1. **`config.TargetCfg` schema gains `Auto bool`** (yaml key
   `auto: true|false`). When `auto: true`, the entry was written by an
   auto-discovery hook (`tryAutoJumphost` / `tryAutoClusterJumphosts`)
   and is owned by roksbnkctl. Hand-written entries (the manual
   `targets add` path) stay `auto: false` (the absent-key default) and
   are NEVER touched by reconcile.

2. **`tryAutoClusterJumphosts` becomes upsert + reconcile** rather
   than upsert-only. After upserting `jumphost-<zone>` for every key in
   the current `{zone => fip}` map, it sweeps the workspace's target set
   for entries matching the prefix `jumphost-` AND `Auto: true`, AND
   not in the current map — and deletes them.

```
roksbnkctl up                         # registers/refreshes; prunes orphans
roksbnkctl targets list               # auto-managed rows marked [auto]
```

- The singular `jumphost` (TGW jumphost; `tryAutoJumphost`) also gets
  `Auto: true` set so it's symmetric, but it is NOT swept (the singular
  is a stable name; there is no orphan-class for a singleton).
- No new flag on `up` to opt out of reconcile. Users who want a
  hand-named `jumphost-mybox` use a name that doesn't match the auto-
  managed pattern (any prefix other than `jumphost-<zone-shape>`) OR
  explicitly leave `auto: false` (in which case reconcile ignores it
  even with a matching name).

## Behavior

- Happy path: post-apply, `tryAutoClusterJumphosts` reads the
  `testing_cluster_jumphost_ips` map, upserts each `jumphost-<zone>`
  target with `Auto: true`, THEN walks the workspace's targets and
  deletes every `name` where (a) name starts with `jumphost-`, (b)
  `Auto == true`, (c) `name` is NOT in the upserted set. Logs one
  `→ Pruned orphan target jumphost-<oldzone>` per deletion. The TGW
  singular `jumphost` is exempt by name (the sweep filter is the
  per-AZ prefix, not the singular).
- Empty `{zone => fip}` map / `testing_create_cluster_jumphosts =
  false`: the auto-managed per-AZ target set goes empty. The reconcile
  sweep prunes every `jumphost-<zone>` target with `Auto: true` in the
  workspace's set. The singular `jumphost` is unchanged (it's owned by
  the TGW jumphost hook, exempt by name).
- Missing / failed `tryAutoClusterJumphosts` output read: same
  best-effort behaviour as v1.6.2 — one `warning:` to stderr, does NOT
  prune (we can't tell orphans from "we just couldn't read"). Prune
  only fires when the output read SUCCEEDS.
- Hand-written `jumphost-prod` (with `auto: false` / no `auto:` key):
  reconcile leaves it alone regardless of whether it matches the
  prefix. The marker is the ownership truth, not the name.
- Migration: a pre-v1.x workspace has every existing `jumphost-<zone>`
  target written without an `auto:` key. The first re-`up` against
  such a workspace upserts each per-AZ target with `Auto: true`
  (re-writing them in place). Targets that exist on disk but are not
  in the current map are NOT auto-pruned on first run because they
  legitimately have `Auto: false` (the absent-key default) at that
  moment — a single-line one-time migration assertion in
  `tryAutoClusterJumphosts` (covered by tests) flags this so users get
  one informational stderr line on first re-`up` after upgrade.
- Interaction with `--workspace` / `--quiet` / `--verbose`: unchanged;
  the reconcile sweep emits `→ Pruned …` only when not `--quiet`, on
  stderr, mirroring the existing post-apply hook noise.
- Filesystem side-effects: writes the workspace's targets YAML (the
  same writer the upsert path uses), nothing else. No IBM Cloud /
  terraform call.

## Acceptance criteria

1. `config.TargetCfg` gains an `Auto bool` field with `yaml:"auto,omitempty"`.
   Existing on-disk targets without the key load as `Auto: false`.
   `remote.SetTarget` is unchanged (callers explicitly set `Auto` in
   the `TargetCfg` they pass).
2. `tryAutoJumphost` and `tryAutoClusterJumphosts` set `Auto: true` on
   every `TargetCfg` they pass to `remote.SetTarget`. A hermetic test
   in `internal/orchestration/` (additive — new `_test.go` file,
   never editing a pre-existing one) asserts this for both hooks
   using the existing fixture map.
3. New `tryAutoClusterJumphosts` reconcile-sweep step: after the
   upsert loop, list the workspace's targets, and delete every entry
   whose name has the `jumphost-` prefix, whose `Auto == true`, and
   whose name is NOT in the current upsert set. Hermetic test
   asserts: (a) orphan deleted, (b) hand-written `auto: false`
   `jumphost-prod` kept, (c) singular `jumphost` kept (excluded by
   name).
4. Empty `{zone => fip}` map → sweep prunes EVERY auto-managed
   `jumphost-<zone>` target. Hermetic test pins this case.
5. Output-read failure path (terraform output unavailable) does NOT
   prune. Hermetic test pins the "no-prune-on-output-failure"
   contract.
6. One-time migration log line on first re-`up` against a workspace
   whose pre-existing `jumphost-<zone>` targets carry no `auto:`
   key — names the targets being re-marked, fired exactly once per
   workspace (idempotent on subsequent runs because the re-write
   stamps `auto: true`). Hermetic test pins this on a fixture.
7. PRD 09 §"Open questions" item 1 + §"Out of scope" item 1 + CHANGELOG
   `### Deferred (v1.x roadmap, post-v1.6.0)` item "Per-AZ jumphost
   stale-target reconcile (option (b))" all move to a fresh CHANGELOG
   `### Added` (or `### Changed`) entry naming this issue, in the
   release cycle that picks this up.
8. The PRD 09 §"Open questions" item 2 — "Should the singular
   `jumphost` also gain the `auto:` marker for symmetry?" — is
   resolved YES as part of acceptance #2 (the marker is set on the
   singular too), but the singular is not swept (per the §"Behavior"
   note above).

## Out of scope (deliberately)

- Renaming the `Auto` field to `ManagedBy: "roksbnkctl"` /
  `Owner: "auto"` or any other shape — PRD 09 §"Open questions" item 1
  lists both options; a `bool` is the cleanest and is what this issue
  asks for. Re-shaping to a typed enum is a follow-up if a second
  auto-source ever needs distinguishing.
- A `--no-reconcile` opt-out flag on `up` — orthogonal; not asked for
  here.
- A `targets list --auto` filter or `[auto]` column in `targets list`
  rendering — useful follow-up, separate issue. (acceptance #2 makes
  this easy to wire later.)
- Reconcile semantics for non-jumphost auto-registered targets — no
  such hooks exist today.
- Generalising the sweep to a `roksbnkctl targets reconcile` CLI verb —
  the post-apply hook is enough for the user-reported confusion class;
  a manual verb is a follow-up.

## Files likely touched

- `internal/config/targetcfg.go` (or wherever `TargetCfg` lives — grep
  `type TargetCfg struct`) — add `Auto bool` field + yaml tag.
- `internal/remote/targets.go` — likely no API change (`SetTarget`
  already takes a full `TargetCfg`); add a `RemoveTarget(workspace,
  name) error` if no equivalent exists, or document the existing one
  by name.
- `internal/orchestration/lifecycle.go` — `tryAutoJumphost` +
  `tryAutoClusterJumphosts` set `Auto: true`; the per-AZ hook gains
  the reconcile-sweep loop after its upsert loop.
- `internal/orchestration/lifecycle_test.go` (or a new
  `_test.go` file in the same package — additive, not editing a
  pre-existing test).
- `docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md` §"Open questions" item 1 +
  §"Out of scope" item 1 — flipped to "Resolved in <release>".
- `CHANGELOG.md` — new `### Added`/`### Changed` entry, drop the
  Deferred bullet.
- `book/src/15-ssh-targets.md` and `book/src/16-on-flag-ssh-jumphosts.md`
  — drop the v1.6.x "orphan caveat" wording; document the
  reconcile + `auto:` marker.

## Notes

This is one of two named post-`v1.5.0` follow-ups carried unchanged
across `v1.5.0`, `v1.6.0`, `v1.6.1`, `v1.6.2` (CHANGELOG `### Deferred
(v1.x roadmap, post-v1.5.0)` and `(post-v1.6.0)` both name it). The
other (`internal/cli` decomposition phases 2+) is a separate refactor
track; this issue is the user-facing UX track.

Sprint 13 staff Issue 3 + architect Issue 2 (resolved-Sprint-13)
defined option (a) as the v1.5.0 shape with option (b) deferred; the
integrator decision was "land (b) when orphaned-target confusion is
reported" — that signal has accumulated across two release cycles.
