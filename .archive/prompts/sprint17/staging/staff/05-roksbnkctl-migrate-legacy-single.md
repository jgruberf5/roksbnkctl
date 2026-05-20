---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: `roksbnkctl migrate` — migrate a v1.0.x `ShapeLegacySingle` workspace to the modern two-phase split shape that `bnk`/`cluster` commands and the Sprint 16 phase-handoff need'
labels: []
assignees: ''
---

## Motivation

PRD 06 §"Open questions" (the §"Migration timing" bullet) records the
integrator's call to defer `roksbnkctl migrate` until a real legacy
user asks. The error-message language across `internal/cli/cluster_phase.go`
(lines 305, 368) and `internal/cli/bnk_phase.go` (lines 99, 138)
already names this command — four error paths refuse to operate on a
`ShapeLegacySingle` workspace and tell the user to "migrate the state
first." There is no `migrate` verb. The trail of references is a UX
trap: the error text promises a fix that doesn't exist.

Independently, every Sprint-15-onward `internal/orchestration` boundary
(applied-tfvars replay, second-phase reuse, cluster-shared override) is
designed against the two-phase shape. A legacy single-state workspace
that wants any of the v1.5.x+ ergonomics has no path forward except
"tear it down and re-deploy" — destructive, time-consuming
(~30+ min for the cluster phase), and risks data loss on the test
artifacts in the trial layer.

The minimum viable migration is a STATE MOVE (`terraform state mv`
loop) — the v1.0.x single state file is partitioned into a new
`state-cluster/terraform.tfstate` (the cluster-shared modules) and the
existing `state/terraform.tfstate` retains only the trial layer.
Workspace `config.yaml` shape detection then naturally reports
`ShapeSplit` and the rest of the v1.5.x+ surface unlocks.

## Proposed surface

A new top-level verb (the literal name PRD 06 + every error path
already references):

```
roksbnkctl migrate [--dry-run] [--auto] [--keep-backup]
```

- `--dry-run` — print the planned `state mv` operations + the
  cluster/trial resource partition, never write anything. Required:
  no. Default: false. Useful for `--dry-run` review before the
  destructive move.
- `--auto` — skip the confirmation prompt the migration emits before
  the first mutating step. Required: no. Default: false. Composes
  with `--quiet`.
- `--keep-backup` — keep `state.pre-migrate-<timestamp>.tfstate.backup`
  alongside both new state files for at least one cycle. Required:
  no. Default: true (the safer default for a one-shot migration).
  Users can re-run with `--keep-backup=false` to clean up post-verify.
- Reads `--workspace` from the global flag (or `flagWorkspace` env)
  exactly like every other lifecycle verb.

```
$ roksbnkctl migrate -w legacy-roks --dry-run
# prints the partition: 17 resources to state-cluster (module.roks_cluster*,
# module.testing client_vpc + jumphost network), 6 to state (the BNK
# trial layer flo/cne_instance/license/cert_manager); 0 to a
# discard-candidate set.
```

## Behavior

- Happy path on `ShapeLegacySingle`: detect shape, refuse if anything
  other than `ShapeLegacySingle` (a `ShapeSplit` / `ShapeClusterOnly` /
  `ShapeEmpty` workspace doesn't need migration; surface the no-op
  clearly). Emit the partition plan to stderr. Prompt unless
  `--auto`. On confirm: snapshot the legacy state to
  `state.pre-migrate-<ts>.tfstate.backup`, create
  `state-cluster/terraform.tfstate` by `terraform state mv -state=...
  -state-out=...` for every cluster-shared module address, leave the
  trial-layer modules in `state/terraform.tfstate`. Verify the
  post-move shape detects as `ShapeSplit`.
- `--dry-run`: emit the same plan, never write. Exits 0 if the
  partition is fully classifiable (every resource maps to either
  cluster or trial); exits non-zero with a clear "unclassifiable
  addresses found: <list>" message if it isn't (a corrupt or
  hand-modified legacy state has resources the migration can't
  place).
- `--keep-backup=true` (the default): the `.backup` file is left in
  place for the user to inspect.
- Empty/never-applied legacy workspace (`ShapeEmpty` masquerading as
  legacy because of `config.yaml` shape): refuse with the actionable
  "nothing to migrate — workspace state is empty; run `roksbnkctl
  init` for a fresh two-phase deploy" message.
- Already-modern workspace (`ShapeSplit` / `ShapeClusterOnly`): refuse
  with "this workspace is already in two-phase shape; nothing to
  migrate." Exit 0 (idempotent re-invocation).
- Error cases (each exits non-zero with a roksbnkctl-level message,
  not a raw terraform-exec dump):
  - state file missing / unreadable → `the workspace's legacy state
    at <path> is unreadable: <err>`
  - any `terraform state mv` fails mid-migration → roll back the
    partial new-state-cluster, leave the backup intact, surface the
    underlying terraform error verbatim
  - mid-process kill (SIGINT) → on next invocation, detect a
    `.in-progress` sentinel and refuse to start a second migration
    until the user removes the sentinel by hand (manual
    recovery — automatic resume would be wrong for a state move)
- Filesystem side-effects: writes to `<workspace>/state-cluster/`,
  `<workspace>/state/`, and one new `*.backup` file. No IBM Cloud
  call. No `terraform apply` / `destroy`.
- `--on` is rejected exactly like the other lifecycle verbs (state
  files are workstation-local; migration on a remote target is
  meaningless).

## Acceptance criteria

1. `roksbnkctl migrate -w <legacy>` against a `ShapeLegacySingle`
   workspace partitions its state into
   `<workspace>/state-cluster/terraform.tfstate` and
   `<workspace>/state/terraform.tfstate`, leaves a
   `state.pre-migrate-<ts>.tfstate.backup` in the original location,
   and `config.DetectShape` post-run reports `ShapeSplit`.
2. `--dry-run` lists the planned moves WITHOUT touching state and
   exits 0 if the partition is fully classifiable.
3. `--dry-run` exits NON-ZERO with a clean "unclassifiable
   addresses: <list>" message when the legacy state contains resource
   addresses the partition cannot place (corrupt / hand-modified
   state). Hermetic test pins this on a fixture with a planted unknown
   address.
4. `roksbnkctl migrate -w <split-or-cluster-only>` exits 0 with a
   clean "already in two-phase shape" message (no error). Hermetic
   test pins it.
5. `roksbnkctl migrate -w <empty>` refuses with the actionable
   message naming `roksbnkctl init`. Hermetic test pins it.
6. The error-message references to "migrate the state" in
   `internal/cli/cluster_phase.go:305,368` and
   `internal/cli/bnk_phase.go:99,138` are updated in lockstep to
   read "run `roksbnkctl migrate`" so the verb the user types
   matches the verb the docs name.
7. Hermetic test in `internal/cli/migrate_test.go` (NEW file, never
   editing a pre-existing test) covers the happy-path + the four
   refuse-or-no-op branches using fixture state files (small,
   hand-rolled tfstate JSON; no live terraform invocation needed for
   the dispatcher tests; the `state mv` step uses `terraform-exec`
   against a tmpdir).
8. **Live `!` verify required before close** (per the
   `live-verify-high-issues` memory) — destructive state move is a
   `high`-severity class. The validator owns a gated
   `scripts/e2e-migrate.sh` driver against a real legacy workspace
   (or a synthesised one from a pre-`v1.4.0` `up`), asserting the
   post-migration two-phase shape behaves identically to a freshly-
   deployed two-phase workspace (down / plan / apply / cluster down
   all green). Integrator/operator-run, not CI.
9. PRD 06 §"Migration timing" (open question item 4) is resolved
   and the section updated to point at the issue resolution.

## Out of scope (deliberately)

- Re-rendering the workspace's `config.yaml` to the latest schema —
  the schema has been backwards-compatible since v0.9, and a config
  refactor is orthogonal.
- Auto-deleting the `.backup` file — `--keep-backup=false` is the
  user's opt-in to remove it; the default preserves it.
- Bi-directional migration ("revert two-phase → legacy") — there is
  no use case for that direction.
- Migrating workspaces that were never deployed (`ShapeEmpty` legacy);
  re-`init` is the correct path.
- Lifting the `ShapeLegacySingle` refusals from `cluster up` / `bnk up`
  / `bnk down` / `cluster down` — keep the refusal but update the
  message text per acceptance #6.
- Wiring migration through `--backend docker` / `k8s` / `ssh:<target>` —
  state lives on the local filesystem; migration runs on the local
  workstation only (mirrors `terraform`'s read-only escape hatch in
  PRD 08).

## Files likely touched

- `internal/cli/migrate.go` (new file) — cobra `migrateCmd` + RunE
  dispatcher + the partition-and-`state mv` orchestration.
- `internal/cli/migrate_test.go` (new file) — hermetic test set.
- `internal/cli/cluster_phase.go` — update the error text on lines
  305 and 368 to name the new verb.
- `internal/cli/bnk_phase.go` — update the error text on lines 99
  and 138 to name the new verb.
- `internal/config/shape.go` (wherever `DetectShape` lives) — likely
  unchanged; the migration is upstream of detection. If a small
  helper is needed to enumerate the cluster-vs-trial module address
  partition, it lands here next to `ShapeSplit` / `ShapeLegacySingle`
  constants.
- `internal/tf/terraform.go` — possibly a thin `StateMv` wrapper
  around `tfexec.Tf.StateMv` if one doesn't already exist.
- `scripts/e2e-migrate.sh` (new) — gated live-verify driver
  (`DRY_RUN=1` + `IBMCLOUD_API_KEY=...` flow, mirroring
  `scripts/e2e-phase-handoff.sh`'s shape).
- `docs/E2E_TEST.md` — new §"State migration (Issue: this issue)"
  section describing how/when to run the driver.
- `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` §"Open questions" —
  resolve item 4.
- `book/src/06-workspaces.md` (or a new chapter section) — document
  the migration command + the `--dry-run` review workflow.

## Notes

- The error paths in `cluster_phase.go` (lines 305, 368) +
  `bnk_phase.go` (lines 99, 138) all already mention "migrate"; the
  search for that string surfaces every site that needs the text
  update in lockstep with the feature.
- The current `root.go:131` warning ("warning: found legacy state at
  <path> — move it to <path> to keep it (we won't auto-migrate)") is
  about a different transition (the v0.9 → v1.0 workspace-root move,
  not the legacy-single → split move) and should stay unchanged. The
  search-and-replace must NOT catch it.
- This is the larger of the two structural follow-ups paged in
  CHANGELOG `### Deferred (v1.x roadmap, post-v1.6.0)`; the other is
  `internal/cli` decomposition phases 2+ (separate issue,
  architect-side).
