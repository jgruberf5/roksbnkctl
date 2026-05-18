# Sprint 9 — staff issues (resolved)

## Issue 1: tools-ibmcloud Dockerfile needed `USER 1000`
**Status**: resolved — Dockerfile patched
**Resolution**: as filed during sprint. The k8s integration test image switch (busybox:1.36 → tools-ibmcloud) requires the tools image to run as a non-root uid to satisfy `runAsJob`'s `RunAsNonRoot: true` SecurityContext. `tools/docker/ibmcloud/Dockerfile` now ends with `USER 1000`. Will land in the next tools-images CI build alongside the v1.2.0 tag.

## Issue 2: runOnOpsPod's ibmcloud login wrap still uses $IBMCLOUD_API_KEY
**Status**: deferred to Sprint 10
**Resolution**: real follow-up work. The Sprint 9 implementation provisions the trusted profile and annotates the SA, but `runOnOpsPod`'s existing login wrap (`internal/exec/k8s.go::runOnOpsPod`) still does `ibmcloud login --apikey "$IBMCLOUD_API_KEY"` rather than `ibmcloud login --trusted-profile-id "$IAM_PROFILE_ID"` (the trusted-profile-aware shape). For now the trusted profile is provisioned but the runtime login still uses the static-key path injected via the Secret — the security benefit (api key not in `docker inspect` for the docker backend; api key not in Kubernetes Secret for ops install --trusted-profile=auto when no fallback) is partial. Full closure needs the login wrap to switch on whether the pod has `iam.cloud.ibm.com/trusted-profile` annotation and inject `IAM_PROFILE_ID` accordingly. Sprint 10 candidate; documented in CHANGELOG `### Deferred` for v1.2.0.

## Issue 3: pre-existing `M` WIP files
**Status**: resolved — no action needed
**Resolution**: as filed; the WIP files referenced in the prompt context were already clean at staff dispatch.

## Issue 4: k8s integration test live-verify
**Status**: deferred to validator + integrator
**Resolution**: validator covered the binary-surface verification; the live kind-cluster end-to-end verify (which requires the tools-ibmcloud image rebuilt with `USER 1000`) is integrator-owned — runs after the next tools-images CI build publishes the updated image to ghcr.io.
