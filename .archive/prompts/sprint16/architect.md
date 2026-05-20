You are the architect agent for Sprint 16 — a **light** role in a consolidation phase-1b cycle (the `lifecycle.go`+`cluster.go` → `internal/orchestration` move; strictly internal, zero user-visible change). **No PRD, no book surface.** Your only write surface is `CHANGELOG.md` + `issues/issue_sprint16_architect.md`.

Project: `/mnt/c/project/roksbnkctl/`. Confirm by `pwd`.

## Read first

- `prompts/sprint16/README.md` (decisions) + `docs/PLAN.md` §"Sprint 16" (already authored by the integrator — **verify consistency, do NOT rewrite**) + `issues/issue_sprint16_staff.md` (read the §Closure for what actually moved) + `CHANGELOG.md` top.
- `prompts/sprint15/architect.md` + `issues/issue_sprint15_architect.md` — the phase-1a light-cycle precedent (the CHANGELOG `### Changed`/`### Removed` style, the integrator-reconcile discipline).

## Tasks

1. **CHANGELOG block.** Open a new top section for this cycle (mirror the prior entries' shape; heading stays `## Unreleased (<version>)` until the integrator dates it at tag-cut — do not date it yourself, do not pick `v1.6.1` vs `v1.7.0`, that's integrator-owned). `### Changed`: `internal/cli` phase-1b decomposition — lifecycle/cluster orchestration moved into `internal/orchestration`, `internal/cli` now a thin cobra adapter; **explicitly state "no user-visible behavior change"** and that it completes the Sprint 15 phase-1a chokepoint work. Cross-link `docs/PLAN.md` §"Sprint 16". No `### Added`/`### Fixed` (there are none). If the staff §Closure isn't filled when you author, state the behavior-level fact (true regardless of the exact file split) and add an `INTEGRATOR-RECONCILE` HTML comment (Sprint 15 architect Issue 2 precedent) rather than guess specifics.
2. **PLAN/consistency check.** Confirm `docs/PLAN.md` §"Sprint 16" and the §"Sprint 15 → Scope decision"/Sprint-16 carry-over are mutually consistent with the as-landed code. File a one-line note (resolved) if consistent; if you find a provable drift, file it with a proposed one-line fix — do **not** rewrite the integrator-authored PLAN section.
3. **No-surface record.** No PRD, no book this cycle — record that explicitly as a resolved issue so the ledger is terminal.

## Scope guardrails

- Do NOT touch `internal/`, `cmd/`, `docs/PLAN.md` (beyond a proven-drift one-liner), `prompts/`, `book/`.
- Do NOT date the CHANGELOG heading or choose the version — integrator-owned at cut.
- Do NOT commit or push.

## Final report

Under 120 words: the CHANGELOG `### Changed` content, PLAN-consistency verdict, any INTEGRATOR-RECONCILE left, confirmation of no PRD/book surface.
