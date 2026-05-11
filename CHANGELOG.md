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
- **Terraform via docker** (`roksbnkctl up/plan/apply/down --backend docker`)
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

### Documentation

The book at <https://jgruberf5.github.io/roksbnkctl/book/> covered the v0.9 surface in **22 published chapters**: 0 (Preface) through 22 (Throughput testing). Sprint 6 landed chapters 23-32 (E2E plan, COS supply chain, troubleshooting, command + config reference, glossary, building from source); Sprint 7 launched the polished book alongside the v1.0 tag.

Per-PRD design rationale (cred propagation, execution backends, kubectl internalisation, etc.) lives under [`docs/prd/`](docs/prd/).

## v1.0.0 — 2026-05-11 (M4 milestone)

The first stable release. roksbnkctl bundles seven sprints of work (M1 → M4) into a single-binary CLI: a 4-command lifecycle (`init` → `up` → `test` → `down`), four execution backends (`local` / `docker` / `k8s` / `ssh:<target>`), a GSLB-aware DNS probe, terraform-via-docker, an in-cluster ops pod, and a full kubectl-internalised cluster-ops surface — all in one statically linked binary with terraform as the only required host install. The published book at <https://jgruberf5.github.io/roksbnkctl/book/> ships alongside the binary as the canonical user documentation.

Milestone history: **v0.7** (M1) landed `--on jumphost` for customer-firewalled environments. **v0.8** (M2) internalised kubectl + oc via client-go. **v0.9** (M3) added the four-backend matrix, the GSLB-aware DNS probe, and terraform-via-docker. **v1.0** (M4) closes out with full E2E coverage, doctor green-by-default on a stock dev box with only `terraform` installed, the polished book launch, and the release artifacts (signed binaries deferred to v1.x — see Deferred below).

### Added

#### Sprint 7 — book launch + v1.0 release artifacts

- **Book published** at <https://jgruberf5.github.io/roksbnkctl/book/> — _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_. 32 chapters + preface + worked-example walkthroughs in each Part, Mermaid diagrams for architecture / lifecycle / GSLB cross-vantage / execution-backend matrix, foreword/preface rewrite, every code example re-verified in a fresh workspace. Dogfooded by ≥1 external user against a real IBM Cloud account before the tag cut (per PLAN.md §"v1.0 (M4)" gate).
- **`roksbnkctl --version` / `roksbnkctl version`** now emits a second line `Docs: https://jgruberf5.github.io/roksbnkctl/book/` pointing at the canonical user-documentation surface. The first line ("`roksbnkctl <ver> (commit <c>, built <d>)`") is byte-identical to the pre-v1.0 shape so scripts that grep on it continue to parse. The shape is pinned by `internal/cli/meta_test.go::TestVersionCmd_OutputShape`. Constant of record: `internal/cli/meta.go::DocsURL`.
- **GitHub Release artifacts** — Linux / macOS / Windows × amd64 / arm64 archives + `checksums.txt` + offline **`roksbnkctl-book-v1.0.0.pdf`** (the same book that ships at GitHub Pages, packaged for offline reading via mdbook-pandoc + XeLaTeX). The release page header links at the book and the footer at `CHANGELOG.md`. Archives now include `LICENSE`, `README.md`, `CHANGELOG.md`, and `MIGRATING.md` alongside the binary so the downloaded tarball is self-contained.
- **PDF release pipeline** — `make release` from the repo root drives a docker-containerised build (via `tools/docker/mdbook/Dockerfile` — bundles mdbook + mdbook-mermaid + mdbook-pandoc + pandoc + texlive-xetex + mermaid-cli) that produces both the HTML (for GitHub Pages) and the PDF (for the GitHub Release page) in one shot. Mermaid diagrams pre-render to SVG via mermaid-cli so the PDF embeds real diagrams rather than literal source text. Local dev iteration on HTML stays lightweight via `make book` + `make book-serve` (host install, no docker required).
- **README rewritten** for the v1.0 narrative — single-line status, terraform-only prereq table, install options (go install / pre-built binary / from-source / self-update), pointer block to the book + CHANGELOG + MIGRATING + PLAN + per-PRD design rationale. Trimmed from 700+ lines to ~90; the book is the canonical documentation surface.

#### Sprint 6 — testing build-out + reference chapter coverage

- **Full e2e Phases I + M + N** — `scripts/e2e-test-backends.sh` expanded with Phase I (SSH backend, 12 steps I0-I11), full Phase M (cred audit including the SSH-side M5/M6 steps), and Phase N (mixed-mode lifecycle N1-N6). LD9 (SSH vantage for DNS probe) wired alongside.
- **`scripts/e2e-test-full.sh`** — combined A-H + I-N + L-DNS runner (~4-6 hour wall time); designed for release branches + manual-trigger CI.
- **`.github/workflows/e2e-full.yml`** — manual-trigger + release-branch CI workflow for the combined runner.
- **`TestProbe_TruncatedFlag`** — dual-stack UDP+TCP mock server pins the TC=1 projection through the TCP retry path (closes Sprint 5 validator Issue 4).
- **`tools/refgen/cobra-md`** + **`tools/refgen/tfvars-md`** — Go-based auto-generators for chapters 27 (Command reference) and 29 (Terraform variable reference). Re-run on every CLI / variables.tf change.
- **`MIGRATING.md`** — top-level migration guide for users coming from v0.6.x `bnkctl` or from manual BNK deployments.
- **`internal/cred/resolver_invariance_test.go`** — pins the cred-resolver contract across all four backends (Phase N Go-side contract).
- **`internal/doctor/doctor_test.go`** — pins the green-by-default contract.
- **EDNS Client Subnet surfacing** — `DNSProbeResult.EDNSClientSubnet` is populated from the resolver's RFC 7871 echo (when present); `omitempty` so non-ECS resolvers don't pollute the JSON.
- **Book chapters 23, 25, 26, 28, 30, 31, 32** — hand-written reference / troubleshooting / glossary; chapters 27 and 29 auto-generated.

### Changed

- **`roksbnkctl doctor`** is **green-by-default on a stock dev box with only `terraform` installed**. The historical checks for `kubectl`, `oc`, `ibmcloud`, `iperf3`, and `dig` are now **informational** rather than warnings/errors — the binary has internalised those surfaces (chapter 2 / chapter 17 for backends; chapter 21 for DNS). Exit code semantic (0 on green / 1 on red) unchanged.
- **`tools/docker/ibmcloud/Dockerfile`** dropped `ENTRYPOINT ["ibmcloud"]`. The docker backend's dispatch layer now prepends the tool binary name explicitly via a new `dockerImageBinary` map; the k8s `jobToolCmdOverride` map mirrors it. Sprint 5's `jobToolCmdOverride` shim for `roksbnkctl` self-exec dns-probe is now unnecessary — the cross-backend invariant is pinned in `TestDockerImageBinary_MirrorsK8sOverrides`.
- **Chapter 22** reordered to surface the bundled-image / SCC story before sample output (Sprint 5 tech-writer Issue 14 carry-over).

### Documentation

The book at <https://jgruberf5.github.io/roksbnkctl/book/> launched alongside the v1.0 tag with **32 chapters + preface + worked-example walkthroughs**. Sprint 6 landed chapters 23-32 (E2E plan, day-2 ops, COS supply chain, troubleshooting, command + config + terraform variable reference, glossary, building from source, extending). Sprint 7 added Mermaid diagrams (architecture, lifecycle, GSLB cross-vantage, execution-backend matrix), rewrote the preface, added per-Part worked-example walkthroughs, re-verified every code example against a fresh workspace, and refreshed PRD 05 §"Phase I" + §"Phase N" step matrices to match the shipped surface.

Per-PRD design rationale (cred propagation, execution backends, kubectl internalisation, DNS probe, lifecycle, …) lives under [`docs/prd/`](docs/prd/). Sprint-by-sprint development history lives in [`docs/PLAN.md`](docs/PLAN.md).

### Deferred (v1.x roadmap)

See [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). High-water-mark v1.x items the v1.0 cut explicitly does NOT ship:

- **Cosign / sigstore release signing** — the `.goreleaser.yml` has a placeholder; the signing infra in `.github/workflows/release.yml` lands in v1.x.
- **Homebrew formula / tap repo** — the `brews:` block is wired but commented out pending an `homebrew-tap` repo.
- terraform `--backend k8s` and `--backend ssh:<target>` (state-handling design open).
- `--truncated` user-facing CLI flag for the DNS probe (Sprint 6 validator carry-over).
- Cross-driver cluster-sharing for `scripts/e2e-test-full.sh`.
- SSH backend `apt-get` bootstrap on RHEL/CentOS/Alpine (Ubuntu-only).
- Native Windows Docker Desktop UID/GID handling for terraform-via-docker.
- F5 corporate theming for the book.

## v1.0.1 — 2026-05-11

Re-cut of the v1.0 release. The original `v1.0.0` tag landed on an earlier commit than intended, so the sprint 7 polish (32-chapter book pass, Mermaid diagrams, release-pipeline containerisation, README v1.0 rewrite, `--version` book URL, `make release` driver) never made it into the `v1.0.0` binaries on the GitHub Release page. `v1.0.1` is the corrected cut — everything the `v1.0.0` CHANGELOG entry above describes plus the two deltas below. **End users should install v1.0.1**; the `v1.0.0` Release page is retained as a historical artifact only.

### Added

- **`install_build_dependencies.sh`** — per-OS prereq installer (Linux apt / macOS brew / Windows WSL2). Drives the same toolchain the book chapter 4 walks readers through (Go, terraform, docker, mdbook stack for contributors). Idempotent — skips anything already present.
- **Book chapter 4 (`Installing roksbnkctl`)** expanded with per-OS prereq install steps mirroring the installer script, so the path from "fresh box" to "first `roksbnkctl up`" is one block of commands per platform.

### Changed

- **Book CI shifted from build-and-deploy to validate-only.** `.github/workflows/book.yml` no longer publishes to GitHub Pages from CI — the pandoc backend required for the PDF output isn't present on the runner, and pulling the multi-GB `tools/docker/mdbook` image on every push is wasteful. The workflow now runs `mdbook test` + `mdbook build` for syntax and link validation on PRs and pushes to main; publishing is driven locally by the release integrator.
- **New publish targets** in the Makefile: `make book-publish` pushes the locally-built `book/book/html/` tree to the `gh-pages` branch under `/book/` via a `git worktree` round-trip (preserves `.nojekyll`, CNAME, anything else on the branch). `make release-publish VERSION=v1.0.1` runs `book-publish` AND uploads the PDF to the GitHub Release as `roksbnkctl-book-v1.0.1.pdf` via `gh release upload`. The combined effect: a single command from the integrator's machine handles both publish surfaces, with no CI image pull.
- **`book/book.toml`** marks `[output.pandoc]` as `optional = true` so host-install mdbook (no pandoc on PATH) skips PDF rendering with a warning instead of failing the entire build. Fixes the underlying CI failure that prompted this re-cut.
- **`.gitignore`** excludes `.env`, `.env.local`, `.env.*.local` — local-secrets files sourced by `scripts/e2e-test-full.sh`. Never commit (contain `IBMCLOUD_API_KEY`).

### Fixed (CI recovery)

The first v1.0.1 tag-cut surfaced two latent CI bugs that the previous PR-only validate gate had hidden. Both fixed in this same v1.0.1 cut:

- **`.goreleaser.yml`** no longer references `./book/book/pandoc/pdf/book.pdf` via `release.extra_files`. The previous comment claimed goreleaser would warn-and-continue on a missing path; in practice it fail-stops the release. The PDF is now uploaded separately by `make release-publish` (which runs `gh release upload` from the integrator's machine after the CI workflow finishes), so the `extra_files` reference had no remaining purpose.
- **`mdbook test` dropped from `.github/workflows/book.yml`'s validate job.** mdbook's test step invokes rustdoc on every untagged code fence, treating it as Rust by default. This book contains zero Rust (it's a Go project's operator-facing docs; the actual languages used are bash / go / hcl / json / yaml / text / mermaid / powershell), so the test step generated only false positives. The `mdbook build` step still validates markdown rendering, link integrity, and structural correctness.
- **Chapter 31 (`Building from source`)** — three untagged code fences (Go version snippet, `tools/docker/` tree, `dist/` tree) explicitly tagged as `text` so they render identically and don't trip any future code-fence-aware tooling.

### Release-flow documentation

Integrator tag-cut sequence is now:

```sh
make release                 # stamp, build HTML+PDF, lint, snapshot, verify Pages
git add -A && git commit -m "chore: prep v1.0.1 release"
git tag v1.0.1 && git push origin main --tags
# wait for .github/workflows/release.yml to publish the GitHub Release
make release-publish VERSION=v1.0.1
```

The old `.github/workflows/book.yml build-deploy` step is gone. See `Makefile`'s `release-publish` target and the `book-publish` target it composes.

## Unreleased (v1.x)

Tracked in [PLAN.md §"What's deliberately deferred to post-v1.0"](docs/PLAN.md). The next dev cycle's CHANGELOG entries land here.
