# Sprint 29 — validator issues (air-gap registry mirror: BOM/replication unit gates + the gated-live air-gap verify)

> **Sprint 29 frame.** Verify that `roksbnkctl registry` replicates the full BNK
> bill-of-materials from FAR into the OpenShift internal registry, and that BNK
> installs from the mirror with **all external registry egress blocked**. Staff
> builds (`issue_sprint29_staff.md`); architect designs
> (`issue_sprint29_architect.md`). Spec: [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md).
> The gated-live air-gap verify (Issue 4) is the acceptance gate.

`Status`: open (draft — not yet dispatched)

---

## Issue 1 — BOM correctness (unit)

**Severity**: high
**Status**: open

- Parse the real `bigip-k8s-manifest-2.3.0-3.2598.3-0.0.170.yaml` fixture →
  exactly 25 charts + 56 images, names/tags intact, paths classified
  (`charts/`|`images/`|`utils/`).
- The non-F5 dep union adds the Jetstack cert-manager chart + its quay.io images
  + `bitnami/kubectl`, version-keyed off `cert_manager_version`/the node-labeler
  tag. `--include-deps=false` drops them.
- Determinism: two `registry bom` runs are byte-identical.

## Issue 2 — Replication engine (unit / fake registry)

**Severity**: high
**Status**: open

- Copy → a fake in-process OCI registry: idempotent (second run is a no-op by
  digest), concurrent (bounded), resumable (interrupt + resume completes).
- Heterogeneous sources: OCI→OCI for repo.f5.com/quay.io/docker.io; the
  classic-Helm `charts.jetstack.io` chart is pulled + repackaged as OCI.
- `verify` flags a deleted/mismatched target artifact; `diff` = BOM − target;
  `prune` removes a target tag absent from the BOM.

## Issue 3 — RegistryTarget / OpenShift impl (live, non-air-gap)

**Severity**: high
**Status**: open

- `Prepare` on a real ROKS cluster (test-003): `defaultRoute` enabled, route
  reachable from the host, push SA can push, BNK SAs get `system:image-puller`.
- `registry replicate --target openshift` mirrors the full BOM; `registry verify`
  green; the route serves the charts to a `helm pull` from the host (Open
  Question 2).

## Issue 4 — Gated-live air-gap install (ACCEPTANCE)

**Severity**: high (the whole point)
**Status**: open

- On a real ROKS cluster: `registry replicate`, then **block all egress** to
  `repo.f5.com`, `quay.io`, `docker.io`, `charts.jetstack.io` (NetworkPolicy /
  node egress), then `roksbnkctl bnk up`.
- PASS = a licensed, Ready TMM with every pod's image pulled from
  `image-registry.openshift-image-registry.svc:5000/...` — zero external pulls.
- Catch the completeness gap (an operator-reconciled image missing from the BOM)
  as `ImagePullBackOff`; feed any miss back to staff/architect (Issue 1 backstop).
- Confirm Open Question 1 live: does the CNE controller pull via RBAC alone, or
  is a token secret needed?

### Scope guards
- Unit gates need no live cluster (fake registry + the manifest fixture).
- The air-gap egress block must be real (NetworkPolicy/egress), not a config flag.

### Acceptance criteria
1. BOM + replication unit gates green (Issues 1-2).
2. Live replicate + verify green on test-003 (Issue 3).
3. **Air-gap `bnk up` reaches licensed Ready TMM with zero external image pulls** (Issue 4).
4. `gofmt`/`vet`/`staticcheck`/`go test ./...` clean.

### Files affected
- `internal/bnkbom/*_test.go`, `internal/registry/**/*_test.go`, a manifest
  fixture under `internal/bnkbom/testdata/`.
- A gated-live script (`scripts/e2e-airgap-mirror.sh`) — the egress-blocked verify.

### Related
- [PRD 11](../docs/prd/11-REGISTRY-MIRROR.md) §Acceptance · `issue_sprint29_staff.md`
  · `issue_sprint29_architect.md` (Open Questions 1-2 resolved live here).
