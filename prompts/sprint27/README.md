# Sprint 27

**Theme:** Replace the terraform-driven BNK-phase Kubernetes work (`null_resource` + `local-exec` + raw `curl` server-side-apply + static `time_sleep`) with a **native, watch-driven reconciler inside roksbnkctl** (`internal/bnk` on top of `internal/k8s`). The headline goal is **SPEED** — make a new BNK-version install deploy + test in a tight iteration loop, by watching real status conditions instead of sleeping and by running independent steps concurrently.

_Filed 2026-06-04 as an integrator architecture request. The BNK phase today is mostly terraform shelling out: the FLO module alone is ~1,200 lines of `local-exec` `helm` + raw `curl` apply, and readiness is gated by ~210s of pure `time_sleep` (cert 30 + FLO SCC 30 + FLO pods 60 + CNE CRD 30 + CNE SCC 30 + License CRD 30) plus helm `--wait` slack and `curl` retry loops — almost all dead waiting even when the cluster is ready in seconds. roksbnkctl already ships client-go v0.30 with a dynamic client + server-side apply (`internal/k8s/apply.go`), so the watch-driven replacement is a natural fit._

## Integrator decisions baked in (do not relitigate)

1. **In-CLI reconciler, NOT an in-cluster operator.** roksbnkctl's binary drives `bnk up`/`bnk down`. No new operator image/CRD/RBAC. (FLO already reconciles the CNE lifecycle in-cluster; this sprint replaces only the terraform *bootstrap*.)
2. **Speed is the primary success metric.** Zero fixed-sleep slack (every wait returns the instant its watched condition is true), independent steps run concurrently (`errgroup`, per the architect's dependency DAG), a fast re-deploy path reconciles only the delta on a version bump, and per-phase timings are reported. The validator's gated-live benchmark must show the native path **materially faster** than terraform.
3. **Reuse `internal/k8s`** (dynamic SSA, field-manager `roksbnkctl`) for every CR/Secret/RBAC/NAD/Job. No reintroducing `curl` or `kubectl` shell-outs.
4. **Keep non-K8s resources in terraform.** The IBM IAM trusted-profile/link/policy in the FLO module and the COS JWT/FAR reads are IBM Cloud API, not Kubernetes — they stay in terraform this sprint.
5. **Helm stays in terraform (RESOLVED).** Convert the three chart installs (cert-manager, FLO, CIS) from `null_resource local-exec helm` to the proper `helm_release` provider with `wait = true`; FAR version-discovery stays terraform-side. **No `helm.sh/helm/v3` Go dependency.** Native Go replaces ONLY the `curl`-applied custom resources + their `time_sleep` gates. The architect draws the exact terraform↔Go handoff boundary (which curl'd resources are helm prerequisites vs post-install CRs); the existing curl modules stay intact as the validator's benchmark baseline.
6. **Legacy terraform BNK path stays behind a flag** this sprint (`--native` / `--legacy-tf` or a `bnk.native_k8s` config toggle). Supports A/B during bring-up and the "don't merge to main until confident" constraint. A later sprint deletes the terraform k8s modules once native is the default.
7. **`live-verify-high-issues` applies** — cluster-mutating + `up`-affecting. The integrator runs the gated-live correctness + speed benchmark before closing.

## Per-role scope

See `docs/PLAN.md` Sprint 27 block + `issues/issue_sprint27_<role>.md` for full detail.

| Role | Scope |
|---|---|
| **Architect** | BLOCKING design inputs: (1) the terraform↔Go **handoff boundary** (which curl'd resources are helm prerequisites that stay terraform vs post-install CRs that move to Go; the no-ping-pong check; how to keep the legacy curl modules as the benchmark baseline); (2) each gated CRD's exact ready-signal, confirmed against real CRDs — esp. CNEInstance + License (`.status` shape); (3) watch-helper API + `ProgressReporter` event shape (incl. `duration`); (4/Issue 5) the safe-parallelism dependency DAG for the Go CR layer. Plus the BNK-phase book chapter + a "why we left terraform local-exec curl" concept note. No Go. |
| **Staff** | All Go. `internal/k8s/wait.go` watch/wait primitives (CRD Established, Deployment Ready, generic CR status, Job complete — actionable timeouts). `internal/bnk` reconciler porting the four terraform modules' k8s ops as apply→watch, built for speed (errgroup parallelism, zero fixed sleeps, fast-redeploy delta path, warm clients, timing instrumentation). Orchestration seam in `RunTrialUp` + terraform gating (`deploy_bnk=false`/`deploy_cert_manager=false` for the native path), `bnk status`, native `bnk down`, the legacy flag. |
| **Validator** | Hermetic tests against client-go **fake** dynamic/typed clients (drive `.status` via the tracker): wait-layer timeouts/short-circuits, reconciler ordering invariants + idempotence + teardown + failure path. Gated-live `scripts/e2e-bnk-native.sh`: correctness + **the speed benchmark (native vs terraform wall-clock, native must be materially faster)** + fast-re-deploy timing. |
| **Tech-writer** (light, runs after) | Drift sweep: `bnk up/down/status --help` vs binary; BNK-phase chapter transcript re-capture; any speedup numbers must trace to the validator's measured benchmark; sweep stale "terraform-driven/`time_sleep`" BNK prose; user-facing CHANGELOG noting the legacy flag. GREEN/RED verdict. |

## Constraints (binding on every role)

- Repo root: `/mnt/d/project/roksbnkctl`. **Feature branch `sprint27-bnk-native-k8s` — do NOT merge to main until the integrator's live correctness + speed verify is GREEN.**
- No `curl`/`kubectl` shell-outs from Go; helm only via the Go SDK if the architect picks it.
- Don't touch the IBM-Cloud (non-k8s) terraform resources; keep the legacy terraform BNK path working behind the flag.
- Staff writes no `_test.go`; validator writes only new test files.
- Do NOT tag a release; the integrator cuts. Do not commit to main; the integrator integrates on the feature branch. No `gh issue create`.
- Hermetic tests use fake clients / `t.TempDir()`; the gated-live driver is operator-run only.
