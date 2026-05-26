> **⚠️ ARCHIVED (2026-05-26):** This document describes the pre-pivot Terraform-embedded design and is retained for history only. The project now uses the Go-SDK phased provisioner driven by cluster.yaml — see [README.md](../../README.md) and [docs/POST_TERRAFORM_DIRECTION.md](../POST_TERRAFORM_DIRECTION.md).

# PRD 07 — EKS cluster module + self-managed SR-IOV node group

The load-bearing design decision for `awsbnkctl`: how to stand up an EKS cluster whose worker nodes can host BNK's data plane. BNK requires SR-IOV; AWS's managed Kubernetes surface doesn't expose SR-IOV cleanly through EKS managed node groups. This PRD specifies the alternative.

> **Status:** draft — authored Sprint 0; refined Sprint 1 against the implementation. The operator-run spike (see [Spike status](#spike-status)) validates the design hypothesis against live AWS; spike findings fold into the [Resolved-in-spike](#resolved-in-spike-placeholder) section. Until the spike completes, treat this PRD as a hypothesis, not a contract; the `v0.2` release tag is gated on spike validation.

## Background

BNK 2.3 (and the 2.x line generally) is validated against Kubernetes clusters where worker nodes expose SR-IOV virtual functions (VFs) as schedulable resources. The CNEInstance reconciler binds a `F5SPKVlan` self-IP to a VF on the node it lands on; without VFs, the data-plane path doesn't come up.

In bare-metal deployments (`dpubnkctl` lineage), BNK runs on hosts with NVIDIA BlueField-3 DPUs and Mellanox NICs — Mellanox SR-IOV is BNK's reference data plane. On IBM Cloud (`roksbnkctl` lineage), ROKS exposes a SR-IOV-capable worker pool out of the box. **AWS EKS does not.**

The relevant AWS primitives:

- **Elastic Network Adapter (ENA)** — AWS's SR-IOV implementation. Supported on most modern instance families; surfaces as a VF interface per ENI. ENA is **not** identical to Mellanox SR-IOV — same kernel SR-IOV plumbing, different driver lineage.
- **Elastic Fabric Adapter (EFA)** — ENA's HPC-targeted sibling, with kernel-bypass libfabric semantics. Higher throughput, narrower instance-family support (`c5n.18xlarge`, `c5n.metal`, `p4d.*`, `hpc6a.*`). EFA is what AWS markets to network-intensive workloads.
- **ENIs (Elastic Network Interfaces)** — the L2 attachment point. Multiple ENIs per instance for instances with sufficient capacity; ENIs map to subnets, not VLANs.

The open question, addressed by the Sprint 1 spike: **does BNK accept an ENA VF as SR-IOV, or does it require Mellanox-specific behaviour the ENA driver doesn't reproduce?**

## Options considered

| Option | Verdict | Why |
|---|---|---|
| **EKS managed node groups + Multus + SR-IOV CNI** | rejected | Managed node groups don't expose the SR-IOV device plugin's required kernel paths or driver tuning without custom AMIs, and custom AMIs negate most of the "managed" value. The seam between AWS-managed AMI updates and our SR-IOV stack is fragile. |
| **EKS self-managed node groups + Multus + SR-IOV CNI** | **selected** | We own the launch template + AMI, can pin to AL2023 (or AL2) with the SR-IOV driver versions BNK expects, can apply kernel parameters at boot via user-data, and can co-install the SR-IOV device plugin DaemonSet at cluster ready. Trade-off: AMI lifecycle is now our problem, not AWS's. |
| **EKS + Fargate** | rejected | Fargate doesn't expose SR-IOV at all (or any low-level network knobs); it's a managed-pod offering, not a managed-node one. |
| **EC2 + kubeadm (no EKS)** | rejected | We lose IRSA, EKS API auth, the EKS console surface, and the standard upgrade path. The complexity of running our own control plane outweighs the SR-IOV freedom. roksbnkctl explicitly uses managed K8s for the same reason. |
| **EKS Auto Mode** | rejected | Auto Mode is Karpenter-driven and doesn't currently integrate with SR-IOV device plugins. Revisit in v1.x. |
| **EKS on Outposts / Wavelength / Local Zones** | out of scope | Niche; the underlying instance + ENI model is identical to standard EKS, so the work here is a strict prerequisite. |

## Decision

awsbnkctl v1.0 ships an EKS cluster + **self-managed node group** with:

- **Instance types:** `c5n.4xlarge` default, `c5n.9xlarge` / `m5n.4xlarge` / `m5dn.4xlarge` as alternatives. ENA-SR-IOV enabled; EFA off (not needed for the BNK control + data-plane path, and EFA narrows the instance family). v1.x evaluates EFA for the high-throughput tier.
- **AMI:** EKS-optimised AL2023 for the cluster's Kubernetes version. Pinned per-release via the launch template's `image_id`; the upgrade path is documented in the user-facing book chapter 33.
- **Launch template:** ENA enabled (`enable_ena_support = true`), instance type from the input list, user-data that applies kernel parameters (`intel_iommu=on iommu=pt`) at first boot and signals readiness via `aws-cli` to the EKS control plane.
- **Subnets:** dual-stack (IPv4 + IPv6 optional), at least 3 AZs for HA, private subnets only for workers, public subnets for NAT egress.
- **CNI stack:** AWS VPC CNI as the primary CNI (for pod-to-pod traffic via Pod ENI), Multus on top (chained CNI), SR-IOV CNI for VF allocation, SR-IOV device plugin as the discovery + scheduler-advertisement piece.
- **Cluster auth:** EKS API authentication mode (not aws-auth ConfigMap — the modern surface). awsbnkctl's IAM identity gets `system:masters` by default; production customers override.

## Architecture (rendered)

```
┌───────────────────────────────────────────────────────────────────┐
│ VPC (10.0.0.0/16, 3 AZs)                                          │
│                                                                   │
│   ┌─────────────────────────────┐                                 │
│   │ Public subnets (NAT, ALB)   │                                 │
│   └─────────────────────────────┘                                 │
│                                                                   │
│   ┌─────────────────────────────┐                                 │
│   │ Private subnets             │                                 │
│   │                             │                                 │
│   │   ┌──────────────────────┐  │                                 │
│   │   │ EKS managed control  │  │ ← AWS-managed                   │
│   │   │ plane (HA)           │  │                                 │
│   │   └──────────────────────┘  │                                 │
│   │                             │                                 │
│   │   ┌──────────────────────┐  │                                 │
│   │   │ Self-managed node    │  │ ← we own the launch template    │
│   │   │ group (c5n.4xlarge   │  │   + AMI lifecycle               │
│   │   │  × 3-10 nodes)       │  │                                 │
│   │   │                      │  │                                 │
│   │   │   ENA-SR-IOV ENIs    │  │ ← VFs advertised by SR-IOV      │
│   │   │   (primary + 1-N     │  │   device plugin                 │
│   │   │    secondary ENIs)   │  │                                 │
│   │   └──────────────────────┘  │                                 │
│   └─────────────────────────────┘                                 │
└───────────────────────────────────────────────────────────────────┘

On each worker node:

  ┌──────────────────────────────────────────────────────────┐
  │ kubelet                                                  │
  │   ↑                                                      │
  │ container runtime (containerd)                           │
  │   ↑                                                      │
  │ CNI chain: AWS VPC CNI → Multus                          │
  │                            ↓                             │
  │                         SR-IOV CNI (per-VF allocation)   │
  │   ↑                                                      │
  │ SR-IOV device plugin DaemonSet                           │
  │   (advertises intel.com/sriov as a schedulable resource) │
  └──────────────────────────────────────────────────────────┘
```

## Implementation outline

### Terraform module: `terraform/modules/eks_cluster/`

Wraps `terraform-aws-modules/eks/aws ~> 20.x`. Composition rather than fork — we layer launch-template + SR-IOV manifest deployment on top of the upstream module.

**Inputs:**

| Variable | Type | Default | Description |
|---|---|---|---|
| `region` | string | _required_ | AWS region |
| `cluster_name` | string | _required_ | EKS cluster name |
| `cluster_version` | string | `"1.30"` | Kubernetes version |
| `vpc_id` | string | _required_ | VPC ID (set `create_vpc = true` to provision) |
| `subnet_ids` | list(string) | _required_ | Private subnet IDs, ≥3 AZs |
| `node_instance_types` | list(string) | `["c5n.4xlarge"]` | Self-managed node group instance types |
| `node_min_size` | number | `2` | Node group minimum |
| `node_max_size` | number | `10` | Node group maximum |
| `node_desired_size` | number | `3` | Node group initial size |
| `enable_multus` | bool | `true` | Install Multus DaemonSet |
| `enable_sriov` | bool | `true` | Install SR-IOV CNI + device plugin |
| `sriov_resource_name` | string | `"intel.com/sriov"` | Device plugin resource advertisement key |

**Outputs:**

| Output | Description |
|---|---|
| `cluster_name` | EKS cluster name (echoed) |
| `cluster_endpoint` | EKS API endpoint URL |
| `cluster_ca_data` | EKS CA cert (base64) |
| `cluster_oidc_issuer_url` | OIDC issuer URL for IRSA (Sprint 2 input) |
| `oidc_provider_arn` | IAM OIDC provider ARN |
| `node_group_role_arn` | IAM role ARN for the node group |
| `cluster_ready_id` | Empty resource ID for downstream `depends_on` (carries through roksbnkctl convention) |

### Multus + SR-IOV stack

Deployed via the `kubernetes` provider as part of the module. Three DaemonSets:

1. **`multus-daemonset.yml`** — upstream `k8snetworkplumbingwg/multus-cni v4.x` thick plugin. Reads pod annotations (`k8s.v1.cni.cncf.io/networks`) and chains to additional CNIs after the primary VPC CNI completes.
2. **`sriov-cni-daemonset.yml`** — upstream `k8snetworkplumbingwg/sriov-cni`. Allocates a VF to a pod and configures the netns.
3. **`sriov-device-plugin-daemonset.yml`** — upstream `k8snetworkplumbingwg/sriov-network-device-plugin`. Discovers VFs from the host kernel (`/sys/class/net/<eth>/device/sriov_*`) and advertises them as `intel.com/sriov` (configurable).

A `ConfigMap/sriov-device-plugin-config` declares the VF pool by PCIe vendor/device ID. ENA VFs on `c5n.*` instances surface as Amazon Elastic Network Adapter — vendor `1d0f`, device `ec20` (or similar; **the spike confirms the exact IDs**).

### `internal/aws/`

| File | Surface |
|---|---|
| `client.go` | aws-sdk-go-v2 client constructor; resolves credentials via the standard chain |
| `sts.go` | Caller-identity for doctor; OIDC provider ARN derivation |
| `ec2.go` | Describe instance type availability per region; describe SR-IOV / ENA capability flags; quota lookup for the chosen instance family |
| `eks.go` | Describe-cluster (post-apply); kubeconfig generation (no shell-out to `aws eks update-kubeconfig`); cluster auth token via `sts:GetCallerIdentity` presigned URL (the same mechanism `aws-iam-authenticator` uses) |
| `vpc.go` | Optional VPC discovery / validation |

### CLI surface

| Verb | Behaviour |
|---|---|
| `awsbnkctl up cluster` | Drives `terraform/modules/eks_cluster/` only. Stops before BNK deployment (Sprint 3 closes the full lifecycle). |
| `awsbnkctl down cluster` | Reverse: destroys the cluster module. |
| `awsbnkctl k get nodes -o wide` | Inherited; should show `c5n.4xlarge` nodes with `intel.com/sriov: <N>` in their allocatable resources. |
| `awsbnkctl doctor` | New AWS-aware checks (see PRD 04 inheritance edits). |

## Spike status

The spike protocol below is **operator-run, not agent-run**. The Sprint 1 agent dispatch (architect, staff, validator, tech-writer) authors the Terraform module, the `internal/aws/` Go helpers, the `awsbnkctl up cluster` verb, and the doctor refresh against the *design hypothesis* — i.e. against the option matrix, decision, and architecture described above. None of those agents executes `terraform apply` against live AWS.

The spike runs separately, on a date the operator chooses, against a real AWS account. It costs roughly $5-15 for a few hours of EKS control plane plus two `c5n.4xlarge` instances. The operator follows the day-1/day-2/day-3 protocol below and writes findings into the [Resolved-in-spike](#resolved-in-spike-placeholder) placeholder at the bottom of this PRD.

The `v0.2` release tag is gated on spike completion, not on the Sprint 1 agent dispatch landing on `main`. If the spike surfaces a hypothesis mismatch (e.g. ENA VFs not accepted by BNK's CNEInstance reconciler — see [Spike fail modes](#spike-fail-modes) below), Sprint 1.5 or Sprint 2 rework addresses it before the tag cuts.

The Sprint 1 commit therefore lands the design-as-written, with this section as the explicit pointer to "what was deferred and how it folds back in".

## Spike protocol (operator-run; not part of the Sprint 1 agent dispatch)

Before tagging `v0.2`, validate end-to-end by hand. Output of the spike folds back into this PRD's [Resolved-in-spike](#resolved-in-spike-placeholder) section, replacing the placeholder.

### Day 1 — cluster + node group

1. Provision an EKS cluster (1.30) via `eksctl create cluster --without-nodegroup --version 1.30 --region us-east-1 ...`.
2. Create a self-managed node group via `eksctl create nodegroup --managed=false --node-type c5n.4xlarge --nodes 2 ...`.
3. Verify nodes report `Ready` and surface a primary ENA interface.

### Day 2 — SR-IOV stack

1. Install Multus via the upstream `thick-plugin` DaemonSet from `k8snetworkplumbingwg/multus-cni`.
2. Install SR-IOV CNI binaries via `k8snetworkplumbingwg/sriov-cni`.
3. Install SR-IOV device plugin via `k8snetworkplumbingwg/sriov-network-device-plugin`, configured to discover ENA VFs by vendor/device ID.
4. Verify `kubectl describe node <node>` shows `intel.com/sriov: <N>` in `Allocatable`.

### Day 3 — pod schedules onto a VF

1. Apply a `NetworkAttachmentDefinition` referencing the SR-IOV CNI.
2. Schedule a pod requesting `intel.com/sriov: 1` and annotated with `k8s.v1.cni.cncf.io/networks: <NAD-name>`.
3. `kubectl exec` into the pod; verify a second network interface exists; verify `ethtool -i <iface>` reports the ENA driver and SR-IOV is active.
4. **BNK-specific check:** verify `ip link show` from inside the pod surfaces the VF in a state BNK's CNEInstance reconciler would accept. This is the load-bearing check.

### Spike fail modes

- **VF not appearing:** instance family doesn't support SR-IOV in the way the device plugin expects. Try `c5n.9xlarge` (more VFs per instance) or `c6in.*` (newer ENA generation).
- **VF appears but BNK rejects it:** the most consequential failure mode. Indicates ENA VF semantics don't match BNK's reference (Mellanox). Mitigations in order of preference:
  1. Tune the SR-IOV device plugin config — exhaust device-plugin-side knobs first.
  2. Patch FLO's pre-flight check (if F5 collaborates) to accept ENA-SR-IOV VFs.
  3. Fall back to **the multi-ENI shape** (no SR-IOV; BNK runs on standard ENIs with reduced performance). Document the trade-off; v1.0 ships in this mode if the spike fails.
  4. Last resort: bare-metal EC2 instances (`*.metal` family) with whatever NIC AWS exposes at the bare-metal layer. AWS does not generally surface customer-installable PCIe NICs on standard EC2; if this fall-back is the only path, the project re-scopes to a narrower instance set (e.g. `c5n.metal`) and re-validates SR-IOV semantics from scratch.
- **Multus chaining breaks pod connectivity:** AWS VPC CNI + Multus chaining has known edge cases around pod IP allocation order. Tracked upstream; mitigation is to use `multus-cni`'s `clusterNetwork` field to make VPC CNI the explicit primary.

## Trade-offs accepted

- **User-managed AMI lifecycle.** With self-managed node groups, AMI updates aren't AWS-managed. awsbnkctl pins the AMI per release, rotates it in a minor version bump, and documents the manual rollover in the user book (Sprint 5 chapter).
- **No Karpenter / elastic scaling in v1.0.** Karpenter has no clean integration with SR-IOV device plugins as of writing. v1.0 ships fixed-size node groups; v1.x revisits.
- **Single instance family per cluster (effectively).** SR-IOV device plugin configuration is per-vendor/device-ID; mixing `c5n` and `m5n` is possible but doubles the config matrix. v1.0 docs recommend single-family clusters.
- **AWS-only.** This PRD doesn't extend to Azure or GCP. Azure has accelerated networking + SR-IOV via Mellanox NICs (closer to BNK's reference); GCP has gVNIC. Both deserve their own fork if pursued.

## Open questions (resolved in the spike)

- **Does ENA SR-IOV match BNK's reference closely enough that CNEInstance reconciles cleanly?** → spike day 3.
- **Which exact instance types maximise VF count per dollar?** → spike data, folded into the instance-type default list.
- **What's the right `intel_iommu` / `iommu=pt` kernel parameter posture on AL2023?** → spike day 1 user-data validation.
- **Does the AWS VPC CNI v1.18+ → Multus chaining have stable IP allocation semantics?** → spike day 2; if not stable, document workarounds.
- **Are there per-region instance availability gaps for `c5n` / `m5n`?** → doctor pre-flight check derived from EC2 `DescribeInstanceTypeOfferings`.

## Resolved-in-spike (placeholder)

**Operator-fillable.** When the spike completes, the operator replaces this placeholder with concrete findings. Expected structure:

- A short narrative paragraph: what was provisioned, what passed, what surprised.
- One paragraph per [Open question](#open-questions-resolved-in-the-spike) above, with the answer.
- Re-affirm or revise each entry in [Trade-offs accepted](#trade-offs-accepted) against what the spike actually saw.
- If a [Spike fail mode](#spike-fail-modes) triggered, document which mitigation was applied and whether v0.2 still ships in the original shape or in the fall-back shape.

Until this section is filled, [`docs/PLAN.md`](../PLAN.md) Sprint 1 close notes "spike deferred"; the `v0.2` tag is not cut.

## Cross-references

- [`PLAN.md` § Sprint 1](../PLAN.md) — calendar + deliverables.
- [`prd/08-S3-SUPPLY-CHAIN-IRSA.md`](./08-S3-SUPPLY-CHAIN-IRSA.md) — consumes `cluster_oidc_issuer_url` from this module's outputs.
- [`prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md`](./06-CLUSTER-TRIAL-PHASE-SPLIT.md) — defines the cluster-phase boundary this module fulfils.
- [Multus CNI upstream](https://github.com/k8snetworkplumbingwg/multus-cni) and [SR-IOV CNI](https://github.com/k8snetworkplumbingwg/sriov-cni) — the manifests this module deploys.
- [AWS EKS networking docs](https://docs.aws.amazon.com/eks/latest/userguide/pod-networking.html) — current canonical reference for VPC CNI + Multus combinations on EKS.