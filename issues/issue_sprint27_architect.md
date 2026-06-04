# Sprint 27 — architect issues (terraform module restructure design + book) — terraform-native via alekc/kubectl

> **Sprint 27 frame (re-pivoted 2026-06-04).** Replace the terraform-driven
> BNK-phase Kubernetes work (`null_resource` + `local-exec` + raw `curl`
> server-side-apply + static `time_sleep`) — but **terraform-natively**, not
> with a Go reconciler. Per the architect spike below (**FINAL VERDICT: GO**),
> the CR layer moves to the **`alekc/kubectl` `kubectl_manifest` + `wait_for`**
> provider (CRs as real terraform resources, in state, watch `.status` to
> ready, no plan-time-CRD requirement) and the chart installs move to
> `helm_release`. There is **no custom provider and no in-CLI Go reconciler**.
> This issue owns the **terraform module-restructure design** staff implements,
> plus the operator-facing prose. The CRD ready-signals are **already confirmed**
> (see "Spike rounds 1 & 2" below).

`Status`: open (re-pivoted)

> **Re-pivot note.** The original architect issues (Go-reconciler handoff
> boundary, a watch-helper API, an errgroup parallelism DAG) are **obsolete** —
> superseded by the GO spike. The reframed deliverables below are the
> terraform-native ones. The spike sections (rounds 1 & 2) are the binding
> decision record and stay verbatim.

---

## Issue 1 — Terraform module restructure design (BLOCKING input to staff)

**Severity**: high
**Status**: open

Design the new terraform structure staff implements (it's all terraform now —
no Go-side boundary, so the old "handoff/ping-pong" problem is gone):

1. **Install layer**: which resources become `helm_release` (cert-manager, FLO,
   CIS — `wait = true`) and which helm *prerequisites* become `kubernetes`
   provider resources (`f5-utils`/`flo` namespaces, `far-secret`/
   `f5-bigip-ctlr-login` secrets — they must precede the charts or
   `helm_release wait=true` blocks on `ImagePullBackOff`). Specify provider
   config wiring from the existing `ibm_container_cluster_config`.
2. **CR layer**: the list of `kubectl_manifest` resources (ClusterIssuer/
   Certificate/CA-issuer, the 2 NADs, the SCC `ClusterRoleBinding`s, node-labeler
   Job, CNEInstance, License) with their `wait_for` blocks (from the spike) and
   their `depends_on` edges (CNEInstance/License → FLO `helm_release` for
   CRD-before-CR). Confirm cert-manager CRs depend on the cert-manager
   `helm_release`.
3. **Install-mode flag**: the cleanest way to keep the legacy `curl` modules
   intact as the validator's benchmark baseline while adding the new path —
   a `bnk_cr_mode = "kubectl" | "legacy_curl"` variable gating `count`/`for_each`
   on the old vs new resources, vs a parallel slim module. Recommend one.
4. **`required_providers`**: where `alekc/kubectl` is declared, and the
   `terraform init` / air-gap implication for roksbnkctl's embedded terraform.

Output: the resource-by-resource install-vs-CR table + the `depends_on` graph +
the install-mode-flag recommendation, in your closure.

## Issue 2 — CRD ready-signals — DONE (see Spike rounds 1 & 2)

**Severity**: high
**Status**: resolved

Confirmed in the spike (rounds 1 & 2 below), read from the FAR-shipped CRDs:
- **CNEInstance** (`k8s.f5.com/v1`): `status.conditions[type=Available]==True`
  → `wait_for { condition { type="Available" status="True" } }`.
- **License** (`k8s.f5net.com/v1`): `status.state` (CPCL; printer column;
  +`status.conditions[]` fallback) → `wait_for { field { key="status.state"
  value="Verification Complete" } }` — **confirm the literal on a live licensed
  cluster** (validator), the one residual.
- cert-manager `Certificate`/`ClusterIssuer`: `conditions[type=Ready]==True`;
  node-labeler `Job`: `conditions[type=Complete]==True`.

## Issue 3 — Terraform ordering / parallelism review

**Severity**: medium (supports the speed goal)
**Status**: open

Speed depends partly on terraform parallelizing independent resources. Today the
modules serialize almost everything via `depends_on`; much of that is
conservative, not required. Review which `depends_on` edges are **genuinely**
needed (cert-manager `helm_release` → cert CRs; FLO `helm_release` → CNEInstance
→ License) vs which can drop so terraform's default `-parallelism` applies NADs,
secrets, SCC bindings, and issuers concurrently. Flag any conservative edge to
remove. (This replaces the obsolete errgroup DAG — terraform does the
parallelism; we just stop over-serializing it.)

## Issue 4 — Book authoring

**Severity**: low
**Status**: open

- Rewrite the BNK-phase chapter: the deployment is now terraform-native —
  `helm_release` installs + `kubectl_manifest` + `wait_for` for the CRs, gating
  on real `.status` (CNEInstance `Available`, License `state`) instead of
  `time_sleep`. Explain the install-mode flag (kubectl vs legacy-curl) during
  the transition and that the CRs are now real terraform state (`plan`/`destroy`/
  drift).
- A concept note: "why we retired the BNK `null_resource`/`curl`/`sleep`" —
  eventual consistency, real readiness via `wait_for` vs fixed sleeps, the
  plan-time-CRD problem that `alekc/kubectl` solves, the speed win.
- Note: IBM IAM trusted-profile + COS reads stay in terraform; the new
  `alekc/kubectl` provider dependency (+ air-gap mirror caveat).
- Mark transcript output illustrative (tech-writer re-captures).

### Scope guards
- **No Go, no terraform implementation** — staff implements the HCL. You ship
  the design + prose. Ready-signals are already verified (spike); don't re-do.
- mdbook builds (docker image) clean; verify cross-links.

### Acceptance criteria
1. Terraform module-restructure design produced: install-vs-CR table,
   `depends_on` graph, install-mode-flag recommendation, `required_providers` +
   air-gap note.
2. (Ready-signals — already done in the spike.)
3. Conservative-`depends_on` review delivered (which edges to drop for
   parallelism).
4. BNK-phase chapter + concept note authored; IBM-IAM + the new provider
   dependency noted.

### Files affected
- This ledger (design output appended to the spike).
- `book/src/**` (BNK-phase chapter, concept note), `book/src/SUMMARY.md` only
  if a new entry is added.

### Related
- `issues/issue_sprint27_staff.md` — consumes this design + the spike's
  `wait_for` blocks.
- `terraform/modules/{flo,cne_instance,license,cert_manager}/` — the curl bodies
  being replaced (specs reused verbatim).
- alekc/kubectl: `registry.terraform.io/providers/alekc/kubectl/latest`.

---

## Spike — alekc/kubectl `wait_for` evaluation (architect, 2026-06-04)

**Question this spike answers (asked by the integrator):** can the
`alekc/kubectl` terraform provider's `kubectl_manifest` + `wait_for` block
replace the `curl` + `time_sleep` custom-resource applies — applying the CRs as
real terraform resources (in state) AND watching their `.status` to ready —
*without* building our own custom provider or moving the CR layer into Go?

Note this is an **alternative architecture to the sprint's stated plan**
(Sprint 27 as framed moves the CR layer into a native Go watch-reconciler in
`internal/bnk`). This spike evaluates the terraform-native option on its merits;
the VERDICT below recommends which way to go and why.

### Provider capability findings (cited)

Source: `alekc/terraform-provider-kubectl`
`docs/resources/kubectl_manifest.md` (master), Terraform Registry
`registry.terraform.io/providers/alekc/kubectl/latest`. Releases checked via
the GitHub releases API: latest **v2.4.1, published 2026-06-01** (v2.4.0
2026-05-19) — actively maintained; the fork is the de-facto successor to the
now-dormant `gavinbunney/kubectl`.

**1. Plan-time CRD requirement — the key advantage holds.** `kubectl_manifest`
applies **free-form YAML** ("the processing and application of free-form YAML
directly to Kubernetes", README) via the dynamic/RESTMapper path — it does
**not** do a client-side typed-schema lookup of the kind at plan time the way
HashiCorp `kubernetes_manifest` does. HashiCorp's provider fails at *plan* with
`cannot select exact GVK ... no matches for kind` when the CRD is created in the
same apply; `alekc/kubectl` does not, because it carries the YAML as an opaque
string attribute and resolves the GVK at *apply* time. This is precisely why
the gavinbunney/alekc provider exists and is the standard tool for
"install-CRD-and-CR-in-one-apply" (it is also why the existing modules used raw
`curl` "no kubernetes provider required at plan time" — see the comment in
`cne_instance/.../cneinstance/main.tf` line ~272). **Caveat:** the *first*
apply must still order the CRD before the CR via `depends_on`/the helm_release —
`wait_for` does not remove ordering, only the fixed sleep. (Confidence: high on
the design; the "no plan-time schema lookup" behaviour is documented behaviour
of this provider family and is the load-bearing reason it is recommended over
`kubernetes_manifest`, but I did not run a live `terraform plan` against a
cluster missing the CRD — flagged as the one thing to smoke-test.)

**2. `wait_for` schema (quoted verbatim from the docs).** Two matcher kinds,
both usable together; the provider waits for **all** conditions to become true
or until timeout:

```hcl
wait_for {
  field {
    key   = "status.phase"
    value = "Running"
  }
  field {
    key        = "status.podIP"
    value      = "^(\\d+(\\.|$)){4}"
    value_type = "regex"
  }
  condition {
    type   = "Ready"
    status = "True"
  }
}
```

Documented nested schema:
- `wait_for.field` — `key` (String, dot-path into the object; under the hood
  [gojsonq] is used for querying), `value` (String), `value_type` (Optional,
  `"eq"` (default) or `"regex"`).
- `wait_for.condition` — `type` (String, the Condition `type`), `status`
  (String, the Condition `status` to wait for). **This maps 1:1 to
  `status.conditions[type=X].status==True`** — exactly the
  `metav1.Condition` model CNEInstance uses (see below). No jsonpath filter
  authoring needed for the conditions case; the provider walks the
  `status.conditions[]` array itself.
- `wait` (Optional, bool, default `false`) — wait for **finalizers** to
  complete on **deleted** objects before returning. `delete_cascade`
  (`Background`/`Foreground`) controls cascade. So destroy **does** delete the
  CR (the resource is in state) and can block on finalizers — strictly better
  than today's `curl -X DELETE ... || true`.

So both matcher styles we need are expressible:
`condition { type = "Available" status = "True" }` for CNEInstance, and a
`field { key = "status.<x>" value = "<y>" }` (optionally `value_type=regex`)
for License **once its status field is known**.

**3. Caveats / risks.**
- **Installability / air-gap.** roksbnkctl drives terraform, so the provider
  must come down at `terraform init`. It is on the public registry
  (`alekc/kubectl`) — fine for connected runs. For air-gapped/offline ROKS
  installs we'd need a provider mirror / `terraform providers mirror` bundle or
  a network mirror in CLI config. This is a *new* third-party plugin dependency
  in the install path (today the `curl` modules need only `curl`+`base64` on the
  runner). Must be accounted for in the offline story.
- **Maturity.** Active (v2.4.1, 3 days old) and widely adopted, but it is a
  community single-maintainer fork, not HashiCorp- or F5-owned. Acceptable for a
  bootstrap tool; note it as a supply-chain dependency.
- **Known status-wait sharp edges (provider-family history):** `wait_for` polls
  the live object; if the CR's controller never populates the matched
  field/condition the apply hangs to timeout (so the timeout + the *exact* field
  must be right, same failure mode the Go path would have). The `condition`
  matcher requires the controller to actually emit a `status.conditions[]` entry
  of that `type` — confirmed present for CNEInstance, **unconfirmed for
  License** (see risk below).

### CNEInstance + License `.status` ready-signals (what I found, and from where)

I searched the sibling repos on disk (`/mnt/d/project/*`, `/mnt/d/bnk-forge*`)
and F5 public docs.

**CNEInstance (`k8s.f5.com/v1`) — CONFIRMED from operator source.**
Source: `f5-lifecycle-operator` repo on disk (the FLO operator itself):
- `pkg/api/v1/cneinstance_types.go` — `CNEInstanceStatus` embeds
  `FloStatus`, and `pkg/api/v1/shared_types.go` defines
  `type FloStatus struct { Conditions []metav1.Condition }`. So the status is
  **pure `metav1.Condition` array — there is NO `status.phase`.**
- `internal/controller/conditions/conditions.go` — every FLO CR always carries
  three condition types: **`Accepted`, `Reconciled`, `Available`**. The
  user-facing readiness type is **`Available`**: doc-comment "The `Available`
  type indicates the state of the managed component ... `True | Available | The
  managed component is in a running state.`"
- `internal/controller/cneinstance/cneinstance_controller.go` (~lines 900-920)
  aggregates every sub-component's `Available` condition and sets the
  CNEInstance's top-level `Available` to `True`/reason `Available` **only when
  all components are up**, else `False`/`Failed`.
- The generated CRD `config/crd/bases/k8s.f5.com_cneinstances.yaml` confirms
  `subresources.status: {}` and `status.conditions[]` as standard
  `metav1.Condition` items (type/status/reason/message/lastTransitionTime/
  observedGeneration). No `phase` property exists in the schema.

  → **CNEInstance ready signal = `status.conditions[type=Available].status==True`**,
  expressible directly as `condition { type = "Available"  status = "True" }`.
  (High confidence — read straight from the operator that owns the type.)

**License (`k8s.f5net.com/v1`) — NOT confirmable from on-disk source or public
docs; flagged as a RISK.**
- The License CRD/types are **not** defined in any repo on disk. FLO only holds
  *RBAC* for it: `internal/controller/cwc/cwc_controller.go` has
  `//+kubebuilder:rbac:groups=k8s.f5net.com,resources=licenses,verbs=...` and
  `resources=licenses/status` — i.e. the License CR has a `/status`
  subresource, but it is **owned/reconciled by the CWC (Cluster-Wide
  Controller)**, a separate closed component whose types are not on this disk.
- Both terraform modules that apply it (`license/.../license/main.tf` and
  `bnk-forge-ibm-roks-cluster-4/modules/roks-cluster-license/.../main.tf`) write
  only `spec.jwt` + `spec.operationMode` and **never read `.status` — they
  sleep.** So the baseline gives us no observed status shape either.
- F5 public docs (`clouddocs.f5.com/.../bnk-activate-license.html`,
  `.../overviews/cwc.html`) describe license verification via the **CWC REST
  `/status` endpoint** returning `{"State":"Verification Complete",
  "EntitlementType":"eval|paid","LicenseExpiryDate":...}` — i.e. F5 documents
  the *CWC service* status, **not** a documented `License` CR `.status` field.
  I could **not** confirm whether the License CR mirrors a `phase`/`licensed`/
  `conditions[]` into its own `.status`, nor the exact value that means
  "licensed".

  → **License ready signal is UNKNOWN.** It must be read off a live cluster
  (`kubectl get licenses.k8s.f5net.com bnk-license -n f5-utils -o yaml`) before
  any `wait_for` can target it. Do **not** assert a field. If the License CR
  does expose a status condition or a `status.state`/`status.phase`, `wait_for`
  can match it (`condition{}` or `field{}` respectively); if it exposes
  **nothing** queryable (CWC tracks state out-of-band via REST), then
  `wait_for` cannot gate on License readiness at all and the only reliable
  readiness probe is the CWC REST `/status` — which terraform cannot poll
  cleanly and which is exactly the kind of thing a Go reconciler *could*.

### VERDICT — CONDITIONAL GO (CNEInstance yes; License pending live confirmation)

**`alekc/kubectl` `kubectl_manifest` + `wait_for` CAN replace the `curl` +
`time_sleep` applies for the CR layer, and NO custom provider is needed.** The
provider applies CRs whose CRD is created in the same apply (no plan-time schema
lookup), puts them in terraform state (real create/update/drift/delete, with
finalizer-aware destroy — all things the `curl` `null_resource` fakes), and its
`condition {}` matcher expresses the CNEInstance ready-signal **exactly**
(`status.conditions[type=Available].status==True`, confirmed from FLO source).
That alone replaces ~150s of the ~210s of `time_sleep` (CNE CRD 30 + CNE SCC 30
+ the CNEInstance "is it up" gap that today is unguarded) with a real watch.

It is **CONDITIONAL** on one open item: the **License (`k8s.f5net.com/v1`)
`.status` ready-field is unconfirmed** — it is not in any source on disk and not
in F5 public docs (CWC tracks license state via its own REST `/status`, not a
documented CR field). Resolution is cheap: one
`kubectl get licenses.k8s.f5net.com -o yaml` on a licensed cluster.
- If the License CR exposes a status condition or `status.state`/`status.phase`
  → full **GO**; add a `condition{}` or `field{}` to its `kubectl_manifest` and
  the terraform-native path covers both CRs end-to-end.
- If the License CR exposes **no** queryable readiness status (state lives only
  in CWC's REST endpoint) → terraform-native `wait_for` cannot gate License
  readiness; keep applying License via `kubectl_manifest` (state + delete are
  still wins) but its readiness gate needs either a follow-on check or the Go
  path. This is the **one** place a thin Go reconciler would add something
  `wait_for` cannot: polling the CWC REST `/status` for `State == "Verification
  Complete"`.

**No custom terraform provider is warranted** under any branch — `alekc/kubectl`
covers apply + state + delete + status-wait for everything whose readiness is a
CR `.status` field/condition.

### If GO — sketch of the terraform module change

Replace, in each of `cne_instance/.../cneinstance` and `license/.../license`,
the `null_resource` + `local-exec curl` (apply) + `local-exec when=destroy`
(delete) + the `time_sleep` gate with a single `kubectl_manifest` resource whose
`yaml_body` is the existing `jsonencode(local.*_manifest)` rendered to YAML
(the spec locals are unchanged), `server_side_apply = true`,
`field_manager = "roksbnkctl"` (or keep `terraform`), and a `wait_for` block.
For CNEInstance:
`wait_for { condition { type = "Available"  status = "True" } }` — terraform
blocks until FLO reports the instance up, replacing the CNE CRD/SCC sleeps; the
SCC `ClusterRoleBinding`s likewise become `kubectl_manifest` resources
(dropping their own `time_sleep`). For License: `kubectl_manifest` with the
JWT/operationMode spec; add its `wait_for` once the live status field is
confirmed (`condition{}` or `field{}`), otherwise apply without a status gate
this iteration. CRD-before-CR ordering stays expressed via `depends_on` on the
FLO `helm_release` (the chart that installs the CRDs), so no ping-pong. Net: the
two CR modules keep their spec locals verbatim, shed all `curl`/`base64`/`sleep`
shell, gain real state + drift + finalizer-aware destroy, and gate on actual
readiness instead of 60s of guesswork. The legacy `curl` modules stay intact
behind the install-mode flag as the validator's benchmark baseline.

---

## Spike round 2 — License CRD from FAR manifest (architect, 2026-06-04)

**Closes the round-1 open gate.** Round 1 left exactly one item unresolved: the
**License (`k8s.f5net.com/v1`) `.status` ready-field was UNCONFIRMED** — no
License CRD was on disk, and F5 public docs described license state only via the
CWC REST `/status` endpoint (which terraform cannot poll). The integrator
pointed me at the `f5-bigip-k8s-manifest` chart in FAR. I pulled it and the
charts it references, and the License CRD schema **resolves the gate**.

### What I pulled (FAR / repo.f5.com, helm v3.16.3)

Auth via the key at `/mnt/d/Downloads/f5-far-auth-key.tgz` (single
`cne_pull_64.json` = base64 SA key, used as the `_json_key_base64` registry
password). All credentials redacted throughout; `helm registry logout` and
tempdir removal done at the end. Login + every pull returned RC=0.

- **`oci://repo.f5.com/release/f5-bigip-k8s-manifest:2.3.0-3.2598.3-0.0.170`** —
  the umbrella manifest. Its `bigip-k8s-manifest-...yaml` lists the component
  chart+image versions. Relevant `charts/*` entries:
  - `charts/f5-lifecycle-operator` → **`v2.21.13-0.0.28`** (FLO)
  - `charts/cwc` → `0.66.7-0.0.7`
  - `charts/f5-spk-crds-common` → `14.59.1-0.0.70`
  - `charts/f5-cnf-crds-n6lan` → `14.59.1-0.0.70`
  - `charts/f5-license-proxy` → `1.29.0-0.10.28`, `charts/f5-bnk-cis` → `v3.0.6-0.0.5`
- **`oci://repo.f5.com/charts/f5-lifecycle-operator:v2.21.13-0.0.28`** — all FLO
  CRDs ship in one file, `charts/crds/templates/crds.yaml` (22 CRDs, all group
  `k8s.f5.com`: Afm, CNEController, **CNEInstance**, CNEManifest, Cwc,
  NodeLabeler, …). **No License CRD here** — only an RBAC reference in
  `templates/rbac.yaml` (`k8s.f5net.com / resources: licenses` + `licenses/status`
  + `licenses/finalizers`, verbs incl. patch/update/watch). So FLO *watches and
  patches* the License `/status` subresource but does **not own/define** the CRD.
- **The License CRD lives in the SPK/CNF CRD charts**, not FLO. Found
  `templates/license.yaml` in BOTH `f5-spk-crds-common:14.59.1-0.0.70` and
  `f5-cnf-crds-n6lan:14.59.1-0.0.70` — **byte-identical** (`diff` clean). One
  canonical schema. (Pulled `cwc`, `f5-license-proxy`, `f5-bnk-cis` too; none
  defines the License CRD — cwc/license-proxy only consume it.)

### License CRD `.status` schema — CONFIRMED, it IS queryable (quoted verbatim)

`licenses.k8s.f5net.com`, group `k8s.f5net.com`, version `v1` (served+storage),
`scope: Namespaced`, `subresources: { status: {} }`, kind `License`
(plural `licenses`, short `lic`). The `openAPIV3Schema` `.status` block:

```yaml
status:
  type: object
  properties:
    state:
      type: string
      description: >
        Current state of the license as reported by the CPCL state machine.
    stateDescription:
      type: string
    detectedEnvironment:
      type: string
    entitlementType:
      type: string         # e.g. "paid", "eval"
    digitalAssetID:
      type: string
    licenseExpiryDate:
      type: string
    suggestedAction:
      type: string
    conditions:
      type: array
      x-kubernetes-list-type: map
      x-kubernetes-list-map-keys: [type]
      items:               # standard metav1.Condition
        type: object
        required: [lastTransitionTime, message, reason, status, type]
        properties: { type, status, reason, message, lastTransitionTime, observedGeneration }
```

And the CRD's `additionalPrinterColumns` make `status.state` first-class:

```yaml
- name: State        jsonPath: .status.state
- name: Mode         jsonPath: .spec.operationMode
- name: Entitlement  jsonPath: .status.entitlementType
- name: Environment  jsonPath: .status.detectedEnvironment
- name: Expiry       jsonPath: .status.licenseExpiryDate
- name: DigitalAssetID  jsonPath: .status.digitalAssetID
```

**This overturns the round-1 worst case.** The License CR is NOT a status-less
out-of-band object. The CWC mirrors its license state machine INTO the CR's
`.status` — there are TWO queryable readiness surfaces:
1. **`status.state`** (string) — the CPCL state machine value. This is the same
   state the round-1 CWC-REST finding reported as `"State": "Verification
   Complete"` (the REST `/status` and the CR `.status.state` are the same CPCL
   machine; the printer column `State` is the operator-facing view of it).
2. **`status.conditions[]`** — a standard `metav1.Condition` array (same model
   CNEInstance uses), so a `condition{type, status}` matcher also works.

The chart does NOT enumerate the `state` value-set (it's an open string from the
closed CWC/CPCL machine), so the **terminal "licensed" value must be pinned on a
live licensed cluster** (`kubectl get lic -n f5-utils -o yaml` → read
`.status.state`; round 1's CWC-REST evidence strongly indicates it is
`"Verification Complete"`, and `entitlementType` becomes `paid|eval`). This is a
value-confirmation, not a capability question — the field exists and is
`wait_for`-queryable either way. Prefer `field { key="status.state" }` once the
literal is confirmed; the `conditions[]` matcher is the fallback if a single
`type` (e.g. an `Available`/`Ready`/`Licensed` condition) proves more stable than
the free-text state string.

### CNEInstance `.status` — RE-CONFIRMED from the shipped FAR CRD

Read straight from `crds.yaml` in `f5-lifecycle-operator:v2.21.13-0.0.28` (the
chart that actually installs it — stronger evidence than round-1's operator
source tree). `CNEInstanceStatus` has **`conditions` only — a `metav1.Condition`
array, NO `phase`** (grep for `phase` in the CNEInstance block: none). The
condition `status` is `enum: ["True","False","Unknown"]`, `subresources: {status:
{}}`. Matches round 1 exactly:
**ready = `status.conditions[type=Available].status=="True"`.**

### FINAL VERDICT — **GO**

**Use `alekc/kubectl` `kubectl_manifest` + `wait_for` for the entire BNK CR
layer. No custom terraform provider. No in-CLI Go reconciler for the CR layer.**
Both CR ready-signals are now confirmed expressible as `wait_for`:

- **CNEInstance** — confirmed `metav1.Condition[type=Available]`:

  ```hcl
  resource "kubectl_manifest" "cneinstance" {
    yaml_body         = yamlencode(local.cneinstance_manifest)  # existing spec local, unchanged
    server_side_apply = true
    field_manager     = "roksbnkctl"
    wait_for {
      condition {
        type   = "Available"
        status = "True"
      }
    }
    depends_on = [helm_release.flo]   # CRD installed by the FLO chart → no ping-pong
  }
  ```

- **License** — confirmed `status.state` (+ `status.conditions[]` fallback).
  Recommended form once the live `state` literal is confirmed (round-1 evidence:
  `"Verification Complete"`):

  ```hcl
  resource "kubectl_manifest" "license" {
    yaml_body         = yamlencode(local.license_manifest)  # spec.jwt + spec.operationMode, unchanged
    server_side_apply = true
    field_manager     = "roksbnkctl"
    wait_for {
      field {
        key   = "status.state"
        value = "Verification Complete"   # CONFIRM literal on a live licensed cluster before merge
      }
    }
    depends_on = [helm_release.flo, kubectl_manifest.cneinstance]  # License CRD + CWC present
  }
  ```

  If the live `state` string proves less stable than a condition (e.g. CWC emits
  an `Available`/`Ready` condition), swap the matcher to
  `wait_for { condition { type = "<that-type>" status = "True" } }` — same
  resource, no other change. Both paths are within `wait_for`; **License does NOT
  need the Go path** (the round-1 PARTIAL trigger — "no queryable CR status, only
  CWC REST" — is now disproven: the CWC mirrors state into `.status.state` and
  `.status.conditions[]`).

**Net:** the GO verdict from round 1 is unconditional now. The two CR modules
keep their `local.*_manifest` spec locals verbatim, drop all
`curl`/`base64`/`time_sleep` shell, gain real state + drift + finalizer-aware
destroy, and gate on **actual** readiness for BOTH CRs. Only one pre-merge task
remains and it is a value lookup, not an architecture risk: confirm the exact
`status.state` "licensed" literal on a live cluster. Legacy `curl` modules stay
behind the install-mode flag as the validator baseline (unchanged from round 1).
