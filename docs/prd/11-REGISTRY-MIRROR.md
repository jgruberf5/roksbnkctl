# PRD 11 — Mirror the BNK install bill-of-materials from FAR into a private (cluster-internal) registry

> Prerequisites: [PRD 06 (cluster/trial phase split)](./06-CLUSTER-TRIAL-PHASE-SPLIT.md) and the Sprint 28 three-phase model — this adds an optional **Registry** phase between Cluster and BNK. Builds directly on Sprint 27's terraform-native BNK: the `far_repo_url`-driven chart/image references this redirects are the ones Sprint 27 established.

## Goal

Give `roksbnkctl` the ability to **replicate every artifact a BNK install needs** — the F5 charts + images enumerated by the `f5-bigip-k8s-manifest`, **plus** the non-F5 dependencies (cert-manager, `bitnami/kubectl`) — from the F5 Artifact Repository (FAR, `repo.f5.com`) into a **private registry**, and to **install BNK entirely from that registry**. The first-class target is the **ROKS cluster's own OpenShift internal registry**, so an air-gapped cluster (no F5/internet egress at deploy time) installs BNK from images it hosts itself. The registry surface is **CRUD-shaped** (replicate / list / diff / verify / prune / bom), modeled on the IBM COS client tool.

## Why

The customer's ROKS environment forbids pulling install resources from external repositories at deploy time; everything BNK needs must first be replicated into the cluster's registry and installed from there. `roksbnkctl` pulls from FAR directly today (helm OCI charts + image pull-secrets, all keyed on `far_repo_url`) with **no** mechanism to mirror those artifacts or to redirect the install to a mirror. This PRD adds both, and does it generically (a pluggable target) so the same machinery serves IBM Container Registry or a customer Harbor/Artifactory later.

## Scope

### In scope

- A **bill-of-materials (BOM)** builder: read `f5-bigip-k8s-manifest` (the FAR manifest `roksbnkctl` already fetches for version discovery), expand it to all F5 charts + images, and union with the non-F5 deps (cert-manager chart + images, `bitnami/kubectl`). Deterministic + version-pinned per BNK release.
- A **replication engine**: registry-to-registry OCI copy — charts (as OCI artifacts) *and* images — idempotent, digest-verified, concurrent.
- A pluggable **`RegistryTarget`** abstraction, with the **OpenShift internal registry** as implementation #1.
- The **`roksbnkctl registry`** command group: `bom`, `replicate`, `list`, `diff`, `verify`, `prune`.
- **Install redirection**: the BNK phase pulls charts + images from the mirror instead of FAR — which splits the single `far_repo_url` host into a chart host and an image host — with image pulls authorized by RBAC rather than a FAR pull-secret.
- A `registry:` workspace-config block; rendering in `internal/tf/vars.go`.
- Book chapter (air-gapped install) + tests.

### Out of scope (deferred — additive later)

- ICR and generic-OCI `RegistryTarget` implementations. The abstraction lands now; these are follow-on impls behind it.
- An **in-cluster** replication job. Client-side host-bridge only this sprint (see Design §7).
- cosign/signature verification of mirrored artifacts.
- Mirroring artifacts `roksbnkctl` never installs (e.g. legacy_curl-only paths outside the BOM).

## Background — what exists, and what's missing

- **One redirect variable.** Every FAR reference flows from `far_repo_url` (`terraform/variables.tf:233`, default `repo.f5.com`): charts `oci://${far}/charts/{f5-lifecycle-operator,f5-bnk-cis}` (`modules/flo/modules/flo/main.tf:1432,1470`), images `${far}/images` (`:7`), the CNEInstance `spec.registry.uri` (`modules/cne_instance/modules/cneinstance/main.tf:116`), and the `far-secret` dockerconfigjson (`modules/flo/modules/flo/main.tf:1323`). One variable redirects the whole image/chart surface — but it assumes a single host.
- **The manifest is already in the loop.** `f5-bigip-k8s-manifest` (`oci://repo.f5.com/release/f5-bigip-k8s-manifest`, pinned by `f5_bigip_k8s_manifest_version`, `variables.tf:239`) is pulled and grepped for the FLO/CIS chart versions (`modules/flo/modules/flo/main.tf:417-465`). It enumerates the F5 charts + versions (and image-digest mappings) — the seed for the BOM.
- **The manifest is a self-contained BOM — confirmed against the real 2.3.0 artifact.** `bigip-k8s-manifest-2.3.0-3.2598.3-0.0.170.yaml` is a flat structure: `f5_helm_repo: oci://repo.f5.com` + `f5_docker_repo: repo.f5.com`, then `releases[].helm_charts[]` (25 entries) and `releases[].docker_images[]` (56 entries), each `{name: <path e.g. charts/f5-tmm | images/tmm-img>, version: <tag>}`. Tag-pinned, no digests. **Every image BNK runs is listed** (TMM, DSSM, CWC, rabbit, fluentd, the operator, etc.) — so the BOM needs no chart-rendering, just a YAML parse.
- **cert-manager and a node-labeler are already IN the manifest, F5-packaged.** `charts/f5-cert-manager` + `images/cert-manager-{cainjector,controller,webhook,startupapicheck}` (tag `v2.6.2`), and `charts/node-labeler` + `images/f5-node-labeler`. But `roksbnkctl` today installs the **non-F5** equivalents instead — cert-manager from `charts.jetstack.io` (`modules/cert_manager/modules/cert-manager/variables.tf:34`) + its quay.io images, and `bitnami/kubectl:latest` for the node-labeler Job (`modules/flo/modules/flo/main.tf:1296`). Those two are the *only* artifacts the manifest doesn't cover. The air-gap-correct path is therefore not "mirror Jetstack + Docker Hub too" but **retarget the install onto the manifest's F5-packaged cert-manager + node-labeler** (Design §1; Open Question 6) — leaving the manifest the single complete BOM with zero external registries.
- **FAR auth.** `_json_key_base64` + a service-account JSON unpacked from a COS tarball (`f5-far-auth-key.tgz`, `modules/flo/modules/flo/main.tf:168-207`). Reused as the replication **source** credential.
- **No container-registry client exists.** `internal/ibm/` has IAM + resource-controller + raw-REST only. The replication engine is a new dependency (go-containerregistry / crane + ORAS).
- **The "registry COS" is not a registry.** `create_roks_registry_cos_instance` makes a COS *object-storage* instance (FAR auth + JWT + the OpenShift registry's backing store) — `modules/roks_cluster/modules/cluster/main.tf:237`. It is not the mirror target.
- **The OpenShift internal registry has two faces.** An in-cluster service `image-registry.openshift-image-registry.svc:5000` (what pods pull from) and an external route (off by default; `defaultRoute=true` exposes it). Pods pull via RBAC (`system:image-puller`), no dockerconfigjson.

## Design

### 1. The bill-of-materials (BOM) — `internal/bnkbom`

Pull `f5-bigip-k8s-manifest@<version>` (via the FAR OCI source client, §3) and parse its `bigip-k8s-manifest-<version>.yaml`. Confirmed against the real 2.3.0 manifest (Background), this is a **direct ~20-line YAML parse, not a render**: the file lists every chart (`helm_charts[]`, 25) and every image (`docker_images[]`, 56) as `{name, version}` under `releases[]`, with `f5_helm_repo`/`f5_docker_repo` as the source hosts. Map each into a typed `Artifact{Kind, Repo(=source host), Name(=charts/… | images/… | utils/…), Tag(=version)}`. Images are tag-pinned, so resolve tag→digest at copy time (§3) and record the digest in the mirror record for immutability/verify. The result is a deterministic, version-pinned `BOM` of ~81 artifacts — the complete air-gap install set. `roksbnkctl registry bom` prints it (json / table).

The manifest covers only F5's components. `roksbnkctl` additionally installs **non-F5 deps** the BOM must union in — **decision (Open Question 6): keep these, don't retarget** onto the manifest's F5-packaged equivalents (`charts/f5-cert-manager`, `charts/node-labeler`), so install behavior is unchanged. They are: the **Jetstack cert-manager** chart (`charts.jetstack.io` — a *classic* Helm HTTP repo, not OCI) + its **quay.io** images, and **`bitnami/kubectl`** from **Docker Hub**. Versions come from the tfvars `roksbnkctl` already renders (`cert_manager_version` + the node-labeler tag); cert-manager's image set is harvested by rendering its chart (or a version-pinned known-image list — it's the one place a chart render is still needed), and `bitnami/kubectl` is a single image. So the full BOM = the 81 manifest artifacts ∪ {cert-manager chart + ~5 quay.io images, `bitnami/kubectl`}, and `--include-deps` (default on) controls the union. The cost is three external source registries (`charts.jetstack.io`, `quay.io`, `docker.io`) remaining in the *replication* source set — they are mirrored once into the target, after which the *install* still pulls only from the target. (The F5-packaged equivalents remain a future simplification.)

### 2. The `RegistryTarget` abstraction — `internal/registry`

```go
type Artifact struct { Kind ArtifactKind; Repo, Name, Tag, Digest string } // Kind ∈ {Image, Chart}

type RegistryTarget interface {
    Prepare(ctx context.Context) error          // ensure namespace/repo, route, push identity, pull RBAC
    PushEndpoint() (host string, auth authn.Authenticator, err error) // host-reachable, for replication
    ImagePullRef(a Artifact) string             // cluster-reachable, for pods/kubelet
    ChartPullRef(a Artifact) string             // host-reachable, for the in-process helm provider
}
```

ICR and generic registries collapse all three to one host. The **OpenShift internal registry** impl (`internal/registry/openshift`) is what forces the abstraction to be honest:

- **`Prepare`**: set `defaultRoute=true` on `configs.imageregistry.operator.openshift.io/cluster`; read the route host; ensure the mirror namespace/project; mint a push token (a ServiceAccount with `registry-editor`); bind `system:image-puller` to the BNK namespaces' ServiceAccounts so pods pull without a secret.
- **`PushEndpoint`** → the route host + token.
- **`ChartPullRef`** → `oci://<route>/<ns>/charts/<name>` — the in-process helm provider pulls over the route with token auth, the same shape as today's FAR OCI pull (`repository_username`/`repository_password`).
- **`ImagePullRef`** → `image-registry.openshift-image-registry.svc:5000/<ns>/images/<name>@<digest>` — the kubelet's path.

### 3. The replication engine — `internal/registry/mirror`

For each BOM artifact, copy `<source>/<path>` → `PushEndpoint()/<ns>/<path>`: `crane.Copy` (by digest) for images, ORAS for OCI charts. **Idempotent** (skip when the target digest already matches), **concurrent** (bounded worker pool), **resumable**. **Sources are heterogeneous**, so the engine dispatches on source type:
- `repo.f5.com` (the 81 manifest artifacts) — OCI→OCI, source auth = the FAR `_json_key_base64` credential (reuse the COS-tarball path, or a `registry.source` config cred).
- `quay.io` (cert-manager images) + `docker.io` (`bitnami/kubectl`) — OCI→OCI, anonymous/public source.
- `charts.jetstack.io` (the cert-manager chart) — a **classic Helm HTTP repo, not OCI**, so it is `helm pull`ed and **repackaged as an OCI artifact** on push (the one non-OCI source path).

Destination auth = `PushEndpoint()`. `verify` re-reads each target digest; `diff` = BOM − target; `prune` deletes target tags absent from the BOM.

### 4. CLI surface — `roksbnkctl registry …`

| verb | behavior |
|---|---|
| `bom` | print the resolved bill-of-materials (the air-gap install set) — `--json` |
| `replicate` | mirror the BOM → target. `--target openshift` (default), `--dry-run`, `--concurrency N`, `--include-deps` (default on) |
| `list` | what is present in the target (and/or the BOM) |
| `diff` | BOM vs target — what is missing or stale |
| `verify` | every BOM artifact present in the target with a matching digest |
| `prune` / `rm` | delete mirrored artifacts not in the current BOM |

CRUD-shaped, mirroring the IBM COS client's verb model: `replicate` ≈ create/update, `list`/`diff`/`verify` ≈ read, `prune` ≈ delete.

### 5. Install redirection

A new optional **Registry phase** that runs the mirror and writes a `registry-mirror.json` record into the workspace (sibling of `cluster-outputs.json`) carrying the chart host (route), image host (svc), namespace, and per-artifact digests. When a mirror record is present, the BNK phase (rendered through `internal/tf/vars.go`) does the following:

- splits the today-single `far_repo_url` into a **chart host** (route → the `helm_release.repository` for FLO/CIS and the manifest pull) and an **image host** (svc → `image.repository` in the chart values and the CNEInstance `spec.registry.uri`);
- replaces `far-secret` for image pulls with the `system:image-puller` RBAC the target prepared (keeping a token secret only for the host's chart pulls over the route);
- redirects the non-F5 deps: cert-manager (`cert_manager_repository`/version → the mirror) and the node-labeler image (`bitnami/kubectl` → the mirror).

No new manual tfvars — it all renders from the mirror record.

### 6. Auth

- **Source (FAR):** reuse `_json_key_base64` + the COS-tarball service account (or a `registry.source` credential in config).
- **Target (OpenShift internal):** the cluster kubeconfig (already in hand) → enable the route, mint the push-SA token, set the pull RBAC. No external IBM service is involved.

### 7. Replication locus — client-side host-bridge

`roksbnkctl` runs the copy itself: it holds the FAR credential and reaches the cluster's registry route. An in-cluster copy job would require the *cluster* to reach FAR, which defeats the air-gap — so client-side is the only model that fits the requirement, and it matches the COS-client analogy. (An in-cluster job is deferred for network-restricted targets where even the host cannot reach the registry route.)

### 8. Phase wiring

`registry replicate` runs after the Cluster phase and before BNK; it is **not** part of the composite `up` — a deliberate, occasional supply-chain step, like `gateway`. `up`/`bnk up` error with a clear message (pointing at `registry replicate`) if BNK is configured for a mirror that `registry verify` reports as incomplete.

## Staged delivery

1. **BOM builder** (`internal/bnkbom`) + `registry bom` + tests (manifest parse, non-F5 dep union, version pinning).
2. **FAR OCI source client + replication engine** (crane / ORAS) + `registry list` / `diff` / `verify` against a fake in-process registry.
3. **`RegistryTarget` + OpenShift internal-registry impl** (route enable, push SA, pull RBAC) + `registry replicate` / `prune`.
4. **Install redirection**: the `registry-mirror.json` record + the `far_repo_url` chart-host/image-host split + cert-manager/bitnami redirect in `internal/tf/vars.go`; the BNK-phase consume + the `up` guard.
5. **Docs**: book chapter (air-gapped install), configuration reference (`registry:` block), command-reference regen.
6. **Gated-live air-gap verify**: replicate on a real ROKS cluster, then `bnk up` with all egress to `repo.f5.com` / `quay.io` / `docker.io` blocked → licensed, Ready TMM.

## Acceptance (in-scope subset)

- `registry bom` lists every F5 chart/image + cert-manager + `bitnami/kubectl`, version-pinned and reproducible across runs.
- `registry replicate --target openshift` mirrors the full BOM into the cluster's internal registry; `registry verify` is green.
- With **all external registry egress blocked**, `roksbnkctl bnk up` installs BNK to a licensed, Ready TMM, pulling only from the internal registry.
- After a BNK version bump, `registry diff` shows exactly the changed artifacts; `registry prune` removes the superseded ones.

## Open questions

**Resolved / decided during planning:**

- *Image-list completeness* — **resolved** by inspecting the real `2.3.0-3.2598.3-0.0.170` manifest: it lists all 25 charts + 56 images by tag in a flat `helm_charts[]`/`docker_images[]` structure, so the BOM is a pure-manifest YAML parse, no chart rendering. Residual risk: an image the FLO operator pulls at reconcile time that the manifest omits — the gated-live air-gap test (§Staged delivery 6) catches it as `ImagePullBackOff`.
- *cert-manager / node-labeler sourcing* — **decided: keep the non-F5 deps** (Jetstack cert-manager, `bitnami/kubectl`) and mirror them as separate non-manifest artifacts (Design §1, §3), rather than retargeting onto the manifest's F5-packaged `f5-cert-manager` / `node-labeler`. No install-behavior change; the cost is three external source registries (`charts.jetstack.io`, `quay.io`, `docker.io`) in the *replication* set (the *install* still pulls only from the target). The F5-packaged equivalents remain a future simplification.

**Still open:**

1. **CNEInstance pull without a dockerconfigjson** — confirm the CNE controller pulls from the internal registry via `system:image-puller` RBAC alone; its `spec.registry.imagePullSecrets` currently requires an entry, so a token-backed secret may still be needed even on the internal registry.
2. **Chart OCI over the route** — confirm the OpenShift registry serves helm OCI artifacts cleanly to the in-process helm provider over the external route, including TLS trust for the router cert from the `roksbnkctl` host.
3. **Mirror namespace model** — one shared project for all mirrored artifacts vs. mirroring into the BNK namespaces directly (RBAC simplicity vs. lifecycle coupling on `bnk down`).
4. **Registry phase state** — pure imperative step with a `registry-mirror.json` record (leaning this way — it is a copy, not infrastructure) vs. a terraform `state-registry/`.
