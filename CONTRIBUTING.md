# Contributing to roksbnkctl

## Setting up a contributor host

This guide assumes a clone of the repo on a Linux or macOS host with Go 1.25+, git, make, and docker already installed (the "build tools" assumed-present prerequisites).

For Ubuntu/Debian hosts, the repository ships a one-shot installer for everything else `make build` / `make release` / `scripts/e2e-test-full.sh` rely on:

```bash
./install_build_dependencies.sh
```

What it installs (idempotent — re-running skips anything already present):

- `terraform` (HashiCorp apt repo) — required for the binary's local backend
- `helm` 3 (official apt repo) — required at `roksbnkctl up` time; terraform's `null_resource` + `local-exec` provisioners for the `cert_manager` / `flo` / `cne_instance` modules shell out to host `helm`
- `ibmcloud` CLI + the `kubernetes-service` and `cloud-object-storage` plugins — required for the `roksbnkctl ibmcloud …` passthrough with `--backend local` and for e2e Phase B/I
- `oc` (Red Hat OpenShift CLI, from Red Hat's mirror tarball) — required for the e2e flow's Phase B5 step (`roksbnkctl oc whoami` passthrough). The everyday `roksbnkctl k *` verbs don't need it; the passthrough does
- `jq`, `unzip`, `gnupg`, `openssh-client`, `python3` — dev utilities the e2e scripts and Makefile targets shell out to

What it deliberately does NOT install:

- `mdbook` / `mdbook-pandoc` / `pandoc` / `texlive` / `mermaid-cli` — bundled in `tools/docker/mdbook/Dockerfile`; build once via `make -C tools/docker build-mdbook`
- `goreleaser` — pulled at run-time from `goreleaser/goreleaser:latest`
- `iperf3` — bundled in `tools/docker/iperf3/`, runs via `--backend k8s`
- `kubectl` — sprint 2 internalised the surface; install on host only if you want to shell out for cred-audit assertions in `scripts/e2e-test-backends.sh`

For other Linux distributions (RHEL, Fedora, Arch, openSUSE, Alpine, …) and for macOS, the script doesn't auto-detect — install the prereqs manually per the per-OS recipes in [chapter 4 of the book](book/src/04-installation.md#installing-prerequisites).

End users running a pre-built `roksbnkctl` binary from the GitHub Release page do **not** need this script — they only need `terraform` and (optionally) the passthrough CLIs. See the book's installation chapter for that path.

## Running tests

The unit suite lives under `internal/...` and runs without any external
dependencies — no IBM Cloud credentials, no Terraform, no kubectl.

```bash
go test ./...                  # full suite (the same thing CI runs)
make test-short                # fast subset; -short skips slow tests
go test -short ./...           # equivalent to make test-short
```

CI runs `go test -race ./...` on Linux + macOS; locally you can add
`-race` if you suspect a data race, but it isn't required for the
pre-commit hook (the hook stays under 30s on a clean tree).

The long-running end-to-end test (`scripts/e2e-test.sh`) is documented
separately below — it provisions real cloud resources and is **never**
run in PR CI.

### Running integration tests

`internal/remote/integration_test.go` spins up a real `openssh-server`
container via `testcontainers-go` and exercises the SSH client end-to-end.
Gated behind a build tag so unit tests stay fast:

```bash
make test-integration              # equivalent to:
go test -tags integration -timeout 5m ./internal/remote/...
```

Requires Docker (the container is launched dynamically). CI runs this
on Linux only (`integration` job in `.github/workflows/ci.yml`); macOS
GitHub runners don't ship Docker so the job is skipped there. Run
locally before pushing SSH-related changes — the integration tests
catch real bugs the unit suite can't (the Sprint 1 ctx-cancel fix in
`internal/remote/ssh.go` was found this way).

### Running golden tests (live cluster)

`internal/k8s/golden_test.go` validates PRD 02's byte-equivalence
acceptance criterion: `roksbnkctl k get <resource> -o yaml` must match
`kubectl get <resource> -o yaml` for representative resources (Node,
Pod, Service, ConfigMap), modulo necessarily-volatile fields like
`managedFields`, `resourceVersion`, `creationTimestamp`. Gated behind a
`live` build tag so they only run when explicitly invoked:

```bash
make test-live                     # equivalent to:
go test -tags live -timeout 5m ./internal/k8s/...
```

Requirements:

- A real ROKS (or any Kubernetes) cluster reachable via `$KUBECONFIG`
  or `~/.kube/config`. The tests `t.Skip` cleanly when no cluster is
  available — running without one is harmless.
- `kubectl` on `$PATH` for the comparison side. The internalised `k get`
  is what we're validating; `kubectl get` is the reference.
- `roksbnkctl` built and on `$PATH` (or `$ROKSBNKCTL` set to its path).
  The test `exec`s the binary so an unbuilt working tree is detected
  cleanly.

These tests are **not** run in CI (no live cluster available). Run them
locally before tagging `v0.8` (the M2 milestone) — byte-equivalence is
part of PRD 02's acceptance criteria, and a regression in the
`cli-runtime` printer chain wouldn't be caught by the fast unit suite.

### Running cred-audit tests

Sprint 3 (PRD 04) introduces a security-spine regression test that runs
each backend with a known-secret IBM Cloud API key and asserts the value
never appears in any inspection surface — `os.Environ()`, the argv passed
to `Backend.Run`, the captured stdout/stderr, and (for the docker backend)
`docker inspect` output. PRD 04 §"Acceptance criteria" item 5 requires
this; the implementation lives in `internal/exec/audit_test.go`.

```bash
go test -run CredAudit ./...           # every TestCredAudit_* in the tree
make test-cred-audit                   # convenience wrapper for the same
```

A new make target `test-cred-audit` wraps the `go test -run CredAudit`
invocation so the audit can be run as a single quick check before tagging
a release. The unit-tier audit doesn't require a docker daemon; the
docker-side leak check (`TestIntegration_DockerBackend_NoLeakInInspect` in
`internal/exec/docker_integration_test.go`) is gated behind the
`integration` build tag and runs in CI's `docker-backend` job — see
`.github/workflows/ci.yml`.

If you change anything in `internal/cred/` or `internal/exec/`, run the
audit locally before pushing. A red audit blocks a release: a leaked
credential in any backend is a v0.x stop-ship.

### Running kind-based integration tests

Sprint 4 / PRD 03 second half adds a kind-based integration tier for the
K8s backend. The CI `k8s-backend` job in `.github/workflows/ci.yml` spins
an ephemeral kind cluster via `helm/kind-action@v1` and runs the
integration-tagged tests under `internal/exec` and `internal/cli`. To run
locally:

```bash
# Spin a kind cluster (one-time per workstation; reused across runs):
kind create cluster --name roksbnkctl-test

# Point your kubeconfig at it:
kind get kubeconfig --name roksbnkctl-test > ~/.kube/config-kind
export KUBECONFIG=~/.kube/config-kind

# Run the integration tier:
go test -tags integration -timeout 10m ./internal/exec/... ./internal/cli/...
# or:
make test-k8s-integration
```

Tests skip cleanly when no cluster is reachable, so this is safe even on
a runner without kind installed. To point at an existing kind cluster
(or any kube cluster), set `KUBECONFIG` to its config file before running
the tests.

When you're done:

```bash
kind delete cluster --name roksbnkctl-test
```

The integration tests provision their own ops pod (a stand-in for
`roksbnkctl ops install`) and tear it down via `t.Cleanup`. Leaks
indicate a test bug, not a kind problem.

### Running the full e2e

Sprint 6 introduces [`scripts/e2e-test-full.sh`](./scripts/e2e-test-full.sh)
— the one-button runner that chains the baseline driver (Phases A-H)
and the backends driver (Phases I + K + L + L-DNS + M + N) against the
same workspace + cluster. The same script is wired into the manual-
trigger CI workflow at [`.github/workflows/e2e-full.yml`](./.github/workflows/e2e-full.yml).

**Cost & duration**: ~4-6 hours wall time, ~$8-13 of IBM Cloud spend
(cluster + LBs + COS, doubled because Phase N spins a second up/down
cycle to validate cross-backend state portability).

**Cluster behaviour on success**: with `--teardown` the cluster is
torn down after a green run; without it (the default) the cluster is
left up so the integrator can inspect / re-run / kick off a manual
GSLB check.

**Cluster behaviour on failure**: the cluster is *always* left up on
failure so the integrator can inspect the live state before either
re-running or manually tearing down.

**Required env vars**:

```bash
IBMCLOUD_API_KEY=...                        # required
./scripts/e2e-test-full.sh                  # full pass; cluster stays up on success
```

**Optional env vars** (Sprint 6 SSH-target-specific):

```bash
ROKSBNKCTL_E2E_SSH_TARGET=jumphost          # enables Phase I + M5/M6 + N3
ROKSBNKCTL_E2E_SSH_NON_UBUNTU=name          # enables Phase I7 (non-Ubuntu detection)
ROKSBNKCTL_E2E_SSH_NO_NOPASSWD=name         # enables Phase I8 (sudo-password-required)
ROKSBNKCTL_E2E_INIT_BACKEND=docker          # initial backend for Phase N1 (default: local)
```

See `docs/E2E_TEST.md` §"Full e2e (e2e-test-full.sh)" for the env-var
table + per-phase coverage notes.

### Adding a new e2e phase

E2E phases follow the PRD 05 → `scripts/e2e-test-backends.sh` workflow:

1. **PRD update**: extend `docs/prd/05-E2E-TEST-PLAN.md` with the
   new phase's step matrix (table-driven: step ID → command → pass
   criterion). The PRD is the source of truth for what the phase
   asserts; the driver script is the implementation.
2. **Driver implementation**: add `phase_<letter>()` to
   `scripts/e2e-test-backends.sh`, mirroring the shape of the existing
   `phase_K` / `phase_L` / `phase_M` functions. Use the helper
   functions (`step`, `capture`, `assert_contains`,
   `assert_not_contains`) — they handle DRY_RUN gating, logging, and
   colour output uniformly.
3. **Skip rules**: every step that depends on an external resource
   (cluster, SSH target, docker daemon) MUST skip-cleanly with a
   yellow `⊘` rather than failing the phase when the resource is
   missing. Use `[[ -n "$ENV_VAR" && ... ]]` gates around each block.
4. **Dispatch**: add `should_run X && phase_X` to `main()`. Update
   the `PHASE_FROM` default in the config block if your phase needs
   to run before the current default's sort position.
5. **Documentation**: update `docs/E2E_TEST.md` §"Sprint 4 — backend
   matrix driver" to reference the new phase, and the per-phase
   coverage table under §"Full e2e (e2e-test-full.sh)".

### Running scripts/e2e-test-backends.sh locally

Sprint 4 introduces `scripts/e2e-test-backends.sh` — a sibling to
`scripts/e2e-test.sh` that exercises the four-backend matrix introduced
in PRDs 03 + 04. It covers PRD 05 §K (docker), §L (k8s), and §M (cred
audit).

**Pre-requisites** — different per phase:

- **All phases**: a workspace + cluster brought up by a prior
  `scripts/e2e-test.sh` run (Phase D's `roksbnkctl up`). The backends
  driver does NOT bring its own cluster up; it reuses the live one.
- **Phase K (docker)**: a reachable Docker daemon (`docker info` must
  succeed). The `RUN_K6=1` opt-in additionally requires `sudo
  systemctl stop docker` privileges (it stops + restarts the host's
  dockerd to test the no-daemon negative path).
- **Phase L (k8s)**: a Kubernetes cluster reachable via the workspace's
  kubeconfig. kind, ROKS, OpenShift, anything that speaks the kube API
  works.

**Running**:

```bash
# After scripts/e2e-test.sh's Phase D has brought the cluster up:
IBMCLOUD_API_KEY=... ./scripts/e2e-test-backends.sh

# Resume from a specific phase (K, L, or M):
PHASE_FROM=L ./scripts/e2e-test-backends.sh

# Inspect the plan without executing anything (great for review):
DRY_RUN=1 ./scripts/e2e-test-backends.sh

# Opt in to the destructive K6 step (stops + restarts dockerd):
RUN_K6=1 ./scripts/e2e-test-backends.sh
```

Per-phase logs land in `/tmp/roksbnkctl-e2e-backends/<phase>-<ts>.log`
for forensics on failure.

### Running DNS probe unit tests

Sprint 5 (PRD 03 §"DNS probe") replaces the old `net.Resolver`-based DNS
probe with a [miekg/dns](https://github.com/miekg/dns)-based
implementation. The unit tests spin up an in-process miekg `dns.Server`
on a loopback ephemeral port — no external network required, no `dig`
prerequisite.

```bash
# Unit tier (in-process miekg/dns mock server):
go test -tags dnsprobe -run Probe ./internal/test/...

# Integration tier (against 8.8.8.8 + a local stub in parallel):
go test -tags 'integration dnsprobe' -timeout 5m -run IntegrationProbe ./internal/test/...
```

The integration tier's `8.8.8.8` test self-skips when the test host
has no external network reachability (corporate firewalls, air-gapped
runners) — the local-stub test in the same file always runs.

The `dnsprobe` build tag keeps the new tests gated until the staff
agent's miekg-based `Probe` API lands in `internal/test/dns.go`. Once
the integrator merges the staff + validator commits, the tag drops
and the tests become part of the default `go test ./...` suite.

### Testing GSLB scenarios manually

The `--gslb-compare` flow is the v0.9 release-gate manual checklist
item. To validate it against a real F5 BIG-IP Next GSLB record:

```bash
# Bring up a cluster + ops pod (Sprint 4 lifecycle):
roksbnkctl up --auto -w demo --var-file ~/bnkfun/terraform.tfvars
roksbnkctl ops install -w demo

# Probe a GSLB-managed name from local + k8s vantages:
roksbnkctl test dns \
  --target gslb-managed.example.com \
  --type A \
  --server <gslb-vip>:53 \
  --gslb-compare \
  -o json

# Expected: gslb_divergence=true when local and k8s land on different
# F5 BIG-IP Next datacenters; gslb_divergence=false when both vantages
# happen to share the same datacenter routing.

# To force a CI-friendly assertion that GSLB is actually doing
# something (no silent identical-answer regression), add:
roksbnkctl test dns ... --gslb-compare --require-divergence
# exits non-zero when gslb_divergence is false
```

For local development without a GSLB, point `--server` at any anycast
resolver (8.8.8.8, 1.1.1.1) and probe a geo-resolved name like
`www.google.com` — divergence is sometimes visible just from the
different anycast paths local-laptop vs in-cluster takes.

A note on LD8 (Phase L-DNS step 8) target choice: `www.google.com` is
the documented exemplar, but anycast can produce identical answers by
chance. The integrator's manual v0.9 sign-off should use a known-
divergent target — either an F5 BIG-IP Next GSLB record from Phase D's
deployment, or a name with strong DC-affinity DNS like
`www.amazon.com` (Route 53 latency-based routing) — to assert
`gslb_divergence: true` deterministically.

### Building tool images locally

The PRD 03 docker backend pulls per-tool images at runtime:

- `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud` — Ubuntu base + `ibmcloud-cli` + `container-service` plugin
- `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3` — Alpine base + `iperf3`

Released images are built and pushed by
`.github/workflows/tools-images.yml` on every `v*` tag push. For local
development against the docker backend (without waiting on a tag), build
the images yourself via the `tools/docker/Makefile`:

```bash
cd tools/docker
make build-ibmcloud                    # ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev
make build-iperf3                      # ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:dev
make build-all                         # both
make clean                             # remove the local images
```

The default tag is `dev`, which matches what `internal/exec/docker.go`
looks up at runtime when no override is configured. Override via
`TAG=...` if you want to test against a specific version locally.

## Pre-commit hook

`scripts/pre-commit.sh` runs three checks against the working tree:

1. `gofmt -d -l .` — fail if any file is unformatted.
2. `go vet ./...` — fail on any vet finding.
3. `go test -short ./internal/...` — fail on any short-mode unit test.

Install it as your local Git pre-commit hook:

```bash
make pre-commit-install
```

That symlinks `.git/hooks/pre-commit` to the script, so future updates
to the script are picked up automatically — no reinstall needed.

To bypass it for a one-off commit (e.g. a WIP commit on a feature
branch):

```bash
git commit --no-verify
```

CI re-runs all three checks (plus staticcheck and go test on
ubuntu-latest + macos-latest), so `--no-verify` only delays the
feedback loop — it doesn't get the change merged.

## Code style

- **gofmt** is enforced. CI fails when `gofmt -d -l .` produces a
  non-empty diff. Run `gofmt -w .` (or rely on your editor's
  format-on-save) before committing.
- **go vet** is enforced. CI fails on any `go vet ./...` finding.
- **staticcheck** is enforced on Linux and macOS via
  `dominikh/staticcheck-action@v1`. Run `staticcheck ./...` locally
  if you want the same feedback before pushing.
- **Imports** are grouped into three blocks separated by a blank line:
  stdlib first, third-party second, project (`github.com/jgruberf5/...`)
  third. `goimports -local github.com/jgruberf5/roksbnkctl` produces
  this layout automatically.

## Long-running smoke test

The full end-to-end test (`scripts/e2e-test.sh`) provisions a real
ROKS cluster + BNK deployment on IBM Cloud, exercises every roksbnkctl
verb against it, and tears down. It's the canonical "did we break
anything" check before tagging a release.

### Prerequisites
- `IBMCLOUD_API_KEY` env var (or extracted from `~/bnkfun/terraform.tfvars`)
- `~/bnkfun/terraform.tfvars` with cluster + region + RG values
- terraform on PATH
- kubectl, oc, ibmcloud, iperf3 on PATH (Phase 3 plans to remove these
  prereqs — see `docs/prd/03-EXECUTION-BACKENDS.md`)

### Running

```bash
./scripts/e2e-test.sh                       # full pass from scratch
PHASE_FROM=D ./scripts/e2e-test.sh           # resume from phase D
DRY_RUN=1 ./scripts/e2e-test.sh              # show plan without execution
```

### Cost & duration

~3-4 hours wall time. ~$5-10 of IBM Cloud spend per full pass (cluster +
load balancers + COS). The test is **never** run in PR CI — release
branch nightly only, until 3 consecutive nights green, then tag.

## Working on the book

The web book — _Deploying and Testing BIG-IP Next for Kubernetes with
roksbnkctl_ — lives under `book/` and ships matched to each release tag.
Source markdown is at `book/src/`; the build output and TOC are
generated by [mdBook](https://rust-lang.github.io/mdBook/).

### Local preview

```bash
make book-serve              # mdbook serve book/ --open
```

### Adding or extending a chapter

1. Edit `book/src/SUMMARY.md` to add the chapter link (mdBook uses this
   file as the table of contents).
2. Drop a markdown file in `book/src/` matching the link (kebab-case
   filename, `# Chapter title` h1 matching the SUMMARY entry).
3. If your chapter introduces project-specific acronyms or names that
   trigger the cspell warning, add them to `cspell.json`.
4. **Feature PRs include the matching chapter update.** A code change
   that lands a new flag, command, or behavior should land the
   corresponding chapter edit in the same PR. The book CI on PRs runs
   `mdbook test book/` (broken-link check) so xrefs stay sound.

### Style

Prose voice is clipped technical, lower-case prose, code-block-heavy.
Examples should be runnable as written. Cross-reference other chapters
with relative links (e.g. `see [Workspaces](./06-workspaces.md)`) so
mdBook's internal-link checker can verify them.

## Releasing

The release pipeline is driven by `make release` from the repo root.
The single command sequences five steps and aborts on the first
failure:

```
make release
  [1/5] Stamping CHANGELOG.md v1.0.0 date          # 2026-MM-DD → today
  [2/5] Building HTML + PDF book                   # tools/docker/mdbook image
  [3/5] Linting .goreleaser.yml                    # goreleaser/goreleaser:latest
  [4/5] Snapshot build (multi-platform binaries)   # produces dist/
  [5/5] Verifying GitHub Pages is enabled          # gh api, idempotent
```

After the driver returns green, review the diff and cut the tag:

```bash
git add -A && git commit -m "chore: prep v1.0.0 release"
git tag v1.0.0 && git push origin main --tags
```

Pushing `main` triggers `.github/workflows/book.yml` (rebuilds the HTML
book and deploys to the `gh-pages` branch under `/book/`); pushing the
tag triggers `.github/workflows/release.yml` (runs goreleaser
for-real, attaches the PDF via `release.extra_files`, publishes the
GitHub Release).

### Release-time tooling

| Tool | Source |
|---|---|
| `mdbook` + `mdbook-mermaid` + `mdbook-pandoc` + `pandoc` + `texlive-xetex` + `@mermaid-js/mermaid-cli` | Bundled in [`tools/docker/mdbook/Dockerfile`](./tools/docker/mdbook/Dockerfile) — ~6 GB image, build once with `make -C tools/docker build-mdbook`. |
| `goreleaser` | Pulled at run-time from `goreleaser/goreleaser:latest` (~150 MB). No host install needed. |
| `gh` | Required on `PATH` for the `pages-assure` step. Authenticate once with `gh auth login`. |

Override defaults on the command line if needed:

```bash
make release RELEASE_DATE=2026-06-01                            # back-date the CHANGELOG
make release GORELEASER_IMAGE=goreleaser/goreleaser:v2.4.1      # pin a goreleaser version
```

### Individual targets

`make release` is the canonical driver, but each sub-step is also
exposed for iteration:

```bash
make stamp-changelog              # idempotent — no-op if placeholder already gone
make book                         # HTML book only, host install (fast)
make book BOOK_BACKEND=docker     # HTML via docker image (no host mdbook needed)
make book-pdf BOOK_BACKEND=docker # PDF book (docker required — LaTeX + mermaid-cli)
make book-serve                   # live preview at http://localhost:3000
make goreleaser-check             # YAML/schema lint
make goreleaser-snapshot          # multi-platform binary dry-run, writes dist/
make pages-assure                 # check (or enable) GitHub Pages on the repo
```

The snapshot build writes to `dist/`, which is `.gitignore`d.

### Why a docker-based pipeline?

The release-time stack (LaTeX + Chromium + Node.js + Rust + Go) is
weighty enough that asking every contributor to install it on their
host would be unkind. The docker images encapsulate the version pinning
so the release a maintainer cuts in 2026 is reproducible by a
maintainer in 2028 against the same images. For day-to-day book
iteration, `make book` against a host `mdbook` install remains the
fast path — the docker pipeline only runs when producing release
artifacts.

## Sprint execution and the prompts/ folder

Sprint work runs through dispatched sub-agent prompts checked in under
`prompts/sprint<N>/<role>.md`. Each sprint has four parallel roles —
**architect**, **staff**, **validator**, **tech-writer** — whose
prompts live as plain markdown so any future contributor or LLM can
re-dispatch them.

Issues filed by agents during a sprint go to
`issues/issue_sprint<N>_<role>.md`; the integrator (human or LLM
aggregating output) writes resolution notes to
`issues/resolved_sprint<N>_<role>.md`.

See [`prompts/README.md`](./prompts/README.md) for the full pattern,
including the canonical "kicking off Sprint N" checklist.
