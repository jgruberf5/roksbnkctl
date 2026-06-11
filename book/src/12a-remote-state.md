# Remote state (COS / S3)

By default, roksbnkctl keeps terraform state in **local files** under your workspace — `~/.roksbnkctl/<workspace>/<phase>/terraform.tfstate`, one per phase. That's the right thing on a laptop, and it's fine for a single containerized runner with a [mounted volume](./04-installation.md#state-lives-in-a-volume-not-the-image). It breaks down in two cases:

- **Stateless / ephemeral runners** — a CI container with no persistent volume loses its state between runs.
- **Fleet / parallel runs** — local state has **no locking**, so two runs against the same state can corrupt it.

The fix is an optional **remote state backend** on IBM Cloud Object Storage (which exposes an S3-compatible API): each phase's state lives in a bucket, with a lock, so state is decoupled from the container and concurrent runs are safe. This chapter is how to turn it on, and how to move an existing workspace over.

The default is unchanged — **omit the `state:` block and everything works exactly as before.** Nothing here is required unless you want remote state.

## Turning it on

Add a `state:` block to your workspace `config.yaml`:

```yaml
state:
  backend: s3
  s3:
    endpoint: "https://s3.us-south.cloud-object-storage.appdomain.cloud"
    bucket:   "acme-bnk-tfstate"       # you create this ahead of time
    region:   "us-south"
    key_prefix: "roksbnkctl"           # optional; defaults to the workspace name
    # access_key_source / secret_key_source: env var names (see Credentials)
```

Three things are your responsibility to provision first; roksbnkctl consumes them and errors clearly if they're missing:

1. **The bucket** — create it in COS (it is your state store; roksbnkctl never creates or deletes it).
2. **HMAC credentials** — COS's S3 API authenticates with **HMAC** access/secret keys (a *Service Credential* with "Include HMAC Credential" enabled), **not** the IAM API key roksbnkctl uses elsewhere.
3. **terraform ≥ 1.10** — required for the native S3 lockfile (below). Selecting `s3` on an older terraform fails a preflight check with a clear message; the [runner image](./04-installation.md#path-c--run-from-the-all-in-one-container-image-no-install) bundles a new-enough terraform.

### Credentials

The HMAC keys are **never** written into `config.yaml`, the rendered HCL, or the state object. roksbnkctl reads them from the environment and passes them to terraform as `AWS_*` env at runtime:

```bash
export ROKSBNKCTL_COS_HMAC_ACCESS_KEY="…"
export ROKSBNKCTL_COS_HMAC_SECRET_KEY="…"
```

`access_key_source` / `secret_key_source` name the env vars to read; left unset they default to the two above, falling back to the standard `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY`. In CI you just export two secrets.

## How state is laid out

All four phases share the one bucket, each at its own key:

```
<key_prefix>/<workspace>/cluster/terraform.tfstate
<key_prefix>/<workspace>/bnk/terraform.tfstate
<key_prefix>/<workspace>/testing/terraform.tfstate
<key_prefix>/<workspace>/gateway/terraform.tfstate
```

So the cluster / BNK / testing / gateway states never collide, and the parallel `up` (BNK ∥ Testing) operates on distinct keys.

## Locking — and the single-writer guarantee

COS has no DynamoDB equivalent, so the lock is terraform's **native S3 lockfile** (`use_lockfile`, added in terraform 1.10 — hence the version floor): a small `<key>.tflock` object alongside each state object. It guarantees **one writer per phase key** — a second `apply` against the same phase blocks/fails on the lock rather than corrupting state.

What it does **not** do: serialize *different* phases or *different* workspaces — those are different keys with different locks, by design. So a parallel `up` is safe, and two operators on two different workspaces don't contend.

## Migrating an existing workspace

A **new** workspace with `state: backend: s3` just works — the first `up` creates state straight in the bucket. To move an **existing** local-state workspace over, set the `state:` block, export the HMAC keys, then:

```bash
roksbnkctl state migrate
```

It copies each deployed phase's local state into the bucket (via `terraform init -migrate-state`), one phase at a time:

```text
→ migrating cluster → s3://acme-bnk-tfstate/roksbnkctl/prod/cluster/terraform.tfstate
✓ cluster migrated
→ migrating bnk → s3://acme-bnk-tfstate/roksbnkctl/prod/bnk/terraform.tfstate
✓ bnk migrated
· testing: remote key … already holds state — skipping (use --force to overwrite)

state migrate: 2 migrated, 1 skipped, 0 failed
Local state files left in place — verify the remote read-back, then remove them when satisfied.
```

Two safety properties:

- **It won't clobber.** Before copying, it HEADs the target key; if the key already holds state, that phase is **skipped** (pass `--force` to overwrite deliberately).
- **It leaves your local files in place.** Migration is a copy, not a move — verify a `roksbnkctl status` / `plan` reads cleanly from the bucket, *then* delete the local `terraform.tfstate` files when you're satisfied.

## Notes and limits

- **Default is local.** An absent `state:` block (or `backend: local`) renders byte-identical to pre-remote-state behavior — no migration, no surprises.
- **COS is the supported target.** Other S3-compatible stores (AWS S3, MinIO) will likely work via the same block, but COS is what's tested.
- **roksbnkctl doesn't provision the bucket or keys.** That's deliberate — the state store is yours to own and lifecycle.

## See also

- [Chapter 4 §"Path C — the runner image"](./04-installation.md#path-c--run-from-the-all-in-one-container-image-no-install) — the stateless runner this pairs with.
- [Chapter 28 — Configuration reference §`state:`](./28-configuration-reference.md#state-block) — the field-by-field table.
- [Chapter 6 — Workspaces](./06-workspaces.md) — the local `~/.roksbnkctl/<workspace>/` layout this replaces for state.
