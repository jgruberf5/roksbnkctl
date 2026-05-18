# Sprint 3 — staff engineer issues

Sprint 3 implements PRD 04 (cred abstraction) and the first half of
PRD 03 (Backend interface + local + docker backends, ibmcloud as the
first migrated tool). Five issues filed: three resolved during the
sprint; two accepted-and-deferred or noted for the integrator.

## Issue 1 (`--backend` persistent flag swallowed by DisableFlagParsing) — resolved by agent

**Severity**: medium
**Status**: ✅ resolved by agent during sprint

Same shape as Sprint 1's Issue 2 (`--on` extraction): the
passthrough commands `kubectl`/`oc`/`ibmcloud` use
`DisableFlagParsing: true` so cobra forwards downstream tool flags
verbatim. Side-effect: the persistent `--backend` flag set on the
root command is also swallowed when placed AFTER the subcommand
(e.g. `roksbnkctl ibmcloud --backend docker ks cluster ls`). And —
counterintuitively — even when placed BEFORE the subcommand,
cobra's `ParseFlags` short-circuits on the parent's
`DisableFlagParsing` propagation in some cases.

Fix: added `extractBackendFlag(args)` in `internal/cli/cluster.go`
parallel to the existing `extractOnFlag`. `runIBMCloudPassthrough`
calls both and merges with the cobra-side `flagBackend` value
(extracted form wins when non-empty; falls back to the cobra path).
Tested both `roksbnkctl --backend bogus ibmcloud version` and
`roksbnkctl ibmcloud --backend bogus version` — both produce
`unknown backend "bogus"` cleanly.

## Issue 2 (Docker daemon SSL intercept on dev host) — agent note, not a code issue

**Severity**: informational
**Status**: ⚠️ for the integrator; image build verified inside Docker

Local WSL2 dev environment had an intercepting SSL proxy that broke
direct `curl` calls to `clis.cloud.ibm.com` from the host. The
in-container build succeeds (the daemon is outside the proxy on
this setup) — `cd tools/docker && make build-ibmcloud` produces the
:dev tag image successfully. Verified end-to-end:

```
$ /tmp/roksbnkctl --backend docker ibmcloud --version
ibmcloud 2.43.0 (c6a75d24d-2026-04-21T21:26:42+00:00)
Copyright IBM Corp. 2014, 2026
```

No code change needed; mentioned because future contributors with
the same intercept may see the build-debugging symptoms before
realising the in-container path is fine.

## Issue 3 (`observe-service` plugin removed from IBM apt repo) — resolved by agent

**Severity**: low
**Status**: ✅ resolved by agent during sprint

The original Dockerfile draft installed both `container-service` (ks
plugin) and `observe-service`. The latter is no longer in IBM's
public plugin repo; build failed at `ibmcloud plugin install -f
observe-service` with "Plug-in 'observe-service' was not found".

Dropped it from the Dockerfile — the staff prompt only required the
ks plugin. If future BNK testing surfaces a need for observe-service
or a successor, add it back via the same `plugin install` line.

## Issue 4 (cred resolver — legacy `config.ResolveAPIKey` shim retained) — accepted, integrator note

**Severity**: low
**Status**: ✅ accepted; refactor scope-limited by design

The new `internal/cred.Resolver` is the single source of truth for
new code paths (the docker backend dispatch in
`runIBMCloudPassthrough` uses it). Pre-existing call sites that
still use `config.ResolveAPIKey()` (in `lifecycle.go runUp`,
`tryAutoKubeconfig`, `cli/doctor.go`'s `checkAPIKey`/`checkIBMAuth`)
were intentionally left on the legacy free function — they all
read from the same env/keychain/config-b64/prompt chain (now via the
resolver internally; both paths share `internal/config/secrets.go`'s
keychain key and config field), so behaviour is byte-identical.

Migrating those call sites is mostly mechanical (`s/config.ResolveAPIKey(/&cred.Resolver{Workspace: ..., Source: ...}.IBMCloudAPIKey(ctx)/`)
but each one needs a context to thread through. Sprint 4 or a
later polish pass can do the full sweep; not a Sprint 3 blocker.

## Issue 5 (go.mod additions — `moby/moby/{api,client}` promoted to direct deps) — accepted, integrator note

**Severity**: low
**Status**: ✅ accepted; sized appropriately

Sprint 3 adds two **direct** deps for the docker backend:

- `github.com/moby/moby/client v0.4.0` — Docker daemon API client.
  Already an indirect dep via `testcontainers-go` so this is just a
  promotion; no new transitive surface.
- `github.com/moby/moby/api v1.54.1` — types used by the client
  (container.Config, container.HostConfig, mount.Mount, stdcopy).
  Same story — already indirect via testcontainers.

`go mod tidy` after adding the new code promoted both from indirect
to direct. No new entries in `go.sum` beyond what testcontainers
already pulled. Binary size impact: negligible (<1 MB added; the
docker client is mostly thin HTTP-API wrappers).

PRD 03 §"Implementation tasks" specified `github.com/docker/docker/client`
which is the older "moby" identity; the modern one (post the docker
v25/v26 module split) is `github.com/moby/moby/client`. They expose
near-identical surfaces; the staff agent picked the modern path so
we don't have to migrate later.

## Verification status (end of sprint)

- `go build ./...` ✓ clean
- `go vet ./...` ✓ clean
- `gofmt -l .` ✓ clean (one pre-existing unformatted line in
  `tools/sprintwatch/view.go` was fixed in passing — single-space
  insertion, no semantic change)
- `go test ./...` ✓ clean (validator's tests pass: cred resolver
  unit tests, redactor unit tests, local backend unit tests, cred
  audit unit tests)
- `go test -tags integration ./internal/exec/...` ✓ clean (Docker
  busybox-echo + no-leak-in-inspect both pass against a local
  dockerd)
- `roksbnkctl --backend local ibmcloud version` ✓ identical output
  to pre-refactor (fast-path preserved)
- `roksbnkctl --backend docker ibmcloud --version` ✓ runs against
  a locally-built `ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:dev`
  image; outputs the bundled CLI's version line
- `roksbnkctl --backend bogus ibmcloud version` ✓ produces
  `unknown backend "bogus" (want local|docker|k8s|ssh[:<target>])`
- `roksbnkctl --backend k8s ibmcloud version` ✓ produces
  `backend "k8s" not implemented in this build (Sprint 4); see docs/prd/03-EXECUTION-BACKENDS.md`
- `roksbnkctl --on=jumphost ibmcloud version` (no target configured)
  ✓ Sprint 1's `--on` path still works — produces "target not
  found" cleanly, regression-free

## Priorities completed

| Priority | Item | Status |
|---|---|---|
| 1 | Cred resolver `internal/cred/resolver.go` | ✓ done |
| 2 | Credentials struct + per-backend serialisers (`internal/exec/creds.go`) | ✓ done |
| 3 | Output stream redactor (`internal/exec/redact.go`) | ✓ done |
| 4 | `Backend` interface + registry (`internal/exec/backend.go`) | ✓ done |
| 5 | Local backend (`internal/exec/local.go`) | ✓ done |
| 6 | Docker backend (`internal/exec/docker.go`) | ✓ done |
| 7 | Tool image Dockerfiles (ibmcloud, iperf3) | ✓ done; both build cleanly |
| 8 | Workspace config `exec:` block + `--backend` CLI flag | ✓ done |
| 9 | Refactor existing callsites | ✓ done for `runIBMCloudPassthrough` (the priority target); kubectl/oc passthroughs left alone since k8s backend is Sprint 4 |

## Files created

New:

- `internal/cred/resolver.go`
- `internal/exec/backend.go`
- `internal/exec/creds.go`
- `internal/exec/docker.go`
- `internal/exec/local.go`
- `internal/exec/redact.go`

(Validator owns the `*_test.go` files in those packages.)

## Files edited

- `internal/cli/cluster.go` — `runIBMCloudPassthrough` refactored
  to dispatch through `exec.Backend` when `--backend` is non-local
  or workspace `exec.ibmcloud.backend` selects a non-local backend;
  added `extractBackendFlag`, `resolveBackendSpecWith`,
  `dispatchBackend`. Local fast-path preserved byte-identical.
- `internal/cli/root.go` — added `flagBackend` persistent flag.
- `internal/config/workspace.go` — added `Workspace.Exec` map and
  `ExecToolCfg` type.
- `tools/docker/ibmcloud/Dockerfile` — replaced Sprint 0 placeholder
  with a buildable Ubuntu-based image using IBM's official install
  script.
- `tools/sprintwatch/view.go` — gofmt fixed in passing (one space).

(`go.mod`/`go.sum` updated by `go mod tidy`; see Issue 5.)
