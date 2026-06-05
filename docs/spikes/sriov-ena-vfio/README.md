# Spike: SR-IOV / vfio-pci dataplane for `pattern: sriov-external` on EKS

**Status:** **LIVE GATE RUN 2026-06-05 → GREEN (kernel/DPDK layer).** The
foundational feasibility is proven on a real awsbnkctl node. `pattern:
sriov-external` stays blocked at validation pending the one remaining unknown:
**F5 TMM's own support for an ENA-vfio dataplane** (see "Remaining risk" below).

## Live spike results (2026-06-05)

Run on a real awsbnkctl-provisioned EKS node (external-only cluster), via a
privileged host-namespace probe pod modeled on phase17c's iface-discovery pod —
binding the actual data-path ENI (`ens8`, the one awsbnkctl tags
`no_manage=true`) to vfio-pci after releasing it from the f5-tmm DaemonSet.

- Node: m6i.4xlarge, **Amazon Linux 2023**, kernel **6.1.172-216.329.amzn2023**,
  EKS 1.30. Probe userspace: Fedora 40 container, **DPDK 23.11**.
- **No-IOMMU is stock**: `CONFIG_VFIO_NOIOMMU=y`; `modprobe vfio`/`vfio-pci`
  clean; `enable_unsafe_noiommu_mode` set to `Y` — **no custom AMI / rebuilt
  vfio-pci module required** (overturns the desk-research assumption).
- **ENA → vfio-pci bind: OK** (`drv=vfio-pci unused=ena`).
- **PMD init: OK** under vfio No-IOMMU (`ena_get_metrics_entries: 0x6 customer
  metrics supported`), port up at the ENI's real MAC.
- **TX: clean** — 22.8M packets, 1.2 GB, **0 TX-errors** (~3.8M pps), txonly.
- **RX: clean (the #278 kill criterion) — PASSED.** Flooded 7,332,000 UDP
  packets at the ENI IP; testpmd rxonly received **exactly 7,332,000**,
  **RX-missed 0, RX-errors 0, RX-nombuf 0**. No RX drop.

Gotchas found (encode into any real sriov-external implementation):
- The probe pod MUST request `hugepages-2Mi` (+ a memory limit) — without it the
  hugetlb cgroup limit is 0 and DPDK EAL OOMs (`rte_service_init() failed:
  Cannot allocate memory`) even though the host has free hugepages.
- Hugepages must be mounted in the pod (HugePages `emptyDir`), `ulimit -l
  unlimited`, `--iova-mode=pa` (No-IOMMU ⇒ physical addressing).
- Promiscuous mode is "not supported" on ENA-vfio (ignorable — VIP/SelfIP
  traffic is unicast to the ENI's own MAC, which RX'd fine).
- No write-combining patch was used; functional throughput was fine without it
  (WC/LLQ remains a *peak-perf* optimization, not a correctness requirement).
- No-IOMMU = no DMA isolation (security trade-off for production).

## TMM-on-vfio PROVEN (2026-06-05, live)

Pushed past the substrate to TMM itself, live on the same node. **F5 TMM runs
its DPDK dataplane on the vfio-bound ENA on AWS EKS** — the `tmm.0` process holds
`/dev/vfio/vfio`, `/dev/vfio/0` (IOMMU group) and an `anon_inode:[vfio-device]`
fd open (7/7 Running, 0 restarts). This overturns the "DPU/Robin-only" assumption.

The mechanics that made it work (and the dead-ends ruled out):

- **NAD `type: sriov` is WRONG for a whole ENA.** sriov-cni demands a VF parent
  (`/sys/bus/pci/devices/<bdf>/physfn/net`); a whole ENA bound to vfio has no
  `physfn` → `FailedCreatePodSandBox`. F5's own NAD (spk-demo-tool) uses
  `type: host-device`, not sriov-cni.
- **host-device delivery → mapres CRASHES.** With the ENA delivered as a kernel
  netdev (host-device CNI) and `TMM_GENERIC_SOCKET_DRIVER` dropped, mapres has no
  `/dev/vfio` (the TMM pod mounts only `/dev/net/tun`, not host `/dev`) and dies
  right after `<do_delay>` ("core config file does not exist").
- **device-plugin delivery → WORKS.** The sriov-network-device-plugin's
  `Allocate()` injects `/dev/vfio/<group>` + `/dev/vfio/vfio` + `PCIDEVICE_INTEL_COM_ENS8`
  into the TMM pod purely from the **resource request** (`intel.com/ens8: 1`),
  independent of any CNI. With `TMM_GENERIC_SOCKET_DRIVER` dropped, mapres
  DPDK-inits and `tmm.0` opens the vfio device.
- **Crucial subtlety:** the device-plugin allocation needs only the *resource
  request*, NOT a Multus network annotation. Dropping `k8s.v1.cni.cncf.io/networks`
  avoids the sriov-cni failure entirely while still delivering the device.

Per-interface recipe (encode into the real pattern):
1. Bind the external ENA to vfio-pci (No-IOMMU) on the node — `vfio-node-prep`.
2. sriov-network-device-plugin exposes it as `intel.com/ens8`
   (selectors: vendor `1d0f`, driver `vfio-pci`, the ENA's pciAddress). **Restart
   the plugin AFTER the bind** — it registers 0 devices if it scans first.
3. TMM pod must request `intel.com/ens8: 1` (gets `/dev/vfio` + `PCIDEVICE` injected),
   drop `TMM_GENERIC_SOCKET_DRIVER`, keep `ROBIN_VFIO_RESOURCE_1: ens8`, and have
   NO `k8s.v1.cni.cncf.io/networks` annotation (no sriov-cni).

## FULL END-TO-END PROOF (2026-06-05) — operator-managed, traffic flows

The `passthru` CNI closed the last gap. **`type: passthru`** (a built-in no-op
CNI, present in `/opt/cni/bin` on EKS) satisfies Multus for the NAD without
running sriov-cni — so the **cne-controller manages the whole thing**: from
`networkAttachments: [external-sriov]` it adds the `intel.com/ens8` resource
request + the `passthru` network annotation; Multus runs passthru (success); the
device plugin injects `/dev/vfio` + `PCIDEVICE`; TMM DPDK-inits on the vfio ENA.

Live result, operator-managed (cne-controller running, no manual pod hacks):
- f5-tmm pod 7/7 Running, `tmm.0` holds `/dev/vfio/vfio` + IOMMU-group + vfio-device fds.
- **`http-routing-e2e` scenario: HTTP 200** — jumphost → curl → TMM(DPDK/vfio) →
  Gateway VIP 10.0.10.100 → HTTPRoute → nginx. TMM forwards L7 over the vfio ENA.

### The complete working recipe (what `sriov-external` must do)

1. **Node**: bind the external ENA to `vfio-pci` (No-IOMMU) — `vfio-node-prep`
   DaemonSet (stock AL2023, no custom AMI). nodeSelector `app=f5-tmm`.
2. **Device plugin**: `sriov-network-device-plugin` + a ConfigMap selecting the
   ENA (vendor `1d0f`, driver `vfio-pci`, the ENA's pciAddress) as resource
   `intel.com/ens8`. **Restart the plugin pod AFTER the vfio bind** (it registers
   0 devices if it scans the device while still on `ena`).
3. **NAD** `external-sriov`: `type: passthru` + annotation
   `k8s.v1.cni.cncf.io/resourceName: intel.com/ens8`. (NOT `type: sriov`.)
4. **CNEInstance**: `networkAttachments: [external-sriov]`; drop
   `TMM_GENERIC_SOCKET_DRIVER`; keep `ROBIN_VFIO_RESOURCE_1: ens8`; do NOT hardcode
   `PCIDEVICE_INTEL_COM_ENS8` (the device plugin injects it + `/dev/vfio`).
5. The cne-controller wires the rest (resource request + annotation + pod).

### Wired into awsbnkctl + validated clean (2026-06-05)

Steps 1–4 are now baked into the tool: `Phase20bSriovDataplane` (gated on
`DataplaneBinding()=="sriov"`) applies node-prep → device-plugin → passthru NAD
and waits for `intel.com/ens8`; phase 20 skips the host-device NAD for sriov; the
CNEInstance render drops `TMM_GENERIC_SOCKET_DRIVER` + points at the sriov NAD;
validation allows `sriov-external` (experimental warning).

**Validated end-to-end on a clean `awsbnkctl up --config examples/sriov-external/cluster.yaml`**
(cluster `bnk-sriov`, zero manual steps): phase 20b advertised `intel.com/ens8`,
the cne-controller brought TMM up 7/7 on the vfio resource (`tmm.0` holds the vfio
fds), and the `http-routing-e2e` scenario returned **HTTP 200** (jumphost → TMM
DPDK/vfio → Gateway VIP → HTTPRoute → nginx). The pattern is real and code-driven.

Kept as experimental (warning, not blocked) pending broader soak/multi-run
confidence; `external-only` (sock) remains the conservative single-interface
default. Flip the warning off in `validatePattern` once it has more mileage.

## Why this is gated

`sriov-external` would bind TMM's external ENI via **`vfio-pci` (DPDK userspace)**
instead of the kernel `host-device` CNI that `external-only` / `dual-interface`
use. Desk research found this is **not an F5-documented path on the EKS/Host
build** and is fragile:

- **No usable IOMMU in EC2 guests.** Non-`.metal` Nitro instances expose no
  vIOMMU, so `vfio-pci` must run in **No-IOMMU mode**
  (`enable_unsafe_noiommu_mode=1`) with a **custom-rebuilt write-combining
  `vfio-pci` module** (amzn-drivers `enav2-vfio-patch`) baked into a custom AMI —
  per-kernel maintenance forever.
- **Not what F5 documents for the Host build.** F5's only `sriov`/`vfio` NAD docs
  target **BNK-on-DPU (BlueField SFs)**, not AWS ENA. `ROBIN_VFIO_RESOURCE_*` are
  Robin-CNF-on-OpenShift (Intel E810 VFs). The Host build F5 documents is
  **Multus + host-device** — exactly what this tool ships today.
- **Field RX-drop reports.** ENA-vfio shows TX-ok / RX-drops in the wild
  (amzn-drivers issue #278). Whether TMM's ENA path even initializes against a
  `vfio` resource is unknown.
- The `sriov-network-device-plugin` *can* expose ENAs by
  `vendors:["1d0f"] + drivers:["vfio-pci"], deviceType: vfio` (no PF/VF needed) —
  that half is solved; the **dataplane half is the risk.**

→ Don't write the `sriov-external` dataplane on faith. Run the live gate first.

## The gate (≈1 instance-hour, no cluster needed)

Kill criterion: **bidirectional RX *and* TX** must increment under `testpmd`.

1. Launch one **non-`.metal` 6th-gen** EC2 (e.g. `c6i.large`, AL2023) in a
   throwaway VPC; attach a secondary ENI.
2. Hugepages on; `modprobe vfio-pci`; enable No-IOMMU
   (`enable_unsafe_noiommu_mode=1`); install the **write-combining-patched
   `vfio-pci`** via amzn-drivers `enav2-vfio-patch` / `get-vfio-with-wc.sh`.
3. Ensure the secondary ENI is detached from the kernel `ena` driver and away
   from the VPC CNI, then `dpdk-devbind.py --bind=vfio-pci <secondary-ENI-BDF>`.
4. Run `testpmd` in txrx mode; drive traffic; confirm **RX and TX counters both
   increment** (the #278 failure mode is RX=0).
5. Record results (instance type, kernel, driver SHA, counters) in this directory.

## Outcomes

- **Green (clean RX/TX):** open the follow-up — a phase to install
  `sriov-network-device-plugin` (DaemonSet, ENA selector `vendors:["1d0f"]` +
  `drivers:["vfio-pci"]`, `deviceType: vfio`), a `sriov-external` NAD template
  (`type: sriov`, `resourceName`), a custom-AMI build step for the WC `vfio-pci`
  module, flip the `validatePattern` gate in `internal/intent/cluster.go`, and
  escalate to F5 for TMM-on-Host `vfio` support confirmation.
- **Red (RX drops / TMM won't init):** keep `sriov-external` blocked, record why
  here, and `external-only` remains the single-interface answer.

## Where the gate lives in code

`internal/intent/cluster.go` → `validatePattern()` returns the "experimental,
blocked pending spike" error for `PatternSRIOVExternal`. Removing that branch is
the last step once the gate is green.
