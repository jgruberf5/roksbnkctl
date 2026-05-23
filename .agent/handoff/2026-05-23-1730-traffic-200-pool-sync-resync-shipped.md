# Handoff — HTTP 200 end-to-end + pool-member root cause + resync helper drafted

**Written**: 2026-05-23 17:30 (AEST)
**Branch**: `slice-11-tmm-sigsegv-fixes` (PR #24 open) — uncommitted work this session
**Cluster state**: `syd-tracer` UP. TMM 7/7. Test client EC2 `i-0387e9d852da361e7` UP with dual-ENI. Curl from BNK_EXT secondary ENI → VIP 10.0.10.100 returns **HTTP 200**.

## Where we got to this session

| Step | Outcome |
|---|---|
| Confirm prior session's HTTP 500 still reproduced via EICE-tunneled SSH | Yes — HTTP 500, RST gone, TMM accepting but not connecting to backend. |
| `kubectl debug` into f5-tmm pod netns → `ip route` | Same as prior handoff said: no `10.0.1.0/24` route, only `/32` for the TMM node (10.0.1.177). |
| `ping 10.0.1.76` from TMM netns | **Works** — TTL=126, via default route through `tmm` interface. So the missing kernel route is NOT the cause. |
| `tmctl pool_member_stat` filter for nginx pool | **Found real root cause**: pool member encoded `0A:00:01:46` = 10.0.1.70 (OLD pod IP). Current nginx pod is `10.0.1.76`. TMM has stale pool. |
| Check `kubectl get endpointslices nginx-cvvms` | Shows 10.0.1.76 — k8s state correct since 06:23:51Z. |
| Restart cne-controller pod | **No effect** — controller comes back, HTTPRoute shows `ResolvedRefs=True`, but TMM pool still has 10.0.1.70. |
| Annotation-bump on HTTPRoute | Controller explicitly ignores: `"Only CR status/finalizer is updated, ignoring the update"`. |
| Spec patch on HTTPRoute (weight 1→2→1) | **Triggers reconcile**: `"Updating HTTPRoute" + "GatewayReconciler: handling http route update"`. TMM pool member updates to `0A:00:01:4C` = 10.0.1.76. |
| Curl from BNK_EXT client | **HTTP 200**, 5/5 requests, 0.008s. ✓ |

## Root cause (validated, documented)

The F5 `cne-controller` resolves HTTPRoute `backendRefs` → Service → EndpointSlice → TMM pool members **only at HTTPRoute spec reconcile time**. It does NOT subscribe to EndpointSlice change events for user-defined backend services. When a backend pod is rescheduled, the EndpointSlice updates correctly but TMM's `pool_member` table retains the stale pod IP → HTTP 500 silently.

This is captured as a known issue in `aws-gpu-setup/SESSION_FINDINGS_2026_05_19_part4.md` line 82 ("bounce HTTPRoute to refresh stale pool members"). We added a clean reproduction + diagnostic + suggested fix to file with F5: `docs/upstream-issues/cne-controller-endpointslice-not-watched.md`.

**Why the prior handoff's "missing kernel route" theory was wrong:** the route table inspection was correct (no `10.0.1.0/24`), but TMM doesn't actually need that route for the pod IP — TMM's default route through its own `tmm` interface handles backend routing. The HTTP 500 was always pool-member resolution, never L3 routing.

Memory saved: `~/.claude/projects/.../memory/project_pool_member_sync_root_cause.md`. Indexed in MEMORY.md.

## What shipped this session (uncommitted)

```
docs/upstream-issues/cne-controller-endpointslice-not-watched.md   — F5 bug write-up
docs/prd/09-SCENARIOS-FRAMEWORK.md                                 — PRD adopting kindbnkctl scenario pattern
.agent/handoff/2026-05-23-1730-traffic-200-pool-sync-resync-shipped.md (this file)
.agent/tasks/active/slice-11b-bnk-resync/                          — Builder task scaffold + context bundle + TASK.md
```

**In flight (Builder agent):** `pkg/bnk.ResyncHTTPRoutes` + `awsbnkctl bnk resync` CLI. Task spec at `.agent/tasks/active/slice-11b-bnk-resync/TASK.md`. Acceptance: builds, tests pass, dry-run smoke against syd-tracer. Builder will write `work/v1.md` when done.

## Follow-up slices captured in the PRD

- **Slice-12**: new lifecycle phase that provisions the jumphost EC2 (dual-ENI: MGMT primary + BNK_EXT secondary) + EICE endpoint. Replaces the manual provisioning we did this session. State persisted to `state.env`; symmetric `down`.
- **Slice-13**: `internal/scenarios/` framework ported from kindbnkctl (`Scenario` interface, self-registration, `Manifests/Apply/Verify/Cleanup`), plus first scenario `http-routing-e2e` and the `awsbnkctl test traffic` alias. Reports include an ASCII env diagram of the exercised path.

## Recommended next-session prompt

```
/goal commit + push slice-11b (bnk resync + F5 issue + PRD + handoff). If Builder finished, verify acceptance; if not, resume Builder via the agent ID in the previous session.

Then start slice-12: new lifecycle phase that provisions the test jumphost EC2
(dual-ENI: MGMT primary + BNK_EXT secondary) + EICE endpoint. The current
syd-tracer cluster has one manually provisioned — read its config from AWS:
  aws ec2 describe-instances --instance-ids i-0387e9d852da361e7 \
    --region ap-southeast-2 --profile Users-292785712872
This is the reference shape; replicate via Go SDK in a new phase.

PRD: docs/prd/09-SCENARIOS-FRAMEWORK.md §"Slice-12".
```

## Don't re-investigate

1. Missing 10.0.1.0/24 kernel route — NOT the cause; pings work via default tmm route.
2. cne-controller restart fixes pool sync — IT DOES NOT; tested live.
3. Annotation/finalizer patches trigger reconcile — they DO NOT; explicitly ignored by controller.
4. EndpointSlice watch in cne-controller — not implemented for user backends; only internal F5 services have it.
5. Whether HTTP 500 is L3 routing — NO; it's pool-member resolution. Once pool member is current, curl works first try.
6. EC2 jumphost configuration — already running, working. Just needs a code phase to provision idempotently.

## Cluster state at handoff

```
syd-tracer:
  control plane: TMM 7/7 (3h36m+), 18/18 BNK subsystems Available, License Active, Gateway Programmed=True
  pool member:   10.0.1.76:80 (current — post-resync; will go stale if nginx pod restarts)
  VIP:           10.0.10.100 — returns HTTP 200 from BNK_EXT vantage

jumphost (i-0387e9d852da361e7):
  state:       running, $0.02/hour
  primary ENI: 10.0.1.128 (MGMT)
  secondary:   10.0.10.202 (BNK_EXT) on eni-05c06202744ed29dc
  SG:          sg-02d9cc6f5229fc934 (syd-tracer-tc)
  EICE:        eice-0b70f2dcc3ec845b0
  access:      aws ec2-instance-connect send-ssh-public-key ... then
               ssh -o "ProxyCommand=aws ec2-instance-connect open-tunnel ..." ...
```

Cluster + jumphost are idle but consuming money (~$50/day for the cluster + ~$0.50/day for the jumphost). Next session: validate the work + decide whether to keep cluster for slice-12/13 iteration or `down` it.
