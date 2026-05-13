# PRD 06 — cluster/trial phase split: `bnk` command group + composite lifecycle

> Post-v1.0 follow-up; not part of the original PRD 00-05 "trim host tools" arc.
>
> Prerequisites: v1.0.x cluster-phase work (the existing `roksbnkctl cluster up/down/register/show` lives at `internal/cli/cluster_phase.go` and writes `~/.roksbnkctl/<workspace>/cluster-outputs.json`).
>
> Estimated effort: small-to-medium (~400 LOC + tests + book chapter edits); ~1 week.

## Goal

Make the two-phase shape (durable cluster underneath, short-lived BNK trial on top) the **default** lifecycle, while preserving v1.0.x single-state behavior for already-deployed workspaces. Add a `roksbnkctl bnk` command group so a trial can be torn down without destroying the cluster underneath, and convert the unscoped `roksbnkctl up` / `down` into shape-aware composites that delegate to the right phase commands.

## Why

The v1.0.x flow has two pain points:

1. **`roksbnkctl down` is all-or-nothing for users who didn't opt into the two-phase split.** Anyone who ran a plain `roksbnkctl up` ended up with the cluster modules and the BNK trial modules in the same `terraform.tfstate`. Tearing down the trial — common during iterative BNK testing — required destroying the cluster too (a 30-minute rebuild). The two-phase commands (`cluster up` + register-then-up) solve this, but discoverability is poor: new users never find them.

2. **The mental model has no command for "the trial layer."** We ship `roksbnkctl cluster up/down` for the cluster phase, and `roksbnkctl up/down` for "everything," but there's no `roksbnkctl bnk up/down` for "just the trial." Users who do learn about the cluster-phase split still have to remember that the unscoped `up`/`down` means "trial when cluster exists, both when it doesn't" — coupling that fights the orthogonality of the surrounding cluster commands.

Both pain points compound: a user iterating on a BNK trial against a stable cluster has no clean teardown command, so they either keep state directories straight by hand or take the 30-minute hit per iteration.

## Scope

### In scope

- New top-level command group `roksbnkctl bnk` with two subcommands:
  - `roksbnkctl bnk up` — provisions the trial against an existing cluster; bootstraps the cluster phase first (with a confirmation prompt) when none is registered.
  - `roksbnkctl bnk down` — destroys the trial only; leaves the cluster phase intact.
- Shape detection (`config.DetectShape`) that classifies a workspace based on on-disk state alone (no `terraform` calls):
  - `ShapeEmpty` — neither phase has resources.
  - `ShapeClusterOnly` — cluster phase populated, trial phase empty.
  - `ShapeSplit` — both phases populated independently (the new normal).
  - `ShapeLegacySingle` — trial state contains cluster-phase modules from a pre-split `roksbnkctl up` run.
- Composite `roksbnkctl up` / `down` that dispatch on shape:
  - `up` on Empty/Split/ClusterOnly → `cluster up` then trial-state apply.
  - `up` on LegacySingle → monolithic trial-state apply (preserves v1.0.x behavior byte-for-byte).
  - `down` on Empty → error "nothing to destroy."
  - `down` on Split → trial-state destroy, then `cluster down`.
  - `down` on ClusterOnly → `cluster down`.
  - `down` on LegacySingle → monolithic trial-state destroy.
- Refusal logic on the phase-scoped commands:
  - `cluster up` refuses on `ShapeLegacySingle` (cluster lives in the trial state; applying the cluster phase would create a duplicate).
  - `cluster down` refuses on `ShapeLegacySingle`, `ShapeSplit`, and `ShapeEmpty` (only operates on `ShapeClusterOnly`; replaces the v1.0.x warning-but-prompt behavior with a hard refusal when trial state exists).
  - `bnk up` and `bnk down` refuse on `ShapeLegacySingle` (can't isolate the trial when it shares state with the cluster).
  - `bnk down` refuses on `ShapeEmpty` and `ShapeClusterOnly` (no trial to destroy).
- Refusal messages point at the resolution: "use `roksbnkctl up`/`down`" for legacy, "use `roksbnkctl bnk down` first" for trial-on-top scenarios.
- **`roksbnkctl status` reflects the phase split** (Sprint 10 scope addition) — the v1.0.x status output shows a single "Last apply" line drawn from `state/terraform.tfstate` mtime, which conflates the cluster phase and the BNK trial under the new shape. Sprint 10 adds two per-phase lines, each reading the corresponding state file independently, so a reader can tell at a glance which phase is currently deployed without running `cluster show` + inspecting tfstate by hand. See §"`status` command integration" under §"Design".

### Out of scope

- **`roksbnkctl migrate` command** — splitting an existing legacy single-state workspace's tfstate into separate `state/` + `state-cluster/` trees via `terraform state mv`. Real engineering effort and one-shot state surgery. Deferred until a real legacy user asks. Refusal messages reference it as future work.
- **`roksbnkctl bnk plan` / `bnk apply` / `cluster plan` / `cluster apply`** — top-level `plan` / `apply` already operate on the trial state and that behavior is unchanged. Symmetry additions deferred to a later cycle.
- **Docker backend composition** — the current docker dispatch in `runTrialUp`/`runTrialDown` covers the trial state. The new composite `runUp` will call `runClusterUp` (which has no docker shortcut today) followed by trial apply. In docker mode on empty/split workspaces, cluster apply runs locally and the trial step runs in docker — almost certainly not what users want. The composite explicitly disables itself on non-local backends for the empty/split paths; legacy single-state and the direct `cluster up`/`bnk up` calls retain v1.0.x docker behavior. A follow-up PRD covers full docker-mode composition.
- **Multiple BNK trials on one cluster** — `cluster-outputs.json` already supports the pattern (each `bnk up` in a fresh workspace reuses the registered cluster), but UX polish around switching trials, naming trials, and the "which trial is current" prompt is left for a separate effort.
- **OpenShift cluster auto-registration on first `bnk up`** — users still go through `cluster register` for clusters they didn't provision via `cluster up`. Auto-discovery is plausible (we already have `ibm.GetCluster`) but is its own scope.

## Design

### Shape detection

```go
// internal/config/tfstate.go
type WorkspaceShape int

const (
    ShapeUnknown WorkspaceShape = iota
    ShapeEmpty
    ShapeClusterOnly
    ShapeSplit
    ShapeLegacySingle
)

func DetectShape(workspace string) (WorkspaceShape, error)
```

Signals (all on-disk, no terraform calls):

| Signal | How | Tells us |
|---|---|---|
| Trial state has any resources | `len(state.resources) > 0` on `state/terraform.tfstate` | Trial phase has been applied |
| Cluster state has any resources | same check on `state-cluster/terraform.tfstate` | Cluster phase has been applied |
| Trial state contains cluster modules | walk `state.resources[]`, match `module` field against `module.roks_cluster`, `module.cert_manager`, `module.testing` (the modules that `deploy_bnk=false` in `cluster_phase.go` provisions) | Legacy single-state — cluster and trial share one tfstate |

Missing tfstate files → "no resources" (workspace not applied yet). Malformed JSON → surfaced as error so dispatch doesn't silently misroute.

The cluster-module match uses `strings.HasPrefix(r.Module, prefix+".")` plus exact equality to cover both root-of-module and nested-module addresses. Empirically verified against the canada-roks workspace (135 resources, includes `module.roks_cluster`, `module.roks_cluster.module.cluster`, `module.cert_manager`, `module.testing.*`, plus `module.flo.*`, `module.cne_instance.*`, `module.license.*`).

### Dispatch table

| Command | Empty | ClusterOnly | Split | LegacySingle |
|---|---|---|---|---|
| `up` | `cluster up` → trial up | trial up | `cluster up` (no-op refresh) → trial up | monolithic trial up |
| `down` | error: nothing to destroy | `cluster down` | trial down → `cluster down` | monolithic trial down |
| `bnk up` | confirm + `cluster up` → trial up | trial up | trial up | refuse |
| `bnk down` | refuse: no trial | refuse: no trial | trial down | refuse |
| `cluster up` | `cluster up` | `cluster up` (no-op refresh) | `cluster up` (no-op refresh) | refuse |
| `cluster down` | refuse: nothing to destroy | `cluster down` | refuse: trial exists | refuse |

"Trial up" / "trial down" denote the existing terraform-apply / terraform-destroy paths against `state/` (the v1.0.x `runUp` / `runDown` bodies, factored out as `runTrialUp` / `runTrialDown` private helpers).

The composite `up` and `down` are pure dispatchers — they detect shape, log the chosen path, and delegate to the leaf commands. No business logic in the composite itself.

### Command surface

```
roksbnkctl
├── up                 composite: shape-aware, calls cluster + bnk underneath
├── down               composite: shape-aware, calls bnk + cluster underneath
├── plan               trial-scoped (unchanged from v1.0.x)
├── apply              trial-scoped (unchanged from v1.0.x)
├── cluster
│   ├── up             cluster-only; refuses on LegacySingle
│   ├── down           cluster-only; refuses if trial state non-empty
│   ├── register       unchanged
│   └── show           unchanged
└── bnk                NEW
    ├── up             trial-only; auto-bootstraps cluster (with confirm) if missing
    └── down           trial-only; leaves cluster in place
```

Existing `cluster up` / `cluster down` keep their flags (`--auto`, `--var-file`, `--no-kubeconfig`). The new `bnk up` / `bnk down` add the same flag set so users have one mental model.

### `bnk up` auto-bootstrap UX

```
$ roksbnkctl bnk up
No cluster registered for this workspace.
→ Provisioning the cluster phase first (ROKS cluster + transit gateway +
  registry COS + cert-manager + jumphost; ~30 min) before the BNK trial.
Continue? [y/N]
```

Gated by `--auto` for CI. Skipped entirely when `cluster-outputs.json` is present (`ShapeClusterOnly` or `ShapeSplit`). The cluster phase's own confirmation prompt still fires inside the nested `cluster up` call — users see two prompts in the empty-workspace case (one for "do you want to bootstrap the cluster phase," one for "apply this terraform plan"). Acceptable for a 30-minute operation; revisit if user feedback objects.

### Refusal messages

Every refusal points at a concrete resolution:

| Shape + command | Message |
|---|---|
| `bnk up` on `LegacySingle` | `this workspace is legacy single-state; `bnk up` can't isolate the trial phase. Use `roksbnkctl up` for in-place behavior, or migrate the state first` |
| `bnk down` on `LegacySingle` | `this workspace is legacy single-state; `bnk down` can't isolate the trial phase. Use `roksbnkctl down` to tear down both, or migrate the state first` |
| `bnk down` on `Empty`/`ClusterOnly` | `no BNK trial state to destroy in this workspace` |
| `cluster up` on `LegacySingle` | `this workspace was provisioned with v1.0.x single-state — its cluster lives in the trial state file. Use `roksbnkctl up` to operate on it, or migrate the state to two-phase shape first` |
| `cluster down` on `LegacySingle` | `this workspace is legacy single-state; cluster and BNK trial share one state. Use `roksbnkctl down` to tear down both, or migrate the state first` |
| `cluster down` on `Split` | `BNK trial state exists in this workspace; run `roksbnkctl bnk down` first (or `roksbnkctl down` to tear down both phases)` |
| `cluster down` on `Empty` | `nothing to destroy in this workspace` |
| `down` on `Empty` | `nothing to destroy in this workspace` |

The "migrate the state first" references describe a `roksbnkctl migrate` command that does not exist yet and is out of scope (see §"Out of scope"). The refusals point at it so the message stays valid once the command lands; users who hit the refusal today get the unambiguous alternative (the unscoped `up` / `down`).

### `status` command integration (Sprint 10 scope addition)

`runStatus` in `internal/cli/inspect.go` consumes `config.DetectShape` and emits two per-phase deployment lines instead of the v1.0.x single `Last apply` line that conflates the two phases. Output shape by `WorkspaceShape`:

| Shape | New status lines (replaces the single "Last apply" line) |
|---|---|
| `ShapeEmpty` | `Cluster phase:  not deployed`<br>`BNK trial:      not deployed` |
| `ShapeClusterOnly` | `Cluster phase:  deployed (last apply 2026-05-13 14:08:33 MST)`<br>`BNK trial:      not deployed` |
| `ShapeSplit` | `Cluster phase:  deployed (last apply 2026-05-13 14:08:33 MST)`<br>`BNK trial:      deployed (last apply 2026-05-13 14:15:01 MST)` |
| `ShapeLegacySingle` | `Shape:          legacy single-state (cluster + trial in one tfstate)`<br>`Last apply:     2026-05-13 14:15:01 MST` (existing v1.0.x line preserved) |

Per-phase last-apply timestamps come from each state file's mtime — `<state-dir>/terraform.tfstate` for the BNK trial, `<state-cluster-dir>/terraform.tfstate` for the cluster phase. Pattern already in use elsewhere in `runStatus`. Failures to read either state file (e.g., directory missing on a fresh workspace) degrade silently to "not deployed" rather than surfacing an error — every section in `runStatus` is best-effort by convention.

For `ShapeLegacySingle`, the chapter-8/10/11 reframe already tells v1.0.x users their shape; the status output adds a one-line shape callout so the reader sees "I'm on legacy single-state" at a glance without having to grep the docs. The existing v1.0.x `Last apply` line is preserved verbatim in this shape only so existing scripts that parse status output don't break.

The Sprint 10 architect mirrors this design in chapter 24 (Day-2 ops) where `status` output is documented, and validator's live-verification adds a `status` invocation against the four shapes (matches the dispatch matrix's structure).

## Implementation tasks

| Order | Item | Files |
|---|---|---|
| 1 | `WorkspaceShape` enum, `DetectShape`, `tfstateHasResources`, `trialStateHasClusterModules`; remove the duplicate `tfstateHasResources` already in `workspace.go` | `internal/config/tfstate.go` (new), `internal/config/workspace.go` (edit) |
| 2 | `bnk` cobra group; `bnk up` with cluster-bootstrap pre-flight; `bnk down`; flag wiring matching `cluster up`/`down` | `internal/cli/bnk_phase.go` (new) |
| 3 | Refactor `runUp` body to `runTrialUp` (private helper, same body); add composite `runUp` dispatcher keyed on `DetectShape`. Same for `runDown` → `runTrialDown`. | `internal/cli/lifecycle.go` (edit) |
| 4 | `runClusterUp` adds `ShapeLegacySingle` refusal; `runClusterDown` adds `ShapeLegacySingle` / `ShapeSplit` / `ShapeEmpty` refusals; remove the old "warning-but-prompts-anyway" trial-exists copy in `runClusterDown` | `internal/cli/cluster_phase.go` (edit) |
| 5 | Unit tests: shape detection against synthetic tfstate fixtures (one per shape); dispatch tests for `runUp`/`runDown`/`runBnk*`/`runCluster*` via fake state directories | `internal/config/tfstate_test.go` (new), `internal/cli/bnk_phase_test.go` (new), extensions to `internal/cli/lifecycle_test.go` if it exists |
| 6 | CHANGELOG entry under Unreleased / `v1.1.0`; book chapter updates (chapter 10 "Deploying BNK trials" gains a `bnk up`/`bnk down` section; chapter 11 "Tearing down" gains a phase-aware decision matrix; chapter 8 "The cluster phase" cross-links to `bnk`) | `CHANGELOG.md` (edit), `book/src/10-*.md`, `book/src/11-*.md`, `book/src/08-*.md` (edits) |
| 7 (Sprint 10) | `runStatus` (`internal/cli/inspect.go`) consumes `config.DetectShape` + each phase's `terraform.tfstate` mtime; emits the per-phase deployment lines per §"`status` command integration". Replaces the v1.0.x single "Last apply" line for non-`LegacySingle` shapes; preserves it verbatim for `ShapeLegacySingle` to keep existing scripts parsing status output stable. Chapter 24 (Day-2 ops) updated to document the new lines with a sample per-shape. Unit test against the four-shape fixture set in `internal/config/testdata/` (reuses Sprint 8's fixtures). | `internal/cli/inspect.go` (edit), `book/src/24-day-2-ops.md` (edit), `internal/cli/inspect_test.go` (new or extend) |

A reference prototype lives on the `spike/bnk-phase-split` branch (commit `00181d0`). Empirical evidence that the shape detector correctly identifies the real canada-roks legacy state. The branch is **reference only** — the staff agent re-implements from this PRD; the spike is not for merge.

## Acceptance criteria

- `go build ./...`, `go test ./...`, `go vet ./...`, `gofmt -d -l .` all clean.
- `roksbnkctl bnk --help` lists `up` and `down`.
- New shape-aware behavior verified against the `canada-roks` workspace (or an equivalent legacy single-state workspace):
  - `roksbnkctl -w <legacy> bnk down` → refuses with legacy-single-state error.
  - `roksbnkctl -w <legacy> cluster down` → refuses with legacy-single-state error.
  - `roksbnkctl -w <legacy> down` → still operates as today (asks for confirmation, then monolithic destroy).
- New shape-aware behavior verified against an empty workspace:
  - `roksbnkctl -w <empty> bnk down` → "no BNK trial state to destroy."
  - `roksbnkctl -w <empty> down` → "nothing to destroy."
- New shape-aware behavior verified against a split workspace (cluster-only after `cluster up` succeeds on a fresh workspace):
  - `roksbnkctl -w <cluster-only> cluster down` → no longer refuses; runs the cluster destroy.
  - `roksbnkctl -w <cluster-only> bnk up` → no cluster-bootstrap prompt; proceeds straight to the trial.
- Live verification on a real IBM Cloud workspace (sandbox-permitting): `cluster up` → `bnk up` → `bnk down` → `bnk up` cycle leaves the cluster intact across the down/up pair; `cluster down` after the second `bnk down` succeeds without orphans.
- **Sprint 10**: `roksbnkctl status` against each of the four shapes (empty, cluster-only, split, legacy-single-state) emits the expected per-phase deployment lines per §"`status` command integration". `ShapeLegacySingle` still emits the v1.0.x `Last apply` line verbatim (script-compat). The four-shape `internal/config/testdata/` fixture set from Sprint 8 is the basis for the new `inspect_test.go` table test.

## Open questions

- **Double-prompt UX in `bnk up` on empty workspace** — the outer `bnk up` asks "bootstrap the cluster phase first?" then the inner `cluster up` asks "apply this plan?". Two prompts for one user command. Tolerable for a 30-minute operation; thread `--auto` through if user feedback says otherwise.
- **`cluster up` no-op refresh on `ShapeSplit`** — composite `up` calls `cluster up` even when the cluster is already healthy, partly to keep `cluster-outputs.json` fresh. Terraform's no-op plan is fast (~10s) but the refresh hits IBM Cloud APIs. If users with stable clusters notice the latency, add a `--skip-cluster-refresh` flag to the composite.
- **Should composite `up` propagate `--auto` to the inner `cluster up` and trial apply automatically?** Today the flag's package-level var (`flagAuto`) is shared, so it does — but the semantics of `--auto` for a composite are subtler than for a leaf command (skips both confirmations). Document explicitly in the help text rather than splitting into separate flags.
- **Migration timing.** The PRD scopes out `roksbnkctl migrate` but the refusal messages reference it. Three options for when migrate lands: (a) ship the refusals now, add migrate when a real legacy user asks; (b) ship migrate alongside this PRD as a stretch goal; (c) defer the LegacySingle refusals too and only ship the new bnk surface. The PRD chooses (a) on the rationale that legacy users have the working `up`/`down` flow and aren't blocked.

## Related work

- **PRD 03 (execution backends)** — the docker-backend composition gap noted in §"Out of scope" is a follow-up. Future PRD will define how the composite dispatcher composes phase-by-phase across backends.
- **v1.0.x cluster-phase commit history** — `cluster up`/`down`/`register`/`show` shipped in (search the changelog for the cluster-phase entry); the design of the two-phase shape is established. This PRD builds on it.
- **Book chapter 8** ("The cluster phase") — current text describes `cluster up`/`down` as the *opt-in* two-phase mode; this PRD makes the two-phase shape the *default* shape for new workspaces, so chapter 8 needs a framing edit alongside the new `bnk` chapter material.
