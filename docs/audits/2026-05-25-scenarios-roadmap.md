# Scenario suite roadmap (2026-05-25)

Goal (user-directed): expand the `awsbnkctl scenarios` suite beyond `http-routing-e2e` (#8) to cover
F5 BNK how-tos #6–#10 + traffic-split + multi-VIP + egress. Triangulated from three sources:
clouddocs how-tos, `mwiget/kindbnkctl` (port-ready patterns), and the live gold-reference cluster
`aws-syd-test-cluster` (read-only survey).

## Decisions (user, 2026-05-25)
- Sequence: **verifier fix + 3 Green ports first** (Batch 1), then the rest (Batch 2).
- Traffic-split flavor: **HTTP weighted** (not L4).
- Egress: **design spike first** (no port-ready reference; investigate feasibility).
- AI how-tos #6/#7: **build as Amber** (control-plane reconcile only; no enforcement/cache backend testable on EKS host-device).

## Reference sources
- **clouddocs**: how-tos index at clouddocs.f5.com/bigip-next-for-kubernetes/latest/how-tos/. #8 = HTTP steering (done). #9 = Proxy Protocol iRule (L4). #10 = LB to external resources. #6 = token counting (AI). #7 = AI optimization/semantic cache.
- **kindbnkctl** (`internal/scenarios/`): port-ready dirs — `proxyprotocol` (#9), `extrespool` (#10), `aitokencount` (#6), `aisemcache` (#7), `tcpl4lb` (L4 70/30 split), `httproutee2e` (#8). NO multi-VIP, NO egress. Verifies via in-cluster FRR/BGP pod (we use jumphost EICE+curl instead).
- **aws-syd-test (gold reference, live)**:
  - GatewayClass `bnk-gatewayclass` (ctrl `f5.com/f5-operator-f5-cne-controller`).
  - **Live weighted HTTP split**: `default/llm-route` (NO hostname) → `nova-micro:8080 w33`, `nova-lite:8080 w33`, `claude-3-haiku:8080 w34`, parent `llm-gateway` @ `10.0.11.100`. ← traffic-split reference.
  - **Chassis F5BnkGateway**: `f5-operator/bnk-gateway-chassis` → one pool `defaultListenerNetworks: [{name: external-net, start 10.0.11.100, end 10.0.11.200}]`. Gateways in app namespaces draw VIPs from this shared pool. ← multi-VIP reference (multiple Gateways, distinct VIPs from one chassis pool).
  - **Egress CRs exist** (not currently configured for internet egress): `F5SPKEgress` (k8s.f5net.com/v3), `F5SPKSnatpool`, `F5SPKStaticRoute`, plus `Vrf`/`GreTunnel`/`RoutingConfig`/`GlobalRoutingConfig`. ← egress spike must design around these.
  - **Return-path static route** (live): `f5-operator/return-to-jumphost-subnet` F5SPKStaticRoute `{destination 10.0.1.0/24, gateway 10.0.11.1, interface external, type gateway}`. ← how return traffic reaches the jumphost subnet.
  - perf-proxies ns: envoy/haproxy LLM perf backends; bnk-demo-client: smartllm-client. open5gs ns present (5G core) but no live Gateway/route CRs.

## Batch 1 — build now (code-only; one later cold cycle validates all)

### 1. Verifier correctness fix (Cycle-3 Finding #1) — shared, unblocks reliable 5/5 for ALL scenarios
Root cause CORRECTED (not timing — the Gateway-Programmed wait passed):
- **(a) Missing `Host` header** — `internal/jumphost/jumphost.go:150` curls `http://<vip>/` with no `Host:`; our HTTPRoute is hostname-scoped (`awsbnkctl.local`) → 404. Fix: add `Hostname` to `ProbeOptions` + `-H "Host: <hostname>"`; thread the scenario's HTTPRoute hostname through. (aws-syd-test's llm-route avoids this by using NO hostname — an alternative, but the Host-header fix is more general and keeps hostname-match coverage.)
- **(b) F5BnkGateway GVR typo** — `httproutee2e/scenario.go:208` resource `f5bnkgateways` → must be `f5-bnkgateways`.
- **(c) Diagram `(unknown)` (cosmetic)** — `runner.go:123` `EnvDiagramInput` omits `Namespace`; live reads short-circuit. Fix: expose scenario namespace to the env-diagram.

### 2. Port #9 `proxy-protocol-l4` (Green) — from kindbnkctl `proxyprotocol`
L4Route + `F5BigCneIrule` (PROXY v1 iRule) + `BNKNetPolicy`. Verify nginx `proxy_protocol` sees real client IP. Swap FRR-curl → jumphost. Pin a distinct VIP (e.g. `.106`).

### 3. Port #10 `external-resource-pool` (Green) — from kindbnkctl `extrespool`
F5 `Pool` CR (`k8s.f5net.com/v1`) as HTTPRoute backendRef (off-cluster/non-Service backend). Pin a distinct VIP (e.g. `.101`).

### 4. New `http-traffic-split` (Green) — HTTP weighted, mirror aws-syd-test llm-route
Two+ nginx backends with distinct markers, weighted HTTPRoute backendRefs (e.g. 70/30 or 33/33/34). Curl N times via jumphost (with Host header per fix #1), assert BOTH backends served (don't assert exact ratio at low N — kindbnkctl precedent). Exercises the pool-member resync with real distribution. Pin a distinct VIP.

## Batch 2 — after Batch 1

### 5. AI #6 `ai-token-counting` (Amber) — from kindbnkctl `aitokencount`
Gateway `spec.infrastructure` annotation `k8s.f5.com/ai-token-counting`. Assert annotation reconciles; no enforcement backend → Amber (control-plane only).

### 6. AI #7 `ai-semantic-cache` (Amber) — from kindbnkctl `aisemcache`
Gateway `k8s.f5.com/ai: semantic_cache=enabled`, HTTPRoute `k8s.f5.com/sse-enabled`. Amber (no model/cache backend).

### 7. `multi-vip` (Green, net-new) — model on aws-syd-test chassis pattern
Either two Gateways drawing two VIPs from one F5BnkGateway pool range, or two listeners. Two HTTPRoutes → two backends. Curl both VIPs, assert each serves its backend. No upstream scenario reference; aws-syd-test chassis pool is the model.

### 8. Egress design spike (net-new, investigation) — NO port-ready reference
Investigate `F5SPKEgress` + `F5SPKSnatpool` + `F5SPKStaticRoute` to make a pod/source egress through TMM to an external target with SNAT, and how to VERIFY on AWS (source IP seen externally, e.g. curl httpbin.org/ip). Note aws-syd-test only has a return-path static route, not internet egress — so this is genuinely new. Output: feasibility + a concrete scenario design (or a "not feasible on host-device/EKS without X" finding).

## Build-time findings & status (2026-05-25)

**clouddocs 2.3 is the AUTHORITATIVE CRD reference** (user directive) — over kindbnkctl, which is a different distribution/version. Index: `clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/`.

Key corrections found by cross-checking clouddocs:
- **No `Pool` CR in BNK 2.3.** Only `F5SPKSnatpool` (SNAT-only, no members). #10 was initially built on `kind: Pool` (would fail at apply) and REWORKED to the documented **EndpointSlice** path (selectorless Service + hand-managed EndpointSlice → external IP; HTTPRoute core-Service backendRef). The undocumented `F5pool` CRD teased in 2.3 release notes (no published schema) is deferred — inspect via `kubectl explain f5pool` on a live 2.3 cluster.
- **F5BnkGateway has no documented `provider` field** (docs list only name/ipv4BaseCidr/ipv6BaseCidr/startAddress/endAddress). httproutee2e uses `provider: f5-ip-provider` and cycle-3 worked, so it's tolerated/ignored — minor cleanup candidate, not a blocker.
- #9 CRs all CONFIRMED in clouddocs: L4Route (`gateway.k8s.f5net.com/v1`, `pvaAccelerationMode` real), F5BigCneIrule (`spec.iRule`, `f5-big-cne-irules`), BNKNetPolicy (`bnknetpolicies`, extensionRefs/targetRefs).

**Batch 1 — COMPLETE & committed** (branch docs/audit-sydney-cross-check, unpushed):
- `1f8e575` verifier fix · `6de7587` scaffolding extraction · `53f3f7e` http-traffic-split · `2843bcf` proxy-protocol-l4 · `c05d2fb`→`9fe3b8b` external-resource-pool (Pool→EndpointSlice rework).

**Open LIVE-VERIFY items (next spin-up):**
1. All four scenarios are code-complete but only http-routing-e2e ran live (cycle 3). traffic-split / external-resource-pool / proxy-protocol-l4 await a live cycle.
2. #9 L4Route Accepted status JSON path — assumed `.status.parents[].conditions` (docs didn't print it); confirm, else adjust `waitL4RouteCondition`.
3. #10 EndpointSlice external-LB — confirm BNK's cne-controller resolves a selectorless Service's hand-managed EndpointSlice to the external (jumphost) IP.
4. VIP coexistence — httproutee2e's F5BnkGateway pool is greedy (.100–.200) and overlaps the new scenarios' pinned VIPs (.101/.102/.103). To run ALL scenarios in one cycle, narrow httproutee2e's pool to single-address .100 (or assign non-overlapping ranges). Tracked here, not yet done.
5. Scenarios `clean` SSA field-manager ownership (Cycle-2 Finding #3) — relevant once multiple scenarios run/clean in one cycle.

## Porting notes (apply to all ports)
- **Verification**: replace kindbnkctl's FRR/BGP-pod curl with awsbnkctl jumphost EICE+SSH+curl (the httproutee2e pattern), WITH the Host header (Batch-1 fix #1).
- **VIPs**: each scenario pins a distinct /32 from `10.0.10.0/24` to avoid collisions (`http-routing-e2e` uses `.100`). Or adopt a chassis-pool model later.
- **Apply**: MUST use `internal/k8s.ApplyOptions.Run` (SSA + live RESTMapper), never `applyRawYAML`.
- **Register**: side-effect import in `internal/cli/scenarios.go`.
- **Open**: Cycle-2 Finding #3 (scenarios `clean` leaves SSA field-manager ownership) still affects re-runs — relevant once we run multiple scenarios in one cycle.
