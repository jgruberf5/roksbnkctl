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

## Adapting to other projects

The four-role split is project-shape-specific (it fits codebases with a meaningful prose surface — books, RFCs, PRDs — alongside code). For projects without that prose surface, you might collapse architect + staff into one role and skip tech-writer; or you might add a fifth role (security reviewer, performance gate). The pattern that generalises is:

- **One role per disjoint surface** the sprint has to touch
- **One read-only end-of-sprint role** to verify the aggregate
- **Issue files as the hand-off contract** between roles and between sprints
- **A separate integrator step** that commits and tags (the agents don't commit their own work)

The role files in this directory are starting points. Fork them, retarget the surfaces, adjust the off-limits boundaries for your project.

## License and reuse

These role files are written for this repo but are not project-specific in their content — they describe a pattern, not a particular codebase. Copy them into other projects; adjust the `## When to use` and `## Inputs you'll receive` sections to match the new project's conventions.
