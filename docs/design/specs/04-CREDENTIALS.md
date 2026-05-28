> **⚠️ ARCHIVED (2026-05-26):** This document describes the pre-pivot Terraform-embedded design and is retained for history only. The project now uses the Go-SDK phased provisioner driven by cluster.yaml — see [README.md](../../README.md) and [docs/POST_TERRAFORM_DIRECTION.md](../POST_TERRAFORM_DIRECTION.md).

# PRD 04 — credential propagation across execution backends (cross-cutting)

> Cross-cutting concern for execution backends. Read this before designing or reviewing the backend interfaces.
>
> Estimated effort: small in code (~300 LOC), medium in design care.
>
> **Note.** This PRD has been trimmed to the AWS-retargeted credential surface. The pre-pivot IBM Cloud Trusted Profile sections are preserved in git history. Host-side credentials resolve via the AWS standard chain (env → profile → instance role → SSO); in-cluster workload identity is IRSA (see [PRD 08](./08-S3-SUPPLY-CHAIN-IRSA.md)).

## Resolved: AWS retarget {#resolved-aws-retarget}

The IBM → AWS cred-chain retarget closed alongside the Workspace alias rename (`Workspace.IBMCloud` → `Workspace.AWS`). The user-facing shape lives in [Chapter 14 — Credentials and the AWS resolver chain](../../book/src/14-credentials-resolver.md); the in-cluster shape lives in [Chapter 25 — S3 (and optional ECR) supply chain](../../book/src/25-cos-supply-chain.md) and [PRD 08](./08-S3-SUPPLY-CHAIN-IRSA.md).

### Where the AWS chain lives in the tree

The shipped tree splits the resolver and the IBM-legacy chain into two packages. The following is the canonical map from "the AWS standard chain" to "the package + function that implements it":

- **`internal/aws/` is the AWS standard chain.** `internal/aws/client.go::NewClients` wraps `config.LoadDefaultConfig(ctx, ...)` from `github.com/aws/aws-sdk-go-v2/config` and constructs the per-service SDK handles (STS, EC2, EKS, VPC; S3 + IAM lazily). The optional `aws.Options.Region` / `aws.Options.Profile` overrides feed `config.WithRegion` / `config.WithSharedConfigProfile`; everything else (env vars, shared config, SSO cache, IMDS, ECS / EKS pod-identity, web-identity token file) comes from the SDK's own chain. `aws.CredentialsConfigured(ctx, opts)` is the no-network probe that returns the resolved provider's `Source` string (`"EnvConfigCredentials"`, `"SharedConfigCredentials"`, `"IMDSv2"`, etc.) so doctor can name the resolution path without burning an STS call. `aws.HasEnvCredentials()` is the cheaper "did the operator set `AWS_PROFILE` or `AWS_ACCESS_KEY_ID`" pre-check used by the doctor row's failure-mode message.
- **`internal/aws/sts.go` is the live-credentials probe.** `Clients.CallerIdentity(ctx)` wraps `sts:GetCallerIdentity` and projects the response into the awsbnkctl-shaped `CallerIdentity{Account, ARN, UserID}` — `Account` is load-bearing for OIDC provider ARN derivation in PRD 08's IRSA wiring. AccessDenied from this call means "the chain resolved a key but AWS rejected it"; that's distinct from `CredentialsConfigured`'s "no credentials at all" failure, and the two-tier surface is the contract doctor renders.
- **`internal/cred/resolver.go` is the legacy IBM resolver, retained for naming back-compat only.** The package still compiles the IBM chain (env / keychain / workspace `api_key_b64` / prompt) but no production caller materialises a non-empty value: the `Workspace.IBMCloud` schema block was dropped, so `apiKeyFromConfig` is a no-op, and the `IBMCloudAPIKey` method is unreachable from `runFullLifecyclePlan` and the AWS-shape doctor rows. The package is **deprecated** and slated for deletion along with the dormant `internal/exec/creds.go::Credentials.IBMCloudAPIKey` field, the docker `credShimScript` tmpfile-bind-mount path, and the SSH wrapper-script `IBMCLOUD_API_KEY` env propagation.
- **In-cluster identity is not awsbnkctl code at all.** IRSA inside the cluster is auto-injected by the EKS-managed pod-identity webhook: the webhook sees the pod's ServiceAccount has the `eks.amazonaws.com/role-arn` annotation, mounts a projected SA token at `/var/run/secrets/eks.amazonaws.com/serviceaccount/token`, and sets `AWS_ROLE_ARN` + `AWS_WEB_IDENTITY_TOKEN_FILE` on the container's env. aws-sdk-go-v2 inside the pod sees those env vars and assumes the role via `sts:AssumeRoleWithWebIdentity` automatically — the same standard chain `internal/aws/client.go` uses on the host. The pod-identity webhook is part of EKS, not awsbnkctl; the IAM role and the SA annotation are created by `terraform/modules/iam_irsa/` per PRD 08.

In short: the *contract* the AWS chain implements (env → shared config → SSO → IMDS → ECS / EKS pod-identity → web-identity) is the SDK's, and `internal/aws/` is the package that consumes it. `internal/cred/` is a retirement candidate that doesn't sit on the AWS path.

The chain order itself, as the SDK resolves it:

| # | Source | SDK `Credentials.Source` string | Notes |
|---|---|---|---|
| 1 | Env vars (`AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` [+ `AWS_SESSION_TOKEN`]) | `EnvConfigCredentials` | The CI / explicit-injection path. `AWS_PROFILE` selects an alternate profile from the shared config; `AWS_REGION` overrides the workspace's region setting. |
| 2 | Shared config files (`~/.aws/credentials`, `~/.aws/config`) | `SharedConfigCredentials` | The dev-box path. Honours `AWS_PROFILE`; honours `source_profile` for role chaining. |
| 3 | SSO / IAM Identity Center (`sso_session` / `sso_account_id` in `~/.aws/config`) | `SSOProvider` | Picks up `aws sso login`-cached tokens. `awsbnkctl` does not initiate SSO login itself in v0.x; the operator runs `aws sso login` once per session and the cached token flows through this chain transparently. |
| 4 | EC2 instance-role IMDSv2 | `IMDSv2` | The CI-on-EC2 / bastion path. Used when `awsbnkctl` runs from an EC2 instance with an attached instance profile; no static keys needed on disk. |
| 5 | ECS / EKS pod task role (`AWS_CONTAINER_CREDENTIALS_RELATIVE_URI`) | `EcsContainer` | The "ops pod running awsbnkctl against another cluster" path. Same chain link the EKS pod-identity webhook injects, but for the *host-side* awsbnkctl invocation, not the in-cluster FLO pod. |
| 6 | Web identity token (`AWS_WEB_IDENTITY_TOKEN_FILE` + `AWS_ROLE_ARN`) | `WebIdentityCredentials` | The GitHub-Actions-OIDC-against-AWS path. |

The `Source` column is the literal string aws-sdk-go-v2 returns on the resolved `aws.Credentials.Source` field; `aws.CredentialsConfigured(ctx, opts)` returns it verbatim so the doctor row `aws credentials resolved` can name the path without an extra lookup table.

There is **no interactive prompt fallback** — when no chain link resolves, `awsbnkctl` errors with a deterministic message naming the resolved provider list and pointing at `awsbnkctl doctor` for diagnosis. This is a deliberate departure from the upstream `awsbnkctl` chain's "prompt for API key" tail: AWS credentials are multi-field (key ID + secret + optional session token + optional region + optional MFA), they have no canonical single-field stdin shape, and the recommended path on every supported platform is `aws configure` or `aws sso login` rather than ad-hoc prompting. The TTY/non-TTY behaviour is uniform: both error identically, both point at the same remediation.

### In-cluster: IRSA replaces Trusted Profile

The prior IBM Trusted Profile auto-provisioning model maps directly onto IRSA on AWS — the same "no static key in any Secret" property, the same projected-SA-token-as-OIDC-proof flow, the same lifetime semantics. The shape:

- **EKS OIDC provider** — created by the EKS cluster phase; replaces the prior IBM Cloud OIDC issuer.
- **IAM role per workload** — `awsbnkctl-ops-<workspace>` ops-pod role (later slice) + `awsbnkctl-<workspace>-flo-supply-reader` FLO role (intermediate slice); each one's trust policy pins `<oidc-issuer>:sub` to a specific `system:serviceaccount:<ns>:<name>`. Replaces the per-workspace trusted-profile naming.
- **Pod-identity webhook injection** — the EKS-managed webhook injects `AWS_ROLE_ARN` + `AWS_WEB_IDENTITY_TOKEN_FILE` + a projected SA token volume; aws-sdk-go-v2 inside the pod sees those env vars and assumes the role via `sts:AssumeRoleWithWebIdentity`. Replaces the IBM IAM endpoint's projected-SA-token exchange.

There is no `--trusted-profile={auto,on,off}` flag on the AWS surface — IRSA is the only in-cluster cred path, and the "static key in a Secret" fallback the IBM Cloud path retained is not offered. The trust chain is fully Terraform-managed; doctor probes the assume-role end-to-end (see [Chapter 25 § "Verifying the supply chain end-to-end"](../../book/src/25-cos-supply-chain.md#verifying-the-supply-chain-end-to-end)).

### Backend × credential matrix (AWS retarget)

The retargeted equivalents of the inherited tables further down this PRD:

| Backend | host-side AWS creds | in-cluster AWS creds | SSH key |
|---|---|---|---|
| **local** | env / profile / IMDS / SSO via the standard chain; same chain aws-sdk-go-v2 uses. `AWS_PROFILE` / `AWS_REGION` propagate to terraform via `TF_VAR_*` and to the AWS provider via its native env vars. | n/a | n/a |
| **docker** | bind-mount `~/.aws/` read-only at `/root/.aws:ro` (single dir, not parent); pass `AWS_PROFILE` / `AWS_REGION` / `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` / `AWS_SESSION_TOKEN` by name (no `=value` form, value inherits). For SSO-cached tokens, the mount picks up `~/.aws/sso/cache/`. | n/a | n/a |
| **k8s** | the ops pod's IRSA role (later slice); SA-annotated, webhook-injected. No static key in any Secret. | IRSA via the ops pod's IAM role; the same role aws-sdk-go-v2 picks up via env-var injection. | n/a — SSH not run from inside the cluster |
| **ssh** | propagate `AWS_PROFILE` (preferred) or `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY` + `AWS_SESSION_TOKEN` via `ssh -o SetEnv=...`; wrapper-script fallback when `AcceptEnv` isn't configured. The remote target's `~/.aws/credentials` is the canonical source when `AWS_PROFILE` alone is propagated. | n/a | the **backend's own** SSH key — separate from cred-for-tools (unchanged from the inherited shape) |

The anti-patterns from the inherited "Anti-patterns to avoid" list translate directly:

- ❌ `--env AWS_SECRET_ACCESS_KEY=$KEY` — value visible in `docker inspect`. Use `--env AWS_SECRET_ACCESS_KEY` (bare name).
- ❌ Bind-mounting `~/` or `~/.aws/sso/` parent — exposes other profiles' tokens. Bind-mount `~/.aws/` itself, read-only.
- ❌ Embedding AWS keys in workspace `config.yaml`. The AWS path has no `api_key_b64` equivalent; static keys belong in `~/.aws/credentials` (the standard chain reads them there) or in env vars.

### Doctor surface (AWS-shaped)

The doctor checks that replace the inherited IBM-Cloud-shaped rows:

| Row | Check |
|---|---|
| `aws credentials resolved` | `sts:GetCallerIdentity` succeeds; reports the resolved provider name (env / profile / IMDS / SSO / IRSA / container). |
| `aws region resolved` | `AWS_REGION` env or `~/.aws/config` profile region is set and is a valid AWS region string. |
| `aws eks describe-cluster` | When a workspace cluster name is set, `eks:DescribeCluster` succeeds against it. |
| `aws s3 supply-chain bucket reachable` | When the supply-chain bucket exists, `s3:HeadBucket` succeeds (uses the host-side identity, not IRSA — IRSA is in-cluster only). |
| `aws iam:GetOpenIDConnectProvider` | OIDC provider exists and matches the eks_cluster output. |
| `aws irsa flo role assumable` | The FLO IRSA role's trust policy resolves; condition keys match the FLO module SA defaults. |

The interactive-prompt-loop failure mode the inherited PRD covers (CI / non-TTY runs hanging on the `IBMCLOUD_API_KEY` prompt) does not recur on the AWS path — see "no interactive prompt fallback" above.

### Migration notes

Users coming from the IBM Cloud path:

1. `IBMCLOUD_API_KEY` → `AWS_PROFILE` (preferred) or `AWS_ACCESS_KEY_ID` + `AWS_SECRET_ACCESS_KEY`. Run `aws configure` once per dev box; awsbnkctl picks it up via the standard chain.
2. OS keychain entry for the IBM API key → no equivalent needed; `~/.aws/credentials` (mode `0600` by `aws configure`) is the canonical disk location.
3. Workspace `ibmcloud.api_key_b64` → no equivalent. The AWS retarget removes the plaintext-on-disk shortcut; static keys live in `~/.aws/credentials` or in env, nowhere else.
4. `awsbnkctl ops install` (later slice) provisions an IRSA role for the ops pod's ServiceAccount; no flag needed (IRSA is the only path).
5. FLO Trusted Profile → FLO IRSA role; the `iam_irsa` Terraform module (early slice) creates and binds it; see PRD 08.

See the [`POST_TERRAFORM_DIRECTION.md`](../../POST_TERRAFORM_DIRECTION.md) doc for the project-wide migration context.


---

> **Historical content trimmed.** The original PRD contained extensive pre-pivot history (IBM Cloud Trusted Profile auto-provisioning, the docker tmpfile-bind-mount pattern for `IBMCLOUD_API_KEY`, SSH wrapper-script env propagation for IBM-shaped tooling). That content is preserved in git history; the AWS-retargeted shape above replaces it end-to-end.
