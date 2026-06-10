# PRD 14 — Registry targets: ICR (default) + generic OCI / Artifactory

> **Sprint 30 Issue 1.** PRD 11 (air-gap registry mirror) shipped one
> `RegistryTarget` impl — the **OpenShift internal registry** — and named ICR /
> generic OCI as "follow-on impls behind the interface." `registry replicate`
> defaults to `"openshift"` and hard-errors on any other target
> (`buildTarget`, internal/cli/registry.go:297: *"unsupported registry target …
> (only \"openshift\" is implemented)"*). Sprint 30 builds those follow-ons:
>
> 1. An **IBM Container Registry (ICR)** target, and make it the **default** for
>    `registry replicate`.
> 2. A **generic OCI registry** target (registry:2 / Harbor / **Artifactory**),
>    proven by a book walkthrough that replicates FAR into a private Artifactory.
>
> Config + flag plumbing lives in the same `registry` command group; the BOM
> engine and install-redirect are target-agnostic already (addressing lives in
> the target — the category-as-project lesson from PRD 11). Stages + design
> decisions: `issues/issue_sprint30_staff.md` / `issues/issue_sprint30_architect.md`.

`Status`: draft (not yet dispatched)

---

## Background — the target contract

The `mirror.Target` / `RegistryTarget` contract (PRD 11 §2) splits three
addresses so the OpenShift two-face registry stays honest:

- `Prepare(ctx, …)` — make the target ready (namespaces, auth, RBAC).
- `PushRef` / push endpoint — host-reachable, where `replicate` writes.
- `ImagePullRef` — cluster-reachable, what pods pull.
- `ChartPullRef` — host-reachable, what the helm provider pulls.

ICR and generic OCI **collapse** these to a single host (push == pull == chart),
which is the easy case; the contract already supports it. The work is the impl +
auth + the default switch, not the contract.

## Scope

### Default → ICR

- `buildTarget` default changes from `"openshift"` to `"icr"`. The workspace
  `registry.target` config field and the `--target` flag still override
  (openshift / icr / generic).
- Open Question 1: does defaulting to ICR require an ICR namespace/instance the
  user must pre-provision, and should `init` interview for it? An ICR target
  needs: a registry region host (`<region>.icr.io`, e.g. `de.icr.io`), a
  **namespace** (ICR's tenant unit), and auth (an IAM API key / `iamapikey`).
  Default assumption: **reuse the workspace's existing IBM API key**
  (`targets.<t>.api_key_b64`) as the ICR pull/push credential; the ICR namespace
  comes from a new `registry.icr_namespace` (default derived from `prefix`).
- Open Question 2: ICR pull-secret on the cluster — pods pull from
  `<region>.icr.io/<namespace>/…`, which needs an `iamapikey` pull secret in the
  BNK namespaces. Decide whether `replicate`/redirect provisions it (vs. relying
  on the global pull secret ROKS already wires for `*.icr.io`). **Likely the
  ROKS global `*.icr.io` pull secret already covers it** — confirm on a live
  cluster.

### Generic OCI target (Artifactory)

- A `"generic"` target keyed entirely off config: a host, a repository path
  prefix (namespace), and static auth (username/password or token). No
  cloud-provider assumptions — Artifactory, Harbor, and a bare `registry:2` all
  fit.
- Config shape (new under `registry:`): `generic_host`, `generic_repo_prefix`,
  and auth via an existing-style `*_b64` credential field or an env-overridable
  secret (ties into PRD 13 Issue 4).
- The install-redirect (`registry-mirror.json` → `far_image_repo_url` /
  `far_chart_repo_url`) already renders from the target's pull refs; the generic
  target just supplies `<host>/<prefix>` for both.

## Open questions (architect to resolve before dispatch)

1. ICR addressing + whether `init` must interview an ICR namespace; default
   namespace derivation from `prefix`.
2. ICR cluster pull secret — rely on ROKS global `*.icr.io` secret vs. provision.
3. Generic-target auth field shape + how it composes with PRD 13 env override.
4. Does flipping the default to ICR break the existing air-gap (openshift) flow
   for current users — do we need a migration note / explicit `target: openshift`
   in already-initialized workspaces? (Existing `config.yaml`s with no
   `registry.target` would switch from openshift→icr on upgrade.)

## Deliverable: book walkthrough — FAR → private Artifactory

A step-by-step book page (tech-writer ledger) covering: provision an Artifactory
OCI repo, set `registry.target: generic` + host/prefix/creds (or env override),
run `registry replicate`, and point a BNK install at the Artifactory mirror.

## Non-goals

- Implementing every cloud registry (ECR/GCR/ACR) — ICR + generic OCI cover the
  IBM-native and the bring-your-own cases.
