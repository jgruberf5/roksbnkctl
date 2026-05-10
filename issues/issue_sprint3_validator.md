# Sprint 3 — validator issues

Format matches Sprint 2. `Severity: roadmap` is reserved for non-blocking
forward-looking observations; `low/medium/high/blocker` for actionable
findings.

## Issue 1: validator + staff parallel land matched cleanly

**Severity**: informational
**Status**: ✅ resolved at validator-run time

**Description**: Validator deliverables are `internal/cred/resolver_test.go`
and `internal/exec/{redact,local,docker,docker_integration,audit}_test.go`,
testing the packages staff agent owns (`internal/cred`, `internal/exec`).
Staff and validator run in parallel per the Sprint 3 dispatch model; in
this run the staff implementation landed in the working tree before
validator finished writing its tests, so all assertions ran green
end-to-end without any post-merge reconciliation.

Specifically:
- `Resolver{Workspace, NonInteractive}.IBMCloudAPIKey(ctx)` matches staff's signature
- `Credentials.DockerArgs(tempDir) (envArgs, mountArgs, cleanup, err)` matches
- `Backend` interface + `RunOpts` match
- `NewRedactor(w, secrets)` returning an `io.Writer` that's also `io.Closer` for end-of-stream flush matches
- `ResolveBackend("local")` and `ResolveBackend("docker")` registry lookup works
- `--backend` CLI flag is wired (B10 docker prelim runs in dry-run with the flag accepted by cobra)

**Files affected**: `internal/cred/resolver_test.go`,
`internal/exec/{redact,local,docker,docker_integration,audit}_test.go`

**Resolution**: no action — verification gates all green at validator
report time. If a future staff refactor changes any of these signatures,
the failing tests will be the canary.

## Issue 2: tests assume a specific Credentials.DockerArgs signature

**Severity**: low
**Status**: open (informational)

**Description**: The docker-backend tests in `internal/exec/docker_test.go`
assume `Credentials.DockerArgs(tempDir string) (envArgs, mountArgs []string, cleanup func(), err error)`
per `prompts/sprint3/staff.md` Priority 2. If the staff agent chooses a
different shape (e.g. returns a single struct, or splits env-vs-mount
into separate methods), these tests will need a small refactor — the
*assertions* (no `KEY=value` form, kubeconfig mounts as a single file at
`/root/.kube/config:ro`, no `.kube/` parent-dir mount) are stable; the
plumbing around extracting them changes.

**Files affected**: `internal/exec/docker_test.go`
**Proposed fix**: integrator notices any signature drift during merge and
nudges the test plumbing without touching the assertions.

## Issue 3: redactor test assumes io.Closer for end-of-stream flush

**Severity**: low
**Status**: open (informational)

**Description**: `internal/exec/redact_test.go`'s `flush()` helper calls
`Close()` if the redactor implements `io.Closer`. PRD 04 / staff.md
Priority 3 doesn't mandate a specific flushing API — buffering across
writes is required, but how the trailing buffer is drained is left open
("a wrapping `io.Writer` with regex-based redaction"). If the staff
implementation chooses a different flush API (a `Flush()` method, or
auto-flush on every write boundary), the test helper needs updating.

**Files affected**: `internal/exec/redact_test.go`
**Proposed fix**: validate during integration; rename `flush()` body to
match staff's API. The assertions themselves don't change.

## Issue 4: integration tests for docker backend run argv-as-image-then-cmd

**Severity**: low
**Status**: open (informational)

**Description**: `docker_integration_test.go`'s `BusyboxEcho` test passes
`[]string{"busybox:latest", "echo", "hello-from-docker"}` to `Backend.Run`,
assuming the docker backend extracts argv[0] as the image name. PRD 03
§"Backend interface" leaves the image-selection mechanism unspecified —
staff might choose a `RunOpts.Image` field instead, in which case the
test will need that field set explicitly.

The cleanest API is probably `RunOpts.Image string` (analogous to how
SSH backend has `RunOpts.Target string` implicitly via the `ssh:<target>`
spec). Flagging here so the integrator can align the test with whichever
shape staff picks.

**Files affected**: `internal/exec/docker_integration_test.go`
**Proposed fix**: minor mechanical edit during integration if needed.

## Issue 5: cred-audit test exercises only the local backend at unit tier

**Severity**: roadmap
**Status**: open (forward-looking)

**Description**: `internal/exec/audit_test.go` runs the audit assertions
(no leak in argv / parent env / wrapped output) via the local backend
only. The docker tier of the audit lives in `docker_integration_test.go`
behind the `integration` tag. The K8s and SSH tiers are out of scope this
sprint (Sprint 4 ships those backends). When Sprint 4 lands the SSH
backend, the cred-audit suite extends with `TestCredAudit_NoLeakInSSHWrapper`
(asserting the wrapper script's `trap 'rm' EXIT` actually fires); when
Sprint 5 lands the K8s backend, `TestCredAudit_NoLeakInPodSpec` asserts
the secret never appears in `kubectl get pod -o yaml`.

**Files affected**: forward-looking
**Proposed fix**: extend `audit_test.go` per-sprint as backends ship.
PRD 04 §"Acceptance criteria" item 5 lists the full surface — each
new backend brings its own audit case.

## Issue 6: tools-image workflow doesn't gate on Dockerfile changes for PRs

**Severity**: roadmap
**Status**: open (intentional design choice)

**Description**: `.github/workflows/tools-images.yml` triggers on tag
pushes only (`tags: ['v*']`) — not on PRs that modify
`tools/docker/<image>/Dockerfile`. A PR that breaks the ibmcloud
Dockerfile will land on `main` without the build job catching it; the
breakage surfaces only at the next tag push.

The trade-off: building both images on every PR adds ~5 minutes of CI
wall time and consumes GHCR quota. For a repo that releases roughly
monthly, the tag-only path is reasonable; if a Dockerfile change ships
without a quick local `cd tools/docker && make build-all` smoke test,
the breakage waits until release.

A future improvement: build (don't push) on PRs that touch
`tools/docker/**`, push only on tags. Keeps CI under quota while
catching Dockerfile syntax breaks. PLAN.md Sprint 5 fits this
naturally (image versioning + release infrastructure work).

**Files affected**: `.github/workflows/tools-images.yml`
**Proposed fix**: defer to Sprint 5; track here for visibility.

## Issue 7: Phase B10 e2e step — `--backend docker` flag confirmed wired

**Severity**: informational
**Status**: ✅ resolved (staff Priority 8 landed)

**Description**: `scripts/e2e-test.sh phase_B`'s B10 step invokes
`roksbnkctl ibmcloud --backend docker iam oauth-tokens`. Staff.md
Priority 8 introduces the `--backend` persistent CLI flag; verified at
validator-run time that `internal/cli/root.go` and `internal/cli/cluster.go`
have been refactored to accept and dispatch on `--backend`. Dry-run of
the e2e script renders B10 cleanly:

```
[12:09:22] → B10 docker backend ibmcloud iam (dry-run)
[12:09:22]   cmd: /bin/true -w e2e ibmcloud --backend docker iam oauth-tokens
```

The integration tier (`internal/exec/docker_integration_test.go`'s
`TestIntegration_DockerBackend_NoLeakInInspect`) ran live against a real
docker daemon and asserted that the IBMCLOUD_API_KEY value never appears
in `docker inspect` output — the security-spine acceptance criterion
from PRD 04 is met for Sprint 3's docker scope.

**Files affected**: none — this is the verification record.
**Resolution**: integrator merges as-is.
