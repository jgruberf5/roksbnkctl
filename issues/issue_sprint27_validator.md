# Sprint 27 — validator issues (terraform-native BNK: validate + gated-live correctness/speed benchmark + License live-confirm)

> **Sprint 27 frame (re-pivoted 2026-06-04).** The BNK phase becomes
> terraform-native — `helm_release` installs + `alekc/kubectl`
> `kubectl_manifest` + `wait_for` for the CRs (architect spike GO). There is no
> Go reconciler, so the fake-client unit-test surface from the original plan is
> gone. Validator's job: light terraform-side checks, and the gated-live
> proof that the new path is **correct, materially faster than the legacy
> baseline, and that the License `wait_for` literal is right**.

`Status`: open (re-pivoted)

---

## Issue 1 — Terraform-side checks (hermetic)

**Severity**: medium
**Status**: open

No live cluster needed:
- `terraform fmt -check` + `terraform validate` clean on the new/modified
  modules (both install-mode branches parse).
- `terraform init` resolves the `alekc/kubectl` provider (confirm the version
  constraint + that roksbnkctl's init path fetches it; record the provider
  version pinned).
- A static assertion that the **kubectl-mode path contains zero `time_sleep`**
  and zero `local-exec curl` (grep the rendered/selected module), and that the
  CNEInstance/License `kubectl_manifest` resources carry the spike's `wait_for`
  blocks + the `depends_on` on the FLO `helm_release`.
- Confirm the legacy-curl mode still selects the old modules unchanged (the
  benchmark baseline is intact).
- The small roksbnkctl render/flag Go change: `go test ./internal/tf/...`
  asserts the install-mode toggle renders correctly; `go vet` + `staticcheck`
  clean.

## Issue 2 — Gated-live: correctness + **speed benchmark** + License confirm

**Severity**: high (the speedup is the sprint's primary success metric)
**Status**: open

`scripts/e2e-bnk-native.sh` (new; mirrors the gating + `redact()` + `DRY_RUN`
shape of `scripts/e2e-init-var-file.sh`), against an existing cluster phase:

1. **Correctness**: `bnk up` in kubectl mode brings up cert-manager → FLO →
   CNEInstance → License; assert each reaches ready (the CRs are in terraform
   state and the apply only succeeds once `wait_for` is satisfied — so a clean
   apply IS the readiness assertion; additionally `kubectl get` the live
   `.status` to double-check CNEInstance `Available=True` and License
   `status.state`).
2. **License `status.state` live-confirm** (the one residual from the spike):
   capture the actual terminal `.status.state` literal on the licensed cluster
   (`kubectl get licenses.k8s.f5net.com -n f5-utils -o jsonpath='{.status.state}'`)
   and confirm it matches the `wait_for` value (`"Verification Complete"`); if it
   differs, report the real value so staff can pin it (or switch to the
   `conditions[]` matcher).
3. **Speed benchmark** — the headline. Time `bnk up` in **kubectl mode** vs
   **legacy-curl mode** on equivalent clusters; report both wall-clocks + the
   delta. The kubectl path must be **materially faster** (the ~210s of legacy
   `time_sleep` is the floor). **Fail the driver if kubectl mode is not faster.**
4. **Fast re-deploy**: with BNK up, bump `f5_bigip_k8s_manifest_version` and time
   `bnk up` again — assert the terraform plan/apply touches only the delta
   (changed `helm_release` + CNEInstance) and is markedly faster than a cold up.
5. **Teardown**: `bnk down` (terraform destroy) removes the CRs cleanly; assert
   the CNEInstance/License/issuers are gone (no orphaned CRs, no stuck namespace
   finalizers).

Gated on `IBMCLOUD_API_KEY` + an existing cluster; honors `DRY_RUN`; redacts
secrets; **exits non-zero on any correctness miss, on a wrong License literal,
or if kubectl mode isn't faster than legacy**.

### Acceptance criteria
1. Terraform `fmt`/`validate`/`init` clean; no-`time_sleep`/no-`curl` assertion
   on the kubectl path; legacy baseline intact; the render/flag Go change
   tested + vet/staticcheck clean.
2. Gated-live proves correctness, **measures kubectl-vs-legacy wall-clock with
   kubectl materially faster**, confirms the License `status.state` literal, and
   proves clean teardown + fast re-deploy.

### Files affected
- **New**: `scripts/e2e-bnk-native.sh`; a small `internal/tf/*_test.go` for the
  install-mode render (if staff adds the toggle there).
- No fake-client / `internal/bnk` tests — that layer doesn't exist (terraform
  owns the CRs).

### Related
- `issues/issue_sprint27_staff.md` — the terraform surface under test.
- `issues/issue_sprint27_architect.md` (Spike rounds 1 & 2) — the confirmed
  `wait_for` blocks + the License-literal residual this driver closes.
- `scripts/e2e-init-var-file.sh` — the gated-live driver shape to mirror.
- Integrator memory [[live-verify-high-issues]] — cluster-mutating; the live
  benchmark + License confirm gate closure.
