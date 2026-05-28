> **⚠️ ARCHIVED (2026-05-26):** This document describes the pre-pivot Terraform-embedded design and is retained for history only. The project now uses the Go-SDK phased provisioner driven by cluster.yaml — see [README.md](../../README.md) and [docs/POST_TERRAFORM_DIRECTION.md](../POST_TERRAFORM_DIRECTION.md).

# PRD 08 — S3 supply chain + IRSA workload identity

The cloud-side primitives that BNK pulls from at runtime: the FAR pull-key archive, the JWT licence, and (optionally) a mirror of the FAR registry's container images. Plus the workload identity that lets FLO pull these without a static AWS key on the wire.

`roksbnkctl` puts both in IBM Cloud Object Storage (COS) and binds FLO's service account to an IBM Trusted Profile. `awsbnkctl` replaces both: artefacts live in S3, optionally ECR for image mirroring, and FLO's service account binds to an IAM role via IRSA (IAM Roles for Service Accounts) using the EKS OIDC provider that PRD 07's `eks_cluster` module creates.

> **Status:** draft — Sprint 2. Builds on PRD 07's `cluster_oidc_issuer_url` + `oidc_provider_arn` outputs. The S3 + IRSA design is offline-validatable (`terraform validate`, mocked aws-sdk-go-v2). Live-AWS validation lands when the operator-run Sprint 1 spike validates PRD 07; this PRD's modules layer cleanly on top of a working `eks_cluster` apply.

> **Acronyms** used heavily below: **IRSA** = IAM Roles for Service Accounts; **OIDC** = OpenID Connect; **KMS** = AWS Key Management Service; **CMK** = customer-managed KMS key; **FAR** = F5 Application Runtime; **FLO** = the BNK control-plane operator that the workspace deploys; **STS** = AWS Security Token Service.

## Background

BNK 2.3's FLO operator needs three artefacts at apply time:

1. **FAR pull-key archive** (`f5cne-far-auth-*.tar.gz`) — credentials FLO uses to pull BNK container images from `dev-registry.f5.com`.
2. **Subscription JWT** (`f5cne-subscription-*.jwt`) — proves a valid F5 subscription; required by CNEInstance reconciliation.
3. **CA cert chain** — for the cluster's internal mTLS. cert-manager generates this; not part of this PRD.

In `roksbnkctl` flow, (1) and (2) are uploaded to an IBM COS bucket by `roksbnkctl init`; FLO pulls them at apply time using a Trusted Profile that allows COS read against the bucket. The trust chain ties the FLO Kubernetes service account → Trusted Profile → IBM IAM role with `cos:object:read` permission scoped to the bucket.

On AWS the equivalent shape:

- **S3 bucket** (server-side encrypted with KMS) — stores the FAR archive + JWT + any other Sprint 3 module needs.
- **IRSA** — IAM Roles for Service Accounts. EKS's OIDC provider issues tokens that AWS STS exchanges for AWS credentials; the FLO service account's pod sees AWS creds in `AWS_WEB_IDENTITY_TOKEN_FILE` + role ARN env vars.
- **(Optional) ECR mirror** — for air-gapped deployments where outbound HTTPS to F5's FAR registry isn't allowed. `skopeo copy` mirrors the FAR images into the customer's ECR; FLO's image references are rewritten to point at ECR. v1.0 stretch; v1.x first-class.

## Goal

After `awsbnkctl init` (AWS path) + `awsbnkctl up`:

- An S3 bucket exists under the customer's account, KMS-encrypted, restricted to the FLO IRSA role.
- The FAR archive + JWT are uploaded as `aws_s3_object` resources.
- An IAM OIDC provider exists for the EKS cluster (created by `terraform/modules/eks_cluster/` per PRD 07; this PRD references it).
- An IAM role with trust policy bound to the FLO service account exists; the role's permission policy grants `s3:GetObject` on the supply-chain bucket prefix.
- FLO's deployment annotates its service account with `eks.amazonaws.com/role-arn`; the EKS pod identity webhook injects the credential env vars.

No static AWS access key appears in any Kubernetes Secret or pod spec.

## Options considered

| Option | Verdict | Why |
|---|---|---|
| **S3 + IRSA** | **selected** | First-class AWS workload identity; no static keys on the wire; EKS-native trust chain. Matches the design intent that lifted from roksbnkctl's Trusted Profile pattern. |
| **S3 + EC2 instance role** | rejected | Worker node role applies to every pod on the node, not just FLO — over-broad. IRSA is the modern best practice for per-workload AWS access. |
| **S3 + static IAM user access key in K8s Secret** | rejected | Static credentials on the wire; defeats the design intent. A v0.x-style "just get it working" shortcut, not v1.0 fit. |
| **ECR + IRSA (instead of S3)** | partial | ECR works for image artefacts, not for non-image artefacts like the JWT. v1.0 uses **both**: S3 for artefacts, optional ECR mirror for images. |
| **AWS Secrets Manager for the JWT** | considered | A cleaner home for secrets than S3, but adds a second cloud primitive without functional gain — IRSA can read S3 too, and the JWT's threat model (read-only at apply time) doesn't benefit from rotation semantics Secrets Manager provides. v1.x revisit. |

## Decision

awsbnkctl v1.0 ships:

- **Single S3 bucket per workspace.** Bucket name pattern: `awsbnkctl-<workspace>-<random-suffix>`. SSE-KMS with a customer-managed key (CMK) created in the workspace's region.
- **Bucket versioning enabled by default.** The `s3_supply_chain` module sets `aws_s3_bucket_versioning.status = "Enabled"` unconditionally (no `var.enable_bucket_versioning` toggle); operators get a built-in audit trail of FAR / JWT rotations without opting in. The artefacts are small (kilobytes) and rotate annually-ish, so the storage cost of the retained noncurrent versions is rounding-error compared to the CMK itself. Lifecycle expiry of noncurrent versions is **not** configured by the module — operators who want to bound the retention window add an `aws_s3_bucket_lifecycle_configuration` resource downstream, or accept indefinite retention as the conservative-by-default audit trail. (Sprint 2 originally drafted this as "off by default with a `var.enable_bucket_versioning` opt-in"; the module landed with versioning forced-on because the audit-trail value clearly outweighs the storage cost. This Sprint 3 PRD pass updates the design contract to match the shipped module.)
- **Bucket policy** scopes `s3:GetObject` to the FLO IRSA role ARN. Bucket-policy semantics make a scoped `Allow` (with default-deny on every other principal) sufficient on its own; v1.0 ships an **explicit `Deny`** for non-IRSA principals on top of that as defence-in-depth against future hand-edits of the policy in the AWS console.
- **`aws_s3_object` resources** for the FAR archive + JWT, sourced from local paths the operator supplied to `awsbnkctl init`. `init` writes those paths into the workspace's generated `terraform.tfvars` (`far_auth_file_local_path` + `jwt_file_local_path`); the actual upload is `terraform apply`'s job, not `init`'s. This keeps the supply chain reproducible from `terraform apply` alone (matches the "one declarative source of truth" intent). The `internal/aws/s3.go` `PutObject` helper exists for doctor probes and for the v1.x "live re-upload without `terraform apply`" path, not for the init-time upload flow.
- **IAM OIDC provider** — created by `terraform/modules/eks_cluster/` per PRD 07; this module references it via `data.aws_iam_openid_connect_provider`.
- **IAM role** with trust policy:
  ```json
  {
    "Effect": "Allow",
    "Principal": {"Federated": "<oidc_provider_arn>"},
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {
      "StringEquals": {
        "<oidc-issuer>:sub": "system:serviceaccount:flo-system:flo-controller",
        "<oidc-issuer>:aud": "sts.amazonaws.com"
      }
    }
  }
  ```
- **Role permission policy** — `s3:GetObject` on `arn:aws:s3:::<bucket>/*`; nothing else.
- **(Optional, v1.0 stretch) ECR mirror module** — gated on `var.enable_ecr_mirror`. Creates an ECR repository per FAR image, runs `skopeo copy` via a `null_resource` `local-exec` provisioner using the tools-image. FLO's image references are rewritten to point at ECR.

## Architecture (rendered)

```
┌─────────────────────────────────────────────────────────────────┐
│ AWS account                                                     │
│                                                                 │
│   ┌──────────────────────────┐                                  │
│   │ S3 bucket                │                                  │
│   │   awsbnkctl-<ws>-<rnd>   │ ← KMS-encrypted (CMK)            │
│   │                          │                                  │
│   │   far-auth.tar.gz        │                                  │
│   │   subscription.jwt       │                                  │
│   └────────┬─────────────────┘                                  │
│            │                                                    │
│            │ s3:GetObject (bucket policy)                       │
│            │                                                    │
│   ┌────────▼─────────────────┐                                  │
│   │ IAM role                 │                                  │
│   │   FLO-supply-chain-      │                                  │
│   │     reader               │                                  │
│   │                          │                                  │
│   │   trust: sts:Assume-     │                                  │
│   │     RoleWithWebIdentity  │ ← OIDC federation                │
│   │   bound to flo-system/   │                                  │
│   │     flo-controller SA    │                                  │
│   └────────┬─────────────────┘                                  │
│            │                                                    │
│            │ assume-role via OIDC                               │
│            │                                                    │
│   ┌────────▼─────────────────┐                                  │
│   │ IAM OIDC provider        │ ← created by PRD 07's            │
│   │   from EKS cluster       │   eks_cluster module             │
│   └──────────────────────────┘                                  │
└─────────────────────────────────────────────────────────────────┘

In the cluster:

  ┌─────────────────────────────────┐
  │ flo-system namespace            │
  │                                 │
  │   ServiceAccount flo-controller │
  │     annotated:                  │
  │     eks.amazonaws.com/role-arn  │
  │     = <FLO-IRSA-role-arn>       │
  │                                 │
  │   Pod (FLO controller)          │
  │     env injected by             │
  │     EKS pod-identity webhook:   │
  │       AWS_ROLE_ARN              │
  │       AWS_WEB_IDENTITY_TOKEN_   │
  │         FILE (mounted token)    │
  └─────────────────────────────────┘
```

## Implementation outline

### Terraform module: `terraform/modules/s3_supply_chain/`

**Inputs:**

| Variable | Type | Default | Description |
|---|---|---|---|
| `region` | string | _required_ | AWS region |
| `workspace_name` | string | _required_ | Used in bucket name + tagging |
| `kms_key_arn` | string | `""` | Existing CMK; if empty, module creates one |
| `far_auth_file_local_path` | string | _required_ | Path to local FAR archive |
| `jwt_file_local_path` | string | _required_ | Path to local subscription JWT |
| `bucket_name_override` | string | `""` | Override generated bucket name |

**Outputs:**

| Output | Description |
|---|---|
| `bucket_name` | The bucket name (generated or overridden) |
| `bucket_arn` | The bucket ARN |
| `far_auth_object_key` | S3 key of the FAR archive |
| `jwt_object_key` | S3 key of the JWT |
| `kms_key_arn` | KMS CMK ARN (consumed by IRSA module for KMS:Decrypt permission) |

### Terraform module: `terraform/modules/iam_irsa/`

**Inputs:**

| Variable | Type | Default | Description |
|---|---|---|---|
| `region` | string | _required_ | AWS region |
| `oidc_provider_arn` | string | _required_ | From PRD 07's `eks_cluster` module |
| `cluster_oidc_issuer_url` | string | _required_ | From PRD 07's `eks_cluster` module |
| `flo_namespace` | string | `"flo-system"` | Where FLO's SA lives |
| `flo_service_account_name` | string | `"flo-controller"` | The SA name |
| `s3_bucket_arn` | string | _required_ | From `s3_supply_chain` module |
| `kms_key_arn` | string | _required_ | From `s3_supply_chain` module (for KMS:Decrypt on encrypted objects) |

**Outputs:**

| Output | Description |
|---|---|
| `flo_role_arn` | The IAM role ARN to annotate on FLO's ServiceAccount |
| `flo_role_name` | The IAM role name |

### Terraform module: `terraform/modules/ecr_mirror/` (optional, v1.0 stretch)

Gated on `var.enable_ecr_mirror`. Creates one `aws_ecr_repository` per image in the FAR archive (parsed at plan time via a `null_resource` that lists images), then runs `skopeo copy --src-authfile=<far-auth> docker://dev-registry.f5.com/<image> docker://<ecr-uri>/<image>` for each.

Out of scope this sprint if it stretches the staff agent's budget — the staff brief explicitly allows deferring ECR mirror to Sprint 3 follow-up. OBSOLETE per D-001 (post-TF direction).

### `internal/aws/` additions

| File | Surface |
|---|---|
| `s3.go` | `PutObject(ctx, bucket, key, body)`, `HeadObject(ctx, bucket, key)`, `GetObject(ctx, bucket, key)`. Used by doctor probes (`HeadObject` against the workspace prefix) and by the v1.x "rotate a single object without re-running `terraform apply`" path. The init-time upload of the FAR archive + JWT goes through `aws_s3_object` resources, not these helpers — see [Decision](#decision). |
| `iam.go` | `GetOIDCProvider(ctx, arn)`, `HasIRSARole(ctx, roleName)`. Used by doctor checks. |
| `s3_test.go`, `iam_test.go` | Mocked unit tests via the SDK middleware-test pattern. |

### CLI surface

| Verb | Behaviour |
|---|---|
| `awsbnkctl init` (AWS path) | Interactive wizard prompts for region, VPC, FAR archive path, JWT path, FLO namespace. Writes the resolved local paths into the workspace's generated `terraform.tfvars` (`far_auth_file_local_path` + `jwt_file_local_path`); does **not** upload to S3 directly. The subsequent `awsbnkctl up cluster` apply provisions the bucket and uploads the artefacts via `aws_s3_object` resources. |
| `awsbnkctl up cluster --dry-run` | (existing from Sprint 1) extends to include S3 + IRSA module plan. |
| `awsbnkctl doctor` | New rows: `aws s3:PutObject permission` (uses a probe key under a workspace prefix); `aws iam:GetOpenIDConnectProvider permission` probe. EC2 vCPU quota check from Sprint 1 staff Issue 2 also lands here (now that workspace + region are post-init available). |

## Trade-offs accepted

- **Customer-managed KMS key by default.** Costs $1/month per key. Alternative: SSE-S3 (free, AWS-managed). v1.0 uses CMK because the supply-chain bucket holds the JWT (sensitive); v1.x evaluates SSE-S3 as a doc'd alternative for cost-sensitive customers.
- **Bucket name uses `<workspace>-<random>`** rather than a customer-overridable input as the primary path. The override exists for governance scenarios. Trade-off: friction-free for fresh deployments, controllable for enterprise.
- **Single FLO IRSA role.** v1.0 doesn't separate FLO-controller permission from FAR-image-pull permission. v1.x revisits if F5 surfaces per-component permission needs.
- **No automatic FAR-image-cycle rotation.** When F5 publishes a new BNK release, the operator re-runs `awsbnkctl init` with the new FAR archive; the S3 object updates. ECR mirror (when enabled) auto-syncs on next `awsbnkctl up`.
- **Bucket versioning forced on; no `enable_bucket_versioning` toggle.** Operators who would otherwise disable versioning to save the (rounding-error) cost of noncurrent-version storage don't get that knob. The trade-off is the rotation audit trail: a JWT or FAR-archive overwrite can be diffed against the prior version without restoring from backup. The storage-cost objection doesn't survive arithmetic — two kilobyte objects rotated annually accumulate single-digit megabytes over the v1.x lifetime of a deployment.

## Open questions (resolved during Sprint 2)

- **Bucket policy: explicit `Deny` or scoped `Allow` only?** → **Explicit Deny ships.** AWS bucket-policy semantics make either correct, but the explicit Deny defends against future hand-edits in the console that might otherwise loosen access.
- **Where does the FAR archive live during `awsbnkctl init` if the operator doesn't already have it locally?** → Out of scope: F5 distributes the FAR archive separately, and `init` accepts a local path. The wizard errors if the path doesn't resolve.
- **Should `awsbnkctl init` upload via aws-sdk-go-v2 directly (no terraform) or via a terraform `aws_s3_object` resource?** → **`aws_s3_object`** so the supply chain is reproducible from `terraform apply` alone (matches the "one declarative source of truth" intent). `init` writes the local paths into `terraform.tfvars`; `up` applies. `internal/aws/s3.go` exists for doctor probes and rotation, not the init-time upload.
- **Does the IRSA module need both `oidc_provider_arn` and `cluster_oidc_issuer_url`, or just the ARN?** → **Both.** The ARN is needed for the trust policy's `Principal.Federated`; the issuer URL is needed (with the `https://` scheme stripped) to construct the `<oidc-issuer>:sub` and `<oidc-issuer>:aud` condition keys. PRD 07's `eks_cluster` module exposes both as outputs.

## Resolved-in-spike

This PRD's design doesn't directly depend on PRD 07's spike findings — S3, IRSA, and bucket policies are well-trodden AWS surface that `terraform validate` and mocked aws-sdk-go-v2 tests cover offline. But the **live-apply path** depends on the EKS OIDC provider existing, which means PRD 07's `eks_cluster` module must successfully `terraform apply` before this PRD's modules can. The Sprint 1 operator-run spike (PRD 07 § "Spike protocol") validates that prerequisite. After spike validation, the Sprint 2 modules layer cleanly on top; no Sprint 2 rework is anticipated from spike findings.

If the spike forces a fall-back to the multi-ENI shape (PRD 07 § "Spike fail modes"), this PRD's design is unaffected — IRSA + S3 are independent of the data-plane choice.

## Cross-references

- [`PLAN.md` § Sprint 2](../PLAN.md) — calendar + deliverables.
- [`prd/07-EKS-CLUSTER-SRIOV.md`](./07-EKS-CLUSTER-SRIOV.md) — supplies `cluster_oidc_issuer_url` + `oidc_provider_arn` outputs this PRD consumes.
- [`prd/04-CREDENTIALS.md`](./04-CREDENTIALS.md) — inherited; the cred chain that resolves AWS keys at the host level (env / profile / instance role / SSO). IRSA is the *in-cluster* cred shape — distinct from but complementary to PRD 04's host-side chain.
- [AWS IRSA docs](https://docs.aws.amazon.com/eks/latest/userguide/iam-roles-for-service-accounts.html) — canonical reference.
- [`MIGRATING.md` § From roksbnkctl](../../MIGRATING.md) — captures the IBM-Trusted-Profile ↔ IRSA swap for users coming from the IBM Cloud path.