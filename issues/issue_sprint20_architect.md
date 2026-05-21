# Sprint 20 — architect issues (release-publish hardening)

> **Sprint 20 frame.** First regular work sprint post-`v1.6.4`.
> Release-tooling-only — no user-facing surface, no book chapter
> affected, no PRD touched. Architect has no deliverables this
> sprint; the closure note documents that fact for sprintwatch.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — No architect deliverables for Sprint 20

**Severity**: low (informational)
**Status**: resolved

### Motivation

Sprint 20's surface is `Makefile`'s `release-publish` target.
It changes no user-visible behaviour, ships no new flag or
output, and doesn't touch any book chapter or PRD. Architect's
role here is to confirm — explicitly, so sprintwatch counts a
terminal state — that no architect deliverables are needed.

### Acceptance criteria

1. The closure section names WHY there's no architect work
   (release-tooling-only; no book/CHANGELOG/PRD touched).
2. Status flips `open → resolved` in the same closure edit.

### Out of scope

Everything except the closure note. Specifically: do NOT add a
CHANGELOG bullet for this sprint at architect's hand — that
decision belongs to the integrator at cut time (internal
release-tooling fixes are sometimes called out in a `### Internal`
section, sometimes not; integrator's judgement).

### Files affected

- `issues/issue_sprint20_architect.md` (this file's closure
  section). Nothing else.

### Related

- Sprint 20 staff Issue 1 — the actual code change this sprint
  ships.

### Closure — architect, 2026-05-21

Confirmed: no architect deliverables for Sprint 20. The sprint's surface is `Makefile`'s `release-publish` target — release-tooling-only, with no user-facing behaviour change, no new flag or output, no CLI surface touched (so `book/src/27-command-reference.md` is unaffected), no book chapter relevant (the staleness hardening is invisible to readers), no PRD coverage (no PRD documents `release-publish`), and no CHANGELOG bullet at architect's hand (any `### Internal` mention is the integrator's call at cut time). Architect's role this sprint is exactly this closure note so sprintwatch records a terminal state. Status flipped `open → resolved`.
