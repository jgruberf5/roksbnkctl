# Sprint 1 — staff engineer issues, resolution notes

Five issues filed: 2 resolved during the agent's own work, 3 accepted-and-deferred to later sprints with rationale documented.

## Issue 1 (jumphost_shared_key root TF output) — resolved by agent

The TF testing module exposed the PEM as `module.testing.testing_jumphost_shared_private_key`; tfexec's `Output()` only returns root-level outputs. Agent added a sensitive root-level passthrough in `terraform/outputs.tf`:

```hcl
output "jumphost_shared_key" {
  sensitive = true
  value     = try(module.testing.testing_jumphost_shared_private_key, "")
}
```

If the testing module ever renames the inner output, the root passthrough needs a matching update.

**Status**: ✅ resolved by agent during sprint

## Issue 2 (--on extraction for `DisableFlagParsing` commands) — resolved by agent

`exec`, `kubectl`, `oc`, and `ibmcloud` use `DisableFlagParsing: true` so cobra forwards downstream flags. That blocks the persistent `--on` flag from reaching `flagOn`. Agent added an inline `extractOnFlag()` helper in `internal/cli/cluster.go` that scans `args` for `--on <name>` before dispatch. Documented in the inline comment.

`roksbnkctl --on jumphost exec ls` (flag before subcommand) hits cobra's normal persistent-flag path; only `roksbnkctl exec --on jumphost ls` (flag after subcommand) needs the manual extractor.

**Status**: ✅ resolved by agent during sprint

## Issue 3 (ssh-agent conn deliberately leaked) — accepted, deferred to Phase 3

Agent's `signerFromAgent()` returns the first agent signer without closing the underlying unix-socket connection, because agent-backed signers hold a reference and panic if the conn closes mid-handshake. Process exit GC's the FD.

For Sprint 1 (short-lived CLI invocations) this is fine. Phase 3's long-running ops pod path may need a refactored agent integration; tracked here so Sprint 4's PRD 03 staff agent doesn't lose the context.

**Status**: ✅ accepted; deferred to Phase 3 (PRD 03 § SSH backend)

## Issue 4 (TOFU TTY-prompt unit-test gap) — accepted, deferred to integration tests

The `y/N` TOFU prompt path requires a real TTY `*os.File`, which can't be synthesised in a unit test. Existing unit tests cover insecure-accept, mismatch-reject, and non-TTY-reject paths. The y/N path is exercised by the validator agent's `internal/remote/integration_test.go` (testcontainers-go against a real openssh-server container).

Coverage: 61.1% on `internal/remote/` from unit tests; integration tests push it higher in CI.

**Status**: ✅ accepted; integration test layer covers the gap

## Issue 5 (go.mod toolchain bump from new test deps) — accepted

Adding `gliderlabs/ssh` for in-process SSH server testing (staff) and `testcontainers/testcontainers-go` for live-container integration testing (validator) bumped:

- `golang.org/x/crypto` v0.24.0 → v0.48.0 (gliderlabs needs newer tree)
- The `go` directive in `go.mod` from `1.23` → `1.25.0` (testcontainers' otel transitives require 1.24+; their newest version requires 1.25)

Both forced by required runtime test deps. The CI workflow now reads the Go version from `go.mod` via `go-version-file: go.mod` so future bumps don't need workflow edits.

Documented for v0.7 release notes: build requires Go 1.25+ (was 1.23).

**Status**: ✅ accepted; CI updated to track go.mod
