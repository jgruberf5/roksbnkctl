# Sprint 27 — staff issues (native Go BNK-phase reconciler — retire terraform null_resource/local-exec/curl)

> **Surfaced 2026-06-04** as an integrator architecture request. The BNK
> phase of deployment is, today, mostly terraform driving the cluster
> through `null_resource` + `local-exec` running `helm` and **raw `curl`
> server-side-apply**, with readiness gated by static `time_sleep` blocks
> (30s/60s) and a couple of hand-rolled `curl` retry loops. The FLO module
> alone (`terraform/modules/flo/modules/flo/main.tf`) is ~1,200 lines of
> this. Nothing watches real status — it's "apply, sleep, hope," which is
> the source of the BNK phase's flakiness and the bulk of its complexity.
>
> roksbnkctl already has the Go tooling to do this properly:
> `internal/k8s` ships a client-go v0.30 **dynamic client + server-side
> apply** (`internal/k8s/apply.go`, field-manager `roksbnkctl`),
> get/list/delete/logs/exec, and per-workspace kubeconfig handling. This
> sprint replaces the terraform K8s interactions with a **native,
> watch-driven reconciler inside roksbnkctl** that applies resources and
> **watches actual status conditions** (CRD Established, Deployment Ready,
> CNEInstance/License/Certificate `.status`) with context deadlines instead
> of fixed sleeps, reporting live progress back to the operator.
>
> **Primary goal: SPEED.** The watch-based design is not just for
> robustness — it exists to make the BNK phase **dramatically faster** so an
> operator can deploy a new BNK version and test it in a tight iteration
> loop. Today's terraform path burns **~210s in pure `time_sleep`** alone
> (cert 30 + FLO SCC 30 + FLO pods 60 + CNE CRD 30 + CNE SCC 30 + License CRD
> 30), on top of helm `--wait` slack, `curl` retry backoffs, and the whole
> terraform init/plan/apply/provider-init overhead — almost all of it dead
> waiting even when the cluster is ready in seconds. The native reconciler
> must (a) return the instant a watched condition is met (zero fixed-sleep
> slack), (b) run independent steps **concurrently**, and (c) offer a **fast
> re-deploy** path that reconciles only the delta on a version bump. Treat
> wall-clock as a first-class acceptance metric, not an afterthought.

`Status: open` (not yet dispatched).

### Locked decisions (integrator, do NOT relitigate)

- **In-CLI reconciler, NOT an in-cluster operator.** roksbnkctl's own
  binary drives the BNK install during `bnk up` and tears it down on
  `bnk down`. No new operator image, CRD, or RBAC to publish. (BNK's actual
  CNE lifecycle is already reconciled in-cluster by FLO; this sprint only
  replaces the terraform *bootstrap* of cert-manager + FLO + the CRs.)
- **Reuse `internal/k8s`** (dynamic SSA, field-manager `roksbnkctl`) for
  every CR/Secret/RBAC/NAD/Job apply. Do not reintroduce `curl` or shelling
  to `kubectl`.
- **Keep non-K8s resources in terraform.** The IBM IAM trusted-profile /
  link / policy in the FLO module (`flo/main.tf` ~1159-1191) and the COS
  JWT/FAR-auth reads are IBM Cloud API, not Kubernetes — leave them in
  terraform (a later sprint may move them to `internal/ibm`).
- **Helm stays in terraform (RESOLVED, integrator 2026-06-04).** Keep the
  three chart installs (cert-manager, f5-lifecycle-operator, f5-bnk-cis) in
  terraform, but **convert them from `null_resource` + `local-exec helm` to
  the proper `helm_release` provider** so terraform waits on real chart
  readiness (`wait = true`) instead of `--wait=false` + `time_sleep`. **No
  `helm.sh/helm/v3` Go dependency.** Native Go replaces ONLY the `curl`-
  applied custom-resource parts + their `time_sleep` gates. The FAR
  version-discovery (`helm pull f5-bigip-k8s-manifest`, parse FLO/CIS
  versions) stays terraform-side too (it feeds the `helm_release` versions).

---

## Issue 1 — Watch/wait primitives in `internal/k8s`

**Severity**: medium (foundational capability the reconciler depends on)
**Status**: open

Today `internal/k8s` has SSA apply + get/delete but **no watch, informer, or
rollout-status machinery** (confirmed). Add a small, well-tested wait layer
the reconciler builds on. Build on the existing `BuildDynamicClient` /
`Clientset` / `RESTConfig` in `internal/k8s/client.go`.

`internal/k8s/wait.go` (new):
- `WaitCRDEstablished(ctx, names ...string) error` — watch
  `apiextensions.k8s.io/v1 CustomResourceDefinition` until each has
  condition `Established=True` (replaces every `time_sleep` that waits for
  a CRD to register).
- `WaitDeploymentReady(ctx, ns, name)` / `WaitRolloutComplete` — watch a
  Deployment/DaemonSet until `availableReplicas == desired` &
  `observedGeneration` current (replaces helm `--wait` and the FLO/CIS pod
  sleeps).
- `WaitResourceCondition(ctx, gvr, ns, name, condType, want string)` —
  generic dynamic-client watch on an arbitrary object's
  `.status.conditions[type==condType].status == want` (drives Certificate
  `Ready`, ClusterIssuer `Ready`).
- `WaitResourceJSONPath(ctx, gvr, ns, name, jsonpath, predicate)` — generic
  wait on a `.status` field for CRs whose readiness isn't a standard
  condition (CNEInstance `.status.phase`, License `.status.*`). Confirm the
  exact ready signal with the architect's CRD-schema notes.
- `WaitJobComplete(ctx, ns, name)` — watch a Job to `Complete` (node-labeler).
- All use `watch.Interface` via the dynamic/typed client with a
  `ctx`-derived deadline; on context expiry return an actionable timeout
  naming the resource + the last-seen status (NOT a bare "timed out").
  Prefer `cache`/`watchtools.UntilWithSync` (client-go's `tools/watch`) over
  hand-rolled loops where it fits.

## Issue 2 — `internal/bnk` reconciler package

**Severity**: high (the core of the sprint)
**Status**: open

This sprint splits the BNK phase into a **terraform install layer** and a
**native Go custom-resource layer** with a clean handoff (terraform applies
the installs, then the Go reconciler runs post-apply). The architect pins the
exact boundary (`issue_sprint27_architect.md`) — which currently-`curl`'d
resources are helm *prerequisites* (stay terraform) vs *post-install* CRs
(move to Go). Port the resource bodies from the terraform modules — they are
the behavior spec.

**Terraform install layer** (staff converts the HCL; no `curl`, no
`time_sleep` — real provider waits):
- cert-manager / f5-lifecycle-operator / f5-bnk-cis → `helm_release` with
  `wait = true` (replaces `null_resource local-exec helm --wait=false` +
  `time_sleep`). FAR version-discovery stays terraform-side (feeds the
  release versions).
- The helm *prerequisites* that must exist before the charts install — the
  `f5-utils`/`flo` namespaces and the `far-secret`/`f5-bigip-ctlr-login`
  secrets (image-pull + CIS login) — move from `curl` to the terraform
  `kubernetes` provider (`kubernetes_namespace`/`kubernetes_secret`) so
  terraform owns the full install DAG. (Per architect's boundary — these are
  not "CRs", and a missing image-pull secret at pod-create makes `helm_release
  wait=true` block; so they must precede the charts.)

**Native Go custom-resource layer** (`internal/bnk`, runs post-apply against
the workspace kubeconfig; each step **apply (SSA) → watch to ready**) — the
genuinely-brittle parts terraform applies via `curl` then sleeps on:
1. cert-manager CRs: self-signed `ClusterIssuer` + `ext-ca` `Certificate` +
   CA `ClusterIssuer` → watch Certificate `Ready` (consumed at FLO *runtime*,
   not at helm-install time — so post-handoff is safe; confirm with architect).
2. The two `NetworkAttachmentDefinition`s (`ens3-ipvlan-l2`, `macvlan-conf`).
3. The privileged-SCC `ClusterRoleBinding`s (FLO + CIS + the per-SA set).
4. node-labeler SA/Role/Binding/`Job` → `WaitJobComplete`.
5. **CNEInstance** (`k8s.f5.com/v1`, port spec from `cneinstance/main.tf`
   ~79-207) → watch `.status` to ready (replaces `curl PATCH` + two 30s
   sleeps).
6. **License** (`k8s.f5net.com/v1`, JWT from COS via terraform output /
   `internal/cos`) → retry transient webhook 4xx/5xx, watch `.status`
   (replaces the `curl` poll+retry loop + 30s sleep).

Design notes:
- A `Reconciler` struct holding the `*k8s.Client` + a `ProgressReporter`
  interface (so the CLI prints live and tests assert on emitted events).
- Each step is idempotent (SSA + "already ready" short-circuit) so a
  re-run after a partial failure converges — the watch-based equivalent of
  terraform's "apply again."
- Values that flowed between terraform modules (namespaces, cluster-issuer
  name, network attachments, FAR creds, trusted-profile id, manifest
  version) become reconciler inputs; source the IBM-side ones
  (trusted-profile id) from terraform outputs / `internal/tf` output read,
  the kubeconfig from the workspace state dir
  (`internal/tf/terraform.go` KubeconfigDir).
- **Teardown**: a reverse-order `Destroy(ctx)` that deletes License →
  CNEInstance → helm releases → secrets/NADs/issuers → namespaces, watching
  deletions to completion (replaces the destroy-time provisioners + the
  finalizer-strip dance, which becomes a real wait, not a sleep).

## Issue 3 — Speed: parallelism, fast re-deploy, and timing instrumentation

**Severity**: high (this is the primary motivation — see frame)
**Status**: open

The reconciler must be built for speed from the start, not optimized later:

- **Concurrency.** Model the resources as a dependency DAG (architect ships
  the safe-parallelism DAG) and execute independent nodes **concurrently**
  via `golang.org/x/sync/errgroup` (already an indirect dep — confirm).
  Examples of safe parallel work: create both namespaces at once; apply the
  two NADs + the three FAR/login secrets + the self-signed issuer
  concurrently; run the node-labeler Job in parallel with the FLO/CIS helm
  installs where ordering allows; watch independent Deployments concurrently.
  Only serialize true dependencies (cert-manager CRDs before issuers; FLO
  before CNEInstance before License).
- **Zero fixed-sleep slack.** Every wait returns the instant its watched
  condition is true. No `time.Sleep` as a readiness gate anywhere. Short-
  circuit already-satisfied conditions (CRD already Established, Deployment
  already Ready, CR already at desired status) so re-runs skip instantly.
- **Fast re-deploy / version-bump path.** A `Reconcile` that diffs desired
  vs live and acts only on deltas, so bumping `f5_bigip_k8s_manifest_version`
  re-pulls + re-rolls only what changed (CNEInstance spec + the rolled
  Deployments) rather than re-running the whole bootstrap. SSA makes the
  apply idempotent; the win is skipping already-converged steps and not
  re-pulling unchanged charts. Cache the FAR OCI pull keyed by version so an
  unchanged version skips the network round-trip.
- **Warm clients.** Build the dynamic/typed client, discovery, and REST
  mapper once per reconcile and reuse them (the `internal/k8s` apply path
  rebuilds the mapper per call today — reuse it across steps here).
- **Timing instrumentation.** Record + report per-phase wall-clock durations
  (emit them through the `ProgressReporter`), so the operator sees where time
  goes and the speedup is measurable/provable. `bnk status` / a `--timings`
  summary prints the breakdown.

Where the speed comes from under the terraform-keeps-helm split (honest
accounting): (1) the **~210s of fixed `time_sleep` is removed entirely** —
the cert/FLO/CIS sleeps become `helm_release wait=true` real-readiness waits
(terraform-side), and the CNE/License/Job sleeps become Go watches; (2) the
Go custom-resource layer **parallelizes** independent steps and short-circuits
already-ready ones; (3) the **fast re-deploy** path skips converged steps.
Terraform DOES remain in the critical path for the install layer (its
init/plan/apply overhead + the `helm_release` waits are not eliminated), so
the win is concentrated in killing the dead sleeps + the Go CR-layer
watch/parallelism, not in removing terraform. Target (refine with the
integrator after the first live run): materially faster wall-clock than the
current path, with the 210s of sleep gone as the floor. The validator's
gated-live timing comparison (`issue_sprint27_validator.md`) measures native
vs the legacy baseline.

## Issue 4 — Orchestration + CLI wiring, status surface, terraform gating

**Severity**: high
**Status**: open

- **Seam**: in `internal/orchestration/lifecycle.go:RunTrialUp`, after the
  cluster-shared terraform apply, when the native path is enabled, run the
  `internal/bnk` reconciler against the workspace kubeconfig instead of
  letting terraform deploy the BNK modules. Mirror the existing post-apply
  hook pattern (`tryAutoKubeconfig`).
- **Terraform gating**: drive the bnk-phase terraform so it provisions only
  cluster-shared infra + the IBM IAM trusted profile — set `deploy_bnk=false`
  and `deploy_cert_manager=false` for the native path (the toggles already
  exist in `terraform/variables.tf`). The native reconciler owns everything
  the four k8s modules did. No half-managed namespace/CR across both systems.
- **`bnk down`**: route to `Reconciler.Destroy` for the native path
  (`internal/cli/bnk_phase.go`).
- **Status surface** ("report back status"): live progress to stderr during
  `bnk up` (phase + per-resource condition, modeled on the existing
  `→ … / ✓ …` style in lifecycle.go), AND a `roksbnkctl bnk status` command
  that builds the k8s client and reports the live `.status` of cert-manager,
  FLO, CNEInstance, and License on demand (read-only; reuses the wait
  layer's status reads without blocking).
- A feature flag / config gate (e.g. `bnk.native_k8s: true` in the workspace
  config, or a `--native`/`--legacy-tf` flag) so the terraform path remains
  available until the native path is live-verified — supports the "don't
  merge to main until confident" constraint and an A/B during bring-up.

### Scope guards
- Don't touch the IBM-Cloud (non-k8s) terraform resources.
- Don't reintroduce `kubectl`/`curl`/`helm` shell-outs from Go (helm, if
  used, is the Go SDK per architect).
- Keep the legacy terraform BNK path working behind the flag this sprint.
- Tests are validator's surface — no `_test.go` from staff.

### Files affected
- **New**: `internal/k8s/wait.go`, `internal/bnk/*.go` (reconciler, steps,
  reporter), `internal/cli/bnk_status.go`.
- `internal/orchestration/lifecycle.go`, `internal/orchestration/second_phase_reuse.go`
  (gating), `internal/cli/bnk_phase.go` (native up/down wiring + flag).
- `internal/config/workspace.go` (native toggle, if config-driven).
- `go.mod` — **no `helm.sh/helm/v3`**; only any client-go `tools/watch` +
  `golang.org/x/sync/errgroup` (likely already indirect) usage.
- **Terraform**: a `helm_release`-based install layer for cert-manager +
  FLO + CIS (with `wait = true`) and `kubernetes_namespace`/`kubernetes_secret`
  for the helm prerequisites — structured (per architect) as a flag-selected
  config so the **existing `curl`-based modules stay intact as the legacy
  baseline** the validator benchmarks against. The native path's terraform
  applies only the install layer + IBM IAM; the Go reconciler owns the CRs.
  A later sprint removes the legacy curl modules once native is the default.

### Acceptance criteria
1. `bnk up --native` against a real cluster brings up cert-manager → FLO →
   CNEInstance → License with **no `time_sleep`-equivalent fixed waits** —
   every gate is a watch on a real condition.
2. **Speed**: independent steps run concurrently; per-phase timings are
   reported; the warm-cluster native `bnk up` wall-clock is **materially
   faster** than the terraform path (validator measures the delta — target
   set with the integrator after the first live run, but recovering the
   ~210s of pure sleep is the floor).
3. **Fast re-deploy**: a version-bump re-run reconciles only the delta
   (no full re-bootstrap, no re-pull of unchanged charts) and is markedly
   faster than a cold `up`.
4. Live progress prints per phase; `bnk status` reports current `.status` of
   all four layers.
5. A mid-run failure (e.g. CRD slow) waits on the watch up to a ctx deadline
   and reports an actionable timeout naming the resource + last status, not a
   silent sleep-then-fail.
6. `bnk down --native` removes everything in reverse order, watching deletes.
7. Legacy terraform path still works behind the flag.
8. `go build ./...`, `go vet ./...`, `staticcheck ./...` clean.

### Related
- `issues/issue_sprint27_architect.md` — helm decision, CRD ready-signal
  schemas, the watch-helper API design, book authoring.
- `issues/issue_sprint27_validator.md` — fake/dynamic-client hermetic tests +
  gated-live e2e.
- `terraform/modules/{cert_manager,flo,cne_instance,license}/` — the spec
  being ported.
- `internal/k8s/apply.go` (SSA), `internal/k8s/client.go` (clients) — reused.
- Integrator memory [[live-verify-high-issues]] — this is `up`-affecting and
  cluster-mutating; live verify gates closure.
