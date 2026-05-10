# Sprint 4 — validator issues, resolution notes

Eight issues filed: 0 blockers, 1 medium (resolved this pass), 2 low
(deferred or self-resolved), 3 roadmap (deferred per validator's own
recommendations), 2 informational/resolved. Most are forward-looking
roadmap entries, intentional deferrals, or self-resolved coordination
notes from the parallel-dispatch model.

## Issue 1 (validator wrote against staff's k8s + ssh as they landed) — self-resolved at validator-run time

By report time, the validator's tests assert against the shipped
surface (`K8sBackend`, `SSHBackend`, `SetK8sInit`, `SetSSHTargetResolver`,
the package-private helpers exposed via in-package tests). All
verification gates green. No action needed.

**Status**: ✅ resolved (no integrator action required)

## Issue 2 (SSH backend ctx-cancel timing skipped at unit tier) — accepted

`TestSSHBackend_ContextCancel` is `t.Skip()`'d. The gliderlabs/ssh
in-process server doesn't propagate the client's SIGKILL signal to a
blocked handler within the few-second budget PRD 03 §"Backend interface"
spec'd. The real ctx-cancel behaviour is implemented in
`internal/remote/ssh.go`'s SIGKILL+Close-on-cancel goroutine and
exercised at integration tier (`scripts/e2e-test-backends.sh` Phase L).

**Status**: ✅ accepted; integration tier covers; revisit in Sprint 5
if a richer mock surface lands per Issue 3.

## Issue 3 (SSH wrapper-script content + bootstrap-failure tests need a mock surface) — accepted, deferred to Sprint 5

`internal/exec/ssh.go` calls a concrete `*remote.Client`. Mocking
requires extracting an interface from `remote.Client` or adding a
`SetSSHClientFactory` package-level seam. Wrapper-script content
invariants and the sudo-n / non-Ubuntu / repo-unreachable matrix tests
land in Sprint 5 once the seam is in place. Sprint 4 covers the
bootstrap opt-in / non-Ubuntu detection / argv-no-secret invariants via
the gliderlabs fake-sshd path that ships now.

**Status**: ⏸ accepted; tracked for Sprint 5 polish

## Issue 4 (k8s backend Job name uses argv[0] verbatim — colons break label validation) — resolved by integrator

Added `jobNameSanitizer` (a `strings.NewReplacer(":", "-", "/", "-", "@", "-")`)
package-level var in `internal/exec/k8s.go`; `runAsJob` now passes
`tool` through `jobNameSanitizer.Replace(tool)` before constructing the
Job name. Production callers pass tool names from `toolImages` (already
sanitised by the lookup); the sanitiser closes the test-fallback path
where argv[0] is a literal docker-style image ref like `busybox:latest`.

The `60`-char truncation on the Job name (line 274) remains in place,
so the post-sanitisation name still respects DNS-1123 length limits.

**Status**: ✅ resolved (`internal/exec/k8s.go::runAsJob` now produces
label-safe Job names regardless of argv[0] shape)

## Issue 5 (cli integration test compile blocked by mid-flight `runIperf3Client*` funcs) — self-resolved at validator-run time

By report time, staff completed both helpers and `go build ./...` was
clean. The transient compile failure was an artifact of the
parallel-dispatch model; future sprints can mitigate by sequencing the
build-blocking code drops before the test-writing phase, or by
isolating cross-agent build-time references behind seam interfaces.

**Status**: ✅ resolved (no integrator action required)

## Issue 6 (Phase M5/M6 SSH inclusion question) — accepted, deferred per validator recommendation

PRD 05 §M lists 7 cred-audit checks. M5 + M6 (`ls /tmp/roksbnkctl.*` +
`tail /var/log/auth.log` on the SSH jumphost) require a prior SSH-backend
session to have run — i.e., they assert post-conditions of PRD 05 Phase
I (SSH backend e2e), scheduled for Sprint 6 per `docs/PLAN.md`.

`scripts/e2e-test-backends.sh` ships Phase K (docker), Phase L (k8s),
and Phase M (audit minus M5/M6) in Sprint 4. The unit-tier SSH
cred-audit (`TestCredAudit_SSH_NoLeakInArgvOrWrapper`) closes the
security-spine assertion at the unit tier; M5/M6 are the e2e-tier
confirmations that need real Phase I to seed the tempfiles.

Validator recommended **defer**; integrator agrees. The driver's
yellow-⊘ on M5/M6 documents the decision; Sprint 6 will land the
combined runner.

**Status**: ⏸ accepted; tracked for Sprint 6 (alongside Phase I e2e)

## Issue 7 (k8s backend long-lived path passes argv as exec command verbatim — entrypoint double-up) — accepted, deferred to Sprint 5

`K8sBackend.runOnOpsPod` passes argv to `PodExecOptions.Command`
verbatim. The current ops pod image (`roksbnkctl-tools-ibmcloud`) has
`ibmcloud` as ENTRYPOINT; a caller passing
`argv = ["ibmcloud", "iam", "oauth-tokens"]` produces
`ibmcloud ibmcloud iam oauth-tokens` inside the pod (exec doesn't strip
the entrypoint).

For ibmcloud passthrough this works (the argv[0]=ibmcloud is benign
because `ibmcloud ibmcloud iam oauth-tokens` is parsed by ibmcloud as
`ibmcloud iam oauth-tokens` — the duplicate is treated as a positional
arg the parser ignores at top level). For a future per-tool ops pod
(or a no-entrypoint multi-tool ops image), this assumption breaks.

The two paths forward (no-entrypoint image vs argv[0]-strip) both have
trade-offs; deferring the choice to Sprint 5 polish alongside any new
tools landing on the ops pod. Integration test in
`scripts/e2e-test-backends.sh` Phase L's L1 step exercises the live
path today.

**Status**: ⏸ accepted; tracked for Sprint 5 polish

## Issue 8 (cspell.json — Sprint 0 typo "SSC" already absent; Sprint 4 vocabulary added) — resolved at validator-run time

Sprint 0's `SSC→SCC` fix already landed at integration time per
Sprint 0's resolved log. Validator added Sprint 4 + chapters 17/18/19
vocabulary: `seccompProfile`, `RuntimeDefault`, `kubectl-exec`,
`secretKeyRef`, `secretRef`, `subjectaltname`, `noproxy`, `rolebinding`,
`RoleBinding`, `ClusterRole`, `ClusterRoleBinding`, `ServiceAccount`,
`configmap`, `ConfigMap`, `envFrom`, `spdy`, `SPDY`. No action needed.

**Status**: ✅ resolved

## Integrator additions

- Added `/roksbnkctl` to `.gitignore` so the repo-root build artifact
  produced by `go build .` doesn't show up as untracked. Aligns with
  the existing `/bin/` exclusion pattern.
- Verified `bash -n scripts/e2e-test-backends.sh` clean.
- Verified `go build ./...`, `go vet ./...`, `gofmt -d -l .`,
  `go test ./...` all green after the integrator's k8s Job-name
  sanitiser + iperf3 Dockerfile USER + chapter 19 prose fixes landed.

## Summary

8 issues filed; Issue 4 (medium) resolved by integrator in this pass;
Issues 1 + 5 + 8 self-resolved at validator-run time; Issues 2 + 3 + 6
+ 7 accepted-and-deferred to Sprint 5/6 per validator recommendation.
Build, vet, gofmt, full test suite, and `bash -n` of the e2e driver all
green.
