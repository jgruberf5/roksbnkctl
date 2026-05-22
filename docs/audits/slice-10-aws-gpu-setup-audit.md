# Slice-10 — aws-gpu-setup audit

**Author:** Lead (post-slice-9 live retest)
**Trigger:** Slice-9's AL2023 fix unblocked Multus and let License reach Active, but the host-device data-plane (TMM trunks ⇄ SelfIPs ⇄ Gateway VIPs) was still incomplete. Six concrete gaps were discovered LIVE by manually patching the running cluster — codified here per `[[feedback-systematic-aws-gpu-setup-audit]]`.

---

## 1 · Scope

Audit the slice of `aws-gpu-setup` that completes the **host-device TMM data plane** between EKS-Active and CNEInstance.Available:

1. The IRSA inline policy actions cne-controller uses to program VPC routes when wiring the Gateway VIP path.
2. The NAD `cniVersion`/`name` shape that Multus requires for host-device interfaces.
3. The default node count required for dSSM quorum + TMM headroom.
4. The TMM SelfIP secondary-IP assignment on each ENI (F5 Multi-AZ PDF p.9 requirement).
5. The F5SPKVlan CR that announces those SelfIPs inside the TMM pod netns.
6. The GatewayClass that registers the BNK cne-controller as a Gateway API implementation.

Pre-conditions assumed from earlier slices: AL2023 AMI (slice-9), hugepages capacity wait (slice-8), forge upsert (slice-8), EBS CSI addon (slice-8).

---

## 2 · Two-column line-by-line mapping

### 2.1 IRSA policy (aws-gpu-setup `up.sh` Phase 8 → our Phase 18)

| aws-gpu-setup | Our equivalent | Status |
|---|---|---|
| `up.sh:CneControllerVpcRead` policy action list: `ec2:DescribeVpcs`, `ec2:DescribeSubnets`, `ec2:DescribeRouteTables`, `ec2:DescribeNetworkInterfaces`, `ec2:DescribeInstances`, `ec2:DescribeAvailabilityZones`, **`ec2:CreateRoute`**, **`ec2:DeleteRoute`**, **`ec2:ReplaceRoute`**, `ec2:ModifyNetworkInterfaceAttribute`, `ec2:AssignPrivateIpAddresses`, `ec2:UnassignPrivateIpAddresses` | `phase18_irsa_oidc.go:cneControllerVpcReadPolicy` had everything EXCEPT `CreateRoute / DeleteRoute / ReplaceRoute` | **✗ GAP-1 (this slice)** — without route-mutate actions the cne-controller logs "Cloud prerequisites not met" each reconcile when Phase23b's GatewayClass triggers VIP route programming. |

### 2.2 NetworkAttachmentDefinitions (aws-gpu-setup `manifests/network-attachment-defs.yaml` → our `host-device/network-attachment-defs.yaml.tmpl`)

| aws-gpu-setup | Our equivalent | Status |
|---|---|---|
| `config: |- { "cniVersion": "0.3.1", "name": "external-network", "type": "host-device", "device": "ens8" }` | Our template was missing the `"name"` field | **✗ GAP-2 (this slice)** — Multus uses `name` as the network-identity key; absence causes some Multus versions to misroute or refuse to wire the NAD. Matches PDF p.21 example. |

### 2.3 Node count (aws-gpu-setup `vars.env:110` → our cluster.yaml defaults)

| aws-gpu-setup | Our equivalent | Status |
|---|---|---|
| `vars.env:110` `BNK_WORKER_COUNT="3"  # ≥3 for dSSM quorum (§9 F9)` | `intent.applyDefaults` set `DesiredSize=1` regardless of pattern | **✗ GAP-3 (this slice)** — single-node packs the BNK pod set onto one worker; f5-tmm (7-container ≈ 7.6 vCPU) has nowhere to land + dssm-db/sentinel only reach 2/3 (no quorum). Bumping default to 3 for host-device pattern matches aws-gpu-setup exactly. |

### 2.4 SelfIP secondary-IP assignment (aws-gpu-setup `up.sh:assign_selfip` → our Phase 17)

| aws-gpu-setup | Our equivalent | Status |
|---|---|---|
| `up.sh:assign_selfip` calls `aws ec2 assign-private-ip-addresses --network-interface-id <eni> --private-ip-addresses <selfip> --allow-reassignment` after each ENI attach. Comment cites F5 Multi-AZ PDF p.9: *"whatever listener address is created in the TMM must also be added to the corresponding instance interface that will use it."* | Phase 17 attached the ENIs but never assigned the secondary SelfIPs | **✗ GAP-4 (this slice)** — without secondary IPs on the ENIs, F5SPKVlan SelfIP plumbing silently fails (cne-controller's `CNFSecondaryIPsTMMPodIPMap` stays empty). Now wired in Phase 17 after attach. SelfIP values derived from `cl.Network.DataPath.{External,Internal}.CIDR` as `<subnet>.240` (matches `vars.env: TMM_EXT_SELFIP=10.0.10.240, TMM_INT_SELFIP=10.0.20.240`). |

### 2.5 F5SPKVlan CR (aws-gpu-setup `manifests/f5spkvlan.yaml` → our new `host-device/f5spkvlan.yaml.tmpl`)

| aws-gpu-setup | Our equivalent | Status |
|---|---|---|
| `manifests/f5spkvlan.yaml` declares `ext-vlan` (trunk 1.1, selfip_v4s=[TMM_EXT_SELFIP], prefixlen_v4=24) + `int-vlan` (trunk 1.2, internal=true, selfip_v4s=[TMM_INT_SELFIP]). Applied by `deploy-bnk.sh [13/13]` after waiting for `f5-spk-vlans.k8s.f5net.com` CRD. | Not present in our codebase before slice-10. | **✗ GAP-5 (this slice)** — without these CRs, the AWS-side SelfIPs from GAP-4 are not announced inside the TMM pod netns. New `Phase23bSPKVlanGatewayClass` waits for the CRD (installed by FLO after CNEInstance reconciles, up to 10 min) then applies both. |

### 2.6 GatewayClass (aws-gpu-setup `manifests/gatewayclass.yaml` → our new `host-device/gatewayclass.yaml.tmpl`)

| aws-gpu-setup | Our equivalent | Status |
|---|---|---|
| `manifests/gatewayclass.yaml` declares `<GWC>` with `controllerName: f5.com/<INSTANCE_NS>-f5-cne-controller`. Applied by `deploy-bnk.sh [12/13]`. | Not present in our codebase before slice-10. | **✗ GAP-6 (this slice)** — operator-facing Gateway CRs need a `gatewayClassName` to target; without this CR, the cne-controller doesn't register as a Gateway API implementation and downstream test traffic can't reach TMM. New phase applies the cluster-scoped CR alongside F5SPKVlan. |

---

## 3 · Out-of-scope for this slice — captured for future slices

| Item | Where | Notes |
|---|---|---|
| TMM SIGSEGV in mapres on AL2023 | aws-gpu-setup hit similar; documented as "lucky rebuild" in `bnk-tmm-recovery-runbook.md` | Not actionable from awsbnkctl side; needs F5 vendor engagement or BNK version pin. Defer to dedicated slice. |
| forge `credential_template_id` for AWS-SSO cluster access | `models/system.py:CloudCredentialTemplate` | Needed so forge can `kubectl` the EKS cluster after registration. Side-quest tracked in task #12. |
| test-gateway.yaml + test-nginx.yaml (actual traffic test) | aws-gpu-setup `manifests/` | Needed for `awsbnkctl test` to exercise data-plane traffic end-to-end. Deferred to slice-11+ once TMM Ready is reliable. |
| Phase 14 SelfIP assignment timing (aws-gpu-setup does it in the same phase as ENI create+attach) | Phase 17 keeps the same single-phase pattern | Behavioural match — no further work. |

---

## 4 · Acceptance for slice-10 PR

- [x] Audit doc captures GAP-1..6 with line-level citations.
- [x] Phase 18 IRSA policy adds `ec2:CreateRoute`, `ec2:DeleteRoute`, `ec2:ReplaceRoute`.
- [x] NAD template adds `"name": "external-network"` / `"name": "internal-network"`.
- [x] `intent.applyDefaults` bumps `DesiredSize` and `MinSize` from 1→3 for `pattern: host-device` (preserves explicit operator overrides).
- [x] `DataPathSpec.SelfIPs` field added; auto-derived to `<subnet>.240` via `intent.DeriveSelfIP` when not explicitly set.
- [x] Phase 17 calls `ec2:AssignPrivateIpAddresses` for each SelfIP after the ENI attach; persists `TMM_EXT_SELFIP`, `TMM_INT_SELFIP`, `TMM_SELFIP_PREFIXLEN` state keys.
- [x] New `host-device/f5spkvlan.yaml.tmpl` + `host-device/gatewayclass.yaml.tmpl` manifests.
- [x] New `Phase23bSPKVlanGatewayClass` (up + down) wired into `runPhasedUp` between Phase 23 (License) and Phase 24 (CWC), and into `runPhasedDown` in reverse.
- [x] Render helpers `RenderF5SPKVlan` + `RenderGatewayClass` added.
- [x] `EC2API` interface gains `AssignPrivateIpAddresses`; mock updated.
- [x] CI green.
- [ ] Live retest (deferred — see §3 "TMM SIGSEGV" — this slice ships infra-side completeness; TMM-Ready is a downstream BNK-vendor issue).

---

## 5 · Live findings from the slice-9 retest (to resolve next week)

The slice-9 live retest reached **License Active** + 9/10 BNK subsystems
Available, but TMM never reached Ready. Recorded here verbatim so the next
investigation has the full evidence trail.

### 5.1 What worked

- **AL2023 fix unblocked Multus**: secondary ENIs named `ens7` / `ens8`,
  Multus attached `net1` / `net2` cleanly into the TMM pod netns. No more
  `Link not found` errors.
- **Phase 11b hugepages-capacity wait fired and cleared** as designed
  (~25s for kubelet to re-advertise `hugepages-2Mi=4Gi`).
- **License CR reached `status.state=Active`** on the first reconcile after
  the `licenses.k8s.f5net.com` CRD landed.
- **Forge upsert path** (PR #21) handled the existing orphan project
  (`awsbnkctl-syd-tracer` id=35) — reused project + PUT refreshed kubeconfig
  on the existing cluster record.

### 5.2 What did NOT work — TMM SIGSEGV cycle

`f5-tmm-pod-manager` (a sub-component of the f5-cne-controller pod) deleted
the f5-tmm pod every ~2 min. Each new pod reached 5/7 containers Ready,
plateaued, then the pod-manager force-recreated it. Inside each pod the
`f5-tmm` container received SIGTERM at ~1.1s after start:

```
<set_use_phys_mem> WARNING: Generic sock driver enabled. Disabling physical memory restriction...
<is_ht_environment> INFO: Detected HT Environment
"m"="core file generated by the host; transitioning to idle mode" "dest"="/var/crash"
"m"="grpc server is starting up" "address"="0.0.0.0:19893"
"m"="shutdown triggered"
Terminated
Shell received SIGTERM, waiting for children to exit...
```

The host had **13+ TMM SIGSEGV core dumps** in `/var/lib/systemd/coredump`
(every one `tmm64.no_pgo` SIGSEGV, ~16 MB). The crashagent reads the
`/shared/core` hostPath mount, sees the prior crash, and transitions the
container to "idle mode" → kubelet probe fails → restart → another mapres
SIGSEGV → another core file → loop.

### 5.3 What we ruled out during live debug

- **Memory**: TMM container has 16 GiB limit; no `OOMKilled` events.
- **`TMM_GENERIC_SOCKET_DRIVER` env**: removed per F5 troubleshooting hint
  — TMM still segfaulted in mapres at the same point.
- **FLO hot reconcile loop**: scaled `f5-lifecycle-operator` to 0; the
  pod-manager kept recreating TMM and TMM kept SIGSEGV-ing within the
  startup-probe window.
- **CNEInstance missing fields**: live-patched `advanced.envDiscovery.enabled=false`,
  `pseudoCNI.enabled=true`, `telemetry.{logging,metric}Subsystem.enabled=true`
  (FLO accepted; no change in TMM behavior).
- **F5SPKVlan CRs**: live-applied ext-vlan + int-vlan + GatewayClass;
  cne-controller validation-webhook accepted them but TMM still SIGSEGV-ed
  before the cne-controller could push the SelfIP plumbing into TMM.
- **IRSA permissions**: live-added `ec2:CreateRoute / DeleteRoute /
  ReplaceRoute`; cne-controller restarted; no change in TMM behavior.
- **dSSM quorum**: scaled nodegroup 1 → 3 → 4 nodes; dssm pods redistributed
  (dssm-db / dssm-sentinel reached 3/3 ready on the larger nodegroup). TMM
  still SIGSEGV-ed.

### 5.4 What was NEVER verified during live debug (next-week TODO)

These three would be the highest-value experiments because each one targets
a distinct theory of the segfault root cause. Listed in priority order:

1. **PCI BDF mapping for AL2023 device-index 2 / 3 ≠ `0000:00:0{7,8}.0`.**
   Our `PCIDEVICE_INTEL_COM_ENS{7,8}` env vars assume Nitro hypervisor maps
   `device-index N → 00:0N.0`. Classifier blocked the privileged debug pod
   that would have read `/sys/class/net/ens7/device` symlink directly. If
   the actual BDF differs (e.g. PCI hot-plug ordering shuffles the bus
   addresses), mapres reads the wrong BDF → calls into garbage memory →
   SIGSEGV. Quick verify next week:
   ```bash
   kubectl debug node/<tmm-node> --image=busybox --profile=sysadmin -- chroot /host sh -c \
     'for i in $(ls /sys/class/net/ | grep "^ens"); do
        bdf=$(readlink /sys/class/net/$i/device | sed "s|.*/||")
        echo "$i -> $bdf"
      done'
   ```
   Then compare against our state.env `EXTERNAL_PCI` / `INTERNAL_PCI`. If
   they mismatch, Phase 17 needs to read the BDF post-attach via SDK rather
   than hardcoding `0000:00:0{7,8}.0`.

2. **cgroup v2 vs v1.** AL2023 uses **cgroup v2** by default; AL2 uses v1.
   BNK TMM image (built for an older base) may expect v1 (e.g. for hugepages
   accounting or CPU pinning via cpuset cgroups). Test by adding kernel
   boot arg `systemd.unified_cgroup_hierarchy=0` to the launch-template
   userdata → forces cgroup v1 on AL2023 nodes. If TMM stops segfaulting,
   we have the root cause + a permanent fix.

3. **Clean `/shared/core` + first-boot run.** The crashagent's "idle mode"
   transition is fed by prior crash residue. On a TRULY fresh cluster (no
   stale `/shared/core` on the host), does TMM segfault on its FIRST mapres
   run, or only after the first crash? If first-run is clean, the cycle is
   self-reinforcing; if first-run still segfaults, the cores are an effect
   not a cause. Test by tearing down + re-upping with this PR's defaults
   (3-node, AL2023, all infra fixes) — observe the FIRST TMM container's
   startup before any restarts.

### 5.5 Hand-off summary for next-week investigator

Start by reading `aws-gpu-setup/bnk-tmm-blocker.md` + `bnk-tmm-recovery-runbook.md`
in full — those documents capture aws-gpu-setup's struggle with the same
class of issue. Their "lucky rebuild" resolution suggests intermittency;
the PCI-BDF and cgroup-version theories above are the structural
hypotheses worth testing first.

If §5.4 experiments don't resolve it, escalate to F5 support with:
- Full TMM container log (the `crashagent` + `mapres` + `shutdown triggered`
  sequence at the 1.1s mark).
- A `coredumpctl info <pid>` from one of the SIGSEGV cores.
- BNK manifest version (`2.3.0-3.2598.3-0.0.170`) + node OS
  (`Amazon Linux 2023.11.20260509`, kernel `6.1.170-210.320.amzn2023.x86_64`).
- aws-gpu-setup as a reference implementation that exhibits the same
  intermittency on the same OS / version combo.
