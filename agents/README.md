# Agent roles

This directory holds **tool-agnostic role definitions** for the four-agent sprint pattern this project uses: architect, staff, validator, tech-writer. The files are plain markdown — they work as system prompts for any LLM-backed coding tool (Claude Code, Cursor, Continue, Aider, the Anthropic / OpenAI APIs directly, or pasted into a chat UI).

The pattern is a **parallel-fanout multi-agent workflow**: three agents work concurrently against disjoint file surfaces during a sprint, a fourth runs read-only at the end as the release-readiness gate, and a human (or another agent) integrates the aggregate.

## The four roles

| Role | Surface | When it runs |
|---|---|---|
| [`architect`](./architect.md) | Design + prose: PRDs, plan files, top-level docs, cross-cutting design decisions | Parallel with staff + validator during the sprint |
| [`staff`](./staff.md) | Implementation: code, build/release config, focused tests for what changed | Parallel with architect + validator during the sprint |
| [`validator`](./validator.md) | Regression gate: example correctness, cross-links, search-index, test suite, CI workflows, e2e DRY_RUN | Parallel with architect + staff during the sprint |
| [`tech-writer`](./tech-writer.md) | Read-only review + dogfooding simulation + gate-criteria audit | At end of sprint, after the other three have finished |

The architect / staff / validator agents **edit project files** but **do not commit**. The tech-writer agent **edits only its own issue file**. An **integrator** (human or another agent) reads all four issue files, folds findings into a coherent commit-or-revert decision, and cuts the release tag.

## How agents coordinate

Three coordination mechanisms, all in the repo:

1. **Off-limits files in the task brief.** Each sprint's task brief lists which files each agent owns and which are off-limits (typically: architect → prose surface; staff → code/config surface; validator → test scripts + CI). Boundaries are hard — surface conflicts as issues, don't merge silently.

2. **Issue files as the hand-off contract.** Each agent files `issues/issue_<sprint>_<role>.md` with one issue per finding. The next sprint's architect / staff fold relevant resolved findings; the integrator reads all four at tag-cut time.

3. **Read-first lists in the task brief.** Each agent reads the project's conventions file (`AGENTS.md` / `CLAUDE.md`), the plan file (`docs/PLAN.md`), relevant PRDs / design docs, and the prior sprint's `resolved_<sprint>_*.md` files before doing any work. The role file says *how* to ground; the task brief says *where* to look.

## How to invoke

A sprint invocation has two pieces: the **persistent role** (this directory) + the **per-sprint task brief** (typically `prompts/<sprint>/<role>.md`). The task brief is short — it names the sprint scope, the parallel agents, the read-first list, the deliverables, and the issue-file path. The role file carries everything else.

### Generic invocation (any LLM, any tool)

Concatenate the role file with the task brief and use it as the system prompt or initial-turn message. The role file defines who the agent is and how it works; the task brief defines what this specific sprint asks of it.

```
SYSTEM PROMPT = <contents of agents/architect.md> + "\n\n---\n\n" + <contents of prompts/sprint-N/architect.md>
USER MESSAGE  = "Begin sprint N. Report back when verification gates are clean."
```

### Per-tool wiring

| Tool | How to wire the role file |
|---|---|
| **Claude Code** | Create a thin `~/.claude/agents/<role>.md` with YAML frontmatter (`name`, `description`) whose body says "Read `agents/<role>.md` for your role definition, then process the task brief." Invoke via the Agent tool with `subagent_type: <role>`. Or paste the role file into the conversation directly. |
| **Cursor** | Add `.cursor/rules/<role>.mdc` referencing `agents/<role>.md`. Switch rules per sprint. |
| **Aider** | `aider --read agents/architect.md --read prompts/sprint-N/architect.md` then chat as usual. The `--read` files are persistent context. |
| **Continue** | Reference `agents/<role>.md` from `.continuerc.json` as a custom slash command or context provider. |
| **Anthropic SDK / OpenAI SDK** | Read both files at startup, concatenate, pass as `system` parameter. The user message scopes the iteration. |
| **Plain chat UI (any LLM)** | Paste the role file's contents into the system / instructions slot; paste the task brief into the first user turn. |

### Spawning the four roles in parallel

Three idiomatic patterns:

1. **Single human orchestrator + four concurrent agent sessions.** Open four terminal windows (or four Aider sessions, or four Claude Code worktrees) and run each role against the same git branch. The off-limits-files contract keeps them from stepping on each other.

2. **One orchestrator agent that spawns three sub-agents in parallel.** Claude Code's Agent tool with parallel `subagent_type` invocations is one way; the OpenAI / Anthropic SDKs can do the same with concurrent API calls. The orchestrator hands each sub-agent its role file + task brief and waits for all to finish before invoking the tech-writer.

3. **Sequential single-LLM pass through all four roles.** When parallelism isn't available, run architect → staff → validator → tech-writer in sequence, one LLM session per role, clearing context between roles. Slower but works with any tool.

## Writing a sprint task brief

A task brief is typically 50-100 lines. It assumes the role file carries the rest. Sections to include:

- **Sprint scope (1-2 sentences).** What this sprint is about, what's the release-gate context.
- **Read first.** Concrete files: plan file section, relevant PRDs, prior-sprint resolved issues, the auto-generated reference docs if any.
- **Coordinate with parallel agents.** Who else is running, what files they own (so this role doesn't touch them), what hand-off contracts exist.
- **Your scope.** Concrete files / paths this role owns this sprint.
- **Tasks (priority order).** Numbered deliverables. Stop at a priority boundary if budget tightens.
- **Issue tracking.** Path to the issue file; severity guide if the project conventions differ.
- **Verification before reporting done.** Checklist the role works through before claiming finished.
- **Final report shape.** What the closing message should include.

Each sprint's task briefs live at `prompts/<sprint>/<role>.md`. The first sprint's briefs become templates for later sprints — copy, retarget the scope, retarget the read-first list, retarget the deliverables.

## Example: a full sprint kickoff

What follows is a concrete template you can copy into `prompts/sprint-N/` and retarget. The scenario is generic: **add a new `report` subcommand** to a CLI project, alongside a new documentation chapter and release-notes entry. Replace `<project>`, `<feature>`, etc. with your specifics.

### `prompts/sprint-N/README.md` — orchestrator's view

```markdown
# Sprint N

**Theme:** Add `<project> report` subcommand + chapter + release notes

_Sprint N adds a new top-level subcommand (`<project> report`) that
summarises workspace state. Three surfaces touched: code (new command +
flag parsing + tests), prose (new chapter, README pointer, CHANGELOG
entry), regression (existing flows untouched, new command's examples
verified). End-of-sprint gate: book builds clean, test suite green,
new chapter dogfoodable by a first-time reader._

Carry-overs from Sprint N-1:
1. <issue from prior sprint that belongs to architect>
2. <issue from prior sprint that belongs to staff>
3. <issue from prior sprint that belongs to validator>

Four-agent dispatch:

- **Architect** — new chapter at `book/src/<NN>-report-command.md`;
  cross-links from chapters X and Y; CHANGELOG entry under the
  Unreleased section's "Added" subsection.
- **Staff engineer** — `<project> report` cobra command at
  `internal/cli/report.go` + matching test; flag parsing; sample
  output captured via golden-file test.
- **Validator** — re-run example-correctness sweep on the new chapter;
  full regression of build / test / vet / lint; cross-link audit.
- **Tech-writer** — read-only review at end of sprint; dogfooding loop
  on the new chapter from a first-time-reader perspective; final
  gate verdict.

The integrator commits the aggregate and (if scope warrants) cuts a
new release tag.
```

### `prompts/sprint-N/architect.md`

```markdown
You are playing the role described in `agents/architect.md`. Sprint N
scope: add a new chapter for the `<project> report` subcommand, plus
cross-links and a CHANGELOG entry.

## Read first

- `agents/architect.md` — your role identity.
- `AGENTS.md` / `CLAUDE.md` — project conventions.
- `docs/PLAN.md` §"Sprint N" — sprint scope.
- `book/src/SUMMARY.md` — where the new chapter fits.
- `prompts/sprint-(N-1)/architect.md` — prior-sprint task shape.
- `issues/resolved_sprint-(N-1)_*.md` — relevant carry-overs.

## Coordinate with parallel agents

- **Staff** is implementing `internal/cli/report.go` + test. Do NOT
  touch any file under `internal/` or any `*_test.go`.
- **Validator** is running the regression sweep. Do NOT touch files
  under `scripts/` or `.github/workflows/`.
- **Tech-writer** runs at sprint end; their issues land after yours.

## Your scope

`book/src/<NN>-report-command.md` (new), `book/src/SUMMARY.md` (add
TOC entry), cross-links in chapters X and Y, `CHANGELOG.md` under
Unreleased's Added subsection.

## Tasks (priority order)

1. Draft the new chapter (~150 lines): purpose, flags, sample output,
   cross-links to backend chapters where relevant.
2. Add the TOC entry to `SUMMARY.md` at the right position.
3. Cross-link from chapters X and Y to the new chapter.
4. Add a CHANGELOG.md `### Added` bullet for the new command.

## Issue tracking

File at `issues/issue_sprint-N_architect.md`. Severity: blocker / high /
medium / low / roadmap.

## Verification before reporting done

- `mdbook build book/` succeeds locally.
- SUMMARY.md entry resolves; new chapter renders.
- Cross-links from chapters X and Y resolve.
- CHANGELOG entry sits under the right subsection.

## Final report

Under 200 words: files created, files edited, line counts, issues
filed (counts by severity), anything the integrator should know.

Do NOT commit.
```

### `prompts/sprint-N/staff.md`

```markdown
You are playing the role described in `agents/staff.md`. Sprint N scope:
implement the `<project> report` subcommand with flag parsing and a
focused test pinning the new command's output shape.

## Read first

- `agents/staff.md` — your role identity.
- `AGENTS.md` / `CLAUDE.md` — project conventions.
- `docs/PLAN.md` §"Sprint N" — sprint scope.
- `internal/cli/` — existing command patterns (read `version.go` and
  `init.go` to match style).
- `prompts/sprint-(N-1)/staff.md` — prior-sprint task shape.

## Coordinate with parallel agents

- **Architect** is writing the new chapter. Do NOT touch `book/src/`,
  `CHANGELOG.md`, or `docs/PLAN.md`.
- **Validator** is running the regression sweep. Do NOT touch
  `scripts/` or `.github/workflows/`.

## Your scope

`internal/cli/report.go` (new), `internal/cli/report_test.go` (new),
any wiring needed in `internal/cli/root.go` to register the command.

## Tasks (priority order)

1. Implement `<project> report` — cobra command, flag set (--workspace,
   --format json|text), reads workspace state via existing accessors.
2. Write the test pinning output shape — golden-file or string-contains
   assertion; one test per output format.
3. Smoke-verify: `go build ./...`, `go test ./...`, `go vet ./...`,
   `gofmt -d -l .` all clean.

## Issue tracking

File at `issues/issue_sprint-N_staff.md`.

## Verification before reporting done

- Build / test / vet / formatter clean.
- New command appears in `<project> --help` output.
- `<project> report` runs end-to-end against a sample workspace.

## Final report

Under 200 words: files created, files edited, smoke-check status,
issues filed, deferred-to-integrator items.

Do NOT commit.
```

### `prompts/sprint-N/validator.md`

```markdown
You are playing the role described in `agents/validator.md`. Sprint N
scope: regression-gate the staff's implementation and architect's chapter,
plus re-verify example correctness on the touched chapters.

## Read first

- `agents/validator.md` — your role identity.
- `AGENTS.md` / `CLAUDE.md` — project conventions.
- `docs/PLAN.md` §"Sprint N" — sprint scope.
- `prompts/sprint-(N-1)/validator.md` — prior-sprint task shape.

## Coordinate with parallel agents

- **Architect** owns `book/src/`, `CHANGELOG.md`, `docs/`. File issues
  against their surface; don't edit it.
- **Staff** owns `internal/cli/`. File issues against their surface;
  don't edit it.

## Your scope

`scripts/` (test drivers), `.github/workflows/` (CI configs), lint
configs, your own issue file.

## Tasks (priority order)

1. Run the full regression sweep: `go build / test / vet / gofmt`.
   Any red → blocker → stop sprint progress.
2. Spot-check the new chapter's code examples against the actual
   `<project> report` surface from staff's implementation. File one
   issue per divergence.
3. Cross-link audit on the new chapter's `[Chapter X](./XX-...)` and
   `#anchor` references.
4. If the project has e2e drivers, run them in DRY_RUN mode as a smoke.

## Issue tracking

File at `issues/issue_sprint-N_validator.md`. One issue per finding.

## Verification before reporting done

- Build / test / vet / formatter status documented.
- New chapter's examples spot-checked against staff's implementation.
- Cross-link audit run.
- E2E DRY_RUN status documented (clean / red / sandbox-blocked).

## Final report

Under 200 words: regression status, chapters spot-checked, issues
filed (counts by severity), regression-gate verdict (any blockers?).

Do NOT commit.
```

### `prompts/sprint-N/tech-writer.md`

```markdown
You are playing the role described in `agents/tech-writer.md`. Sprint N
scope: read-only review of what the other three agents produced, plus
a dogfooding loop on the new chapter from a first-time-reader's
perspective.

## Context — what the other agents produced

- **Architect** added `book/src/<NN>-report-command.md`, updated
  `SUMMARY.md`, cross-linked from chapters X and Y, added a CHANGELOG
  Added entry.
- **Staff** implemented `<project> report` at `internal/cli/report.go`
  with a focused test.
- **Validator** ran the regression sweep and example-correctness pass;
  their issues live in `issues/issue_sprint-N_validator.md`.

## Tasks

1. **New chapter quality.** Voice / audience / runnable-examples /
   cross-links / no placeholder content.
2. **Dogfooding loop.** Read the new chapter as if you'd never used
   the tool. Where would a first-time reader get stuck? File one
   issue per stuck-point.
3. **Cross-document drift sweep.** Does CHANGELOG match the chapter?
   Does the chapter match the binary's actual flags (per staff's
   implementation)? Does the SUMMARY entry match the chapter's H1?
4. **Gate verdict.** Are there any blocker-class issues from the
   three agents that the integrator must resolve before committing?

## Issue tracking

`issues/issue_sprint-N_tech-writer.md`. Read-only — edit ONLY this file.

## Final report

Under 200 words: chapters reviewed, issues filed (counts by severity),
dogfooding stuck-points, drift caught, release-readiness verdict.

Do NOT edit any project file except your issue file. Do NOT commit.
```

### Putting it together

A typical sprint kickoff looks like:

1. **Human writes** `prompts/sprint-N/README.md` (the orchestrator's view) and the four role briefs above.
2. **Human (or an orchestrator agent) launches the four agents** — three in parallel (architect / staff / validator) and tech-writer queued for end-of-sprint. Each agent gets `agents/<role>.md` + `prompts/sprint-N/<role>.md` as its system prompt; the user message is something terse like *"Begin sprint N. Report back when verification gates are clean."*
3. **Each agent files** `issues/issue_sprint-N_<role>.md` and stops without committing.
4. **Tech-writer runs last** with the other three issue files as part of its read-first list.
5. **The integrator** (human or another agent) reads all four issue files, folds them, commits the aggregate, and (if appropriate) cuts the release tag.

## Adapting to other projects

The four-role split is project-shape-specific (it fits codebases with a meaningful prose surface — books, RFCs, PRDs — alongside code). For projects without that prose surface, you might collapse architect + staff into one role and skip tech-writer; or you might add a fifth role (security reviewer, performance gate). The pattern that generalises is:

- **One role per disjoint surface** the sprint has to touch
- **One read-only end-of-sprint role** to verify the aggregate
- **Issue files as the hand-off contract** between roles and between sprints
- **A separate integrator step** that commits and tags (the agents don't commit their own work)

The role files in this directory are starting points. Fork them, retarget the surfaces, adjust the off-limits boundaries for your project.

## License and reuse

These role files are written for this repo but are not project-specific in their content — they describe a pattern, not a particular codebase. Copy them into other projects; adjust the `## When to use` and `## Inputs you'll receive` sections to match the new project's conventions.
