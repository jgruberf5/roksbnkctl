# Sprint 1 — validator issues

Format matches Sprint 0. `Severity: roadmap` is reserved for non-blocking
forward-looking observations; `low/medium/high/blocker` for actionable
findings.

## Issue 1: integration test API surface tracks staff's draft

**Severity**: low
**Status**: open
**Description**: `internal/remote/integration_test.go` references the
package symbols `Connect`, `Client`, `Run`, `Close`, `RunOpts{Stdin,
Stdout, Stderr, Env, TTY}`, and `Target{Name, Host, Port, User, Signer}`.
At the time of writing this validator drop, staff's
`internal/remote/{ssh,targets,keys,hostkeys,agent}.go` are present in
the working tree (mid-flight) and the API names used by the integration
test match staff's actual definitions. Verified by visual inspection:

- `Target` struct in `targets.go:21-33` has `Name/Host/Port/User/Signer`
  plus `KeyPath/KeySource/HostKeyCallback` (validator's test ignores
  the last three — fine).
- `Connect(ctx, *Target) (*Client, error)` in `ssh.go:65`.
- `RunOpts{Stdin, Stdout, Stderr, Env, TTY}` in `ssh.go:45-51`.
- `(*Client).Run(ctx, []string, RunOpts) (int, error)` in `ssh.go:157`.
- `(*Client).Close() error` in `ssh.go:138`.

If staff renames any of these before the sprint integrates, the
integration test must be updated alongside. No automated guard for
this — just attention during review.

**Files affected**: `internal/remote/integration_test.go`
**Proposed fix**: visual-diff staff's final commit against the symbols
referenced in the test before the sprint integrator merges.

## Issue 2: TOFU integration test deferred

**Severity**: low
**Status**: open
**Description**: PRD 01 §Host key handling specifies a TOFU prompt
flow against `~/.roksbnkctl/known_hosts` plus an `--insecure-host-key`
flag. Validating "second connect is silent" end-to-end requires
injecting a custom known_hosts path and a per-call insecure override
into the SSH client. Staff's current API exposes these via the global
flag (`flagInsecureHostKey`) and via `HostKeyOptions{Insecure: bool}`
fed into `remote.HostKeyCallback`, but the **path** to known_hosts is
hardcoded inside `hostkeys.go` (it's not a per-call override). Patching
HOME for the duration of a single test is workable but fragile when
parallelised. The simple mitigation — a `HostKeyOptions{KnownHostsPath
string}` field — is a small staff-side addition that didn't make the
Sprint 1 cut.

**Files affected**: `internal/remote/hostkeys.go` (staff), follow-up
test in `internal/remote/integration_test.go`.
**Proposed fix**: add `HostKeyOptions.KnownHostsPath string` to
hostkeys.go (default to the current `~/.roksbnkctl/known_hosts` when
empty); revisit the deferred TOFU integration test in Sprint 1.5 or
alongside Sprint 3's cred audit work. Tracked here so it isn't lost.

## Issue 3: `tryAutoJumphost` referenced but not yet defined (staff WIP)

**Severity**: blocker (for the *integrator*, not for validator's
deliverables)
**Status**: open
**Description**: The working tree at validator-run time has staff's
`internal/cli/lifecycle.go` referencing `tryAutoJumphost(...)` from
three call sites (lines 131, 143, 182), but no `func tryAutoJumphost`
exists anywhere in `internal/cli/`. Builds fail:

```
internal/cli/lifecycle.go:131:3: undefined: tryAutoJumphost
internal/cli/lifecycle.go:143:2: undefined: tryAutoJumphost
internal/cli/lifecycle.go:182:2: undefined: tryAutoJumphost
```

This is staff's task 7 from `prompts/sprint1/staff.md` (auto-populate
jumphost post-apply). The function is presumably staged but not yet
committed to staff's working files. Validator did NOT touch
`internal/cli/lifecycle.go` — surfacing this so the sprint integrator
flags it back to staff.

**Files affected**: `internal/cli/lifecycle.go`
**Proposed fix**: staff completes their task 7 and adds the function
definition. A follow-up commit by staff should make the build green
again before the sprint integrates.

## Issue 4: cred-leak preview audit — Sprint 1 surface clean

**Severity**: low
**Status**: open (informational)
**Description**: Reviewed staff's `internal/cli/remote.go` and
`internal/remote/ssh.go` for the cred-leak prerequisites called out in
the validator brief:

- ✓ **`IBMCLOUD_API_KEY` value never appears in argv**:
  `internal/cli/cluster.go runIBMCloudPassthrough` builds argv as
  `append([]string{"ibmcloud"}, argv...)` — only the verb chain (`iam
  oauth-tokens` etc.). The key value flows through `envExtra` →
  `RunOpts.Env` → `sess.Setenv` (`internal/remote/ssh.go:171-180`),
  which is the proper SSH env channel.

- ✓ **`targets show` does NOT print key material**:
  `internal/cli/targets.go:106-117` prints only Host/Port/User and the
  *source descriptor* (`tf-output:jumphost_shared_key`), never the
  resolved PEM bytes. Same for `targets list`.

- ⚠ **Wrapper-script fallback path NOT YET implemented**: PRD 01
  acknowledges that some sshd configs reject `Setenv` unless
  `AcceptEnv` whitelists the variable; the documented fallback is a
  remote wrapper script with `chmod 0700 + trap 'rm -f $0' EXIT`.
  Staff's `Run` (`ssh.go:171-180`) ignores Setenv reject errors silently
  (`_ = sess.Setenv(...)`), which is fine for v0.7's "best-effort"
  contract but means env-pass over ssh **silently fails to propagate**
  on a strict sshd. PLAN.md sequences this fallback into Sprint 4
  (PRD 03's SSH backend), so it's expected — flagging here so the
  Sprint 4 staff agent doesn't forget.

- ⚠ **No automated regression guard**: a unit test that exec's the
  remote command and inspects the SSH `Setenv` request frames would
  catch a future regression where someone adds the key to argv "for
  convenience". PLAN.md sequences automated cred-audit tests into
  Sprint 3 (PRD 04 cred abstraction). Documenting here so the Sprint 3
  validator picks up where this preview leaves off.

**Files affected**: forward-looking
**Proposed fix**: Sprint 4 staff implements the wrapper fallback per
PRD 03; Sprint 3 validator adds the automated argv-doesn't-contain-key
unit test alongside the cred resolver.

## Issue 5: existing tests survive Targets field addition (no regression)

**Severity**: informational
**Status**: resolved (verified)
**Description**: Confirmed that `internal/config/context_test.go` still
passes after staff's edit to `internal/config/workspace.go` (added
`Targets map[string]TargetCfg` field per PRD 01). The test's
`SaveAndLoadWorkspace_Roundtrip` only asserts a small subset of
fields and uses `yaml.Marshal/Unmarshal` round-trip — the new optional
field with `yaml:"targets,omitempty"` round-trips cleanly without
breaking existing assertions. `go test ./internal/config/...` is green.

The only test that *could* break would be one that did
`reflect.DeepEqual(savedYAMLBytes, expectedBytes)` against a hardcoded
YAML fixture; no such test exists. No action needed.

**Files affected**: none
**Proposed fix**: none

## Issue 6: testcontainers-go forces a Go toolchain bump

**Severity**: medium
**Status**: open
**Description**: Adding `testcontainers/testcontainers-go` (any recent
version, including the older v0.31.0) pulls in a transitive chain
testcontainers → `docker/docker/client` → `otel/contrib/.../otelhttp`
→ `otel`, and the current `go.opentelemetry.io/otel` modules require
`go 1.24.0` (v1.41.0) or `go 1.25.0` (v1.43.0). `go mod tidy` accordingly
bumps the `go` directive in `go.mod` from `1.23` to `1.25.0`.

Implications:

1. **CI workflow**: previously `setup-go.go-version: '1.23'`. Validator
   updated both the `test` matrix and the new `integration` job to
   `go-version-file: go.mod` so they track the directive automatically;
   no further drift.
2. **Local devs on Go 1.23 toolchains** will be auto-prompted to
   download Go 1.25 by `go.toolchain` resolution — fine on machines
   with network access, breaks in air-gapped CI runners. Document in
   `CONTRIBUTING.md`'s "building from source" section if it becomes
   a recurring support question.
3. **Tag-gated tidy**: future `go mod tidy` runs need
   `GOFLAGS="-tags=integration"` (or the tag set whatever package uses)
   to keep testcontainers in go.mod; running plain `go mod tidy` would
   strip it from the require block, since no non-tagged source
   references it.

**Files affected**: `go.mod` (now declares `go 1.25.0`),
`.github/workflows/ci.yml` (now uses `go-version-file: go.mod`).
**Proposed fix**: keep the auto-bumped directive; document the tagged
`go mod tidy` invocation in CONTRIBUTING.md; revisit if otel projects
backport to Go 1.23.

## Issue 7 (roadmap): book chapter drift guard

**Severity**: roadmap
**Status**: informational
**Description**: The architect agent updated 6 chapters this sprint
(1, 2, 3, 4, 7, 16). `book/src/16-on-flag-ssh-jumphosts.md` documents
the `--on` flag and target config; if staff renames flags or restructures
the YAML before the sprint cuts, that chapter drifts silently —
`mdbook test` only checks internal links, not real-world fidelity. A
future polish pass (Sprint 7's "every code example test-verified in a
fresh workspace") catches this; not a Sprint 1 gate.

**Files affected**: forward-looking
**Proposed fix**: track in the Sprint 7 launch checklist; not actionable
this sprint.

## Issue 8: SSH client context-cancellation does not promptly tear down running sessions

**Severity**: medium
**Status**: open
**Description**: `internal/remote/integration_test.go ::
TestIntegration_ContextCancellation` reproduces this against a real
sshd container:

```
1. Connect()
2. Spawn `sleep 30` via Run(ctx, ...)
3. After 2s, cancel ctx
4. Assert Run returns within 10s
```

Result: Run does **not** return within 10s. The remote `sleep 30`
runs to completion (or test timeout), and Run blocks until the remote
process exits naturally. Staff's `(*Client).Run` at
`internal/remote/ssh.go:209-216` wires ctx to `sess.Close()` in a
goroutine, but `golang.org/x/crypto/ssh.Session.Run` blocks on
io.Copy of the session's stdout/stderr — closing the session does not
unblock those copies if the underlying TCP connection is still open
and the remote process keeps writing (or blocks on reads).

PRD 01 §Implementation tasks 1 explicitly says: "Context cancellation
closes the session within a few seconds." Current behavior diverges.

**Files affected**: `internal/remote/ssh.go` (staff)
**Proposed fix**: in addition to `sess.Close()`, send a Signal/Kill
via the SSH session protocol on ctx cancel, OR close the underlying
`*ssh.Client` connection (more aggressive — kills all sessions on the
client). The latter is simpler and matches what `--on jumphost`
expects (one connection per command in v0.7). Reference:
https://pkg.go.dev/golang.org/x/crypto/ssh#Session.Signal —
`sess.Signal(ssh.SIGTERM)` followed by a 1s grace then `sess.Close()`.

This bug is detected by the validator's integration test and is
**not** a Sprint-1 blocker for v0.7 release if users rarely Ctrl-C
during remote commands, but the test will continue to fail in CI
until staff fixes the wiring. Suggested resolution path: integrator
applies the small fix in staff's commit before tagging, or the test
is relaxed to a 30s budget with a TODO referencing this issue. The
test as-written is the correct PRD assertion.

---

*Total filed: 8 issues — 1 blocker (staff WIP), 2 low (test surface,
TOFU deferral), 2 medium (Go toolchain bump, ctx cancel bug), 1
informational-resolved (no regression), 2 roadmap.*
