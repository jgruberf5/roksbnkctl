# Phased development & testing plan

Execution plan synthesizing the six PRDs in [`docs/prd/`](./prd/) into sequenced work, with development and testing interleaved per sprint. References the PRDs by number; read those for the *what*, this for the *when* and *how*.

## Goals & top-level milestones

| Milestone | Tag | Outcome |
|---|---|---|
| **M1** | `v0.7` | `--on jumphost` works; user can drive `roksbnkctl ibmcloud`/`exec`/`shell` over SSH against an auto-discovered jumphost. Book infra live with first 4 chapters drafted. |
| **M2** | `v0.8` | `kubectl` no longer required on host for the happy path; native `roksbnkctl k get/apply/logs/exec`. Book at ~10 chapters. |
| **M3** | `v0.9` | `--backend docker|k8s|ssh` works for ibmcloud, iperf3, terraform; DNS probe internalized + GSLB-aware. Book at ~22 chapters covering the full feature surface. |
| **M4** | `v1.0` | All E2E Phases A-H plus I-N + L-DNS pass on a clean dev host (no kubectl/oc/iperf3/dig installed); credential audit clean. **Web book published at `https://jgruberf5.github.io/roksbnkctl/book/`** — *Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl* — fully cross-linked, dogfooded, with diagrams. |

Estimated calendar time: **~14 weeks** (seven 2-week sprints) for a single focused engineer. Doubling that for "real-world with reviews, distractions, and integration debt" puts the M4 target around **7 months out**.

### About the book

The book — **_Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_** — is the canonical user-facing documentation surface, complementing the in-tree README/PRDs (which are repo-internal). It's built with [**mdBook**](https://rust-lang.github.io/mdBook/) (markdown source under `book/src/`, static-site output to `book/book/`, deployed via GitHub Actions to GitHub Pages). Key reasons for mdBook:

- Lightweight: just markdown + a tiny TOML config; no React build chain
- Linear-narrative book shape (sidebar TOC, prev/next, search) — fits a tutorial+reference hybrid
- Easy local preview: `mdbook serve` watches and rebuilds
- Battle-tested by Rust's own books, Kubernetes sub-projects, gitoxide, many others
- Themable later if F5 branding is wanted

Chapters land **incrementally per sprint** — each sprint's developer writes the chapter for what they just built, while the why is fresh. The final sprint is dedicated polish + diagrams + dogfooding + launch.

## Phase overview — sequencing decisions

```
┌───────────────────────────────────────────────────────────────────┐
│ Sprint 0 (week 0)        Foundations: CI matrix, dev shortcuts    │
│                          Book infra: mdBook setup + skeleton +    │
│                          GitHub Pages workflow                    │
├───────────────────────────────────────────────────────────────────┤
│ Sprint 1 (weeks 1-2)     PRD 01 — SSH client + --on flag          │
│                          Book chapters: Concepts, Install,        │
│                          Quick Start, Remote execution            │
│   ↓                                                               │
│ Sprint 2 (weeks 3-4)     PRD 02 — kubectl internalization         │
│                          Book chapters: Internal kubectl, Day-2   │
│   ↓                                                               │
│ Sprint 3 (weeks 5-6)     PRD 04 — cred abstraction (informs 3)    │
│                          PRD 03 — local + docker backends         │
│                          Book chapters: Credentials, Backends     │
│                          (intro), Workspace config                │
│   ↓                                                               │
│ Sprint 4 (weeks 7-8)     PRD 03 — k8s + ssh backends              │
│                          Tool migration: iperf3, ibmcloud         │
│                          Book chapters: K8s + SSH backends,       │
│                          Choosing a backend, Ops pod              │
│   ↓                                                               │
│ Sprint 5 (weeks 9-10)    PRD 03 — DNS probe (miekg/dns + GSLB)    │
│                          Tool migration: terraform (docker only)  │
│                          Book chapters: DNS testing for GSLB,     │
│                          Throughput, Connectivity                 │
│   ↓                                                               │
│ Sprint 6 (weeks 11-12)   PRD 05 — E2E Phases I-N + L-DNS          │
│                          Hardening, doctor refresh                │
│                          Book chapters: E2E test plan, Reference, │
│                          Troubleshooting, Contributing            │
│   ↓                                                               │
│ Sprint 7 (weeks 13-14)   Book launch: dogfood, polish, diagrams,  │
│                          cross-link, gh-pages publish, v1.0 cut   │
└───────────────────────────────────────────────────────────────────┘
```

**Dependency rationale**:
- SSH client (Sprint 1) blocks the SSH backend in Sprint 4
- Cred abstraction (Sprint 3, first half) shapes the `Backend` interface, so it must precede backend implementations
- kubectl internalization (Sprint 2) gives the K8s backend a reusable in-cluster client builder
- DNS probe (Sprint 5) reuses the K8s backend's Job pattern from Sprint 4
- E2E phases (Sprint 6) gate the v1.0 release

## Sprint 0 — foundations + book infra (week 0)

### Goal

Set up the developer workflow + CI matrix + book authoring pipeline so the next 14 weeks of changes can land safely *and* doc updates accompany every feature.

### Code deliverables

| Item | Detail |
|---|---|
| CI matrix expansion | GitHub Actions: `go test ./...` on Linux + macOS; `gofmt`, `go vet`, `staticcheck`. Stretch: Windows compile check. |
| Pre-commit hook | `gofmt`, `go vet`, `go test ./internal/...` (skip slow tests via `-short`) |
| Tool image build skeleton | `tools/docker/Makefile` + GitHub Actions workflow that *can* build images on tag — pushed only when tools/docker/* changes |
| Doctor v2 sketch | Refactor `roksbnkctl doctor` so it can grow per-backend checks without rewriting; introduce `Check{Name, Status, Detail}` struct |

### Documentation deliverables (book infrastructure)

| Item | Detail |
|---|---|
| `book/` directory | mdBook source tree at the repo root |
| `book/book.toml` | Book config: title = *Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl*, authors, language, `output.html.git-repository-url`, search enabled, syntax highlighting, sidebar style |
| `book/src/SUMMARY.md` | Full chapter outline (see "Book outline" section below) — each chapter file created as a stub with title + 2-3 line "coming in Sprint X" placeholder |
| `book/src/preface.md` | Introduction + "how to read this book" |
| `.github/workflows/book.yml` | On push to main: install mdbook, run `mdbook build book/`, deploy `book/book/` to `gh-pages` branch via `peaceiris/actions-gh-pages` |
| `Makefile` targets | `make book` (build), `make book-serve` (preview at localhost:3000), `make book-clean` |
| README link | Top-of-README badge linking to `https://jgruberf5.github.io/roksbnkctl/book/` |
| CONTRIBUTING.md | "How to add a chapter" section: edit `SUMMARY.md`, drop a markdown file in `book/src/`, link from a feature PR |

### Test deliverables

- Existing `go test ./...` baseline runs in CI (already green from the rename + e2e work)
- Existing `scripts/e2e-test.sh` documented in CONTRIBUTING.md as the long-running smoke test
- Book CI: `mdbook test book/` runs on every PR — fails on broken internal links, malformed code blocks
- Spell check via `cspell` or similar on `book/src/**/*.md` (warning, not gate)

### Gate to Sprint 1

- All existing tests still green; CI matrix runs on PRs; doctor refactor merged
- Book builds locally and via CI; first deploy lands at the GitHub Pages URL (even if every chapter is a "coming in Sprint X" stub)

### Risks

- CI matrix may surface platform-specific bugs (path handling, socket types) — budget half a day for surprises
- mdBook's GitHub Pages deploy needs `gh-pages` branch + Pages source set in repo settings — one-time admin task; document in CONTRIBUTING

### Book outline (the full SUMMARY.md target)

The chapter map for `book/src/SUMMARY.md`. Each chapter is a separate markdown file. Chapters land per the "Documentation deliverables" sections in Sprints 1-6 below.

```
PART I — CONCEPTS
  1. What is BIG-IP Next for Kubernetes (BNK)
  2. Why ROKS (Red Hat OpenShift on IBM Cloud)
  3. What roksbnkctl does (and doesn't do)

PART II — GETTING STARTED
  4. Installation
  5. Doctor: checking your environment
  6. Workspaces
  7. Quick start: from API key to deployed BNK

PART III — CLUSTER LIFECYCLE
  8. The cluster phase (cluster up/down)
  9. Registering an existing cluster
  10. Deploying BNK trials on top
  11. Tearing down

PART IV — CONFIGURATION
  12. Workspace config (config.yaml)
  13. Terraform variables (terraform.tfvars)
  14. Credentials and the resolver chain
  15. SSH targets

PART V — REMOTE EXECUTION
  16. The --on flag and SSH jumphosts
  17. Execution backends: local, docker, k8s, ssh
  18. Choosing a backend per tool
  19. The in-cluster ops pod

PART VI — TESTING
  20. Connectivity testing
  21. DNS testing for GSLB
  22. Throughput testing
  23. The E2E test plan

PART VII — OPERATIONS
  24. Day-2 ops: status, logs, k get/apply/exec
  25. COS supply chain management
  26. Troubleshooting

PART VIII — REFERENCE
  27. Command reference
  28. Configuration reference
  29. Terraform variable reference
  30. Glossary

PART IX — CONTRIBUTING
  31. Building from source
  32. Extending roksbnkctl
```

---

## Sprint 1 — SSH client + `--on` flag (PRD 01)

### Goal

Ship M1 (`v0.7`): users can run `roksbnkctl ibmcloud --on jumphost ks cluster ls` against an auto-discovered jumphost without installing anything new.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | `internal/remote/ssh.go` — `Client` struct: connect, `Run(ctx, argv, opts)`, `Shell(ctx)` | new |
| 2 | `internal/remote/keys.go` — file / agent / `tf-output:<name>` key sources | new |
| 3 | `internal/remote/hostkeys.go` — `~/.roksbnkctl/known_hosts` + TOFU prompt | new |
| 4 | `internal/config/workspace.go` — add `Targets map[string]TargetCfg` | edit |
| 5 | `internal/cli/root.go` — persistent `--on string` flag | edit |
| 6 | `internal/cli/cluster.go` — passthroughs (`kubectl`, `oc`, `ibmcloud`, `exec`, `shell`) dispatch via `remote.Client.Run` when `flagOn != ""` | edit |
| 7 | `internal/cli/targets.go` — new `roksbnkctl targets list/show/add/remove` | new |
| 8 | `internal/cli/lifecycle.go runUp` — auto-populate `targets.jumphost` post-apply from TF outputs | edit |

### Test deliverables

- **Unit tests** (`internal/remote/*_test.go`): mocked SSH server using `github.com/gliderlabs/ssh` — connect, run, exit-code, host-key TOFU, key-source resolution
- **Integration test** (`internal/remote/integration_test.go`, `// +build integration`): connects to an `sshd` container via `testcontainers-go`, runs `whoami`, asserts output. Run with `-tags=integration`.
- **E2E patch**: extend `scripts/e2e-test.sh` Phase B (post cluster up) with three steps: `roksbnkctl exec --on jumphost -- whoami`, `roksbnkctl targets list`, `roksbnkctl ibmcloud --on jumphost iam oauth-tokens`. Reuses the existing cluster.

### Documentation deliverables

- **Chapter 1: What is BIG-IP Next for Kubernetes (BNK)** — context-setting; what BNK does, why someone would deploy it
- **Chapter 2: Why ROKS** — IBM Cloud's managed OpenShift; what it gives you over self-managed
- **Chapter 3: What roksbnkctl does (and doesn't do)** — the tool's scope, the explicit non-goals (not a generic IBM Cloud CLI, not a Kubernetes CLI), the relationship to bundled HCL
- **Chapter 4: Installation** — single-binary install via curl, apt repo, or `go install`; OS support matrix
- **Chapter 7: Quick start** — the 3-command happy path (`init` → `up` → `test` → `down`) with sample output
- **Chapter 16: The --on flag and SSH jumphosts** — primary feature delivered this sprint; targets config, key sources, host key handling, auto-discovery from TF outputs

### Gate to Sprint 2

- M1 merged + tagged `v0.7`
- Unit + integration tests green
- E2E (run manually): jumphost steps pass on a real ROKS cluster
- Six chapters above are published (not stubs); book renders cleanly on GH Pages

### Risks

- TF output `jumphost_shared_key` is a sensitive value — confirm we can read it via terraform-exec's `Output()` without it being redacted to `<sensitive>`. **Mitigation**: spike this in week 1 day 1; if blocked, fall back to writing the key to `~/.roksbnkctl/<ws>/state/jumphost.pem` from a dedicated null_resource in the HCL.
- `golang.org/x/crypto/ssh` PTY handling on Windows is incomplete — restrict TTY mode to Linux/macOS for v0.7; document the limitation

---

## Sprint 2 — kubectl internalization (PRD 02)

### Goal

Ship M2 (`v0.8`): `roksbnkctl k get/apply/logs/exec/port-forward` works without `kubectl` on PATH (top-level aliases for `get` and `logs`; `apply` not aliased to avoid shadowing the lifecycle `roksbnkctl apply`).

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | `internal/k8s/client.go` extension — `BuildClientset(kubeconfig)`, `BuildDynamicClient`, `BuildOpenShiftClient`, in-cluster fallback | edit |
| 2 | `internal/k8s/get.go` — typed + dynamic resource fetcher | new |
| 3 | `internal/cli/k_get.go` — cobra wiring; `cli-runtime` `PrintFlags` for `-o yaml/json/wide/jsonpath` | new |
| 4 | `internal/k8s/apply.go` — server-side apply with kustomize base resolution | new |
| 5 | `internal/cli/k_apply.go` — cobra wiring | new |
| 6 | `internal/k8s/logs.go` extension — raw pod-name path | edit |
| 7 | `internal/k8s/exec.go` — SPDY executor wrapper | new |
| 8 | `internal/k8s/port_forward.go` — SPDY port-forwarder | new |
| 9 | `internal/k8s/describe.go` — delegates to `k8s.io/kubectl/pkg/describe` | new |
| 10 | `internal/cli/k_*.go` — wire `roksbnkctl k get/apply/describe/delete/exec/logs/port-forward` plus top-level aliases for `get` and `logs`; `apply` deliberately not aliased to avoid shadowing the lifecycle `apply` (terraform apply) | new |
| 11 | `internal/cli/doctor.go` — downgrade kubectl/oc from required to informational | edit |

### Test deliverables

- **Unit tests** with `k8s.io/client-go/kubernetes/fake` clientset for `internal/k8s/get,apply,delete,logs`
- **Golden-file tests** against a live cluster: `roksbnkctl k get nodes -o yaml` byte-compared to `kubectl get nodes -o yaml`, ignoring `managedFields/resourceVersion/creationTimestamp`. Run only with `-tags=live`.
- **E2E patch**: existing `scripts/e2e-test.sh` Phase D — replace `roksbnkctl kubectl get pods -n f5-bnk` (D3) with `roksbnkctl k get pods -n f5-bnk`. Add a new D-internal step that `mv kubectl kubectl.hidden`'s the binary, runs `roksbnkctl k get nodes`, restores.

### Documentation deliverables

- **Chapter 5: Doctor** — environment check; what the green/yellow/red status means; how to fix common failures
- **Chapter 6: Workspaces** — kubectl-style multi-environment isolation; new/use/list/delete; the parking-lot pattern from e2e
- **Chapter 8: The cluster phase** — `cluster up`/`down`; what's deployed (cluster + cert-manager + jumphost); the `state-cluster/` state dir
- **Chapter 9: Registering an existing cluster** — `cluster register <name>`; how COS instance name discovery works; when you'd use this vs `cluster up`
- **Chapter 10: Deploying BNK trials** — `roksbnkctl up`; what the 77 resources are
- **Chapter 11: Tearing down** — `down`; `cluster down`; what gets cleaned vs what stays
- **Chapter 24: Day-2 ops** — `roksbnkctl k get/apply/logs/exec/port-forward` (the new internalized verbs); kubectl/oc passthroughs as escape hatches

### Gate to Sprint 3

- M2 merged + tagged `v0.8`
- E2E with kubectl PATH-stripped passes on a live cluster
- Byte-equivalence test passes for `get -o yaml` on Node, Pod, Service, ConfigMap
- Seven chapters above published; book TOC reflects the new structure

### Risks

- `cli-runtime`'s API surface has churned across k8s versions; pin to a known-good (`v0.30.x` is the current stable) and avoid bleeding-edge features
- OpenShift CRDs (Phase 2.1) require `openshift/client-go` which has its own version dance — defer to Sprint 5 polish if not clean by sprint end
- `kubectl exec`-equivalent for users with `oc rsh` muscle memory: doc the rough mapping in README

---

## Sprint 3 — credentials + first backends (PRD 04 + PRD 03 partial)

### Goal

Land the cred abstraction (informs all backends) and ship `local` + `docker` backends for ibmcloud + iperf3.

### Week 1: cred abstraction (PRD 04)

| Order | Item | Files |
|---|---|---|
| 1 | `internal/exec/creds.go` — `Credentials` struct, per-backend serializers | new |
| 2 | `internal/cred/resolver.go` — single source of truth for "give me the API key" (env → keychain → config-b64 → prompt) | new (extracted from existing scattered logic) |
| 3 | `internal/exec/redact.go` — output stream wrapper that masks API keys | new |
| 4 | Unit tests for the resolver with table-driven cases (env-only, keychain-only, both, neither) | new |

### Week 2: backends (PRD 03 first half)

| Order | Item | Files |
|---|---|---|
| 5 | `internal/exec/Backend` interface + registry | new |
| 6 | `internal/exec/local.go` — refactor existing `os/exec` callsites through this | new (migration) |
| 7 | `internal/exec/docker.go` — uses `github.com/docker/docker/client`; respects all the cred-passing rules from PRD 04 | new |
| 8 | `tools/docker/ibmcloud/Dockerfile` + `tools/docker/iperf3/Dockerfile` | new |
| 9 | GitHub Actions workflow: build + push tools images on tag | new |
| 10 | Workspace config `exec:` block parsing | edit |
| 11 | `--backend` CLI flag at root | edit |

### Test deliverables

- **Unit**: cred resolver + redactor + local backend (with `os/exec` happy + sad path)
- **Integration**: docker backend against a local Docker daemon — `roksbnkctl ibmcloud --backend docker iam oauth-tokens` with a stub IBM API server (`net/http/httptest`)
- **Cred audit unit test**: assert that `os.Environ()` after a backend run does not contain `IBMCLOUD_API_KEY`; assert that container args don't contain key value
- **E2E patch**: add a Phase K-prelim to `e2e-test.sh` that exercises `--backend docker` for `ibmcloud iam oauth-tokens`

### Documentation deliverables

- **Chapter 12: Workspace config (config.yaml)** — full schema reference with annotated example; what every field does; defaults
- **Chapter 13: Terraform variables** — the `terraform.tfvars` surface, the `--var-file` layering rule, when to use `roksbnkctl tfvars` to bootstrap
- **Chapter 14: Credentials and the resolver chain** — how `IBMCLOUD_API_KEY` resolves (env → keychain → config-b64 → prompt); `kubeconfig` discovery; SSH key sources; what's safe to commit vs not (PRD 04 distilled for users)
- **Chapter 15: SSH targets** — companion to Chapter 16 (which already exists from Sprint 1); deeper on `tf-output:` key sources, agent integration, host-key TOFU
- **Chapter 17 (intro):** Execution backends — high-level: what the four backends are, why each exists; the `--backend` flag; per-tool defaults

### Gate to Sprint 4

- Cred audit test green: API key value never appears in any inspectable surface (logs, argv, container metadata)
- Docker backend produces output identical to local backend for `ibmcloud ks cluster ls`
- Doctor's `--backend docker` check accurate
- Five chapters published; book TOC has 18+ chapters live

### Risks

- IBM Cloud may not publish a maintained `ibmcloud-cli` Docker image; if so, build from upstream tarball — adds ~half-day
- Docker daemon socket permissions vary across distros; doctor handles this gracefully (no panic, just clear "docker daemon unreachable")

---

## Sprint 4 — k8s + SSH backends, tool migration (PRD 03 second half)

### Goal

Round out the four-backend matrix; migrate iperf3 (default `k8s`) and ibmcloud (selectable, all four backends) onto it.

### Week 1: k8s backend

| Order | Item | Files |
|---|---|---|
| 1 | `internal/exec/k8s.go` — Pod + Job templates, projected Secret for creds, log streaming | new |
| 2 | `internal/cli/ops.go` — `roksbnkctl ops install/show/uninstall` | new |
| 3 | `internal/exec/k8s_install.yaml` — embedded RBAC manifests | new |
| 4 | iperf3 SCC fix in `internal/test/throughput.go` — `securityContext` block correct for `restricted-v2` | edit |

### Week 2: ssh backend + iperf3/ibmcloud migration

| Order | Item | Files |
|---|---|---|
| 5 | `internal/exec/ssh.go` — wraps Sprint 1's `remote.Client`; adds file materialization, env propagation (SetEnv + wrapper fallback), Ubuntu apt-bootstrap | new |
| 6 | iperf3 backend selection: default `k8s`, supports `local`/`ssh` — wire in `cli/test.go test throughput` | edit |
| 7 | ibmcloud backend selection: default `local`, supports all four — wire in `cli/cluster.go ibmcloud passthrough` | edit |
| 8 | Doctor: per-backend availability checks (`--backend k8s/ssh`) | edit |

### Test deliverables

- **Unit**: backend-specific argv-builder tests (no IBM key in argv, kubeconfig path mounted correctly, etc.)
- **Integration**: k8s backend against `kind` cluster in CI — apply ops install, run a no-op probe, assert pod ran + cleaned up
- **E2E**: extend `scripts/e2e-test-backends.sh` (new file) with PRD 05 Phases K (docker), L (k8s) full coverage. Reuses cluster from baseline e2e Phase D.

### Documentation deliverables

- **Chapter 17 (full):** Execution backends — extends the Sprint 3 intro with the full per-backend deep-dive: local exec details, docker run shape + recommended args, the in-cluster pod orchestration, SSH backend with apt-bootstrap. Each backend gets a "when to use it" table.
- **Chapter 18: Choosing a backend per tool** — decision tree: GSLB DNS testing? local + k8s. iperf3 throughput? k8s default. ibmcloud from a customer-firewalled office? ssh. Frozen toolchain version in CI? docker.
- **Chapter 19: The in-cluster ops pod** — `roksbnkctl ops install/show/uninstall`; what gets deployed (namespace, SA, ClusterRole, RoleBinding, Secret); RBAC privileges granted; rotation/refresh story

### Gate to Sprint 5

- M3-prelim: `roksbnkctl test throughput --backend k8s` runs entirely in cluster, no host iperf3 required
- `roksbnkctl ibmcloud --backend ssh:jumphost ks cluster ls` works on fresh Ubuntu jumphost (auto-installs ibmcloud CLI)
- Phase K + Phase L from PRD 05 pass on a live cluster
- Three chapters published; book has all execution-backend material covered

### Risks

- **OpenShift SCC** for iperf3 pod: the `restricted-v2` SCC requires very specific securityContext — getting it wrong means the throughput test fails the same way it did during baseline e2e. Spike on Day 1 of the sprint with a manual `oc apply` to verify the manifest before automating.
- **SSH apt-bootstrap** sudo policies: jumphosts provisioned by the upstream HCL run as `root` so this is fine for e2e; users with non-root jumphosts will need NOPASSWD sudo for `apt-get`. Doc the failure mode clearly.
- **ibmcloud-cli upstream apt repo** GPG key handling — may require `gpg --dearmor` step on newer Ubuntu (deprecated `apt-key` warnings); test on 22.04 + 24.04

---

## Sprint 5 — DNS probe + terraform docker backend + polish

### Goal

Ship the GSLB-aware DNS probe (Phase 3 sub-feature) and finish the long-tail polish needed for a v0.9 release candidate.

### Week 1: DNS probe (miekg/dns)

| Order | Item | Files |
|---|---|---|
| 1 | Add `github.com/miekg/dns` dep | go.mod |
| 2 | `internal/test/dns.go` — replace existing `net.Resolver` impl with miekg-based `Probe` struct: `--server`, `--type`, `--iterations`, RTT capture | edit |
| 3 | `internal/cli/test.go` — extend `dns` subcommand with new flags + `--gslb-compare` multi-vantage mode | edit |
| 4 | `internal/exec/k8s.go` — add `dns-probe` Job mode that execs `roksbnkctl` itself in-cluster (no separate image) | edit |
| 5 | Workspace config: add `test.dns.resolvers` map and `test.dns.default_target` | edit |

### Week 2: terraform-via-docker + polish

| Order | Item | Files |
|---|---|---|
| 6 | terraform docker backend: bind-mount `~/.roksbnkctl/<ws>/state/`, run `hashicorp/terraform:<v>` image | edit |
| 7 | `--backend docker` for `roksbnkctl up`/`plan`/`apply`/`destroy` | edit |
| 8 | (defer k8s + ssh terraform backends to v1.x — state-handling is fiddly, not worth blocking v0.9 on) | doc |
| 9 | Doctor: DNS-probe-specific check (mostly a no-op since miekg is built-in); k8s ops-pod health for backend=k8s | edit |
| 10 | README + docs/ updates for new flags, backend selection, GSLB workflow examples | edit |

### Test deliverables

- **Unit**: miekg-based probe with mocked DNS server (`miekg/dns` ships its own server library — useful for testing); record-type variation, server selection, RTT extraction, error paths (NXDOMAIN, SERVFAIL, timeout)
- **Integration**: probe against `8.8.8.8` and a local stub server in parallel; assert RTT > 0, answers parsed
- **E2E**: write Phase L-DNS in `scripts/e2e-test-backends.sh` per PRD 05 — record-type variation, GSLB cross-vantage compare, latency stats, NXDOMAIN negative
- **Manual**: real GSLB validation against the F5 BIG-IP Next deployment from Phase D — confirm `gslb_divergence` is true when probing from local vs k8s

### Documentation deliverables

- **Chapter 20: Connectivity testing** — `roksbnkctl test connectivity`; the `extra_hosts` config; what a pass/fail looks like; insecure-TLS option
- **Chapter 21: DNS testing for GSLB** — flagship chapter; the GSLB problem statement, why per-vantage probing matters, `--server`/`--type`/`--iterations` flags, `--gslb-compare` workflow, JSON schema, sample F5 BIG-IP Next GSLB scenarios with expected divergence
- **Chapter 22: Throughput testing** — iperf3 internalized via the k8s backend; the LoadBalancer-vs-ClusterIP modes; when host iperf3 install is still useful (north-south from outside cluster)
- Update **Chapter 17** with terraform docker-backend section (added in this sprint's code work)

### Gate to Sprint 6

- M3 merged + tagged `v0.9`
- Phase L-DNS passes including the GSLB divergence detection
- terraform `--backend docker` runs a real `up` cycle end-to-end against `hashicorp/terraform:1.5.7` (or current pin)
- Three chapters published; testing section of book complete; total ~22 chapters live

### Risks

- `miekg/dns` API has minor breaking changes between major versions; pin to a stable release tag in go.mod
- GSLB divergence detection requires a target where local and k8s actually return different answers; if testing against `8.8.8.8` for `www.google.com` returns identical answers due to anycast, document a more illustrative target (e.g., a TF-deployed internal GSLB record)
- terraform state in a Docker bind-mount has UID/permission gotchas — Linux container runs as root by default; bind-mount-owned-by-user can have permission issues. Pre-create dirs with `chown` or use `--user $(id -u):$(id -g)` consistently

---

## Sprint 6 — E2E test plan build-out + reference docs

### Goal

Land all E2E phases passing on a clean dev host with no host install of kubectl/oc/iperf3/dig. Land the reference + troubleshooting + contributing chapters of the book. Sprint 7 cuts the v1.0 tag after dogfood + polish.

### Code / config deliverables

| Order | Item | Files |
|---|---|---|
| 1 | `scripts/e2e-test-backends.sh` — full Phases I-N + L-DNS driver (some pieces written in earlier sprints; this consolidates) | edit |
| 2 | `scripts/e2e-test-full.sh` — runs A-H + I-N + L-DNS against the same cluster, ~5 hour total | new |
| 3 | Phase M (cred audit) implementation — automated checks of `docker inspect`, `kubectl get events`, ssh tempfile cleanup | new |
| 4 | Phase N (mixed-mode lifecycle) wiring | new |
| 5 | Doctor refresh: green-by-default on a stock dev box (`terraform` only required) | edit |
| 6 | Migration notes from v0.6.x or earlier (in book + as a top-level MIGRATING.md) | new |

### Test deliverables (this sprint *is* the testing sprint)

- All 14 individual phase steps from PRD 05 pass on a fresh test run
- Combined runner script provides a "one button" full-coverage test for CI
- Cred-leak audit (Phase M) clean: API key never appears in any inspectable surface across all backends
- `scripts/e2e-test-full.sh` tagged in CI as a manual-trigger workflow (too long for every PR; run on release branch + on demand)

### Documentation deliverables

- **Chapter 23: The E2E test plan** — user-facing version of PRD 05 ("here's how the E2E suite is structured, here's how to run it locally, here's what each phase validates"); links to PRD 05 for design rationale
- **Chapter 25: COS supply chain management** — `roksbnkctl cos instance/bucket/object`; the BNK supply chain (FAR images, JWT licenses, schematics)
- **Chapter 26: Troubleshooting** — common failure modes from real deployments: terraform-exec retries, ROKS cluster propagation lag, kubeconfig fetch 404s, OpenShift SCC violations on test pods, cluster-down → workspace-delete current-workspace gotcha. Each entry: symptom → root cause → fix.
- **Chapter 27: Command reference** — exhaustive `--help` rendered into the book; auto-generated from cobra via `cobra-cli` or a small Go program
- **Chapter 28: Configuration reference** — every field of `config.yaml` with type, default, allowed values
- **Chapter 29: Terraform variable reference** — every variable in `terraform/variables.tf` with default + description (auto-generated from HCL)
- **Chapter 30: Glossary** — BNK, ROKS, FAR, FLO, CIS, GSLB, SCC, etc.
- **Chapter 31: Building from source** — Go version, cross-compile, the `tools/docker/` images, `mdbook serve` for docs
- **Chapter 32: Extending roksbnkctl** — adding a new backend, adding a new test suite, the PRD process

### Gate to Sprint 7

- All E2E phases pass on a clean test host
- All previous sprints' acceptance criteria still hold (no regressions)
- Doctor green-by-default on a stock dev box
- All 32 chapters drafted (some still rough — Sprint 7 polishes)

### Risks

- **E2E flakiness**: ROKS cluster apply takes 30-50 min; transient API errors during apply add another 5-15 min; throughput tests depend on outbound network. Mitigation: PRD 05 already designs each step to be re-runnable (`PHASE_FROM=`); add jitter+retry to the assertion phases that hit external APIs.
- **Cluster cost**: a full e2e run costs ~$5-10 of IBM cloud spend (cluster + LBs + COS). Document this in CONTRIBUTING.md so contributors don't get surprised.
- **Slow CI**: 5 hours is too long for a PR check. Solution: gate v1.0 release branch on full e2e; PR checks run only the unit + integration tiers.
- **Chapter 27/29 auto-generation**: cobra-to-markdown and HCL-to-markdown converters need writing. Budget half a day each; have manual fallback if generators are flaky.

---

## Sprint 7 — book launch + v1.0 cut (weeks 13-14)

### Goal

Ship M4 (`v1.0`): the binary AND the book published together as a coherent v1.0 release.

### Code / config deliverables

| Order | Item | Files |
|---|---|---|
| 1 | README rewrite for v1.0 — terraform-only prereq, link to the book as the canonical learning path | edit |
| 2 | `roksbnkctl --version` includes the book URL | edit |
| 3 | Release notes (CHANGELOG.md): v0.7 → v1.0 summary | new |
| 4 | GitHub release artifacts: signed binaries for Linux + macOS, checksums.txt, the published book PDF (mdbook can output PDF via mdbook-pdf or a print stylesheet) | new |
| 5 | `goreleaser.yml` finalized for v1.0 — multi-platform binaries, GitHub release, optional Homebrew formula stub | edit |

### Documentation deliverables (book launch)

| Order | Item | Detail |
|---|---|---|
| 1 | **Polish pass** on every chapter | consistent voice, working code examples (every `roksbnkctl ...` snippet test-run in a fresh workspace), TOC cross-links, no "coming in Sprint X" placeholders left |
| 2 | **Diagrams** | architecture diagram (cluster + BNK + ops pod + jumphost); execution-backend matrix diagram; lifecycle flow (init → up → test → down); GSLB cross-vantage diagram. Authored in Mermaid (renders in mdBook) so they're version-controlled. |
| 3 | **Foreword / preface** | what motivated the tool; who this book is for; how to read it (linear vs reference) |
| 4 | **Worked example walkthroughs** in each Part — concrete end-to-end scenarios users can copy-paste |
| 5 | **Internal cross-linking review** — every "see Chapter X" reference resolves; the Reference part backlinks to relevant Concepts chapters |
| 6 | **Search index** — verify mdBook search finds the right chapters for queries like "GSLB", "kubeconfig", "jumphost" |
| 7 | **Dogfooding loop** — at least one external user reads the book and runs the full quick-start workflow against their own IBM Cloud account; feedback integrated |
| 8 | **Launch announcement** prep — README + book preface point at the published URL; a `book/src/CHANGES.md` lists what landed for v1.0 |

### Gate to v1.0 release

Tag `v1.0` only when **all** of the following hold:

- All E2E phases (A-H + I-N + L-DNS) pass on a clean test host
- All previous sprints' acceptance criteria still hold (no regressions)
- Cred audit clean (Phase M)
- Doctor green-by-default on a stock dev box (terraform only required)
- Book published at `https://jgruberf5.github.io/roksbnkctl/book/`, all 32+ chapters complete, dogfooded by ≥1 external user, no "coming in Sprint X" placeholders, all code examples verified
- Release artifacts (binaries, checksums, optional PDF book) attached to the GitHub release
- README links to the book; book links back to the repo

### Risks

- **Dogfood feedback** may surface real gaps that take >1 sprint to address. Mitigation: scope an early Sprint 7 "preview" deploy to a friendly user (week 13) so feedback has time to land before the v1.0 tag (week 14).
- **PDF generation** via mdbook-pdf can be flaky on complex layouts. Mitigation: PDF is a "nice to have"; HTML book is the canonical surface. Skip PDF if it blocks v1.0.
- **mdBook themes / branding** — F5 may want the book themed with corporate styling. Default mdBook theme for v1.0; theming deferred to v1.1.

---

## Cross-sprint testing strategy

### The testing pyramid

```
                       ┌─────────────┐
                       │  E2E (live  │   ~5 hours; gates v1.0; manual trigger
                       │  IBM Cloud) │
                       ├─────────────┤
                       │ Integration │   ~5 minutes; testcontainers-go,
                       │   (kind +   │   stub IBM API; PR check (post-Sprint 3)
                       │  httptest)  │
                       ├─────────────┤
                       │   Unit      │   <30 seconds; every commit;
                       │ (table-     │   pre-commit hook + PR check
                       │  driven Go) │
                       └─────────────┘
```

### Per-sprint testing additions

| Sprint | Unit | Integration | E2E |
|---|---|---|---|
| 0 | existing | existing | existing |
| 1 | mocked SSH server | testcontainers-go sshd | extend Phase B |
| 2 | client-go fake | live cluster (golden) | replace D3 with native |
| 3 | cred resolver + redactor | local docker daemon | Phase K-prelim |
| 4 | backend argv builders | kind cluster | Phases K + L |
| 5 | miekg with stub server | DNS probe vs 8.8.8.8 | Phase L-DNS |
| 6 | new fixtures as needed | new audit checks | Phases I + J + M + N + assembly |
| 7 | (regression sweeps only) | (regression sweeps only) | full A-H + I-N + L-DNS for the v1.0 gate; dogfood scenarios |

### Per-sprint book chapters (cumulative)

| Sprint | Chapters added (cumulative count) |
|---|---|
| 0 | book skeleton (32 stubs); 0 published |
| 1 | 1, 2, 3, 4, 7, 16 → **6 published** |
| 2 | 5, 6, 8, 9, 10, 11, 24 → **13 published** |
| 3 | 12, 13, 14, 15, 17 (intro) → **18 published** |
| 4 | 17 (full), 18, 19 → **21 published** |
| 5 | 20, 21, 22 → **24 published** |
| 6 | 23, 25, 26, 27, 28, 29, 30, 31, 32 → **33 published** (Chapter 17 also revised) |
| 7 | (polish only — diagrams, cross-links, foreword) → **all chapters launch-ready** |

### Continuous gates

- Every commit: pre-commit (gofmt + vet + unit tests + staticcheck)
- Every PR: full unit + integration tests on Linux + macOS
- Release branch: nightly `e2e-test-full.sh` until green for 3 consecutive nights, then tag

## Risk register (consolidated)

| Risk | Sprint | Severity | Mitigation |
|---|---|---|---|
| TF output sensitivity blocks reading jumphost key | 1 | medium | spike day 1; fallback null_resource + file write |
| `cli-runtime` API churn | 2 | low | pin to k8s.io/cli-runtime@v0.30.x |
| OpenShift SCC for iperf3 pod | 4 | medium | manual `oc apply` spike before automation |
| `miekg/dns` API change | 5 | low | pin major version |
| terraform docker backend state perms | 5 | medium | use `--user` consistently; pre-create dirs |
| E2E flakiness from external network | 6 | medium | retry + jitter on external probes; clear "test infra unstable" vs "real failure" classification |
| ROKS cluster cost in CI | 6 | low | document in CONTRIBUTING; full e2e is manual-trigger only |
| Windows compatibility | all | low | set "Linux + macOS first" expectation; degraded TTY support documented |
| Book chapter drift behind code | 1-6 | medium | each feature PR must include the corresponding chapter update; PR template asks for it; book CI fails on broken xrefs |
| Auto-generated reference chapters (Ch 27, 29) flaky | 6 | low | manual fallback ready; auto-gen is a nice-to-have, not blocking |
| Dogfood feedback surfaces gap close to v1.0 cut | 7 | medium | early dogfood deploy in week 13; week 14 reserved for integrating findings; willing to slip v1.0 by 1-2 weeks rather than ship with known doc gaps |
| mdbook-pdf flaky on layouts | 7 | low | PDF is optional; HTML book is canonical |

## Definition of done — per release

### v0.7 (M1)

- Sprint 0 + 1 complete
- `--on jumphost` validated against a live cluster
- README documents `targets:` config block + `roksbnkctl targets` commands
- Book infrastructure live; 6 chapters published (Concepts intro + Install + Quick start + Remote execution)

### v0.8 (M2)

- Sprint 2 complete
- `roksbnkctl k get/apply/logs/exec/port-forward` covers BNK-relevant operations
- Doctor downgrades kubectl/oc to informational
- Byte-equivalence test green for representative resources
- Book at 13 chapters covering Concepts + Getting Started + Cluster Lifecycle + early Operations

### v0.9 (M3)

- Sprints 3-5 complete
- Four backends working for at least ibmcloud + iperf3
- DNS probe internalized; GSLB divergence detection works
- Cred audit (unit + integration tier) clean
- Book at 24 chapters covering everything user-facing through testing

### v1.0 (M4)

- Sprints 6 + 7 complete
- All E2E Phases A-H + I-N + L-DNS pass on a clean host
- README rewritten for terraform-only-prereq install
- Tagged release with binaries for Linux + macOS (Windows compile-only)
- At least one external user has done a full lifecycle dogfood
- **Book launched** at `https://jgruberf5.github.io/roksbnkctl/book/`:
  - All 32+ chapters complete and polished (no placeholders)
  - Mermaid diagrams in place (architecture, lifecycle, GSLB, backend matrix)
  - Every code example test-verified in a fresh workspace
  - Search works for canonical queries (GSLB, jumphost, kubeconfig, …)
  - Dogfood feedback integrated
  - Optional PDF artifact attached to the GitHub release

## Sprint 8 — cluster/trial phase split (PRD 06; post-v1.0)

### Goal

Ship `v1.1.0`: make the two-phase lifecycle the default for new workspaces, add `roksbnkctl bnk up/down` so trial-only teardowns are a first-class command, and convert the unscoped `up`/`down` into shape-aware composites that preserve v1.0.x behavior for legacy single-state workspaces.

Reference spike: `spike/bnk-phase-split` branch (commit `00181d0`) — proof-of-concept that the shape detector identifies the real `canada-roks` legacy state correctly. The branch is reference-only; the staff agent re-implements from PRD 06.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | `WorkspaceShape` enum, `DetectShape`, `tfstateHasResources`, `trialStateHasClusterModules` | `internal/config/tfstate.go` (new) |
| 2 | Remove duplicate `tfstateHasResources`; drop unused `encoding/json` import | `internal/config/workspace.go` (edit) |
| 3 | `bnk` cobra group; `bnk up` (auto-bootstrap cluster phase with confirm); `bnk down`; flag wiring matching `cluster up`/`down` | `internal/cli/bnk_phase.go` (new) |
| 4 | Refactor: rename `runUp` body → `runTrialUp`, `runDown` body → `runTrialDown`; new composite `runUp`/`runDown` keyed on `DetectShape` | `internal/cli/lifecycle.go` (edit) |
| 5 | `runClusterUp` refuses on `ShapeLegacySingle`; `runClusterDown` refuses on `LegacySingle`/`Split`/`Empty`; drop the v1.0.x warning-but-prompt copy | `internal/cli/cluster_phase.go` (edit) |

### Test deliverables

- **Unit tests for shape detection**: synthetic minimal tfstate fixtures (one per shape — `empty`, `cluster-only`, `split`, `legacy-single`) checked into `internal/config/testdata/`; `DetectShape` table-test covers all four plus the missing-file and malformed-json edge cases.
- **Unit tests for dispatch**: `internal/cli/bnk_phase_test.go` covers the bnk refusal matrix using a faked `WorkspaceStateDir` (set `ROKSBNKCTL_HOME` to a temp dir; populate the tfstate fixtures by shape).
- **Live verification (manual, sprint integration)**: against the existing `canada-roks` legacy workspace — `bnk down` and `cluster down` refuse with legacy-single-state errors; `down` still works monolithically (don't actually destroy — verify it gets to the confirm prompt). Against a fresh sandbox workspace — full `cluster up` → `bnk up` → `bnk down` → `bnk up` → `cluster down` cycle.
- **E2E patch**: extend `scripts/e2e-test.sh` (or a v1.1-specific subset) with a new Phase that runs the `cluster up` → `bnk up` → `bnk down` → `cluster down` cycle and asserts cluster identity persistence via `cluster-outputs.json` across the trial down/up boundary.

### Documentation deliverables

- **Chapter 8 ("The cluster phase")** — reframe from "opt-in two-phase mode" to "the default for new workspaces"; cross-link to the new `bnk` chapter material.
- **Chapter 10 ("Deploying BNK trials")** — add a `roksbnkctl bnk up`/`bnk down` section with the bootstrap-prompt sample output, the dispatch table from PRD 06 §"Dispatch table" (user-facing simplification), and worked examples for the four shapes.
- **Chapter 11 ("Tearing down")** — add a phase-aware decision matrix: "I want to keep the cluster → `bnk down`; I want everything gone → `down`; I want only the cluster → `cluster down` (after `bnk down`)."
- **CHANGELOG `v1.1.0`** section under `## Unreleased` → renamed to `## v1.1.0 — <date>` at tag time. Added subsection covers `bnk` group, composite up/down, shape detection, refusal logic.

### Gate to `v1.1.0` tag

- All four agents' issue files at `Status: resolved` or `accepted`.
- `go build/test/vet/gofmt` green.
- Live verification (canada-roks refusals + at least one full sandbox cycle) documented in the integration commit message or `resolved_sprint8_*.md`.
- Chapter 8/10/11 edits render cleanly in `mdbook build`; cross-links resolve.
- `roksbnkctl --help` lists `bnk` alongside `cluster`.
- CHANGELOG `v1.1.0` entry final.

### Risks

- **Double-confirm UX in `bnk up` on empty workspace** — bootstrap prompt + apply prompt for one user command. Mitigation: `--auto` threads through; document the two-prompt shape in chapter 10.
- **Docker backend composition gap** — composite `up` on empty/split workspaces against a docker-backend workspace would run `cluster up` locally then trial in docker. Mitigation: the composite explicitly disables itself on non-local backends for empty/split paths in this sprint; full docker-mode composition is a follow-up PRD. Document the limitation in chapter 17 (Execution backends).
- **No automated migration for legacy single-state** — refusal messages reference a `roksbnkctl migrate` that doesn't exist. Mitigation: legacy users have the working `up`/`down` flow and aren't blocked; ship migrate when a real user asks. Document the migration story (and its absence) in chapter 11.

### Carry-overs from prior sprints

None expected — v1.0 closed cleanly with the Sprint 7 integration. Sprint 8 starts a new cycle against `main`.

---

## Sprint 9 — PRD 04 cred-passing closure + CI polish (post-v1.1)

### Goal

Ship `v1.2.0`: close out the two PRD 04 deferred items that turned up as integration-test gaps during the v1.1.x cycle, plus the smaller CI / Makefile polish that prevents the v1.1.0 → v1.1.1 → v1.1.2 cascade from repeating.

The PRD 04 items are the headline work — they unblock two `t.Skip`'d integration tests landed on `776fe56` and close §"Open questions" items in PRD 04 that have been open since the v0.9 cycle.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | **Cred tmpfile-bind-mount pattern** for docker backend — write `IBMCLOUD_API_KEY` to a per-run `0600` tempfile, bind-mount read-only at `/run/secrets/ibmcloud_api_key`, set `IBMCLOUD_API_KEY_FILE=/run/secrets/ibmcloud_api_key` and a small `entrypoint-shim` (or inline `sh -c export IBMCLOUD_API_KEY=$(cat …) && exec …` wrap) so the existing dockerImageBinary["ibmcloud"] login wrap sees the key. Closes PRD 04 §"Open questions" §"M2 cred audit"; unblocks `TestIntegration_DockerBackend_NoLeakInInspect`. | `internal/exec/docker.go` (edit), `internal/exec/docker_integration_test.go` (remove `t.Skip`) |
| 2 | **K8s trusted-profile auto-provisioning** path for the ops pod (PRD 04 §"Implementation tasks" task 8 + §"Open questions" first item) — when the resolved IBM Cloud API key has IAM perms to create a trusted profile, `roksbnkctl ops install` provisions `roksbnkctl-ops` linked to the ops pod's SA + projected SA token, and the ops pod assumes the profile at runtime so the static API key never lands in the Secret. Fall back to the v1.0.x static-key Secret when perms don't allow. New flag: `--trusted-profile=auto\|on\|off` (default `auto`). | `internal/exec/k8s.go` (edit), `internal/cli/ops.go` (edit), `internal/ibm/trusted_profile.go` (new) |
| 3 | **Job pod `RunAsUser` strategy** (option 1 from `k8s_integration_test.go:101-119` TODO): switch the JobMode echo smoke test from `busybox:1.36` to `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:<tag>` (already runs as uid 1000). Keeps `runAsJob`'s strict `RunAsNonRoot: true` SecurityContext intact for all callers. Unblocks `TestIntegration_K8sBackend_JobMode_Echo`. | `internal/exec/k8s_integration_test.go` (edit; remove `t.Skip`) |
| 4 | **`TESTCONTAINERS_RYUK_DISABLED=true`** in CI integration job env — kills the docker-hub `testcontainers/ryuk` pull that produced the intermittent "too many requests" flake on `TestIntegration_Connect_Whoami`. Ephemeral runners don't need the reaper. | `.github/workflows/ci.yml` (edit) |
| 5 | **`Makefile` pre-tag checklist** additions to `release` target — run `staticcheck ./...` and `go build -tags integration ./...` as part of the local gate so the next cut catches the same shape of gap that produced v1.1.0 → v1.1.1 → v1.1.2. | `Makefile` (edit) |

### Test deliverables

- Skip-removal counts as the v1.2.0 acceptance: `go test -tags integration ./internal/exec/...` green for both `TestIntegration_DockerBackend_NoLeakInInspect` and `TestIntegration_K8sBackend_JobMode_Echo` against a kind cluster + local docker daemon.
- **Live-verify** the trusted-profile path against a real IBM Cloud workspace: `roksbnkctl ops install --trusted-profile=auto` provisions the profile, the ops pod assumes it, `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` succeeds without a static-key Secret. Sandbox-permitting; document the run in the integration commit.
- **No regression** on the static-key fallback: `roksbnkctl ops install --trusted-profile=off` produces the v1.0.x-shaped Secret + works as today.

### Documentation deliverables

- **PRD 04** §"Open questions" items closed → moved to a new §"Resolved in Sprint 9" subsection (mirrors PRD 03's §"Resolved in Sprint 4" pattern). Document the tmpfile-bind-mount design and the trusted-profile flow.
- **Chapter 14 (Credentials and the resolver chain)** — short section on the tmpfile pattern (one paragraph; readers don't need to know the docker plumbing details, just that `docker inspect` no longer leaks the key) and the `--trusted-profile=auto\|on\|off` flag.
- **Chapter 19 (The in-cluster ops pod)** — `roksbnkctl ops install --trusted-profile=auto` flow + how to verify the profile is in use (`oc get serviceaccount roksbnkctl-ops -o yaml` showing the trusted-profile annotation).
- **CHANGELOG `v1.2.0`** entry under `## Unreleased (v1.x)`.

### Gate to `v1.2.0` tag

- All four agents' issue files at `Status: resolved` or `accepted`.
- **Whole-tree** `go build/test/vet/gofmt/staticcheck` green + `go build -tags integration ./...` green (this is the new pre-tag gate item from Code deliverable 5).
- Both previously-skipped integration tests pass under `-tags integration` against a real kind + docker setup; the skip markers are removed (not left in place).
- `mdbook build book/` clean; chapter 14 + 19 cross-links resolve.
- CHANGELOG `v1.2.0` entry final.

### Risks

- **Trusted-profile provisioning** needs IAM `iam-identity` permissions on the caller's API key. The auto path must detect missing perms and fall back cleanly — verified via a sandbox run with a deliberately-scoped key. Mitigation: the `--trusted-profile=auto` semantics include the fallback by definition; staff verifies the failure-mode against a real IAM-restricted key.
- **Tmpfile lifetime** — the tempfile must outlive every container that needs it (long-running ops pods, terraform docker runs that can take 20+ minutes) but get cleaned up on backend exit. Pattern: `t.TempDir`-equivalent at backend-init time, registered with `runtime.SetFinalizer` or the existing context-cancel cleanup goroutine. Validator regression-checks that no `/tmp/roksbnkctl.*` files survive a normal `roksbnkctl --backend docker` invocation.
- **Trusted-profile name collisions** — multiple workspaces against the same IBM Cloud account would race for `roksbnkctl-ops`. Either namespace by workspace (`roksbnkctl-ops-<workspace>`) or reuse the same profile across workspaces. PRD 04 update should document the chosen approach.

### Carry-overs from prior sprints

The two `t.Skip` markers on `776fe56` (`TestIntegration_DockerBackend_NoLeakInInspect` + `TestIntegration_K8sBackend_JobMode_Echo`) are the explicit Sprint 9 inputs. Both tests' TODO comments name the design choices Sprint 9 closes.

---

## What's deliberately deferred to post-v1.0

These came up during the PRDs but aren't blocking v1.0:

### Code

- terraform `--backend k8s` and `--backend ssh` (state-handling work — v1.1)
- OpenShift CRDs in `roksbnkctl k get` (`Project`, `Route`, etc. — v1.1 — tracked in PRD 02 § Phase 2.1)
- ~~IAM trusted-profile auto-provisioning~~ — scheduled for Sprint 9 / `v1.2.0`
- RHEL/CentOS/Alpine SSH apt-bootstrap (v1.x — PRD 03 explicitly out of scope)
- Windows full TTY support (v2 — needs ssh-agent named-pipe protocol)
- Multi-hop SSH ProxyJump (v1.1 — PRD 01 deferred)
- Long-running ops pod with kubeconfig refresh on token rotation (v1.1 — PRD 04 open question)
- Bash completion for `roksbnkctl k <verb> <resource-name>` with live API lookups (v1.1)

### Book

- F5 corporate theming (v1.1 if branding requested)
- Translated editions (v2)
- Video walkthroughs / screen recordings embedded in chapters (v1.1)
- Per-chapter "try it now" interactive sandbox (v2)
- Versioned book — keeping a `v0.9/`, `v1.0/`, `v1.1/` URL path so older releases' docs stay accessible (v1.1; book is single-version for v1.0)
