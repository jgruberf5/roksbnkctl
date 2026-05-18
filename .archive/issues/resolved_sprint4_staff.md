# Sprint 4 — staff engineer issues, resolution notes

Three issues filed: 1 medium handed off, 2 resolved by the staff agent
during the sprint with integrator follow-up notes. The medium has been
resolved by the integrator in this pass.

## Issue 1 (`tools/docker/iperf3/Dockerfile` lacks `USER 1000`) — resolved by integrator

Added `USER 1000` to `tools/docker/iperf3/Dockerfile` after the
`apk add` line, with a comment cross-referencing
`internal/k8s/iperf3.go`'s `RunAsNonRoot: true` policy.

The two-line addition closes the loop staff opened: plain k8s users
running the iperf3 fixture pod no longer hit
`container has runAsNonRoot and image will run as root` because the
image now boots as uid 1000 by default. The pod's
`securityContext.RunAsUser: 1000` (set in `internal/k8s/iperf3.go`)
matches.

The validator's `tools-images.yml` workflow already publishes
`roksbnkctl-tools-iperf3` on tag pushes; the next tag will ship the
non-root image to ghcr.io.

**Status**: ✅ resolved (integrator-applied; tools/docker/iperf3/Dockerfile
+ internal/k8s/iperf3.go round-trip is now consistent)

## Issue 2 (`:dev` tag — version-pinning landed; `:dev` fallback retained for dev builds) — resolved by agent, integrator note

Staff landed the version-pinning fix in `internal/exec/docker.go::toolImageTag`
+ `internal/cli/ops.go::opsImage`. A `dev` build pulls `:dev`; a
tag-released binary (`Version="v0.10.0"`) pulls `:v0.10.0`. The local
`tools/docker/Makefile` build path remains `:dev`-tagged.

The follow-up — having CI also publish `:dev` on `main` pushes so a
fresh `go install ./cmd/roksbnkctl` from HEAD pulls a working image —
is reasonable but small. Tracked here for Sprint 5 polish; not blocking
v0.9. Today, HEAD installers either install a tagged release or
`cd tools/docker && make build-all` first.

**Status**: ✅ resolved (staff); ⏸ tracked for Sprint 5 polish (the
optional `:dev`-on-main publish)

## Issue 3 (SSH wrapper-script env fallback — tcsh / unusual remote shells) — accepted

The SSH backend invokes `sh wrapperPath` (not `$SHELL`) so the remote's
login shell is irrelevant. PRD 03 §"SSH" only commits to Ubuntu/POSIX-sh
in v1; tcsh/csh edge cases are real-world non-issues for the Sprint 4
surface.

**Status**: ✅ accepted (documented at `internal/exec/ssh.go::runViaWrapper`)

## Integrator additions (Sprint 4 polish carry-overs verification)

Spot-checked the four polish carry-overs from Sprint 3:

- **5a. Legacy `config.ResolveAPIKey` migration** — confirmed via
  `grep -rn "config\.ResolveAPIKey" internal/`. Migration complete in
  production code; only the `Deprecated:`-marked shim remains in
  `internal/config/secrets.go` for the package-local test path. ✓
- **5b. `:dev` → version-pinned tag** — `toolImageTag()` reads
  `cli.Version` via `SetToolImageTag`; ops pod uses identical resolution
  via `opsImage()`. ✓
- **5c. 126/127 split** — `docker.go` returns 127 for daemon-down /
  image-pull failures, 126 for container-create-then-failed. `k8s.go`
  uses `k8sExitFailedToStart=127` and `k8sExitStartedThenFailed=126` per
  PRD 03. `ssh.go` returns 127 for connection refused / target
  unreachable, 126 for sudo / non-Ubuntu / wrapper-spawn failures. ✓
- **5d. Per-tool default map** — `internal/cli/cluster.go::perToolDefaultBackend`
  maps `iperf3→k8s`, `ibmcloud→local`, `terraform→local`. Chapter 18
  documents this; tests in `internal/exec/audit_test.go` exercise the
  resolution. ✓

## Summary

3 issues filed; Issue 1 (medium) resolved by integrator in this pass;
Issues 2 + 3 self-resolved by agent / accepted. All four PRD 03 polish
carry-overs verified landed. Build, vet, gofmt, and full test suite
green.
