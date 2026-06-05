# Spike: SR-IOV / vfio-pci dataplane for `pattern: sriov-external` on EKS

**Status:** desk research done (negative-leaning) — **live testpmd gate not yet run.**
`pattern: sriov-external` is blocked at validation until this spike returns a
clean result.

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
