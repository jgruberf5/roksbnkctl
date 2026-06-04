# Sprint 27 — validator issues (hermetic reconciler tests + gated-live speed benchmark)

> **Sprint 27 frame.** Validator proves the native `internal/bnk` reconciler
> + `internal/k8s/wait.go` watch layer (staff `issue_sprint27_staff.md`)
> behaves correctly AND **measures the speedup** that is the sprint's whole
> point. Two surfaces: hermetic tests against client-go's fake clients, and a
> gated-live e2e that times the native `bnk up` against the terraform
> baseline and exercises the fast re-deploy path.

`Status`: open

---

## Issue 1 — Hermetic tests for the watch layer + reconciler

**Severity**: high
**Status**: open

Use client-go's **fake clients** so watches/conditions are testable without a
live cluster — `k8s.io/client-go/dynamic/fake` (dynamic) and
`k8s.io/client-go/kubernetes/fake` (typed), both support `Watch` + reactors,
and `fakeClient.Tracker()` can be driven to flip a resource's `.status` mid-
watch. envtest is an option only if the team already has it set up; fake
clients are the lighter default.

`internal/k8s/wait_test.go` (new):
- `WaitCRDEstablished`: returns immediately when the CRD already has
  `Established=True`; blocks then unblocks when a reactor flips the condition;
  returns an **actionable timeout** (names the resource + last-seen status)
  when the ctx deadline passes without the condition — assert the message
  shape, this is acceptance-critical.
- `WaitDeploymentReady`: unblocks when `availableReplicas` reaches desired +
  generation current; times out actionably otherwise.
- `WaitResourceCondition` / `WaitResourceJSONPath`: drive a CNEInstance-shaped
  and License-shaped unstructured object's `.status` via the tracker; assert
  it returns on the architect's pinned ready-signal and not before.
- `WaitJobComplete`: flips to `Complete`.

`internal/bnk/*_test.go` (new):
- A reconcile against fake clients applies the expected GVK set (assert SSA
  patches recorded on the tracker for namespaces, secrets, NADs, issuers,
  CNEInstance, License) in the right order, and emits the expected
  `ProgressReporter` events (phase/resource/state + a non-zero `duration`).
- **Idempotence / short-circuit**: a second reconcile against already-ready
  state applies no changes and completes fast (assert no redundant waits).
- **Parallelism correctness**: independent steps may complete in any order;
  hard serial edges (cert-manager CRDs → issuers; FLO → CNEInstance →
  License) are never violated — assert ordering invariants on the recorded
  event stream, not wall-clock.
- **Teardown**: `Destroy` deletes in reverse order; assert delete calls +
  delete-watches.
- **Failure path**: a step whose watched condition never flips surfaces the
  actionable timeout and aborts the reconcile (no silent success).

## Issue 2 — Gated-live e2e: correctness + **speed benchmark** + fast re-deploy

**Severity**: high (the speedup is the sprint's primary success metric)
**Status**: open

`scripts/e2e-bnk-native.sh` (new; mirrors the gating + `redact()` + `DRY_RUN`
shape of `scripts/e2e-init-var-file.sh`):

1. **Correctness**: against a real cluster (an existing cluster phase), run
   `roksbnkctl bnk up --native`; assert cert-manager, FLO, CNEInstance, and
   License all reach ready (query their live `.status`), and that the BNK
   trial actually serves (reuse whatever the existing live verify asserts for
   a healthy BNK).
2. **Speed benchmark** — the headline metric. Time the native `bnk up`
   end-to-end and compare against a terraform-path `bnk up` baseline on an
   equivalent cluster. Report both wall-clocks and the delta. The native path
   must be **materially faster** (the ~210s of terraform `time_sleep` is the
   floor; parallelism + terraform-overhead removal should beat that). Capture
   the per-phase timing breakdown the reconciler emits.
3. **Fast re-deploy**: with BNK already up, bump
   `f5_bigip_k8s_manifest_version` (or re-run unchanged) and time the
   reconcile — assert it's markedly faster than the cold `up` and only
   touches the delta (no full re-bootstrap; unchanged charts not re-pulled).
4. **Timeout behavior**: optionally, point at a deliberately-unsatisfiable
   condition (short `--timeout`) and assert the actionable timeout message +
   non-zero exit.
5. **`bnk down --native`**: tears everything down; assert resources gone.

Gated on `IBMCLOUD_API_KEY` + an existing cluster; honors `DRY_RUN`; redacts
secrets; exits non-zero on any assertion miss or if the native path is NOT
faster than the baseline (the speed gate is an assertion, not a nicety).

### Acceptance criteria
1. Hermetic wait-layer + reconciler tests pass against fake clients, incl.
   the actionable-timeout message shape, idempotent short-circuit, ordering
   invariants, and teardown.
2. Gated-live e2e proves correctness, **measures native-vs-terraform
   wall-clock with the native path materially faster**, and proves the
   fast-re-deploy delta path.
3. `go test ./...` PASS; `go vet` + `staticcheck` clean. New test files only
   (no edits to pre-existing `_test.go`).

### Files affected
- **New**: `internal/k8s/wait_test.go`, `internal/bnk/*_test.go`,
  `scripts/e2e-bnk-native.sh`.

### Related
- `issues/issue_sprint27_staff.md` — the surface under test (esp. Issue 3
  speed + the timing instrumentation the benchmark reads).
- `issues/issue_sprint27_architect.md` — the pinned CRD ready-signals (so
  tests assert the right `.status`) + the parallelism DAG (so ordering
  invariants match).
- `scripts/e2e-init-var-file.sh` — the gated-live driver shape to mirror.
- Integrator memory [[live-verify-high-issues]] — cluster-mutating; the live
  benchmark gates closure.
