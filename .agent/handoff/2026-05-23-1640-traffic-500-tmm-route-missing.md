# Handoff — Traffic test client wired; RST→HTTP 500; TMM missing pod-CIDR route

**Written**: 2026-05-23 16:40 (AEST)
**Branch**: `slice-11-tmm-sigsegv-fixes` (PR #24 open) — pushed
**Cluster state**: `syd-tracer` UP. TMM 7/7. Test client EC2 `i-0387e9d852da361e7` UP with dual-ENI (primary 10.0.1.128 mgmt + secondary 10.0.10.202 BNK_EXT). EC2 Instance Connect Endpoint `eice-0b70f2dcc3ec845b0` provisioned (SSH from this laptop's IP is being silently dropped by the upstream network for 3.x.x.x AWS IPs — TCP/80 succeeds, 22 times out — bypassed via EICE WebSocket tunnel).

## Where we got to this session

| Step | Outcome |
|---|---|
| 5 control-plane fixes in `d994f08` (PR #24) | TMM 7/7 Ready, 18/18 BNK subsystems Available, License Active, Gateway Programmed=True, VIP 10.0.10.100 lands on BNK_EXT ENI. |
| Launch test client EC2 with dual-ENI per Tokyo / aws-gpu-setup pattern | EC2 `i-0387e9d852da361e7` running: ens5=10.0.1.128 (mgmt, SG `syd-tracer-tc`), ens6=10.0.10.202 (BNK_EXT, SG_BNK_DATA). EICE-tunneled SSH works (`aws ec2-instance-connect open-tunnel ...` proxycmd + `id_ed25519`). |
| First curl from `curl --interface 10.0.10.202 http://10.0.10.100/` | TCP connected, GET sent, **Connection reset by peer** at TMM. tmctl confirmed VS exists, `no_acl_match=30`, pool_member table only has `snat_automap` (no nginx backend). |
| `cloud-network-mapping` ConfigMap audit | **Found 6th bug**: `MGMT_SUBNET` in state.env was a STALE subnet ID (`subnet-0ad9aec3dd55e2fe3` from a previous session's cluster). `ensureMGMTSubnetAlias()` had an early-return if value already set — preserved the stale value across runs. CM rendered with wrong subnet ID for 10.0.1.0/24 → cne-controller computed wrong backend gateway → no static route into TMM. |
| Live-patched the CM with correct subnet ID + restarted cne-controller | Pool member 10.0.1.70:80 NOW appears in TMM's `pool_member_stat`. Static routes `static-route-10.0.1.70-0` and `auto-node-subnet-route-10-0-1-177-32` pushed via gRPC. |
| Code fix: `ensureMGMTSubnetAlias` always recomputes (commit `4fc851f`) | Pushed to PR #24. Tests updated. |
| Curl again from BNK_EXT client | **HTTP 500 (Server: BigIP)** — RST gone, TMM accepts request, applies policy, but can't reach backend. |
| Inspect TMM kernel route table (`ip route` in debug sidecar — shares netns with f5-tmm) | **Missing the cluster pod CIDR route.** Present: `default via 169.254.0.254 dev tmm`, `10.0.1.177 via 10.0.10.1 dev ext-vlan` (only the TMM node's /32), `10.0.10.0/24 dev ext-vlan`, `10.0.20.0/24 dev int-vlan`. **Should have but DOESN'T**: `10.0.1.0/24 via 10.0.20.1 dev int-vlan` (per `aws-gpu-setup/bnk-tmm-recovery-runbook.md` "TMM internal routing" section). Without this, when TMM SNATs to 10.0.10.240 and tries to forward to 10.0.1.70:80, the packet has no route → TMM returns HTTP 500. |

## Why the route is missing

cne-controller pushes `auto-node-subnet-route-<node-ip>-32` — one /32 per *cluster node* (we have 3 nodes: ip-10-0-1-{177,195,200}). We only see the route for 10.0.1.177 because that's the only node TMM knows about as "BNK-eligible" — only that one has the `app=f5-tmm` label.

Tokyo's working setup had *the whole 10.0.1.0/24* routed via int-vlan, not per-node /32 routes. Two hypotheses for what's different:

1. **BNK 2.3.0 changed the route-pushing semantics** vs. 2.2.x (Tokyo). Now controller only pushes /32 routes for BNK-labeled nodes. For nginx-backend hosted on a different node, the route doesn't exist.
2. **We're missing a cluster-wide setting** that tells cne-controller to push the whole subnet route. The `cloudNetworkMapping` ConfigMap structure might support a flag we're not setting.

Workarounds to try in next session:
- **A. Move nginx onto the TMM node only** (we already have `nodeSelector: app=f5-tmm` in `/tmp/test-traffic.yaml`). Pod IP 10.0.1.70 then matches the `10.0.1.177/32` route via TMM's own backplane? Unclear if that helps — the route is to the *node* IP, not pod IPs on that node. Need to verify nginx scheduled on the same node (it is — we confirmed earlier).
- **B. Manually inject the kernel route via the debug sidecar** as a one-off proof: `ip route add 10.0.1.0/24 via 10.0.20.1 dev int-vlan` inside the pod netns. If HTTP 200 returns, the BNK chart is missing this push; we file an F5 bug or add it via daemonset.
- **C. Patch CNEInstance with a `routes:` block** that tells FLO to add the subnet route. The CRD might have it but our template omits it. Check `kubectl explain f5tmm.spec` for route-related fields.

## Static-route a2 encoding (decoded)

The gRPC pushes use F5's declTmm encoding where IPv4 lives in the high 4 bytes of a uint64 with the bytes in network order, written as a 64-bit number:

- `a2:5044313104876240896` = `0x4601000A00000000` → bytes `46 01 00 0A` little-endian → `10.0.1.70` (nginx pod IP) ✓
- `a2:12754475666934530048` = `0xB0EDA88000000000` → I couldn't decode this cleanly to a recognizable IP. Likely a different encoding for the gateway value (maybe network-aware, with a zone ID prefix), OR this represents the TMM-internal mesh address.

Decoding the gateway address may reveal what cne-controller is asking TMM to use as the next-hop for the static route — and whether that gateway IP is even reachable from TMM's netns.

## Test client + EICE — keep or tear down?

```
EICE: eice-0b70f2dcc3ec845b0 (free; in public subnet, allows tunneled SSH without an external SG opening)
TC EC2: i-0387e9d852da361e7 (t3.small, ~$0.02/hour AUD ~ $0.50/day)
TC SG: sg-02d9cc6f5229fc934 (syd-tracer-tc) — has SSH from 180.150.47.79/32
TC secondary ENI: eni-05c06202744ed29dc at 10.0.10.202 in BNK_EXT
SSH access pattern from this laptop:
  aws ec2-instance-connect send-ssh-public-key --instance-id i-0387e9d852da361e7 \
    --instance-os-user ubuntu --ssh-public-key file://~/.ssh/id_ed25519.pub
  ssh -o "ProxyCommand=aws ec2-instance-connect open-tunnel --instance-id i-0387e9d852da361e7" \
      -i ~/.ssh/id_ed25519 ubuntu@i-0387e9d52da361e7
  (auto-allow ProxyCommand options need the AWS_PROFILE/REGION env or --profile flags)
```

## Recommended next-session prompt

```
/goal land traffic HTTP 200 end-to-end on syd-tracer + extend awsbnkctl with `test traffic` subcommand and code-fix the TMM-kernel-route bug.

Cluster + test client are still up. Read .agent/handoff/2026-05-23-1640-traffic-500-tmm-route-missing.md first — it documents WHY we're at HTTP 500 and the three workaround paths (A/B/C) to try.

Concrete first step: SSH into the test client via EICE and curl the VIP — confirm we're still at HTTP 500 (or it may have settled to 200 if the controller continued pushing more routes). Then try workaround B (manually inject `ip route add 10.0.1.0/24 via 10.0.20.1 dev int-vlan` via the debug sidecar). If 200 returns, that's the missing piece — file a BNK bug + add a Phase 23c that pokes the route in.

Account: 292785712872 ap-southeast-2. SSO: aws sso login --profile Users-292785712872.
```

## Files touched this session (post PR #24)

```
internal/aws/phases/phase19_cloud_network_mapping.go      — MGMT_SUBNET always recompute
internal/aws/phases/phase19_cloud_network_mapping_test.go — assertion updated
.agent/handoff/2026-05-23-1640-traffic-500-tmm-route-missing.md (this file)
```

Already-committed in PR #24 (pushed to origin):
- `d994f08` — 5 control-plane fixes
- `92843e3` — original handoff
- `4fc851f` — MGMT_SUBNET fix (this session's extension)

## Don't re-investigate

1. PCI BDFs (verified correct).
2. cgroup v2 (verified fine).
3. tmm-core emptyDir vs hostPath (it's emptyDir, per-pod, no stale).
4. crashagent "core file generated by host" log — startup status, not a crash.
5. TMM_CPU=4 / PalCpuSet=0-3 — root cause of SIGSEGV, ALREADY FIXED in d994f08.
6. IRSA trust-policy SA-name mismatch — ALREADY FIXED in d994f08.
7. resolveGVR missing F5SPKVlan/GatewayClass — ALREADY FIXED.
8. Phase 25 readiness check via conditions[] — ALREADY FIXED.
9. SG bi-directional rules — ALREADY FIXED.
10. MGMT_SUBNET stale-value preservation — ALREADY FIXED in 4fc851f.
11. SSH from this laptop directly to 3.x AWS IPs — silently dropped somewhere upstream. Use EICE tunnel.
