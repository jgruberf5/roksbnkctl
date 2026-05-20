---
name: Feature request
about: Propose a new command, flag, or capability for roksbnkctl
title: 'feat: `--skip-cluster-refresh` flag on composite `roksbnkctl up` so a stable `ShapeSplit` workspace skips the inner cluster-phase `terraform plan` no-op'
labels: []
assignees: ''
---

## Motivation

Today, composite `roksbnkctl up` on a `ShapeSplit` workspace (cluster
phase + trial phase both already deployed) unconditionally calls
`in.RunClusterUp(ctx)` BEFORE the trial-phase work
(`internal/orchestration/lifecycle.go::RunUp`, lines 126-134). The
intent is documented in PRD 06 §"Open questions" #2 and in the inline
comment at `RunUp` line 129 — "cluster up is a no-op refresh ... partly
to keep `cluster-outputs.json` fresh." For a healthy split workspace
where the user just wants to re-apply trial-layer drift, the inner
cluster-phase `terraform plan` is pure latency: it hits IBM Cloud APIs
(VPC / cluster / TG describe calls), takes ~10-30 seconds, and produces
no changes — but the user has no way to skip it.

This bites two real workflows:

1. **Iterating on bnk-trial-layer tfvars only.** A user editing
   `flo`/`cne_instance`/`license`/`cert_manager` inputs runs `up`
   repeatedly; every iteration pays the cluster-phase refresh tax.
2. **CI pipelines** that already know the cluster is healthy (a
   separate scheduled `cluster up` job, or a hand-rolled probe) — the
   per-run cluster refresh is wasted spend.

PRD 06's own §"Open questions" names this exact knob: "If users with
stable clusters notice the latency, add a `--skip-cluster-refresh` flag
to the composite." The hour has come.

## Proposed surface

A boolean flag on the composite `up` only (not on `cluster up` or `bnk
up`):

```
roksbnkctl up [--skip-cluster-refresh] [--auto] [--var-file <path>] ...
```

- `--skip-cluster-refresh` — when set on a `ShapeSplit` workspace,
  skip the inner `RunClusterUp` and proceed straight to `RunTrialUp`.
  Required: no. Default: `false` (current behaviour).

Behaviour on each detected shape:

- `ShapeSplit` — `--skip-cluster-refresh` skips the inner
  `RunClusterUp`. Without the flag, behaviour is unchanged.
- `ShapeEmpty` — `--skip-cluster-refresh` is a HARD ERROR
  (`refusing --skip-cluster-refresh: workspace has no cluster phase
  yet, can't skip what's needed`). The cluster phase is mandatory
  here; silently ignoring would mislead.
- `ShapeClusterOnly` — `--skip-cluster-refresh` is a SILENT NO-OP
  (composite already skips the inner cluster up — there's no trial
  state). Document this in `--help`.
- `ShapeLegacySingle` — `--skip-cluster-refresh` is a HARD ERROR
  (single-state has no separable cluster phase to skip; the option
  doesn't apply).

## Behavior

- Happy path on `ShapeSplit`: `roksbnkctl up --skip-cluster-refresh -w
  e2e` runs the trial-phase preamble (`writeAndInitSecondPhase` —
  which still reads `cluster-outputs.json` if present, so the
  second-phase override still fires correctly), then `terraform plan`,
  prompt-or-`--auto`, `terraform apply`, post-apply hooks. Skipped:
  the inner `in.RunClusterUp` call. Logs one stderr line
  `→ Skipping cluster-phase refresh (--skip-cluster-refresh; assumes
  the cluster is healthy)`.
- `--skip-cluster-refresh` does NOT affect any error path: a
  missing/corrupt `cluster-outputs.json` still surfaces in
  `loadReuseClusterOutputs` exactly as today.
- Interaction with `--auto`: orthogonal. `--auto
  --skip-cluster-refresh` is the common CI combination.
- Interaction with `--on`: the existing `rejectOnFlag("up")` is
  unchanged; `--on` on composite `up` still errors before the new
  flag is consulted.
- No filesystem side-effect change: `cluster-outputs.json` is not
  re-written by the trial phase (it's the cluster phase's
  responsibility), so skipping the inner cluster up means the file's
  mtime doesn't refresh — which is the point.
- Exit code semantics unchanged.

## Acceptance criteria

1. New `--skip-cluster-refresh` boolean flag on composite `up` only
   (not on `cluster up` or `bnk up`), default `false`. Surfaced in
   `--help` with the four-shape behaviour table above.
2. `LifecycleInputs` (`internal/orchestration/lifecycle.go`) gains a
   `SkipClusterRefresh bool` field; the cli adapter passes the flag
   value through the existing chokepoint.
3. `RunUp` on `ShapeSplit` skips the `in.RunClusterUp(ctx)` call when
   `in.SkipClusterRefresh == true` and emits one stderr line naming
   the assumption.
4. `RunUp` on `ShapeEmpty` with `SkipClusterRefresh == true` returns
   the documented hard-error (text reviewed for the standard
   `roksbnkctl`-error voice). Hermetic test pins the exact error
   string.
5. `RunUp` on `ShapeClusterOnly` with `SkipClusterRefresh == true`
   silently no-ops on the flag (no error, no stderr noise, no
   behaviour change). Hermetic test pins this.
6. `RunUp` on `ShapeLegacySingle` with `SkipClusterRefresh == true`
   returns the documented hard-error. Hermetic test pins it.
7. Additive `_test.go` in `internal/orchestration/` (NEW file, never
   editing a pre-existing test per the Sprint 16 parity rule) covers
   all four shape × flag-value combinations using the existing
   `internal/config/testdata/` fixture set Sprint 8 / Sprint 10's
   `inspect_test.go` already uses.
8. PRD 06 §"Open questions" item 2 is resolved (and the section
   updated to point at the issue resolution).

## Out of scope (deliberately)

- Re-shaping `up`'s composite into separate verbs — the dispatcher is
  fine; this is purely a new opt-in skip flag.
- A `--skip-trial` symmetric flag — orthogonal; users have `cluster
  up` already.
- Auto-detection of "cluster is healthy, skip the refresh" — explicit
  flag only. Silent skipping would be surprising. (Future: a probe
  hook can set the flag in CI configs.)
- Wiring `--skip-cluster-refresh` through `--backend docker` / `k8s` /
  `ssh:<target>` differently — the flag is at the composite-dispatcher
  layer, before any backend dispatch, so backends inherit it for free.
- Adding telemetry / a counter on how often the flag is used.

## Files likely touched

- `internal/cli/lifecycle.go` (the thin cobra adapter) — bind the new
  flag, pass through to `LifecycleInputs`.
- `internal/orchestration/lifecycle.go` — add the field on
  `LifecycleInputs`, gate the inner `in.RunClusterUp` call in
  `RunUp`'s `ShapeSplit` arm, add the hard-error / no-op arms for the
  other three shapes.
- `internal/orchestration/lifecycle_<test>.go` (new additive test file
  — NOT editing the pre-existing `lifecycle_e2e_test.go` /
  `lifecycle_e2e_integration_test.go`).
- `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` — flip §"Open questions"
  item 2 to "Resolved in <release>".
- `book/src/27-command-reference.md` (auto-generated by
  `tools/refgen/cobra-md`) — flag will surface automatically; rerun
  the generator.

## Notes

The PRD 06 open question already pre-named the flag, names the cause
(per-run IBM Cloud API call latency), names the audience ("users with
stable clusters"), and names the workflow trigger ("if users with
stable clusters notice the latency"). This issue is the implementation
of that named option.

Adjacent: a `cluster-outputs.json` mtime refresh is the OTHER reason
the inner refresh runs (named in the same code comment). Today the
refresh-without-changes path already calls `persistClusterOutputs(...,
"cluster-up")` in `runClusterUp` (`internal/cli/cluster_phase.go` line
326), which updates the `RecordedAt` field. Users who set
`--skip-cluster-refresh` lose that mtime refresh — that is the
acknowledged trade-off the flag opts into.
