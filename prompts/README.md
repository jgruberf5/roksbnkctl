# Agent dispatch prompts

This folder holds the verbatim prompts dispatched to parallel sub-agents
during sprint execution. One folder per sprint; one markdown file per
role per sprint:

```
prompts/sprint<N>/architect.md
prompts/sprint<N>/staff.md
prompts/sprint<N>/validator.md
```

## Why these are checked in

1. **Auditability** — for each sprint integration commit, the exact
   instructions each role received are preserved. Months later it's
   possible to answer "why did the staff-engineer agent do X?" by
   reading what it was told to do.

2. **Reproducibility** — a future session (different LLM, different
   contributor) can re-dispatch a role by reading the prompt file and
   sending it to its agent tool. The pattern doesn't depend on any one
   integrator remembering it.

3. **Refinement over time** — when a sprint surfaces "the staff
   prompt should have mentioned X" feedback, the prompt file gets
   updated for next sprint's analogous role. Compounds across sprints.

## Format

Each prompt file is plain markdown — the same content sent verbatim as
the `prompt` parameter on the `Agent` tool call. Sections typically
include:

- **Role + sprint identification** (one-line opener)
- **Project context** (where the repo is, key files agents need to
  read first)
- **Coordination notes** (which other agents are running in parallel,
  which files to leave alone)
- **Numbered tasks** (concrete deliverables with target paths)
- **Issue tracking format** (the `issues/issue_sprint<N>_<role>.md`
  schema agents follow when filing problems)
- **Verification before reporting done**
- **Final report shape** (constrains agent output length/format)

## Dispatching

To re-run a sprint role:

```bash
# Read the prompt
cat prompts/sprint1/staff.md

# In a Claude Code session, dispatch via the Agent tool:
Agent(
    description: "Sprint 1 staff engineer — SSH client",
    subagent_type: "general-purpose",
    prompt: "<paste contents of prompts/sprint1/staff.md>",
)
```

The integrator (human or LLM) is then responsible for aggregating output,
resolving issues filed by the agent, and creating
`issues/resolved_sprint<N>_<role>.md` per the `issues/` folder
convention.
