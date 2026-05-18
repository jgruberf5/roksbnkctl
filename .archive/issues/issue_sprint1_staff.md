# Sprint 1 — staff engineer issues

## Issue 1: jumphost_shared_key root-level TF output added
**Severity**: low
**Status**: resolved
**Description**: PRD 01's auto-population pseudocode reads
`stringOutput(outputs, "jumphost_shared_key")`, but the upstream HCL
only exposed the PEM as `module.testing.testing_jumphost_shared_private_key`.
tfexec's Output() returns root-level outputs only, so an explicit
top-level passthrough was required for the auto-populate flow to find it.
**Files affected**: `terraform/outputs.tf` (added `jumphost_shared_key`,
sensitive, value = `try(module.testing.testing_jumphost_shared_private_key, "")`).
**Proposed fix**: applied. If the testing module ever changes the
inner output name, update the root passthrough to match.

## Issue 2: --on with `roksbnkctl exec` requires manual flag extraction
**Severity**: low
**Status**: resolved
**Description**: `exec`, `kubectl`, `oc`, and `ibmcloud` use
`DisableFlagParsing: true` so cobra doesn't grab flags meant for the
wrapped binary (e.g. `kubectl get pods --all-namespaces`). That means
the persistent `--on` flag never reaches `flagOn`. Worked around by
pulling `--on` out of `args` manually before dispatch (see
`extractOnFlag` in `internal/cli/cluster.go`). Users who write
`roksbnkctl --on jumphost exec ls` still hit the persistent flag path
because cobra parses persistent flags before dispatch; only
`roksbnkctl exec --on jumphost ls` needs the manual extractor.
**Files affected**: `internal/cli/cluster.go`.
**Proposed fix**: applied. Documented in the inline comment.

## Issue 3: ssh-agent host conn deliberately leaked for signer lifetime
**Severity**: low
**Status**: open
**Description**: `signerFromAgent()` returns the first signer from
`ssh.Agent.Signers()` but doesn't close the underlying unix-socket
conn — agent signers hold a reference and panic if the conn closes
mid-handshake. We rely on process exit to GC the FD. Fine for
short-lived CLI invocations; revisit if Phase 3 ever long-runs.
**Files affected**: `internal/remote/keys.go`.
**Proposed fix**: thread the conn back to the caller via Connect's
lifecycle, or refactor to a Resolver interface that closes after
Connect returns. Out of scope for v0.7.

## Issue 4: known_hosts TOFU TTY-prompt path lacks unit-test coverage
**Severity**: low
**Status**: open
**Description**: `HostKeyCallback`'s "prompt user, write on yes"
branch needs an `*os.File` whose `Fd()` is a TTY, which can't be
synthesised from a `strings.Reader`. The existing tests cover the
silent-accept (Insecure), mismatch-rejects, and non-TTY-rejects paths,
but the actual y/N path is exercised only in manual / integration
testing.
**Files affected**: `internal/remote/hostkeys.go`,
`internal/remote/hostkeys_test.go`.
**Proposed fix**: validator agent's integration_test.go (running
against a real openssh-server container) is a better fit than a unit
test here. Coverage for `internal/remote/` lands at ~61% from unit
tests alone; integration tests should push it to spec.

## Issue 5: tests bumped go.mod's golang.org/x/crypto and toolchain
**Severity**: low
**Status**: open
**Description**: Adding `gliderlabs/ssh` for in-process SSH server
testing triggered `go mod tidy` to upgrade `golang.org/x/crypto` from
v0.24.0 → v0.48.0 and bump the module's go directive to 1.25.0. The
validator agent's `testcontainers-go` integration test additionally
pulled a wide tree of transitive deps. The integrator should review
go.mod / go.sum diffs before committing.
**Files affected**: `go.mod`, `go.sum`.
**Proposed fix**: review and accept; both bumps are forced by
required runtime deps for new testing surfaces.
