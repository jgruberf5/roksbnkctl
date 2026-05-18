You are the architect agent for Sprint 2 of the roksbnkctl project. Your scope is **book chapter authoring** for the 7 chapters that need to land for the v0.8 release. No infrastructure work this sprint — all infra (book.yml, mdBook config, GitHub Pages publish path) is established from Sprints 0/1.

Project location: `/mnt/d/project/roksbnkctl/`. The book is _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_, served at `https://jgruberf5.github.io/roksbnkctl/book/`.

## Read first

- `docs/prd/02-KUBECTL-INTERNAL.md` — the Sprint 2 PRD that the staff agent is implementing. Chapter 24 (Day-2 ops) is the user-facing surface for that work.
- `docs/PLAN.md` Sprint 2 section, especially "Documentation deliverables" — confirms which 7 chapters land this sprint.
- `book/src/SUMMARY.md` — existing TOC; do not change ordering or filenames.
- The existing chapter stubs at `book/src/05-doctor.md`, `06-workspaces.md`, `08-cluster-phase.md`, `09-registering-existing-cluster.md`, `10-deploying-bnk-trials.md`, `11-tearing-down.md`, `24-day-2-ops.md` — these are placeholder stubs you'll replace with real content.
- Sprint 1's chapter 4 (`book/src/04-installation.md`), chapter 7 (`book/src/07-quick-start.md`), chapter 16 (`book/src/16-on-flag-ssh-jumphosts.md`) — reference for tone, structure, code-block style, GitHub-canonical URLs for PRD links.
- `prompts/sprint1/architect.md` — Sprint 1's architect prompt as a template. Sprint 2's chapters are operational where Sprint 1's were introductory; adjust accordingly.

## Coordinate with parallel agents

A staff-engineer agent is implementing PRD 02 across `internal/k8s/`, `internal/cli/k_*.go`, and editing `internal/cli/doctor*.go` + `internal/cli/cluster.go` (to mark `kubectl`/`oc` passthrough as informational). A validator agent is adding fake-clientset unit tests under `internal/k8s/*_test.go`, golden-file byte-equivalence tests, editing `.github/workflows/ci.yml`, patching `scripts/e2e-test.sh` Phase D (replace D3 with native `roksbnkctl k get`), and updating `docs/E2E_TEST.md`.

**Do not touch their files.** Your scope is `book/src/<chapter>.md` only.

## Tasks

For each chapter below, replace the stub content with real prose. Aim for 150-300 lines per chapter, code-block-heavy. Use relative markdown links (`[Workspaces](./06-workspaces.md)`) for in-book cross-references and GitHub-canonical URLs for PRD links (see Sprint 1's chapter 16 for examples).

### Chapter 5 — `book/src/05-doctor.md` — "Doctor: checking your environment"

The `roksbnkctl doctor` command end to end. Sections worth covering:

- What doctor checks (the row format from chapter 4 — re-explain briefly, then deep-dive)
- Each check explained: terraform, iperf3, kubectl, oc, ibmcloud, kubeconfig, workspace, ibmcloud api key, ibm cloud auth
- Sprint 2 changes: kubectl + oc downgraded from "needed" to "informational" (since `roksbnkctl k get` etc. cover the happy path natively); the row format may have a new column for backend-specific checks (BackendName from internal/doctor/check.go)
- Common failures + fixes (this is the chapter readers land on when something's broken)
- The `doctor --target <name>` SSH check from Sprint 1
- How to read the exit code (0 if all green or warnings only; non-zero on errors)

### Chapter 6 — `book/src/06-workspaces.md` — "Workspaces"

Per-environment isolation modeled on kubectl contexts. Sections:

- The `~/.roksbnkctl/<workspace>/` layout (config.yaml, state/, cluster-outputs.json)
- The 3-command path (init/use/list)
- `roksbnkctl ws new`, `ws use`, `ws current`, `ws list`, `ws delete --force`
- The current-workspace pointer (`~/.roksbnkctl/config.yaml`'s `current_workspace`)
- The `-w` / `--workspace` flag for one-off overrides
- The "parking-lot pattern" — Phase H's e2e cleanup uses an `e2e-cleanup` workspace because `ws delete` refuses to drop the current workspace. Document this pattern as a tip for users who want to delete their default workspace.
- Forward-link to Chapter 12 (workspace config, Sprint 3) for schema details

### Chapter 8 — `book/src/08-cluster-phase.md` — "The cluster phase"

`roksbnkctl cluster up` / `down` lifecycle that's separate from BNK trials. Sections:

- What's deployed (cluster + cert-manager + jumphost), what's not (BNK itself — that comes from `roksbnkctl up`)
- The two-state-dir model (`state-cluster/` vs `state/`)
- The `deploy_bnk=false` override the cluster-phase command forces
- The `cluster-outputs.json` written on success
- Worked example: cluster up → kubectl get nodes → cluster down
- Cross-link to chapter 9 (register existing) + chapter 10 (deploy BNK on top)

### Chapter 9 — `book/src/09-registering-existing-cluster.md` — "Registering an existing cluster"

`roksbnkctl cluster register` for clusters not created by roksbnkctl. Sections:

- When to use this (an existing ROKS cluster you want roksbnkctl to manage BNK on)
- Required input: cluster name + COS instance name
- Auto-discovered fields (vpc_id, region, RG, cluster_id) via the IBM SDK
- The `cluster-outputs.json` write
- The naming convention for COS instances (cluster-name-cos-instance) — see PRD 03 § cluster register for the convention
- Worked example: register canada-roks → show → use as if you'd done cluster up

### Chapter 10 — `book/src/10-deploying-bnk-trials.md` — "Deploying BNK trials on top"

The `roksbnkctl up` path, assuming a cluster already exists (registered or cluster-up'd). Sections:

- What "deploying BNK" means: helm + cert-manager + flo + cne_instance + license + cluster-side bits
- The 77-resource shape (give or take per upstream HCL changes)
- Cross-link to chapter 7's quick start which already shows the happy path; this chapter goes deeper on what each module does
- The token rotation observation (re-running `up` against an existing cluster replaces ~41 helm null_resources because admin-cert tokens rotate between runs)
- Reading TF plan output: + create vs <= read vs # destroy
- Forward-link to chapter 11 (down) and chapter 22 (throughput testing the deployed BNK)

### Chapter 11 — `book/src/11-tearing-down.md` — "Tearing down"

`roksbnkctl down` and `roksbnkctl cluster down`. Sections:

- The two destroys: trial vs cluster phase
- Order matters: BNK down first (trial), then cluster down — the upstream HCL's resource graph requires this
- What survives (the workspace's config.yaml; the COS instance's bucket if the deployment uploaded artefacts to it)
- `--auto` for non-interactive
- Cleaning up workspaces: `ws delete --force` after the destroy completes (and the parking-lot trick from chapter 6 if it's the current workspace)
- Cost note: an undestroyed cluster keeps billing IBM Cloud — point to where to verify (the IBM Cloud console)

### Chapter 24 — `book/src/24-day-2-ops.md` — "Day-2 ops"

This is the chapter most coupled to the staff agent's PRD 02 implementation. Cover:

- Sprint 2 introduces the **internalised k8s commands**: `roksbnkctl k get`, `k apply`, `k describe`, `k delete`, `k logs`, `k exec`, `k port-forward`. Top-level aliases for the most common verbs (`roksbnkctl get`, `apply`, `logs`).
- Why: drops the kubectl host install requirement for the everyday workflow.
- The `kubectl`/`oc` passthroughs **stay** as escape hatches. When to reach for the passthrough (rare flags, `kubectl rollout`, anything not in the internalised subset).
- Worked examples for each verb. Read the staff agent's actual implementation — `internal/cli/k_*.go` files — to verify your example commands work as-written. Don't paraphrase from PRD 02; the staff agent's choices may have diverged.
- Output format compatibility: `-o yaml/json/wide/jsonpath` should match `kubectl` byte-for-byte for unstructured resources. Note this and link to the validator agent's golden-file tests in `internal/k8s/*_golden_test.go`.
- The OpenShift extensions (Phase 2.1): `roksbnkctl k get projects/routes/imagestreams` — only mention if staff implemented these; PLAN.md scopes them as Phase 2.1 which may slip.
- Doctor change: `kubectl`/`oc` are now informational (not warnings) when missing.

Chapter 24 should read as the canonical reference for "what's the kubectl-equivalent in roksbnkctl?" — a reader migrating from kubectl muscle memory should be able to use this chapter as their cheat sheet.

## Style guidance

- Lower-case prose; sentence-case section headers
- Code blocks for any command; inline code for filenames and identifiers
- Cross-reference other chapters with relative links
- Short paragraphs; one idea per paragraph
- Examples should be runnable as written
- When citing PRDs, link as `[PRD 02](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/02-KUBECTL-INTERNAL.md)` — GitHub canonical URL avoids the published-book 404 issue Sprint 1 surfaced

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint2_architect.md`:

```markdown
# Sprint 2 — architect issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | resolved
**Description**: ...
**Files affected**: ...
**Proposed fix**: ...
```

If clean, file with `*No issues filed.*`.

## Verification before reporting done

- All 7 chapter files have replaced their stubs with real content
- `mdbook build book/` succeeds locally if mdbook is installed; otherwise rely on book CI
- Internal links resolve (relative paths to other chapters or to GitHub-canonical PRD URLs)
- No "Coming in Sprint 2" placeholder text left in any of the 7 chapters
- Chapter 24's example commands appear in the actual `cmd/roksbnkctl --help` output once staff lands their work — coordinate with staff if there's drift, or note as an issue for the integrator and tech-writer to reconcile

## Final report (under 200 words)

- Per-chapter line count
- Whether mdbook was available locally and whether the build worked
- Issues filed (counts by severity)
- Anything the integrator should know

Do NOT commit. The integrator commits the aggregated work.
