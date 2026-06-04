# Sprint 27 — architect issues (helm strategy, CRD ready-signal schemas, watch-API design, book)

> **Sprint 27 frame.** Replace the terraform-driven BNK-phase Kubernetes
> work (`null_resource` + `local-exec` + raw `curl` server-side-apply +
> static `time_sleep`) with a native, watch-driven reconciler inside
> roksbnkctl (`internal/bnk`, building on `internal/k8s`). Staff
> (`issue_sprint27_staff.md`) owns the Go. This issue owns the **design
> inputs staff codes against** — the helm strategy, the exact
> ready-signal for each CRD the watches gate on, and the watch-helper API
> shape — plus the operator-facing prose.

`Status`: open

---

## Issue 1 — Terraform↔Go handoff boundary (helm decision RESOLVED)

**Severity**: high (the boundary is a BLOCKING input — staff can't structure
the install layer vs the Go CR layer without it)
**Status**: open

**Decision (integrator 2026-06-04):** Helm stays in terraform. Convert the
three chart installs (cert-manager, f5-lifecycle-operator, f5-bnk-cis) from
`null_resource` + `local-exec helm` to the proper **`helm_release` provider**
with `wait = true` (real readiness, no `time_sleep`); FAR version-discovery
stays terraform-side. **No `helm.sh/helm/v3` Go dependency.** Native Go
replaces ONLY the `curl`-applied custom resources + their `time_sleep` gates.

Your job is to draw the **exact handoff boundary** so the BNK phase is a clean
two-stage flow (terraform installs → Go reconciles CRs) with no ping-pong:

1. For each currently-`curl`'d resource in the four modules, classify it as
   **terraform (helm prerequisite)** or **Go (post-install CR)**. The hard
   cases to get right:
   - **Namespaces + secrets** (`f5-utils`/`flo`, `far-secret`,
     `f5-bigip-ctlr-login`): must exist BEFORE the charts (image-pull secret;
     `helm_release wait=true` would block on `ImagePullBackOff` otherwise) →
     terraform `kubernetes_namespace`/`kubernetes_secret`. Confirm.
   - **cert-manager `ClusterIssuer`/`Certificate`/CA issuer**: FLO's helm
     values *reference* the issuer, but does FLO consume it at **install**
     time (must pre-exist → terraform) or only at **runtime** when it issues
     certs during CNEInstance reconcile (can be Go, post-handoff)? This
     determines whether they're terraform or Go. Verify against FLO chart
     behavior — do not guess.
   - **NADs, SCC bindings, node-labeler Job, CNEInstance, License**: post-
     install → Go.
2. Confirm there is **no resource that a `helm_release` depends on which is
   produced by the Go layer** (that would force terraform→Go→terraform
   ping-pong). If one exists, push it into terraform.
3. Recommend how to keep the **legacy `curl`-based modules intact as the
   benchmark baseline** while adding the new helm_release path (flag-selected
   install-mode variable vs a parallel slim module set) — the validator times
   native vs legacy, so both must run.

Output: the resource-by-resource boundary table + the install-DAG vs Go-DAG
split, in your closure.

## Issue 2 — CRD ready-signal schemas (BLOCKING input to the watches)

**Severity**: high
**Status**: open

The whole point of the sprint is to **watch real status instead of sleeping**.
For each gated resource, pin the exact field/condition that means "ready" so
staff's `internal/k8s/wait.go` watches the right thing. Confirm against the
live CRDs (read the CRD `openAPIV3Schema` / `kubectl get <cr> -o yaml` on a
running cluster, or the upstream CRD definitions) — do NOT guess:

| Resource | GVR | Ready signal to confirm |
|----------|-----|-------------------------|
| CustomResourceDefinition | `apiextensions.k8s.io/v1` | `status.conditions[type=Established].status==True` |
| cert-manager `Certificate` | `cert-manager.io/v1` | `status.conditions[type=Ready].status==True` |
| cert-manager `ClusterIssuer` | `cert-manager.io/v1` | `status.conditions[type=Ready].status==True` |
| Deployment (cert-manager, FLO, CIS) | `apps/v1` | `availableReplicas==spec.replicas` & current `observedGeneration` |
| **CNEInstance** | `k8s.f5.com/v1` | **unknown — confirm**: `.status.phase`? a `conditions[]`? what value == deployed? |
| **License** | `k8s.f5net.com/v1` | **unknown — confirm**: `.status.phase`/`.status.licensed`? what value == active? |
| node-labeler `Job` | `batch/v1` | `status.conditions[type=Complete].status==True` |

The CNEInstance and License ready-signals are the two unknowns that matter
most (today terraform never checks them — it just sleeps). Get these right;
they define `WaitResourceCondition` vs `WaitResourceJSONPath` usage in staff
Issue 1.

## Issue 3 — Watch-helper API shape + reconciler progress contract

**Severity**: medium
**Status**: open

Co-design (with staff) the small surface in `internal/k8s/wait.go` and the
`internal/bnk` progress-reporter interface so the CLI's live status and the
`bnk status` command have a stable contract. Recommend client-go's
`tools/watch` (`watchtools.UntilWithSync`) over hand-rolled loops, and a
`ProgressReporter` event shape (`{phase, resource, state, message, duration}`)
that both stderr rendering and validator's hermetic assertions consume (note
the `duration` field — see Issue 5). Keep it small — this is an API-review
deliverable, not Go you write.

## Issue 5 — Safe-parallelism dependency DAG (BLOCKING input to the speed work)

**Severity**: high (speed is the sprint's primary motivation — see staff
Issue 3)
**Status**: open

The watch-based design exists to make the BNK phase **fast** (the integrator's
stated goal: deploy + test new versions in a tight loop). Staff parallelizes
independent steps via `errgroup`; to do that safely they need an authoritative
**dependency DAG** — which steps genuinely depend on which, and which can run
concurrently. Today terraform serializes almost everything via `depends_on`;
much of that ordering is conservative, not required.

Produce the DAG: for every reconcile step (namespaces, the 3 secrets, the 2
NADs, self-signed issuer + ext-ca Certificate + CA issuer, cert-manager
install, FLO/CIS installs, the SCC bindings, node-labeler Job, CNEInstance,
License, FAR version discovery), state its **true** prerequisites and what it
can run alongside. Call out the hard serial edges (cert-manager CRDs
Established → issuers; FLO Deployment Ready → CNEInstance; CNEInstance CRD
Established → License) versus the wide parallel fan-outs (namespaces ∥;
secrets ∥ NADs ∥ issuer; node-labeler Job ∥ helm installs). Flag any ordering
that LOOKS required in terraform but isn't (e.g. an SCC binding that only
needs its namespace, not the whole FLO install).

Also recommend: a sensible default concurrency cap, per-watch timeout
defaults, and which steps are worth caching across runs (FAR OCI pull keyed by
version) for the fast-re-deploy path. This DAG is what lets staff turn ~210s
of serial sleep into a few seconds of parallel watching.

## Issue 4 — Book authoring

**Severity**: low
**Status**: open

- Rewrite the BNK-phase chapter: the deployment now runs as a native
  watch-driven reconcile (cert-manager → FLO → CNEInstance → License), what
  each phase waits on, and how `bnk status` reports live state. Explain the
  `--native` / legacy-terraform flag during the transition.
- A concept note: "why we moved the BNK phase off terraform local-exec" —
  eventual consistency, real readiness via watches vs. fixed sleeps, status
  reporting. Cross-link the troubleshooting chapter (timeouts now name the
  resource + last status).
- Note explicitly that the IBM IAM trusted-profile + COS reads stay in
  terraform.
- Mark any transcript output illustrative (tech-writer re-captures).

### Scope guards
- **No Go** — `internal/k8s`, `internal/bnk`, `internal/orchestration`,
  `internal/cli` are staff's. You ship decisions + schemas + prose.
- Verify CRD ready-signals against real definitions, not guesses; cite the
  source.
- mdbook builds (docker image) clean; verify cross-links.

### Acceptance criteria
1. Terraform↔Go handoff boundary drawn: a resource-by-resource
   terraform(install/prereq)-vs-Go(post-install CR) table, the
   no-ping-pong check, and the legacy-baseline-preservation recommendation.
2. Every gated resource's ready-signal confirmed + cited (especially
   CNEInstance + License).
3. Watch-helper API + progress-event shape (incl. `duration`) agreed with
   staff.
4. Safe-parallelism dependency DAG produced (Issue 5) — hard serial edges vs
   parallel fan-outs, concurrency cap, watch-timeout defaults, cacheable
   steps.
5. BNK-phase chapter + concept note authored; IBM-IAM-stays-in-terraform
   noted; the speed motivation explained.

### Files affected
- This ledger / `resolved_sprint27_architect.md` (decisions + schemas).
- `book/src/**` (BNK-phase chapter, concept note), `book/src/SUMMARY.md` only
  if a new entry is added.

### Related
- `issues/issue_sprint27_staff.md` Issues 1-3 — consume these decisions.
- `terraform/modules/{flo,cne_instance,license,cert_manager}/` — the current
  behavior being ported; the CR specs to read.
- `internal/k8s/client.go` / `apply.go` — the reused client surface.
