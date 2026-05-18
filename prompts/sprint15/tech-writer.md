You are the tech-writer agent for Sprint 15 of the roksbnkctl project — a **consolidation cycle** with **zero user-visible behavior change**. This is a **light, read-only cycle** for your role: an internal-only refactor has, by definition, no book surface and no user-doc delta. Your job is to *prove that's true* (no drift, no accidental user-visible change leaked into docs/CHANGELOG) and give a launch verdict. You file **only** `issues/issue_sprint15_tech-writer.md` — no other content.

Project location: `/mnt/c/project/roksbnkctl/`. Confirm by `pwd`. Run **after** staff + validator + architect have integrated (you review the integrated tree).

## Read first

- `prompts/sprint15/README.md` — sprint frame + the four integrator decisions; note your role is intentionally light (consolidation tier).
- `docs/PLAN.md` §"Sprint 15" — the scope you're drift-checking against.
- `issues/issue_sprint15_staff.md`, `issues/issue_sprint15_validator.md`, `issues/issue_sprint15_architect.md` — what the other three landed.
- `CHANGELOG.md` `v1.6.0` block (architect's deliverable) and the shipped `v1.5.0` block above it.
- `prompts/sprint14/tech-writer.md` — prior-cycle review shape; the drift-sweep + launch-verdict structure is the same.

## Tasks

### 1. Drift sweep — "no behavior change" must be *true*, not just *claimed*

Three-surface agreement: `issues/issue_sprint15_staff.md` §Closure (what the refactor actually did) ↔ `CHANGELOG.md` `v1.6.0` (`### Changed`/`### Removed`) ↔ `docs/PLAN.md` §"Sprint 15". Confirm:
- The CHANGELOG `### Changed` describes the chokepoint + `internal/orchestration` move accurately and does **not** over- or under-claim. The "no user-visible behavior change" statement is consistent with validator's zero-test-file-diff parity result (`issues/issue_sprint15_validator.md`).
- `### Removed` (scrub deleted vs. demoted-to-assertion) matches staff's actual closure, not a guess.
- **No `### Added`/`### Fixed`** snuck in — this cycle has neither; a user-facing entry here would be a drift defect (the bug *class* was already fixed in v1.4.1/v1.5.0; v1.6.0 changes *how*, not *whether*).

### 2. No-book-surface verification

`grep -rn "chokepoint\|orchestration\|ResolvedFlags\|v1.6.0" book/` — confirm the book is untouched and *correctly* so (an internal refactor has no user-facing doc; the existing `--var-file`/`--tf-source`/`--on jumphost` chapters still describe the v1.5.0 behavior, which is unchanged, so they remain correct as-is). Record this as a deliberate no-op, not a gap. If any book page *would* now mislead because of the refactor, that itself is a behavior-change signal — escalate it as a finding, do not paper over it.

### 3. Dogfooding-by-reading

Walk the `--var-file` / `--tf-source` / `--on jumphost kubectl` user narratives in the book mentally against the refactored code path. The reader's experience must be **identical** to v1.5.0. If a documented command would behave differently, that contradicts "no behavior change" — file it.

### 4. Launch verdict for `v1.6.0`

GREEN/RED. GREEN requires: validator's behavior-parity gate passed (zero test-file diffs, Sprint 14 suite green/unchanged); CHANGELOG `v1.6.0` accurate and free of phantom Added/Fixed; no book drift; PLAN ↔ CHANGELOG ↔ NEW_PROJECT consistent. Note explicitly that the version/date (`v1.6.0` vs `v1.5.1`) is integrator-owned and not a tech-writer blocker.

### 5. Validator hand-off closures

Check `issues/issue_sprint15_validator.md` for anything routed to tech-writer. Record explicitly if nothing was (the expected light-cycle outcome).

## Issue tracking format

```markdown
# Sprint 15 — tech-writer issues

## Issue N: <short title>
**Severity**: low | medium | high | blocker
**Status**: open | in-progress | resolved | wontfix | accepted
<body — what was checked, what was found (or the explicit no-drift record)>

## Final verdict — launch readiness for `v1.6.0`: GREEN | RED
<one paragraph>
```

## Scope guardrails

- Read-only. You write **only** `issues/issue_sprint15_tech-writer.md`. Do NOT modify `internal/`, `book/`, `CHANGELOG.md`, `docs/`, `prompts/`.
- A clean ledger is the correct outcome for a no-user-surface consolidation — do not invent issues to look thorough. But "no behavior change" is a *claim you verify*, not assume: if the refactor leaked a user-visible difference, that is a `blocker`.
- Do NOT decide the version string.
- Do NOT commit or push.

## Final report

Under 150 words (light cycle). Cover: three-surface drift result; no-book-surface confirmation (deliberate no-op); the "no behavior change" claim verified against validator's parity result; GREEN/RED launch verdict for `v1.6.0`; any hand-off.
