You are the architect agent for Sprint 15 of the roksbnkctl project — a **consolidation cycle** with **zero user-visible behavior change**. This is a **light cycle** for your role: there is **no PRD** (zero new features) and **no book surface** (internal-only refactor — nothing in `book/` should change, and you must confirm that). Your only authored surface is the `CHANGELOG.md` `v1.6.0` block plus a consistency check of two already-landed docs. Your scope: `CHANGELOG.md`. **Do not touch `internal/`, `book/`, `prompts/`, or `docs/PLAN.md` (it is integrator-authored).**

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Confirm by `pwd`.

## Read first

- `prompts/sprint15/README.md` — sprint frame + the four integrator decisions; note the **consolidation tier** (your role is intentionally light).
- `docs/PLAN.md` §"Sprint 15" — §"Gate to `v1.6.0` tag" names exactly what the CHANGELOG block must contain. **Do not rewrite PLAN** — it is integrator-authored; read it as the spec.
- `CHANGELOG.md` — the existing `## v1.5.0 — 2026-05-18` block (the shipped release) and the Keep-a-Changelog conventions the file follows. Mirror the prior blocks' structure.
- `NEW_PROJECT_STARTING_POINT.md` §"Tiering the sprint process by change size" — already committed (`f38f171`); you verify it is final/consistent, you do **not** re-author it.

## Tasks

### 1. `CHANGELOG.md` — `v1.6.0` block (the only authored deliverable)

Add a new `## v1.6.0 — <date>` block (or `## Unreleased (v1.6.0)` if you follow the held-block precedent — match whatever the file's current convention is for an un-cut release; the integrator owns the tag/date and may re-designate `v1.5.1` under strict SemVer, so phrase the heading so a version/date swap is a one-line edit and note that explicitly in a comment or intro line):

- **Intro (~2 sentences)** — frames the cycle as an internal consolidation: one path/env normalization chokepoint retiring the recurring shell-CWD / SSH-boundary bug class as a *class*, plus phase-1 decomposition of the `internal/cli` god-package. Cross-link `docs/PLAN.md §"Sprint 15"`. State plainly: **no user-visible behavior change** (a v1.5.0 user upgrading sees identical behavior).
- **`### Changed`** — internal consolidation: the per-RunE `--var-file`/`--tf-source` normalization and the `--on` env handling are now produced by a single invocation-time chokepoint (`cli.ResolvedFlags`); lifecycle/dispatch orchestration moved from `internal/cli` into `internal/orchestration` (phase 1). Explicitly: behavior is identical to v1.5.0; this is structural hardening so the recurring boundary-bug class cannot reopen.
- **`### Removed`** — the defensive `remoteSafeEnv` / `localPathEnvKeys` env-scrub, obviated by the chokepoint (or "demoted to a single boundary assertion" — match what staff actually landed; coordinate via `issues/issue_sprint15_staff.md` §Closure, do not guess).
- **No `### Added` / `### Fixed`** — there are no features and no user-facing bug fixes this cycle (the bug *class* was already fixed per-instance in v1.4.1/v1.5.0; this only changes *how*, not *whether*). Do not imply a user-facing fix.
- **`### Deferred`** — carry forward v1.5.0's deferred list unchanged + note `cli` decomposition phases 2+ and per-AZ reconcile option (b) remain tracked post-`v1.6.0`.

### 2. Consistency check (verify-only, no rewrite)

- `docs/PLAN.md` §"Sprint 15" — confirm the §"Gate" CHANGELOG requirement matches what you wrote. If PLAN and your CHANGELOG block disagree, **PLAN wins** (it's integrator-authored) — adjust the CHANGELOG, file the discrepancy in `issues/issue_sprint15_architect.md`, do not edit PLAN.
- `NEW_PROJECT_STARTING_POINT.md` §"Tiering the sprint process by change size" — confirm it exists, is internally consistent, and that PLAN §"Sprint 15" §"Process deliverable" correctly points at it. It is already landed; a one-line "verified consistent" record is the expected outcome.
- Book audit: `grep -rn "v1.5.0\|v1.6.0\|chokepoint\|orchestration" book/` — confirm **no** book change is needed (internal refactor, no user surface). Record the no-op explicitly so the tech-writer/validator don't expect a book delta.

### 3. File `issues/issue_sprint15_architect.md`

Format per the issue schema below. Expected outcome this light cycle: one substantive issue (the CHANGELOG `v1.6.0` block, `resolved`) + a short "PLAN/NEW_PROJECT consistency verified, no book surface" record. If everything is consistent, say so explicitly — a clean light-cycle ledger is the correct result, not a sign you missed something.

## Issue tracking format

```markdown
# Sprint 15 — architect issues

## Issue N: <short title>
**Severity**: low | medium | high | blocker
**Status**: open | in-progress | resolved | wontfix | accepted
**Files affected**: <paths>
### What changed / What was verified
<body>
```

## Scope guardrails

- Do NOT touch `internal/`, `book/` (confirm it needs no change), `prompts/`, or `docs/PLAN.md`.
- Do NOT invent a user-facing Added/Fixed entry — this cycle has none.
- Do NOT decide the version/date — write the block so a `v1.6.0`/`v1.5.1` + date swap is one edit; flag it as integrator-owned.
- Do NOT commit or push.

## Verification before reporting done

- The `v1.6.0` CHANGELOG block matches `docs/PLAN.md` §"Sprint 15" §"Gate" exactly (Changed/Removed/Deferred; no Added/Fixed; "no behavior change" stated).
- `### Removed` matches what staff actually landed (scrub deleted vs. demoted) per `issues/issue_sprint15_staff.md`.
- Book confirmed unchanged; PLAN + NEW_PROJECT consistency confirmed.
- `mdbook build book/` exits 0 (clean by no-op — no book edits).

## Final report

Under 150 words (light cycle). Cover: the `v1.6.0` CHANGELOG block (Changed/Removed/Deferred summary; version/date left integrator-owned); PLAN ↔ CHANGELOG ↔ NEW_PROJECT consistency result; explicit "no book surface" confirmation; issue ledger status.
