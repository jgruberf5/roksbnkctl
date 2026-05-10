# Extending roksbnkctl

This chapter is the hacking guide for contributors. It covers the four most common extension shapes — adding a new execution backend, a new test suite, a new tool to an existing backend, a new chapter to the book — plus the PRD process the project uses to coordinate larger changes and the four-agent sprint-dispatch pattern Sprints 0-6 ran on.

For *building* the binary, see [Chapter 31 — Building from source](./31-building-from-source.md). For *using* the binary, see the rest of the book.

## Adding a new execution backend

A backend is anything implementing the `Backend` interface in [`internal/exec/backend.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/exec). The four backends shipped at v1.0 — `local`, `docker`, `k8s`, `ssh:<target>` — are each a single file under that package.

The end-to-end shape:

1. **Implement the interface.** Create `internal/exec/<your-backend>.go`. The contract is `Run(ctx context.Context, argv []string, opts RunOpts) (int, error)`. Honour `opts.Stdin/Stdout/Stderr`, `opts.WorkDir`, `opts.Env`, `opts.Credentials`, `opts.HostMounts`, and `opts.RunAsUser`. Return the subprocess exit code as the first return; second is for backend-side errors (couldn't start, ctx cancelled, etc.).

2. **Register it.** Call `exec.Register(name string, b Backend)` from the package's `init()` block. The `ResolveBackend(spec string)` function in `internal/exec/backend.go` dispatches `--backend <name>` to the registered backend.

3. **Handle credentials safely.** Read [PRD 04](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md) before touching `opts.Credentials`. The cardinal rule: **never pass credential values via argv** — they end up in `ps` output, container metadata, and process accounting. Pass by reference (env var by name, projected Secret, SSH `SetEnv`) and let the runtime do the value plumbing.

4. **Wire the redactor.** Wrap `opts.Stdout` and `opts.Stderr` with [`internal/exec.NewRedactor`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/exec/redact.go) before handing them to the subprocess. The redactor masks any credential value that leaks into the tool's stdout/stderr. The `local` and `docker` backends do this in their wrappers; copy the pattern.

5. **Add a doctor check.** Doctor's per-backend availability check needs to recognise your backend. Add an entry under `internal/cli/doctor_backend.go` reporting whether your backend's prerequisites are satisfied (e.g., "is the daemon running", "is the SDK reachable"). Green-by-default on a stock dev box is the goal — yellow-skip rather than red-fail when prerequisites are missing.

6. **Add per-backend cred-audit assertions.** The cred-leak audit at [`internal/exec/audit_test.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/exec) (and Phase M of the e2e plan) needs to know what surfaces your backend produces — container inspection, process listing, log files. Add a `TestCredAudit_<YourBackend>` subtest asserting the API key value never appears in any of them.

7. **E2E phase.** Add a new phase to `scripts/e2e-test-backends.sh` with concrete pass/fail criteria. Cross-link from [PRD 05](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/05-E2E-TEST-PLAN.md) so the test plan stays the source of truth.

8. **Documentation.** Add a deep-dive subsection to [Chapter 17 — Execution backends](./17-execution-backends.md) and a decision-tree entry to [Chapter 18 — Choosing a backend per tool](./18-choosing-backend.md). Without docs the backend doesn't exist for users.

A backend PR that lands all eight steps is a complete contribution; one that lands the code but skips the audit and docs will get a "please come back with…" review comment.

## Adding a new test suite

The test subtree (`roksbnkctl test <suite>`) holds three suites at v1.0: `connectivity`, `dns`, `throughput`. Adding a fourth (e.g., `tls-handshake`, `latency`, `tcp-flowstate`) follows a five-step recipe:

1. **Implement the runner.** Create `internal/test/<suite>.go`. The suite produces results in the `roksbnkctl.<suite>.v1` JSON schema — pick a top-level shape consistent with the existing suites (`ProbeResult` for single-probe, `ProbeSuiteResult` for an aggregate with `results[]`).

2. **Wire a subcommand.** Add `internal/cli/test_<suite>.go` with a cobra command under `test`. The flag surface should mirror the existing suites' patterns — `--target`, `--iterations`, `-o json`, `--backend` (when the suite is backend-aware).

3. **Pick the backends.** Most test suites are backend-aware (the suite runs from a network vantage that the backend selects). DNS and throughput accept `local` / `k8s` / `ssh:<target>` and reject `docker`; connectivity is currently `local`-only. Decide which backends make sense for your suite — the deciding question is "does the vantage change the answer?".

4. **Wire the JSON schema constant.** Add `roksbnkctl.<suite>.v1` to your suite's output. CI assertions diff against this — bumping the version is a breaking change, document it in CHANGELOG.

5. **Add an E2E phase.** New phase under [PRD 05](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/05-E2E-TEST-PLAN.md) and corresponding script section in `scripts/e2e-test-backends.sh`.

6. **Documentation.** New chapter or major section in Part VI of the book (currently chapters 20-23). Cross-link from [Chapter 23 — The E2E test plan](./23-e2e-test-plan.md) and [Chapter 18 — Choosing a backend per tool](./18-choosing-backend.md).

The DNS probe (Sprint 5) is the canonical worked example — read the Sprint 5 architect prompt and the resulting `internal/test/dns.go` + `internal/cli/test.go` to see all five steps in their landed form.

## Adding a new tool to an existing backend

The `docker`, `k8s`, and `ssh` backends each maintain a map of tool-name → image / package. Adding a new tool (e.g., `mtr`, `tcpdump`, `helm`) means an entry in each backend's map.

### Docker backend

[`internal/exec/docker.go::toolImages`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/exec/docker.go) maps tool names to image specs:

```go
var toolImages = map[string]string{
    "ibmcloud":  "ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud",
    "iperf3":    "ghcr.io/jgruberf5/roksbnkctl-tools-iperf3",
    "terraform": "hashicorp/terraform:1.5.7",
    "<your>":    "<your-image-ref>",
}
```

Tag resolution is handled by `SetToolImageTag` (set in `internal/cli/root.go::init`) — a `:dev` tag for a from-source binary, `:<release-tag>` for a tagged release. If your image needs its ENTRYPOINT bypassed (e.g., for image-specific argv mangling), add a `jobToolCmdOverride` entry.

### K8s backend

[`internal/exec/k8s.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/exec/k8s.go) holds two patterns — long-lived ops pod (for tools that share state, like `ibmcloud`) and one-shot Job (for tools that produce a single output, like `iperf3` or DNS probes). New tools pick one pattern:

- **Ops pod**: add the tool's image to the ops pod's container spec at install time, or `kubectl exec` into the existing ops pod and run the host-installed binary.
- **One-shot Job**: build a Pod template using the same image conventions as iperf3, run, stream logs, capture exit code, delete. The Job pattern is the right call for tools where the result is the only thing that matters.

### SSH backend

[`internal/exec/ssh.go`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/exec/ssh.go) maintains a map of tool names to apt-package names for the `--bootstrap` auto-install:

```go
// toolPackage carries apt-repo metadata + package name; see the
// production form in internal/exec/ssh.go for the full struct shape
// (IBM repo URL + GPG key + apt-source line for ibmcloud-cli, etc.).
var toolPackages = map[string]toolPackage{
    "ibmcloud": { /* IBM apt repo + key + "ibmcloud-cli" */ },
    "iperf3":   { /* plain ubuntu-main "iperf3" */ },
    "<your>":   { /* repo + key + "<deb-package>" */ },
}
```

The bootstrap step runs `apt-get install -y <packages>` on the SSH target when the tool isn't already on PATH. Non-Debian targets are out of scope for v1.0; the bootstrap fails clearly with a message pointing at the manual-install path.

For each backend, the implementation work is small (one map entry). The doctor checks, e2e coverage, and docs are the bulk — same shape as adding a new backend, scaled to the smaller surface.

## Adding a new chapter to the book

The book is mdBook with markdown source under [`book/src/`](https://github.com/jgruberf5/roksbnkctl/tree/main/book/src). Adding a chapter:

1. Create `book/src/<NN>-<slug>.md` — the file. Numbered prefix for sort order.
2. Add the chapter to [`book/src/SUMMARY.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/book/src/SUMMARY.md) — the TOC. Use the existing parts (Concepts, Getting Started, Cluster Lifecycle, …) or add a new part if it doesn't fit.
3. Run `make book-serve` to live-preview at `http://localhost:3000` with auto-reload.
4. Cross-link from related chapters at the bottom (the "Cross-references" section every chapter ends with).
5. Push. [`.github/workflows/book.yml`](https://github.com/jgruberf5/roksbnkctl/blob/main/.github/workflows/book.yml) re-deploys to `gh-pages` on every merge to `main`.

The book follows a consistent style:

- Lower-case prose, sentence-case section headers.
- Code blocks for any command, inline `code` for filenames and identifiers.
- Short paragraphs, one idea each.
- Examples should be runnable as written.
- PRD references use the full GitHub URL (`https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md`) to avoid the published-book 404 issue surfaced in Sprint 1.

## The PRD process

The project uses numbered **Product Requirements Documents** under [`docs/prd/`](https://github.com/jgruberf5/roksbnkctl/tree/main/docs/prd) for larger feature work — anything that touches multiple files, spans more than one sprint, or has open design questions that need to be settled before code lands.

When a feature warrants a PRD vs. a direct PR:

| Use a PR | Use a PRD |
|---|---|
| Single-file change | Multi-file change across `internal/{exec,cli,config,…}` |
| Bug fix | New subsystem (a new backend, a new test suite) |
| Doc fix | New surface that needs a stable contract (a JSON schema, a workspace-config field) |
| Refactor with no behaviour change | A change that needs threat-model thinking (creds, network, multi-tenancy) |
| Drive-by polish | Anything cross-cutting >50 LOC |

The PRD lifecycle:

1. **Draft**: open as a markdown file under `docs/prd/NN-<TITLE>.md`. The structure should follow the existing PRDs ([00-OVERVIEW](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/00-OVERVIEW.md), [01-SSH](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/01-SSH-AND-ON-FLAG.md), [02-KUBECTL](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/02-KUBECTL-INTERNAL.md), [03-BACKENDS](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md), [04-CREDS](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md), [05-E2E](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/05-E2E-TEST-PLAN.md)): goal, approach, file-by-file plan, test plan, acceptance criteria, open questions.
2. **Review**: open a PR adding the PRD. Discuss in the PR. Open questions get resolved by edit or by punting to a follow-up issue.
3. **Implement**: the PRD becomes the implementation plan. Per-sprint tasks land in [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) referencing the PRD by number.
4. **Land**: code PRs reference the PRD; the PRD itself is the *spec*, code is the *implementation*. When the implementation diverges from the PRD, the PRD gets updated to match — never the other way around (the binary's behaviour is the source of truth).

The PLAN.md per-sprint planning rhythm interleaves code + tests + docs per sprint. Each sprint's prompts (under `prompts/sprint<N>/`) translate the PLAN into concrete agent tasks.

## The four-agent sprint dispatch

Larger sprints (Sprints 3-6) are dispatched as four parallel agents:

- **Architect** — designs the surface, drafts the book chapters that explain it, files architect-side issues.
- **Staff engineer** — writes the production Go and shell code, modifies the bundled HCL when needed.
- **Tech-writer** — reviews the architect's chapters for accuracy, fluency, and cross-link integrity. Files tech-writer-side issues.
- **Validator** — writes / extends the e2e test scripts and CI workflows, files validator-side issues.

The dispatch lives at `prompts/sprint<N>/{architect,staff,tech-writer,validator}.md` — one prompt per agent. Each agent runs independently against the same repo snapshot. An integrator at the end folds the four agents' outputs together, resolves the issues each filed against the others, and commits the aggregate.

When to dispatch four agents vs. just open a PR:

| Direct PR | Four-agent sprint |
|---|---|
| Single feature, single sprint, <10 files | Multi-feature sprint with code + docs + tests scope |
| Bug fix | New PRD landing |
| Drive-by improvement | Sprint-gate milestone work |
| You're the only contributor | Coordinating with reviewers who'd otherwise serialise |

`prompts/README.md` documents the agent-coordination pattern. The sprint dispatch is the project's way of running review-and-implementation in parallel rather than serial — it works when the surfaces are well-separated (code vs docs vs tests don't conflict on file ownership) and the integrator has enough context to merge the four lanes.

## Cross-references

- [Chapter 17 — Execution backends](./17-execution-backends.md) — the four-backend matrix you're extending.
- [Chapter 19 — The in-cluster ops pod](./19-in-cluster-ops-pod.md) — the k8s-backend pattern your new tool might join.
- [Chapter 20-22](./20-connectivity-testing.md) — the three existing test suites your new suite would join.
- [Chapter 23 — The E2E test plan](./23-e2e-test-plan.md) — where your new phase belongs.
- [Chapter 31 — Building from source](./31-building-from-source.md) — the build-side counterpart to the hacking side.
- [PRD 00 — Overview](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/00-OVERVIEW.md) — the PRD index.
- [`docs/PLAN.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/PLAN.md) — the per-sprint planning rhythm.
- [`prompts/README.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/prompts/README.md) — the four-agent dispatch pattern.
