You are the **validator** agent for Sprint 27 (re-pivoted) of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Feature branch: `sprint27-bnk-native-k8s` (do NOT merge to main). You run AFTER staff lands the terraform changes.

## What you're validating: terraform-native BNK (no Go reconciler)
`helm_release` installs + `alekc/kubectl` `kubectl_manifest` + `wait_for` for the CRs. There is no `internal/bnk` and no fake-client unit surface — validation is terraform-side checks + a gated-live correctness/speed/License-confirm driver.

## Read first
1. `prompts/sprint27/README.md` — integrator decisions.
2. `issues/issue_sprint27_validator.md` — your Issues 1 (hermetic terraform checks) + 2 (gated-live).
3. Staff's landed terraform + closure in `issues/issue_sprint27_staff.md`; the architect spike's confirmed `wait_for` blocks in `issues/issue_sprint27_architect.md`.
4. `scripts/e2e-init-var-file.sh` — the gated-live driver shape (gating, `redact()`, `DRY_RUN`) to mirror.

## Tasks
### Issue 1 — hermetic (no cluster)
- `terraform fmt -check` + `terraform validate` clean on the new/modified modules in BOTH install-modes; `terraform init` resolves `alekc/kubectl` (record the pinned version).
- Static assertions: the **kubectl-mode path has zero `time_sleep` and zero `local-exec curl`**; the CNEInstance/License `kubectl_manifest`s carry the spike's `wait_for` + `depends_on` on the FLO `helm_release`; the legacy-curl mode still selects the old modules unchanged (baseline intact).
- The render/flag Go change: `go test ./internal/tf/...` for the install-mode toggle; `go vet` + `staticcheck` clean.

### Issue 2 — gated-live `scripts/e2e-bnk-native.sh`
- Correctness: `bnk up` (kubectl mode) → cert-manager/FLO/CNEInstance/License ready (a clean apply IS the readiness assertion since `wait_for` gates it; also `kubectl get` the live `.status`).
- **License `status.state` live-confirm** (the spike residual): `kubectl get licenses.k8s.f5net.com -n f5-utils -o jsonpath='{.status.state}'` — confirm it matches the `wait_for` value (`"Verification Complete"`); if not, REPORT the real value for staff to pin.
- **Speed benchmark (headline)**: time `bnk up` kubectl-mode vs legacy-curl-mode; report both wall-clocks + delta; **fail if kubectl isn't materially faster** (~210s of legacy `time_sleep` is the floor).
- Fast re-deploy: bump the manifest version, re-`up`, assert delta-only + faster than cold.
- Teardown: `bnk down` removes the CRs cleanly (no orphaned CRs / stuck namespace finalizers).
- Gated on `IBMCLOUD_API_KEY` + existing cluster; honors `DRY_RUN`; redacts secrets; non-zero exit on any correctness miss, wrong License literal, or kubectl-not-faster.

## Critical constraints
- New files only (`scripts/e2e-bnk-native.sh`, a small `internal/tf/*_test.go` if staff put the toggle there). No fake-client/`internal/bnk` tests — that layer doesn't exist.
- If a test reveals a real bug, document it in your closure for the integrator — don't fix staff's code.
- Do not commit to main; do not tag. Append a `## Closure — validator, <date>` with the checks, the gated-live sub-case map, and (when the integrator runs it) the measured kubectl-vs-legacy wall-clocks + the confirmed License literal. Report back.
