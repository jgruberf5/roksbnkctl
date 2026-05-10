# Sprint 5 — staff engineer issues, resolution notes

Three issues filed: 1 low (Dockerfile ENTRYPOINT polish — handed off,
deferred), 1 medium (terraform UID/GID gotcha — accepted, integrator
note), 1 informational (terraform `--backend k8s/ssh` v1.x deferral —
accepted, clear error at dispatch). All non-blocking for v0.9.

## Issue 1 (LOW — `tools/docker/ibmcloud/Dockerfile` ENTRYPOINT removal) — accepted (deferred)

Sprint 5 staff verified that the Sprint 4 validator Issue 7 concern
about `runOnOpsPod` argv-entrypoint double-up was overly conservative:
`kubectl exec` runs the supplied command directly inside the running
container's filesystem; the image's ENTRYPOINT does NOT prepend at exec
time (that only applies at container start, and the ops pod's
`command:` already overrides it to `sleep infinity` per
`k8s_install.yaml`). So argv flows through verbatim from
`runOnOpsPod`.

For `runAsJob`, `Container.Command` does override the image's
ENTRYPOINT, but staff added a `jobToolCmdOverride` map for tools that
need the full `Command + Args` shape (e.g., the new `roksbnkctl`
self-exec dns-probe Job). `iperf3` and `ibmcloud` keep the legacy
shape; user-visible behaviour unchanged.

The Dockerfile change (drop `ENTRYPOINT ["ibmcloud"]`) is **optional
polish**. Sprint 5 ships with the `jobToolCmdOverride` shim as the
sufficient interim resolution. If a future sprint drops the Dockerfile
ENTRYPOINT, the docker backend's `resolveDockerImageAndArgv` fallback
will need to prepend the tool binary explicitly when the image has no
ENTRYPOINT.

**Status**: ⏸ accepted (interim shim sufficient; Dockerfile change
deferred — not blocking v0.9)

## Issue 2 (MEDIUM — terraform `--backend docker` UID/GID gotchas) — accepted (integrator note for live verification)

The terraform docker backend bind-mounts the workspace state dir at
`/state` and pins `--user $(id -u):$(id -g)` so terraform-in-container
writes the state file with host-user ownership. Verified on Linux/WSL2
at code-review time.

Edge cases to be aware of (not regressions in v0.9 — flagged for
future hardening):

- **Windows host (no UID/GID)**: SID-style strings rejected by docker;
  shim falls back to image-default (root). WSL2 sidesteps this. Native
  Windows runners are a v1.x concern.
- **macOS host (Docker Desktop)**: UID-mapping handled by the
  virtualisation layer; no special handling.
- **CI runners (GitHub Actions Linux)**: runner UID typically 1001;
  state files written as 1001:1001 — matches runner user, no issue.
- **Remote docker daemon (DOCKER_HOST=tcp://…)**: bind-mount path is
  daemon-relative, not client-relative. Cross-host docker-tf is
  unsupported in v0.9; a clear error in `dockerTerraformExec` would
  help — file for v1.x.

**For the v0.9 release manual sign-off** (per the validator's new
`docs/E2E_TEST.md` §"v0.9 release checklist" item 3): when running
`roksbnkctl up --backend docker` against a real IBM Cloud workspace,
verify (a) state file is host-user-owned post-apply, and (b)
re-running `roksbnkctl up --backend local` afterward picks up the
same state cleanly. Document any encountered edge cases in the e2e log.

**Status**: ✅ accepted (code-review verified; live e2e is integrator
sign-off territory)

## Issue 3 (INFORMATIONAL — terraform `--backend k8s` and `--backend ssh:<target>` deferred to v1.x) — accepted

PRD 03 §"State concerns" + PLAN.md Sprint 5 row 8 explicitly defer
non-local terraform backends beyond docker to v1.x. `runTerraformLifecycleDocker`
errors clearly when the user passes `--backend k8s` or `--backend ssh:<target>`
with a message pointing at PRD 03 §"State concerns" and recommending
`--backend local` (host) or `--backend docker` (containerised) instead.

**Status**: ✅ accepted (clear error at dispatch site; tracked for v1.x)

## Integrator-side spot-checks

Verified the staff's claims against the landed code:

- **Sprint 4 polish carry-over 4a (Issue 7)**: `runOnOpsPod` comment at
  `internal/exec/k8s.go:189-194` correctly explains that exec doesn't
  prepend ENTRYPOINT. `jobToolCmdOverride` map landed for the
  `roksbnkctl` self-exec Job mode (used by the dns-probe k8s path).
- **Sprint 4 polish carry-over 4b (Issue 3)**: `SetSSHClientFactory`
  seam at `internal/exec/ssh.go:79-117`. `remoteClient` interface is
  the minimum subset SSHBackend uses (Run + Close); production
  `*remote.Client` satisfies it natively (no production-side change).
  Validator's `ssh_wrapper_test.go` exercises the seam.
- **Sprint 4 polish carry-over 4c (`:dev` on main)**: validator
  landed this in `.github/workflows/tools-images.yml`. See
  `resolved_sprint5_validator.md` Issue 6.

## Summary

3 issues filed; 1 deferred polish (Dockerfile ENTRYPOINT), 1 accepted
with integrator-checklist note (terraform UID/GID), 1 informational
v1.x deferral (terraform k8s/ssh backends). All Sprint 4 carry-overs
landed cleanly. Build, vet, gofmt, and full test suite green.
