# Sprint 29 — architect, resolved design

Resolutions for the open questions in [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md)
and `issue_sprint29_architect.md`. The BOM model (Issue 1), the `RegistryTarget`
contract (Issue 2), and the install-redirect shape (Issue 4) are pinned by the
shipped code (`internal/bnkbom`, `internal/registry/openshift`,
`internal/config.RegistryMirror`). This file resolves the four open questions
(Issue 3).

## OQ1 — CNEInstance pull without a dockerconfigjson

**Decision: RBAC-first, with a token-secret fallback.** Pods pull the mirrored
images from `image-registry.openshift-image-registry.svc:5000/<ns>` via the
`system:image-puller` binding `Prepare` creates for the BNK namespaces' service
accounts — no `dockerconfigjson`. The CNEInstance `spec.registry.imagePullSecrets`
field, however, is currently *required* by the CR schema. Plan: render it as an
**empty list** and rely on RBAC; if the live verify (Stage 6) shows the controller
still demands a named secret, mint a long-lived token secret bound to the mirror
push/pull SA and reference it — a one-line addition to the redirect render. The
`far-secret` (FAR service-account dockerconfigjson) is **dropped** in mirror mode.

## OQ2 — helm-OCI over the route (TLS trust)

**Decision: rely on the ROKS apps-domain certificate; document the custom-CA
escape hatch.** The in-process helm provider pulls charts over the registry route
(`oci://<route>/…`). ROKS exposes the registry route under the cluster's
`*.apps.<domain>` ingress, which carries a publicly-trusted certificate by default,
so the `roksbnkctl` host trusts it with no extra config. For clusters with a custom
or self-signed ingress cert, the host must add the router CA to its trust store
(or the helm provider's `--ca-file`); the air-gap-install chapter documents this.
No code dependency either way — the existing helm-OCI pull pattern
(`repository_username`/`repository_password`) is reused, pointed at the route with
the push token.

## OQ3 — Mirror namespace model

**Decision: one shared mirror project (`bnk-mirror`), decoupled from BNK
lifecycle.** All mirrored artifacts live in a single project; `Prepare` binds
`system:image-puller` on it for the `f5-bnk` / `f5-utils` / `f5-app` service
accounts. The mirror **outlives** BNK redeploys — `bnk down` does not touch it (a
feature: re-`bnk up` against the same mirror with no re-replication). `registry
prune` / a future `registry rm` is the only thing that removes mirrored artifacts.
This favors RBAC simplicity + reuse over per-namespace coupling.

## OQ4 — Registry phase state

**Decision: imperative, with a `registry-mirror.json` record — no terraform
state.** Replication is a copy, not infrastructure terraform should own (no drift
to reconcile, no destroy graph). The phase runs the engine and writes
`internal/config.RegistryMirror` (shipped) as the durable handoff the BNK phase
reads — exactly mirroring the `cluster-outputs.json` pattern. There is no
`state-registry/`. `registry verify` re-derives state from the live target, so the
record is a convenience/handoff, not the source of truth.

## Consequent guidance for staff (Stage 4 terraform split)

The redirect (the remaining Stage 4 terraform-body work) renders from the record:
- `far_image_repo_url = <ImageHost>` → chart `image.repository` + CNEInstance
  `spec.registry.uri` (the in-cluster service path).
- `far_chart_repo_url = <ChartHost>` → the `helm_release.{flo,cis}` `repository`
  + the manifest-pull host.
- `use_registry_mirror = true` → flo module drops `far-secret`, sets
  `imagePullSecrets: []` on the CNEInstance, and points cert-manager
  (`cert_manager_repository`) + the node-labeler image at `<ImageHost>`.
This requires splitting the single `far_repo_url` variable in the root +
`modules/flo` + `modules/cne_instance` into the chart/image pair, defaulting both
to `far_repo_url` when no mirror is configured (zero behavior change off the
air-gap path).
