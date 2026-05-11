# COS supply chain management

BIG-IP Next for Kubernetes (BNK) pulls its runtime artefacts — the F5 Application Runtime (FAR) container images, the JWT licence used at install + renewal time, the f5-bigip-k8s-manifest Helm chart, and the schematic JSON the deployer renders — from IBM Cloud Object Storage (COS). The COS bucket is the **supply chain**: it's how artefacts produced upstream (F5 build pipeline, licence-issuing service, schematic generator) reach the cluster.

`roksbnkctl cos` is the management surface for that supply chain. Three command levels — `cos instance`, `cos bucket`, `cos object` — cover the full CRUD on COS resources without touching the `ibmcloud` CLI; everything (most visibly `cos object put` for uploads and `cos object get` for downloads) goes through the IBM Cloud Go SDKs ([go-sdk-core](https://pkg.go.dev/github.com/IBM/go-sdk-core/v5), [platform-services-go-sdk](https://pkg.go.dev/github.com/IBM/platform-services-go-sdk), [ibm-cos-sdk-go](https://pkg.go.dev/github.com/IBM/ibm-cos-sdk-go)).

## What COS is in this stack

COS is IBM's S3-compatible object store. Two layers matter here:

- **Instance**: a service instance under [Resource Controller](https://cloud.ibm.com/docs/account?topic=account-resource-controller). Instances are global; they don't pin to a region. A workspace typically uses one COS instance per environment (dev, staging, prod) and shares it across multiple buckets.
- **Bucket**: an S3-style bucket with regional affinity, hosted on a COS instance. Buckets carry the storage class (`standard`, `vault`, `cold`, `smart`) and the access policy (HMAC keys, service-instance creds, public ACLs — the latter are off-limits for BNK supply-chain use).

The BNK supply chain reads from one COS bucket per cluster's BNK install. The bucket holds:

| Object | What it is | Consumed by |
|---|---|---|
| `f5-far-auth-key.tgz` | FAR repository pull credentials — the F5-internal artefact key that lets FLO download FAR container images | `flo` module at install time |
| `trial.jwt` (or production equivalent) | BNK subscription JWT — the licence the CNE Instance presents | `flo` and `license` modules |
| `schematic-<v>.json` | The deployer's schematic JSON for the deployed BNK version | informational, not directly mounted into the cluster |
| (optional) FAR image tarballs | Pre-pulled FAR images for air-gapped installs | `flo` when running in disconnected mode |

The bucket structure is defined by the upstream HCL — concretely by the `ibmcloud_resources_cos_bucket` variable, which defaults to `bnk-schematics-resources`. The instance defaults to `bnk-orchestration`.

## The three command levels

```bash
roksbnkctl cos instance {create|delete|list}
roksbnkctl cos bucket   {create|delete|list} --instance <name-or-CRN>
roksbnkctl cos object   {put|get|delete|list} --instance <name-or-CRN>
```

All three layers resolve credentials through the standard [credential resolver chain](./14-credentials-resolver.md) — env var, OS keychain, workspace `api_key_b64`, prompt. There's no separate "COS credential"; the IBM API key authenticates against Resource Controller (instance ops) and IAM-signed S3 requests (bucket and object ops).

### `cos instance`

Manages COS service instances at the account level via [Resource Controller](https://cloud.ibm.com/apidocs/resource-controller).

```bash
# Create a Standard-plan instance under the workspace's resource group
roksbnkctl cos instance create bnk-orchestration --plan standard

# Override the plan by catalog UUID when roksbnkctl hasn't mapped the tier
roksbnkctl cos instance create bnk-orchestration --plan-id <uuid>

# List instances in the account
roksbnkctl cos instance list

# Delete an instance (default: recursive — removes bound HMAC keys, service creds)
roksbnkctl cos instance delete bnk-orchestration
roksbnkctl cos instance delete bnk-orchestration --no-recursive --auto
```

| Flag | Default | Notes |
|---|---|---|
| `--plan` | `standard` | Friendly name (`standard`, `lite`); maps to a Resource Controller plan UUID internally. |
| `--plan-id` | — | Catalog UUID — bypasses the friendly-name mapping. Use when IBM ships a plan tier `roksbnkctl` hasn't seen yet. |
| `--target` | `global` | COS instances are global; this is left as a flag for forward compatibility. |
| `--no-recursive` | (off) | On delete, do NOT remove bound HMAC keys and service credentials. Hardly ever what you want. |
| `--auto` | (off) | On delete, skip the y/N confirmation. |

The resource group is read from the workspace's `ibmcloud.resource_group` field (defaulting to `default` when unset).

### `cos bucket`

Manages buckets within a named instance. The `--instance` flag is required for every `bucket` and `object` call — buckets aren't globally unique, only unique within an instance.

```bash
# Create a standard-class bucket
roksbnkctl cos bucket create bnk-schematics-resources \
  --instance bnk-orchestration \
  --class standard

# List buckets on the instance
roksbnkctl cos bucket list --instance bnk-orchestration

# Delete (the bucket must be empty first; cos object delete --recursive isn't implemented yet)
roksbnkctl cos bucket delete bnk-schematics-resources --instance bnk-orchestration
```

| Flag | Default | Notes |
|---|---|---|
| `--instance` | (required) | Instance name or CRN — the CRN starts with `crn:v1:` and is used as-is; a bare name is looked up via Resource Controller. |
| `--region` | workspace region | The IBM Cloud region the bucket is pinned to. Override only when you're crossing regions deliberately. |
| `--class` | `standard` | Storage class: `standard` (frequently accessed), `vault` (infrequent), `cold` (archive), `smart` (auto-tiered). The BNK supply chain uses `standard` because FLO reads at install + every restart. |

### `cos object`

Manages objects (files) within a bucket. The key syntax is `<bucket>/<key/with/slashes>` — the parser splits on the first slash, so `bucket/dir/file.tgz` parses as bucket `bucket`, key `dir/file.tgz`.

```bash
# Upload (streaming; multipart auto-engages for large files)
roksbnkctl cos object put bnk-schematics-resources/f5-far-auth-key.tgz \
  ./local/f5-far-auth-key.tgz \
  --instance bnk-orchestration

# Download (streaming)
roksbnkctl cos object get bnk-schematics-resources/f5-far-auth-key.tgz \
  ./downloaded.tgz \
  --instance bnk-orchestration

# Delete
roksbnkctl cos object delete bnk-schematics-resources/old-trial.jwt \
  --instance bnk-orchestration

# List (with an optional key prefix)
roksbnkctl cos object list bnk-schematics-resources \
  --instance bnk-orchestration

roksbnkctl cos object list bnk-schematics-resources/schematics/ \
  --instance bnk-orchestration
```

The list output is a tab-separated `KEY SIZE MODIFIED` table — pipe through `column -t` for readability or `cut -f1` to extract just the keys.

## The BNK supply chain shape

A typical `bnk-schematics-resources` bucket after a clean install looks like:

```
$ roksbnkctl cos object list bnk-schematics-resources --instance bnk-orchestration
KEY                                     SIZE        MODIFIED
f5-far-auth-key.tgz                     2412        2026-05-08T14:12:33Z
trial.jwt                               1857        2026-05-08T14:12:34Z
schematic-2.3.0-3.2598.3-0.0.170.json   18432       2026-05-08T14:13:01Z
```

Three pieces of metadata in the upstream HCL ([`terraform/variables.tf`](https://github.com/jgruberf5/roksbnkctl/blob/main/terraform/variables.tf)) pin the bucket layout:

| HCL variable | Default | Object |
|---|---|---|
| `f5_cne_far_auth_file` | `f5-far-auth-key.tgz` | FAR pull credentials |
| `f5_cne_subscription_jwt_file` | `trial.jwt` | Subscription JWT |
| `f5_bigip_k8s_manifest_version` | `2.3.0-3.2598.3-0.0.170` | Schematic filename inferred from this |

Changing any of these in `terraform.tfvars` (or the workspace `bnk:` block, which renders into tfvars) changes which COS keys FLO will look for. The HCL doesn't auto-discover key names — they're literal.

For air-gapped installs where the cluster can't reach `repo.f5.com`, additional pre-pulled FAR image tarballs go in the same bucket and the `far_repo_url` variable points at a COS-backed proxy. That topology is out of scope for v1.0; the supply chain shape described here is the connected-mode happy path.

## Multipart upload and streaming download

FAR image tarballs run 1-5 GB. `cos object put` streams the input file into the bucket in 5 MB parts using S3-style multipart uploads:

- For files **under 5 MB**, a single-part `PutObject` is used (the SDK's default).
- For files **over 5 MB**, the SDK auto-engages multipart upload — the file is split into 5 MB parts, each uploaded in parallel (up to 4 concurrent parts), and finalised with a `CompleteMultipartUpload` call.

The split is transparent — there's no `--multipart` flag to set. The SDK handles it under the hood. If you want to verify multipart is happening for a specific file, watch with `roksbnkctl --verbose cos object put …` and the SDK's debug logging surfaces the part count.

`cos object get` is similarly streaming: the SDK pipes the body straight to the destination file without buffering it in memory. Multi-gigabyte downloads on a memory-constrained jumphost are safe.

If a multipart upload is interrupted (network drop, `^C`), the partial-upload state lingers on COS until cleaned up. Today `roksbnkctl` doesn't expose a "list and abort orphan multipart uploads" command — that's a v1.x addition. The workaround is to use `ibmcloud cos list-multipart-uploads` directly via [Chapter 17's docker backend](./17-execution-backends.md#docker-backend) or the IBM Cloud console.

## Workspace config integration

The workspace `cos:` block is optional — if the bucket is already populated (manually, or by an external CI pipeline), the block can be omitted entirely. When set, it triggers an auto-upload at `roksbnkctl up` time so the FAR pull and licence land before FLO needs them.

```yaml
# ~/.roksbnkctl/<workspace>/config.yaml
cos:
  instance: bnk-orchestration
  bucket: bnk-schematics-resources
  upload:
    - source: ./local/f5-far-auth-key.tgz
      key: f5-far-auth-key.tgz
    - source: ./local/trial.jwt
      key: trial.jwt
```

The block maps directly to [`internal/config/workspace.go::COSCfg`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/config/workspace.go):

| Field | Type | Purpose |
|---|---|---|
| `instance` | string | COS instance name or CRN. Looked up via Resource Controller at runtime. |
| `bucket` | string | Bucket name within the instance. |
| `upload` | list of `{source, key}` | Optional pre-flight uploads. `source` is a host filesystem path (relative or absolute); `key` is the destination object key in the bucket. |

Pre-flight uploads run before `terraform apply`, so FLO sees the artefacts when it pulls. Idempotent: re-running `up` re-uploads, which COS treats as overwrite — safe.

## When the supply chain matters

Three lifecycle moments where the COS bucket is in play:

### Install time

`roksbnkctl up` provisions FLO, which queries the bucket for `f5-far-auth-key.tgz` and `trial.jwt`. Missing either object → FLO fails to start → `terraform apply` retries (per [`internal/cli/lifecycle.go::applyWithRetry`](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/cli/lifecycle.go)) for ~3 attempts before erroring. The fix is always "put the missing object in the bucket and re-run `up`"; the lifecycle retry hides transient bucket-policy propagation lag but won't paper over a genuinely-empty bucket.

### Upgrade time

When `bnk.manifest_version` (or the `f5_bigip_k8s_manifest_version` HCL variable) bumps, FLO pulls a new FAR image tarball and re-renders the CNE Instance. If the new manifest version references a FAR image that isn't already in `repo.f5.com`'s public registry (rare, but happens for pre-release builds), the bucket holds the air-gapped fallback. Standard upgrades — the connected-mode case — don't touch the bucket; they just pull from `repo.f5.com` using the credentials in `f5-far-auth-key.tgz`.

### Licence rotation

When the trial expires or a production licence arrives, swap `trial.jwt` for the new file:

```bash
roksbnkctl cos object put bnk-schematics-resources/trial.jwt \
  ./new-license.jwt \
  --instance bnk-orchestration

# Force FLO to re-read the licence (delete the CNE Instance's License resource;
# FLO's reconciler re-creates it from the updated JWT)
roksbnkctl k delete license -n f5-bnk --all
```

FLO picks up the new JWT within 60-90 seconds. No `roksbnkctl up` re-run required for licence rotation alone.

## Worked example: rotating COS supply-chain assets

End-to-end Part VII scenario: the FAR auth key on file is about to expire, a new one arrived from the F5 distribution side, and you need to rotate it without taking BNK down. The same flow handles licence-JWT rotation (swap `trial.jwt` for the production JWT) and FAR-image-tarball uploads for air-gapped clusters. Cross-link to [Chapter 14](./14-credentials-resolver.md) for the API-key half of the rotation story; this walkthrough focuses on the COS object half.

```bash
# 1. Sanity-check the current state
roksbnkctl cos object list bnk-schematics-resources --instance bnk-orchestration

# 2. Upload the new auth key (overwrites the existing file)
roksbnkctl cos object put bnk-schematics-resources/f5-far-auth-key.tgz \
  ./new-far-auth-key.tgz \
  --instance bnk-orchestration

# 3. Verify the upload
roksbnkctl cos object list bnk-schematics-resources --instance bnk-orchestration
# Expected: the f5-far-auth-key.tgz row's MODIFIED timestamp is now

# 4. (optional, air-gapped only) Upload the FAR image tarball
roksbnkctl cos object put bnk-schematics-resources/far-2.3.0-images.tgz \
  ./far-2.3.0-images.tgz \
  --instance bnk-orchestration

# 5. Force FLO to re-read the supply chain
roksbnkctl k delete pod -n f5-bnk -l app=flo
# (FLO's controller restarts; the new pod re-pulls f5-far-auth-key.tgz on first reconcile)

# 6. Verify FLO is healthy with the new key
roksbnkctl logs flo
# Expected: no "failed to pull FAR image: unauthorized" lines
```

The third step is the verification gate. If FLO's logs still show auth failures after the pod restart, the new auth key was rejected by `repo.f5.com` — re-issue the key on the F5 side, not in the bucket.

## Cross-references

- [Chapter 12 §"`cos:` — COS supply-chain (optional)"](./12-workspace-config.md#cos--cos-supply-chain-optional) — the workspace-config block this chapter operationalises.
- [Chapter 13 — Terraform variables](./13-terraform-variables.md) — `f5_cne_far_auth_file`, `f5_cne_subscription_jwt_file`, `ibmcloud_cos_instance_name`, `ibmcloud_resources_cos_bucket` are the HCL handles.
- [Chapter 14 — Credentials](./14-credentials-resolver.md) — the API-key resolution that auths every `cos` call.
- [Chapter 24 — Day-2 ops](./24-day-2-ops.md) — `roksbnkctl logs flo` is the post-rotation verification command.
- [Chapter 26 — Troubleshooting](./26-troubleshooting.md) — bucket-policy propagation lag, missing-object failure shapes.
