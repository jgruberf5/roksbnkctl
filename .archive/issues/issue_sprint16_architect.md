# Sprint 16 — architect issues (consolidation phase-1b, post-v1.6.0)

> **Sprint 16 frame.** Light role. Only write surface: `CHANGELOG.md`
> + this ledger. No PRD, no book. `docs/PLAN.md` §"Sprint 16" is
> integrator-authored — verify consistency, do not rewrite. Do not date
> the CHANGELOG heading or pick the version (`v1.6.1` vs `v1.7.0`) —
> integrator-owned at cut. See `prompts/sprint16/architect.md`.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — CHANGELOG `### Changed` block + PLAN-consistency + no-surface (light cycle)

`Status: resolved` — recorded by the integrator (light role, consolidation tier; no separate architect agent dispatched, per `prompts/sprint16/README.md`).

- **CHANGELOG.** New top block above `## v1.6.0 — 2026-05-18`: heading left `## Unreleased (v1.6.1 / v1.7.0)` with the integrator-owned-heading comment refreshed (date/version is a one-line edit at cut; not decided here). Intro + a single `### Changed` bullet: `internal/cli` phase-1b decomposition (lifecycle + cluster/remote-passthrough orchestration → `internal/orchestration`; `cli` thin adapter; function-field DI so `orchestration` never imports `cli`); explicitly **"no user-visible behavior change"**, completes Sprint-15 phase-1a, remaining ~27 files = tracked phase-2. No `### Added`/`### Fixed` (none). Staff §Closure was filled, so specifics are stated from as-landed code — no `INTEGRATOR-RECONCILE` needed.
- **PLAN consistency.** `docs/PLAN.md` §"Sprint 16" + the §"Sprint 15 → Scope decision" / Sprint-16 carry-over are mutually consistent with the as-landed code (lifecycle+cluster moved, `cli` thin, one-directional boundary). No drift; PLAN not rewritten.
- **No surface.** No PRD, no book this cycle (internal-only) — recorded; nothing to drift.

---

## Issue 2 — CHANGELOG/PLAN follow-up (post-`v1.6.1` phase-handoff regression)

**Severity**: low (docs-only follow-up; tracks the `high` validator Issue 2 fix)

**Status**: resolved

**Description.** Post-`v1.6.1` live `!` verify surfaced validator Issue 2 — the `up` second (bnk/testing) phase re-created the cluster phase's already-made cluster VPC / transit gateway / client VPC, so IBM Cloud rejected the run with duplicate-name errors; the phase-1b parity gate was correct-but-blind to it (no hermetic test exercises a post-cluster-phase workspace). Per integrator decision 1 (`prompts/sprint16/followup-issue2-README.md`) this is a **user-facing bugfix**, so:

- **CHANGELOG.** Added a new `## v1.6.2 — 2026-05-19` section **above** `## v1.6.1`, with a `### Fixed` block (not `### Changed` — `v1.6.1` was "no user-visible behavior change"; this is the opposite, a user-facing fix per decision 1). One entry: `up` no longer fails partway through with an IBM Cloud `Provided Name … is not unique` / `A gateway with the same name already exists` duplicate when the bnk/testing phase runs after the cluster phase — the second phase now reuses the cluster-phase VPC / transit gateway / client VPC via the `cluster-outputs.json` handoff (`use_existing_cluster_vpc` + `existing_cluster_vpc_id` + `testing_create_client_vpc=false`) instead of re-creating same-named resources. User-facing tone; cross-links `docs/PLAN.md §"Sprint 16"` and `issues/issue_sprint16_validator.md` Issue 2.
- **PLAN.** Appended an additive `### Follow-up (post-\`v1.6.1\`)` subsection to §"Sprint 16" (existing §"Sprint 16" text **unchanged** — note is purely additive): records the live-verify-surfaced regression, that the parity gate was correct-but-blind (no hermetic test exercised a post-cluster-phase workspace), the fix (existing-resource handoff completed in terraform + Go, shipped as `v1.6.2`), and that closure is gated on the live `!` verify per the `live-verify-high-issues` discipline. Cross-links validator Issue 2.

**Version tag is integrator-owned.** The `## v1.6.2 — 2026-05-19` heading is written for the expected patch shape (decision 1), but the actual version/tag is **integrator-owned at cut** and is **gated on the live `!` verify** of validator Issue 2 (decision 3 + `live-verify-high-issues`): `high`-severity Issue 2 cannot be closed on unit/hermetic tests alone, so neither the tag nor the Issue 2 closure is finalized here. Final closure is integrator/operator-owned after the live run.

**Files affected**:
- `CHANGELOG.md` (new `## v1.6.2 — 2026-05-19` section with `### Fixed`, above `## v1.6.1`)
- `docs/PLAN.md` (additive `### Follow-up` subsection inside the existing §"Sprint 16"; no existing text rewritten)

**Related**: `issues/issue_sprint16_validator.md` Issue 2 (the `high` bug being fixed; closure live-`!`-gated); `prompts/sprint16/followup-issue2-README.md` (integrator decisions 1/3); `prompts/sprint16/followup-issue2-architect.md` (this dispatch); `issues/issue_sprint16_staff.md` Issue 1 (phase-1b split that introduced the gap); memory `live-verify-high-issues`. Did **not** commit — the integrator commits.
