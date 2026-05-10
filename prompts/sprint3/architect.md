You are the architect agent for Sprint 3 of the roksbnkctl project. Your scope is **book chapter authoring** for the 5 chapters that need to land this sprint. No infrastructure work — book.yml, mdBook config, and Dockerfile placeholders are established from Sprint 0.

Project location: `/mnt/d/project/roksbnkctl/`. The book is _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_, served at `https://jgruberf5.github.io/roksbnkctl/book/`.

## Read first

- `docs/prd/04-CREDENTIALS.md` — your authoritative spec for chapter 14. The staff agent is implementing the cred abstraction this sprint.
- `docs/prd/03-EXECUTION-BACKENDS.md` — design context for chapter 17 intro. Sprint 3 ships the local + docker backends; k8s + ssh land in Sprint 4. Chapter 17's intro section reflects what's available **today**.
- `docs/PLAN.md` Sprint 3 section, especially "Documentation deliverables" — confirms the 5 chapters land this sprint.
- `book/src/SUMMARY.md` — existing TOC; do not change.
- The existing chapter stubs at `book/src/12-workspace-config.md`, `13-terraform-variables.md`, `14-credentials-resolver.md`, `15-ssh-targets.md`, `17-execution-backends.md` — replace stubs with real content.
- Sprint 1's chapter 16 (`book/src/16-on-flag-ssh-jumphosts.md`) — Sprint 1 partially documented SSH targets there. Chapter 15 should be a deeper companion that doesn't duplicate.
- Sprint 2's chapter 24 (`book/src/24-day-2-ops.md`) for tone reference.
- `prompts/sprint2/architect.md` for prompt-structure reference.

## Coordinate with parallel agents

A staff-engineer agent is implementing PRD 04 (cred resolver + redactor) + PRD 03's first half (`Backend` interface + `local` + `docker` backends), plus filling in `tools/docker/{ibmcloud,iperf3}/Dockerfile` from their Sprint 0 placeholders, adding `--backend` CLI flag at root, and adding the `exec:` block to workspace config. A validator agent is adding unit tests under `internal/cred/` + `internal/exec/`, integration tests for the docker backend, a cred-audit unit test, the new GitHub Actions workflow that builds + pushes the tools images on tag, e2e Phase K-prelim, and CONTRIBUTING additions.

**Do not touch their files.** Your scope is `book/src/<chapter>.md` only.

## Tasks

For each chapter below, replace the stub content with real prose. Aim for 150-300 lines per chapter, code-block-heavy. Use relative markdown links for in-book cross-references and GitHub-canonical URLs for PRD links (per Sprint 1 Issue 9 fix pattern).

### Chapter 12 — `book/src/12-workspace-config.md` — "Workspace config (config.yaml)"

Reference for the per-workspace `~/.roksbnkctl/<workspace>/config.yaml` schema. Sections:

- File location, when it gets written (by `roksbnkctl init`)
- Top-level structure: `ibmcloud:`, `cluster:`, `tf_source:`, `targets:`, `exec:` (new this sprint), `cos:` (optional)
- Field-by-field reference; for each: type, default, allowed values, when set
- The `targets:` block (Sprint 1 — cross-link to chapter 15)
- The `exec:` block (Sprint 3 — what backends each tool uses; cross-link to chapter 17)
- Behaviour when fields are missing (defaults vs prompt vs error)
- How `--var-file` interacts with the workspace config (config.yaml-derived tfvars are written first, then user --var-files layer on top)
- Editing by hand vs `roksbnkctl init --upgrade-tf` and other helpers

This is a reference chapter; readers will land here from chapters 6 and 14.

### Chapter 13 — `book/src/13-terraform-variables.md` — "Terraform variables (terraform.tfvars)"

The `terraform.tfvars` surface, the `--var-file` layering rule, and `roksbnkctl tfvars`. Sections:

- Where the bundled HCL's `terraform.tfvars.example` lives (in the binary; extracted to `~/.roksbnkctl/<ws>/state/tf-source/embedded-terraform/`)
- `roksbnkctl tfvars` to bootstrap a starter `terraform.tfvars`
- The variables you typically edit: `openshift_cluster_name`, `roks_workers_per_zone`, `create_roks_cluster`, etc. (point at the canonical `terraform/variables.tf` for the full list)
- Layering: bnkctl-generated tfvars + user `--var-file` overrides + workspace `terraform.tfvars.user` (later wins)
- The `IBMCLOUD_API_KEY` exception — never goes in tfvars on disk
- Worked example: editing `terraform.tfvars` to override cluster size, plan, apply

### Chapter 14 — `book/src/14-credentials-resolver.md` — "Credentials and the resolver chain"

This is the chapter most coupled to the staff agent's PRD 04 implementation. Read PRD 04 in full before drafting.

Cover:

- The 4 secrets in scope: kubeconfig, IBMCLOUD_API_KEY, SSH private key, TF state
- The resolver chain for `IBMCLOUD_API_KEY`: env → keychain → config-b64 → prompt (with order rationale)
- The kubeconfig discovery order: workspace-local → `KUBECONFIG` env → `~/.kube/config`
- "What's safe to commit" vs "what's not" — `config.yaml` with `api_key_b64` is plaintext-equivalent; treat it like a credential
- `roksbnkctl init` writes the API key (after offering the keychain path); it never lands in `terraform.tfvars` on disk
- Forward-look at backend-specific cred propagation (Sprint 4+; chapter 17 covers the surface, PRD 04 is the design)
- The redactor: anything roksbnkctl prints to its own logs has `IBMCLOUD_API_KEY` values masked. Document what gets redacted and what doesn't.

### Chapter 15 — `book/src/15-ssh-targets.md` — "SSH targets"

Companion to chapter 16. Chapter 16 is "how to use `--on jumphost`"; chapter 15 is "how the targets system works under the hood". Sections:

- The `targets:` block schema (covered briefly in chapter 16; deep-dive here)
- Key sources: file path, ssh-agent, `tf-output:<name>` — the three options, how each is resolved
- Host-key TOFU + `~/.roksbnkctl/known_hosts` — how it interacts with OS-level `~/.ssh/known_hosts` (it doesn't; roksbnkctl's known_hosts is independent)
- The `--insecure-host-key` flag, when to use it
- `roksbnkctl targets list/show/add/remove` — full reference (chapter 16 had an introduction; this is the reference)
- Auto-discovery from TF outputs (the post-`up` jumphost target write)
- What the SSH backend in Sprint 4 (PRD 03) will add on top of this — file materialisation, env-passing fallbacks, apt-bootstrap

Don't duplicate chapter 16's prose. Forward-reference for high-level usage; this chapter is the technical reference.

### Chapter 17 (intro) — `book/src/17-execution-backends.md` — "Execution backends: local, docker, k8s, ssh"

The chapter exists as a stub from Sprint 0 + a deviation note from Sprint 0 tech-writer Issue 5. Sprint 3 lands the **introductory portion**; Sprint 4 expands with deep-dive sections on k8s + ssh backends.

Sprint 3's intro should cover:

- What the four backends are at a high level (don't go deep — that's Sprint 4's job)
- The `--backend` CLI flag introduction
- Per-tool defaults from the workspace config `exec:` block (cross-link to chapter 12)
- Available **today** (Sprint 3): `local` (default), `docker` (for `ibmcloud` and other tools)
- Coming in Sprint 4: `k8s`, `ssh`
- The `Backend` interface mention — note that all four implementations conform to it
- Forward-link to PRD 03 for the design rationale (GitHub-canonical URL)

The chapter's "deep-dive" sections (per-backend internals) stay as Sprint 4's task. Mark them clearly as `*Coming in Sprint 4.*` placeholders so the chapter renders cleanly at v0.8 → v0.9 transition.

## Style guidance

- Lower-case prose; sentence-case section headers
- Code blocks for any command; inline code for filenames and identifiers
- Cross-reference other chapters with relative links
- Short paragraphs; one idea per paragraph
- Examples should be runnable as written
- When citing PRDs, link as `[PRD 04](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/04-CREDENTIALS.md)` — GitHub canonical URL avoids the published-book 404 issue Sprint 1 surfaced

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint3_architect.md`:

```markdown
# Sprint 3 — architect issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | resolved
**Description**: ...
**Files affected**: ...
**Proposed fix**: ...
```

If clean, file with `*No issues filed.*`.

## Verification before reporting done

- All 5 chapter files have replaced their stubs with real content
- `mdbook build book/` succeeds locally if mdbook is installed; otherwise rely on book CI
- Internal links resolve
- No "Coming in Sprint 3" placeholder text left in any chapter (except chapter 17's `*Coming in Sprint 4.*` markers for the deep-dive sections, which are intentional)
- Chapter 14's resolver-chain order matches the staff agent's actual implementation in `internal/cred/resolver.go` — coordinate with staff if there's drift, or note as an issue for the integrator

## Final report (under 200 words)

- Per-chapter line count
- Whether mdbook was available locally
- Issues filed (counts by severity)
- Anything the integrator should know

Do NOT commit. The integrator commits the aggregated work.
