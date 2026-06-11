# Sprint 29 — architect issues (air-gap registry mirror: design the BOM, the RegistryTarget contract, the install redirect, resolve the open questions, book)

> **Sprint 29 frame.** The customer's ROKS environment forbids pulling install
> resources from external repositories at deploy time — every BNK artifact must
> be replicated into the cluster's own registry and installed from there.
> `roksbnkctl` pulls from FAR directly today (helm OCI + image pull-secrets, all
> keyed on `far_repo_url`) with no mirror path. Add a CRUD-shaped `registry`
> command group + an optional **Registry phase** (after Cluster, before BNK) that
> reads the `f5-bigip-k8s-manifest` into a bill-of-materials, mirrors every chart
> + image into a pluggable target (**OpenShift internal registry** first), and
> redirects the BNK install off `far_repo_url` onto the mirror. Staff
> (`issue_sprint29_staff.md`) owns the Go. This ledger owns the **design
> decisions staff codes against** + the operator prose. Full spec:
> [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md).

`Status`: resolved — shipped in v1.10.0 (2026-06-10)

---

## Issue 1 — The BOM model + the manifest contract

**Severity**: high
**Status**: open

The BOM is the spine. Planning already resolved the biggest unknown (PRD 11
Open Question 1, now closed): the real `f5-bigip-k8s-manifest-2.3.0-…​.yaml` is
a flat, complete enumeration — `f5_helm_repo`/`f5_docker_repo` + `releases[]`
with `helm_charts[]` (25) and `docker_images[]` (56), each `{name, version}`,
tag-pinned. So the BOM is a pure YAML parse, **no chart-rendering**. Pin:

1. The typed model — `Artifact{Kind ∈ {Chart,Image}, SourceHost, Name (charts/…|
   images/…|utils/…), Tag, Digest}` and the `BOM` aggregate. Digest is resolved
   at copy time and recorded for verify/immutability.
2. The **non-F5 dep union** (decision locked: keep, don't retarget — PRD 11 §1):
   Jetstack cert-manager (chart from `charts.jetstack.io` + its quay.io images,
   harvested by rendering the chart or a version-pinned known-image list) and
   `bitnami/kubectl`. Versions come from the tfvars `roksbnkctl` already renders
   (`cert_manager_version` + the node-labeler tag). Pin where the image list for
   cert-manager comes from (render vs. known-list) and how it's version-keyed.
3. The completeness backstop — an image the FLO operator pulls at reconcile that
   the manifest omits surfaces only at the gated-live air-gap test as
   `ImagePullBackOff`. Decide whether to add a pre-flight "operator image
   reference scan" or accept the gated-live test as the check.

## Issue 2 — The `RegistryTarget` contract + the OpenShift two-address model (BLOCKING)

**Severity**: high
**Status**: open

Pin the interface staff builds impls against, and the OpenShift impl's behavior.

- The 3-endpoint contract (PRD 11 §2): `Prepare`, `PushEndpoint` (host-reachable),
  `ImagePullRef` (cluster-reachable), `ChartPullRef` (host-reachable). ICR/generic
  collapse these to one host; the OpenShift impl is what forces the split — name
  the contract so it stays honest.
- The **OpenShift internal-registry** impl: `Prepare` enables `defaultRoute=true`
  on `configs.imageregistry.operator.openshift.io/cluster`, reads the route host,
  ensures the mirror namespace, mints a push ServiceAccount (`registry-editor`),
  and binds `system:image-puller` for the BNK namespaces' SAs. `ImagePullRef` →
  `image-registry.openshift-image-registry.svc:5000/<ns>/images/<name>@<digest>`;
  `ChartPullRef` → `oci://<route>/<ns>/charts/<name>`.

## Issue 3 — Resolve the four open questions (BLOCKING for Stages 3-4)

**Severity**: high
**Status**: open

PRD 11 §Open questions (still open). Resolve each, on a live ROKS cluster where
needed (test-003 is available):

1. **CNEInstance pull without a dockerconfigjson** — does the CNE controller pull
   from the internal registry via `system:image-puller` RBAC alone? Its
   `spec.registry.imagePullSecrets` currently requires an entry. If RBAC isn't
   enough, design the token-backed secret even on the internal registry.
2. **helm-OCI over the route** — does the OpenShift registry serve helm OCI
   artifacts cleanly to the in-process helm provider over the external route,
   including TLS trust for the router cert from the `roksbnkctl` host? Pin how the
   host trusts the route CA.
3. **Mirror namespace model** — one shared project for all mirrored artifacts vs.
   mirroring into the BNK namespaces directly (RBAC simplicity vs. lifecycle
   coupling on `bnk down`). Recommend one.
4. **Registry phase state** — imperative step with a `registry-mirror.json`
   record (leaning this way) vs. terraform `state-registry/`. Recommend one.

## Issue 4 — The install-redirect design

**Severity**: high
**Status**: open

The mirror is useless unless BNK installs from it. Design (PRD 11 §5):

- The `registry-mirror.json` record shape (chart host=route, image host=svc,
  namespace, per-artifact digests) — sibling of `cluster-outputs.json`.
- **Splitting the single `far_repo_url`** into a chart host (the helm_release
  `repository` + the manifest pull) and an image host (chart values
  `image.repository` + the CNEInstance `spec.registry.uri`). Today every ref
  flows from one var — specify the new rendered vars `internal/tf/vars.go`
  emits from the mirror record.
- Replacing `far-secret` for image pulls with `system:image-puller` RBAC (keep a
  token secret only for the host's chart pulls), and redirecting cert-manager
  (`cert_manager_repository`/version) + the node-labeler `bitnami/kubectl` image.
- The `up`/`bnk up` guard: error (pointing at `registry replicate`) when a mirror
  is configured but `registry verify` is incomplete.

## Issue 5 — Book authoring

**Severity**: low
**Status**: open

- New chapter: the air-gapped install — the Registry phase, the `registry` verbs
  (the CRUD/COS-client framing), the BOM, mirroring into the internal registry,
  and `bnk up` against the mirror with external egress blocked.
- The pluggable-target note (OpenShift first; ICR/generic later).
- Mark transcripts illustrative (tech-writer re-captures).

### Scope guards
- **Design + prose only** — no Go, no terraform-body changes. Don't relitigate
  Sprint 27/28.
- Recommend, don't relitigate, the locked decisions (pluggable/OpenShift-first;
  keep Jetstack+bitnami; client-side locus; charts in the registry).
- mdbook builds (docker image) clean.

### Acceptance criteria
1. BOM model + non-F5 dep-union sourcing pinned (Issue 1).
2. `RegistryTarget` contract + OpenShift two-address behavior pinned (Issue 2).
3. All four open questions resolved with a recommendation each (Issue 3).
4. Install-redirect design (the `far_repo_url` split, the mirror record, the
   `up` guard) specified (Issue 4).
5. Air-gap install chapter authored (Issue 5).

### Files affected
- This ledger / a `resolved_sprint29_architect.md` (the design).
- `book/src/**` (new air-gap-install chapter; `SUMMARY.md`).

### Related
- [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md) — the spec this implements.
- `issues/issue_sprint29_staff.md` — consumes this design.
- `terraform/variables.tf:233` (`far_repo_url`), `terraform/modules/flo/modules/flo/main.tf`
  (chart pulls, `far-secret`, version discovery 417-465), `modules/cne_instance/modules/cneinstance/main.tf:116`
  (`spec.registry.uri`), `internal/tf/vars.go` (the render to redirect).
- Sprint 27 (the `far_repo_url`-driven terraform-native refs) + Sprint 28 (the
  phase model the Registry phase joins).
