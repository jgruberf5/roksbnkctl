# Sprint 29 — staff issues (air-gap registry mirror: BOM, replication engine, RegistryTarget/OpenShift, install redirect, CLI)

> **Surfaced 2026-06-08** as a customer requirement: the ROKS environment forbids
> pulling install resources from external repositories at deploy time. Every BNK
> artifact must be replicated into the cluster's own registry and installed from
> there. Build a CRUD-shaped `registry` command group + an optional **Registry
> phase** that mirrors the BNK bill-of-materials from FAR into a pluggable target
> (**OpenShift internal registry** first) and redirects the BNK install onto it.
>
> ```
>   f5-bigip-k8s-manifest  ──parse──▶  BOM (25 charts + 56 images)  ∪  {Jetstack cert-manager, bitnami/kubectl}
>                                              │
>            registry replicate (crane/ORAS)  ▼
>   repo.f5.com|quay.io|docker.io|charts.jetstack.io  ──▶  <OpenShift internal registry>/<ns>/{charts,images}
>                                              │
>            bnk up (redirected via registry-mirror.json)  ▼
>   charts ← route (helm provider) · images ← svc:5000 (kubelet, system:image-puller RBAC)
> ```
> Full spec: [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md). Design decisions:
> `issue_sprint29_architect.md` (BLOCKING for Stages 3-4).

`Status`: open (draft — not yet dispatched)

### Locked decisions (integrator; confirm before dispatch)
- **Pluggable `RegistryTarget`**, OpenShift internal registry as impl #1 (ICR /
  generic OCI are follow-on impls behind the interface).
- **Full BOM** = the 81 manifest artifacts ∪ the non-F5 deps (**keep** Jetstack
  cert-manager + `bitnami/kubectl`, mirror as externals — do NOT retarget onto
  the manifest's F5-packaged `f5-cert-manager`/`node-labeler`).
- **Charts AND images both hosted in the registry** (charts pulled by the host's
  helm provider over the route; images by pods over the in-cluster service).
- **Client-side replication** — the `roksbnkctl` host bridges FAR → the registry
  route. No in-cluster copy job (would need the cluster to reach FAR).
- **New dependency**: go-containerregistry (crane) + ORAS. `internal/ibm/` has no
  container-registry client today.
- **Registry phase is NOT in composite `up`** — explicit, like `gateway`.

---

## Issue 1 — BOM builder (`internal/bnkbom`) [Stage 1]

**Severity**: high
**Status**: open

- Pull `f5-bigip-k8s-manifest@<f5_bigip_k8s_manifest_version>` via the FAR OCI
  source client (Issue 2) and parse `bigip-k8s-manifest-<version>.yaml` into the
  typed `BOM` (per architect Issue 1) — a flat `helm_charts[]`/`docker_images[]`
  YAML parse, no rendering. Reuse the version-discovery path that
  `modules/flo/modules/flo/main.tf:417-465` shells today, lifted into Go.
- Union the non-F5 deps: the Jetstack cert-manager chart + its quay.io image set
  (render the chart or a version-pinned known-image list, per architect) and
  `bitnami/kubectl`, version-keyed off `cert_manager_version` + the node-labeler
  tag.
- `roksbnkctl registry bom` — print the resolved BOM (`--json` / table).
- Tests: parse the **real 2.3.0 manifest fixture** (the one already on disk from
  test-003's deploy — 25 charts + 56 images), the dep union, version pinning,
  determinism.

## Issue 2 — Replication engine + read verbs (`internal/registry/mirror`) [Stage 2]

**Severity**: high
**Status**: open

- Registry-to-registry copy by digest: `crane.Copy` for images, ORAS for OCI
  charts. **Idempotent** (skip on digest match), **concurrent** (bounded pool),
  **resumable**. Resolve tag→digest at copy time and record it.
- **Heterogeneous sources** (PRD 11 §3): `repo.f5.com` (FAR `_json_key_base64`
  cred, reuse the COS-tarball path), `quay.io` + `docker.io` (public OCI), and
  `charts.jetstack.io` (classic Helm HTTP repo — `helm pull` + repackage as OCI
  on push). Dispatch on source type.
- `registry list` / `diff` (BOM − target) / `verify` (every artifact present +
  digest-matched). Test against a fake in-process OCI registry.

## Issue 3 — `RegistryTarget` + OpenShift impl (`internal/registry`, `internal/registry/openshift`) [Stage 3]

**Severity**: high
**Status**: open — BLOCKED by architect Issues 2 & 3

- The `RegistryTarget` interface (architect Issue 2): `Prepare`, `PushEndpoint`,
  `ImagePullRef`, `ChartPullRef`.
- OpenShift impl: `Prepare` flips `defaultRoute=true` on the image-registry
  operator config, reads the route, ensures the mirror namespace, mints a
  `registry-editor` push SA, binds `system:image-puller` for the BNK SAs.
  Endpoints per architect.
- `registry replicate` (`--target openshift` default, `--dry-run`,
  `--concurrency`, `--include-deps` default on) + `prune`.

## Issue 4 — Install redirection [Stage 4]

**Severity**: high
**Status**: open — BLOCKED by architect Issues 3 & 4

- Write/read the `registry-mirror.json` record (chart host, image host, ns,
  digests) — sibling of `cluster-outputs.json`, in `internal/config`.
- `internal/tf/vars.go`: when the record is present, **split `far_repo_url`**
  into the rendered chart host (route → helm_release `repository`) and image host
  (svc → chart `image.repository` + CNEInstance `spec.registry.uri`); drop
  `far-secret` for images (RBAC); redirect cert-manager + `bitnami/kubectl`.
- The BNK-phase consume; the `up`/`bnk up` guard when `registry verify` is
  incomplete.

## Issue 5 — CLI wiring + config

**Severity**: medium
**Status**: open

- The `registry` command group (`bom`, `replicate`, `list`, `diff`, `verify`,
  `prune`) wired in `internal/cli`, resolving the workspace via the
  Sprint-29-fixed `resolvedWorkspaceName()` path.
- The `registry:` workspace-config block (`internal/config/workspace.go`):
  target kind, namespace, optional source/target creds, include-deps.

### Scope guards
- Don't touch the FLO/CIS/CNEInstance terraform bodies beyond the
  `far_repo_url`-split render wiring (the refs already flow from the var).
- Registry phase stays OUT of composite `up`.
- No live-cluster dependency in unit tests (fake registry + the manifest fixture).

### Acceptance criteria
1. `registry bom` lists all 81 manifest artifacts + the non-F5 deps, version-pinned.
2. `registry replicate --target openshift` mirrors the full BOM; `registry verify` green.
3. `bnk up` against a populated mirror installs from the internal registry (Stage 4).
4. `gofmt`/`vet`/`staticcheck`/`go test ./...` clean; build to PATH per the
   binary-path trap (`go build -o ~/.local/bin/roksbnkctl`).

### Files affected
- **New**: `internal/bnkbom/`, `internal/registry/` (+ `/mirror`, `/openshift`),
  `internal/cli/registry.go`, `internal/config/registry_mirror.go`.
- **Modified**: `internal/tf/vars.go`, `internal/config/workspace.go`,
  `internal/cli/lifecycle.go` (register `registry`; the `up` guard), `go.mod`.

### Related
- [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md) · `issue_sprint29_architect.md` ·
  `issue_sprint29_validator.md`.
- `terraform/variables.tf:233`, `modules/flo/modules/flo/main.tf`,
  `modules/cne_instance/modules/cneinstance/main.tf:116`, `internal/tf/vars.go`.
