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

## Sprint 10 — PRD 04 in-pod closure + PRD 06 status integration + CI hardening (post-v1.2)

### Goal

Ship `v1.3.0`: close PRD 04's runtime-cred-flow side (the in-pod `ibmcloud login` wrap that Sprint 9 deferred), close PRD 06's `status` integration (the new requirement added post-Sprint-9 — `roksbnkctl status` shows per-phase deployment instead of the v1.0.x single "Last apply" line), tighten the local pre-tag gate against the v1.2.x cascade's remaining gap (integration-test execution vs compile-only), and fold the six tech-writer polish issues deferred from Sprint 9.

Headline closure: `roksbnkctl ops install --trusted-profile=auto` followed by `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` returns a fresh IAM token end-to-end — the v1.2.0 partial-closure admonition in chapter 19 comes out.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | **In-pod `ibmcloud login` wrap closure** ([PRD 04 §"Resolved in Sprint 9" carry-over](docs/prd/04-CREDENTIALS.md), staff Issue 2 from Sprint 9) — `runOnOpsPod`'s ibmcloud login wrap switches on whether the pod's SA carries `iam.cloud.ibm.com/trusted-profile`: trusted-profile-annotated pods do `ibmcloud login -a https://cloud.ibm.com --cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID" -r "${IBMCLOUD_REGION:-us-south}" --quiet` (the `--cr-token @<path>` form reads a projected SA token mounted at the cited path; IBM IAM validates that JWT against the trusted profile's `ROKS_SA` claim link); static-key pods continue the v1.0.x `--apikey "$IBMCLOUD_API_KEY"` path. `IAM_PROFILE_ID` injected into the pod spec at install time when the trusted profile is provisioned, alongside a projected SA-token volume (audience `iam`) on the pod spec. | `internal/exec/k8s.go` (edit), `internal/exec/k8s_install.yaml` (edit — pod spec env + projected-token volume injection), `internal/cli/ops.go` (edit — manifest renderer passes IAM_PROFILE_ID through) |
| 2 | **`roksbnkctl status` per-phase deployment** ([PRD 06 §"`status` command integration"](docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md#status-command-integration-sprint-10-scope-addition)) — `runStatus` consumes `config.DetectShape` + each phase's `terraform.tfstate` mtime; emits per-phase `Cluster phase:` / `BNK trial:` deployment lines for non-`LegacySingle` shapes; preserves the v1.0.x `Last apply` line verbatim for `ShapeLegacySingle` (script-compat). | `internal/cli/inspect.go` (edit), `internal/cli/inspect_test.go` (new or extend) |
| 3 | **Local pre-tag gate covers integration-test *execution*, not just compilation** — the v1.2.x cascade (v1.2.0 → v1.2.1 → 76af28d) traced to `make release` running `go build -tags integration ./...` (compile check) but not `go test -tags integration` (which requires a kind cluster + docker daemon). Two options for staff to pick: (a) `make integration-test` target users run when they have kind available, with `make release` adding a strong-worded `command -v kind` check + an "are you sure?" prompt before tagging if kind isn't reachable; (b) full kind-bringup in `make release` (heavy; might be too slow for routine local tag-cuts). Validator picks the option that fits the project's tag-cut cadence. | `Makefile` (edit), maybe `scripts/integration-test.sh` (new) |
| 4 | **Chapter polish (Sprint 9 deferred)** — five low/medium tech-writer issues from Sprint 9. Specifically: chapter 19 `ops show` shape (Issue 4); chapter 19 `<workspace>` vs `sandbox-roks` placeholder consistency (Issue 13); chapter 19 §"Credential propagation" v1.2 callout placement (Issue 9); chapter 14 "warning" vs "warning block" wording (Issue 7); chapter 14 §"What's new in v1.2" section position (Issue 8). | `book/src/14-credentials-resolver.md` (edit), `book/src/19-in-cluster-ops-pod.md` (edit) |

### Test deliverables

- **Live trusted-profile end-to-end** (sandbox-permitting): `cluster up` → `ops install --trusted-profile=auto` → `bnk up` → `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` returns a fresh IAM token (no `missing API key`). Validates that the in-pod login-wrap closure actually works end-to-end against a real IBM Cloud account.
- **`status` integration**: unit test against the four-shape `internal/config/testdata/` fixtures from Sprint 8 — assert each shape produces the expected lines.
- **Re-verify the `TestIntegration_K8sBackend_JobMode_Echo`** banner-assertion fix on `76af28d` (no t.Skip back; runs clean against the rebuilt tools-ibmcloud image with `USER 1000` + `HOME=/home/runner`).

### Documentation deliverables

- **Chapter 19 partial-closure admonition removal** — the §"Trusted-profile flow (v1.2+)" callout at the top now reads as historical context. The v1.3.0 closure is complete; users running `--trusted-profile=auto` get the trusted-profile path end-to-end (provisioning + runtime cred flow). Cross-link to a brief CHANGELOG note about the v1.2.x partial-closure → v1.3.0 full-closure transition.
- **Chapter 19 §"Verifying the profile is in use"** — un-guard the `roksbnkctl --backend k8s ibmcloud iam oauth-tokens` smoke test. The Sprint 9 `> Heads up — Sprint 10 carry-over` admonition comes out; the "fresh OAuth token returns" sample is now the v1.3.0 reality.
- **Chapter 24 (Day-2 ops)** — new section on `roksbnkctl status`'s per-shape output, with samples for each of the four shapes per PRD 06 §"`status` command integration".
- **CHANGELOG `v1.3.0`** entry under `## Unreleased (v1.x)`: `### Added` (status per-phase deployment), `### Changed` (in-pod login wrap is now trusted-profile-aware; status output replaces single `Last apply` for non-Legacy shapes), `### Fixed` (the five Sprint-9-deferred chapter polish issues), `### Deferred` (remove the now-closed in-pod login-wrap bullet from the post-v1.2.0 deferred list).

### Gate to `v1.3.0` tag

- All four agents' issue files at `Status: resolved` or `accepted`.
- Whole-tree `go build/test/vet/gofmt/staticcheck/-tags integration build` green; **plus `-tags integration test`** if the staff's choice of code-deliverable-3 option (a) bundles `make integration-test` into the pre-tag gate, or with the kind-availability check passing if option (b).
- Live trusted-profile end-to-end smoke test recorded in the integration commit (or `resolved_sprint10_validator.md`).
- Chapter 19 admonition + smoke-test guard removed; chapter 24 documents `status` per-shape.
- CHANGELOG `v1.3.0` entry final.

### Risks

- **In-pod login wrap closure** may surface IBM IAM-side issues that unit tests don't catch — most likely: the cluster's OIDC issuer URL propagation delay (mentioned in PRD 04 §"Resolved in Sprint 9"). The pod's first `ibmcloud login --cr-token @/var/run/secrets/tokens/token --profile "$IAM_PROFILE_ID"` may fail with `failed to assume trusted profile` for 30-60s after `ops install` returns. Mitigation: staff's implementation adds a brief in-wrap retry (3 attempts, 20s apart) for the trusted-profile auth path specifically; validator's live verification documents whether the retry is sufficient.
- **`status` output format change** breaks scripts that parsed the v1.0.x single "Last apply" line. Mitigation: ShapeLegacySingle preserves the line; ShapeEmpty/ClusterOnly/Split replace it with the new per-phase lines. CHANGELOG `### Changed` explicitly calls out the script-compat behaviour. Anyone on a non-legacy workspace running such a script was already broken by Sprint 8's phase split (the script's `Last apply` would have been the trial-only mtime, not the cluster's) — the v1.3.0 change makes this visible rather than silently misleading.
- **`make integration-test` target** depends on each contributor having kind + docker available locally. Mitigation: keep `make release` working without the integration-test execution; surface a clear warning when kind isn't found; document the install path in CONTRIBUTING.md.

### Carry-overs from prior sprints

- **Sprint 9 staff Issue 2** (in-pod login wrap) — explicit Sprint 10 deliverable 1.
- **Sprint 9 tech-writer Issues 4, 7, 8, 9, 13** — explicit Sprint 10 deliverable 4.
- **Sprint 9 validator Issue 4** (live trusted-profile sandbox verify) — Sprint 10 validator picks this up as part of test deliverable 1.
- **PRD 06 `status` integration** (added post-Sprint-9 per [4e5f103](https://github.com/jgruberf5/roksbnkctl/commit/4e5f103)) — explicit Sprint 10 deliverable 2.

---

## Sprint 11 — PRD 07 deployed-tfvars snapshot (post-v1.3)

### Goal

Ship `v1.4.0`: land [PRD 07](docs/prd/07-DEPLOYED-TFVARS.md) — after every successful `terraform apply` for a workspace phase, write a canonical-HCL `terraform.applied.tfvars` capturing the effective var-file inputs that produced the current state. Single small PRD, single sprint, no carry-over from prior sprints.

### Drivers / why now

Today's apply chain layers tfvars from three sources (`config.yaml`-derived, `terraform.tfvars.user`, cluster-phase override) but persists nothing — only `terraform.tfstate` survives, and it captures derived values rather than the inputs that produced them. Users who lose or regenerate `config.yaml`, want to re-create the workspace on a teammate's host, need to audit "what did I deploy 90 days ago?", or have a corrupted state needing `terraform import` against the same inputs have no on-disk record of the effective vars. See [PRD 07 §"Why"](docs/prd/07-DEPLOYED-TFVARS.md#why) for the full rationale. The fix is small (~150 LOC + tests) and unlocks DR, audit, and team-handoff workflows that previously required out-of-band reconstruction.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | **`WriteAppliedTFVars(workspace, phase, sources []string) error`** — new helper that reads each source tfvars file in order, applies the `ibmcloud_api_key` redaction, and emits a source-attributed canonical-HCL snapshot to the phase's state dir at mode `0600`. Idempotent (same inputs → byte-identical output). Variables sorted alphabetically within each section. Returns error on write failure (callers log-and-continue per PRD 07 anti-pattern 4). | `internal/config/applied_tfvars.go` (new) |
| 2 | **`Workspace.Apply` hook** — `internal/tf/terraform.go::Workspace.Apply` calls `WriteAppliedTFVars` after the underlying `tf.Apply` returns nil, passing the same `varFiles` slice it gave to `terraform apply`. Failure mode: log a `warn` to stderr (`could not write terraform.applied.tfvars: <err>`) and continue. A failed apply produces no snapshot; the prior successful apply's file remains in place. | `internal/tf/terraform.go` (edit) |
| 3 | **Unit tests** — fixture-driven tests against the four-shape `internal/config/testdata/` workspaces from Sprint 8 (Empty / ClusterOnly / Split / LegacySingle), asserting: file written at the expected path with mode `0600`; source-attribution comments present; `ibmcloud_api_key` (and only that variable) emitted as `<redacted>`; alphabetic variable order within each section; re-running with identical inputs produces a byte-identical file; destroy flow does NOT modify the prior snapshot. Legacy-shape test asserts the `phase=legacy-single` header marker. | `internal/config/applied_tfvars_test.go` (new) |

### Test deliverables

- **Staff's four-shape unit-test sweep** covering the assertion list in code deliverable 3.
- **Validator's live verify** against a sandbox `roksbnkctl cluster up` → `bnk up`: confirm both phase files land at the documented paths after each apply, mode `0600`, redaction correct, header timestamp parses as RFC3339. Verifies the architect-surface chapter 6 sample matches staff's actual output byte-for-byte (any drift gets fixed on the architect side per PRD 07 / chapter 6 cross-link consistency task).
- **Tech-writer drift sweep** at end of sprint: regenerate the chapter 6 worked example from a live apply output, confirm no stale fields.

### Risks

- **Goroutine ordering in `Workspace.Apply`** — the hook fires after `tf.Apply` returns. If a future refactor introduces concurrent post-apply work that touches the same state dir, the snapshot write could race. Mitigation: the current `Apply` is straight-line synchronous; PRD 07 names this risk so future authors don't accidentally introduce a race.
- **Redaction list incompleteness** — only `ibmcloud_api_key` is redacted today, which is correct for the current variable surface. If a future cycle adds a new credential-grade variable (e.g., a registry pull-secret token), the redaction list extends in code — one-line addition. Mitigated by `0600` mode and the per-user workspace dir; the snapshot doesn't world-leak even if a new sensitive var slips in temporarily.
- **`ShapeLegacySingle` mis-attribution** — the legacy shape merges cluster and trial var-files into one snapshot. The source-attribution comments (`# === from config.yaml ===`, etc.) still cleanly separate the inputs, but readers expecting per-phase split need to see the `phase=legacy-single` header marker to know they're on a legacy workspace. Mitigated by the unit-test assertion on the header marker.

### Gate to `v1.4.0` tag

- All four agents' issue files at `Status: resolved`, `wontfix`, or `accepted`.
- Whole-tree `go build / test / vet / gofmt / staticcheck / -tags integration build/test` green (the v1.3.0 pre-tag gate from Sprint 10 code deliverable 3 carries forward unchanged).
- Live snapshot verify recorded in the integration commit or `resolved_sprint11_validator.md`: after `cluster up`, `~/.roksbnkctl/<workspace>/state-cluster/terraform.applied.tfvars` exists at mode `0600` with the expected source-attribution comments and `<redacted>` line.
- Chapter 6 §"`terraform.applied.tfvars` — what's deployed right now" final; CHANGELOG `v1.4.0` entry final; `mdbook build book/` clean; cross-links to PRD 07 + PRD 04 resolve.

### Carry-overs from prior sprints

None. Sprint 11 is a single-PRD cycle. Prior-sprint deferred items (chapter 14 §"What's new in v1.2" position, chapter 19 §"5. Create the Pod" `env:` block) remain deferred — no movement this sprint.

---

## Sprint 12 — relative-path resolution fixes (patch cycle, post-v1.4.0)

### Theme

Focused patch release (`v1.4.1`) closing **two sibling relative-path-resolution bugs** surfaced post-v1.4.0, both instances of the same shell-CWD-vs-state-dir trap: the headline `--var-file` fix and the analogous `--tf-source` local-path fix. No new PRDs; the design surfaces are `issues/issue_sprint12_staff.md` Issue 1 §"Root cause" + §"Proposed fix" (var-file) and `issues/issue_sprint12_validator.md` Issue 5 (tf-source).

### Scope expansion — Issue 5 pulled forward from Sprint 13

The cycle opened as a strict single-bug patch (the `--var-file` fix). The validator's analogous-gotcha sweep surfaced [`issues/issue_sprint12_validator.md` Issue 5](../issues/issue_sprint12_validator.md): a relative `--tf-source=./...` local path hits the *same* class of bug — it passes the `init`-time existence check (shell CWD) but is persisted relative into `config.yaml` and detonates on a *later* `up` / `plan` / `apply` run (terraform's CWD = per-phase state dir). It was originally filed as a Sprint 13 follow-up; the integrator decided to pull it into Sprint 12 so `v1.4.1` closes both siblings of the trap together rather than shipping a half-fix and revisiting the same code path one patch cycle later. Still patch-scope — a second small normalization mirroring the var-file helper, not a feature.

### Drivers / why now

- **`--var-file`** — user reported `Failed to read variables file. Given variables file ./terraform.tfvars does not exist.` on `roksbnkctl up --var-file=./terraform.tfvars --auto` invoked from a directory containing the named file — the exact flow surfaced as the out-of-band action in [validator Sprint 11 Issue 2](../issues/issue_sprint11_validator.md). Root cause: `flagVarFiles` values flow verbatim to a terraform invocation whose CWD is the per-phase state dir (`~/.roksbnkctl/<workspace>/state[-cluster]/`), not the shell PWD.
- **`--tf-source`** — a relative local `--tf-source=./...` is persisted verbatim into `config.yaml` at `init` and later handed to terraform whose CWD is the per-phase state dir; the source directory then can't be found on the next lifecycle run. Same trap, but persisted across invocations rather than failing in-place — strictly worse user experience, so worth closing in the same patch.

Small surface, high user-visible value — both fixes patch cleanly into `v1.4.1` without disturbing the v1.4.0 PRD 07 surface.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | **`resolveVarFiles(vfs []string) ([]string, error)`** — small helper that walks the `--var-file` slice, joins relative entries against `os.Getwd()`, pre-flight `os.Stat`s the resolved path so the error message names both the user-supplied and resolved-absolute forms, and pass-throughs absolute entries via `filepath.Clean`. Called once per command at the top of each `RunE` that consumes `flagVarFiles` (the five sites named in `issues/issue_sprint12_staff.md` Issue 1 §"Files affected"). | `internal/cli/lifecycle.go`, `internal/cli/cluster_phase.go`, `internal/cli/bnk_phase.go` |
| 2 | **Unit tests** — `lifecycle_test.go` table covering: absolute path pass-through (unchanged), relative path against CWD (joined + cleaned), missing-file error message (names both user-supplied + resolved path), and the `~`-expansion question (validator decides whether the project's existing convention already handles `~` or whether the helper needs explicit `os.UserHomeDir`-based expansion — staff records the decision in the test names). | `internal/cli/lifecycle_test.go` |
| 3 | **`--tf-source` local-path normalization** (Issue 5, pulled forward from Sprint 13) — resolve a relative local `--tf-source` value to an absolute path before it is persisted into `config.yaml` at `init`, so a later `up` / `plan` / `apply` resolves it correctly regardless of invocation CWD. Mirrors the var-file fix's posture; absolute paths and the URL / GitHub source forms are pass-through. Exact placement and helper naming are staff's call (landing in parallel). | `internal/cli/` (init / lifecycle source-config path) |

### Test deliverables

- **Staff's unit-test trio** per code-deliverable 2.
- **Validator's seven-step regression sweep** (build / vet / fmt / test / staticcheck / `-tags integration` build / `-tags integration` test against ephemeral kind) confirms no regression in the wider surface.
- **Validator's pre-fix bug reproduction** against `main` at HEAD~1 confirming the symptom, plus post-fix confirmation that the same reproduce script succeeds.

### Risks

- **`~`-expansion semantics** — `filepath.Join(cwd, "~/foo.tfvars")` does NOT expand `~`; if the project's existing convention is to rely on the shell for expansion (which is the standard Go posture), the helper's behavior is correct as written. Staff verifies via `grep -rn '"~/' internal/cli/` and records the decision in the unit-test name.
- **Other path-shaped flags with the same gotcha** — any flag whose value flows verbatim to a terraform invocation (e.g., `--backend-config=<path>` if `init` exposes it) is vulnerable to the same shell-CWD-vs-state-dir trap. Validator's regression sweep should surface any; out-of-scope flags get filed as architect-surface follow-ups for v1.4.2 / v1.5.

### Gate to `v1.4.1` tag

- Seven-step regression sweep green; both bugs (`--var-file`, `--tf-source`) reproduce against pre-fix `main`; both fixes make them pass.
- All four agents' issue files at `Status: resolved`, `wontfix`, or `accepted` (validator Issue 5 moves from `open`/Sprint-13-deferred to `resolved` once staff's `--tf-source` fix lands).
- CHANGELOG `v1.4.1` block final (two `### Fixed` bullets); PLAN.md §"Sprint 12" final; chapter 6 polish nudges land cleanly; `mdbook build book/` exit 0.

---

## Sprint 13 — `--on` kubeconfig leak fix + read-only `terraform` + per-AZ jumphost auto-registration (minor cycle, post-v1.4.1)

### Theme

Minor release (`v1.5.0`) bundling the high-severity `--on`-env kubeconfig-leak fix (originally designated a `v1.4.2` fast-follow) with two ergonomic features that came out of the same post-v1.4.0 per-AZ-jumphost user-testing thread: a read-only `roksbnkctl terraform` escape hatch and automatic registration of the per-AZ cluster jumphosts as `jumphost-<zone>` targets, plus the book documentation that ties them together. The integrator decided to ship the bugfix in the same cycle as the features rather than cut a standalone `v1.4.2` patch and a later `v1.5.0` — the three items are tightly related (all surfaced in one user session, all about reaching/operating the deployed cluster from the workstation) and the bugfix is not regressed by or coupled to the feature code, so a single `v1.5.0` closes the whole thread at once. This re-targets the CHANGELOG `v1.4.1 §Deferred` "fix targeted for v1.4.2" note to `v1.5.0`.

This is a multi-deliverable feature cycle (shape closer to Sprint 10 than the single-PRD Sprint 11 or the patch Sprint 12). Two new PRDs are authored this cycle: [PRD 08](docs/prd/08-TERRAFORM-READONLY.md) (read-only `terraform` escape hatch) and [PRD 09](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md) (per-AZ jumphost auto-registration). The implementation-ready design surface is the carried-forward [`issues/issue_sprint13_staff.md`](../issues/issue_sprint13_staff.md) Issues 1–3 (Sprint 12 staff Issues 3/4/5 verbatim) — staff is not blocked on PRD prose; the architect formalizes the PRDs in parallel.

### Drivers / why now

All three items were surfaced in a single post-v1.4.0 live user-testing session (the same thread that produced Sprint 12's `--var-file`/`--tf-source` fixes), against the documented private-cluster `--on jumphost` workflow:

- **KUBECONFIG leak (high)** — after any successful local `roksbnkctl up` (which writes the admin kubeconfig to local `~/.kube/config`), `roksbnkctl --on <target> kubectl|oc …` deterministically fails with `connection to the server localhost:8080 was refused`. `workspaceEnv()` appends `KUBECONFIG=<local path>` and `runPassthrough` forwards the *same* env slice to both the local exec path and the SSH-target path; the local filesystem path is meaningless on the target and shadows the cloud-init-provisioned `/home/ubuntu/.kube/config`. This breaks the canonical private-cluster workflow ([Chapter 16](book/src/16-on-flag-ssh-jumphosts.md), [Chapter 9 §"…existing cluster"](book/src/09-registering-existing-cluster.md)). Same "a path correct for the local machine is wrong once it crosses a boundary" family as Sprint 12's fixes — here the boundary is local-host → SSH-target.
- **No read-only `terraform` escape hatch (feature)** — there is no supported way to run read-only terraform (`output`, `state list`, `show`, `providers`, `version`) against a workspace's managed state. The only workaround is the fragile, undocumented `cd ~/.roksbnkctl/<ws>/state[-cluster] && TF_DATA_DIR=$PWD/terraform terraform …`, which leaks internal layout and is one fat-fingered `apply`/`state rm` from corrupting managed state. Real cases hit this in user testing (looking up per-AZ jumphost FIPs, inspecting a partial apply). See [PRD 08](docs/prd/08-TERRAFORM-READONLY.md).
- **Per-AZ jumphosts not auto-registered (feature)** — with `testing_create_cluster_jumphosts = true` the deploy builds one cluster jumphost per cluster-VPC AZ, each on its own FIP with the shared key, but `tryAutoJumphost` only seeds the singular TGW `jumphost`. The user must discover the others exist, look up FIPs, and `targets add` each by hand. See [PRD 09](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md). Hard doc coupling with the architect's book deliverable (below): if the auto-registration lands, the manual `targets add` walkthrough collapses to "verify with `targets list`" — the two ship in lockstep.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | **KUBECONFIG-leak fix** ([`issues/issue_sprint13_staff.md` Issue 1](../issues/issue_sprint13_staff.md)) — the env that crosses the SSH boundary must not carry local-only filesystem paths. Split `workspaceEnv()` into a machine-portable value-only core (`IBMCLOUD_*`) + a local-only addendum (`KUBECONFIG`); `runPassthrough` forwards only the core on the `on != ""` branch, the full env on the local branch. Sweep **all** `dispatchRemote(` call sites (`runExec` and any other) for the same treatment; add a defense-in-depth sanitize/assert in `dispatchRemote` so every caller is covered. Correctness must come from never *sending* the local path, not from the target sshd's `AcceptEnv` rejecting it. | `internal/cli/cluster.go`, `internal/cli/remote.go`, `internal/cli/` tests |
| 2 | **Read-only `terraform` escape hatch** ([PRD 08](docs/prd/08-TERRAFORM-READONLY.md); [`issues/issue_sprint13_staff.md` Issue 2](../issues/issue_sprint13_staff.md)) — new `roksbnkctl terraform` (alias `tf`) passthrough, **read-only by allowlist** (`output`, `show`, `state list`, `state show`, `providers`, `version`, `graph`, `validate`, `fmt -check`, `state pull`), with a sub-verb guard so `state rm`/`mv`/etc. cannot slip through a permitted top-level `state`, and a mutation-flag scrub. Phase-correct cwd + env reusing `tf.Open`/`config.Workspace[Cluster]StateDir` — **no path/env re-derivation at the CLI layer** (that is exactly the Issue-1 / Sprint-12 bug class). Side-effect-free against a never-applied workspace (no source fetch / `init`; clear "run `roksbnkctl up` first" error). `--on` explicitly rejected (managed state is workstation-local). | `internal/cli/terraform.go` (new), `internal/cli/cluster.go` (register), `internal/tf/terraform.go` (`RunReadOnly`, and an `OpenReadOnly`/option if `tf.Open` isn't side-effect-safe for never-applied) |
| 3 | **Per-AZ jumphost auto-registration** ([PRD 09](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md); [`issues/issue_sprint13_staff.md` Issue 3](../issues/issue_sprint13_staff.md)) — extend the post-`up` hook (sibling `tryAutoClusterJumphosts` next to `tryAutoJumphost`) to read `testing_cluster_jumphost_public_ips` (a `{zone => fip}` map; add a `mapOutput` helper beside `stringOutput`), reuse the existing `jumphost_shared_key` tf-output, and upsert one `jumphost-<zone>` target per AZ via the idempotent `SetTarget`. Best-effort/non-fatal, mirroring `tryAutoJumphost`. **Integrator decision: stale-target handling = option (a) upsert-only** with a documented caveat (orphaned `jumphost-<oldzone>` entries linger until manual `targets remove`); option (b) reconcile is a deliberate post-v1.5.0 follow-up (it needs prefix-ownership semantics or an `auto:` schema marker, out of scope this cycle). | `internal/cli/lifecycle.go` (extend post-`up` hook + `mapOutput`), `internal/cli/lifecycle_test.go` |
| 4 | **Unit tests** — Issue-1: remote-dispatch env asserts `IBMCLOUD_*` present and `KUBECONFIG` absent; local passthrough env still has `KUBECONFIG`. Issue-2: allowlist accept/reject matrix + `state <mutating-subverb>` guard + never-applied-workspace error + `--on` rejection. Issue-3: map-output parse, empty/`[]`/absent → no-op, multi-zone → N upserts, key-PEM-missing → skip. | `internal/cli/*_test.go`, `internal/tf/*_test.go` |

### Documentation deliverables (architect / book)

| Item | Detail |
|---|---|
| [PRD 08](docs/prd/08-TERRAFORM-READONLY.md) | New canonical design doc for the read-only `terraform` escape hatch (allowlist policy, sub-verb guard, phase resolution via existing plumbing, side-effect-free open, `--on` rejection, the "roksbnkctl owns terraform's cwd + `TF_DATA_DIR`" invariant). Authored from `issues/issue_sprint13_staff.md` Issue 2. |
| [PRD 09](docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md) | New canonical design doc for per-AZ jumphost auto-registration (the `{zone => fip}` map output, shared-key reuse, idempotent upsert, the stale-target option-(a) decision + its caveat, parity with `tryAutoJumphost`'s best-effort posture). Authored from `issues/issue_sprint13_staff.md` Issue 3. |
| Chapter 16 + Chapter 15 (architect Issue 1) | Per-AZ cluster-jumphost reachability docs ([`issues/issue_sprint13_architect.md` Issue 1](../issues/issue_sprint13_architect.md), carried from Sprint 12 architect Issue 9): 16 §"Working examples" gets the hop-via-`jumphost` pattern + a §"What `--on` doesn't do" pointer; 15 §"Auto-discovery from terraform outputs" gets the per-AZ auto-registration described. **Hard coupling with code deliverable 3**: written for the *post-Issue-3* world (auto-registered targets → "verify with `targets list`"); the IP-lookup one-liners use `roksbnkctl terraform output …` (code deliverable 2) with the raw-`terraform` fallback noted only for the pre-v1.5.0 reader. Ship in lockstep. |
| CHANGELOG `v1.5.0` | `### Added` (read-only `terraform`, per-AZ auto-registration), `### Fixed` (KUBECONFIG leak — the re-targeted `v1.4.1 §Deferred` known-issue), `### Deferred` (carry-forward + the option-(b) reconcile follow-up). Re-point the `v1.4.1 §Deferred` known-issue note from `v1.4.2` to `v1.5.0`. |
| Chapter 6 / lifecycle help (tech-writer follow-up) | Sprint 12 tech-writer §"Sprint 13 awareness": now that `--tf-source` resolves relative paths, the `init` / `up --tf-source` cobra help (`internal/cli/lifecycle.go:86,89`, "override TF source (path or URL)") wants the same relative-path-resolution scrutiny the `--var-file` help got. Low-severity discoverability nudge — architect surface, fold in if the help text still misleads. |

### Test deliverables

- **Staff's three unit-test suites** per code deliverable 4.
- **Validator's seven-step regression sweep** (build / vet / fmt / test / staticcheck / `-tags integration` build / `-tags integration` test against ephemeral kind) — unchanged gate from Sprints 10–12.
- **Validator's bug reproduction** for Issue 1: confirm the `up` → `--on <target> kubectl` `localhost:8080` symptom against pre-fix `main` (or, since the agent shell can't drive a live `--on` against a real jumphost, a focused unit test asserting the remote-dispatch env composition), then confirm the fix makes it pass. Live `--on jumphost kubectl` verify is the user's out-of-band action (same hand-off shape as Sprint 11 Issue 2 / Sprint 12).
- **Validator's feature acceptance checks** for PRD 08 (allowlist matrix, never-applied error, `--on` rejection) and PRD 09 (map-parse no-op cases, multi-zone upsert) via the unit suites + a doc-coupling audit that the architect's chapter 15/16 edits match the *as-landed* auto-registration behaviour (no manual-`targets-add` drift).
- **Tech-writer drift sweep** at end of sprint across `issues/issue_sprint13_staff.md` ↔ code ↔ PRD 08/09 ↔ CHANGELOG `v1.5.0` ↔ chapters 15/16, plus a dogfooding pass on the now-working `--on jumphost kubectl` flow and the `roksbnkctl terraform output` one-liner.

### Risks

- **Env-split blast radius** — splitting `workspaceEnv()` and sweeping every `dispatchRemote(` caller risks missing a call site or accidentally dropping a value-grade var (`IBMCLOUD_*`) from the *local* path. Mitigation: the unit test asserts both directions (remote = no KUBECONFIG, local = KUBECONFIG present) and `grep -n "dispatchRemote(" internal/cli/` enumerates the call sites for the sweep.
- **`tf.Open` side effects on a never-applied workspace** — PRD 08 requires read-only invocations to *not* trigger a source fetch / `init`. If `tf.Open` is not side-effect-safe, an `OpenReadOnly` (or option) is required — scope it in PRD 08, not discovered mid-implementation.
- **Cloud-init boot-timing race (out of scope, cross-referenced)** — `terraform/modules/testing/main.tf:80-104` writes `/home/ubuntu/.kube/config` via `ibmcloud ks cluster config --admin` guarded by `|| true`, asynchronously; a freshly-booted jumphost can transiently lack a kubeconfig and produce the *same* `localhost:8080` symptom independent of the env leak. The Issue-1 fix is necessary but not sufficient against this race; it is a separate architect/infra hardening item, explicitly **not** in v1.5.0 scope — cross-reference in `issues/issue_sprint13_architect.md` so it isn't conflated with the env-leak verify.
- **Stale `jumphost-<zone>` orphans** — option (a) upsert-only leaves orphaned targets after a zone removal / `testing_create_cluster_jumphosts=false` flip. Accepted for v1.5.0 with a documented caveat; option (b) reconcile is a tracked post-v1.5.0 follow-up.
- **Doc/code lockstep** — architect Issue 1's chapter 15/16 prose is written for the post-auto-registration world; if code deliverable 3 slips, the chapters must not ship describing behaviour that isn't in the binary. Gate criterion below enforces lockstep.

### Gate to `v1.5.0` tag

- Seven-step regression sweep green; Issue-1 symptom reproduces against pre-fix `main` and the fix makes it pass (unit-level; live `--on` verify is the user's out-of-band action).
- PRD 08 allowlist accept/reject matrix + sub-verb guard + never-applied + `--on`-rejection tests green; PRD 09 map-parse / no-op / multi-zone-upsert / key-missing tests green.
- All four agents' issue files at `Status: resolved`, `wontfix`, or `accepted`.
- PRD 08 + PRD 09 final under `docs/prd/`; `docs/prd/00-OVERVIEW.md` references them if it indexes PRDs; CHANGELOG `v1.5.0` block final (Added/Fixed/Deferred) with the `v1.4.1 §Deferred` known-issue note re-pointed to `v1.5.0`; PLAN.md §"Sprint 13" final.
- Chapter 15/16 per-AZ-jumphost docs match the as-landed auto-registration behaviour (no manual-`targets-add` drift); all new cross-links resolve; `mdbook build book/` HTML backend exit 0.

### Carry-overs from prior sprints

- **Sprint 12 staff Issue 3** (KUBECONFIG leak) — Sprint 13 code deliverable 1; carried verbatim into `issues/issue_sprint13_staff.md` Issue 1.
- **Sprint 12 staff Issues 4 + 5** (read-only `terraform`; per-AZ auto-registration) — code deliverables 2 + 3 / PRD 08 + PRD 09; carried into `issues/issue_sprint13_staff.md` Issues 2 + 3.
- **Sprint 12 architect Issue 9** (per-AZ jumphost book docs) — architect documentation deliverable; carried into `issues/issue_sprint13_architect.md` Issue 1.
- **Sprint 12 tech-writer §"Sprint 13 awareness"** (`--tf-source` cobra-help relative-path scrutiny) — folded into the architect documentation deliverables as a low-severity nudge.
- Prior-sprint deferred items (chapter 14 §"What's new in v1.2" position, chapter 19 §"5. Create the Pod" `env:` block, `ops install`/`ops uninstall` snapshot) remain deferred — no movement this sprint.

---

## Sprint 14 — get-well: jumphost kubeconfig provisioning fix + close the validation blind spot (get-well cycle, folds into the held `v1.5.0`)

### Theme

**Get-well cycle.** Sprint 13 fixed the KUBECONFIG env leak (live-verified) but the canonical private-cluster workflow — `roksbnkctl up` then `roksbnkctl --on jumphost kubectl|oc` — is **still broken** because the jumphost has no kubeconfig at all: cloud-init's `ibmcloud login` + `ibmcloud ks cluster config --cluster … --admin` are guarded by `|| true`, so any boot-time failure is swallowed and `/home/ubuntu/.kube/config` is never written (live-confirmed 2026-05-18 14:54: `KUBECONFIG=[]` on the wire — env fix working — but `/home/ubuntu/.kube/config: No such file or directory`). The two `localhost:8080` causes are indistinguishable to a user, so per the integrator **hold-and-merge** decision `v1.5.0` is **not** cut at Sprint 13 close: the `## Unreleased (v1.5.0)` CHANGELOG block stays open and this sprint lands the kubeconfig fix **into the same `v1.5.0`**, so the release that finally ships makes `--on jumphost kubectl|oc` work end-to-end. Integrator decision: **option C** (both layers). Plus one structural deliverable pulled forward from the Sprint 15 consolidation analysis: the e2e + `--on` integration test that closes the **validation blind spot** — this high-severity defect reached a human in live testing, not the four-agent gate, because no test composes the full `up → --on <target>` path.

### Drivers / why now

- **The documented happy path is still broken.** `book/src/16-on-flag-ssh-jumphosts.md` / `09-registering-existing-cluster.md` advertise `--on jumphost kubectl|oc` as the private-cluster workflow; it deterministically fails on any jumphost where the silent cloud-init step didn't land a kubeconfig. Root cause + the option-A/B/C analysis are in [`issues/issue_sprint13_architect.md` Issue 2](../issues/issue_sprint13_architect.md) (escalated low→**HIGH** from live testing).
- **The gate has an integration blind spot.** A *high*-severity defect was found by the user running live against IBM Cloud, not by the validation gate — the unit suites never compose `up → --on <target>` env + kubeconfig resolution. The slowest possible feedback loop is catching high-sev bugs in human live-testing. Closing it here (not deferring the whole consolidation) makes the kubeconfig fix itself gate-verifiable rather than live-verified-by-the-user.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | **Option C part A — harden cloud-init kubeconfig provisioning.** Replace the bare `|| true` on the jumphost `ibmcloud login` + `ibmcloud ks cluster config --cluster "${var.roks_cluster_name_or_id}" --admin` with a bounded retry/readiness loop (cluster may not be Ready at boot); on exhaustion write a loud failure marker (`/var/log/jumphost-setup.log` + a sentinel file) instead of silently continuing; ensure `/home/ubuntu/.kube/config` (and `/root/.kube/config`) is reliably produced when the cluster eventually becomes reachable. Fixes **new** deploys robustly. | `terraform/modules/testing/main.tf` |
| 2 | **Option C part B — roksbnkctl `--on` kubeconfig self-heal.** When an `--on <target>` dispatch (`kubectl`/`oc`) targets a host with no usable kubeconfig, repair it on the fly: run `ibmcloud ks cluster config --cluster <id> --admin` on the target (it is already `ibmcloud login`'d as `ubuntu` per the cloud-init fork) before the wrapped command, and/or have the post-`up` hook push a freshly-fetched admin kubeconfig to each seeded jumphost target. Unblocks **already-broken / already-running** jumphosts with no `terraform` recreate (the user's current host). Idempotent; must not mask a genuinely down cluster (surface the real error, don't loop forever). | `internal/cli/cluster.go`, `internal/cli/remote.go`, `internal/cli/lifecycle.go` (post-`up` hook), `internal/cli/` tests |
| 3 | **E2e + `--on` integration smoke (pulled forward from the Sprint 15 consolidation plan, deliverable 2).** A behavior-level test that drives `up → --on <target>` and asserts the remote-vs-local env composition **and** the kubeconfig self-heal path (the exact surface the Sprint 13 Issue 1 leak slipped through); plus a `-tags integration` smoke for the `--on`/passthrough path against ephemeral kind. The Sprint 13 Issue 1 + this sprint's kubeconfig fix become permanent regression guards: an Issue-1-class or missing-remote-kubeconfig defect must fail a test, not a human. | `internal/cli/lifecycle_e2e_test.go` (new), `internal/cli/` integration-tagged test |

### Test deliverables

- **Staff's e2e + `--on` integration suites** per code deliverable 3 — the headline gate addition.
- **Validator's seven-step regression sweep** (build / vet / fmt / test / staticcheck / `-tags integration` build / `-tags integration` test against ephemeral kind) — unchanged; the new e2e/`--on` test runs inside it.
- **Validator's live-verify hand-off**: `roksbnkctl up` then `roksbnkctl --on jumphost kubectl get pods` must succeed **end-to-end** (no `localhost:8080`) — the user's out-of-band action, but now backed by the e2e/integration gate so it is no longer the *only* signal (closes the blind spot). The 2026-05-18 14:54 diagnostic command is the repro baseline.
- **Tech-writer drift sweep + caveat removal**: the `v1.5.0`/`v1.4.1 §Deferred` known-issue notes and the book ch15/16/09 "may still fail / pre-v1.5.0 caveat" prose must be **removed/flipped to resolved** (not merely re-pointed) once the flow works — the central doc deliverable of this cycle.

### Risks

- **Cloud-init is not unit-testable.** Part A runs at instance boot; it can only be validated by the `-tags integration` smoke + the user's live `up`. Mitigation: keep the retry/readiness logic small and shell-lint-clean; the loud failure marker means a future failure is *visible*, not silent (the actual defect being fixed).
- **Self-heal masking a real outage.** Part B must distinguish "jumphost has no kubeconfig" (heal) from "cluster is genuinely down" (surface the error, bounded retry, don't spin). Mitigation: bounded attempts + pass through the real `ibmcloud ks cluster config` error on exhaustion.
- **Hold-and-merge changelog hygiene.** The `v1.5.0` block must end up describing *one* coherent release (env leak + kubeconfig both fixed), not two half-stories. Mitigation: tech-writer drift sweep owns reconciling the `v1.5.0` §Fixed + deleting the carried known-issue notes.

### Gate to the (finally tag-ready) `v1.5.0`

- Live `roksbnkctl up` → `roksbnkctl --on jumphost kubectl get pods` succeeds end-to-end (user out-of-band) **and** the new e2e + `--on` integration test is green in the seven-step sweep (the bug is now caught by the gate, not only by a human).
- All four agents' Sprint 14 issue files at `Status: resolved`, `wontfix`, or `accepted`; `issues/issue_sprint13_architect.md` Issue 2 flips to `resolved`.
- CHANGELOG `## Unreleased (v1.5.0)` `### Fixed` includes the kubeconfig provisioning fix; the `v1.4.1 §Deferred` + `v1.5.0` carried known-issue notes are **removed/resolved** (not re-pointed); book ch15/16/09 caveats deleted; `mdbook build book/` exit 0.
- Only after this gate is `v1.5.0` integrator-tag-ready (the held release now genuinely makes `--on jumphost kubectl|oc` work).

### Carry-overs / explicitly out of scope

- **Sprint 13 architect Issue 2** (cloud-init kubeconfig failure) — this sprint's headline; carried into `issues/issue_sprint14_staff.md` Issue 1 + `issues/issue_sprint14_architect.md` Issue 1.
- **Structural consolidation** (single path/env chokepoint refactor, `internal/cli` god-package decomposition, sprint-process tiering) — preserved as [§"Sprint 15"](#sprint-15--consolidation-root-cause-the-boundary-bug-class--decompose-the-cli-god-package-consolidation-cycle-post-v150) below; deliberately **not** this sprint (a get-well cycle ships the fix; the refactor is its own cycle). Only the consolidation plan's e2e/blind-spot test (its old deliverable 2) is pulled forward here.
- **Option-(b) per-AZ stale-target reconcile** — unchanged; still a deliberate post-`v1.5.0` follow-up.

---

## Sprint 15 — consolidation: root-cause the boundary-bug class + decompose the `cli` god-package (consolidation cycle, post-`v1.5.0`)

### Theme

A **debt-paydown / consolidation cycle** (`v1.6.0`, after the held `v1.5.0` ships via Sprint 14; **no user-visible behavior change** — the integrator may re-designate `v1.5.1` under strict SemVer since no API/behavior changes, but the structural surface is large enough to warrant a minor). Zero new features. Two coupled structural fixes that together stop the velocity decay observed across Sprints 11–13: (1) collapse the recurring "a path/env correct locally is wrong once it crosses a boundary" bug class to a single chokepoint, (2) begin decomposing the `internal/cli` god-package. (The third original goal — closing the integration-test blind spot that let a *high*-severity bug reach a human — was **pulled forward into Sprint 14** as its e2e/`--on` test, since it makes the kubeconfig fix gate-verifiable; Sprint 15 *consumes* that suite as the behavior-parity harness rather than building it.) Plus a **process deliverable**: tier the sprint process by change size so fixed per-sprint ceremony stops dominating small changes (mirrored into `NEW_PROJECT_STARTING_POINT.md` so the next project doesn't reproduce the slowdown).

### Drivers / why now

Evidence-based, from the Sprint-13-close project-health review:

- **The bug class is recurring, not incidental.** Sprint 12 Issues 1 + 2 (`--var-file`, `--tf-source`) and Sprint 13 Issue 1 (KUBECONFIG leak) are the *same* defect shape — a value correct in the invocation context is consumed in a different context (terraform's per-phase state-dir CWD; the SSH target). Each was patched as an instance: `resolveVarFiles` wired at **8+ RunE call sites**, `--tf-source` normalized separately in `init.go`, and `workspaceEnv()` split into `workspaceEnvCore`/`remoteSafeEnv` with a `localPathEnvKeys` scrub list. All idempotent and correct, but there is **no single chokepoint** — the next path/env-valued flag or the next `dispatchRemote` caller is one omission away from re-opening the class. Patching instances is now a recurring per-sprint tax.
- **The validator blind spot — addressed in Sprint 14, leveraged here.** The Sprint 13 Issue 1 KUBECONFIG leak (severity **high**) was discovered by the user running `roksbnkctl up` then `--on jumphost kubectl` live against IBM Cloud — *not* by the four-agent validation gate; the unit suites never composed the full `up → --on <target>` path. Sprint 14 closed that gap with the e2e + `--on` integration suite. Sprint 15 does **not** rebuild it — it relies on that suite as the behavior-parity gate for the refactor below (any diff in it during the consolidation is a drift signal). The remaining structural debt is the recurring bug class itself and the god-package.
- **`internal/cli` is a god-package.** 16,218 LOC / 29 files = **61% of all internal code**; `lifecycle.go` 1,058 LOC, `cluster.go` 739, `ops.go` 701. The Sprint-13 health read projects refactoring becomes the binding constraint by ~1.5× current LOC. Marginal change cost is already visible in the Sprint 11→13 cadence decay (32 commits/day → a four-calendar-day two-bug patch).

Build/vet are clean (`go build`/`go vet ./...` rc=0) and features still ship — this is structural strain, not rot, so consolidation (not a greenfield restart) is the correct response.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | **Single path/env normalization chokepoint.** Introduce one resolved-invocation context (working name `cli.ResolvedFlags`, computed once in a cobra `PersistentPreRunE` or a single `resolveInvocationContext()` at command entry) that: normalizes every path-valued flag (`--var-file`, `--tf-source`, any future one) against `os.Getwd()` exactly once; and classifies process env into a machine-portable core (`IBMCLOUD_*`) vs. local-only (`KUBECONFIG`, any future local-path-valued var). Downstream code consumes the resolved struct; **no RunE and no `dispatchRemote` caller re-derives**. Delete the now-unreachable per-RunE `resolveVarFiles` fan-out and the defensive `remoteSafeEnv`/`localPathEnvKeys` scrub (or demote the scrub to a single assertion at the boundary). This structurally retires Sprint 12 Issues 1/2 + Sprint 13 Issue 1 as a *class*. | `internal/cli/root.go`, `internal/cli/lifecycle.go`, `internal/cli/cluster.go`, `internal/cli/cluster_phase.go`, `internal/cli/bnk_phase.go`, `internal/cli/remote.go`, `internal/cli/init.go` |
| 2 | **`internal/cli` decomposition — phase 1a (behavior-preserving).** Establish the `internal/orchestration` service layer and land the deliverable-1 chokepoint + the env classification (`Resolve`/`ResolvedFlags`, `NormalizeVarFiles`/`NormalizeLocalPath`, `WorkspaceEnv[Core]`/`ScrubLocalOnly`/`LocalOnlyEnvKeys`) in it; `internal/cli` consumes it via one-line delegating wrappers + the single `root.go` `PersistentPreRunE`. **Re-scoped (integrator decision 2026-05-18, see §"Scope decision" below):** the bulk move of `lifecycle.go` + `cluster.go` *RunE orchestration* into the new layer is **phase 1b → tracked Sprint 16** — a ~1.6k-LOC pure-structural move with zero user-visible payoff, disproportionate risk to bundle here once the consolidation *thesis* (deliverable 1) is already banked. Phase 1a leaves `lifecycle.go`/`cluster.go` consuming orchestration but not yet emptied. | `internal/orchestration/` (new), `internal/cli/root.go` + delegating wrappers |
| 3 | **Regression / guard tests + cheap-win coverage.** (a) Behavior-parity: the entire pre-existing suite — **including the Sprint 14 e2e + `--on` integration suite** — passes **unchanged** (no golden `-update` churn; that suite is the refactor's parity harness). (b) A guard test that fails if any RunE or `dispatchRemote` caller re-derives a path/env instead of consuming `ResolvedFlags` (greppable invariant, asserted in CI). (c) Fold in `internal/cos` unit tests (currently **0%**, 408 LOC) as a low-cost coverage win while the consolidation is open. | `internal/cli/*_test.go`, `internal/orchestration/*_test.go`, `internal/cos/*_test.go` (new) |

### Process deliverable

- **Tier the sprint process by change size.** Codify patch / minor-feature / consolidation / greenfield tiers, each with *proportionate* ceremony (which of the four agents run, ledger depth, gate weight). Sprint 12 shipped a ~50-line helper but generated ~2,053 lines of four-agent ledger + drift-sweep tables + re-review passes — fixed per-sprint overhead now dominates small changes. The tiering applies to roksbnkctl's own remaining maintenance sprints **and** is written into [`NEW_PROJECT_STARTING_POINT.md`](../NEW_PROJECT_STARTING_POINT.md) §"Tiering the sprint process by change size" so the next project doesn't reproduce this curve. This sprint itself runs at the *consolidation* tier (full staff + validator, light architect/tech-writer — it's internal, no PRD, no book surface).

### Test deliverables

- **Staff's guard suite + `internal/cos` coverage** per code deliverable 3. The **Sprint 14 e2e + `--on` integration suite is reused unchanged as the refactor's behavior-parity harness** — not rebuilt here.
- **Validator's seven-step regression sweep** (build / vet / fmt / test / staticcheck / `-tags integration` build / `-tags integration` test against ephemeral kind) — unchanged gate, but now the **behavior-parity assertion is the headline gate**: the full pre-existing suite (incl. the Sprint 14 e2e/`--on` suite) must pass with zero diffs (no test edited to accommodate the refactor; a changed test is a behavior change and fails the gate).
- **Validator's chokepoint-invariant audit**: `grep` proves zero per-RunE / per-`dispatchRemote` path-or-env re-derivation; the deleted `remoteSafeEnv`/`localPathEnvKeys` scrub is provably unreachable (or demoted to one boundary assertion).
- **No new feature acceptance** (there are no features) — the Sprint 14 Issue-1 + kubeconfig regression guards must stay **green and unedited** through the refactor (proof the consolidation preserved the boundary-bug + remote-kubeconfig fixes structurally).

### Risks

- **Refactor blast radius / silent behavior drift.** Moving 1,800 LOC of orchestration risks subtle behavior change. Mitigation: the behavior-parity gate (entire pre-existing suite green *unchanged*); any test that needs editing to pass is treated as a drift signal, not a test fix.
- **`cli` split scope creep.** "Decompose the god-package" invites a big-bang. Mitigation: phase-1 boundary is *exactly* `lifecycle.go` + `cluster.go`, written into the gate; the other 27 files are an explicitly deferred tracked follow-up.
- **E2e test infra cost / flake.** A full-lifecycle test can be slow/flaky. Mitigation: docker/stubbed-terraform deterministic core in the unit gate; the kind-backed `--on` smoke is `-tags integration`, separate from the fast gate (same split Sprints 10–13 already use).
- **Chokepoint regressions an edge case.** Centralizing path/env handling could mis-handle a flag a scattered site handled specially. Mitigation: deliverable-4 guard + the parity gate; enumerate every current `resolveVarFiles`/`workspaceEnv*`/`dispatchRemote` site before deleting it.

### Gate to `v1.6.0` tag

- **Behavior parity:** entire pre-existing unit + integration suite green with **zero test-file diffs**; no user-visible behavior change (a manual `up`/`--on`/`terraform`/`targets` smoke matches v1.5.0 output).
- **Single chokepoint proven:** `grep` shows no RunE / `dispatchRemote` caller re-deriving a path or env; the defensive `remoteSafeEnv`/`localPathEnvKeys` scrub is deleted or demoted to one unreachable-by-construction assertion; the Sprint 14 e2e Issue-1 + kubeconfig regression guards remain green and unedited through the refactor.
- **`cli` phase-1a boundary clean (re-scoped):** the `internal/orchestration` layer exists with the chokepoint + env classification landed there; `orchestration` does **not** import `internal/cli` (one-directional boundary, asserted); `internal/cli` consumes it via one-line delegating wrappers + the single `root.go` `PersistentPreRunE`. The full emptying of `lifecycle.go`/`cluster.go` into the layer is **phase 1b / Sprint 16** (tracked) — explicitly NOT a `v1.6.0` gate criterion per the §"Scope decision" below.

### Scope decision (integrator, 2026-05-18)

Once deliverable 1 landed it was clear the consolidation **value** — structurally retiring the recurring "value correct in one context, wrong across a boundary" bug class via a single chokepoint — was already complete and parity-verified. Deliverable 2's remaining part (the wholesale ~1,655-LOC behavior-preserving move of `lifecycle.go`+`cluster.go` RunE orchestration into `internal/orchestration`, with heavy `flag*` package-global recoupling) is a pure structural move with **zero user-visible payoff** and a large regression surface on a tool that shipped `v1.5.0` the same day. Per Sprint 15's own process-tiering deliverable (proportionate effort), the integrator re-scoped phase 1 → **1a (this sprint): orchestration layer + chokepoint/env landed & consumed; 1b (Sprint 16, tracked): the bulk file move**. `v1.6.0` ships on the banked consolidation value (deliverable 1) + deliverable 3 + the parity guarantee; it does not gate on 1b.
- All four agents' issue files at `Status: resolved`, `wontfix`, or `accepted`.
- CHANGELOG `v1.6.0` block final — `### Changed` (internal consolidation; explicitly "no user-visible behavior change"), `### Removed` (the defensive env-scrub now obviated by the chokepoint); PLAN.md §"Sprint 15" final; `NEW_PROJECT_STARTING_POINT.md` §"Tiering the sprint process by change size" final; `mdbook build book/` exit 0 (no book surface this cycle — clean by no-op).

### Carry-overs / explicitly out of scope

- **`cli` decomposition phase 1b → Sprint 16** (tracked) — the wholesale move of `lifecycle.go` + `cluster.go` RunE orchestration into `internal/orchestration`. Re-scoped out of Sprint 15 per §"Scope decision" (the orchestration layer + chokepoint are landed; this is the remaining pure-structural file move, zero user-visible payoff). Sprint 16 = phase 1b.
- **`cli` decomposition phases 2+** (the remaining ~27 `cli` files) — tracked follow-up after phase 1b.
- **Option-(b) per-AZ stale-target reconcile** — unchanged from Sprint 13; still a deliberate post-v1.5.0 follow-up.
- **Cloud-init kubeconfig provisioning fix** (`terraform/modules/testing/main.tf` + roksbnkctl `--on` self-heal, option C) — **landed in Sprint 14**, not here; Sprint 15 must not regress it (its e2e/`--on` guard is part of the parity gate).
- **Any user-visible feature or behavior change** — out of scope by definition; this is strictly internal hardening.

---

## Sprint 16 — consolidation phase-1b: empty `lifecycle.go` + `cluster.go` into `internal/orchestration` (consolidation cycle, post-`v1.6.0`)

### Theme

The deferred second half of the Sprint 15 `cli` decomposition. Sprint 15 phase-1a landed the `internal/orchestration` service layer + the single path/env chokepoint and made `internal/cli` consume it via delegators; the integrator re-scoped the **bulk move** of the two hottest files out of that sprint (see [§"Sprint 15 → Scope decision"](#scope-decision-integrator-2026-05-18)). Sprint 16 is exactly that bulk move: relocate the lifecycle / cluster / remote-passthrough **RunE orchestration** (~1,655 LOC across `internal/cli/lifecycle.go` + `internal/cli/cluster.go`) into `internal/orchestration`, leaving `internal/cli` a thin cobra adapter (flag binding + `PersistentPreRunE` + delegating RunEs). **Strictly internal — zero user-visible behavior change**, identical posture to Sprint 15. Consolidation tier (full staff + validator, light architect + tech-writer; no PRD, no book surface). Version is integrator-owned at cut: `v1.6.1` under strict SemVer (no API/behavior change), or `v1.7.0` if the structural surface is judged minor-worthy.

### Drivers / why now

`internal/cli` remains the god-package (`lifecycle.go` 991 LOC, `cluster.go` 664 — the two hottest files, ~per the Sprint-13 health read the binding constraint on change cost). Phase-1a proved the orchestration boundary is sound (one-directional import, chokepoint guard CI-asserted, behavior parity held). Phase-1b pays down the remaining structural debt while the boundary is fresh and the parity harness (Sprint 14 e2e/`--on` suite) is in place to backstop it. Deferring further only lets the two files keep accreting.

### Code deliverables

| Order | Item | Files |
|---|---|---|
| 1 | **Move lifecycle orchestration → `internal/orchestration`.** Relocate `runUp`/`runTrialUp`/`runPlan`/`runApply`/`runDown`/`runTrialDown` + their helpers (`openTF`, `applyWithRetry`, `tryAuto*`, `runTerraformLifecycleDocker`/`dockerTerraform*`, `resolveClusterIdentity`, …) into the service layer. `internal/cli/lifecycle.go` shrinks to thin cobra `RunE` shims that bind flags and call `orchestration`. Behavior-preserving; flag globals passed in (no orchestration→cli import). | `internal/orchestration/` (new files), `internal/cli/lifecycle.go` |
| 2 | **Move cluster / remote-passthrough orchestration → `internal/orchestration`.** Relocate `runShell`/`runExec`/`runKubeconfig*`/`run*Passthrough`/`dispatchBackend`/`ensureIBMCloudLoggedIn`/`runWithEnv`/`clusterFromTFOutput` + the `extract*Flag` helpers; `internal/cli/cluster.go` → thin shims. The Sprint 14 `--on` self-heal (`selfheal.go`) and the chokepoint/env layer stay where they are (already in the right place). | `internal/orchestration/` (new files), `internal/cli/cluster.go` |
| 3 | **Coverage that travels with the move.** Any test that referenced moved unexported symbols is handled by keeping the public seam in `orchestration` and the thin shim in `cli` — **no pre-existing test file may be edited** (parity gate). New `internal/orchestration/*_test.go` may be added for newly-exported orchestration entry points. | `internal/orchestration/*_test.go` (new) |

### Test deliverables

- **Behavior-parity assertion is the headline gate** (unchanged from Sprint 15): the entire pre-existing unit + integration suite — **including the Sprint 14 e2e + `--on` integration suite** — passes with **zero test-file diffs** vs the `v1.6.0` baseline. A pre-existing test edited to accommodate the move is drift, not a fix, and fails the gate.
- **Validator's seven-step regression sweep** + the full hermetic `go test -race ./...` (CI's exact command), run by whoever has a working toolchain (integrator-run if the validator agent's session is toolchain-denied, per the Sprint 15 precedent).
- **`cli` phase-1b boundary audit:** `internal/cli` no longer owns lifecycle/cluster orchestration; `lifecycle.go` + `cluster.go` are thin adapters; `internal/orchestration` still does **not** import `internal/cli`; the Sprint 15 chokepoint guard test stays green & unedited.

### Risks

- **Refactor blast radius.** ~1,655 LOC of orchestration moving packages; subtle behavior drift is the main risk. Mitigation: the behavior-parity gate (entire pre-existing suite + Sprint 14 `--on` harness green *unedited*); move in two staged commits (lifecycle, then cluster) re-running the gate after each.
- **Flag-global recoupling.** `cli` RunEs read package-level `flag*` vars; the moved code must take them as parameters/struct, not import `cli`. Mitigation: pass an explicit inputs struct; the one-directional import is audited.
- **Scope creep into the other ~27 `cli` files.** Phase-1b is *exactly* `lifecycle.go` + `cluster.go`; the rest stay a tracked phase-2 follow-up.

### Gate to the (integrator-owned) `v1.6.1`/`v1.7.0` tag

- Behavior parity: entire pre-existing suite + Sprint 14 `--on` harness green with **zero test-file diffs** vs `v1.6.0`; full hermetic `go test -race ./...` green; build/vet/gofmt/staticcheck/integration-build clean.
- `cli` phase-1b boundary clean: `lifecycle.go` + `cluster.go` are thin adapters; `internal/orchestration` does not import `internal/cli`; chokepoint guard green & unedited; Sprint 14 kubeconfig fix not regressed.
- All four agents' Sprint 16 issue files terminal (`resolved`/`accepted`/`wontfix`); CHANGELOG block final (`### Changed` = internal decomposition, explicitly "no user-visible behavior change"); PLAN §"Sprint 16" final.

### Carry-overs / explicitly out of scope

- **`cli` decomposition phases 2+** (the remaining ~27 `cli` files) — tracked follow-up, deliberately not this sprint.
- **Option-(b) per-AZ stale-target reconcile** — unchanged; still a deliberate post-`v1.5.0` follow-up.
- **Sprint 14 kubeconfig fix + Sprint 15 chokepoint** — must not regress; their guards are part of the parity gate.
- **Any user-visible feature or behavior change** — out of scope by definition.

### Follow-up (post-`v1.6.1`): phase-handoff regression — validator Issue 2

A live `!` verify after the `v1.6.1` cut surfaced a regression the phase-1b parity gate was correct-but-blind to: a full `up` second (bnk/testing) phase re-created the cluster phase's already-made cluster-shared network, so IBM Cloud rejected the run with duplicate-name errors ([`issues/issue_sprint16_validator.md` Issue 2](../issues/issue_sprint16_validator.md)). The parity gate stayed GREEN by design — no hermetic test exercises a workspace that has already completed the cluster phase, so the duplication introduced alongside the phase-1b lifecycle/cluster split was not test-observable.

A first fix attempt (terraform + Go existing-resource handoff: `use_existing_cluster_vpc` / `existing_cluster_vpc_id` / `testing_create_client_vpc=false` from `cluster-outputs.json`) landed and passed the hermetic regression test, but the **live `!` verify (run-id `20260519-181511`) came back RED** and revealed Issue 2 was materially broader than first analysed: the second phase re-created the **entire cluster-shared network** — cluster subnets, cluster public gateways, the transit gateway, the testing client VPC, and the testing jumphost subnets/SG — not just the cluster VPC. Only the `use_existing_cluster_vpc` VPC reuse took effect; the per-resource `create_roks_transit_gateway=false` / `testing_create_client_vpc=false` toggles did not suppress their resources, and subnets/public-gateways/jumphost-network were never in the handoff model. The premature `### Fixed` claim was reverted; Issue 2 was reopened/expanded and staff re-dispatched. The corrected fix (the round-2 architecture, commit `0db0ad6`) stops the second phase re-provisioning the cluster-shared modules at all: when `cluster-outputs.json` exists the bnk/testing phase writes a forced `bnk-phase-override.tfvars` (`create_roks_cluster=false` + `roks_cluster_id_or_name`, `use_existing_cluster_vpc=true` + `existing_cluster_vpc_id`, `create_roks_transit_gateway=false`, all three `testing_create_*=false`), appended last to the var-file chain — symmetric with the proven `cluster-phase-override.tfvars`. **Live-verified GREEN, run-id `20260519-202202`** (cluster phase `72 added`, bnk phase `60 added`, A1–A4 ✓, two-phase self-teardown ✓, zero `canada-*` residue) and shipped as the `v1.6.2` patch. The Issue 4 e2e-driver teardown defect (it ran only the trial `down`, stranding the cluster phase on a failed live verify) is fixed in the same patch. **Validator Issue 3** — the PRD 07 `terraform.applied.tfvars` snapshot was write-only, so bare `roksbnkctl down`/`plan`/`apply -w <ws>` deterministically failed on required no-default vars — is also folded into `v1.6.2`, with its own RED→GREEN cycle: a first round-2 fold-in pointed terraform directly at the canonical multi-section snapshot and was caught broken by live verify run-id `20260519-220236` (terraform: `Each argument may be set only once` — the same key legitimately appears in multiple sections of the snapshot, and the redacted-secret line would have overridden `TF_VAR_ibmcloud_api_key` from env). The round-3 fix derives a deduped, secret-free `<phase state dir>/.applied-replay.tfvars` from the canonical snapshot at lifecycle-op time (snapshot itself unchanged for audit) and points terraform at that. **Live-verified GREEN run-id `20260519-234554`** (A1–A5 ✓ including A5 bare `plan -w <ws>` succeeded via the applied-tfvars replay; both-phase teardown ✓; zero `canada-*` residue). A small adjacent UX item — option (b): pre-empt terraform's raw missing-required-var stack with an actionable roksbnkctl-level error when neither a snapshot nor an explicit `--var-file` is available — was finished *before* the GitHub Release of `v1.6.2` (don't pile adjacent fixes into a release that's already mid-cut) and shipped in the same tag after its own live verify: **GREEN run-id `20260520-035616`** (A6 ✓ pre-up bare `plan -w <ws>` refused with the actionable error before any cloud call; A1–A5 carry-over ✓). A new e2e assertion pair **A5** + **A6** now live-probes the Issue 3 fix on every subsequent run. This whole cycle is the `live-verify-high-issues` discipline working as designed — the hermetic-GREEN round-1/round-2 fixes were each caught broken in practice before any tag.

---

## Sprint 28 — three-phase split: Cluster / BNK / Testing (parallel + independent lifecycle) (draft, on feature branch `sprint28-three-phase-split`, stacked on Sprint 27)

_Surfaced 2026-06-04 during the Sprint 27 live verify. roksbnkctl is two phases today — **cluster** (`state-cluster/`) and **trial** (`state/`), where "trial" lumps BNK (cert-manager + FLO + CNEInstance + License) AND the testing jumphosts into one state. That coupling means you can't tear down BNK while keeping the jumphosts (to reuse for testing), and the two unrelated workloads can't deploy in parallel. The live verify made the seams obvious (cert-manager phase placement; a jumphost capacity blip sinking the whole apply)._

Split into **three independent phases**: **Cluster** (ROKS cluster + cluster VPC + TGW; create OR reuse) → **BNK** (k8s workloads, fully cluster-dependent) ∥ **Testing** (jumphosts; pure IBM VPC, depends on the cluster only for the network — VPC + TGW). BNK and Testing are independent → run **in parallel** after Cluster, and **tear down independently** (`bnk down` leaves the jumphosts for reuse, and vice versa). Sprint 27's real-terraform-state per phase is the prerequisite that makes independent per-phase `destroy` clean — which is why this stacks on Sprint 27.

Locked decisions (recommended where noted — confirm before dispatch): **three states** (`state-cluster/`, BNK state, `state-testing/`; architect pins BNK `state/` vs `state-bnk/` + the pre-Sprint-28 migration); **parallel BNK ∥ Testing** after the Cluster phase completes (3 phases, not a 4th VPC-only phase); **create OR reuse cluster** (skip the Cluster phase via the existing `create_roks_cluster=false` + cluster-outputs handoff); a new **`roksbnkctl testing up/down`** phase command (architect resolves `testing` vs the existing `test`/`test hosts` probe group); **teardown** = `bnk down` ∥ `testing down` leave the cluster, bare `down` destroys BNK ∥ Testing then Cluster (one composite confirm), `cluster down` refuses while BNK/Testing exist; `live-verify-high-issues` applies.

| Role | Scope |
|---|---|
| **Architect** | The three-state model + the BNK-state migration decision (state dirs, module→phase ownership — esp. the cluster VPC + the shared jumphost SSH key, the `cluster-outputs.json` fields Testing/BNK need); the `testing-phase-override` + the per-phase presence/shape model (replacing the 4-shape enum); the parallelism (up) + teardown-ordering + concurrent-stderr design; the CLI naming (`testing` vs `test`); the lifecycle book chapter + migration note. No Go. |
| **Staff** | Split the trial phase into BNK + Testing states; the `testing-phase-override.tfvars` writer + the cluster/bnk override updates (jumphosts leave the cluster phase); expand `DetectShape` to per-phase presence; `RunTestingUp`/`Down` + the **errgroup parallel BNK∥Testing dispatch** in `RunUp`/`RunDown`; the new `roksbnkctl testing` CLI group; `bnk` → BNK-only; the `cluster down` guard; reuse-existing-cluster; the migration. No Sprint 27 module-body changes. |
| **Validator** | Hermetic: presence/shape model, byte-exact override generation, dispatch-decision table, parallel-dispatch ordering + errgroup error propagation, the cluster-down guard. Gated-live `scripts/e2e-three-phase.sh`: parallel up + `bnk down`-leaves-testing (and inverse) + cluster-down guard + reuse-existing-cluster + migration. |
| **Tech-writer** (light, runs after) | Drift sweep: the new `testing` command + three-phase lifecycle chapter vs the binary; the **`testing` vs `test` disambiguation**; migration note; user-facing CHANGELOG. GREEN/RED verdict. |

`live-verify-high-issues` applies — cluster-mutating. The integrator runs the gated-live parallel-up + independent-down verify (and reuse-existing-cluster + migration) before merging. Hermetic dispatch/presence/override tests + the gated-live driver are the closure inputs.

Version at cut: integrator-owned, after Sprint 27 ships and this branch proves out. Significant orchestration re-architecture (new state, parallel exec, new phase command) → likely a minor bump. No PRD — integrator architecture request from the Sprint 27 live verify. Stacks on `sprint27-bnk-native-k8s`; lands after 27 merges.

Sprint launch: `prompts/sprint28/` scaffolded alongside this block on the `sprint28-three-phase-split` branch. Architect dispatched FIRST (the state/presence/override design is the blocking input to staff); then staff; then validator after staff lands; tech-writer after integration. See `prompts/sprint28/README.md`. **Not merged until the integrator's live verify is GREEN and Sprint 27 has merged.**

---

## Sprint 27 — terraform-native BNK phase: retire null_resource/curl/sleep via helm_release + alekc/kubectl (draft, on feature branch `sprint27-bnk-native-k8s`)

_Surfaced 2026-06-04 as an integrator architecture request. The BNK phase is, today, mostly terraform driving the cluster through `null_resource` + `local-exec` running `helm` and **raw `curl` server-side-apply**, with readiness gated by **~210s of static `time_sleep`** (cert 30 + FLO SCC 30 + FLO pods 60 + CNE CRD 30 + CNE SCC 30 + License CRD 30) plus helm `--wait` slack and `curl` retry loops — the FLO module alone is ~1,200 lines of this, and nothing watches real status (it's "apply, sleep, hope")._

**The fix is terraform-native** — terraform stays the state keeper; the brittle parts move to proper providers. An architect decision spike (rounds 1 & 2, recorded in `issues/issue_sprint27_architect.md`; the round-2 License confirmation came from pulling the FAR `f5-bigip-k8s-manifest` CRDs) returned **FINAL VERDICT: GO** on the `alekc/kubectl` provider. So: chart installs → **`helm_release`** (`wait = true`); the `curl`-applied custom resources → **`alekc/kubectl` `kubectl_manifest` + `wait_for`** (CRs as real terraform resources, in state, watching `.status` to ready — crucially WITHOUT the plan-time-CRD requirement that breaks hashicorp `kubernetes_manifest`, the reason the originals used `curl`); namespaces + secrets → the `kubernetes` provider. **No Go reconciler, no `internal/bnk`, no custom provider.** _(The original framing — a native Go watch-reconciler in `internal/bnk` — was set aside once the spike proved `wait_for` covers apply+state+delete+status-wait for every BNK CR.)_

**Primary goal: SPEED.** Make a new BNK-version install deploy + test in a tight loop. The ~210s of fixed `time_sleep` dies (`wait_for` + `helm_release wait=true` are real-readiness); terraform parallelizes independent resources; and **fast re-deploy comes free** — bump the version and `terraform plan` diffs only the changed CNEInstance spec + helm versions. The validator's gated-live benchmark must show the kubectl path **materially faster** than the legacy-curl baseline.

Confirmed ready-signals (spike): **CNEInstance** (`k8s.f5.com/v1`) `wait_for { condition { type="Available" status="True" } }`; **License** (`k8s.f5net.com/v1`) `wait_for { field { key="status.state" value="Verification Complete" } }` (literal confirmed live by the validator; `status.conditions[]` is the fallback matcher). Locked: keep IBM IAM trusted-profile + COS reads in terraform; legacy `curl` modules stay intact behind a `bnk_cr_mode = "kubectl"|"legacy_curl"` install-mode flag as the benchmark baseline; `live-verify-high-issues` applies.

| Role | Scope |
|---|---|
| **Architect** | The terraform module-restructure design (install-vs-CR resource table, the `depends_on` graph, the install-mode-flag structure, `required_providers` + `alekc/kubectl` air-gap note); a conservative-`depends_on` review so terraform parallelizes independents; the BNK-phase book chapter + a "why we retired the BNK curl/sleep" concept note. Ready-signals already confirmed (the spike). No Go, no HCL implementation. |
| **Staff** | All terraform HCL: `helm_release` install layer + `kubernetes_namespace`/`kubernetes_secret` prereqs; `kubectl_manifest` + `wait_for` CR layer (CNEInstance/License/issuers/NADs/SCC/Job — specs reused verbatim in `yaml_body`); `alekc/kubectl` in `required_providers`; the install-mode flag gating legacy vs new. Plus the small roksbnkctl Go change to render the toggle (`internal/tf/vars.go`, `internal/cli/bnk_phase.go` `--legacy-bnk`) + optional light `bnk status`. No Go reconciler / custom provider. |
| **Validator** | Hermetic terraform checks (`fmt`/`validate`/`init` resolves `alekc/kubectl`; no-`time_sleep`/no-`curl` assertion on the kubectl path; legacy baseline intact; render-toggle Go test). Gated-live `scripts/e2e-bnk-native.sh`: correctness + **the License `status.state` live-confirm** + **the speed benchmark (kubectl vs legacy wall-clock — kubectl must be materially faster, asserted)** + fast-re-deploy + clean teardown. |
| **Tech-writer** (light, runs after) | Drift sweep: install-mode surface vs binary; BNK-phase chapter reconciled to the terraform-native reality (`kubectl_manifest`+`wait_for`, CRs in state); speed numbers trace to the validator's measured kubectl-vs-legacy benchmark; sweep stale `curl`/`time_sleep` BNK prose; document the new `alekc/kubectl` provider dependency + air-gap caveat; user-facing CHANGELOG. GREEN/RED verdict. |

`live-verify-high-issues` applies — cluster-mutating. The integrator runs the gated-live correctness + speed benchmark (kubectl vs legacy baseline) + the License-literal confirm before merging the feature branch. The hermetic terraform checks plus the gated-live driver are the closure inputs.

Version at cut: integrator-owned, **after** the feature branch proves out. New capability behind the install-mode flag → likely a minor bump; deleting the legacy `curl` modules (once kubectl is the default) is a separate later sprint. No PRD — integrator architecture request, resolved by the architect spike. Much smaller than first framed (the Go reconciler + custom provider both fell away) — it's a terraform-modules change plus a small render/flag Go change.

Sprint launch: `prompts/sprint27/` scaffolded + the architect spike (GO) on the `sprint27-bnk-native-k8s` branch. Architect dispatched FIRST (the module-restructure design is the blocking input to staff); then staff; then validator after staff lands; tech-writer after integration. See `prompts/sprint27/README.md`. **Branch is not merged to main until the integrator is confident in the workings** (explicit integrator constraint).

---

## Sprint 26 — prefix-driven generated `terraform.tfvars` + `init` interview rewrite (draft, not yet dispatched)

_Surfaced 2026-06-04 as an integrator feature request. `roksbnkctl init` collects a few values into `config.yaml`, and `internal/tf/vars.go:RenderTFVars` renders a **sparse** `terraform.tfvars` at `up`-time — emitting only the handful of fields the operator set and letting every resource *name* fall through to the upstream module defaults (`tf-cluster-vpc`, `tf-openshift-cluster`, `tf-tgw`, …). Because every workspace inherits the **same** default names, two workspaces that both create infra collide at the IBM Cloud account level — the same collision class that stranded `canada-roks-*` in the 2026-05-28 incident behind `issues/issue_sprint25_staff.md`. Sprint 25 *detects* orphans after the fact; this sprint *prevents* the collisions by making a single **workspace prefix** the base for every account-scoped name, deriving + length-validating all of them, and rendering a complete `terraform.tfvars` that drives a full `up`. There is also no name length validation today — an over-long name is only rejected by IBM Cloud mid-`apply`._

User-confirmed design decisions (do not relitigate): **(1)** `config.yaml` stays authoritative — store `prefix` + per-resource toggles + existing-resource refs there, derive/validate/render the full name set at every `up`/`plan`/`apply` (deterministic from the prefix, so regeneration is safe). **(2)** Kubernetes namespaces stay at fixed conventional values (`cert-manager`, `f5-bnk`, `f5-utils`) — only account-scoped IBM Cloud infra names are prefixed. **(3)** Length overflow rejects + re-prompts (no silent truncation); non-TTY → hard error. Backward compatible — empty-`prefix` legacy configs keep the old sparse render. A supplied `--var-file` still overrides any generated name via the Sprint 19 `terraform.tfvars.user` layering.

| Role | Scope |
|---|---|
| **Architect** Issue 1 | The **design inputs staff codes against** + the operator-facing prose. (a) The compact suffix scheme (cluster name == prefix so the prefix-length limit equals the tightest resource limit; `<prefix>-cluster-vpc` / `-tgw` / `-registry-cos` / `-client-vpc` / `-jh-tgw` / `-jh`). (b) The authoritative per-resource-type length/charset **constraint table** with **cited** IBM Cloud limits (ROKS cluster ~35, IS-resources 63, COS 180) — verified against the IBM Terraform provider's validators; this is a blocking input — staff must not invent limits. (c) Book authoring: rewrite the init chapter (prefix prompt, create toggles, existing-resource discovery, printed name plan), add a "resource naming & collision avoidance" concept section, document `prefix` + the `resources:` block in the configuration reference, update the `terraform.tfvars.example` header. No Go. |
| **Staff** Issue 1 | All the Go. New `internal/naming` package (`Derive`/`ValidatePrefix`/`SanitizeToPrefix` + the constraint table from architect's spec). Additive `config.Workspace` fields (`Prefix` + a `ResourcesCfg`/`ResourceToggle` block). `internal/tf/vars.go:RenderTFVars` full render when `Prefix != ""` (every derived name + `create_*` toggle emitted exactly once; legacy sparse path preserved for empty prefix); delete the **vestigial** `RenderTFVarsWithClusterOutputs`/`WriteTFVarsWithClusterOutputs` (no live callers — live handoff is the separate `bnk-phase-override.tfvars`). Rewrite the `init.go` interview: prefix loop (validate + re-prompt; non-TTY hard error), per-resource create toggles, existing-resource discovery prompts on declined dependencies, print the resolved name plan. `--var-file` path derives a sanitized prefix non-interactively + defaults `Resources` to all-create. No edits to credential/TF-source/API-key code or the phase-handoff override files. |
| **Validator** Issues 1 + 2 | Issue 1 (hermetic): `internal/naming/naming_test.go` (derive/validate/overflow-message/zone-suffix/idempotent-sanitize); `internal/tf/vars_test.go` updated for the full prefix render (every var present + single-occurrence, no `tf-*` leak) plus a preserved legacy sparse case; delete the dead `secondphase_handoff_test.go`; `internal/cli/init_prefix_test.go` (prefix persistence, non-TTY invalid-prefix error, existing-resource capture) + a minimal `Prefix` expectation update to the Sprint 19 `init_var_file*_test.go`. Issue 2 (gated-live): `scripts/e2e-init-prefix.sh` — `init`→`plan` asserts the full generated name set in the rendered tfvars for two distinct prefixes, proves disjoint names (no-collision), and proves `terraform.tfvars.user` override wins. Gated on `IBMCLOUD_API_KEY`, honors `DRY_RUN`, redacts secrets. |
| **Tech-writer** Issue 1 (light, runs after) | Drift sweep over the integrated tree: regenerate the command reference so `init --help` matches the binary; confirm the configuration reference's `prefix` + `resources:` schema matches the YAML a fresh `init` writes; re-capture the init-chapter interview transcript byte-for-byte against the built binary (drop the illustrative placeholder); sweep out stale "`tf-*` default names" / "supply every name via `--var-file`" prose; confirm the CHANGELOG bullet is user-facing and notes backward compatibility. GREEN/RED launch verdict ends the closure. |

`live-verify-high-issues` applies — the rendered-`terraform.tfvars` change is `up`-affecting. Integrator runs `roksbnkctl init -w <ws>` then bare `plan` (and optionally a dual-workspace no-collision `up`) and confirms generated names land with no `tf-*` defaults, before flipping staff Issue 1 to `resolved`. The hermetic class plus the gated-live driver are the closure inputs.

Version at cut: integrator-owned. New feature changing default `init` + render behavior but backward-compatible (legacy empty-`prefix` configs unchanged) → expected `v1.8.0` under strict SemVer; integrator confirms. No PRD — integrator feature request closing the recurring naming-collision friction (the prevention complement to Sprint 25's detection). Pairs with — but is independent of — Sprint 25 (`doctor --orphan-sweep`); once names are config-recorded the orphan-sweep can read them from the rendered tfvars instead of re-deriving a formula.

Sprint launch: `prompts/sprint26/` scaffolded alongside this PLAN.md update. Architect + staff + validator dispatched in parallel (architect's constraint table is a blocking input to staff — dispatch together, but staff codes the table values once architect pins them); tech-writer runs after they integrate. See `prompts/sprint26/README.md`.

---

## Sprint 24 — `roksbnkctl test hosts {list,add,remove,clear}` CLI surface (draft, not yet dispatched)

_Filed 2026-05-27 as a low-priority CLI ergonomic during the demo.sh re-verify of the Sprint 22 fixes. The presenter asked how to add test hosts so `roksbnkctl test dns` and `roksbnkctl test connectivity` would work against the canada-roks workspace. There is no CLI for it — the only path today is hand-editing `~/.roksbnkctl/<workspace>/config.yaml` to add a `test.connectivity.extra_hosts` list. The error message the test commands themselves emit when no hosts are configured (`internal/cli/test.go:803`) literally points the operator at YAML editing. Other workspace-config surfaces with comparable shape (`targets`, `cluster register`) have first-class CLI; test hosts don't. Originally filed as a future-sprint placeholder behind Sprint 23 (the resource-damage class); promoted into the `v1.7.1` release scope on 2026-05-27 per the integrator's decision to bundle the UX gap alongside the safety fixes._

Scope is tight. Only the `test.connectivity.extra_hosts` slice gets first-class CLI — the `test.dns.{default_target,resolvers}` and `test.throughput.*` config fields have working flag-driven equivalents (`--target`, `--server`, `--duration`, etc.) and adding CLI for them is a separate scope decision deferred to a later sprint.

| Role | Scope |
|---|---|
| **Staff** Issue 1 | New `roksbnkctl test hosts` command group with four subcommands mirroring `targets {list,add,remove,clear}` ergonomics: `list` (one URL per line; `--output json` emits the slice as a JSON array; empty list → zero bytes + exit 0), `add <url> [<url> ...]` (idempotent, no-op on already-present, validates via `url.Parse`), `remove <url> [<url> ...]` (idempotent, no-op on absent), `clear` (confirmation prompt defaults to No; `--auto` skips). Each subcommand declares `Args: cobra.NoArgs` (`list`, `clear`) or `cobra.MinimumNArgs(1)` (`add`, `remove`) per the Sprint 21 strictness contract. Persistence routes through the existing `internal/config` workspace marshaller (same path `targets add` uses for `TargetCfg`). Additive `internal/cli/test_hosts.go` + `internal/cli/test_hosts_test.go`; no edits to pre-existing `_test.go`. Update `internal/cli/test.go:803`'s error message to point at the new CLI (`add via roksbnkctl test hosts add <url>`) instead of the YAML path. |
| **Architect** Issue 1 | One short subsection in `book/src/12-running-tests.md` (or the chapter where the test commands are documented — verify via `book/src/SUMMARY.md`) covering the four new subcommands with a worked example. Cross-link from the `test connectivity` / `test dns` chapter sections so an operator hitting the "no hosts configured" error finds the CLI path immediately. Same shape as the existing `targets` documentation. |
| **Validator** Issue 1 | Hermetic test in `internal/cli/test_hosts_test.go` (new additive file) covering each subcommand against a temp-`ROKSBNKCTL_HOME` workspace: (a) `add <url>` against empty config; (b) `add <url>` already-present → no-op + log; (c) `remove <url>` absent → no-op + log; (d) `list` on empty config → zero bytes + exit 0; (e) `list --output json` → `[]`; (f) `clear` prompt defaults to No; (g) `clear --auto` skips prompt. Comment-preservation property optional — if the existing marshaller doesn't preserve comments, document the limitation in the closure rather than blocking the sprint. |
| **Tech-writer** Issue 1 (light, runs after; **covers Sprint 23 round-2 + Sprint 24 in one pass**) | Drift sweep over BOTH this sprint's deliverables AND the Sprint 23 round-2 work that landed in commit `e4b7f7b` (the tech-writer's round-1 GREEN verdict at `158ae55` predated that). Per-finding fields use `**Verdict**:` (not `**Status**:`) per the `a2b78da` convention. Five sweeps minimum: (1) new `test hosts` CLI surface in `book/src/12-running-tests.md` matches binary output byte-for-byte; (2) `internal/cli/test.go:803`'s error message points at the new CLI; (3) PRD 06 §"Design" wording matches the post-round-2 cert_manager-narrowed criterion (round-1 architect work + round-2 staff comment-update); (4) chapter 31 building-from-source drift sweep accuracy against the post-Sprint-22 + Sprint-23-round-2 state; (5) bnk-phase-override header comment + content match validator's regression test. GREEN/RED launch verdict notes `v1.7.1` is now unblocked (Sprint 22 + 23 round-1 + 23 round-2 + 24 ship together). |

`live-verify-high-issues` does NOT apply — this is a UX feature, not a resource-affecting fix. The hermetic test class is sufficient closure for the staff Issue. The Sprint 23 round-2 work IS subject to the live-verify rule, and that closure is already documented in the Sprint 23 PLAN.md section above (round-3 live verify GREEN on 2026-05-27).

Version at cut: `v1.7.1` patch — bundled release covering Sprint 22 (down-prompt + DetectShape), Sprint 23 (round-1 + round-2 phase-separation hardening, both live-verified GREEN), and Sprint 24 (test hosts CLI surface). No breaking surface. CHANGELOG entry references all three sprints; the test hosts CLI is the most operator-visible addition.

Sprint launch: prompts/sprint24/ scaffolded alongside this PLAN.md update. Validator + architect + staff dispatched in parallel when integrator says go; tech-writer runs after they integrate (covering both Sprint 23 round-2 and Sprint 24 in one pass per the table above). See `prompts/sprint24/README.md`.

---

## Sprint 23 — Phase-separation leak fix — `bnk-phase-override.tfvars` coverage gap (closed 2026-05-27 — live-verified GREEN with residual)

_Surfaced 2026-05-27 during the Sprint 22 down-prompt + DetectShape live verify against canada-roks. Pre-Sprint 22 the DetectShape false-positive masked this leak by routing operators through the LegacySingle dispatch; post-fix it's a real `bnk down` resource-damage hazard. A normal post-`up` Split trial state carries 40 data-source refreshes (expected — the BNK trial modules read cluster info via data lookups) plus **two managed cluster-shared resources** that the `bnk-phase-override.tfvars` produced by `writeAndInitSecondPhase` fails to suppress: `module.testing.tls_private_key.jumphost_shared_key` and `module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`. A `roksbnkctl bnk down` against this state would target ALL trial-state managed resources for destruction — including the registry COS instance (cluster loses image storage) and the jumphost shared key (divergence from the cluster-phase key; SSH targets break). Exactly the cluster-shared-resource-damage class the Sprint 8 phase split was designed to prevent. Sprint 22's `v1.7.1` tag is GATED on this — bundle both into one patch._

The header comment on `internal/orchestration/second_phase_reuse.go` claims the override turns "ALL cluster-shared creation off." Live evidence proves that claim inaccurate for at least two resources. The investigation in `issues/issue_sprint23_staff.md` already establishes the architecture facts:

- `terraform/modules/testing/main.tf:279` declares `resource "tls_private_key" "jumphost_shared_key"` with NO `count` / `for_each` gate — every `module.testing` apply creates a fresh key, including the second-phase apply that should be a pure existing-cluster consumer.
- `terraform/modules/roks_cluster/modules/cluster/main.tf:232` declares `resource "ibm_resource_instance" "cos_instance"` with `count = var.create_cluster && var.create_cos_instance ? 1 : 0`. The outer `module "cluster"` block at `terraform/modules/roks_cluster/main.tf:26-28` passes `create_cluster = var.create_roks_cluster` and `create_cos_instance = var.create_roks_registry_cos_instance`. The override sets `create_roks_cluster = false`, which SHOULD propagate to `count = 0`. Live trial state shows the resource IS present, so either (a) the propagation chain has a gap the issue doesn't yet identify, or (b) the override isn't reaching trial state on all paths (e.g. when `cluster-outputs.json` is missing or partially written). Staff investigation must confirm which.

Live-verify per `live-verify-high-issues` memory is REQUIRED for closure: a fresh `up` on a clean workspace, then assert the trial state file has zero managed entries under `module.roks_cluster.*` / `module.testing.*` prefixes (`jq` one-liner in the issue file).

| Role | Scope |
|---|---|
| **Staff** Issue 1 | Investigation-first read of the HCL count-gates + the override propagation. Then fix shape (likely: add `count` gate to `tls_private_key.jumphost_shared_key` driven by an already-overridden testing toggle; confirm or add a flag for `cos_instance` propagation). Update `bnk-phase-override.tfvars` content in `internal/orchestration/second_phase_reuse.go` if a new flag is needed. Make the file's header comment accurate ("ALL cluster-shared creation off" must either become true or get qualified). Add a regression-test assertion in `internal/orchestration/second_phase_reuse_test.go` for the new override content. |
| **Architect** Issue 1 | PRD 06 §"Design" wording update for the narrowed DetectShape criterion (Sprint 22 staff-flagged follow-up — line ~120 of `docs/PRD/06-CLUSTER-TRIAL-PHASE-SPLIT.md`, "trial state contains cluster-phase modules" needs the "managed `ibm_container_vpc_cluster`" qualifier). |
| **Architect** Issue 2 (light) | `book/src/31-building-from-source.md` chapter 31 drift sweep (Sprint 22 architect-flagged follow-up): the `tools/docker/` tree listing omits `mdbook/` entirely; the `make` examples predate the mdbook target; the trigger description is wrong. Reconcile against the post-Sprint-22 state. |
| **Validator** Issue 1 | Hermetic regression test extending `internal/orchestration/second_phase_reuse_test.go` to pin the new override content (whatever new tfvars staff lands). Also document — but do NOT execute — the live-verify recipe: `jq '.resources[] \| select(.mode=="managed" and (.module \| startswith("module.roks_cluster") or startswith("module.testing")))' ~/.roksbnkctl/<ws>/state/terraform.tfstate` must return empty after a fresh `up` against any workspace. The integrator runs the live verify via `!`. |
| **Tech-writer** Issue 1 (light, runs after) | Drift sweep covering staff + architect + validator deliverables. Per-finding fields use `**Verdict**:` (not `**Status**:`) per the `a2b78da` convention. GREEN/RED launch verdict explicitly notes that `v1.7.1` is now unblocked (Sprint 22 + 23 ship together). |

`live-verify-high-issues` APPLIES — closure is gated on a live verify (`!` invocation by the integrator). Hermetic tests are necessary but not sufficient.

Version at cut: `v1.7.1` patch — Sprint 22's down-prompt + DetectShape fixes + Sprint 23's phase-separation fix ship together. No breaking surface; both are bug fixes. CHANGELOG entry references both sprints.

Sprint launch: when ready, integrator dispatches staff + architect + validator in parallel via `Agent`; aggregates + commits + runs gates; then dispatches tech-writer over the integrated tree.

Related candidates (NOT in this sprint's scope, surfaced for the integrator's awareness when planning Sprint 24+):

- `Makefile:107` recovery hint refinement (add `docker pull` alternative alongside the local build) — architect-flagged during Sprint 22, lower-priority Makefile ergonomic.
- `roksbnkctl test hosts {list,add,remove,clear}` CLI gap — already filed as `issues/issue_sprint24_staff.md`, lower priority than this sprint.
- Audit other state-shape heuristics for the same "any-resource-under-prefix" vs. "managed marker type" confusion pattern — staff-flagged during Sprint 22, low priority.

### Closure (2026-05-27 — live-verified GREEN with residual)

Sprint 23 took two rounds. Round 1 (three-way integration commit `f8fac2d` + tech-writer GREEN at `158ae55`) closed the two specific resources cited in the original issue file (`tls_private_key.jumphost_shared_key` count-gated; `create_roks_registry_cos_instance = false` added to the bnk-phase override). Hermetic tests passed, tech-writer's drift sweep verdict was GREEN.

The 2026-05-27 live verify against canada-roks (per the `live-verify-high-issues` memory) caught what the hermetic tests could not — three additional defects in sequence:

1. **Round-2 issue 1**: the round-1 count gate on `tls_private_key.jumphost_shared_key` was applied to the resource + the outputs.tf consumers, but **missed** the five `jumphost_user_data` locals at `terraform/modules/testing/main.tf:39,40,188,193,194`. Locals are unconditionally evaluated at plan time, so the `[0]` indexing on an empty tuple produced "Invalid index" — trial-phase plan failed. Fixed by wrapping each ref in `length(...) > 0 ? ... : ""` (same pattern the round-1 staff applied to `outputs.tf`).

2. **Round-2 issue 2**: the original issue file cited only two leak resources, but live verify exposed a **broader leak class** — six managed cluster-phase entries in trial state after the round-1 fix. Four were catastrophic: `module.cert_manager.module.cert_manager.null_resource.{cert_manager,cert_manager_namespace}`, `time_sleep.cert_manager_ready`, and `module.roks_cluster.module.cluster.ibm_is_security_group_rule.cluster_sg_inbound_all`. The first three carried destroy provisioners that run `kubectl delete namespace cert-manager` on `bnk down` — wiping cert_manager + every cert it issued. Root cause: the outer `terraform/modules/cert_manager/main.tf:35` hardcoded `enabled = true`; the inner cert-manager submodule already had count-gate machinery (`count = var.enabled ? 1 : 0`) but it was never exposed. Fix: new `var.deploy_cert_manager` (default `true`) plumbed through outer wrapper + root + bnk-phase override forces it `false`; the SG rule gained `count = var.create_cluster ? 1 : 0`.

3. **Round-2 issue 3**: after rounds 1+2 landed, trial-phase plan failed with "Invalid template interpolation value" at `flo/modules/flo/main.tf:364-365`. The inner cert-manager module's `namespace` output returns `null` when `enabled = false`; flo's `null_resource.ca_certificate` interpolates that value into a curl command, and null-in-template is an HCL error. Fix: `terraform/main.tf:86` switched from `cert_manager_namespace = module.cert_manager.cert_manager_namespace` to `cert_manager_namespace = var.cert_manager_namespace` (the root variable, always defined). flo now reads the canonical namespace name directly without depending on whether trial-phase manages cert_manager.

Round-2 shipped as commit `e4b7f7b`. Round-3 live verify against canada-roks: `up --auto` exit 0, trial-phase plan/apply succeeded, **jq leak filter returned 2 entries** (down from 6): `module.cert_manager.null_resource.roks_cluster_gate` and `module.testing.null_resource.roks_cluster_gate`. Both are inert `null_resource`s with `triggers = { dep = "direct-apply" }` and NO destroy provisioner. Destroying them via `bnk down` removes them from state only — zero cloud impact, zero cluster impact.

**Operational acceptance criterion** ("no resource-damage hazard on `bnk down`") is MET. **Strict criterion** ("zero managed cluster-phase entries in trial state") is NOT met — the 2 inert entries remain. Closure paragraph in `issues/issue_sprint23_staff.md` documents both criteria honestly. A future cleanup adding `count = var.create_roks_cluster ? 1 : 0` to the two bootstrap gates (in `cert_manager/providers.tf:33` and `testing/providers.tf:18`) would close the residual; not safety-critical, deferred.

**Tech-writer round-2 re-sweep**: the round-1 tech-writer's GREEN verdict (`158ae55`) was based on the round-1 integrated tree. Round-2's nine-file diff (commit `e4b7f7b`) landed AFTER. Rather than re-dispatching tech-writer for Sprint 23 round-2 in isolation, the re-sweep is **folded into Sprint 24's tech-writer scope** — one pass covers both. (Sprint 24 is being rolled into the same `v1.7.1` cut per the integrator's 2026-05-27 decision; bundling the tech-writer pass minimizes ceremony cost.)

**Lessons banked for the integrator's memory + future sprints**: three rounds of live verify caught what the hermetic test class could not. The Sprint 23 regression test pins the override CONTENT but never runs `terraform plan` against the integrated HCL + state. A small `terraform plan -refresh=false` sanity gate in CI — running against a fixture state with the override layered — would catch this entire class hermetically. Filed as Sprint 23 round-2 future candidate #5 in the staff issue's closure section.

Version at cut: still `v1.7.1` patch, now bundling Sprint 22 down-prompt + DetectShape + Sprint 23 round-1 + Sprint 23 round-2 phase-separation hardening + Sprint 24 test-hosts CLI gap (per the 2026-05-27 scope-expansion decision). CHANGELOG entry references all three sprints.

---

## Sprint 22 — `mdbook` CI matrix + `down` composite UX + `DetectShape` correctness (in flight; staff scope shipped 2026-05-27, validator/architect/tech-writer pending)

_Originally drafted 2026-05-21 as a CI-only sprint (one workflow-file edit to fold `mdbook` into the `tools-images.yml` matrix alongside `ibmcloud` and `iperf3`). Mid-flight on 2026-05-27, a presenter demo.sh re-verify cycle surfaced two staff-class bugs in the same problem domain (the cluster/trial phase-split dispatch, PRD 06) that the integrator fixed in-conversation and folded into this sprint rather than spawning a parallel sprint. The original CI scope remains; two staff Issues now sit alongside it as the sprint's primary deliverables. Staff scope shipped; validator/architect/tech-writer scopes have not been dispatched yet — release tag is gated on Sprint 23 (phase-separation leak surfaced during the same verify cycle), so this sprint's closure paragraph waits on Sprint 23._

### Why the scope grew

The integrator's 2026-05-27 demo.sh dry-run hit two operator-visible failures in sequence:

1. `roksbnkctl down` against a `cluster + bnk` Split-shape workspace appeared to destroy only the trial phase (operators had to manually run `roksbnkctl cluster down` after to finish the teardown). Root cause: `RunDown`'s Split-branch dispatcher prompted twice — once in `RunTrialDown`, once in `runClusterDown` — and the second prompt defaulted to No. Operators saying "yes" to the first prompt and walking away (or assuming one Yes covered both) hit Enter on the second and got "aborted" — bnk gone, cluster still running, no error.
2. After fixing #1, the verify cycle hit a second failure: `roksbnkctl bnk down` on a freshly-applied Split workspace errored with `"this workspace is legacy single-state"`. Root cause: `DetectShape`'s `trialStateHasClusterModules` heuristic matched any resource under `module.roks_cluster` / `module.cert_manager` / `module.testing` prefixes without filtering on `mode` or `type`. Post-`up` trial states legitimately carry data-source refreshes under those prefixes (the BNK trial modules read cluster info via data lookups) plus two stray managed resources (`tls_private_key.jumphost_shared_key`, `ibm_resource_instance.cos_instance`) for reasons being tracked under `issue_sprint23_staff.md`. The heuristic mis-classified these as LegacySingle.

Both fixes shipped during the same conversation that surfaced them; both passed unit tests at integration; the DetectShape fix's live verify is gated on Sprint 23's phase-separation work landing first (per `live-verify-high-issues` memory and the integrator's release-hold decision).

### Per-role scope

| Role | Scope |
|---|---|
| **Staff** Issue 1 (shipped, `cbb9c1b`) | `DetectShape` correctness — tighten `trialStateHasClusterModules` in `internal/config/tfstate.go` to require `mode == "managed"` AND `type == "ibm_container_vpc_cluster"` AND a `clusterPhaseModules` prefix match. The ROKS cluster resource is the unambiguous v1.0.x marker; data sources and stray managed resources of other types under cluster-phase prefixes are benign. New fixture `tfstate_split_data_in_trial.json`; 2 new helper tests pinning the mode + type filters; 3 existing helper tests updated to include the now-required fields. Full write-up in `issues/issue_sprint22_staff.md`. |
| **Staff** Issue 2 (shipped, `18415eb`) | `roksbnkctl down` composite UX — `RunDown`'s Split branch in `internal/orchestration/lifecycle.go` now takes ONE up-front confirmation that names both phases, then flips `in.Auto = true` so `RunTrialDown` + `runClusterDown` leaves don't re-prompt. The cli adapter's `RunClusterDown` closure mirrors `in.Auto` onto `flagAuto` for the cluster-down call's duration (cluster_phase.go stayed in cli per Sprint 16 phase-1b scope and reads `flagAuto`, not `in.Auto`). Pre-fix, operators saying Yes to the first prompt and missing the second walked away with bnk gone and cluster still running. |
| **Validator** Issue 1 (pending) | Add `mdbook` to the `strategy.matrix.image` list in `.github/workflows/tools-images.yml` (alongside the existing `ibmcloud` and `iperf3` entries). Verify the existing `Build and push (tag)` / `Build and push (main)` / `Build and push (dispatch)` steps work for `mdbook` with `context: .` + `file: ./tools/docker/${{ matrix.image }}/Dockerfile`. The mdbook Dockerfile (`tools/docker/mdbook/Dockerfile`) doesn't reference repo-root paths so the broader context is harmless (same as `iperf3`'s situation per the existing workflow comment). Trigger a manual `workflow_dispatch` run after merge to verify the matrix expansion before relying on it for the next main-push or tag-push. |
| **Architect** Issue 1 (light, pending) | One short note in `CONTRIBUTING.md` (or wherever the docker-image-publishing flow is documented — verify in `docs/PRD/03-*` or the contributor docs) confirming `mdbook` is now CI-managed; remove any "remember to manually push the mdbook image" callouts if they exist. |
| **Tech-writer** Issue 1 (light, runs after; pending) | Drift sweep covering BOTH the CI work and the staff fixes: (a) confirm the new mdbook matrix entry produced the expected `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev` push on the post-integration main push, spot-check the manifest digest matches what the workflow built; (b) verify `book/src/11-tearing-down.md` reflects the new combined down-prompt copy (already updated in `18415eb` — confirm no other chapters drifted); (c) GREEN/RED launch verdict. |

`live-verify-high-issues` partially applies: the CI matrix work is verifiable by a single `workflow_dispatch` run, but the DetectShape staff fix is high-sev and needs an `up` → `down` live cycle on a real workspace. That live verify is **gated on Sprint 23** (the phase-separation leak — `bnk down` against the leaked managed resources would orphan cluster-shared infrastructure). Don't dispatch the live verify until Sprint 23 lands.

Version at cut: gated on Sprint 23. The two staff fixes are bug fixes (no breaking surface), so semver suggests patch (`v1.7.1`), but the tag waits on Sprint 23's phase-separation fix shipping with it. Combined release narrative: "down dispatch correctness + phase-separation hardening."

Sprint launch: prompts/sprint22/ scaffold drafted 2026-05-27 alongside this PLAN.md update. The staff prompt is closure-only (no active work — both Issues shipped pre-dispatch, mirroring inversely Sprint 20's "no architect deliverables (release-tooling-only)" precedent). Validator + architect dispatched in parallel when integrator says go; tech-writer runs after they integrate. See `prompts/sprint22/README.md`.

Related candidate (NOT in this sprint's scope, surfaced for the integrator's awareness when planning Sprint 25 or later): commit `8544ab0`'s message flags that `make staticcheck` should be in the sprint-standard gate set — 7 consecutive CI failures on `main` (Sprint 18 round-4 through Sprint 21) went unnoticed because staticcheck only runs in CI, not in the local gate list each role's closure verifies against. Folding that into a future sprint would close the same blind-spot class one level deeper. Sprint 23 (phase-separation) and Sprint 24 (test hosts CLI gap) are also already filed as staff placeholders — Sprint 23 release-gates, Sprint 24 is lower priority.

---

## Sprint 21 — Cobra/pflag argv-parser strictness — reject stuck-together short-flag-values + `cobra.NoArgs` audit (first regular work sprint post-`v1.6.4`, runs in parallel with Sprint 20)

_Drafted 2026-05-21 from the integrator's live-use observation. The operator typed `roksbnkctl init -ws canada-roks --var-file ./terraform.tfvars` intending `--workspace canada-roks`. Cobra/pflag's stuck-together-shorthand parser interpreted `-ws` as `-w s`; the positional `canada-roks` was silently dropped because `init` didn't declare `Args: cobra.NoArgs`. A workspace named `s` was created carrying the correct `canada-roks` cluster identity inside it; a follow-on bare `cluster up` then resolved to `current_workspace: default` and produced a 25-resource-destroy / 41-resource-add plan against real cloud state. The plan was caught at review, but the user explicitly signalled this is the priority class to close: "I don't want to see any more foolish syntax errors, like I made, messing up a resource heavy task." Captured as the `argv-strictness-prevents-resource-damage` integrator memory._

Tiny scope, mostly argv preflight; partitioned the regular way:

| Role | Scope |
|---|---|
| **Staff** Issue 1 | (a) Argv preflight in `cmd/roksbnkctl/main.go` that walks the cobra tree at process start, collects value-requiring short flags, scans `os.Args[1:]` for stuck-together short-flag-value tokens (`-X<suffix>` where `X` is value-requiring and `<suffix>[0] != '='`), exits non-zero with an actionable error before `Execute()`. (b) Audit every cobra command under `internal/cli/`; add `Args: cobra.NoArgs` to every command that doesn't take positionals. Commands that DO take positionals (`ws delete`, `cos object get`, `cos bucket get`, `cluster register`, etc.) keep their existing `Args:`. |
| **Architect** Issue 1 | One short paragraph in the first-run / command-line chapter naming the strictness contract; quote the binary's verbatim error text for `-ws foo`. Regen `book/src/27-command-reference.md` if any `Args:` change affects per-command Usage. Cross-chapter sweep for stuck-together-shorthand examples. |
| **Validator** Issue 1 | Hermetic test under `internal/cli/` covering: (a) stuck-together short-flag-value rejected with actionable error; (b) canonical forms (space + equals) accepted byte-identically; (c) `cobra.NoArgs` pins per command staff added the constraint to. Additive file only; no edits to any pre-existing `_test.go`. |
| **Tech-writer** Issue 1 (light, runs after) | Drift sweep — book paragraph matches binary verbatim output, chapter 27 regen'd, no stuck-together examples survive, CHANGELOG entry calls out the BREAKING aspect prominently. GREEN/RED launch verdict. |

`live-verify-high-issues` does NOT apply — argv shape doesn't need cloud validation; hermetic tests are sufficient closure. The whole point of this sprint is to surface typos BEFORE any cloud call.

Version at cut: integrator-owned. Behavioural change for any operator using `-fvalue` stuck-together → minor bump (`v1.7.0`) is defensible; integrator can also choose patch (`v1.6.5`) if the disruption is judged low. CHANGELOG entry must call out the change in `### Changed` (and arguably `### BREAKING`).

Sprint launch: integrator dispatches staff + architect + validator in parallel (single message, three `Agent` calls reading `prompts/sprint21/*.md`); aggregates + commits + runs gates; then dispatches tech-writer over the integrated tree.

### Closure (2026-05-21)

All four scope items landed GREEN at three-way integration commit `a3efcfd`; shipped as `v1.7.0` (minor bump — first behavioural change since `v1.6.0`'s internal consolidation; operators using `-fvalue` stuck-together shorthand must switch to `-f value` or `-f=value`). `live-verify-high-issues` does NOT apply — argv shape is parse-time, not cloud-affecting; the whole point of this sprint is to surface typos BEFORE any cloud call, so a hermetic gate is the stronger proof than a live `!` driver would be (validator AC1 pins the no-workspace-dir-created property via `os.Stat` under `t.TempDir()`, which the rejection shape categorically cannot violate).

- **Staff Issue 1** (argv preflight + `cobra.NoArgs` audit) — hermetic GREEN. Preflight at `cmd/roksbnkctl/main.go` walks the cobra tree at process start (no hand-maintained typo list), unions the value-requiring shorts via pflag's `NoOptDefVal == ""` marker (booleans skipped — `-vvv` / `-it` keep working), and rejects any `-X<suffix>` shape (where `X` is value-requiring, `<suffix>` is non-empty, `<suffix>[0] != '='`) with an actionable error naming the offending token, both acceptable shapes, both long-flag equivalents, and a "did you mean" derived from the binary's live flag set. Detection scope honours `DisableFlagParsing` passthroughs (`kubectl` / `oc` / `ibmcloud` / `terraform` / `exec` — argv after the subcommand name flows untouched to the wrapped tool) and the `--` argv sentinel. 32 commands gained `Args: cobra.NoArgs`; 14 positional-shape constraints preserved byte-identically; 5 passthroughs intentionally kept `DisableFlagParsing` without `Args:`; 11 parent-route commands without `RunE` left alone. No `RunE` body edited.
- **Architect Issue 1** (book paragraph + chapter 27 regen + cross-sweep) — paragraph at `book/src/07-quick-start.md:59–71` matches the binary's actual stderr **byte-identically** (re-captured by the tech-writer drift sweep against the landed tree: `ROKSBNKCTL_HOME=/tmp/probe-tw go run ./cmd/roksbnkctl init -ws foo` produces output line-for-line equal to the quoted text). Chapter 27 regen output byte-identical (refgen renders `Use` / `Short` / `Long` / flag-tables, not `Args:` shape, so 32 newly-pinned `NoArgs` rows produce the same markdown). Cross-chapter sweep zero hits — all candidate stuck-together matches are bool-stacking (`-it`), Go toolchain (`-ldflags`), hyphenated identifiers, or the architect's own demonstrative quote of the rejected typo.
- **Validator Issue 1** (hermetic tests) — `internal/cli/argv_strictness_test.go` shipped (+542 LOC additive, no pre-existing `_test.go` touched). 6 top-level test functions + 30 `NoArgs_Sweep` sub-cases (one per staff `NoArgs → ADDED` row) all PASS under `go test -race ./internal/cli/` (`ok ... 64.904s`). Two harness surfaces: subprocess-driven for the preflight (lives in `package main`, shared `sync.Once` build), in-process `runRootCmd` for the `cobra.NoArgs` audit.
- **Tech-writer Issue 1** (post-integration drift sweep) — GREEN verdict. 0 high findings; 2 medium (this PLAN.md closure subsection and the `v1.7.0` CHANGELOG entry — both written into the close commit alongside this paragraph); 1 low (staff closure note cites preflight helpers at lines 54/95/128/144 when the actual locations are 54/113/151/168, doc-comment drift; source unaffected; cosmetic — left as-is in commit `a3efcfd`'s history).

Version at cut: **`v1.7.0` shipped 2026-05-21**. Minor bump per SemVer for the behavioural change; CHANGELOG entry inline-prefixes the strictness bullet with `**BREAKING for any operator using stuck-together short-flag-values…**` per project precedent (`CHANGELOG.md:515` style — no separate `### BREAKING` section, which keep-a-changelog 1.1.0 doesn't define).

Lesson banked for the integrator's memory: the `argv-strictness-prevents-resource-damage` rule is now codified at the binary's argv boundary; the typo that nearly destroyed 25 IBM Cloud resources can no longer reach `RunE`. The follow-on `current_workspace` stale-resolution UX (the SECOND half of the original failure mode — `bare cluster up` resolved to `default` from a prior cycle) is **not** closed by this sprint and remains an open future concern.

---

## Sprint 20 — `make release-publish` stale-PDF hardening (first regular work sprint post-`v1.6.4`)

_Drafted 2026-05-21 immediately after the Sprint 19 v1.6.4 cut surfaced a real defect in the release pipeline: `make release-publish` uploaded a stale `book/book/pandoc/pdf/book.pdf` from the prior cycle under the v1.6.4 asset name, because the target's prereq check was "does the PDF exist?" not "is it current?". The current cycle's `book-pdf` had failed silently (missing `ulem.sty` in the docker image — that's commit `a46fb53`, unrelated to this sprint), and the publish target was happy with whatever `book.pdf` was on disk. Sprint 19's integrator caught the stale-upload manually via a grep against the gh-pages-published HTML; this sprint makes that manual recovery unnecessary._

Tiny scope, one role's worth of code; partitioned the regular way so sprintwatch counts a terminal state for each role:

| Role | Scope |
|---|---|
| **Staff** Issue 1 | Edit `Makefile`'s `release-publish` target to `rm -rf book/book/pandoc/` + re-invoke `$(MAKE) book-pdf BOOK_BACKEND=docker` + gate the upload step on the rebuild's exit code. Symmetric hardening for `book-publish` if its HTML side carries the same staleness risk. Docstring update naming the Sprint 19 v1.6.4 event so future maintainers understand the rationale. |
| **Architect** Issue 1 | None — release-tooling-only, no user-facing surface. Closure note explicitly says "no architect deliverables", flips status to `resolved`. |
| **Validator** Issue 1 | New hermetic test (`scripts/test-release-publish-staleness.sh` or a bats equivalent under `tests/scripts/` if bats is in the tree) that plants a sentinel stale PDF, stubs `gh` on PATH, and asserts the rebuild preamble wipes the stale bytes + `gh release upload` is never called with stale content. Hermetic — no real `gh`, no docker pull, no actual PDF render. |
| **Tech-writer** Issue 1 (light, runs after) | Drift sweep — docstring matches recipe, symmetric staleness risks on neighbouring targets caught, `docs/PLAN.md` closure subsection lands. GREEN/RED launch verdict. |

`live-verify-high-issues` does NOT apply — this is artifact-shape risk, not cloud-integration-shape. The hermetic test is sufficient closure. Integrator's live test is whoever cuts the next release runs `make release-publish VERSION=v…` against a tree with a planted stale `book.pdf` and confirms the rebuild fires + the right PDF gets uploaded.

Version at cut: integrator-owned. Internal tooling fix → likely a patch bump (`v1.6.5`) bundled with whatever user-facing changes ride along; could also defer to the next user-facing-feature cut and ride that release.

Sprint launch: integrator dispatches staff + validator in parallel (tech-writer + architect can run sequentially with minimal work). Aggregates + commits + runs the hermetic test + dispatches tech-writer over the integrated tree.

**Closure — 2026-05-21 (integrator-merged at commit `973fc67`).** Hermetic gate GREEN: `bash scripts/test-release-publish-staleness.sh` returned 8/8 PASS (run-id `20260521-145005`) — two scenarios (S1 HTML-side, S2 PDF-side), four assertions each (A1 rebuild-step-reached, A2 planted-bytes wiped, A3 gh-upload-not-called-or-fresh, A4 make-exit-non-zero). Staff's `Makefile` edits (lines 384-458) added the rm-then-rebuild + exit-code gate to both publish steps; the dead `[ -f book.pdf ]` existence prereq is gone. `book-publish` standalone is intentionally unchanged (preserves contributor entrypoint usability); the symmetric HTML hardening lives at `release-publish`'s call site instead. Validator's hermetic driver (`scripts/test-release-publish-staleness.sh`, 394 LOC, plain bash) stubs `gh` + `docker` on PATH so the test never invokes the real binaries or `docker pull`s, and runs from a sandbox tmpdir so the real working-tree `Makefile` is never executed from. Architect: no deliverables (release-tooling-only). Tech-writer: 9 findings (0 high · 1 medium [this PLAN.md closure subsection, now filed] · 8 low [5 positive confirmations + 3 informational]); verdict **READY TO CUT**. The next release cycle's `make release-publish` will fail loudly on a stale `book/book/pandoc/` or `book/book/html/` rather than silently uploading prior-cycle artifacts under the new tag's name — closing the v1.6.4 cycle's manual-recovery gap.

Version at cut: integrator-owned. Internal release-tooling fix → can ride the next user-facing change's release (e.g. Sprint 21's argv strictness, which is also pending) or cut as `v1.6.5` independently. Tech-writer's F9 finding suggests deferring the CHANGELOG bullet to the next user-facing cut; the docstring at `Makefile:404-413` is sufficient in-repo provenance.

---

## Sprint 19 — `roksbnkctl init --var-file <path>` — workspace-persistent tfvars at init time (first regular work sprint post-`v1.6.3`)

_Drafted 2026-05-20 after live use of `v1.6.3` surfaced the residual gap that v1.6.2's `.applied-replay.tfvars` mechanism couldn't reach: between `init` and the first successful `up`, there is no `terraform.applied.tfvars` snapshot yet, so bare `roksbnkctl <verb> -w <ws>` still refuses with the Sprint 16 option-(b) actionable error. The plumbing to close the gap already exists in the tree (`tfws.HasUserTFVars()` already auto-layers `state/terraform.tfvars.user` if present); Sprint 19's work is `init`-side only — add a `--var-file <path>` flag that automates the file copy + skips the corresponding interview prompts. The operator's `./terraform.tfvars` (or equivalent) becomes the workspace's persisted variable identity at init time._

Single scope item, partitioned by role per the regular work-sprint shape:

| Role | Scope |
|---|---|
| **Staff** Issue 1 | `internal/cli/init.go`: bind `--var-file <path>` to the `init` cobra command; on supply, parse via `internal/config/applied_tfvars.go`'s existing `readTFVarsAssignments`, seed the interview-targeted `config.yaml` fields (region / cluster name / version / workers / etc.), skip the corresponding prompts, copy the file verbatim to both `<WorkspaceStateDir>/terraform.tfvars.user` and `<WorkspaceClusterStateDir>/terraform.tfvars.user` at mode `0600`. Lifecycle / orchestration / cos / ibm explicitly out of scope — Sprint 18 hardened them; the existing `HasUserTFVars()` layering does the work for free. |
| **Architect** Issue 1 | Update the init book chapter (verify path via `book/src/SUMMARY.md`) with a §"Skip the interview: `init --var-file`" subsection (when, flow, what's persisted, why-it-matters, secrets-on-disk note, diagnostics paragraph). Regenerate `book/src/27-command-reference.md` via `go run ./tools/refgen/cobra-md` so the new flag is in the canonical reference (Sprint 18's tech-writer caught this regen drift post-integration — Sprint 19 lands it up front). Cross-chapter sweep to point existing "supply `--var-file` on every command" prose at the new flow. |
| **Validator** Issue 1 | Additive `internal/cli/init_var_file_test.go` covering the five sub-cases (happy path, config seeding, missing file, malformed file, no-flag byte-identical behaviour). Opt-in `scripts/e2e-init-var-file.sh` mirroring the Sprint 18 driver shape: `init --var-file` then bare `plan -w <ws>` — should succeed because `HasUserTFVars()` picks up the seeded file. Assertion A3 verifies the actual layering log line, not just exit-0 proxy. |
| **Tech-writer** Issue 1 (light, runs after) | Drift sweep — `init --help` output matches the regenerated chapter 27; init chapter has all five subsection components; cross-chapter sweep landed; no stale "supply `--var-file` on every command" survived. GREEN/RED launch verdict ends the closure. |

`live-verify-high-issues` applies — integrator runs `roksbnkctl init -w <ws> --var-file ./terraform.tfvars` then bare `plan` / `down` and confirms they succeed without re-supplying `--var-file`. Closure on the live `!` GREEN.

Version at cut: integrator-owned. Pure-additive flag → expected shape `v1.6.4` under strict-SemVer; `v1.7.0` only if integrator judges minor-worthy. No PRD — straight from the integrator's live-use observation that closes a real user-reported friction.

Sprint launch: integrator dispatches architect + staff + validator in parallel (single message, three `Agent` tool calls reading `prompts/sprint19/*.md`); aggregates + commits + runs gates + runs the live verify; then dispatches tech-writer over the integrated tree.

**Closure — 2026-05-21 (cut as `v1.6.4`).** Live `!` verify GREEN, run-id `20260521-031343`: `roksbnkctl init -w e2e-init-vf-… --var-file ./terraform.tfvars` followed by bare `roksbnkctl plan -w e2e-init-vf-…` (no `--var-file`) exits 0; stderr carries the `→ Layering user tfvars from /home/jgruber/.roksbnkctl/<ws>/terraform.tfvars.user` line from `internal/orchestration/lifecycle.go`'s `writeAndInit`, confirming the `HasUserTFVars()` codepath that pre-existed in the tree closes the v1.6.3 gap end-to-end. Round-1 hermetic cycle had been clean; round-2 fixes were needed for two related defects the live cycle caught:

- **The seeded file's destination was wrong.** Round-1 wrote `terraform.tfvars.user` into `state/` AND `state-cluster/`; `tf.Workspace.UserTFVarsPath()` (one-line helper in `internal/tf/terraform.go`) resolves to `filepath.Join(filepath.Dir(stateDir), "terraform.tfvars.user")` → `<workspace-root>/terraform.tfvars.user`. The two in-state-dir copies were invisible to `HasUserTFVars()`. Round-2 collapses to a single workspace-root copy that serves both phases. The staff + validator ledgers' round-1 dataflow trace claimed `state/terraform.tfvars.user` was the lookup path without checking the function's body — the carry-forward discipline (added to the staff ledger's round-2 closure) is: when a ledger pins an existing function as the "auto-magic" landing site, quote the function's body into the ledger before claiming a destination.
- **The Sprint 16 actionable-error gate didn't know about the new persistence path.** `orchestration.RequireSnapshotOrVarFile` rejected bare `plan/apply/down` when neither an applied-tfvars snapshot nor an explicit `--var-file` was present — but it predated the workspace-persistent `terraform.tfvars.user` Sprint 19 adds. The gate's signature was widened to take a `hasUserTFVars bool` third argument (Sprint 19 is the only caller-side change; the gate's `internal/orchestration` location remained correct); error message now names BOTH remedies (`--var-file <path>` flag or `init --var-file <path> -w <ws>` re-init).

Architect's docs (chapters 6, 10, 12, 13, 27) were updated round-2 to reflect the workspace-root path. Three integrator-mechanical edits across the architect's round-1 prose (the "two copies" / "state-dirs" assertions); a stale-copy diagnostic note left in chapter 6 §"Diagnostics" so an operator who has phantom in-state-dir copies from an early-build run knows they're harmless.

Closing the sprint: all four role ledgers flipped `Status: open → resolved`; CHANGELOG `## v1.6.4 — 2026-05-21` written; tag `v1.6.4` cut + pushed (goreleaser publishes binaries); `make release-publish VERSION=v1.6.4` publishes the PDF + HTML book to `gh-pages`; `scripts/archive-sprint.sh 19` archives the sprint ledger + role prompts.

---

## Sprint 18 — `cos bucket get` + mermaid PDF text-missing fix (first regular work sprint post-`v1.6.2`)

_Drafted 2026-05-20 after Sprint 17's backlog-grooming attempt was abandoned and the two GitHub Issues (`#1 cos bucket get`, `#2 mermaid PDF rendering`) were captured into local ledgers and deleted from GitHub. **Source of truth for Sprint 18 work = `issues/issue_sprint18_*.md`**, not GitHub Issues — GitHub remains the destination for external-user reports via `.github/ISSUE_TEMPLATE/`, not a parallel queue for in-flight roksbnkctl work._

Two scope items, partitioned by role per the Sprint 15/16 work-sprint shape:

| Role | Scope |
|---|---|
| **Staff** Issue 1 | Implement `roksbnkctl cos bucket get --instance <inst> <bucket> <local-dir>` — recursive streaming download, symmetric with the existing `cos bucket {create,list,delete}` group. Reuses `cos object get`'s per-object streaming path. Sequential download (concurrency deferred). Files: `internal/cos/bucket.go`, `internal/cli/cos.go`, new `internal/cos/bucket_get_test.go`. |
| **Architect** Issue 1 | Diagnose + fix the mermaid PDF text-missing rendering bug (most likely a font-availability or SVG→PDF conversion issue inside `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`'s `/opt/render-mermaid.lua` Lua filter). Three ranked hypotheses in the issue spec; diagnostic-then-fix. HTML book pipeline must keep working. |
| **Validator** Issues 1 + 2 | Hermetic `bucket_get_test.go` cases (a)–(g) covering sha256 round-trip / nested-key subdirs / `--no-clobber` / error paths; opt-in `scripts/e2e-cos-bucket-get.sh` for the live sha256 round-trip on a real bucket. Separate regression smoke check on the mermaid fix (`pdftotext`-driven label assertion wired into the PDF build so a future regression in the docker image's fonts fails the build, not silently ships). |
| **Tech-writer** Issue 1 (light, read-only, runs after) | Drift sweep over the integrated tree: `--help` text matches sibling COS verbs, CHANGELOG bullets are user-facing, COS chapter pins the new verb example, mermaid fix mentioned in §Diagnostics not as a feature. GREEN / RED launch verdict ends the closure. |

`live-verify-high-issues` discipline applies to the staff feature — the validator builds the gated-live driver; integrator runs the live `!` verify (real COS bucket, sha256 round-trip) before flipping staff Issue 1 to `resolved`.

### Closure (2026-05-20)

All four scope items live-GREEN against a real IBM Cloud account; shipped as `v1.6.3`. Two additional pre-existing defects in the shared `cos` command path (filed as Sprint 18 staff Issues 2 + 3 after the three-way integration landed and manual testing surfaced them) were folded into the same release per integrator decision — same release-cost-of-time, ships working.

- **Staff Issue 1** (`cos bucket get`) — live GREEN, recursive download of `bnk-schematics-resources` (us-south, 9 objects) into a local directory with sha256 round-trip on every file.
- **Staff Issue 3** (`cos *` 404 on cross-region buckets) — live GREEN. The COS S3 API is endpoint-scoped, so a single home-region S3 handle 404s on out-of-region buckets; round-2 introduced a `BucketRegionResolver` + per-bucket region cache + per-region S3-handle cache. (Round-3 was an integrator-prescribed `s3:GetBucketLocation`-shortcut attempt that shipped + live-verified RED + was reverted on `main`; round-4 swapped in a parallel HeadBucket fan-out mirroring `ibmcloud cos`'s own coordinator. Both rounds' hermetic invariants kept passing byte-unchanged through the revert/replacement cycle.)
- **Staff Issue 2** (`cos *` ~10× slower than `ibmcloud cos`) — live GREEN. Three integrator-prescribed rounds (2/3/4) of `internal/cos/client.go` hardening hermetic-passed but moved the live wall-clock by ~1 second total; round-5 was dispatched investigate-first (with live-cloud-call authority) and the profile data was unambiguous — 76.4s of the 88s lived in `internal/ibm/cos_instance.go::ListCOSInstances`'s unfiltered Resource Controller v2 pagination over every resource in the account, not in `internal/cos/`. Round-6 narrowed the SDK call server-side via the COS service-catalog offering UUID; live wall-clock dropped to **1.86s** (under the 1.4s `ibmcloud cos` baseline; ~47× speedup on the user-reported defect). The cos client work in rounds 2 + 4 was correct; it just wasn't where the cost was.
- **Architect Issue 1** (mermaid PDF text-missing) — live GREEN. mermaid-cli v11 emits node/edge labels via SVG `<foreignObject>` + embedded XHTML; librsvg (pandoc's SVG-to-PDF rasteriser) does not implement `<foreignObject>`, so the previous SVG-output Lua filter produced text-less diagrams. The filter pivoted to mermaid-cli's PNG path (Puppeteer + Chromium, which renders `<foreignObject>` natively and has the mermaid browser-font stack baked in). New regression smoke check at `scripts/check-pdf-mermaid-labels.sh` fails the build if a future contributor regresses the docker image's font set.
- **Validator Issues 1 + 2** — all hermetic + gated-live tests landed; live drivers verified against the real account; smoke check wired into `make book-pdf`.
- **Tech-writer Issue 1** — 3 findings (cobra-md regen, chapter 25 pinning, SVG→PNG comment drift across 4 files) addressed in the integration commit; GREEN verdict.

Lessons banked for the integrator's memory (`live-verify-high-issues`, `no-piling-into-active-release`): three integrator-prescribed rounds on Issue 2 moved nothing — the only one that did was the one that started with instrumentation + data. **Default to investigate-first prompts on bugs whose root cause isn't obvious from the symptom.**

Version at cut: `v1.6.3` shipped 2026-05-20. No `v1.7.0` bump — the new `cos bucket get` is additive and the other three are bug fixes, all under the strict-SemVer patch envelope.

---

## Sprint 17 — backlog-grooming attempt — **abandoned** (post-`v1.6.2`)

Briefly attempted 2026-05-20 immediately after the `v1.6.2` cut: each of the four roles was dispatched to survey its area and draft GitHub-ready issue markdown files into `prompts/sprint17/staging/<role>/`, with the integrator filing the accepted set via `gh issue create`. **Abandoned mid-flight by integrator decision** — the review was taking disproportionate time relative to the signal in the drafts, and the working tree was carrying more in-process artifacts than the rest of the backlog process warranted. No GitHub issues were filed from this sprint; the 19 staff/architect/validator drafts + the 1 partial tech-writer draft + the dispatch prompts + the issue ledgers were moved to [`.archive/prompts/sprint17/`](../.archive/prompts/sprint17/) and [`.archive/issues/`](../.archive/issues/) for auditability. Future backlog-grooming will use a smaller, ad-hoc shape (file issues directly as they're noticed, via the templates at `.github/ISSUE_TEMPLATE/`) rather than the four-role parallel-dispatch the consolidation/code sprints use.

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
- ~~In-pod `ibmcloud login` wrap closure (Sprint 9 staff Issue 2)~~ — scheduled for Sprint 10 / `v1.3.0`
- Bash completion for `roksbnkctl k <verb> <resource-name>` with live API lookups (v1.1)

### Book

- F5 corporate theming (v1.1 if branding requested)
- Translated editions (v2)
- Video walkthroughs / screen recordings embedded in chapters (v1.1)
- Per-chapter "try it now" interactive sandbox (v2)
- Versioned book — keeping a `v0.9/`, `v1.0/`, `v1.1/` URL path so older releases' docs stay accessible (v1.1; book is single-version for v1.0)
