# Sprint 20 — tech-writer issues (release-publish hardening drift sweep)

> **Sprint 20 frame.** First regular work sprint post-`v1.6.4`.
> Tech-writer runs **after** the integrator has landed staff +
> validator's deliverables. Drift sweep over the Makefile +
> docs/PLAN.md closure note. GREEN/RED launch verdict.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Post-integration drift sweep for `release-publish` hardening

**Severity**: low
**Status**: open

### Motivation

Sprint 20's surface is small (one Makefile target + a hermetic
test), but the same drift classes that bit Sprint 18 / 19 still
apply: a docstring that doesn't match the as-landed recipe, a
symmetric staleness risk on a neighbouring target that staff's
scope didn't anticipate, a `docs/PLAN.md` closure that omits the
hermetic-test runbook. Tech-writer catches them before the
v1.6.5 (or whatever-next-tag) cut.

### Drift surface to walk

For the `release-publish` hardening:

- **`Makefile` `release-publish` recipe** — verify the staff
  edit landed (rm-then-rebuild + exit-code gate). Cite line
  numbers.
- **`Makefile` `release-publish` docstring** — verify it
  mentions the Sprint 19 v1.6.4 stale-upload event in the
  rationale section.
- **Symmetric staleness risks** — read `book-publish`,
  `book-pdf`, `book`, `release`. Look for the same shape;
  flag any that didn't get hardened (severity tag per finding).
- **`docs/PLAN.md` §"Sprint 20"** — verify the closure
  subsection is present, names the hermetic-test runbook,
  and names the v1.6.4 stale-upload event as the precipitating
  trigger.
- **`README.md` / contributing docs** — verify they don't
  carry stale instructions referencing the un-hardened recipe.

### Acceptance criteria

1. Every finding in this issue's Closure section names a
   specific file path + line number.
2. Findings tagged by severity; each `high` finding blocks the
   release cut.
3. A final GREEN / RED launch verdict ends the closure.

### Out of scope

- Restyling Makefile targets; rewriting recipe bodies. Drift
  sweep only — recommend fixes, don't apply them.
- Touching any non-`issues/` file. Read-only on existing repo
  content.

### Optional Part B (≤2 issues)

If the integrated work surfaces a cross-cutting release-tooling
gap the other roles didn't close, file it as Issue 2 (or 2+3)
here. Strict cap.

### Files affected

- `issues/issue_sprint20_tech-writer.md` (this file's Closure
  section). Read-only on the integrated tree.

### Related

- Staff Issue 1, validator Issue 1 — both reviewed for drift.
- Sprint 19 tech-writer Issue 1 — the precedent shape for this
  work (8 low-sev findings → all informational → GREEN).
