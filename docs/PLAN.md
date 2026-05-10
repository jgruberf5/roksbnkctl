# Phased development & testing plan

Execution plan synthesizing the six PRDs in [`docs/prd/`](./prd/) into sequenced work, with development and testing interleaved per sprint. References the PRDs by number; read those for the *what*, this for the *when* and *how*.

## Goals & top-level milestones

| Milestone | Tag | Outcome |
|---|---|---|
| **M1** | `v0.7` | `--on jumphost` works; user can drive `roksbnkctl ibmcloud`/`exec`/`shell` over SSH against an auto-discovered jumphost |
| **M2** | `v0.8` | `kubectl` no longer required on host for the happy path; native `roksbnkctl k get/apply/logs/exec` |
| **M3** | `v0.9` | `--backend docker|k8s|ssh` works for ibmcloud, iperf3, terraform; DNS probe internalized + GSLB-aware |
| **M4** | `v1.0` | All E2E Phases A-H plus I-N + L-DNS pass on a clean dev host (no kubectl/oc/iperf3/dig installed); credential audit clean |

Estimated calendar time: **~12 weeks** (six 2-week sprints) for a single focused engineer. Doubling that for "real-world with reviews, distractions, and integration debt" puts the M4 target around **6 months out**.

## Phase overview — sequencing decisions

```
┌───────────────────────────────────────────────────────────────────┐
│ Sprint 0 (week 0)        Foundations: CI matrix, dev shortcuts    │
├───────────────────────────────────────────────────────────────────┤
│ Sprint 1 (weeks 1-2)     PRD 01 — SSH client + --on flag          │
│   ↓                                                               │
│ Sprint 2 (weeks 3-4)     PRD 02 — kubectl internalization         │
│   ↓                                                               │
│ Sprint 3 (weeks 5-6)     PRD 04 — cred abstraction (informs 3)    │
│                          PRD 03 — local + docker backends         │
│   ↓                                                               │
│ Sprint 4 (weeks 7-8)     PRD 03 — k8s + ssh backends              │
│                          Tool migration: iperf3, ibmcloud         │
│   ↓                                                               │
│ Sprint 5 (weeks 9-10)    PRD 03 — DNS probe (miekg/dns + GSLB)    │
│                          Tool migration: terraform (docker only)  │
│   ↓                                                               │
│ Sprint 6 (weeks 11-12)   PRD 05 — E2E Phases I-N + L-DNS          │
│                          Hardening, doctor refresh, v1.0 cut      │
└───────────────────────────────────────────────────────────────────┘
```

**Dependency rationale**:
- SSH client (Sprint 1) blocks the SSH backend in Sprint 4
- Cred abstraction (Sprint 3, first half) shapes the `Backend` interface, so it must precede backend implementations
- kubectl internalization (Sprint 2) gives the K8s backend a reusable in-cluster client builder
- DNS probe (Sprint 5) reuses the K8s backend's Job pattern from Sprint 4
- E2E phases (Sprint 6) gate the v1.0 release

## Sprint 0 — foundations (week 0)

### Goal

Set up the developer workflow and CI matrix so the next 12 weeks of changes can land safely.

### Code deliverables

| Item | Detail |
|---|---|
| CI matrix expansion | GitHub Actions: `go test ./...` on Linux + macOS; `gofmt`, `go vet`, `staticcheck`. Stretch: Windows compile check. |
| Pre-commit hook | `gofmt`, `go vet`, `go test ./internal/...` (skip slow tests via `-short`) |
| Tool image build skeleton | `tools/docker/Makefile` + GitHub Actions workflow that *can* build images on tag — pushed only when tools/docker/* changes |
| Doctor v2 sketch | Refactor `roksbnkctl doctor` so it can grow per-backend checks without rewriting; introduce `Check{Name, Status, Detail}` struct |

### Test deliverables

- Existing `go test ./...` baseline runs in CI (already green from the rename + e2e work)
- Existing `scripts/e2e-test.sh` documented in CONTRIBUTING.md as the long-running smoke test

### Gate to Sprint 1

- All existing tests still green; CI matrix runs on PRs; doctor refactor merged

### Risks

- CI matrix may surface platform-specific bugs (path handling, socket types) — budget half a day for surprises

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

### Gate to Sprint 2

- M1 merged + tagged `v0.7`
- Unit + integration tests green
- E2E (run manually): jumphost steps pass on a real ROKS cluster

### Risks

- TF output `jumphost_shared_key` is a sensitive value — confirm we can read it via terraform-exec's `Output()` without it being redacted to `<sensitive>`. **Mitigation**: spike this in week 1 day 1; if blocked, fall back to writing the key to `~/.roksbnkctl/<ws>/state/jumphost.pem` from a dedicated null_resource in the HCL.
- `golang.org/x/crypto/ssh` PTY handling on Windows is incomplete — restrict TTY mode to Linux/macOS for v0.7; document the limitation

---

## Sprint 2 — kubectl internalization (PRD 02)

### Goal

Ship M2 (`v0.8`): `roksbnkctl get/apply/logs/exec/port-forward` works without `kubectl` on PATH.

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
| 10 | `internal/cli/k_*.go` — wire `roksbnkctl k get/apply/describe/delete/exec/logs/port-forward` plus top-level aliases for `get/apply/logs` | new |
| 11 | `internal/cli/doctor.go` — downgrade kubectl/oc from required to informational | edit |

### Test deliverables

- **Unit tests** with `k8s.io/client-go/kubernetes/fake` clientset for `internal/k8s/get,apply,delete,logs`
- **Golden-file tests** against a live cluster: `roksbnkctl k get nodes -o yaml` byte-compared to `kubectl get nodes -o yaml`, ignoring `managedFields/resourceVersion/creationTimestamp`. Run only with `-tags=live`.
- **E2E patch**: existing `scripts/e2e-test.sh` Phase D — replace `roksbnkctl kubectl get pods -n f5-bnk` (D3) with `roksbnkctl k get pods -n f5-bnk`. Add a new D-internal step that `mv kubectl kubectl.hidden`'s the binary, runs `roksbnkctl k get nodes`, restores.

### Gate to Sprint 3

- M2 merged + tagged `v0.8`
- E2E with kubectl PATH-stripped passes on a live cluster
- Byte-equivalence test passes for `get -o yaml` on Node, Pod, Service, ConfigMap

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

### Gate to Sprint 4

- Cred audit test green: API key value never appears in any inspectable surface (logs, argv, container metadata)
- Docker backend produces output identical to local backend for `ibmcloud ks cluster ls`
- Doctor's `--backend docker` check accurate

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

### Gate to Sprint 5

- M3-prelim: `roksbnkctl test throughput --backend k8s` runs entirely in cluster, no host iperf3 required
- `roksbnkctl ibmcloud --backend ssh:jumphost ks cluster ls` works on fresh Ubuntu jumphost (auto-installs ibmcloud CLI)
- Phase K + Phase L from PRD 05 pass on a live cluster

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

### Gate to Sprint 6

- M3 merged + tagged `v0.9`
- Phase L-DNS passes including the GSLB divergence detection
- terraform `--backend docker` runs a real `up` cycle end-to-end against `hashicorp/terraform:1.5.7` (or current pin)

### Risks

- `miekg/dns` API has minor breaking changes between major versions; pin to a stable release tag in go.mod
- GSLB divergence detection requires a target where local and k8s actually return different answers; if testing against `8.8.8.8` for `www.google.com` returns identical answers due to anycast, document a more illustrative target (e.g., a TF-deployed internal GSLB record)
- terraform state in a Docker bind-mount has UID/permission gotchas — Linux container runs as root by default; bind-mount-owned-by-user can have permission issues. Pre-create dirs with `chown` or use `--user $(id -u):$(id -g)` consistently

---

## Sprint 6 — E2E test plan build-out + v1.0

### Goal

Ship M4 (`v1.0`): all E2E phases (A-H + I-N + L-DNS) pass on a clean dev host with no host install of kubectl/oc/iperf3/dig. Credential audit clean.

### Code / config deliverables

| Order | Item | Files |
|---|---|---|
| 1 | `scripts/e2e-test-backends.sh` — full Phases I-N + L-DNS driver (some pieces written in earlier sprints; this consolidates) | edit |
| 2 | `scripts/e2e-test-full.sh` — runs A-H + I-N + L-DNS against the same cluster, ~5 hour total | new |
| 3 | Phase M (cred audit) implementation — automated checks of `docker inspect`, `kubectl get events`, ssh tempfile cleanup | new |
| 4 | Phase N (mixed-mode lifecycle) wiring | new |
| 5 | Doctor refresh: green-by-default on a stock dev box (`terraform` only required) | edit |
| 6 | README rewrite: install instructions reflecting "terraform only" prereq | edit |
| 7 | Migration notes for users coming from v0.6.x or earlier | new |

### Test deliverables (this sprint *is* the testing sprint)

- All 14 individual phase steps from PRD 05 pass on a fresh test run
- Combined runner script provides a "one button" full-coverage test for CI
- Cred-leak audit (Phase M) clean: API key never appears in any inspectable surface across all backends
- `scripts/e2e-test-full.sh` tagged in CI as a manual-trigger workflow (too long for every PR; run on release branch + on demand)

### Gate to v1.0 release

- Tag `v1.0` only when:
  - All E2E phases pass on a clean test host
  - All previous sprints' acceptance criteria still hold (no regressions)
  - README + docs accurately reflect the final shipped surface
  - Cred audit clean
  - At least one external user has run a full lifecycle on their own IBM Cloud account and reported success (dogfooding gate)

### Risks

- **E2E flakiness**: ROKS cluster apply takes 30-50 min; transient API errors during apply add another 5-15 min; throughput tests depend on outbound network. Mitigation: PRD 05 already designs each step to be re-runnable (`PHASE_FROM=`); add jitter+retry to the assertion phases that hit external APIs.
- **Cluster cost**: a full e2e run costs ~$5-10 of IBM cloud spend (cluster + LBs + COS). Document this in CONTRIBUTING.md so contributors don't get surprised.
- **Slow CI**: 5 hours is too long for a PR check. Solution: gate v1.0 release branch on full e2e; PR checks run only the unit + integration tiers.

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

## Definition of done — per release

### v0.7 (M1)

- Sprint 0 + 1 complete
- `--on jumphost` validated against a live cluster
- README documents `targets:` config block + `roksbnkctl targets` commands

### v0.8 (M2)

- Sprint 2 complete
- `roksbnkctl k get/apply/logs/exec/port-forward` covers BNK-relevant operations
- Doctor downgrades kubectl/oc to informational
- Byte-equivalence test green for representative resources

### v0.9 (M3)

- Sprints 3-5 complete
- Four backends working for at least ibmcloud + iperf3
- DNS probe internalized; GSLB divergence detection works
- Cred audit (unit + integration tier) clean

### v1.0 (M4)

- Sprint 6 complete
- All E2E Phases A-H + I-N + L-DNS pass on a clean host
- README rewritten for terraform-only-prereq install
- Tagged release with binaries for Linux + macOS (Windows compile-only)
- At least one external user has done a full lifecycle dogfood

## What's deliberately deferred to post-v1.0

These came up during the PRDs but aren't blocking v1.0:

- terraform `--backend k8s` and `--backend ssh` (state-handling work — v1.1)
- OpenShift CRDs in `roksbnkctl k get` (`Project`, `Route`, etc. — v1.1 — tracked in PRD 02 § Phase 2.1)
- IAM trusted-profile auto-provisioning (v1.1 — PRD 04 open question)
- RHEL/CentOS/Alpine SSH apt-bootstrap (v1.x — PRD 03 explicitly out of scope)
- Windows full TTY support (v2 — needs ssh-agent named-pipe protocol)
- Multi-hop SSH ProxyJump (v1.1 — PRD 01 deferred)
- Long-running ops pod with kubeconfig refresh on token rotation (v1.1 — PRD 04 open question)
- DNS probe `--require-divergence` CI assertion mode (v1.1)
- Bash completion for `roksbnkctl k <verb> <resource-name>` with live API lookups (v1.1)
