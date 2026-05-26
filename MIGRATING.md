# Migrating to awsbnkctl

> **Note (2026-05-26):** This document was written when awsbnkctl used an embedded Terraform tree. The project has since pivoted to a Go-SDK phased provisioner driven by a `cluster.yaml` intent file. Terraform is no longer required and is being removed from the repository. The migration steps below that reference `awsbnkctl init`, `terraform.tfvars`, or the TF-embedded workflow no longer apply.
>
> The current workflow is: copy `examples/syd-tracer/cluster.yaml`, edit it for your account, then run `awsbnkctl up --config <file>`. See [README.md](README.md) and [docs/POST_TERRAFORM_DIRECTION.md](docs/POST_TERRAFORM_DIRECTION.md) for the current design.

---

The sections below are retained for historical reference only.

## From manual EKS + BNK deployment (historical)

If you currently stand BNK up on EKS by hand — `terraform apply` against your own HCL, `aws eks update-kubeconfig`, `kubectl apply -f cert-manager.yaml`, hand-rolled FLO values, manually-uploaded FAR pull keys — awsbnkctl replaces that workflow. The current awsbnkctl uses the AWS SDK directly (no Terraform) and drives the full activation chain through Phases 00–25.

The `cluster.yaml` intent file is the replacement for `terraform.tfvars`. AWS credentials continue to be resolved via the standard chain (env, profile, SSO, IRSA, IMDS) — no change there.

## From `roksbnkctl` on IBM Cloud (historical)

awsbnkctl originated as a hard fork of roksbnkctl. The CLI surface has diverged significantly. The IBM-Cloud-specific flows (`ibmcloud`, ROKS, IBM COS, IBM Trusted Profiles) have no equivalent and are not supported. The migration is a fresh deployment on EKS using `cluster.yaml`.

| roksbnkctl concept | awsbnkctl equivalent |
|---|---|
| ROKS cluster | EKS cluster (self-managed node group for BNK data-path) |
| `IBMCLOUD_API_KEY` env / keychain | `AWS_PROFILE` / `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` |
| IBM Cloud Object Storage (FAR pull keys + licence artefacts) | S3 bucket (server-side encrypted) |
| IBM Trusted Profile (workload identity for FLO) | IRSA — IAM role for the FLO service account via EKS OIDC provider |
| `roksbnkctl ibmcloud …` passthrough | Dropped — AWS API calls use `aws-sdk-go-v2` directly |

A `roksbnkctl` workspace cannot be migrated automatically. Start with a fresh `cluster.yaml` and `awsbnkctl up --config`.

## Between awsbnkctl versions

Per-version migration notes will be added here as releases are cut. Check [CHANGELOG.md](CHANGELOG.md) for the per-release change log.
