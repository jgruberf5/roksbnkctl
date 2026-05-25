# Scenario suite live-validation cycle (cycle-4) — 2026-05-25

Cluster: `syd-tracer` (BNK 2.3.0, ap-southeast-2, host-device, m6i.4xlarge×3 + jumphost).
Branch: `fix/scenario-vip-overlap-l4status`. AWS account 292785712872.

## Result: 7/7 scenarios green

Final authoritative run = `awsbnkctl scenarios run --all` (one clean pass, exit 0):

| Scenario | Rating | Result |
|---|---|---|
| http-routing-e2e | green | ✅ |
| http-traffic-split | green | ✅ |
| proxy-protocol-l4 | green | ✅ |
| external-resource-pool | green | ✅ |
| multi-vip | green | ✅ |
| ai-token-counting | amber | ✅ (control-plane only) |
| ai-semantic-cache | amber | ✅ (control-plane only) |

up: exit 0, BNK activation complete (CNE ready, License Active, TMM 7/7, cne-controller 5/5).
down: exit 0, **zero AWS residue** (only the read-only gold-ref `aws-syd-test-cluster` remained).

## Bugs found live + fixed (all in this branch)

1. **http-traffic-split probed the wrong VIP.** Manifests pin the Gateway to `.101`, but Verify probed `.100` (the default from `BuildProbeParams`) — it never applied the `.101` override its siblings have. Data path was healthy all along; the probe hit the wrong gateway → `seenA/seenB=false`. Fix: add the `withLastOctet(vip, "101")` override.
2. **EICE SSH key TTL (~60s) exceeded mid-probe.** The key was minted once for a whole 10-iteration body-probe burst; 10 VIP curls exceed 60s, so later iterations failed `Permission denied (publickey)` before backend-a was sampled. Fix: re-push the key before each probe iteration.
3. **external-resource-pool jumphost responder never started.** Launching `python3 -m http.server` via `setsid nohup … &` over the EICE tunnel returned ssh exit 255 and never started. Fix: `sudo systemd-run` transient unit (validated live: rc=0, serves marker, persists, clean stop).
4. **Probe robustness:** added `scenarios.PollMarkers` and wrapped the marker-body probes for brief data-path convergence after `ResyncHTTPRoutes`.
5. **STEP-0 pre-cycle fixes:** narrowed http-routing-e2e's greedy `.100–.200` F5BnkGateway pool to single-address `.100` (so all scenarios' pinned VIPs coexist); hardened `waitL4RouteCondition` to check both `.status.parents[].conditions` and flat `.status.conditions`.

Confirmed: re-running the SAME scenario without `scenarios clean` first hits the SSA field-manager conflict on `.spec.rules` (Cycle-2 Finding #3) — clean before re-run.

## Forge integration — wired correctly (awsbnkctl side)

Root causes of "never added correctly to forge":
- The `forge:` block was commented out in `examples/syd-tracer/cluster.yaml`, so the lifecycle path (Phase09, link in `cl.StateDir()`) was never active → **enabled it** (Phase09 soft-fails, so `up` stays safe). Verified on `down`: `phase 09 down forge: no link, nothing to unregister` (graceful).
- The standalone `forge {register,status,unregister}` CLI read the legacy `~/.awsbnkctl/<ws>` workspace (us-east-1/bnk-demo) → **added `--config <cluster.yaml>` intent mode** (reads metadata.name/region, link in cluster state dir). `forge register --config` now resolves syd-tracer/ap-southeast-2 + reaches MCP.

**Remaining (forge-side, not awsbnkctl):** the running forge MCP server (:8081) rejects `create_project` ("Unknown tool"), though bnk-forge-v2 source defines it → the running forge stack is stale. End-to-end register→status→unregister blocked until the local forge MCP server is rebuilt/restarted.

## Egress — design spike (no scenario built)

Live recon resolved the AWS unknowns: src/dest-check already off on both TMM ENIs ✅; AUTOMAP self-IP 10.0.10.240 is a routable secondary on the ext ENI ✅; external subnet has **no internet route** (in-VPC egress only) ❌. Blocker (#2): the `internal-netdevice` NAD is host-device `ens7`, which is consumed by TMM — app pods can't join the internal VLAN that way, so egress needs `pseudoCNIConfig.vxlan` mode = a dedicated build. No egress scenario committed. Full detail: `../2026-05-25-egress-spike.md`.
