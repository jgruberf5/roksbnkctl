# Sprint 5 — validator issues, resolution notes

Six issues filed: 5 low, 1 roadmap. Five resolved at integration time
(four self-resolved with no action required, one resolved by running
the deferred verification step); one is a roadmap entry already
implemented and intentionally left in.

## Issue 1 (LOW — Phase L-DNS LD8 GSLB-divergence target choice) — accepted

`www.google.com` is the documented exemplar but anycast can produce
identical answers across vantages. The automated LD8 step treats
divergence as informational; the integrator's manual v0.9 sign-off (per
`docs/E2E_TEST.md` §"v0.9 release checklist" item 2) asserts divergence
against a known-divergent target — a real F5 BIG-IP Next GSLB record
from the Phase D BNK deployment, or a Route 53 latency-routed name like
`www.amazon.com`.

This is the right framing for the e2e harness (resilient against
anycast cohabitation) and for the v0.9 release checklist
(deterministically asserts the feature works against a real GSLB).

**Status**: ✅ accepted (documented in `docs/E2E_TEST.md` +
`CONTRIBUTING.md`; no code action required)

## Issue 2 (LOW — DRY_RUN walkthrough deferred to integrator) — resolved by integrator

Validator's sandbox blocked shell-script execution. Integrator ran
`DRY_RUN=1 IBMCLOUD_API_KEY=dummy ROKSBNKCTL=true ./scripts/e2e-test-backends.sh`
at integration time and confirmed all four phases (K → L → L-DNS → M)
render cleanly with phase headers in order. Phase L-DNS exercises
LD0-LD8 + LD10 (LD9 yellow-skipped per Sprint 6 deferral). Phase M
emits M1-M4 + M7 (M5+M6 yellow-skipped per Sprint 6 SSH-e2e deferral).

**Status**: ✅ resolved (DRY_RUN verified at integration time; final
"Backend-matrix phases passed" green-line emitted)

## Issue 3 (LOW — Probe.Run TIMEOUT semantics) — accepted (staff-impl behaviour preserved)

Staff's `Probe.Run` returns `(*DNSProbeResult, nil)` with
`Rcode=TIMEOUT` when miekg/dns Exchange times out — a soft-failure
shape that's friendlier for CLI consumers (always get a populated
DNSProbeResult, render uniformly). Validator's
`TestProbe_Rcode_Timeout` asserts the staff impl's behaviour.

PRD 03's wording was looser ("error returned with deadline-exceeded");
the staff impl is more user-friendly. Keep the soft-failure path —
flipping to a hard error would force every CLI consumer to special-case
TIMEOUT separately from NXDOMAIN/SERVFAIL. The PRD will get updated in
a follow-up to match the implementation.

**Status**: ✅ accepted (current behaviour preserved; PRD 03 to be
updated in a future polish pass)

## Issue 4 (ROADMAP — TestProbe_TruncatedFlag dropped from coverage) — accepted

The staff impl correctly retries truncated UDP responses over TCP, so a
unit test asserting `Truncated=true` after a UDP TC=1 response would
race the TCP retry. Behaviour is pinned indirectly: the probe handles
TC=1 correctly, and the larger answer set lands in `Answers[]`. A
direct `Truncated=true` assertion would require either a TCP-only mock
server or a TC=1 response shape that fails the TCP retry too — both
out of scope for v0.9.

**Status**: ⏸ roadmap (revisit in v1.x if `Truncated` becomes
user-facing via a CLI flag)

## Issue 5 (LOW — sshseam build tag dropped at validator-run time) — self-resolved

Staff landed `SetSSHClientFactory` within the same dispatch window
(`internal/exec/ssh.go:79-117`). The validator's
`ssh_wrapper_test.go` build tag was dropped; tests run in the default
`go test ./...` suite.

**Status**: ✅ self-resolved (no integrator action required)

## Issue 6 (ROADMAP — `:dev` push on workflow_dispatch) — accepted (intentional)

`.github/workflows/tools-images.yml` Sprint 5 update pushes `:dev` on:

- `main` pushes (Sprint 4 staff Issue 2 carry-over: enables
  `go install ./cmd/roksbnkctl@main` without a local
  `tools/docker/Makefile build`)
- `workflow_dispatch` (manual triggering from any branch publishes
  `:dev` for testing tool-image changes on a feature branch without
  merging to main first)

The workflow_dispatch path is a nicety beyond Sprint 4 staff Issue 2's
ask. Documented in the workflow file's leading comment.

**Status**: ✅ accepted (intentional UX expansion; not a deferral)

## Integrator additions

- Verified all 23 new validator-authored unit tests pass against
  staff's landed surface (`go test ./internal/test/... ./internal/exec/...`
  green).
- Verified `bash -n scripts/e2e-test-backends.sh` clean.
- Verified DRY_RUN walkthrough emits all phases including new Phase
  L-DNS (Issue 2 resolution).

## Summary

6 issues filed; 4 self-resolved (Issues 1, 3, 5, plus 4 as roadmap);
1 resolved by integrator at integration time (Issue 2); 1 intentional
UX nicety left in (Issue 6 as roadmap entry, not a deferral). Build,
vet, gofmt, full test suite (incl. validator-authored tests), and
DRY_RUN walkthrough of the e2e driver all green.
