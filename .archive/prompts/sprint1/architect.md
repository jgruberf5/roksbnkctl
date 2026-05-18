You are the architect agent for Sprint 1 of the roksbnkctl project. Your scope this sprint is **book chapter authoring** for the foundational user-facing chapters that need to land for the v0.7 release. No infrastructure work this sprint — `book.yml`, `book.toml`, and all 32 stubs already exist from Sprint 0.

Project location: `/mnt/d/project/roksbnkctl/`. The book is _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_, served at `https://jgruberf5.github.io/roksbnkctl/book/` after first push to `main`.

## Read first

- `docs/prd/01-SSH-AND-ON-FLAG.md` — the Sprint 1 PRD that the staff agent is implementing. Chapter 16 (`--on` flag) describes that work for end users.
- `docs/PLAN.md` Sprint 1 section, especially "Documentation deliverables" — confirms which 6 chapters land this sprint.
- `book/src/SUMMARY.md` — existing TOC; do not change ordering or filenames.
- The existing chapter stubs at `book/src/01-*.md`, `02-*.md`, `03-*.md`, `04-*.md`, `07-*.md`, `16-*.md` — these are placeholder stubs ("*Coming in Sprint 1.*") that you'll replace with real content.
- README.md and the existing `docs/` for tone reference. Keep the clipped technical voice, code-block-heavy, lower-case prose.
- `prompts/sprint0/architect.md` — Sprint 0's architect prompt; the structure (coordination notes, verification, final report) is reusable.

## Coordinate with parallel agents

A staff-engineer agent is implementing PRD 01 across `internal/remote/`, `internal/cli/`, `internal/config/workspace.go`, and `scripts/`. A validator agent is adding integration tests in `internal/remote/integration_test.go`, extending `scripts/e2e-test.sh`, and possibly editing `.github/workflows/ci.yml`. **Do not touch their files.** Your scope is `book/src/<chapter>.md` only.

## Tasks

For each chapter below, replace the stub content with real prose. Aim for ~150-300 lines per chapter, code-block-heavy where it makes sense. Use relative markdown links (`[Workspaces](./06-workspaces.md)`) for cross-references so mdBook's link checker can verify them.

### Chapter 1 — `book/src/01-what-is-bnk.md` — "What is BIG-IP Next for Kubernetes (BNK)"

What it is, what problem it solves, where it fits in F5's product family. Keep it factual; this is a context chapter for a reader who knows generic Kubernetes but might be new to BNK. Sections worth covering:

- One-paragraph elevator pitch
- The components (FLO, CIS, CNE, BIG-IP TMM data plane) — brief; deeper chapters reference back here
- Where it runs (managed Kubernetes; ROKS specifically for this book)
- What problems it solves (north-south + east-west traffic management with L4/L7 features in-cluster)
- A pointer to F5's official BNK docs for definitive product info

### Chapter 2 — `book/src/02-why-roks.md` — "Why ROKS"

Why this book targets IBM Cloud's managed OpenShift specifically. Cover:

- ROKS = Red Hat OpenShift on IBM Cloud (managed)
- What IBM manages vs what the customer manages (control plane, masters, etcd, worker provisioning, security patches)
- Why managed-OpenShift over self-managed for BNK evaluation (skip a multi-week lift, start at "deployed cluster")
- Note that other Kubernetes flavors are out of scope for this book

### Chapter 3 — `book/src/03-what-roksbnkctl-does.md` — "What roksbnkctl does (and doesn't do)"

The tool's scope and explicit non-goals. Sections:

- The 3-command happy path: `init` → `up` → `test`
- What it owns: workspace state, Terraform-exec, kubeconfig fetch, COS supply chain, post-deploy validation
- What it doesn't try to do: not a generic IBM Cloud CLI (that's `ibmcloud`), not a generic Kubernetes CLI (that's `kubectl`), not an OpenShift admin tool (that's `oc`), not a BNK runtime UI
- The relationship to bundled HCL: `terraform/` lives in this repo, embedded into the binary; `tf_source: github|local` overrides exist for power users
- One-paragraph forward look at what's coming in v0.8/0.9/1.0 (kubectl internalization, four execution backends, GSLB DNS testing)

### Chapter 4 — `book/src/04-installation.md` — "Installation"

How to get the binary on your machine. Cover:

- Build from source (the canonical path until release artifacts ship): `git clone`, `make build`, `bin/roksbnkctl install`
- Docker-based build (no host Go required) — copy the snippet from README.md verbatim
- Install destination (`~/.local/bin/roksbnkctl` by default; `--dir` to override)
- Verifying: `roksbnkctl --version`, `roksbnkctl doctor`
- OS support matrix: Linux + macOS first-class, Windows compile-only this round
- Mention that `terraform` is the only required prereq (post-Sprint 2 v0.8 release)

### Chapter 7 — `book/src/07-quick-start.md` — "Quick start"

The `init` → `up` → `test` happy path with sample output. Walk a reader from "I have an IBM Cloud API key" to "deployed BNK with a passing test". Include:

- `roksbnkctl init` (interactive prompts for region, RG, cluster name)
- `roksbnkctl up --auto` (terraform plan + apply, ~50 min for fresh ROKS + BNK)
- `roksbnkctl status` (verify cluster + pods)
- `roksbnkctl test connectivity` and `test dns` examples
- `roksbnkctl down --auto` (teardown)
- A note that example output is illustrative; real output varies

### Chapter 16 — `book/src/16-on-flag-ssh-jumphosts.md` — "The --on flag and SSH jumphosts"

The Sprint 1 feature, end-user perspective. Cover:

- Why this exists: pre-cluster execution, customer firewalls, air-gapped scenarios
- The `targets:` workspace config block (use the example from PRD 01)
- Auto-discovered jumphost from `roksbnkctl up`'s TF outputs
- Key sources: file path, ssh-agent, `tf-output:<name>`
- Host-key TOFU on first connect; `--insecure-host-key` flag for CI
- `roksbnkctl targets list/show/add/remove` command tree
- Working examples:
  ```bash
  roksbnkctl exec --on jumphost -- whoami
  roksbnkctl shell --on jumphost
  roksbnkctl ibmcloud --on jumphost ks cluster ls
  ```
- Cross-link forward to Chapter 17 (Execution backends) which the SSH backend in Phase 3 builds on

Read PRD 01 for the design rationale behind these choices; you're translating PRD-developer-prose into book-end-user-prose.

## Style guidance

- Lower-case prose; sentence-case section headers
- Code blocks for any command; inline code for filenames and identifiers
- Cross-reference other chapters with relative links
- Short paragraphs; one idea per paragraph
- Examples should be runnable as written (assume the reader pasted them into a fresh shell)
- When citing PRDs, link as `[PRD 01](../../docs/prd/01-SSH-AND-ON-FLAG.md)` — the relative path works because mdBook serves from `book/src/`

## Issue tracking

Issues to `/mnt/d/project/roksbnkctl/issues/issue_sprint1_architect.md`:

```markdown
# Sprint 1 — architect issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | resolved
**Description**: ...
**Files affected**: ...
**Proposed fix**: ...
```

If clean, file with `*No issues filed.*`.

## Verification before reporting done

- All 6 chapter files have replaced their stubs with real content
- `mdbook build book/` succeeds locally if mdbook is installed; otherwise rely on CI
- Internal links resolve (relative paths to other chapters or to `../../docs/prd/...`)
- No "Coming in Sprint 1" placeholder text left in any of the 6 chapters

## Final report (under 200 words)

- Per-chapter line count
- Whether mdbook was available locally and what `mdbook test book/` reported (broken-link check)
- Issues filed (counts by severity)
- Anything the integrator should know

Do NOT commit. The integrator commits the aggregated work.
