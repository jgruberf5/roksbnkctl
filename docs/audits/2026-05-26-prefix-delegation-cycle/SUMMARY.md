# Cycle 5 — CNI prefix-delegation + pciBusID NAD live validation (2026-05-26)

Cluster: `syd-tracer` (BNK 2.3.0, ap-southeast-2, host-device, m6i.4xlarge×3 + jumphost).
AWS account 292785712872. Two ups this cycle: 5b (the validated re-provision) after 5a (the diagnostic failure).

## Headline

A recurring cold-start failure — BNK activation hanging at **licensing** (rabbitmq unreachable → CWC crash-loop → TMM never licensed → Phase 25 timeout) — was root-caused to the **AWS VPC CNI secondary-ENI asymmetric-routing + source/dest-check** interaction, and fixed at the source with **prefix delegation**. Validated end-to-end: clean first-poll activation, single-ENI worker nodes, 7/7 ingress scenarios green.

## Root cause (cycle 5a, the failed up)

`up` exited 1 at Phase 25: all pods Running but `F5TmmAvailable=False`, `License=""`. Diagnosis chain:

1. `f5-lic-helper` + `f5-spk-cwc` both fail `dial tcp <rabbitmq-clusterIP>:5671: i/o timeout`. CWC crash-loops (7–13 restarts).
2. Probes from a pod on node A to the rabbitmq **pod** on node B: ClusterIP **and** pod-IP both time out; the kube-API ClusterIP works. → not kube-proxy, not DNS, not the listener.
3. `i/o timeout` (not connection-refused) = SYN black-holed = a network-layer drop.
4. The unreachable pod's IP was a secondary IP on the node's **secondary ENI** (device-index 1); a reachable pod on the same node was on the **primary ENI**. Same SG, same subnet, `SourceDestCheck` identical → not SG/src-dest at the static layer.
5. `tcpdump` on the destination node: the ICMP request **arrives on the secondary ENI**, the pod replies, but the **reply egresses the *primary* ENI** (the CNI routes in-VPC traffic via the main table). The reply's source IP belongs to the secondary ENI, so AWS **source/dest check on the primary ENI drops it** → 100% loss to secondary-ENI pods.

This only bit when a critical pod (rabbitmq) happened to land on a secondary ENI — which `WARM_ENI_TARGET=1` makes happen eagerly. Per-run scheduling is why it was intermittent ("usually got past licensing, stuck on use-cases").

Confirmation: disabling source/dest check on the destination node's primary ENI made the pod immediately reachable (5/5 ping, 5671 open) — proving the mechanism. But disabling src/dest check is an **unsupported workaround** (AWS closed the VPC-CNI feature request "not planned"; needs re-applying per ENI).

## Fix: prefix delegation (the standard EKS answer)

`ENABLE_PREFIX_DELEGATION=true` makes the CNI assign /28 prefixes on the **primary** ENI instead of attaching a secondary ENI. BNK is ~25–30 pods/node — fits on the primary ENI → **no secondary ENI → the asymmetry cannot occur**. AWS-recommended on Nitro.

Implemented as a new phase `Phase08bVPCCNIPrefix` (adopts the `vpc-cni` managed addon with `{"env":{"ENABLE_PREFIX_DELEGATION":"true","WARM_PREFIX_TARGET":"1","WARM_ENI_TARGET":"0"}}`, `ResolveConflicts=OVERWRITE`, no pinned version), sequenced **after Phase09ForgeRegister, before Phase10NodeGroup** so nodes boot in prefix mode. Poll tolerates non-ACTIVE pre-nodegroup (0 DaemonSet pods) to avoid hanging. `Down` is a no-op (addon dies with the cluster). max-pods left at the AL2023 default (ample for our density). Architect-validated; the critical catch was removing the `EXTERNAL_PCI`/etc. overwrite from Phase19 (would clobber discovery).

### Validated live (cycle 5b)
- vpc-cni addon ACTIVE with the prefix config; aws-node env confirms the flags.
- **Every worker node has a single CNI ENI (device-index 0 only).** The TMM node adds only its data ENIs (idx 2/3). **No secondary/spillover ENI anywhere.** Bonus: device-index 1 is now free on the TMM node (relaxes the host-device ENI-limit).
- rabbitmq + CWC healthy, **0 restarts**, cross-node licensing (rabbitmq on one node, lic-helper on another) succeeded.
- Phase 25: `[1/18] ready=true lic=Active` — clean activation on the **first poll** (vs 18/18 timeout in 5a). `UP EXIT: 0`, postflight all OK, `functionallyReady=true`.

## pciBusID NADs (gold-ref-aligned)

The host-device NADs now select the TMM data NICs by **PCI bus address** (`pciBusID: 0000:00:08.0` external / `0000:00:07.0` internal) instead of kernel ifname (`ens8`/`ens7`). Rationale: ifnames are derived from the PCI slot (udev) and phase17 itself warns they could drift; the gold-ref `aws-syd-test` already uses `pciBusID`. Recon confirmed the gold-ref NAD form (and that its CNEInstance keys env vars on in-pod `eth1/eth2`, not host ifnames — noted as a separate, out-of-scope convention question). Validated: TMM bound, ingress green.

## SSA-clean fix

`scenarios.ApplyManifests` now applies with `Force: true` (centralized helper; 8 scenarios collapsed to one-liner `Apply()`). Root cause: the pool-member resync JSON-patches HTTPRoute weights, leaving an `awsbnkctl`/Update managedFields entry that a non-forced SSA Apply conflicts with on re-run. **Validated live:** re-ran http-routing-e2e without `scenarios clean` → no `.spec.rules` conflict (this errored in prior cycles).

## Node-side MAC discovery (built, not yet validated)

To remove the hardcoded `ens7/ens8`/`07.0/08.0` constants, a new `Phase17cIfaceDiscovery` runs a one-shot privileged pod on the TMM node that maps each f5-cne-device ENI (by MAC) to its real ifname + PCI BDF, writing them to state; the NAD + CNEInstance render now source from state (constant fallback for dry-run/back-compat), and the CNEInstance `PCIDEVICE_INTEL_COM_<IFNAME>` env-var name is templated from the discovered ifname. Hard-fails (no silent fallback). Architect-validated, Lead-verified, gates green. **NOT in this cycle's binary — validates on the next up.** Sets us up for a future SR-IOV variant (PCI-BDF addressing).

## Scenario suite: 7/7 ingress green

http-routing-e2e, http-traffic-split, multi-vip, proxy-protocol-l4 (green), external-resource-pool (green), ai-token-counting, ai-semantic-cache (amber, control-plane). All HTTP 200 / markers as expected. Confirms prefix-deleg + pciBusID + SSA do not regress ingress.

## Egress (egress-snat) — data path BLOCKED + destructive (stays Amber)

The suite initially failed at http-routing-e2e. Diagnosis: the **egress-snat scenario's F5SPKEgress** (`vxlan.create=true`) made TMM install a **far-too-broad route `10.0.0.0/17 dev vxlan100`** capturing *all* pod traffic, but **CSRC cannot create the node-side `vxlan100` VTEP** (`AddDefaultRoute: failed to get link by name [Link not found]`, `RouteState=RouteAddFailed`). Tunnel half-up (TMM side only) → all TMM→in-cluster-pod traffic black-holed. Cleaning egress-snat restored the route and http-routing-e2e went green — proving (a) our fixes are fine and (b) egress is the culprit.

Two distinct issues:
1. **Egress VXLAN data path does not come up** — node-side `vxlan100` VTEP is never created (the multi-step pseudo-CNI VXLAN build the spike flagged as unproven). Egress-snat cannot be promoted to Green.
2. **egress-snat is destructive in `scenarios run --all`** — its broken VXLAN hijacks the whole pod network and breaks every in-cluster-backend ingress scenario applied after it. Needs a route-scoping/guard fix regardless of the data-path work.

## Forge — blocked forge-side (not awsbnkctl)

The rebuilt `bnk-forge-mcp` container fixed the original `create_project` "Unknown tool" blocker (the tool is invoked fine now). New blocker is **forge-side**: the MCP server gets `401 Invalid username or password` proxying to the forge REST backend; a direct `admin/changeme` login also 401s → **the forge admin password has changed**. Phase 09 soft-failed gracefully (cluster unaffected). `reference_localhost_forge_credentials` memory (admin/changeme) is now stale.

## Topology smoke ✅
`awsbnkctl topology --config` against the live cluster populated all live-only fields from state.env (VPC id, IGW/NAT, jumphost + EICE + mgmt/bnk-ext IPs, BNK data subnets, TMM VLAN self-IPs, tmm-node). First live validation of those fields.

## Follow-ups (tasks)
1. **Egress node-side VXLAN** — root-cause why CSRC can't create the `vxlan100` VTEP on the node (dedicated effort; the spike's hard blocker).
2. **Guard egress-snat** — scope its route to the egress namespace / don't let a non-functional VXLAN hijack the pod network + break ingress; exclude from `--all` until fixed.
3. **Validate node-side MAC discovery** on the next up.
4. **Retire/early-exit Phase 24c pod-manager heal** (and slim Phase 24 CWC heal) — the severe "self-heal takes hours" wedge was the secondary-ENI black-hole, now fixed by prefix delegation; 24c's unconditional 7-min poll is likely pure overhead per up. Validate over 1–2 more clean cycles, then retire. Fix the 24c comment (it mis-attributes the cause to a kube-proxy timing race).
5. **Forge** — supply the MCP server valid backend creds; update `reference_localhost_forge_credentials`.
