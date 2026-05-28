> **⚠️ ARCHIVED (2026-05-26):** This document describes the pre-pivot Terraform-embedded design and is retained for history only. The project now uses the Go-SDK phased provisioner driven by cluster.yaml — see [README.md](../../README.md) and [docs/POST_TERRAFORM_DIRECTION.md](../POST_TERRAFORM_DIRECTION.md).

# PRD 00 — overview: scope, inheritance, and the AWS retarget

Index document for `awsbnkctl`'s product requirements docs. This PRD frames the **scope of the AWS retarget**, lists which PRDs are inherited verbatim from `roksbnkctl` and which are net-new, and pins down the load-bearing design decisions.

## What awsbnkctl is

A single-binary Go CLI to deploy F5 BIG-IP Next for Kubernetes (BNK) onto AWS EKS, manage its supply chain in S3 (and optionally ECR), and validate the deployment with built-in DNS, connectivity, and throughput tests. Same 4-command lifecycle (`init` → `up` → `test` → `down`) as roksbnkctl, retargeted at AWS primitives.

## Why this is a fork, not a rewrite

Three of roksbnkctl's five Terraform modules — `cert_manager`, `flo`, `cne_instance`, `license` — are pure Kubernetes manifests parameterised by namespace, image, and cert chain. They port to AWS unchanged. The Go scaffolding (cobra CLI, terraform-exec wrapper, client-go k8s wrapper, miekg/dns probe, doctor framework, four-role sprint pattern, mdBook framework, execution backends, cred resolver) is also reusable as-is. Net new work concentrates on the cloud-side primitives:

- EKS in place of ROKS (cluster + node group + data plane)
- S3 (or ECR mirror) in place of IBM Cloud Object Storage
- IRSA in place of IBM Trusted Profile workload identity
- AWS credential chain in place of IBM Cloud API key chain
- Self-managed node groups on ENA-enabled instances in place of ROKS's managed worker pool, to support BNK's SR-IOV requirement

The fork preserves the upstream relationship: `git remote get-url upstream` returns `https://github.com/jgruberf5/roksbnkctl.git`, and shared-surface improvements can be cherry-picked across with `git fetch upstream && git log upstream/main ^main`.

## Goal

A user with an AWS account, `terraform` 1.5+ installed, and AWS credentials available via the standard chain (env / profile / instance role / SSO) can:

1. Run `awsbnkctl init` (interactive wizard → workspace config).
2. Run `awsbnkctl up` and get a healthy BNK deployment on an EKS cluster with SR-IOV node groups in **~30 minutes** on first run.
3. Run `awsbnkctl test` and confirm DNS, connectivity, and throughput against the deployment.
4. Run `awsbnkctl down` and return the account to clean state.

No `kubectl`, `aws` CLI, `iperf3`, or `dig` host install required — every dependency except `terraform` is internalised, either via SDK or via a bundled tools image.

## PRD inheritance map

PRDs 01-06 are inherited from roksbnkctl. PRDs 07-08 are net-new for the AWS retarget. PRDs 09+ are reserved for v1.x scope.

| PRD | Status | Outcome |
|---|---|---|
| [`01-SSH-AND-ON-FLAG.md`](./01-SSH-AND-ON-FLAG.md) | **inherited** (no rewrite needed) | `--on <target>` flag, embedded SSH client, `targets:` config block, jumphost auto-discovery from TF outputs |
| [`02-KUBECTL-INTERNAL.md`](./02-KUBECTL-INTERNAL.md) | **inherited** | Native `awsbnkctl get/apply/logs/exec/port-forward` via `client-go` — `oc` subset dropped (EKS isn't OpenShift) |
| [`03-EXECUTION-BACKENDS.md`](./03-EXECUTION-BACKENDS.md) | **inherited** (light edits) | Backend abstraction with four implementations (local / docker / k8s / ssh) applied to iperf3, terraform, and AWS API calls. The `ibmcloud` passthrough is dropped (no `aws` CLI passthrough planned — direct SDK use only) |
| [`04-CREDENTIALS.md`](./04-CREDENTIALS.md) | **inherited** (Sprint 2 edits) | Cred propagation; AWS adapter added in Sprint 2 (IRSA + standard chain) |
| [`05-E2E-TEST-PLAN.md`](./05-E2E-TEST-PLAN.md) | **inherited** (Sprint 6 edits) | E2E phases A-N + L-DNS; AWS-shaped equivalents in Sprint 6 |
| [`06-CLUSTER-TRIAL-PHASE-SPLIT.md`](./06-CLUSTER-TRIAL-PHASE-SPLIT.md) | **inherited** | Cluster phase / BNK trial phase split — semantics carry through |
| [`07-EKS-CLUSTER-SRIOV.md`](./07-EKS-CLUSTER-SRIOV.md) | **to author** (Sprint 1) | EKS cluster module + self-managed SR-IOV node group + Multus / SR-IOV CNI / SR-IOV device plugin stack. **Load-bearing design decision.** |
| [`08-S3-SUPPLY-CHAIN-IRSA.md`](./08-S3-SUPPLY-CHAIN-IRSA.md) | **to author** (Sprint 2) | S3 bucket for FAR pull keys + JWT licence + optional ECR mirror; IAM OIDC provider + IRSA for FLO service account |

## Dependency graph

```
                PRD 07 (EKS + SR-IOV)  ◀──── load-bearing
                    │
                    ▼
                PRD 08 (S3 + IRSA)
                    │
                    ▼
                inherited PRD 03 (backends) + PRD 04 (creds)
                    │   inherited PRDs 01, 02, 06 are surface-stable
                    ▼
                inherited PRD 05 (E2E) — AWS phases added Sprint 6
```

PRD 07 gates everything: if SR-IOV on EKS doesn't work the way BNK needs, the entire deployment shape is back to the drawing board. PRD 08 layers cleanly on top because IRSA needs the EKS OIDC provider that PRD 07 creates. The inherited PRDs touch the cloud-side surface only at parameter boundaries (cred chain, backend selection), so they need light edits, not rewrites.

## Success criteria (v1.0)

- Fresh Ubuntu / macOS dev host with only `terraform` installed: `awsbnkctl doctor` reports green; `awsbnkctl up` lands a healthy BNK deployment.
- E2E phases (roksbnkctl PRD 05 inheritance + Sprint 6 AWS phases) all pass.
- A `c5n.4xlarge` worker node advertises SR-IOV VFs to the scheduler; a BNK CNEInstance schedules onto it and reports `Ready` within 15 minutes.
- Credential audit: no AWS access key, secret key, session token, FAR pull-key, or licence JWT appears in `docker inspect`, `ps`, kube events, or any log file readable by another local user.
- `awsbnkctl down` on a fresh deployment leaves no leaked S3 objects, IAM roles, ENIs, or VPCs.

## Out of scope (v1.0)

- AWS regions outside the v1.0 tested set (us-east-1, us-west-2, eu-west-1). Other regions are best-effort.
- Karpenter / EKS Auto Mode / Fargate. v1.0 ships a fixed self-managed node group. v1.x revisits elasticity if SR-IOV semantics permit.
- Multi-region GSLB testing. v1.0 ships single-region DNS probes; v1.x adds multi-region.
- Air-gapped install. v1.0 assumes outbound HTTPS to AWS APIs + ECR + FAR registry; v1.x adds the ECR-mirror-only mode as a first-class story.
- Azure or GCP retargets. Forkable from this codebase the same way `awsbnkctl` was forked from `roksbnkctl`; not the scope of this project.

## Open meta-questions

- **Module path under `JLCode-tech/` vs. an F5-owned org.** v1.0 ships under `github.com/JLCode-tech/awsbnkctl`. Donating to an F5-owned org is a v1.x option; module-path rewrites are an SDK consumer break, so the decision needs to happen before v1.0 if it's happening at all.
- **Tools image hosting.** `ghcr.io/JLCode-tech/awsbnkctl-tools-*` for v1.0; donatable alongside the module path.
- **CNI baseline.** AWS VPC CNI is the obvious default, with Multus + SR-IOV CNI layered on top. Calico-on-EKS is an alternative for customers who already standardise on it; v1.0 supports VPC CNI only, v1.x evaluates Calico.
- **Karpenter readiness.** Karpenter doesn't have a clean integration with SR-IOV device plugins as of writing. Sprint 1 spike confirms the gap; v1.x revisits.