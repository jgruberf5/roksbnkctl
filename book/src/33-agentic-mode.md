# Agentic mode

Agentic mode lets you drive a ROKS + BNK trial with an AI coding agent (Claude
Code, Gemini CLI, aider, …) acting under **role-scoped personas**, while
`roksbnkctl` stays a deterministic tool. The binary embeds **no LLM** — you
bring your own agent CLI; `roksbnkctl` provides the shared knowledge base
(`AGENTS.md`), the role contracts (`personas/`), and a coordination substrate
(`journal/`, `decisions.md`) scaffolded into the workspace.

It's markdown plus a launcher. Nothing about agentic mode changes how the
lifecycle commands behave.

## Quick start

```bash
roksbnkctl -w acme agent init        # scaffold AGENTS.md + personas/ + journal/ into the workspace
roksbnkctl -w acme agent             # list supported CLIs + the workspace default
roksbnkctl -w acme agent claude      # print the invocation to launch Claude Code there
```

`agent init` writes into the workspace dir (`~/.roksbnkctl/<workspace>/`,
alongside `config.yaml`):

```
AGENTS.md            shared reference every persona reads first
CLAUDE.md            one-line @AGENTS.md include for Claude Code
personas/            the four role contracts
decisions.md         decision-log seed
journal/             append-only timeline (handoffs live here)
```

Re-running `agent init` never clobbers your edits — existing files are left
as-is.

## The personas

Act as exactly **one** at a time. Each persona file defines an identity, ordered
goals, a tool allowlist, what's explicitly *not* allowed, a handoff protocol,
and a phase checklist.

| Persona | Role | Touches infra? | Talks to customer? |
|---|---|---|---|
| `solution-architect` | Owns scope (`config.yaml`) + `decisions.md`; drives the phases | No (delegates) | **Yes** (the only one) |
| `cloud-operator` | Runs the lifecycle (`cluster up`, `bnk up`, …), gates destructive steps | **Yes** | No |
| `test-engineer` | Runs connectivity/DNS/throughput probes, records evidence | No (read + test) | No |
| `doc-specialist` | Reads everything, writes `report.md` | No | No |

The boundaries are enforced by convention (the allowlists in each persona
file), not by code — so the personas work with any agent CLI.

## How they coordinate

`config.yaml` is the contract (scope/source of truth). The personas hand off
through the **append-only** journal:

```bash
roksbnkctl -w acme journal add "cloud-operator: cluster up complete, nodes Ready"
roksbnkctl -w acme journal list      # chronological, with one-line summaries
roksbnkctl -w acme journal report    # assemble report.md from decisions.md + the journal
```

Never edit another persona's journal entry — append a follow-up instead. The
solution-architect requests infra work by journaling a request (with the
consent tier); the cloud-operator executes and journals the result; the
doc-specialist turns the timeline into the customer-facing `report.md`.

## Choosing the CLI + endpoint

`roksbnkctl agent <cli>` supports `claude`, `gemini`, `aider`, `openai`, `pi`,
`opencode`, and `agy` (Google). Set the workspace default and an optional LLM
endpoint in `config.yaml`:

```yaml
agent:
  default: claude
  llm_endpoint: ""   # OpenAI-/Anthropic-compatible base URL (cloud vendor or local vLLM); blank = the CLI's own config
```

The `agy` recipe seeds the persona with `-i`, which runs an initial prompt and
then continues interactively. That is deliberate: `pi` and `opencode` auto-load
`AGENTS.md` from the working directory, and their recipes rely on it, but `agy`
does not advertise that behaviour — so its recipe passes the instruction rather
than assuming the file is read.

`roksbnkctl agent <cli>` **launches** the agent, in the workspace directory, with
your terminal attached. Add `--show` to print the invocation instead of running
it — useful for putting it in a script, or for `openai`, whose recipe is guidance
for whichever OpenAI-compatible REPL you use rather than a single command.

```console
$ roksbnkctl agent agy            # launches agy in the workspace
$ roksbnkctl agent agy --show     # prints the invocation, runs nothing
```

`roksbnkctl` still embeds no LLM and sends nothing anywhere itself: it execs your
CLI, exactly as `roksbnkctl kubectl` execs kubectl. Your CLI uses your credentials
and endpoint.

**Do not pipe the `--show` output into a shell.** `roksbnkctl agent <cli> --show |
bash` looks like the obvious shortcut and is silently wrong: `bash`'s standard
input is the pipe carrying the recipe, so the agent's first turn is the remaining
*comment lines of its own recipe*. Nothing errors — the session simply begins as
though you had typed them. If you need to run a printed recipe, use
`eval "$(roksbnkctl agent <cli> --show)"`, which leaves your terminal on standard
input.

Running without `--show` is refused when standard output is not a terminal. An
agent session needs one, and capturing it — `$(...)` or a pipe — would send the
model's output somewhere it does not belong.

## Safety model

The personas inherit `roksbnkctl`'s existing guard-rails rather than replacing
them:

- Destructive commands (`cluster up`, `down`, `cluster down`, `cleanup`) still
  prompt unless `--auto` is passed — and the persona contracts forbid passing
  `--auto` to `cluster up`/`cleanup` without journaled consent.
- Secret-tier commands (`roksbnkctl apikey`) and reading key/JWT/token material
  are off-limits to every persona except where a role genuinely needs it.
- `forge/kubeconfig.yaml` carries the cluster's admin client certificate/key
  (ROKS/OpenShift rejects raw IAM tokens) — treat it as secret-tier and never
  copy it into deliverables.

See the scaffolded `AGENTS.md` for the full operator reference (the phased
lifecycle, the destructive-command gate contract, and the field-tested
gotchas).
