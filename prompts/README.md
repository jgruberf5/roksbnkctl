# Agent dispatch prompts — sprint execution playbook

This folder holds the verbatim prompts dispatched to parallel sub-agents during sprint execution, plus the canonical playbook for **how to kick off Sprint N**. If someone says _"referencing prompts/README.md, kick off Sprint N"_, this document is what they're pointing you at.

The companion folders are:
- [`docs/PLAN.md`](../docs/PLAN.md) — the 7-sprint roadmap (which sprints exist, what's in scope, release tag mapping)
- [`docs/prd/`](../docs/prd/) — six PRDs (`00-OVERVIEW.md` through `05-E2E-TEST-PLAN.md`) defining the *what* per phase
- [`issues/`](../issues/) — the per-sprint issue + resolution log produced by agent dispatch

## The four roles per sprint

Every sprint dispatches **four parallel sub-agents**. Each role has its own prompt file checked into `prompts/sprint<N>/<role>.md`:

| Role | Owns | Reads | Writes |
|---|---|---|---|
| **architect** | design + book authoring + GitHub Actions / infra | the PRD for that sprint, `docs/PLAN.md` | `book/src/`, `.github/workflows/*.yml`, README book links, Makefile (append-only) |
| **staff** | Go implementation, refactors, unit tests | the PRD for that sprint, surrounding existing Go code | `internal/...`, `cmd/...`, `scripts/`, Makefile (append-only), CONTRIBUTING.md (specific sections) |
| **validator** | CI matrix, integration tests, security review, e2e drivers | the PRD for that sprint, `scripts/e2e-test.sh`, existing tests | `tools/`, `.github/workflows/*.yml`, `cspell.json`, CONTRIBUTING.md (smoke-test section), e2e drivers |
| **tech-writer** | readability + example correctness review (read-only) | everything the other three produced | `issues/issue_sprint<N>_tech-writer.md` only — files no other content |

The four roles run **in parallel** when the sprint kicks off. The tech-writer agent reviews **after** the other three finish (so it has something to review) — in practice, dispatch the first three concurrently, integrate, then dispatch tech-writer over the integrated result.

## Why these are checked in

1. **Auditability** — for each sprint integration commit, the exact instructions each role received are preserved. Months later it's possible to answer "why did the staff-engineer agent do X?" by reading what it was told to do.

2. **Reproducibility** — a future session (different LLM, different contributor) can re-dispatch a role by reading the prompt file and sending it to its agent tool. The pattern doesn't depend on any one integrator remembering it.

3. **Refinement over time** — when a sprint surfaces "the staff prompt should have mentioned X" feedback, the prompt file gets updated for next sprint's analogous role. Compounds across sprints.

## Format of each prompt file

Each prompt file is plain markdown — the same content sent verbatim as the `prompt` parameter on the `Agent` tool call. Sections typically include:

- **Role + sprint identification** (one-line opener: "You are the X agent for Sprint N of the roksbnkctl project")
- **Project context** (where the repo is, key files agents need to read first — typically the relevant PRD + `docs/PLAN.md` Sprint N section)
- **Coordination notes** (which other agents are running in parallel, which files to leave alone, which files are append-only-shared)
- **Numbered tasks** (concrete deliverables with target paths and acceptance criteria)
- **Issue tracking format** (the `issues/issue_sprint<N>_<role>.md` schema agents follow when filing problems)
- **Verification before reporting done** (what each agent must self-check before reporting)
- **Final report shape** (constrains agent output length/format — usually "concise summary under 200 words")
- **"Do NOT commit anything"** — only the integrator commits

## Kicking off Sprint N — the canonical checklist

This is the playbook a contributor or LLM follows on a one-line instruction like _"referencing prompts/README.md, kick off Sprint N"_:

### 0. Read the inputs

Before drafting any prompt, read these in order:

1. **`docs/PLAN.md` Sprint N section** — what's in scope, what release tag (if any) it gates, calendar estimate, dependencies on prior sprints, this sprint's testing pyramid additions, this sprint's book chapters
2. **The PRDs that Sprint N implements** — typically one or two of `docs/prd/01-*` through `docs/prd/05-*`. The PRD's "Implementation tasks" list is the authoritative work breakdown.
3. **The previous sprint's `prompts/sprint<N-1>/<role>.md` files** — the four prompts there are templates worth borrowing structure from. Each subsequent sprint reuses ~80% of the previous sprint's prompt scaffolding (coordination notes, issue format, verification block, final-report shape) and rewrites only the task-specific sections.
4. **Outstanding issues from prior sprints** — `issues/issue_sprint<M>_*.md` for all M < N. Anything still flagged `Status: open` is fair game to roll into the new sprint's task list.

### 1. Draft the four prompt files first, BEFORE dispatching anything

Write to disk:

```
prompts/sprint<N>/architect.md
prompts/sprint<N>/staff.md
prompts/sprint<N>/validator.md
prompts/sprint<N>/tech-writer.md
```

Each prompt should be self-contained — an agent runs with no memory of this conversation. State the project location (`/mnt/d/project/roksbnkctl/`), point at the PRD it should read, list coordination notes about the other agents, give numbered tasks with concrete target paths.

The four prompts together should partition the sprint's deliverables with **no overlap and no gaps**. Shared files (Makefile, CONTRIBUTING.md, README.md) need explicit append-only or section-ownership notes so agents don't clobber each other.

**Commit these four files first**, in their own commit ("`prompts/sprint<N>: draft agent prompts ahead of dispatch`"), so the dispatch is reproducible even if the integration goes sideways.

### 2. Dispatch architect + staff + validator in parallel

In a single message containing three concurrent `Agent` tool calls, send:

```
Agent(description: "Sprint N architect — <topic>",
      subagent_type: "general-purpose",
      prompt: "<contents of prompts/sprint<N>/architect.md>")
Agent(description: "Sprint N staff — <topic>",
      subagent_type: "general-purpose",
      prompt: "<contents of prompts/sprint<N>/staff.md>")
Agent(description: "Sprint N validator — <topic>",
      subagent_type: "general-purpose",
      prompt: "<contents of prompts/sprint<N>/validator.md>")
```

The agents work on disjoint file sets in parallel. Each writes to `issues/issue_sprint<N>_<role>.md` if it finds problems.

### 3. Aggregate + integrate + resolve

When all three return:

1. Run `git status` to see the aggregated tree of changes
2. Read each agent's `issues/issue_sprint<N>_<role>.md`
3. Fix every issue marked `Status: open` (or document why it's accepted as-is)
4. Run the verification block from each prompt: `go build ./...`, `go test ./...`, `go vet ./...`, `gofmt -d -l .`, plus any sprint-specific checks
5. Write `issues/resolved_sprint<N>_<role>.md` for each agent — one file per role, one section per issue, documenting how each was handled (fix, accept, defer)
6. Commit the integrated work in **one commit** (the agents' code + your fixes + the resolved files) with a descriptive message naming each agent's contribution

### 4. Dispatch tech-writer over the integrated tree

After the three-way integration commit lands:

```
Agent(description: "Sprint N tech-writer — doc review",
      subagent_type: "general-purpose",
      prompt: "<contents of prompts/sprint<N>/tech-writer.md>")
```

The tech-writer reads everything the others produced and files readability / consistency / example-correctness issues to `issues/issue_sprint<N>_tech-writer.md`.

Integrate exactly as in step 3: fix the issues, write `issues/resolved_sprint<N>_tech-writer.md`, commit. The tech-writer integration is its own commit, not folded into the three-way commit, so the audit trail of what each role produced stays clean.

### 5. Tag a release, if the sprint gates one

`docs/PLAN.md`'s milestone table maps sprints to release tags:

| Sprint | Tag | Gate |
|---|---|---|
| 0 | (no tag) | foundations only |
| 1 | `v0.7` | M1: SSH/--on works against live cluster, 6 chapters published |
| 2 | `v0.8` | M2: kubectl PATH-stripped E2E green, 13 chapters |
| 3-5 | (no tag during; cumulative work) | book at 24 chapters by end of Sprint 5 |
| 5 | `v0.9` | M3: four backends working, DNS+GSLB, cred audit clean |
| 6 | (no tag — flows into Sprint 7) | E2E phases I-N pass, 33 chapters drafted |
| 7 | `v1.0` | M4: book launched + dogfooded + all phases green |

If the sprint's gate criteria are met, tag the release and update CHANGELOG.md (or release notes equivalent). If they aren't, write down what's missing in `issues/issue_sprint<N>_blockers.md` and roll the gap into Sprint N+1's planning before drafting its prompts.

### 6. Push everything

```bash
git push origin main
git push origin v0.X        # only if tagged
```

If `v0.X` is the gate, also create a GitHub Release with the binary artifacts (goreleaser handles this) and a release-notes summary pointing at the relevant chapters of the book.

## Pattern check before each sprint dispatch

A short pre-flight before drafting prompts:

- [ ] Sprint N's PRD has been read end-to-end (not just skimmed)
- [ ] Sprint N's PLAN.md section has been read, including the testing pyramid additions table
- [ ] All open issues from sprints < N have been triaged: rolled into Sprint N's task list, deferred to a later sprint with a note, or closed as won't-fix
- [ ] The four prompts are mutually exclusive (no overlapping deliverables) and collectively exhaustive (no scope gaps vs. PLAN.md Sprint N)
- [ ] Coordination notes in each prompt name the other three agents and their owned files
- [ ] Each prompt's "verification before reporting done" block includes the relevant sprint-specific checks (e.g. byte-equivalent doctor output for Sprint 0, byte-equivalent `kubectl get -o yaml` for Sprint 2)
- [ ] Tech-writer prompt has been adjusted from the previous sprint's template to call out anything new this sprint (e.g. Sprint 2 adds kubectl-internalization chapters that need different consistency checks than Sprint 0's stubs)

## Sprint 0 prompt set (reference)

Sprint 0 is the foundational sprint — its four prompts here are templates worth reading before drafting Sprint 1's. Each was actually dispatched and produced the artifacts now landed in the repo:

- [`sprint0/architect.md`](./sprint0/architect.md) — book infrastructure, 32 chapter stubs, GitHub Pages workflow
- [`sprint0/staff.md`](./sprint0/staff.md) — doctor refactor, CI matrix, pre-commit hook
- [`sprint0/validator.md`](./sprint0/validator.md) — tools/docker/, spellcheck workflow, cspell.json, smoke-test docs
- [`sprint0/tech-writer.md`](./sprint0/tech-writer.md) — readability + consistency review (filed 8 actionable issues, all resolved in the integration pass)

Issues filed during Sprint 0 dispatch and their resolutions are at `issues/issue_sprint0_*.md` and `issues/resolved_sprint0_*.md`.
