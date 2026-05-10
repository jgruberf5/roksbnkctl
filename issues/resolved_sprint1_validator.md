# Sprint 1 — validator issues, resolution notes

Eight issues filed. One real medium-severity bug (Issue 8) **fixed in this integration pass**. One reported as blocker was stale by integration time (staff completed the function). The rest are informational, low-severity deferred, or roadmap.

## Issue 1 (integration test API surface tracks staff's draft) — resolved by visual diff

The test file references `Connect`, `Client.Run`, `Client.Close`, `RunOpts{Stdin,Stdout,Stderr,Env,TTY}`, `Target{Name,Host,Port,User,Signer}`. Confirmed via grep on staff's final `internal/remote/{ssh,targets}.go` that all referenced symbols exist with matching signatures. No drift.

**Status**: ✅ resolved (verified)

## Issue 2 (TOFU integration test deferred) — accepted, tracked for Sprint 1.5

Validating "second connect is silent" end-to-end requires a per-call `KnownHostsPath` override on `HostKeyOptions`. Staff's current API hardcodes `~/.roksbnkctl/known_hosts`. Patching `$HOME` per-test is fragile under parallelism.

Mitigation: add `HostKeyOptions.KnownHostsPath string` to `internal/remote/hostkeys.go` (default to current path when empty); land the deferred TOFU integration test in Sprint 1.5 or fold into Sprint 3's cred-audit work.

**Status**: ✅ accepted; tracked for Sprint 1.5 / Sprint 3

## Issue 3 (`tryAutoJumphost` undefined) — STALE; resolved by staff before integration

Validator caught this during staff's mid-flight WIP — function was referenced but not yet pushed. By the time the integrator (this pass) ran `go build ./...`, staff had completed task 7 and the function exists at `internal/cli/lifecycle.go:331`. Build and tests are green.

**Status**: ✅ stale at integration time; staff completed the work

## Issue 4 (cred-leak preview audit clean) — informational

Validator's audit of staff's `internal/cli/remote.go` and `internal/remote/ssh.go` confirmed:

- ✓ `IBMCLOUD_API_KEY` value never appears in argv (`runIBMCloudPassthrough` builds verb-only argv; key flows through `RunOpts.Env` → `sess.Setenv` proper SSH env channel)
- ✓ `targets show` prints only the source descriptor (`tf-output:jumphost_shared_key`), never resolved PEM bytes
- ⚠ Wrapper-script fallback path NOT YET implemented (PRD 03 / Sprint 4 territory; flagged for that sprint's staff agent)
- ⚠ No automated regression guard (PRD 04 cred-audit work; Sprint 3+)

Sprint 1's cred surface is clean.

**Status**: ✅ resolved as "informational; expected gaps tracked for Phase 3"

## Issue 5 (existing tests survive Targets field add) — informational

`internal/config/context_test.go` round-trips workspace YAML; the new `Targets map[string]TargetCfg` field with `yaml:"targets,omitempty"` round-trips cleanly. `go test ./internal/config/...` green.

**Status**: ✅ resolved (verified by validator)

## Issue 6 (testcontainers-go forces Go toolchain bump) — accepted

Same as staff's Issue 5: testcontainers' otel transitives require Go 1.24+ (newest needs 1.25). `go.mod` directive auto-bumped 1.23 → 1.25.0 via `go mod tidy`. CI now reads version from go.mod via `go-version-file: go.mod` in all jobs.

To document for v0.7 release notes: minimum Go version is now 1.25.

**Status**: ✅ accepted; CI workflow updated

## Issue 7 (book chapter drift guard) — roadmap

Forward-looking observation: chapter 16 documents the `--on` flag; if staff renames flags or restructures `targets:` YAML before sprint cuts, the chapter drifts silently. `mdbook test` only catches internal links, not real-world fidelity.

Tech-writer agent (next in sequence) is explicitly tasked with verifying chapter 16's commands against staff's actual implementation. Sprint 7's "every code example test-verified in a fresh workspace" is the long-term mitigation.

**Status**: ✅ tracked; not actionable in Sprint 1

## Issue 8 (ctx cancellation does not tear down running sessions) — FIXED in integration pass

**Root cause**: `ssh.Session.Run` calls `Wait()` internally, which blocks until the remote process exits naturally. Just calling `sess.Close()` from the cancellation goroutine does NOT signal the remote process; it only marks the session as closed locally — `Wait` keeps blocking on the network read of the exit-status message.

**Fix applied** in `internal/remote/ssh.go` (cancellation goroutine now sends a signal first, then closes):

```go
go func() {
    select {
    case <-ctx.Done():
        _ = sess.Signal(ssh.SIGKILL)  // forces remote process to exit
        _ = sess.Close()              // backstop
    case <-cancelDone:
    }
}()
```

`SIGKILL` rather than SIGTERM because PRD 01 specifies "context cancellation closes the session within a few seconds" and SIGKILL is unblockable. If a future sprint wants graceful cleanup, that's a SIGTERM-then-SIGKILL ladder.

`go build ./...` clean; `go test ./internal/remote/...` clean. The integration test (`TestIntegration_ContextCancellation`) should now pass when run via `go test -tags integration ./internal/remote/...` against Docker.

**Status**: ✅ resolved
**Files touched**: `internal/remote/ssh.go` (4-line change to cancellation goroutine)
