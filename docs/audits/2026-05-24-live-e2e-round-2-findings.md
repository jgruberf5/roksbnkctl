# Live e2e round 2 findings — 2026-05-24 (post #34/#35/#36)

> Third cold-start attempt on syd-tracer after all original cold-start PRs
> (#27-#31), C-6 hotfix (#32), Finding #2 PR (#34), Finding #3 PR (#35), and
> the multi-script Phase 24b hotfix (#36) merged.
>
> Verdict: full e2e acceptance (5/5 HTTP 200 + zero residue + zero intervention)
> **STILL NOT REACHED**. New BNK runtime-layer issues uncovered, **NOT addressable
> in awsbnkctl** without escalating workarounds beyond reasonable scope.

## What did work on first cold try

- AWS infra phases 0-18: green, no retries.
- Phase 12 cert-manager + cert chain: green on first apply (proves the C-6
  RESTMapper Reset hotfix from PR #32).
- Phase 24b patched all 7 `--tls` scripts (proves PR #36 multi-script
  hotfix): `init.sh`, `liveness_probe.sh`, `readiness_probe.sh`,
  `sentinel_liveness_probe.sh`, `sentinel_readiness_probe.sh`,
  `sentinel_startup_probe.sh`, `startup_probe.sh`.
- Phase 25 controller kick fired at iter 6 (3 min) as designed (proves PR #34).
- All 9 control-plane pods reached `phase=Running` within 6 min.
- License CR reached `state=Active` within ~2 min.

## What blocked CNE Available=True (and hence acceptance)

Phase 25 timed out at full 9-min / 18-iter budget. CNEInstance Available
condition stayed `Failed` with three Pending sub-conditions:

### Finding #4 (NEW) — f5-tmm-pod-manager K8s API timeout, CrashLoopBackOff

- `f5-cne-controller-7b5988d4bc-nr2vg` pod: 4/5 Ready, `CrashLoopBackOff`,
  5 restarts in 12 min, last restart 2m41s ago.
- Failing container: `f5-tmm-pod-manager`.
- Last `--previous` log lines: pod-manager initializes, then errors:

  ```
  unable to set up indexers
  error: unable to register Pod deletionTimestamp field index:
    failed to get restmapping: failed to get server groups:
    Get "https://172.20.0.1:443/api": dial tcp 172.20.0.1:443: i/o timeout
  ```

  `172.20.0.1:443` is the EKS API server ClusterIP service. From inside the
  pod, this should always be reachable via kube-proxy. The fact that it
  times out suggests a host-device/Multus networking interaction with the
  TMM node's secondary ENIs that the f5-tmm-pod-manager container's net
  namespace can't route past.

- **Same pattern observed twice** (second + third cold-up attempts; this
  finding wasn't captured in the round-1 findings doc because pod-manager
  was self-recovering at the time of triage).

- **Hypothesis**: Multus secondary-network attachment on the TMM node
  alters its routing in a way that blocks the f5-tmm-pod-manager
  container's exec-time DNS or service-IP resolution. Possibly resolves
  once a kube-proxy reconcile fires after the TMM node's ENIs settle.

### Finding #5 (NEW) — f5-dssm StatefulSet replicas hang after `init.sh`

- `f5-dssm-db-2` (3rd replica, 2/3 Ready, restartCount=0 — never restarted).
- `f5-dssm-sentinel-0` (only sentinel created so far, 2/3 Ready, restartCount=1).
- Both pods exhibit the same log pattern: container starts, `Running init.sh
  for single cluster deployment`, `grpc server is starting up :19891`,
  then **silence** — Redis/Sentinel daemon never logs "ready" or binds
  to its port.
- Readiness probe (correctly patched with `--insecure` by Phase 24b) returns
  `Connection refused` because the daemon isn't listening on `:6379` (db) /
  `:26379` (sentinel) inside the pod.

- **NOT** the TLS hostname issue (Phase 24b fixed that for ALL 7 scripts —
  verified live).
- **IS** something in the BNK 2.3 f5-dssm container image's `init.sh` or
  s6-supervised wrapper that hangs on cold cluster. Possibly a race
  against DSSM cluster formation (sentinel needs primary to be reachable
  before it can join; primary needs sentinel quorum to be writeable; etc).

- aws-gpu-setup's `deploy-bnk.sh` doesn't appear to have a workaround for
  this. The previous slice-11 cold start that reached HTTP 200 was against
  a manually-provisioned cluster, not a fresh `awsbnkctl up` — the user
  may have hit this and bounced pods by hand without it being captured.

## What this means for the cold-start acceptance criterion

The user's stated acceptance: **"one clean cycle returns 5/5 HTTP 200 with
zero AWS residue and zero human intervention."**

After 8 merged PRs (#27-#31, #32, #34, #35, #36) the awsbnkctl-side cold
start is **as clean as we can make it without venturing into F5-image
workarounds beyond Phase 24b**. Two specific blockers remain, both at the
BNK runtime layer:

1. f5-tmm-pod-manager K8s API timeout → CrashLoopBackOff
2. f5-dssm sentinel + 3rd-replica hang after init.sh

Both might be fixable with awsbnkctl workarounds (e.g. a Phase 24c that
bounces stuck pods after N minutes), but at that point we're adding
escalating workarounds for what look like BNK 2.3 cold-start races.

## Escalation options (need user direction)

**Option A — Continue adding workarounds**:
- Phase 24c: bounce f5-tmm-pod-manager if it crashloops > 3 times.
- Phase 24d: bounce f5-dssm pods at 2/3 stuck > 2 min.
- Risk: each workaround is brittle and patches over an underlying image bug.

**Option B — Manual-bounce escape hatch + document**:
- Add a `awsbnkctl heal` subcommand that does the bounces manually.
- Document the procedure in the operator runbook.
- Acceptance stays "zero intervention" only for the warm path; cold-start
  requires `awsbnkctl heal` as a documented step.

**Option C — Escalate upstream to F5**:
- File issues against BNK 2.3 for the two cold-start races.
- Pin awsbnkctl to a future BNK 2.4 that resolves them.
- In the interim, document the manual workaround.

**Option D — Live-iterate with longer cluster time**:
- Spend more cluster time (= more $$) trying to characterise exactly when
  the races resolve naturally. May take 2-3 more cycles ($10-15 each).
- May or may not yield a clean code workaround.

## What landed in the cold-start reliability work overall

Across all 9 merged PRs (#27 + #28 + #29 + #30 + #31 + #32 + #34 + #35 + #36):

- 41 of 42 audit findings closed (1 = M-8 was wrong-deferred; now addressed
  by Phase 24b in PR #35 + #36).
- 6 new findings surfaced via live testing:
  - Finding #1: C-6 RESTMapper Reset (FIXED in #32).
  - Finding #2: Phase 25 budget + controller kick (FIXED in #34).
  - Finding #3: DSSM `--insecure` overlay (FIXED in #35).
  - Finding #3 follow-up: multi-script patch (FIXED in #36).
  - **Finding #4: f5-tmm-pod-manager API timeout (THIS DOC, OPEN — BNK
    runtime issue, escalate).**
  - **Finding #5: f5-dssm replica/sentinel hang after init.sh (THIS DOC,
    OPEN — BNK runtime issue, escalate).**
- Zero AWS residue verified on all 3 down cycles (H-3 SG-revoke working).
- Zero deferred bugs in awsbnkctl code per `feedback_no_deferred_fixes`.

## Cost summary

3 live cycles × ~$3-5 each = **~$10-15 total AWS spend**. All clusters
torn down cleanly with zero residue.
