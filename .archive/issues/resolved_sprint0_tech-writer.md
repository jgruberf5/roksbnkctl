# Sprint 0 — tech writer issues, resolution notes

Resolution log for `issue_sprint0_tech-writer.md`. All 8 issues fixed
in this integration pass. The tech-writer agent caught real problems
the other three agents missed — adding the role retroactively to
Sprint 0 was a productive call.

## Issue 1 (cspell.json typo "SSC" → "SCC") — fixed

**Resolution**: edited `cspell.json` line 23: `"SSC"` → `"SCC"`. Added
seven companion words on the same pass that the agent flagged as
likely-needed soon (`tfvars`, `passthrough`, `passthroughs`,
`kubeconfigs`, `cobra`, `Mermaid`, `xrefs`, `restricted-v2`,
`securityContext`).

**Status**: ✅ resolved
**Files touched**: `cspell.json`

## Issue 2 (CONTRIBUTING.md missing "How to add a chapter") — fixed

**Resolution**: added a "Working on the book" section to
`CONTRIBUTING.md` covering local preview (`make book-serve`),
SUMMARY.md edit + chapter file drop, the cspell.json word-list
convention, the "feature PRs include matching chapter" rule, and a
brief style note (clipped technical voice, runnable examples, relative
xrefs for mdBook's internal-link checker).

**Status**: ✅ resolved
**Files touched**: `CONTRIBUTING.md`

## Issue 3 (CONTRIBUTING.md no prompts/ signpost) — fixed

**Resolution**: added a "Sprint execution and the prompts/ folder"
section to `CONTRIBUTING.md` linking to `prompts/README.md` with a
two-sentence overview of the four-role pattern and the
issue/resolved file workflow.

**Status**: ✅ resolved
**Files touched**: `CONTRIBUTING.md`

## Issue 4 (book.yml no PR trigger / no `mdbook test`) — fixed

**Resolution**: split `.github/workflows/book.yml` into two jobs:
- `validate` — runs on `pull_request`, runs `mdbook test book/` (broken-link + code-block check) plus `mdbook build`. No deploy.
- `build-deploy` — runs on `push` to `main` only (gated via `if: github.event_name == 'push'`). Builds + deploys to gh-pages.

This matches PLAN.md line 107's spec ("`mdbook test book/` runs on every PR — fails on broken internal links, malformed code blocks").

**Status**: ✅ resolved
**Files touched**: `.github/workflows/book.yml`

## Issue 5 (chapter 17 hybrid placeholder) — fixed via option (a)

**Resolution**: normalized chapter 17's stub to `*Coming in Sprint 3.*`
matching the other 31 chapters, then added an italic note in the body
explaining the Sprint 3 (intro) → Sprint 4 (deep-dive) split. Preserves
both grep-ability (`Coming in Sprint 3.` finds chapter 17) and the
richer signal about the chapter's two-stage authoring.

**Status**: ✅ resolved
**Files touched**: `book/src/17-execution-backends.md`

## Issue 6 (README two close blockquotes) — fixed via option (a)

**Resolution**: demoted the book-link line from a blockquote (`> 📖 ...`)
to a plain emphasized line (`📖 ...`). Visual stack of pull-quotes is
gone; status blockquote is the only callout above the fold again.

**Status**: ✅ resolved
**Files touched**: `README.md`

## Issue 7 (cspell.json missing future words) — fixed alongside Issue 1

**Resolution**: see Issue 1 above. The `tfvars`, `passthrough`, `cobra`,
`Mermaid`, `xrefs`, `restricted-v2`, `securityContext`, etc. additions
were folded into the same `cspell.json` edit.

**Status**: ✅ resolved (combined with Issue 1)

## Issue 8 (CONTRIBUTING.md `PHASE_FROM=D` example unverified) — verified, no change needed

**Resolution**: spot-checked `scripts/e2e-test.sh`:

```bash
$ grep -E '^phase_[A-Z]\(\)|PHASE_FROM' scripts/e2e-test.sh
#   PHASE_FROM=D ...
PHASE_FROM=${PHASE_FROM:-A}
phase_A() { ... }
phase_B() { ... }
phase_C() { ... }
phase_D() { ... }
phase_H() { ... }
```

`PHASE_FROM=D` is valid; phase_D is the full lifecycle phase. The
documented example is correct; no fix needed beyond confirmation.

**Status**: ✅ resolved as "verified; example is correct"
