# Sprint 4 — staff engineer issues

Sprint 4 implements the second half of PRD 03 (k8s + ssh execution
backends, the in-cluster ops pod, iperf3 SCC fix, iperf3 + ibmcloud
backend selection, doctor extensions) plus the four polish carry-overs
from Sprint 3 (legacy `config.ResolveAPIKey` migration, `:dev` →
version-pinned tag, 126/127 backend-failure split, per-tool default
backend map).

Three issues filed: 1 informational (validator coordination), 2
agent-resolved-with-followup-noted.

## Issue 1 (iperf3 Dockerfile lacks `USER 1000`) — handed off to integrator/validator

**Severity**: medium
**Status**: ⚠️ filed for the integrator; staff agent doesn't own
`tools/docker/iperf3/Dockerfile` per the prompt's scope rules.

The OpenShift `restricted-v2` SCC fix in `internal/k8s/iperf3.go`
sets `RunAsNonRoot: true` + `RunAsUser: 1000` on the pod's
`securityContext`. On plain k8s (kind, minikube, vanilla
upstream), kubelet enforces `RunAsNonRoot` against the running
uid: a container that starts as uid 0 (root) — which is what the
default `networkstatic/iperf3:latest` image does — gets blocked
with `container has runAsNonRoot and image will run as root`.

OpenShift sidesteps this because its SCC admission injects a
project-allocated uid, overriding the manifest. Plain k8s users
need either:

- (preferred) the bundled `ghcr.io/jgruberf5/roksbnkctl-tools-iperf3`
  image with a `USER 1000` directive in `tools/docker/iperf3/Dockerfile`,
  OR
- a workspace-config override that picks an image already running
  as non-root.

The current `tools/docker/iperf3/Dockerfile` (4 lines) doesn't set
`USER`. Adding `USER 1000` after the `apk add` line is a one-line
fix. Staff agent did not modify it because the prompt's scope rules
exclude `tools/docker/**` for the staff role this sprint (validator
owns the dockerfiles + image-build CI workflow).

A workaround is in place in `internal/k8s/iperf3.go`: the pod's
`securityContext.RunAsUser` is pinned to `1000`, with a code
comment explaining that the bundled tools image is the only one
that's guaranteed to honour this on plain k8s. Users on plain k8s
who hit "container has runAsNonRoot…" should set
`test.throughput.image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:<v>`
in their workspace config (once the image is published) or build it
locally with `cd tools/docker && make build-iperf3`.

**Action for integrator**: add `USER 1000` to
`tools/docker/iperf3/Dockerfile` and update CI to publish the
image alongside the ibmcloud image. Validator's image-build CI
workflow can extend to cover iperf3.

## Issue 2 (`:dev` tag — version-pinning landed; `:dev` fallback retained for dev builds) — resolved by agent, integrator note

**Severity**: low
**Status**: ✅ resolved by agent during sprint; small follow-up
noted for the validator.

Per the polish carry-over (Sprint 3 tech-writer Issue 8): the
`internal/exec/docker.go::toolImages` map previously hard-coded
`:dev` as the tag, which broke `go install ./cmd/roksbnkctl` on a
fresh host because CI doesn't publish `:dev` (only `:latest` and
`:<git-tag>`).

Fix landed in `internal/exec/docker.go`:

- Added `toolImageTag()` resolver. Reads the binary's build-time
  `Version` (set via ldflags) through a package-level seam
  `toolImageTagFn`. `internal/cli/root.go::init()` wires this to
  `cli.Version`.
- A tag-released binary (`Version="v0.10.0"`) pulls
  `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v0.10.0`. A `dev`
  build pulls `:dev`.
- `internal/cli/ops.go::opsImage()` uses identical logic so the k8s
  ops pod runs the same tag the docker backend would.

Note for validator: the `:dev` tag is still required for the local
development workflow (`tools/docker/Makefile`'s build target tags
the image `:dev`). If the validator's Sprint 4 tools-image CI
workflow adds a `:dev` push on `main` so a fresh `go install` from
HEAD works without a local docker build, that's a small UX
improvement; otherwise users on HEAD `go install` need to either
(a) install a tagged release or (b) `cd tools/docker && make
build-all` first. Documented in code comments at
`internal/exec/docker.go:60-79`.

## Issue 3 (SSH wrapper-script env fallback — edge cases on tcsh / unusual remote shells) — accepted

**Severity**: low
**Status**: ✅ accepted; documented for integrator awareness

The SSH backend's wrapper-script env fallback (PRD 04 §"SSH"
fallback path) writes a POSIX `sh` wrapper that uses `set -a` to
auto-export sourced KEY=VALUE pairs. Works on bash/dash/ash/zsh
(every Ubuntu-default shell). Doesn't work on tcsh/csh because
those have entirely different syntax for env-var sourcing.

Sprint 4 explicitly invokes `sh wrapperPath` (not `$SHELL`) so the
remote's login shell is irrelevant — we use whichever `sh` is on
PATH (always present on Ubuntu). PRD 03 §"SSH" only spec'd Ubuntu
support for v1 (RHEL/Alpine deferred to 3.x), so the tcsh/csh
edge case isn't a real-world risk for the Sprint 4 surface.

Documented in code comments at `internal/exec/ssh.go::runViaWrapper`.

## Verification status (end of sprint)

- `go build ./...` ✓ clean
- `go vet ./...` ✓ clean
- `gofmt -l .` ✓ clean
- `go test ./...` ✓ clean (validator's tests pass; cred-audit, local
  backend, docker backend, redactor, config-package tests all green)
- `roksbnkctl --backend bogus ibmcloud version` ✓ produces
  `unknown backend "bogus"` (regression check passed)
- `roksbnkctl --backend docker ibmcloud --version` ✓ Sprint 3 path
  unbroken (regression check passed)
- `roksbnkctl --on jumphost ibmcloud --version` ✓ legacy `--on` path
  unbroken — independent code path until PLAN.md Sprint 5+
  consolidation
- `roksbnkctl ops install/show/uninstall` ✓ compiles and dispatches;
  end-to-end against a kind cluster left for the integrator (no
  local kind available in the sprint VM)
- `roksbnkctl ibmcloud --backend k8s iam oauth-tokens` ✓ compiles +
  dispatches via the long-lived ops pod path; live verification
  against a kind cluster (with `ops install` first) is for the
  integrator
- `roksbnkctl ibmcloud --backend ssh:jumphost ks cluster ls` ✓
  compiles + dispatches; live verification on a fresh Ubuntu
  jumphost is for the integrator (the bootstrap path's
  `lsb_release -is` and `sudo -n apt-get install` chains are
  validator-test territory)
- `roksbnkctl test throughput --backend k8s` ✓ wired; the in-cluster
  client-as-Job path runs through `K8sBackend.runAsJob`
- `roksbnkctl doctor --backend k8s` ✓ compiles and dispatches;
  cluster-side checks need a real cluster
- `roksbnkctl doctor --backend ssh:<target>` ✓ same — needs a real
  jumphost for end-to-end

## Priorities completed

| Priority | Item | Status |
|---|---|---|
| 1a | `internal/exec/k8s_install.yaml` (embedded manifests) | ✓ done |
| 1b | `internal/exec/k8s.go` (long-lived exec + Job paths) | ✓ done |
| 1c | `internal/cli/ops.go` (install/show/uninstall) | ✓ done |
| 1d | iperf3 SCC fix in `internal/k8s/iperf3.go` | ✓ done (note: prompt referenced `internal/test/throughput.go` but the iperf3 server-pod manifest is actually in `internal/k8s/iperf3.go` — fix landed there. See Issue 1 for the Dockerfile USER follow-up.) |
| 2 | `internal/exec/ssh.go` (bootstrap, files, env, TTY, cleanup) | ✓ done |
| 3a | iperf3 backend selection wiring (test.go) | ✓ done |
| 3b | ibmcloud backend selection wiring (cluster.go) | ✓ done |
| 4 | Doctor `--backend k8s/ssh` extensions | ✓ done |
| 5a | Legacy `config.ResolveAPIKey` migration | ✓ done in production code; one shim retained for package-local config-package tests (documented as `Deprecated:`) |
| 5b | `:dev` tag → version-pinned in toolImages | ✓ done |
| 5c | 126/127 backend-failure split | ✓ done — docker now splits, k8s + ssh emit per-PRD codes, local documented as "no 126 case applies" |
| 5d | Per-tool default backend map | ✓ done (in resolveBackendSpecWith) |

## Files created

New:

- `internal/exec/k8s_install.yaml` (embedded manifests)
- `internal/exec/k8s_install.go` (`//go:embed` declaration + accessor)
- `internal/exec/k8s.go` (`K8sBackend` — long-lived ops pod via
  SPDY exec; one-shot Job path for ephemeral tools)
- `internal/exec/ssh.go` (`SSHBackend` — bootstrap, file
  materialisation, env propagation with SetEnv → wrapper-script
  fallback, trap-on-EXIT cleanup)
- `internal/cli/ops.go` (`roksbnkctl ops install/show/uninstall`)
- `internal/cli/doctor_backend.go` (per-backend doctor checks)

## Files edited

- `internal/k8s/iperf3.go` — iperf3 pod SCC fix (PodSecurityContext
  + container SecurityContext correctly populated for restricted-v2).
- `internal/exec/docker.go` — 126/127 split for backend-side errors;
  toolImages tag pinned to binary's Version (with `:dev` fallback);
  `SetToolImageTag` seam.
- `internal/exec/local.go` — code comment update for the 126/127
  split (no behavioural change — the local backend has no 126 case).
- `internal/cli/root.go` — `--bootstrap` persistent flag for the
  SSH backend; `execbackend.SetToolImageTag` wiring in init().
- `internal/cli/cluster.go` — per-tool default backend map
  (`perToolDefaultBackend`); `dispatchBackend` extended for k8s
  long-lived sentinel + ssh target sentinel; ibmcloud passthrough
  routes k8s/ssh/docker through dispatchBackend; legacy
  `config.ResolveAPIKey` migrated to `cred.Resolver`.
- `internal/cli/cluster_phase.go` — same migration.
- `internal/cli/cos.go` — same migration.
- `internal/cli/init.go` — same migration.
- `internal/cli/lifecycle.go` — same migration.
- `internal/cli/test.go` — backend-aware iperf3 client dispatch
  (k8s Job path; ssh path; local fallback); rejects
  `--backend docker` for iperf3 with a clear remediation message.
- `internal/cli/meta.go` — doctor `--backend` flag plumbing.
- `internal/cli/remote.go` — `SetSSHTargetResolver` wiring so the
  SSH backend reuses the legacy `--on` path's tf-output-aware
  signer resolution.
- `internal/doctor/doctor.go` — `cred.Resolver` migration for
  `checkAPIKey` + `checkIBMAuth`.
- `internal/config/secrets.go` — `ResolveAPIKey` retained but
  marked `Deprecated:` and documented as test-only.

## Items deferred / handed off

- Tools-image build (`tools/docker/iperf3/Dockerfile` `USER 1000`):
  filed as Issue 1; integrator + validator scope.
- Tools-image CI publish of `:dev` on main (so a fresh `go install`
  from HEAD works without a local docker build): filed as Issue 2;
  validator scope.
- The Sprint 5+ consolidation of `--on` and `--backend ssh:<target>`
  (PLAN.md notes both should fold into a single SSH path) is left
  alone — Sprint 4 keeps both code paths independent so Sprint 1's
  `--on` regression-check still passes byte-identically.

## Coordination with parallel agents

- Validator's argv-builder unit tests for k8s + ssh: the backend
  shapes' public surface (RunOpts, env-sentinel keys
  `ROKSBNKCTL_K8S_LONG_LIVED` + `ROKSBNKCTL_SSH_TARGET`) is stable;
  validator can exercise them by constructing RunOpts directly.
- Validator's kind-based integration tests can target the install
  manifest by calling `execbackend.K8sInstallYAML()` and applying
  via the existing `decodeOpsManifests` shape (private, but the
  validator can test through `roksbnkctl ops install` instead).
- Validator's k8s + ssh cred-leak audit can extend
  `internal/exec/audit_test.go` — the new K8sBackend / SSHBackend
  register through the same `Register()` call as local + docker, so
  the existing audit harness covers them once the validator wires
  them in.
- Architect's book chapters: nothing in `book/src/` was touched.
- The `cspell.json` `SCC` allowance is the validator's territory
  (Sprint 3 tech-writer flagged `SSC→SCC`); staff didn't touch
  cspell.json.
