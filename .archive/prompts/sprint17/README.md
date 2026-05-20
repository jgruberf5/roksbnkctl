# Sprint 17

**Theme:** backlog grooming — each role surveys its area and drafts GitHub-issue markdown files (one per proposed issue) into `prompts/sprint17/staging/<role>/` using the new `.github/ISSUE_TEMPLATE/{bug_report,feature_request}.md` shapes; the integrator reviews and files via `gh issue create`.

_Drafted from the integrator's decision 2026-05-20 to open a planning sprint immediately after the v1.6.2 cut: the templates now exist, the in-repo `issues/` ledger is closed out for Sprint 16, and there's no PRD to implement next — it's time to enumerate the backlog properly so the next development cycle has a real worklist on GitHub Issues, not in heads._

## Integrator decisions baked in (do not relitigate)

1. **Agents draft, integrator files.** Each agent writes one `.md` per proposed issue under `prompts/sprint17/staging/<role>/NN-<slug>.md`, using the **literal contents** of `.github/ISSUE_TEMPLATE/bug_report.md` or `.github/ISSUE_TEMPLATE/feature_request.md` as the body shape (frontmatter included so the integrator can grep by template type). **No `gh issue create`, no commits.** Mirrors the Sprint 16 "Do NOT commit anything; only the integrator commits" rule — translated to "Do NOT file GitHub issues; only the integrator files".
2. **Dedupe before drafting.** Every proposed issue must be checked against (a) the existing two GitHub issues (`gh issue list -L 100 --state all` — currently #1 `cos bucket get` and #2 `mermaid PDF rendering`), and (b) the in-tree `issues/issue_sprint*_*.md` ledger (especially items already marked `accepted`/`deferred`/`wontfix`). A near-duplicate is a non-deliverable — say so in the agent's final report and skip.
3. **Quality over volume.** Soft caps per role (below). The integrator will reject low-signal drafts at review time rather than file them, so volume past the cap actively hurts.
4. **No PRD, no book surface, no release tag.** This sprint produces issue-drafts on disk. Code/docs/CI deliverables come in **subsequent** sprints that pick issues off the resulting GitHub backlog.

## Per-role survey scope + cap

| Role | Survey scope | Cap |
|---|---|---|
| **staff** | Go code (`internal/**`, `cmd/**`, `tools/**`): `TODO` / `FIXME` / `XXX` comments; PRD "Implementation tasks" lines not landed; `CHANGELOG.md` `### Deferred` blocks; `docs/PLAN.md` §"What's deliberately deferred to post-v1.0" Code subsection. | ≤8 |
| **architect** | The book (`book/src/**`): "out of scope" / "future work" / TODO mentions, broken cross-references, content missing from the table-of-contents; `.github/workflows/**` (missing CI / release / lint / smoke coverage); infra-y `CHANGELOG ### Deferred`. | ≤6 |
| **validator** | Test coverage holes (per-package + e2e); `scripts/**` (missing drivers, gated live-verify gaps); CI matrix gaps; lessons surfaced by the Sprint 16 follow-up (the live-verify-high-issues discipline, the no-piling-into-active-release lesson). | ≤6 |
| **tech-writer** | Cross-cutting drift between code ↔ book ↔ `--help` text; doc-only fixes (typos, broken anchors, stale version pins); book-reference fixes (issue #2 is one example — they may find more). | ≤5 |

Light totals: ≤25 issue-drafts across the four agents. Better to file 15 good ones than 40 mediocre.

## Filing convention (the staging dir)

```
prompts/sprint17/staging/
├── staff/
│   ├── 01-<short-slug>.md
│   ├── 02-<short-slug>.md
│   └── ...
├── architect/
├── validator/
└── tech-writer/
```

Each `<role>/NN-<slug>.md` is a complete GitHub-ready issue:

- Starts with the **frontmatter from the template the agent picked** (so the integrator can grep `^name: Bug report` vs `^name: Feature request`).
- Body follows the section headings the template defines — every section filled in, comment placeholders deleted.
- File **basename** doubles as the `gh issue create --title` hint via the slug; the actual title is in the frontmatter `title:` line.

Slug rules: kebab-case, ≤6 words, no trailing punctuation. `01-cos-bucket-prefix-flag.md` is good; `01-Add_a_prefix_flag_to_cos_bucket_get!!!.md` is not.

## Four-agent dispatch

- **Architect** — `architect.md`. Owns `prompts/sprint17/staging/architect/`.
- **Staff** — `staff.md`. Owns `prompts/sprint17/staging/staff/`.
- **Validator** — `validator.md`. Owns `prompts/sprint17/staging/validator/`.
- **Tech-writer (read-only, light, runs after)** — `tech-writer.md`. Owns `prompts/sprint17/staging/tech-writer/` PLUS a final consolidation pass over the other three roles' drafts in their staging dirs (dedupe, drift, clarity).

Three run in parallel; tech-writer after the integrator pulls the three-way work together. No agent runs `gh`; the integrator does the filing in step 5 below.

## Integrator step-by-step (after agents return)

1. `git status --porcelain prompts/sprint17/staging/` → review the drafts per role.
2. Reject low-signal / duplicate / out-of-scope drafts; consolidate near-duplicates across roles (e.g. two agents both file something book-adjacent — one issue).
3. For each accepted draft: `gh issue create --title "<from frontmatter>" --body "$(cat prompts/sprint17/staging/<role>/NN-<slug>.md)"` — strip the frontmatter from the piped body (GitHub renders the YAML as markdown otherwise). Record the resulting `https://github.com/jgruberf5/roksbnkctl/issues/<N>` URL beside the draft.
4. Commit the staging tree (audit trail) + a sprint17 closure file listing every filed issue with its URL.
5. No tag, no Release.

_No PRD, no book chapter, no CHANGELOG entry — this sprint's deliverable is the GitHub backlog the next development cycle will pull from._
