# Live e2e cold-start findings — 2026-05-24

> Live-test outcome of the cold-start reliability PR cycle (#27-#31 merged to main).
> Test: fresh `awsbnkctl up --config examples/syd-tracer/cluster.yaml --auto` on
> account 292785712872, ap-southeast-2.
>
> Result: cold start reached Phase 25 cleanly (all earlier phases green on first
> attempt apart from one hotfix below). Phase 25 timed out at the 6-min budget
> with CNE Available=False despite all pods Running.

## What worked end-to-end (1st try)

- Phase 00 preflight passed with the new CPU/mem/desiredSize checks (C-2).
- Phase 02-08 standard AWS infra (~10 min).
- Phase 10 m6i.4xlarge × 3 node group pinned to ap-southeast-2a (the host-device
  AZ default + C-3 example).
- Phase 17 ENI attach: ens7 (internal) + ens8 (external), SelfIPs assigned.
- Phase 17b jumphost ENI in BNK_EXT subnet.
- Phase 18 IRSA + OIDC + bi-directional SG ingress (the rules H-3 now revokes
  on down).
- Phase 11b EBS CSI + hugepages-ds.
- Phase 23 + 23b CRD waits with the 3-min trims (C-5) — both succeeded in ~10s.
- Phase 24 CWC heal Ready after ~150s.

## Finding #1 (HOTFIXED, PR #32) — C-6 RESTMapper Reset on NoKindMatch

**Symptom**: After Phase 12 successfully applied cert-manager v1.16.1 CRDs via
the new live RESTMapper (PR #29's C-6 fix), the immediately-following cert-chain
ClusterIssuer apply failed:

```
phase12: applying BNK cert chain: resolve cert-manager.io/v1/ClusterIssuer
  syd-tracer-selfsigned-cluster-issuer: no matches for kind "ClusterIssuer"
  in version "cert-manager.io/v1"
```

**Root cause**: `restmapper.NewDeferredDiscoveryRESTMapper` caches the API
surface; the cache pre-dates the cert-manager.io CRD install in the same up
run. The mapper never invalidates because the `p12CachedDiscovery` shim returns
`Fresh() = true`.

**Fix (PR #32)**: In `applyUnstructured`, on `meta.IsNoMatchError`, call
`Reset()` on the mapper (via type-asserted helper) to force a fresh discovery,
then retry the mapping once. Hard-fail if still unknown.

**Verification**: After hotfix landed (locally), Phase 12 → 23b all proceeded
cleanly on the re-run.

## Finding #2 (FIXED in PR #NN — pending) — Phase 25 6-min budget too aggressive for cold start

**Symptom**: Phase 25 activation poll exhausted its 12-iteration / 6-min budget
(trimmed from 40-iter/20-min by C-4) with:

```
last cne="" lic="Active" pods running=8 pending=0 failed=0 total=8
```

The C-4 diag dump (also new in this cycle) listed all 8 control-plane pods as
`Running` but the CNEInstance `Available` condition never flipped to True.

**Root-cause investigation** (after timeout):

- All f5-cne-core pods 2/2 Ready (rabbit, csrc x3, cwc, fluentd, otel, observer,
  ipam-ctlr, lifecycle-operator, crdconversion, observer-receiver).
- f5-cne-system pods mostly Ready: afm 2/2, analyzer 2/2, cne-controller 5/5,
  downloader 3/3, tmm 7/7, dssm-db-0 3/3.
- **But**: f5-dssm-db-1 stuck at 2/3 (f5-dssm container readiness probe failing
  for 12+ min — see Finding #3), f5-dssm-sentinel-0 stuck at 2/3 + restartCount=1.
- CNEInstance `Available` condition `lastTransitionTime` was at `02:44:27Z`
  (during initial reconcile) — **not re-evaluated** as pods later became Ready.
  Stale reason: "Failed" with the original unready-pod list from 10+ min ago.

**Two separate issues hiding here**:

a) **Budget too aggressive**: even if Finding #3 were fixed, cold-start pod
   bring-up is slower than 6 min in practice (dssm StatefulSet replicas come
   up sequentially; the second replica takes 2+ min after the first; CSRC
   DaemonSet on each of 3 nodes also bursts late). Recommend bumping
   `phase25MaxIter` from 12 → 18 (9 min cap) — still 50%+ tighter than the
   original 20 min, but accommodates a true cold start.

b) **CNE controller reconcile lag**: even with all pods Ready, the Available
   condition takes another reconcile interval to update. Workaround pattern is
   the same as the HTTPRoute pool-member bug
   (memory `project_pool_member_sync_root_cause`): trigger an explicit
   reconcile by patching the CNEInstance (e.g. annotation update). Could be
   added to Phase 25 after the budget elapses — equivalent to
   `pkg/bnk.ResyncCNEInstance` mirroring `pkg/bnk.ResyncHTTPRoutes`.

**Proposed fix (separate PR)**:

1. `internal/aws/phases/phase25_activation_poll.go`: `phase25MaxIter = 18`,
   update comments + dry-run message to "9 min".
2. After (say) 6 polls (3 min) where Available is False AND all sub-condition
   pods are Running, do a no-op patch on the CNEInstance to trigger reconcile
   (mirrors the HTTPRoute pattern). This is the controller-kick workaround.
3. Document the dual workaround in the project memory and a follow-up audit.

**Fix shipped**: Phase 25 budget 12→18 iter (9 min cap); `pkg/bnk.ResyncCNEInstance`
helper added; Phase 25 auto-kicks the cne-controller once after iter 6 if pods are
Running but Available is stale. See `pkg/bnk/resync_cne.go`.

## Finding #3 (OPEN — needs investigation) — DSSM StatefulSet replica Redis startup failure

**Symptom**: `f5-dssm-db-1` (StatefulSet replica) startup probe failing for
12+ minutes:

```
Warning Unhealthy Startup probe failed: Exiting as DB startup probe failed...
```

(repeated every 10s)

The probe shells in and runs `redis-cli -p 6379 --tls --cert/key/cacert ping`,
which returns `Connection refused` — Redis daemon never bound to port 6379
inside the pod.

Container logs show only initial startup (s6-rc supervisor, dssm_wrapper.sh,
qkd grpc server on :19891) but no Redis "Ready to accept connections" line —
even after 12 min. The supervisor isn't restarting the dssm process
(`restartCount: 0`).

**Same StatefulSet, dssm-db-0, is healthy (3/3 Ready).** So this is specific
to replica startup, not primary.

**Hypothesis**: dssm-db-1 is waiting on something from dssm-db-0 (replica
sync handshake?) that requires the sentinel to mediate; sentinel itself is
2/3 with restartCount=1, suggesting a related quorum/timing issue.

**Sweep memory M-8 connection**: M-8 talked about a DSSM `--insecure` overlay
that was deferred as "only matters at first BNK consumer Gateway creation."
That assessment was **wrong** — DSSM readiness is on the activation-poll
critical path. Whether `--insecure` is the right fix here is unclear (the
probe IS using TLS, but connection-refused suggests the server isn't even up,
which is a different issue).

**Next steps (not yet done)**:

1. Reproduce on a fresh cluster with `kubectl exec` into dssm-db-1 immediately
   on creation to watch what the supervisor does.
2. Compare against the aws-gpu-setup reference deployment's DSSM pod spec to
   see whether they apply a JSON patch or args override.
3. Cross-reference with FLO Helm chart 2.21.13 release notes for any known
   DSSM cold-start race.

This finding **blocks** the cold-start acceptance criterion (5/5 HTTP 200 e2e)
until resolved.

## Other observations (informational)

- **`forge: not enabled, skipping`** — syd-tracer cluster.yaml has no `forge:`
  block, so Phase 09 noops. H-6's `credentialTemplateId` plumbing is in place
  but un-exercised here.
- **Phase 17b idempotency** — re-run after the C-6 hotfix correctly skipped
  re-creating the jumphost ("instance i-... already running — skipping launch")
  — H-3's tag-discovery / state-cache path is sound.
- **Phase 18 SG ingress rules** "added (or already present)" — idempotency on
  re-apply working.
- **C-4 diag dump fired correctly** on timeout — listed all 8 pods with phase
  + reason. This is what the operator wanted (vs the previous opaque 20-min
  hang).

## Cost / cleanup

Cluster ran ~50 min from up start to down trigger. Approx AWS spend: ~$3
(EKS control-plane $0.10/hr + 1× m6i.4xlarge $0.96/hr + 1× t3.small jumphost
+ NAT GW + 1 EIP).

Down running at time of writing — will verify zero residue after completion.

## Open the next PR

A follow-up audit doc + PR should:

1. **Land hotfix PR #32** (RESTMapper Reset) — done; in CI.
2. **New PR**: `fix(cold-start): phase 25 budget 12→18 + controller-kick`
   addressing Finding #2.
3. **New PR or escalation**: DSSM replica investigation (Finding #3). Could
   be its own slice if Helm-chart-driven; could be a small patch if it's just
   a probe tuning issue.

Plus the kill-legacy-tf PR (H-1/H-2 + L-4/L-11/L-12/M-12) still owed, which
should be split from the cold-start work entirely.
