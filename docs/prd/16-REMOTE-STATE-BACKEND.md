# PRD 16 — COS/S3 remote terraform state backend

> An optional `state.backend: s3` that keeps workspace terraform state in IBM Cloud Object Storage (S3-compatible) instead of local files — so a stateless runner ([PRD 15](./15-RUNNER-IMAGE.md)) and parallel CI need no shared host volume, and concurrent runs are protected by a lock. Default stays **local** (byte-identical to today); existing workspaces are unchanged. Estimated effort: medium (~250 LOC + a `state:` config block + a terraform-floor bump + tests + a book chapter). Depends on the runner bundling **terraform ≥ 1.10** (the native S3 lockfile).

## Why

State today is the local backend, pinned per phase to `<workspace>/<phase>/terraform.tfstate` (`internal/tf/terraform.go` writes a `roksbnkctl_backend_override.tf` with `backend "local" { path = … }`). That's correct for a laptop and even for a single containerized runner with a mounted volume (PRD 15). It breaks down for:

- **Stateless/ephemeral runners** — a CI container with no persistent volume loses state between runs.
- **Fleet / parallel runs** — local state has **no locking**; two runners against the same volume can corrupt `terraform.tfstate`.

IBM COS exposes an S3-compatible API, and terraform has a first-class `backend "s3"`. This PRD wires roksbnkctl to render that backend (per phase, with a lock) when the operator opts in — the proper answer to "how does the runner hold state" without a volume.

## Goal

A workspace can declare:

```yaml
state:
  backend: s3            # default: local (unchanged)
  s3:
    endpoint:   "https://s3.us-south.cloud-object-storage.appdomain.cloud"
    bucket:     "acme-bnk-tfstate"
    region:     "us-south"
    key_prefix: "roksbnkctl"           # default: the workspace prefix
    access_key_source: "env"           # HMAC keys via the cred.Resolver source chain
    secret_key_source: "env"
```

and every phase's state lives in COS at a per-phase key, locked, with the HMAC keys never touching tfvars, HCL, or the state object. Omitting the block keeps the local backend exactly as today.

## Design

### The injection point

roksbnkctl already writes a per-phase backend override (`internal/tf/terraform.go`). Branch it on `ws.State.Backend`:

- `local` (or absent) → today's `backend "local" { path = <stateDir>/terraform.tfstate }`. Unchanged — golden-tested.
- `s3` → a `backend "s3"` block (below).

The override is per-phase (each phase has its own `sourceDir` under `<stateDir>/tf-source`), so each phase renders its own key — no cross-phase collision, and the existing concurrent BNK∥Testing `up` keeps working (distinct keys ⇒ distinct lock objects ⇒ no false contention).

### The `backend "s3"` block for COS

```hcl
terraform {
  backend "s3" {
    endpoints = { s3 = "<endpoint>" }
    bucket    = "<bucket>"
    key       = "<key_prefix>/<workspace>/<phase>/terraform.tfstate"
    region    = "<region>"

    # COS is S3-compatible but not AWS — skip the AWS-only preflights:
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    skip_s3_checksum            = true   # COS rejects the default AWS checksum trailer

    use_lockfile = true                  # native S3 state locking (no DynamoDB)
  }
}
```

The **per-phase key** (`…/<workspace>/<phase>/terraform.tfstate`) is the critical bit: cluster / bnk / testing / gateway share one bucket without colliding, and `terraform output` for a phase reads its own key.

### Locking — and the terraform-floor decision

Local state has no lock; the point of going remote is safe concurrency. Terraform's S3 backend offers two lock mechanisms:

- **DynamoDB table** — AWS-only; **IBM COS has no DynamoDB equivalent**, so this is off the table.
- **Native S3 lockfile** (`use_lockfile = true`) — a `<key>.tflock` object in the same bucket; **added in Terraform 1.10**. Works against any S3-compatible store, COS included.

**Decision:** require **terraform ≥ 1.10 when `state.backend: s3`**. The runner image (PRD 15) bundles ≥ 1.10; `versions.tf` keeps `required_version = ">= 1.5"` for the local path but roksbnkctl emits a clear preflight error if `state.backend: s3` is selected on a terraform < 1.10 (checked via `terraform version`). No DynamoDB, no extra infra — just the lockfile in the same bucket. (Single-writer guarantee: the lockfile blocks a second concurrent apply on the *same* phase key; it does not serialize different phases or different workspaces, by design.)

### Credentials — HMAC, never in HCL or state

COS's S3 API authenticates with **HMAC** access/secret keys (not the IAM API key roksbnkctl already resolves). Add HMAC resolution reusing the `cred.Resolver` source chain (`env | keychain | config | prompt`), via `access_key_source` / `secret_key_source`:

- Resolved keys are injected into the terraform child as `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` env (the s3 backend reads them) — exactly how the IAM key already rides on the process env as `TF_VAR_ibmcloud_api_key`. They are **never** written into the rendered HCL (which would land in `.terraform/` and logs) nor into the state object.
- `env` source default keys: `ROKSBNKCTL_COS_HMAC_ACCESS_KEY` / `…_SECRET_KEY` (falling back to `AWS_ACCESS_KEY_ID`/`…SECRET…` if set), so a CI runner just exports two secrets.

### Config schema

```go
// internal/config/workspace.go — additive, omitempty; absent → local.
type StateCfg struct {
    Backend string     `yaml:"backend,omitempty"` // "" | "local" | "s3"
    S3      *StateS3Cfg `yaml:"s3,omitempty"`
}
type StateS3Cfg struct {
    Endpoint        string `yaml:"endpoint"`
    Bucket          string `yaml:"bucket"`
    Region          string `yaml:"region"`
    KeyPrefix       string `yaml:"key_prefix,omitempty"`        // default: ws.Prefix
    AccessKeySource string `yaml:"access_key_source,omitempty"` // cred source chain
    SecretKeySource string `yaml:"secret_key_source,omitempty"`
}
```

### Migration: local → s3

Flipping an applied workspace needs `terraform init -migrate-state` per phase (terraform copies the local state into the bucket). A `roksbnkctl state migrate` command drives it for all present phases:

- For each phase with local state, write the s3 backend override and run `init -migrate-state`.
- **Idempotent + safe:** refuse to migrate a phase whose target bucket key already holds state (no silent overwrite); leave the local file in place until the operator confirms the remote read-back. The reverse (`s3 → local`) is the same mechanism with the backends swapped — documented, lower priority.

## Scope

### In scope
- The `state:` config block; the `backend "s3"` render branch in `internal/tf`.
- HMAC credential resolution (cred source chain) + env injection.
- The terraform ≥ 1.10 preflight for `s3`.
- `roksbnkctl state migrate` (local → s3).
- A book chapter (tech-writer Issue 3) + config-reference entry.

### Out of scope
- **roksbnkctl creating the COS bucket / HMAC keys.** The operator pre-provisions the bucket + HMAC credential (it's their state store, their lifecycle). roksbnkctl consumes it; an actionable error if the bucket/keys are missing.
- Non-COS S3 stores (AWS S3, MinIO) — they'll likely work via the same block, but COS is the supported/tested target; others are best-effort.
- A locking story for terraform < 1.10 (none — the floor bump is the answer).

## Acceptance

1. A workspace with `state.backend: s3` plans/applies with state in COS at `<prefix>/<workspace>/<phase>/terraform.tfstate`; a second invocation reads it with **no** local `terraform.tfstate`.
2. `state.backend: local` (or an absent `state:` block) renders byte-identical backend output to pre-PRD-16 (golden test).
3. Two concurrent applies against the same phase key — one blocks/fails on the lockfile; state is not corrupted.
4. HMAC keys never appear in the rendered HCL, the state object, or logs.
5. `roksbnkctl state migrate` moves an existing local state into the bucket with no drift on the next plan, and refuses to clobber an occupied key.
6. Selecting `s3` on terraform < 1.10 fails preflight with an actionable message (not a mid-apply lock error).

## Open questions
- **Bucket bootstrap convenience.** Pre-provision-only (this PRD), or a later `state init` that creates the bucket + HMAC key via the IBM Cloud APIs roksbnkctl already calls? Pre-provision keeps the blast radius small for v1.
- **Locking across the parallel `up`.** Confirmed safe by per-phase keys (BNK and Testing lock different objects). Document so an operator doesn't expect cross-phase serialization.
- **Reverse migration UX** (`s3 → local`) — ship now or defer? Likely defer; the forward path is the demand.

## Cross-references
- [PRD 15 — the all-in-one runner image](./15-RUNNER-IMAGE.md) — bundles terraform ≥ 1.10 and is the primary consumer (stateless runner).
- [PRD 04 — credentials](./04-CREDENTIALS.md) — the `cred.Resolver` source chain HMAC resolution reuses.
- `internal/tf/terraform.go` — the `roksbnkctl_backend_override.tf` writer this branches; `internal/config/workspace.go` — the `state:` block; `terraform/versions.tf` — `required_version`.
- Sprint 31 issues: [staff](../../issues/issue_sprint31_staff.md) Issues 2–3, [validator](../../issues/issue_sprint31_validator.md) Issues 3–4, [tech-writer](../../issues/issue_sprint31_tech-writer.md) Issue 3.
