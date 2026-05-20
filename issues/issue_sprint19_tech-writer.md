# Sprint 19 — tech-writer issues (`init --var-file` post-integration drift sweep)

> **Sprint 19 frame.** First regular work sprint post-`v1.6.3`.
> Tech-writer runs **after** the three-way integration of staff
> (Issue 1: `init --var-file`) + architect (Issue 1: init chapter
> + 27 regen + cross-chapter sweep) + validator (Issue 1:
> hermetic + live tests). Drift sweep + GREEN/RED launch verdict.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Post-integration drift / consistency review for `init --var-file`

**Severity**: low
**Status**: open

### Motivation

The Sprint 18 cycle taught us that integration-time drift is real even with careful role partitioning: the auto-generated CLI reference was stale; the COS chapter didn't pin the new verb; sibling pipeline-comment prose contradicted the as-shipped code. Sprint 19's surface is smaller (one flag on one command) but the same drift classes apply. The tech-writer's job is to catch them before the v1.6.4 cut.

### Drift surface to walk

For the **`init --var-file` flow**:

- **`roksbnkctl init --help`** output text — the new flag's description matches the style of sibling flags (length, voice, references). Capture the actual `--help` output (run `go run ./cmd/roksbnkctl init --help`) and pin findings against it, not against the staff's prompt language.
- **`book/src/27-command-reference.md`** — the regenerated reference includes the new flag with the right shape. Compare against `init --help` output.
- **The init book chapter** (architect identified the path) — has the new §"Skip the interview: `init --var-file`" subsection with all five components (when, flow, what's persisted, why-it-matters, secrets-on-disk, diagnostics).
- **Cross-chapter sweep verification** — every "supply `--var-file` on every command" instance the architect's task A.3 was supposed to touch is actually amended; no stale advice survives.
- **CHANGELOG / PLAN docstring sweep** — no in-code reference to "see CHANGELOG vX.Y.Z" with a version that doesn't match the integrator's target (likely v1.6.4).
- **Secrets-on-disk consistency** — the book's note about `0600` + the workspace-state path matches what the staff implementation actually does (the validator's hermetic test (a) is the source of truth for mode; the chapter must match).

### Acceptance criteria

1. Every finding in this issue's Closure section names a specific file path + line number.
2. Findings tagged by severity (low / medium / high); each high finding blocks the release.
3. A final GREEN / RED launch verdict ends the closure.
4. The findings cite the actual `init --help` output (captured during the review), not the spec language.

### Out of scope

- Restyling chapters; rewriting flow descriptions; adding new chapters. Drift sweep only.
- Touching any non-`issues/` file. Read-only on existing repo content.

### Optional Part B (≤2 issues)

If the integrated work surfaces a cross-cutting docs gap the other roles didn't close, file it as Issue 2 (or 2+3) here. Strict cap.

### Files affected

- `issues/issue_sprint19_tech-writer.md` (this file's Closure section). Read-only on the integrated tree.

### Related

- Staff Issue 1, architect Issue 1, validator Issue 1 — all reviewed for drift; no edits suggested to those ledgers.
- Sprint 18 tech-writer Issue 1 — the precedent shape for this work (3 findings caught → addressed in integration commit → GREEN).
