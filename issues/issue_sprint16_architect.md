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
