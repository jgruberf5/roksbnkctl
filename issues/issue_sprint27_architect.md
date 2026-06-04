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

## Issue 1 — Helm strategy decision (BLOCKING input to staff)

**Severity**: high (a dependency-weight decision that staff cannot start the
chart-install code without)
**Status**: open

Today terraform installs three charts via `local-exec helm`: **cert-manager**
(public jetstack repo, `--wait`), **f5-lifecycle-operator** and **f5-bnk-cis**
(OCI charts from the FAR registry, `--wait=false`), with FLO/CIS versions
discovered at runtime by `helm pull`-ing `f5-bigip-k8s-manifest` and parsing
an embedded manifest (`flo/main.tf` `extract_flo_version`, ~414-462).

Pin ONE approach and document the trade-off:

- **Recommended (mixed):** cert-manager via its **static install manifest**
  applied through the existing `internal/k8s` SSA path (no helm dependency
  for it — cert-manager ships a single versioned manifest); FLO/CIS via the
  **helm Go SDK** (`helm.sh/helm/v3` action + registry packages) for OCI
  pull + runtime templating + values. This keeps helm only where runtime
  chart templating is genuinely required, and removes the host `helm` binary.
- **Alternative A (no helm dep):** render FLO/CIS charts with helm's engine
  libs and SSA-apply — still pulls in helm libraries, loses release state.
- **Alternative B (lightest dep):** keep the three chart installs in
  terraform but switch the FLO/CIS `null_resource` to the proper
  `helm_release` provider (real waits), and have the native reconciler
  replace ONLY the `curl`/`time_sleep` CR-apply parts. Smallest dependency
  footprint; the BNK phase stays split across terraform + Go.

Record the decision (and the `helm.sh/helm/v3` dependency-size implication)
in your closure so staff codes exactly one path. If you pick a path that adds
`helm.sh/helm/v3`, note the `go.mod` blast radius for the integrator.

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
1. Helm strategy pinned with the dependency trade-off documented.
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
