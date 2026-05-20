# Sprint 15 — architect issues (consolidation cycle, post-v1.5.0)

> **Sprint 15 frame.** Consolidation / debt-paydown cycle targeting
> `v1.6.0` (integrator may re-designate `v1.5.1` under strict SemVer —
> **no user-visible behavior change** this cycle; version/tag is
> integrator-owned at cut). Design surface = `docs/PLAN.md` §"Sprint 15"
> (integrator-authored). **No PRD, no book surface.** Runs at the
> **consolidation tier** per `NEW_PROJECT_STARTING_POINT.md`
> §"Tiering the sprint process by change size": full staff + validator,
> light architect + tech-writer.
>
> **Integrator decisions (decided — do not relitigate; see
> `prompts/sprint15/README.md` and `docs/PLAN.md` §"Sprint 15"):**
> 1. Headline gate is **behavior parity** — entire pre-existing suite,
>    incl. the Sprint 14 e2e/`--on` suite, passes with **zero
>    test-file diffs**. An edited pre-existing test = drift, not a fix.
> 2. The Sprint 14 e2e/`--on` suite is the **parity harness**, not a
>    deliverable — consume it unchanged; do not rebuild/modify it.
> 3. `cli` decomposition is **phase 1 = exactly `lifecycle.go` +
>    `cluster.go`** → `internal/orchestration`; the other ~27 `cli`
>    files are a deferred tracked follow-up.
> 4. Must not regress the Sprint 14 kubeconfig fix (cloud-init + `--on`
>    self-heal); per-AZ stale-target reconcile option (b) stays
>    post-`v1.6.0`.

`Status: open | in-progress | resolved | wontfix | accepted`.


---

## Issue 1: CHANGELOG `v1.6.0` block authored (the only authored deliverable)
**Severity**: low
**Status**: resolved
**Files affected**: `CHANGELOG.md`
### What changed / What was verified
Added the consolidation-cycle release block above `## v1.5.0`. Authored per
`docs/PLAN.md` §"Sprint 15" §"Gate to `v1.6.0` tag" (line 1058) — PLAN is the
integrator-authored spec; the block mirrors prior blocks' Keep-a-Changelog
structure.

- **Heading**: used the held-block precedent `## Unreleased (v1.6.0)` (same
  convention `v1.5.0` used while held). Version + date is **integrator-owned**;
  an HTML comment above the heading documents that the cut is a one-line swap
  to `## v1.6.0 — <date>` *or* `## v1.5.1 — <date>` (strict-SemVer
  re-designation — no API/behavior change), and that the block body is
  version-agnostic so no body edit is needed on that swap.
- **Intro**: frames the cycle as internal consolidation (single path/env
  chokepoint retiring the boundary-bug *class*; phase-1 `internal/cli`
  decomposition), cross-links `docs/PLAN.md §"Sprint 15"`, states plainly
  **no user-visible behavior change** (v1.5.0 upgrader sees identical
  behavior), and that the bug class was already fixed per-instance in
  v1.4.1/v1.5.0 — this changes *how* not *whether*.
- **`### Changed`**: two bullets — `cli.ResolvedFlags` single invocation-time
  chokepoint (per-RunE `--var-file`/`--tf-source` + `--on` env now produced
  once; no RunE / `dispatchRemote` re-derives), and `internal/cli` →
  `internal/orchestration` phase-1 move (`lifecycle.go` + `cluster.go`). Both
  explicitly "identical to v1.5.0 / behavior-preserving".
- **`### Removed`**: the defensive `remoteSafeEnv` / `localPathEnvKeys`
  env-scrub, obviated by the chokepoint. **The exact as-landed disposition
  (deleted outright vs. demoted to one boundary assertion) is marked with an
  `INTEGRATOR-RECONCILE` comment** — see Issue 2.
- **No `### Added` / `### Fixed`**: omitted by design — zero features, zero
  user-facing fixes this cycle (the class was already fixed per-instance).
- **`### Deferred`**: carries v1.5.0's list forward unchanged + adds `cli`
  decomposition phases 2+ and re-states per-AZ reconcile option (b) as
  tracked post-`v1.6.0`.

Verified the block satisfies PLAN §"Gate" line 1058 exactly: `### Changed`
(internal consolidation, explicitly "no user-visible behavior change") +
`### Removed` (env-scrub obviated by chokepoint), no Added/Fixed, "no behavior
change" stated. PLAN ↔ CHANGELOG consistent — **no discrepancy, PLAN not
edited.**

## Issue 2: `### Removed` scrub disposition pending staff §Closure (integrator-reconcile)
**Severity**: low
**Status**: resolved — integrator-reconciled 2026-05-18 against the as-landed code (staff ledger §Closure still unfilled, so reconciled directly from source). As-landed: the scattered `localPathEnvKeys` list is **deleted**; `remoteSafeEnv`/`workspaceEnv`/`workspaceEnvCore` are **demoted to one-line delegating wrappers** over `internal/orchestration` (single source of truth, chokepoint-guard-asserted) — NOT deleted outright. The CHANGELOG `### Removed` bullet + the `INTEGRATOR-RECONCILE` comment were rewritten to state this precisely; no user-visible effect (KUBECONFIG still never crosses the `--on` boundary).
**Files affected**: `CHANGELOG.md` (`### Removed` bullet), `issues/issue_sprint15_staff.md` (read-only)
### What changed / What was verified
At authoring time `issues/issue_sprint15_staff.md` had **only the kickoff
seed** ("_No issues filed yet_") — no §Closure, so it is **not yet on record**
whether staff *deleted* the `remoteSafeEnv`/`localPathEnvKeys` scrub outright
or *demoted it to a single boundary assertion*. PLAN §"Sprint 15" permits
either ("deleted or demoted to one unreachable-by-construction assertion").
Per the dispatch instruction not to guess, the `### Removed` bullet states the
superseded-by-chokepoint fact (true under **both** outcomes, with the same
zero user-visible effect) and carries an explicit `INTEGRATOR-RECONCILE` HTML
comment. **Action for the integrator at integration**: read the staff
§Closure, confirm delete-vs-demote, and tighten the bullet's parenthetical to
the as-landed code. No CHANGELOG fact is wrong as written; only the
delete/demote phrasing is provisional. Stays `open` until reconciled.

## Issue 3: PLAN / NEW_PROJECT consistency + no-book-surface — verified
**Severity**: low
**Status**: resolved
**Files affected**: (verify-only — none authored) `docs/PLAN.md`, `NEW_PROJECT_STARTING_POINT.md`, `book/`
### What changed / What was verified
Clean light-cycle verification — all consistent, nothing re-authored:

- **PLAN ↔ CHANGELOG**: `docs/PLAN.md` §"Sprint 15" §"Gate" (line 1058)
  requirement matches the authored `v1.6.0` block (Changed/Removed/Deferred;
  no Added/Fixed; "no behavior change" stated). **No discrepancy** — PLAN
  untouched (integrator-authored, out of scope).
- **`NEW_PROJECT_STARTING_POINT.md` §"Tiering the sprint process by change
  size"**: exists (committed `f38f171`), internally consistent. The
  **Consolidation** tier row ("staff + validator, *light*
  architect/tech-writer (no PRD, no book) … behavior-parity") matches the
  Sprint 15 dispatch. PLAN §"Process deliverable" (PLAN line 1036) points at
  this exact section name — **pointer correct**. Verified consistent, not
  re-authored. *Minor non-blocking observation (out of architect scope —
  NEW_PROJECT already landed, verify-only): line 484's worked-example
  reference reads `docs/PLAN.md` §"Sprint 14"; the consolidation-tier worked
  example is structurally Sprint 15 (Sprint 14 is the get-well cycle). Noted
  for the integrator; not edited — verify-only surface, no behavior/gate
  impact.*
- **Book audit**: `grep -rn "v1.5.0\|v1.6.0\|chokepoint\|orchestration"
  book/src/` — the only authored-source hits are the already-shipped
  v1.5.0 auto-jumphost feature (chs. 15/16, correct as-is) and an unrelated
  config field name (`bnk-orchestration` in ch. 12). A targeted
  `v1.6.0\|chokepoint\|ResolvedFlags\|consolidation` grep over `book/src/`
  returns **no matches**. (`book/book/html/` hits are generated build
  artifacts, not authored surface.) **No book change is needed** — this is
  an internal-only refactor with no user surface. Recorded explicitly so the
  tech-writer / validator do not expect a book delta.
- **`mdbook build book/`**: clean by no-op **by construction** — zero edits
  to `book/src/` (verified above). `mdbook` is not installed in this
  architect environment (exit 127); the actual exit-0 gate runs in the
  validator/integrator environment. Since no book file changed, there is no
  way for the build to differ from `v1.5.0`'s passing state.

**Outcome: a clean light-cycle ledger.** Two `resolved` records (CHANGELOG
authored; PLAN/NEW_PROJECT/book consistency verified) and one `open`
integrator-reconcile flag (scrub delete-vs-demote, pending staff §Closure —
Issue 2). This is the correct result for a consolidation-tier light cycle, not
a sign of missed scope.
