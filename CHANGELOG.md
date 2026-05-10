# Changelog

All notable changes to `roksbnkctl` are documented in this file. Format follows the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) convention; the project uses [semantic versioning](https://semver.org/spec/v2.0.0.html) starting at `v0.9.0`.

Per-sprint design rationale lives in [`docs/PLAN.md`](docs/PLAN.md); per-PRD design specs live under [`docs/prd/`](docs/prd/). This file is the user-facing summary of what changed between releases.

## v0.9.0 — 2026-05-10 (M3 milestone)

The four-backend, GSLB-validation, in-cluster-ops release. Cumulative surface across Sprints 3–5.

### Added

#### Sprint 5 — DNS probe + terraform docker (v0.9 gate sprint)

- **GSLB-aware DNS probe** (`roksbnkctl test dns`)
  - `miekg/dns`-based `Probe` (replaces the std-lib `net.Resolver` impl) with full record-type coverage (A / AAAA / CNAME / MX / NS / TXT / SRV / SOA / PTR / CAA / DS / DNSKEY / ANY plus everything else `dns.StringToType` accepts)
  - New flags: `--target`, `--type`, `--server`, `--iterations`, `--timeout`, `--gslb-compare`, `--require-divergence`
  - Server resolution: literal `<ip>[:<port>]`, `system` (host `/etc/resolv.conf`), `cluster` (in-pod CoreDNS, k8s-backend only), or named-from-workspace-config (`test.dns.resolvers`)
  - RTT distribution (`p50`/`p95`/`p99`) when `--iterations > 1`
  - JSON output: `roksbnkctl.dns.v1.vantage` (single-vantage) and `roksbnkctl.dns.v1` (`--gslb-compare`)
  - `--gslb-compare` fans the probe across `local` + `k8s` (when a kubeconfig is reachable) + every `ssh:<target>` registered in workspace targets; emits `gslb_divergence` boolean
  - `--require-divergence` flips the exit code when no divergence is observed (CI assertion that GSLB is doing something)
  - In-cluster path runs as a one-shot Job re-execing the bundled tools image (no separate `roksbnkctl-cli` image)
  - Workspace config: new `test.dns.resolvers` (named resolver map) and `test.dns.default_target` fields
- **Terraform via docker** (`roksbnkctl up/plan/apply/destroy --backend docker`)
  - `hashicorp/terraform:1.5.7` pinned upstream image
  - Workspace state directory bind-mounted at `/state` (read-write); embedded HCL materialised under `/state/tf-source/<source>/`
  - `--user $(id -u):$(id -g)` keeps state-file ownership aligned with the host user (Linux/WSL2; macOS Docker Desktop transparent)
  - `--backend k8s` and `--backend ssh:<target>` for terraform deferred to v1.x with a clear error pointing at PRD 03 §"State concerns"
- **Doctor extensions** (`roksbnkctl doctor`)
  - DNS-probe sanity check (when workspace has `test.dns.default_target`)
  - K8s ops-pod env runtime probe (`kubectl exec -- printenv`, value redacted in output)
  - Cred rotation freshness warning when the Secret's `roksbnkctl.io/rotated-at` annotation is more than 30 days old
- **Book chapters**: 20 (Connectivity testing), 21 (DNS testing for GSLB — flagship), 22 (Throughput testing); chapter 17 expanded with terraform-via-docker subsection

#### Sprint 4 — k8s + SSH backends, in-cluster ops pod

- **`--backend k8s`** (`internal/exec/k8s.go`)
  - Long-lived ops pod path for ad-hoc commands (`ibmcloud`, future interactive shells); SPDY-channel `kubectl exec` with redactor-wrapped stdout/stderr
  - One-shot Job path for ephemeral tools (iperf3 client, future probes); `ttlSecondsAfterFinished: 60` auto-cleanup; logs streamed via `client-go`
  - `roksbnkctl ops install/show/uninstall` — install/inspect/teardown of namespaces, ServiceAccount, ClusterRole, ClusterRoleBinding, Secret, Pod
  - Embedded RBAC manifests (`internal/exec/k8s_install.yaml`) — least-privilege ClusterRole with `resourceNames`-restricted `secrets/get`
- **`--backend ssh:<target>`** (`internal/exec/ssh.go`)
  - File materialisation to `/tmp/roksbnkctl.<rand>/` on the remote with `trap … EXIT` cleanup
  - Env propagation: SetEnv (preferred, requires sshd `AcceptEnv`) → wrapper-script-with-trap fallback (silent `set +x` source from a 0700 env-file)
  - Per-tool apt-bootstrap behind `--bootstrap` opt-in (Ubuntu only); 126/127 split for sudo / non-Ubuntu / repo-unreachable failures
  - Doctor `--backend k8s` / `--backend ssh:<target>` checks
- **iperf3 SCC fix** for OpenShift `restricted-v2` (`runAsNonRoot`, `runAsUser: 1000`, `seccompProfile: RuntimeDefault`, `capabilities.drop: [ALL]`)
- **Per-tool default backend map**: iperf3 → `k8s`, ibmcloud → `local`, terraform → `local`
- **126/127 backend-failure split** — `127` for "couldn't start" (daemon down, target unreachable), `126` for "started then failed" (container OOMKilled, ssh session died mid-run)
- **Book chapters**: 17 (Execution backends — full deep-dive), 18 (Choosing a backend per tool), 19 (The in-cluster ops pod)

#### Sprint 3 — credential abstraction + first backends

- **`internal/cred.Resolver`** — single-source-of-truth API key resolution chain (env → keychain → config-b64 → prompt)
- **`internal/exec.Backend` interface** + `RunOpts` + `Credentials` shared shape across all backends
- **`--backend local`** + **`--backend docker`** — first two backends; `--backend` persistent root flag wins over workspace-config default
- **Output stream redactor** (`internal/exec/redact.go`) — wraps `io.Writer` to mask the IBM API key value if it ever appears in stream content; defense-in-depth across all backends
- **Vendored tool images** — `ghcr.io/jgruberf5/roksbnkctl-tools-{ibmcloud,iperf3}:<v>`; tag pinned to the binary's `internal/version.Version` value at runtime (release tag → matching image tag)
- **Workspace config `exec:` block** — per-tool default backend selection
- **`tools-images.yml` GitHub Actions workflow** — builds + pushes the tools images on tag (Sprint 5 added `:dev` push on `main` for `go install ./cmd/roksbnkctl@main` UX)
- **Book chapters**: 12 (Workspace config), 13 (Terraform variables), 14 (Credentials and the resolver chain), 15 (SSH targets), 17 intro (Execution backends)

### Changed

- **`hashicorp/terraform:1.5.7`** is the literal pin for the terraform docker backend (not version-resolved like the per-tool tools images)
- **DNS probe schema strings** are now namespaced: `roksbnkctl.dns.v1.vantage` for single-vantage, `roksbnkctl.dns.v1` for multi-vantage `--gslb-compare`
- **`tools/docker/iperf3/Dockerfile`** ships `USER 1000` so the bundled image satisfies `runAsNonRoot: true` policies on plain k8s clusters
- **K8s Job names** now sanitise docker-style argv[0] image refs (colons / slashes / `@`) so the test fallback path doesn't trip k8s label-validation regex

### Deferred (post-v1.0)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). High-water-mark:

- terraform `--backend k8s` and `--backend ssh:<target>` (state-handling design open; v1.x)
- SSH backend `apt-get` bootstrap on RHEL/CentOS/Alpine (Ubuntu-only in v0.9)
- Native Windows Docker Desktop UID/GID handling for terraform-via-docker
- DNS probe `edns_client_subnet` field surfacing (PRD 03 specs it; not emitted in v0.9)

### Documentation

The book at <https://jgruberf5.github.io/roksbnkctl/book/> covers the v0.9 surface in **22 published chapters**: 0 (Preface) through 22 (Throughput testing). Sprint 6 will land chapters 23-32 (E2E plan, COS supply chain, troubleshooting, command + config reference, glossary, building from source).

Per-PRD design rationale (cred propagation, execution backends, kubectl internalisation, etc.) lives under [`docs/prd/`](docs/prd/).

## Unreleased (v1.x)

Tracked in [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). The next milestone is **M4 / v1.0** — the E2E test plan (Phases I-N + L-DNS) passing on a fresh dev box with the full reference + troubleshooting + contributing chapters of the book published.
