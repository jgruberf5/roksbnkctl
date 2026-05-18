You are the architect agent for Sprint 4 of the roksbnkctl project. Your scope is **book chapter authoring** for the 3 chapters that need to land this sprint, plus filling in chapter 17's `*Coming in Sprint 4.*` deep-dive sections that Sprint 3 left as placeholders.

Project location: `/mnt/d/project/roksbnkctl/`. The book is _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_, served at `https://jgruberf5.github.io/roksbnkctl/book/`.

## Read first

- `docs/prd/03-EXECUTION-BACKENDS.md` — your authoritative spec for chapters 17 (full), 18, and 19. The k8s + SSH sections are what Sprint 4 ships; chapter 19 is the in-cluster ops pod RBAC.
- `docs/prd/04-CREDENTIALS.md` — the cross-backend cred propagation rules (especially §"K8s" and §"SSH"). Chapter 17's per-backend deep-dive sections must be consistent with PRD 04's cred-propagation contracts.
- `docs/PLAN.md` Sprint 4 section, especially "Documentation deliverables" — confirms the 3 chapters land this sprint.
- `book/src/SUMMARY.md` — existing TOC; do not change.
- The existing chapter 17 (`book/src/17-execution-backends.md`) — Sprint 3 wrote the intro; you fill in the deep-dive sections (k8s + ssh per-backend internals) that Sprint 3 marked `*Coming in Sprint 4.*`.
- The existing chapter stubs at `book/src/18-choosing-a-backend.md` and `book/src/19-ops-pod.md` — replace stubs with real content.
- Sprint 3's chapters 14 (Credentials) + 17 intro for tone reference; chapter 14's redactor + Credentials struct sections are forward-referenced from chapter 17 deep-dives.
- `prompts/sprint3/architect.md` for prompt-structure reference.

## Coordinate with parallel agents

A staff-engineer agent is implementing PRD 03's k8s + ssh backends (`internal/exec/k8s.go`, `internal/exec/ssh.go`, `internal/cli/ops.go`, `internal/exec/k8s_install.yaml`), the iperf3 SCC fix in `internal/test/throughput.go`, the iperf3 + ibmcloud backend-selection wiring, doctor extensions, and Sprint 3 polish carry-overs (per-tool default map, `:dev` tag fix, legacy `ResolveAPIKey` migration, 126/127 failure-semantics split). A validator agent is adding argv-builder unit tests for k8s + ssh, kind-based integration tests in CI, a new `scripts/e2e-test-backends.sh` covering PRD 05 Phases K + L, k8s + ssh cred-leak audit tests, the cspell `SSC→SCC` fix, and CONTRIBUTING.md updates.

**Do not touch their files.** Your scope is `book/src/<chapter>.md` only.

## Tasks

For each chapter below, replace the stub content (or fill in the placeholders for chapter 17) with real prose. Aim for 200-400 lines per chapter (chapter 19 may be shorter — its scope is narrower). Use relative markdown links for in-book cross-references and GitHub-canonical URLs for PRD links (per Sprint 1 Issue 9 fix pattern).

### Chapter 17 (full) — `book/src/17-execution-backends.md` — "Execution backends: local, docker, k8s, ssh"

Sprint 3 wrote the intro. Sprint 4's job: fill in the per-backend deep-dive sections currently marked `*Coming in Sprint 4.*`.

The intro stays as-is — extend it with these sections (or rewrite the whole chapter if the structure doesn't admit clean extension):

- **§ Local backend** — Sprint 3 already covered this lightly. Expand: the `os/exec` shape, env propagation rules, working-directory honoring, signal handling, exit-code mapping (now including the 126/127 split landing this sprint per the polish carry-over).
- **§ Docker backend** — Sprint 3 covered the run-shape; expand on the cred-propagation specifics (env-by-reference, kubeconfig single-file mount, redactor wrapping), the `:dev` tag resolution behaviour after this sprint's fix, and the auto-remove + ctx-cancel-kill semantics. Cross-link to chapter 14's `Credentials.DockerArgs()` section.
- **§ K8s backend** — full deep-dive. The two patterns: long-lived ops pod (recommended for ibmcloud + ad-hoc; described further in chapter 19) vs one-shot Job (used for iperf3 client + future terraform). Pod + Job spec shape; projected Secret for cred propagation; log streaming via `client-go`'s `Pods().GetLogs(...).Stream()`; exit-code extraction from container status `terminated.exitCode`; the `ttlSecondsAfterFinished: 60` auto-cleanup; the `roksbnkctl-test` namespace for one-shot Jobs vs `roksbnkctl-ops` for the long-lived pod. Cross-link to chapter 19.
- **§ SSH backend** — full deep-dive. Builds on Sprint 1's `internal/remote/ssh.go` (chapter 16). Per-tool apt-bootstrap with the `--bootstrap` opt-in flag (PRD 03 recommendation) — reasoning for opt-in, what happens without it, what fails how. File materialization to `/tmp/roksbnkctl.<random>/<basename>` with `trap 'rm -rf …' EXIT` cleanup. Env propagation: SetEnv path (preferred, requires sshd `AcceptEnv`) and wrapper-script-with-trap fallback (the Sprint 1 validator Issue 4 carry-over) — when each path is taken, what the user sees if SetEnv silently drops vars. Bootstrap failure modes: missing sudo (clear remediation message), non-Ubuntu OS, network unreachable. Forward-link to chapter 16 for `--on jumphost` and to chapter 18 for "when to use SSH backend".

Each per-backend section should end with a short "**when to use it**" callout that previews chapter 18.

The `*Coming in Sprint 4.*` markers must be gone after this rewrite.

### Chapter 18 — `book/src/18-choosing-a-backend.md` — "Choosing a backend per tool"

Decision-tree chapter. Replace the stub with real content.

Sections:

- The four backends one-line each (forward-link to chapter 17's deep-dive)
- Per-tool default backend table: iperf3=`k8s`, ibmcloud=`local`, terraform=`local` (read `internal/cli/<tool>.go` and the `resolveBackendSpecWith` map landing this sprint to confirm — the staff agent is wiring `iperf3→k8s` as the explicit default this sprint per PRD 03 §"iperf3" and PLAN.md Sprint 4 row 6)
- Per-tool **supported-backends** matrix: iperf3 supports `k8s`/`local`/`ssh` (no docker — same network identity as local, no benefit); ibmcloud supports all four; terraform supports `local`/`docker` today, `k8s`/`ssh` deferred to Sprint 5+ per PRD 03 §"terraform" "State concerns"
- Decision tree:
  - GSLB DNS testing → `local` and `k8s` (multi-vantage; Sprint 5 lands the actual probe)
  - iperf3 throughput → `k8s` default; `local` only when measuring laptop-uplink-to-cluster
  - ibmcloud from a customer-firewalled office → `ssh:bastion`
  - Frozen toolchain version in CI → `docker` (per-tool image pinning)
- A short "**when not to use a backend**" subsection — common foot-guns. e.g., `--backend docker` for DNS probe is rejected by design (chapter 17 §SSH deep-dive equivalent); `--backend k8s` requires `roksbnkctl ops install` first (forward-link to chapter 19); `--backend ssh:host` without bootstrap fails clearly when target lacks the tool.
- Workspace config + `--backend` flag override interaction (covered in chapter 12; reference, don't duplicate)

This chapter should be the chapter people land on when they search "which backend should I use".

### Chapter 19 — `book/src/19-ops-pod.md` — "The in-cluster ops pod"

The detailed reference for `roksbnkctl ops install/show/uninstall` and what gets deployed in `roksbnkctl-ops` namespace.

Sections:

- What the ops pod is (long-lived in-cluster pod with bundled tools; the k8s backend's recommended cred-propagation surface for ad-hoc commands)
- `roksbnkctl ops install` — what it does step by step (creates namespace, ServiceAccount, ClusterRole, ClusterRoleBinding, Secret with the workspace's `IBMCLOUD_API_KEY` projected into the pod, then the Pod itself); idempotent — re-running updates the Secret if the cred rotated
- `roksbnkctl ops show` — what it surfaces (pod readiness, image version, last cred rotation, RBAC subject)
- `roksbnkctl ops uninstall` — full namespace + RBAC removal (cluster-scoped objects too); when to run (cluster decommission, cred rotation that's safer with a fresh Secret)
- The embedded RBAC manifests at `internal/exec/k8s_install.yaml` — what rules the ClusterRole grants (least-privilege: jobs in `roksbnkctl-test`, pods/exec in `roksbnkctl-ops`, no cluster-admin), why each rule is there. Cross-reference PRD 03 §"K8s" and PRD 04 §"K8s Secret".
- The cred propagation specifics: Secret name (`roksbnkctl-ibm-creds`), key in Secret (`IBMCLOUD_API_KEY`), how the pod consumes it (env-from-secretRef), what the redactor does to outputs.
- Rotation story: how to rotate the IBM API key without re-running `up` (re-run `roksbnkctl ops install` with the new key — Secret gets updated, pod env doesn't refresh until pod recreate, so `roksbnkctl ops install` should kubectl-rollout the pod after Secret update).
- Operability: where pod logs go, how to debug a stuck install, what happens on cluster API outage during install.

Don't duplicate chapter 17's k8s deep-dive — chapter 17 talks about the **interface mechanics** (Backend.Run dispatching to a long-lived ops pod or a one-shot Job); chapter 19 talks about the **pod itself** (RBAC, Secret, lifecycle).

## Style guidance

- Lower-case prose; sentence-case section headers
- Code blocks for any command; inline code for filenames and identifiers
- Cross-reference other chapters with relative links
- Short paragraphs; one idea per paragraph
- Examples should be runnable as written
- When citing PRDs, link as `[PRD 03](https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd/03-EXECUTION-BACKENDS.md)` — GitHub canonical URL avoids the published-book 404 issue Sprint 1 surfaced

## Issue tracking

`/mnt/d/project/roksbnkctl/issues/issue_sprint4_architect.md`:

```markdown
# Sprint 4 — architect issues

## Issue 1: short title
**Severity**: low | medium | high | blocker
**Status**: open | resolved
**Description**: ...
**Files affected**: ...
**Proposed fix**: ...
```

If clean, file with `*No issues filed.*`.

## Verification before reporting done

- All 3 chapter files have replaced their stubs with real content (chapter 17 has had its `*Coming in Sprint 4.*` markers replaced; chapters 18 + 19 are net-new prose)
- `mdbook build book/` succeeds locally if mdbook is installed; otherwise rely on book CI
- Internal links resolve
- No "Coming in Sprint 4" placeholder text left in chapter 17
- Chapter 17's k8s deep-dive's pod / Job spec details match what staff is implementing in `internal/exec/k8s.go` — coordinate with staff if there's drift, or note as an issue for the integrator
- Chapter 18's per-tool default-backend table matches the actual `resolveBackendSpecWith` map landing this sprint
- Chapter 19's RBAC description matches `internal/exec/k8s_install.yaml`'s rule set

## Final report (under 200 words)

- Per-chapter line count
- Whether mdbook was available locally
- Issues filed (counts by severity)
- Anything the integrator should know

Do NOT commit. The integrator commits the aggregated work.
