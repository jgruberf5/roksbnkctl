# Sprint 27 — staff issues (terraform-native BNK phase — retire null_resource/curl/sleep via helm_release + kubectl_manifest)

> **Surfaced 2026-06-04** as an integrator architecture request, **re-pivoted
> 2026-06-04** after the architect spike (GO — see
> `issue_sprint27_architect.md` "Spike rounds 1 & 2"). The BNK phase is, today,
> mostly terraform driving the cluster through `null_resource` + `local-exec`
> running `helm` and **raw `curl` server-side-apply**, with readiness gated by
> **~210s of static `time_sleep`** plus helm `--wait=false`/`curl` retry loops.
> The FLO module alone is ~1,200 lines of this; nothing watches real status.
>
> **The fix is terraform-native — no Go reconciler, no custom provider.** Keep
> terraform as the state keeper and replace the brittle parts with proper
> providers: chart installs → `helm_release` (`wait = true`); the `curl`-applied
> custom resources → **`alekc/kubectl` `kubectl_manifest` + `wait_for`** (which
> applies arbitrary CRs as real terraform resources, in state, with real
> `plan`/`destroy`/drift, AND watches `.status` to ready — crucially WITHOUT the
> plan-time-CRD requirement that breaks hashicorp `kubernetes_manifest`, the
> reason the originals used `curl`); namespaces + secrets → the `kubernetes`
> provider.
>
> **Primary goal: SPEED** — make a new BNK-version install deploy + test in a
> tight loop. The ~210s of fixed `time_sleep` dies outright (`wait_for` +
> `helm_release wait=true` are real-readiness, not sleeps); terraform
> parallelizes independent resources; and **fast re-deploy comes for free** —
> bump the version and `terraform plan` diffs only the changed CNEInstance spec
> + helm versions and applies those, watching readiness. Terraform *is* the
> delta engine, so this is *less* code than a bespoke reconciler.

`Status: open` (re-pivoted; not yet dispatched).

### Locked decisions (integrator, do NOT relitigate)
- **GO on `alekc/kubectl`** for the CR layer (architect spike GO). No custom
  provider, no `internal/bnk` Go reconciler, no `internal/k8s/wait.go`.
- **`helm_release`** for cert-manager + FLO + CIS installs (`wait = true`);
  FAR version-discovery stays terraform-side.
- **`kubernetes` provider** for the helm *prerequisites* (namespaces +
  image-pull/login secrets — they must precede the charts or `helm_release
  wait=true` blocks on `ImagePullBackOff`).
- **Legacy `curl` modules stay intact behind an install-mode flag** as the
  validator's benchmark baseline.
- **Keep IBM IAM trusted-profile + COS reads in terraform** (unchanged).

---

## Issue 1 — Terraform install layer: `helm_release` + `kubernetes` provider

**Severity**: high
**Status**: open

Per the architect's boundary (`issue_sprint27_architect.md` Issue 1):
- Convert cert-manager / f5-lifecycle-operator / f5-bnk-cis from
  `null_resource local-exec helm` to **`helm_release`** with `wait = true`,
  `atomic` where appropriate, values ported from the current `--set`/values
  blobs. FAR version-discovery (`helm pull f5-bigip-k8s-manifest`, parse FLO/CIS
  versions) stays terraform-side and feeds the `helm_release` `version`.
- Move the helm prerequisites — `f5-utils`/`flo` namespaces and the
  `far-secret` (dockerconfigjson) / `f5-bigip-ctlr-login` secrets — from `curl`
  to `kubernetes_namespace` / `kubernetes_secret`, ordered before the charts.
- Configure the `helm` + `kubernetes` providers from the existing
  `ibm_container_cluster_config` data source (host/token/ca), matching how the
  modules' `providers.tf` already wire the kubernetes provider.

## Issue 2 — Terraform CR layer: `alekc/kubectl` `kubectl_manifest` + `wait_for`

**Severity**: high (the core retirement of `curl` + `time_sleep`)
**Status**: open

Add `alekc/kubectl` to each relevant module's `required_providers` and replace
every `null_resource` + `curl` CR apply (and its `time_sleep`) with a
`kubectl_manifest` (`server_side_apply = true`, `yaml_body` from the existing
`*_manifest` local — **specs unchanged**) + a `wait_for`. The architect's
confirmed ready-signals:

| Resource | `wait_for` |
|----------|-----------|
| **CNEInstance** (`k8s.f5.com/v1`) | `wait_for { condition { type = "Available"  status = "True" } }` |
| **License** (`k8s.f5net.com/v1`) | `wait_for { field { key = "status.state"  value = "Verification Complete" } }` — confirm the literal on a live cluster (validator); swap to a `condition{}` matcher if CWC emits a stable Ready/Available condition |
| cert-manager `Certificate` (`ext-ca`) | `wait_for { condition { type = "Ready" status = "True" } }` |
| ClusterIssuer / CA issuer | `wait_for { condition { type = "Ready" status = "True" } }` |
| NADs, SCC `ClusterRoleBinding`s | apply via `kubectl_manifest` (no wait needed) — drops their sleeps |
| node-labeler `Job` | `wait_for { condition { type = "Complete" status = "True" } }` |

Ordering: the CNEInstance/License `kubectl_manifest`s `depends_on` the FLO
`helm_release` (CRD-before-CR — `wait_for` removes the sleep, NOT the ordering);
the cert-manager CRs `depend_on` the cert-manager `helm_release`. No
terraform→Go ping-pong (it's all terraform). Drop the namespace
finalizer-strip `curl` dance — `kubectl_manifest` destroy + the kubernetes
provider's finalizer-aware delete handle teardown.

## Issue 3 — Provider wiring, install-mode flag, roksbnkctl integration

**Severity**: medium
**Status**: open

- **Provider availability**: roksbnkctl drives terraform via terraform-exec;
  `terraform init` fetches `alekc/kubectl` from the registry automatically.
  Confirm roksbnkctl's init path doesn't pin/lock providers in a way that
  blocks it. **Note the air-gapped implication** (a provider mirror is needed
  where the runner can't reach `registry.terraform.io`) — document it; a
  bundled mirror is a follow-up, not this sprint.
- **Install-mode flag**: a tfvar (e.g. `bnk_cr_mode = "kubectl" | "legacy_curl"`)
  selecting the new `kubectl_manifest`/`helm_release` path vs the untouched
  legacy `curl` modules, so the validator benchmarks both. roksbnkctl renders
  it (`internal/tf/vars.go`) from a workspace-config toggle or a `--legacy-bnk`
  flag — small Go change, mirror the existing toggle-rendering pattern.
- **`bnk status` (optional, light)**: the CR readiness is now in terraform
  state; roksbnkctl can surface it via `terraform output` or the existing
  `internal/k8s` `k get` against the live CRs. Add a thin `roksbnkctl bnk
  status` only if cheap; otherwise defer — the live status is already
  queryable. Do NOT build a watch/reconcile layer.
- **`bnk down`**: unchanged path (terraform destroy) now cleanly removes the
  CRs via `kubectl_manifest` destroy (replaces the destroy-time `curl`).

## Issue 4 — Speed verification hooks

**Severity**: medium (speed is the primary motivation)
**Status**: open

- Ensure the new path has **zero `time_sleep`** in the kubectl path (grep the
  new modules — any remaining `time_sleep` is a bug).
- Confirm terraform's default `-parallelism` lets independent `kubectl_manifest`
  resources (NADs, SCC bindings, secrets) apply concurrently; only the true
  serial edges (FLO `helm_release` → CNEInstance → License) are serialized via
  `depends_on`.
- The fast re-deploy is terraform-native: a `version` bump re-plans only the
  changed `helm_release` + CNEInstance `kubectl_manifest`. No extra code — but
  verify the plan is minimal (no spurious diffs forcing full re-apply; watch
  for `kubectl_manifest` perpetual-diff pitfalls — use `server_side_apply` +
  the provider's field-manager settings to avoid them).

### Scope guards
- No `internal/bnk`, no `internal/k8s/wait.go`, no custom provider, no Go
  watch/reconcile loop — the CR layer is terraform.
- Don't touch IBM-Cloud (non-k8s) terraform resources; keep the legacy `curl`
  modules working behind the install-mode flag.
- Specs (CNEInstance/License/issuer/NAD bodies) port **verbatim** into
  `yaml_body` — this sprint changes the apply *mechanism*, not the manifests.
- Tests are validator's surface.

### Acceptance criteria
1. `bnk up` (kubectl mode) brings up cert-manager → FLO → CNEInstance → License
   with **no `time_sleep`** — `helm_release wait=true` + `kubectl_manifest
   wait_for` gate every step on real readiness.
2. All CRs are real terraform resources (in state); `terraform destroy`/`bnk
   down` removes them cleanly (no destroy-time `curl`).
3. A version bump re-plans/applies only the delta (fast re-deploy), markedly
   faster than a cold `up`.
4. Install-mode flag selects kubectl vs legacy-curl; the legacy path still runs
   (validator baseline).
5. `terraform validate` + `terraform fmt -check` clean on the new modules;
   `go build ./...` / `go vet ./...` / `staticcheck ./...` clean (for the small
   render/flag Go change).

### Files affected
- `terraform/modules/{cert_manager,flo,cne_instance,license}/` — the new
  `helm_release` / `kubernetes_*` / `kubectl_manifest` resources, gated by the
  install-mode variable; `required_providers` += `alekc/kubectl`. The existing
  `curl`/`null_resource` blocks stay (legacy mode) — a later sprint removes them.
- `terraform/variables.tf` (+ module variables) — `bnk_cr_mode` install-mode.
- `internal/tf/vars.go` (+ `internal/config/workspace.go`) — render the toggle.
- `internal/cli/bnk_phase.go` — `--legacy-bnk` flag (+ optional `bnk status`).
- `go.mod` — **no new Go deps** (the provider is a terraform artifact, not Go).

### Related
- `issues/issue_sprint27_architect.md` — the GO spike (rounds 1 & 2), the
  confirmed ready-signals + `wait_for` blocks, the module-restructure boundary.
- `issues/issue_sprint27_validator.md` — gated-live correctness + speed
  benchmark (kubectl vs legacy) + the License `status.state` live-confirm.
- `terraform/modules/flo/modules/flo/main.tf` etc. — the curl bodies being
  replaced (specs reused verbatim).
- alekc/kubectl: `registry.terraform.io/providers/alekc/kubectl/latest`.
- Integrator memory [[live-verify-high-issues]] — cluster-mutating; live
  benchmark gates closure.
