# Issue tracking for sprint execution

This folder tracks issues discovered during sprint implementation by the
parallel agents (architect, staff engineer, validator). It's not a
replacement for GitHub Issues — it's a per-sprint working ledger so the
sprint integrator (the human or LLM aggregating agent output) can see
exactly what each agent flagged and resolve it before the sprint closes.

## File naming

```
issues/issue_sprint<N>_<role>.md      # discovered during sprint N by <role>
issues/resolved_sprint<N>_<role>.md   # resolution notes after fix
```

Roles:
- `architect` — design, infrastructure, book authoring
- `staff` — Go implementation work
- `validator` — tests, CI, security review, e2e

## Issue format

Each `issue_*.md` file is a markdown doc with one section per issue:

```markdown
# Sprint N — <role> issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | in-progress | resolved | wontfix
**Description**: what was found, what was expected
**Files affected**: list of paths
**Proposed fix**: how to resolve
**Related**: links to PRDs, other issues, commits
```

## Resolution

When an issue is fixed, the integrator either:

1. Edits the original `issue_*.md` to flip status to `resolved` with a
   Resolution note + commit SHA, **or**
2. Creates a separate `resolved_*.md` mirror file when the resolution is
   long enough to warrant its own document (architecture changes, post-
   mortems, rollback notes).

The two patterns can coexist; the second is for issues weighty enough to
deserve their own writeup.

## Sprint cadence

See [`docs/PLAN.md`](../docs/PLAN.md) for the seven-sprint roadmap.
Sprint 0 is foundations + book infra; Sprints 1-7 each add features and
ship a release tag (or, in Sprint 0 + 6 + 7's case, contribute to a
release that lands at the end of Sprint 7).
