# Sprint 31 — architect issues (the all-in-one runner image + COS/S3 remote state backend)

> **Sprint 31 frame.** Make roksbnkctl runnable as a single **container install
> target** — one image carrying the binary + every backend dependency it
> dispatches — and give that container a way to keep workspace state that isn't a
> host bind-mount: a **COS/S3 remote terraform state backend**. The two are a
> pair: a self-contained runner image is only fleet/CI-useful if state can live
> outside the container. Dev-only tooling (**mdbook + pandoc**) is explicitly NOT
> in the runner — those are build-time, not use-time.
>
> Architect authors two PRDs and the design decisions the staff/validator/
> tech-writer issues depend on:
>   - **PRD 15 — the all-in-one runner image** (`docs/prd/15-RUNNER-IMAGE.md`)
>   - **PRD 16 — COS/S3 remote terraform state backend** (`docs/prd/16-REMOTE-STATE-BACKEND.md`)

`Status`: open (draft — not yet dispatched)

---

## Issue 1 — PRD 15: the all-in-one runner image

**Severity**: high
**Status**: open

Frame a `tools/docker/runner` image distinct from the existing per-tool images.
Decisions to lock:

- **Contents (use-time only).** Multi-stage: stage 1 builds `roksbnkctl` (reuse
  the `roksbnkctl-tools-ibmcloud` Dockerfile's Go-build stage verbatim — same
  ldflags / version wiring); stage 2 bundles the binary + the dependencies the
  backends dispatch: `ibmcloud` (+ container-service plugin), `terraform`,
  `helm`, `kubectl`, `oc`, `iperf3`, `h2load`. **Excluded:** mdbook, pandoc,
  texlive, mermaid (dev/docs toolchain only — they belong in
  `tools/docker/mdbook`, not here). Decide the base image (ubuntu:22.04 to match
  tools-ibmcloud, vs a slimmer base) and pin/version each CLI.
- **`--backend local` is the in-container default story.** Once every tool is in
  the image, the docker/k8s backends (which exist to fetch a tool from another
  network location) are redundant *inside* the runner — so the runner runs tools
  locally and avoids docker-in-docker. Confirm nothing forces a non-local
  backend by default; decide whether to hard-default `--backend local` when a
  "running in the runner" signal is present, or just document it.
- **State contract.** The image holds NO state; `$ROKSBNKCTL_HOME` (default
  `~/.roksbnkctl`) must point at durable storage. Define the volume contract:
  the `/work` mount the `kubeconfig_dir`/`scratch_dir` defaults already assume,
  `ROKSBNKCTL_HOME=/work/.roksbnkctl`, non-root uid 1000 ownership of `/work`.
  This is the bind/volume answer; PRD 16 is the no-volume answer.
- **Relationship to `tools-ibmcloud`.** The runner supersedes it for human/CI
  *use*; the per-tool images stay for the docker/k8s *backend dispatch* path.
  Decide whether the runner replaces tools-ibmcloud's self-exec role or coexists.

## Issue 2 — PRD 16: COS/S3 remote terraform state backend

**Severity**: high
**Status**: open

Today state is the local backend pinned to `<workspace>/<phase>/terraform.tfstate`
(`internal/tf/terraform.go`). Frame an optional remote backend so a stateless
runner (and parallel CI) need no shared volume.

- **Backend selection model.** A `config.yaml` `state:` block — `backend: local`
  (default, unchanged) `| s3`. For `s3`: endpoint (IBM COS S3 API), bucket,
  region/location-constraint, a key prefix, and HMAC credential sources. Decide
  whether it's global or per-workspace (per-workspace, to match config).
- **Per-phase key layout.** Four phases share one bucket; define the key scheme
  (e.g. `<prefix>/<workspace>/<phase>/terraform.tfstate`) so cluster/bnk/testing/
  gateway states don't collide and `terraform output` reads the right one.
- **Locking.** Local state has none — the gap this closes. Decide the mechanism:
  terraform's native S3 lockfile (`use_lockfile`, TF ≥1.10 — verify the pinned
  terraform version supports it against COS) vs a DynamoDB-equivalent (COS has
  none — likely rules it out). State the single-writer guarantee precisely.
- **Credentials.** COS S3 needs HMAC access/secret keys (not the IAM API key).
  Reuse the `cred.Resolver` source pattern (`*_source` → env/file/COS). Keys must
  never land in tfvars or state.
- **Migration.** Switching a workspace from local → s3 needs
  `terraform init -migrate-state` per phase. Define the one-time migration UX and
  whether roksbnkctl drives it or documents it.

## Issue 3 — Cross-cutting decisions

**Severity**: medium
**Status**: open

- **Air-gap synergy (PRD 11/14).** The runner is the natural single artifact to
  stage behind a firewall; PRD 16 lets its state live in the customer's own COS.
  Note the composition but keep each PRD independently shippable.
- **Sprint sequencing.** PRD 15 (image) ships independent of PRD 16 (state) — the
  volume-mount contract is the fallback. Mark S3 as the fleet-grade follow-on so
  the image can land first.

### Acceptance criteria (architect)
1. PRD 15 + PRD 16 authored under `docs/prd/`, each with goal/scope/design/
   acceptance, cross-linked, and listing the staff/validator/tech-writer surface.
2. The mdbook/pandoc exclusion and the `--backend local` in-container story are
   explicit in PRD 15.
3. The state key layout, locking decision, and HMAC-credential sourcing are
   pinned in PRD 16.

### Related
- [PRD 03 — execution backends](../docs/prd/03-EXECUTION-BACKENDS.md) (the
  per-tool image model the runner sits beside).
- `tools/docker/ibmcloud/Dockerfile` (the Go-build stage to reuse);
  `internal/tf/terraform.go` (the local-backend pinning PRD 16 extends);
  `internal/config/paths.go` (`ROKSBNKCTL_HOME`).
