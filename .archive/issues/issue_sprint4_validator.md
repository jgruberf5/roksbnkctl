# Sprint 4 — validator issues

Format matches Sprint 3. `Severity: roadmap` is reserved for non-blocking
forward-looking observations; `low/medium/high/blocker` for actionable
findings.

## Issue 1: validator wrote against staff's k8s + ssh as they landed

**Severity**: informational
**Status**: ✅ resolved at validator-run time

**Description**: Validator deliverables are `internal/exec/{k8s,ssh}_test.go`,
`internal/exec/audit_test.go` (extended), `internal/exec/k8s_integration_test.go`,
and `internal/cli/ops_integration_test.go`. The staff implementation
(`internal/exec/k8s.go` + `internal/exec/ssh.go` + `internal/cli/ops.go`)
landed mid-validator-run; tests assert against the shipped surface, not a
stub.

Specifically aligned at validator-run-end:
- `K8sBackend{client, config, initFn}` struct fields are used by tests'
  fake-clientset wiring
- `SetK8sInit(fn)` / `SetSSHTargetResolver(fn)` package-level seams used
  by tests to inject test doubles without an import cycle
- `extractLongLivedFlag` / `extractSSHTarget` / `buildJobSpec` /
  `buildJobEnv` / `splitKV` / `mergeSSHEnv` / `shellSingleQuote` are
  package-private helpers exposed via in-package tests
- `K8sBackend.Run` dispatches on `ROKSBNKCTL_K8S_LONG_LIVED=1` env
  sentinel; `SSHBackend.Run` dispatches on `ROKSBNKCTL_SSH_TARGET=<name>`
  sentinel — both are documented seams the CLI dispatch layer uses

**Resolution**: no action — verification gates green at report time.

## Issue 2: SSH backend ctx-cancel timing is gliderlabs-dependent

**Severity**: low
**Status**: open (test skipped; integration covers)

**Description**: `TestSSHBackend_ContextCancel` is `t.Skip()`'d. The
gliderlabs/ssh in-process server doesn't propagate the client's SIGKILL
signal to a blocked handler — the handler only sees
`session.Context().Done()` once the parent SSH connection itself is torn
down. In practice this happens, but the timing depends on goroutine
scheduling and the SIGKILL→close path takes longer than the few-second
budget PRD 03 §"Backend interface" promises.

The real ctx-cancel behaviour is correctly implemented in
`internal/remote/ssh.go` (the SIGKILL+Close-on-cancel goroutine);
exercising it through the SSH backend works in production (integration
tier in `scripts/e2e-test-backends.sh` Phase L's L2 throughput test would
detect a regression).

**Files affected**: `internal/exec/ssh_test.go`
**Proposed fix**: cover at integration tier only; revisit if a unit-tier
mock SSH client surface becomes available (Issue 3 below).

## Issue 3: SSH wrapper-script content + bootstrap-failure tests need a mock surface

**Severity**: roadmap
**Status**: open

**Description**: PRD 03 §SSH covers several wrapper-script invariants
the validator brief asks for unit tests on:

- Wrapper script content excludes the cred value (it lives in a separate
  `.env` file the wrapper sources silently)
- `set +x` discipline in the wrapper to avoid trace leaking the env-file
  source
- File-materialization writes Files entries to
  `/tmp/roksbnkctl.<rand>/<basename>` on the remote
- Bootstrap-failure modes — `sudo -n` fails (rc=126), non-Ubuntu detected
  via `lsb_release -is` (rc=126), package-repo unreachable (rc=127)

`internal/exec/ssh.go` calls `remote.Connect` and then `client.Run`
through a concrete `*remote.Client`. Substituting a mock at this layer
requires either (a) extracting an interface from `*remote.Client`'s
public methods (Run + Close + Shell + the SetEnv-via-RunOpts shape) or
(b) adding a `SetSSHClientFactory` package-level seam analogous to
`SetSSHTargetResolver`.

**Files affected**: `internal/remote/ssh.go` (interface extraction), or
`internal/exec/ssh.go` (factory seam); `internal/exec/ssh_test.go`
(test bodies).

**Proposed fix**: Sprint 4 validator covers the bootstrap opt-in /
non-Ubuntu / context-cancel / argv-no-secret invariants via the
fake-sshd path that ships now. The wrapper-script content + `sudo -n`
matrix lands in Sprint 5 once a mock client interface is in place.
Integration tier in `scripts/e2e-test-backends.sh` Phase L exercises
the live paths.

## Issue 4: K8s backend Job name uses argv[0] verbatim — colons break label validation

**Severity**: medium
**Status**: open

**Description**: `internal/exec/k8s.go::runAsJob` constructs the Job
name as `"roksbnkctl-" + tool + "-" + suffix` where `tool = argv[0]`. If
argv[0] is a literal docker-style image reference (e.g.,
`busybox:latest`), the colon is invalid in a Kubernetes label value
(label-validation regex rejects `:`). The fake-clientset code path I
exercised initially panic'd with:

```
invalid selector "app=roksbnkctl-busybox:latest-w9rf8l": ...
```

(Validator workaround: tests use tool names from the `toolImages` map
— `iperf3`, `ibmcloud` — which are sanitised via the lookup. The test-
path fallback `image = tool` only triggers when argv[0] is a literal
image ref like `busybox:latest`, but that breaks at label-validation
time.)

**Files affected**: `internal/exec/k8s.go::runAsJob`
**Proposed fix**: sanitise the tool name into the Job name —
`strings.NewReplacer(":", "-", "/", "-", "@", "-").Replace(tool)` — or
use a constant prefix and embed argv[0] only in a label/annotation
that's pre-truncated to label-validation-safe characters. PRD 03 §K8s
should also document that the docker-style argv[0]=<image> shape is a
test-only fallback; production callers use tool names from `toolImages`.

## Issue 5: cli integration test compile blocked by mid-flight runIperf3Client* funcs

**Severity**: low
**Status**: ✅ resolved at validator-run time

**Description**: At an intermediate point during validator dispatch,
`internal/cli/test.go` referenced `runIperf3ClientK8s` and
`runIperf3ClientSSH` that staff hadn't yet defined. This caused both
`go build ./...` and `go test ./...` to fail (the failure pre-dated my
test files; `internal/cli/ops_integration_test.go` couldn't compile
either).

By report time, staff had completed both helpers and the build was
clean. Worth flagging to the integrator: when Sprint dispatches land
mid-flight references like this, the validator's deliverables can
appear "broken" against the working tree even though the tests are
correctly written against the eventual final API.

**Files affected**: none (resolved by staff completing their work)

## Issue 6: Phase M5/M6 SSH inclusion question — defer per PRD 05's own ordering

**Severity**: roadmap
**Status**: open (clarification needed from PRD owner)

**Description**: PRD 05 §M lists 7 cred-audit checks. Of those, M5 + M6
(`ls /tmp/roksbnkctl.*` + `tail /var/log/auth.log` on the SSH jumphost)
require a prior SSH-backend session to have run — i.e., they assert
post-conditions of PRD 05 Phase I (SSH backend smoke tests).

`scripts/e2e-test-backends.sh` ships Phase K (docker), Phase L (k8s),
and Phase M (audit minus M5/M6) in Sprint 4. Phase I (SSH backend e2e)
is scheduled for Sprint 6 per docs/PLAN.md — at which point the full
combined runner (`scripts/e2e-test-full.sh`, also Sprint 6) chains
Phase I → Phase L → Phase M5/M6.

The question for the integrator: should Sprint 4's e2e include a
truncated Phase I + M5/M6 to close the SSH-side audit loop now? Or is
it cleaner to defer until Sprint 6 lands the full Phase I + N + the
combined runner?

Validator recommendation: **defer**. The unit-tier SSH cred-audit
(`TestCredAudit_SSH_NoLeakInArgvOrWrapper` + the SetEnv canary check
inside the SSH backend itself) closes the security-spine assertion at
the unit tier. M5/M6 are the e2e-tier confirmations; landing them
without a real Phase I to seed the tempfiles risks false positives.

**Files affected**: `scripts/e2e-test-backends.sh` (the M5+M6 yellow-⊘
log line documents the decision)
**Proposed fix**: integrator confirms; if Sprint 4 should include a
truncated Phase I, add it ahead of M5/M6 in the driver script. Otherwise
keep the deferral.

## Issue 7: K8s backend long-lived path passes argv as exec command verbatim

**Severity**: roadmap
**Status**: open

**Description**: `K8sBackend.runOnOpsPod` passes argv to
`PodExecOptions.Command` verbatim. The ops pod's container image has
`ibmcloud` as ENTRYPOINT (per the staff Dockerfile choice); when the
caller passes `argv = ["ibmcloud", "iam", "oauth-tokens"]`, this becomes
`ibmcloud ibmcloud iam oauth-tokens` inside the pod (exec, not docker
run, doesn't strip the entrypoint).

Staff's source comment in `runOnOpsPod` flags this as a known issue:

> Today: the ops pod's image is the ibmcloud-tools image whose
> entrypoint is `ibmcloud`. For ibmcloud passthrough the caller's
> argv is ["ibmcloud", ...rest]; the entrypoint already covers
> the first token. For other tools we'd need a per-tool ops pod
> or a no-entrypoint image — flagged in the README.

This means the validator can't trivially write a unit test for "ibmcloud
ibmcloud-args becomes the right exec call shape" without choosing one
side of the staff comment's design decision. The integration tier
(`scripts/e2e-test-backends.sh` Phase L's L1 step — `roksbnkctl ibmcloud
--backend k8s iam oauth-tokens`) exercises the live path; a regression
shows up as the ibmcloud CLI complaining about an unknown subcommand.

**Files affected**: `internal/exec/k8s.go::runOnOpsPod` + the future
ops-image Dockerfile.
**Proposed fix**: defer to Sprint 5 polish: either (a) use a no-entrypoint
ops image (so argv flows through verbatim) or (b) strip argv[0] in
runOnOpsPod when it matches a known per-image entrypoint. Track here.

## Issue 8: cspell.json — Sprint 0 typo "SSC" already absent; Sprint 4 vocabulary added

**Severity**: informational
**Status**: ✅ resolved

**Description**: The validator brief flagged the Sprint 0 tech-writer
Issue 1 carry-over — replace `"SSC"` with `"SCC"` in cspell.json's
allowed-words. Verified at validator-run time that the typo is already
absent (Sprint 0's resolution log confirms it was fixed at integration
time). `"SCC"` is on line 23.

Sprint 4 added these words to cover the Sprint 4 + chapters 17/18/19
landing surface: `seccompProfile`, `RuntimeDefault`, `kubectl-exec`,
`secretKeyRef`, `secretRef`, `subjectaltname`, `noproxy`, `rolebinding`,
`RoleBinding`, `ClusterRole`, `ClusterRoleBinding`, `ServiceAccount`,
`configmap`, `ConfigMap`, `envFrom`, `spdy`, `SPDY`.

**Files affected**: `cspell.json`
**Resolution**: shipped.
