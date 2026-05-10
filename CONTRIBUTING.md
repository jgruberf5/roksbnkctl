# Contributing to roksbnkctl

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
