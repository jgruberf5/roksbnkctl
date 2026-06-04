# Sprint 27 (re-pivoted 2026-06-04)

**Theme:** Retire the BNK phase's terraform `null_resource` + `local-exec` + raw `curl` server-side-apply + static `time_sleep` — **terraform-natively**. Per the architect spike (FINAL VERDICT **GO** — `issues/issue_sprint27_architect.md`), keep terraform as the state keeper and replace the brittle parts with proper providers: chart installs → **`helm_release`** (`wait = true`); the `curl`-applied custom resources → **`alekc/kubectl` `kubectl_manifest` + `wait_for`** (CRs as real terraform resources, in state, watching `.status` to ready, no plan-time-CRD requirement); namespaces + secrets → the `kubernetes` provider. **No Go reconciler, no `internal/bnk`, no custom provider.**

**Primary goal: SPEED** — deploy + test a new BNK version in a tight loop. The ~210s of fixed `time_sleep` dies (`wait_for` + `helm_release wait=true` are real-readiness); terraform parallelizes independent resources; and **fast re-deploy comes free** — bump the version and `terraform plan` diffs only the changed CNEInstance spec + helm versions. Terraform *is* the delta engine, so this is *less* code than a reconciler.

_The original framing (a native Go watch-reconciler in `internal/bnk`) was set aside after the spike proved `alekc/kubectl` `kubectl_manifest` + `wait_for` covers apply + state + delete + status-wait for every BNK CR — CNEInstance (`status.conditions[type=Available]==True`) and License (`status.state` / `conditions[]`), both confirmed from the FAR-shipped CRDs._

## Integrator decisions baked in (do not relitigate)

1. **GO on `alekc/kubectl`** for the CR layer. No custom provider, no `internal/bnk`, no `internal/k8s/wait.go`.
2. **`helm_release`** (`wait = true`) for cert-manager + FLO + CIS; FAR version-discovery stays terraform-side. **No `helm.sh/helm/v3` Go dep.**
3. **`kubernetes` provider** for the helm prerequisites (namespaces + `far-secret`/`f5-bigip-ctlr-login` secrets — must precede the charts).
4. **Legacy `curl` modules stay intact behind an install-mode flag** (`bnk_cr_mode = "kubectl" | "legacy_curl"`) as the validator's benchmark baseline.
5. **Keep IBM IAM trusted-profile + COS reads in terraform** (unchanged).
6. **Ready-signals are confirmed** (spike): CNEInstance `wait_for { condition { type="Available" status="True" } }`; License `wait_for { field { key="status.state" value="Verification Complete" } }` — the literal is confirmed live by the validator.
7. **`live-verify-high-issues` applies** — cluster-mutating. The integrator runs the gated-live correctness + speed benchmark before closing/merging.
8. **Feature branch `sprint27-bnk-native-k8s` — NOT merged to main until the integrator is confident.**

## Per-role scope

See `docs/PLAN.md` Sprint 27 block + `issues/issue_sprint27_<role>.md` for detail.

| Role | Scope |
|---|---|
| **Architect** | The terraform module-restructure design (install-vs-CR resource table, the `depends_on` graph, the install-mode-flag structure, `required_providers` + air-gap note); a conservative-`depends_on` review so terraform parallelizes; the BNK-phase book chapter + concept note. Ready-signals already done (spike). No Go, no HCL implementation. |
| **Staff** | All terraform HCL: `helm_release` install layer + `kubernetes_namespace`/`kubernetes_secret` prereqs; `kubectl_manifest` + `wait_for` CR layer (CNEInstance/License/issuers/NADs/SCC/Job — specs reused verbatim); `alekc/kubectl` in `required_providers`; the install-mode flag gating legacy vs new. Plus the small roksbnkctl Go change to render the toggle (`internal/tf/vars.go`, `internal/cli/bnk_phase.go` `--legacy-bnk`) and optional light `bnk status`. No Go reconciler. |
| **Validator** | Hermetic terraform checks (`fmt`/`validate`/`init` resolves `alekc/kubectl`; no-`time_sleep`/no-`curl` assertion on the kubectl path; legacy baseline intact; render-toggle Go test). Gated-live `scripts/e2e-bnk-native.sh`: correctness + **the License `status.state` live-confirm** + **the speed benchmark (kubectl vs legacy wall-clock — kubectl must be materially faster, asserted)** + fast-re-deploy + clean teardown. |
| **Tech-writer** (light, runs after) | Drift sweep: install-mode surface vs binary; BNK-phase chapter reconciled to the terraform-native reality (`kubectl_manifest`+`wait_for`, CRs in state); speed numbers trace to the validator's measured kubectl-vs-legacy benchmark; sweep stale `curl`/`time_sleep` BNK prose; document the new `alekc/kubectl` provider dependency + air-gap caveat; user-facing CHANGELOG. GREEN/RED verdict. |

## Constraints (binding on every role)

- Repo root: `/mnt/d/project/roksbnkctl`. **Feature branch `sprint27-bnk-native-k8s` — do NOT merge to main until the integrator's live correctness + speed verify is GREEN.**
- Terraform-native: no Go reconciler, no `internal/bnk`, no custom provider, no `curl`/`kubectl`/`helm` shell-outs (helm is `helm_release`; CRs are `kubectl_manifest`). No `helm.sh/helm/v3` Go dep.
- CR specs port **verbatim** into `yaml_body` — change the apply mechanism, not the manifests. Keep IBM IAM + COS in terraform; keep the legacy curl modules behind the flag.
- Do NOT tag a release; the integrator cuts. Do not commit to main; the integrator integrates on the feature branch. No `gh issue create`.
- The gated-live driver is operator-run only.
