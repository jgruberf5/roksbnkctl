# Egress design spike (2026-05-25)

Outcome of the Batch-2 egress "design spike first" (user-directed). Authoritative source: clouddocs 2.3.
**Verdict: egress IS a documented BNK feature, but it is NOT yet buildable as a confident Green scenario on AWS/EKS host-device** — clouddocs documents the CRs and the on-prem/DPU data path but gives ZERO AWS-specific guidance, and several AWS prerequisites are unverified. Recommend a small live spike before committing a scenario (or ship a control-plane-only Amber first).

## What's documented (clouddocs 2.3)
No dedicated egress how-to. Egress lives in the CRD reference + the "Deploying Applications and Testing Traffic" page.

### CRs (verbatim from clouddocs)
- **F5SPKEgress** — `k8s.f5net.com/v3`, kind `F5SPKEgress`, plural `f5spkegresses`. Key spec:
  - `snatType` (default `SRC_TRANS_AUTOMAP`): `SRC_TRANS_NONE` (pod IP), `SRC_TRANS_SNATPOOL` (uses F5SPKSnatpool), `SRC_TRANS_AUTOMAP` (uses the external F5SPKVlan self-IP).
  - `egressSnatpool` (string) — references `F5SPKSnatpool.spec.name` (the inner `name`, NOT metadata.name) when snatType=SNATPOOL.
  - `pseudoCNIConfig.namespaces[]` — app namespaces whose outbound traffic this egress applies to (one ns per CR).
  - `pseudoCNIConfig.appPodInterface` (e.g. `eth0`), `pseudoCNIConfig.appNodeInterface` (worker-node iface with TMM connectivity to the internal network).
  - `vxlan.{create,tmmInterfaceName,mtu,port,key,ipv4Subnet,ipv4PrefixLen}` — alternative to appNodeInterface (VXLAN tunnel to the internal F5SPKVlan).
  - Caveat: "Egress CR Modification is not supported" — delete + reapply to change.
- **F5SPKSnatpool** — `k8s.f5net.com/v1`, plural `f5-spk-snatpools`. `name`, `addressList` (LIST-OF-LISTS: outer element per TMM replica), `sharedSnatAddressEnabled` (one address shared across TMM pods).
- **F5SPKStaticRoute** — `k8s.f5net.com/v1`, plural `f5spkstaticroutes`. `destination`/`prefixLen`/`gateway`/`interface`/`type`(default gateway)/`dynamic`; v6 variants are snake_case (`destination_v6` etc.). NOT documented as an egress prerequisite. (aws-syd-test has one live: `return-to-jumphost-subnet` for return-path routing.)

### Documented data path
pod → internal VLAN/interface → TMM (pod's namespace matched by F5SPKEgress `pseudoCNIConfig`) → source-translate (AUTOMAP→external self-IP, or SNATPOOL→pool addr) → out the external VLAN. Routing the pod INTO TMM is via `pseudoCNIConfig` (appNodeInterface) or a VXLAN tunnel — NOT a NAD or pod default-route annotation, per the docs.

### Documented verification
clouddocs method: `kubectl exec` into an app pod, `curl` an external echo server, and observe the SOURCE IP on the destination side (should be the SNAT/self-IP, not the pod IP). clouddocs uses a self-hosted echo server and prints no expected-IP string.

## Gaps / why this is a spike not a build
1. **No AWS/EKS guidance in clouddocs** — the egress docs are on-prem/DPU. Everything below is INFERRED (from classic BIG-IP VE on EKS), to be validated live:
   - **EC2 source/dest-check** must be DISABLED on the ENI that carries SNAT'd egress (otherwise the VPC drops packets whose src isn't the ENI's primary IP). Our host-device phase assigns secondary IPs but does it disable src/dest-check? UNKNOWN — must check.
   - **SNAT addresses must be VPC-routable** secondary IPs on the external-subnet ENI. AUTOMAP uses the external self-IP (10.0.10.240, a phase-17 secondary IP) — plausibly already routable; a SNATPOOL would need additional secondary IPs assigned + routable.
   - **Security group + route table** on the external subnet must permit the outbound flow to the internet (the external subnet is private with a NAT GW in our topology — egress to internet goes NAT-GW; does TMM-SNAT'd traffic traverse it correctly?).
2. **Pod→TMM routing on host-device** — `pseudoCNIConfig.appNodeInterface` assumes a worker-node interface with internal-VLAN connectivity to TMM. On our EKS host-device pattern the internal VLAN is 10.0.20.0/24 (TMM int self-IP 10.0.20.240); how an app pod's eth0 traffic reaches that is unproven. VXLAN mode (`vxlan.create`) may be required. UNKNOWN.
3. **Return path** — egress replies must route back to TMM then the pod; aws-syd-test uses an F5SPKStaticRoute for return-to-subnet. A default/return route out the external interface may be needed (not documented as required).

## Proposed scenario design (for after the live spike)
`egress-snat` (start Amber, promote to Green once the data path is proven):
- Manifests: a test namespace + a curl pod (e.g. `curlimages/curl` sleep) on `eth0`; an `F5SPKEgress` (`k8s.f5net.com/v3`, `snatType: SRC_TRANS_AUTOMAP`, `pseudoCNIConfig.namespaces: [<test-ns>]`, appPodInterface `eth0`, appNodeInterface/vxlan TBD by the live spike). AUTOMAP avoids needing a separate routable SNAT pool — it reuses the external self-IP (10.0.10.240).
- Verify (control-plane, Amber): F5SPKEgress applies + reconciles (no apply error; a Ready/Programmed condition if the CR has one); F5SPKVlan present.
- Verify (data-plane, Green once proven): `kubectl exec` the curl pod → `curl https://httpbin.org/ip` (or an in-VPC echo) → assert the returned `origin` == the external self-IP `10.0.10.240` (proving AUTOMAP SNAT through TMM), NOT the EKS NAT-GW IP. (Using a public reflector is our extension of the documented method.)
- Gotchas to encode: F5SPKEgress is **v3** (snatpool/vlan/staticroute are v1); `egressSnatpool` refs `spec.name`; `addressList` is list-of-lists.

## Recommended next step
A short LIVE spike on the next cold cluster: apply a minimal `F5SPKEgress` (AUTOMAP, one test ns + curl pod), check whether (a) it reconciles, (b) the pod's traffic actually transits TMM, (c) the egress reaches the internet, and (d) what the observed source IP is. Resolve the source/dest-check + pod→TMM-routing unknowns there, THEN build the scenario Green. Until then, egress is NOT in the scenario suite (no guessed/likely-broken scenario committed — per the no-deferred-fixes / don't-ship-unrunnable-code principle).

## Live spike outcome (cycle-4, 2026-05-25 on syd-tracer, BNK 2.3.0)

Ran the live recon on a real cluster. The three AWS unknowns are now resolved, and the hard pod→TMM unknown is characterised:

- **F5SPKEgress CRD present**, plural `f5-spk-egresses.k8s.f5net.com`, versions **v1/v2/v3**. v3 spec matches the doc: `snatType` (enum `SRC_TRANS_NONE|SRC_TRANS_SNATPOOL|SRC_TRANS_AUTOMAP`, default AUTOMAP), `pseudoCNIConfig{appNodeInterface,appPodInterface,namespaces,vxlan,secureSPKIngressPort}`, `vlans{vlanList,disableListedVlans,category}`, `egressSnatpool`, firewall/dns/nat64 fields. F5SPKVlans live: `f5-cne-system/ext-vlan` (selfip 10.0.10.240) + `int-vlan` (selfip 10.0.20.240, interfaces `['1.2']`).
- **UNKNOWN #1 (EC2 source/dest-check) — RESOLVED ✅**: `SourceDestCheck=false` is ALREADY set on BOTH TMM ENIs (external eni-…b97 / internal eni-…f8a) by the host-device phase. SNAT'd egress won't be VPC-dropped.
- **SNAT-IP routability — RESOLVED ✅**: AUTOMAP's external self-IP `10.0.10.240` is a secondary IP physically on the external ENI (alongside scenario VIPs .100–.107), so VPC-routable.
- **UNKNOWN #3 (internet path) — RESOLVED ❌ (negative)**: the external TMM subnet (`subnet-03abd5a6…`) uses the VPC **main** route table, which has ONLY `10.0.0.0/16→local` — **no NAT/IGW route**. So egress out the external self-IP can reach **in-VPC targets only**, NOT the internet. A `curl httpbin.org/ip` test is impossible on this topology; verification must use an in-VPC reflector (e.g. the jumphost).
- **UNKNOWN #2 (pod→TMM routing) — CHARACTERISED, and it's the blocker**: the Multus `internal-netdevice` NAD is `type: host-device, device: ens7` — but **ens7 is consumed by TMM** (it IS TMM's internal-VLAN interface). host-device can't share a NIC, and once ens7 is moved into the TMM pod the worker node has no internal-VLAN interface for `pseudoCNIConfig.appNodeInterface`. So an app pod canNOT join 10.0.20.0/24 the simple way; egress for app pods would require **`pseudoCNIConfig.vxlan` mode** (a VXLAN tunnel from the worker into TMM's internal VLAN). That is an unproven, multi-step build — NOT a quick apply-and-curl test.

**Verdict (updated):** Egress is *feasible in principle* for **in-VPC** targets on this host-device EKS topology (src/dest-check off, self-IP routable), but the app-pod→TMM data path needs the VXLAN pseudo-CNI mode, which is a focused build + debug effort, not a cycle-tail test. No internet egress without adding a NAT/IGW route to the external subnet. **No egress scenario built or committed** (consistent with don't-ship-guessed-code). Recommended follow-up = a dedicated task: build `pseudoCNIConfig.vxlan` egress + an in-VPC reflector test asserting source IP == 10.0.10.240, on its own cluster cycle.

## Sources
- F5SPKEgress CRD: clouddocs.f5.com/bigip-next-for-kubernetes/latest/custom-resource-definitions/spk-egress-crd.html
- F5SPKSnatpool: .../spk-snatpool-crd.html · F5SPKStaticRoute: .../spk-static-route-crd.html · F5SPKVLAN: .../spk-vlan-crd.html
- Deploy + test traffic (curl method): .../deploy-app-test-trafic.html
- Legacy SPK egress data-path narrative (do NOT copy field names): clouddocs.f5.com/service-proxy-use-cases/main/egress_snat.html
