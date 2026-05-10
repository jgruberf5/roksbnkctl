# Sprint 3 — validator issues, resolution notes

Seven issues filed: 0 blockers. Most are informational explanations of test design decisions or roadmap entries for Sprint 4+ extensions.

## Issue 1 (validator + staff parallel land matched cleanly) — resolved at validator-run time

By the time validator submitted, all signature contracts (`Resolver`, `Credentials.DockerArgs`, `NewRedactor`, `Backend`, `ResolveBackend`, `--backend` flag) aligned with staff's actual implementation without reconciliation. Tests validate against the shipped surface, not against a stub. Same productive pattern as Sprint 2's mid-flight reconciliation.

**Status**: ✅ resolved (verified)

## Issue 2 (tests assume `Credentials.DockerArgs` signature) — accepted

The integration test signature reads:

```go
envArgs, mountArgs, cleanup, err := creds.DockerArgs(tempDir)
```

If staff later changes the return shape (e.g. adds a fifth value), the tests need updating. This is the unavoidable cost of testing across the API boundary. Acceptable trade-off; tracked here for awareness.

**Status**: ✅ accepted as informational

## Issue 3 (redactor test assumes `io.Closer` for end-of-stream flush) — accepted

The redactor's buffer-and-rescan logic assumes the underlying writer eventually gets closed (or that the redactor is given a buffer-size sentinel). Tests flush via explicit `Close()` calls. If the API later moves to fire-and-forget semantics, these tests need restructuring.

**Status**: ✅ accepted as informational; matches PRD 04's design

## Issue 4 (integration tests run argv-as-image-then-cmd) — accepted

For the docker integration test, the test runner uses argv `["busybox:latest", "echo", "hi"]` rather than passing `Image` separately and `["echo", "hi"]` as argv. The Backend interface places image selection in `argv[0]` for consistency with `local`/`ssh` (where argv[0] is the binary name). Trade-off: callers building docker-specific run shapes need to know argv[0] is treated as image when `Backend.Name() == "docker"`.

**Status**: ✅ accepted; staff's design choice

## Issue 5 (cred-audit unit tier exercises only local backend) — roadmap

The cred-leak audit currently runs at unit tier against the local backend only. Docker backend's leak surface (env-file, container ENV layers, `docker inspect` output) is covered at integration tier. SSH and k8s backends have their own leak surfaces (wrapper scripts, k8s Secrets, projected SA tokens) that need their own audit tier when those backends land in Sprint 4.

**Status**: ⏸ tracked for Sprint 4 (alongside SSH backend) and Sprint 4 (k8s backend)

## Issue 6 (tools-image workflow doesn't gate on Dockerfile changes for PRs) — roadmap

`.github/workflows/tools-images.yml` triggers only on `tags: ['v*']`. PRs that touch `tools/docker/**/Dockerfile` don't build the image to validate the build still works — a syntax error in the Dockerfile would only surface at tag time. Considered intentional for v0.9 (the image build is slow; PR-time gating would extend CI noticeably). Worth revisiting if Dockerfile churn picks up.

**Status**: ⏸ intentional design choice; track for revisit if needed

## Issue 7 (Phase B10 e2e step — `--backend docker` confirmed wired) — resolved

Verified after staff's Priority 8 (`--backend` flag wiring) landed: `DRY_RUN=1 ./scripts/e2e-test.sh` shows B10 cleanly with `cmd: ... ibmcloud --backend docker iam oauth-tokens`. The flag-extract pattern (Issue 1 above) means the flag works in either position.

**Status**: ✅ resolved (verified)
