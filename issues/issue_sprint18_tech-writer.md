# Sprint 18 — tech-writer issues (cos bucket get + post-v1.6.2 work cycle)

> **Sprint 18 frame.** First regular work sprint post-`v1.6.2`.
> Tech-writer runs **after** the three-way integration of staff
> (Issue 1: `cos bucket get`) + architect (Issue 1: mermaid PDF
> rendering) + validator (Issues 1 + 2: hermetic + live tests). The
> tech-writer's job is a drift/consistency sweep over the integrated
> tree and an optional ≤2 Part-B cross-cutting documentation issues.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Post-integration drift / consistency review for the integrated `cos bucket get` + mermaid PDF fix

**Severity**: low
**Status**: open

**Description.** Once the three-way integration commit for staff +
architect + validator lands on `main`, the tech-writer reviews it
for:

- `--help` text on the new `cos bucket get` matches sibling
  `cos bucket {create,list,delete}` style (flag shapes, positional
  args, required-vs-optional surfacing).
- CHANGELOG bullets for both changes are user-facing — no internal
  jargon (`internal/...`, Lua filter, mermaid-cli args), each
  cross-links the canonical chapter.
- The COS chapter pins a short subsection for the new verb where
  the existing `cos object get` example lives.
- The mermaid fix is mentioned in a §Diagnostics paragraph in the
  relevant book chapter, NOT presented as a feature.
- If the architect required a docker-image rebuild, `book.toml`'s
  image tag is current.
- `book/src/SUMMARY.md` is unchanged (this is a defect fix +
  additive feature, not a TOC change).

**Acceptance criteria**:

1. Every finding in this issue's Closure section names a specific
   file path + line number so the integrator can act precisely.
2. Findings are tagged with severity (low / medium / high); each
   high finding blocks the release, each medium gets an integrator
   judgement-call, each low is FYI.
3. A final GREEN / RED launch verdict line ends the closure —
   GREEN = integrator can ship `v1.6.3` (or `v1.7.0` if so
   judged), RED = address findings first.

**Out of scope**:

- Restyling the mermaid diagrams themselves (the fix is
  pipeline-side, not source-side).
- Wholesale chapter rewrites — drift sweep, not redesign.
- New documentation chapters — that's tracked separately.

**Files affected**: `issues/issue_sprint18_tech-writer.md` (this
file's Closure section). Read-only on the integrated tree.

**Related**: staff Issue 1, architect Issue 1, validator Issues
1 + 2 — all reviewed for drift, no edits suggested to those
ledgers.
