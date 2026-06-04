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

---

## Closure — validator, 2026-06-04

Validated staff's terraform-native BNK landing on branch
`sprint27-bnk-native-k8s` (HEAD `a6d3763`). Not merged, not tagged, no fixes
applied to staff's code (per constraint). New files only: a Go render test, a
hermetic static checker, and the gated-live driver.

### Issue 1 — hermetic (no live cluster) — GREEN

| Gate | Result |
|------|--------|
| `terraform fmt -check -recursive terraform/` | clean (exit 0) |
| `terraform validate` — root `terraform/` | **Success** |
| `terraform validate` — each of cert_manager / cne_instance / flo / license inner modules | **Success** (all 4) |
| `go test ./...` | PASS (incl. new `internal/tf/vars_crmode_test.go`) |
| `go vet ./...` | clean |
| `staticcheck ./...` | clean |
| `bash -n scripts/check-bnk-tf-modes.sh` | clean |

`terraform validate` is mode-agnostic — the `count`/`for_each` gating on
`local.use_kubectl` / `local.use_legacy` is structurally checked regardless of
the `bnk_cr_mode` value, so the single pass covers BOTH modes (matches staff's
note). Tooling: terraform v1.15.2, go1.26.3.

**Resolved `alekc/kubectl` provider version: `2.4.1`** (constraint `>= 2.4.0`),
confirmed in `terraform/.terraform.lock.hcl` AND fetched into
`.terraform/providers/registry.terraform.io/alekc/kubectl/2.4.1/`. Co-resolved:
`hashicorp/helm 2.17.0` (constraint `~> 2.12`), `hashicorp/kubernetes 3.2.0`
(`>= 2.25`). `terraform init` resolves all three from the public registry with
no roksbnkctl-side pin blocking it. (Air-gap caveat per staff/architect stands:
offline runners need a `terraform providers mirror` bundle; the lockfile records
the hashes for a reproducible mirror — integrator should commit it.)

**Static assertions** — `scripts/check-bnk-tf-modes.sh` (new; hermetic
grep/awk block-parser over the 4 inner module main.tf files), all GREEN:
- **C1** — all **6** `time_sleep` resources gated `local.use_legacy` ⇒ **zero
  `time_sleep` in the kubectl path** (the ~210s of fixed sleeps —
  `cert_manager_ready`, `wait_for_flo_scc_policies`, `wait_for_flo_pods`,
  `wait_for_cneinstance_crd`, `wait_for_scc_policies`, `wait_for_license_crd` —
  are all legacy-gated).
- **C2** — all **24** kube-API-mutating curl/`local-exec` `null_resource`s
  gated `local.use_legacy` ⇒ **zero curl CR-apply in the kubectl path**. (The
  FAR auth-archive COS download, the tgz extractor, and the FLO/CIS
  version-discovery helm-pull shell — `far_archive_download`,
  `cne_far_tgz_extractor`, `extract_flo_version` — are correctly NOT
  use_legacy-gated: they curl COS / run `helm pull`, not the kube API, and run
  in BOTH modes per the architect's "version discovery stays terraform-side"
  design. The checker discriminates on `var.kube_host` / `kubectl apply` rather
  than on "any local-exec".)
- **C3** — all **23** new `helm_release` / `kubernetes_namespace_v1` /
  `kubernetes_secret_v1` / `kubectl_manifest` resources gated
  `local.use_kubectl` (count or for_each) ⇒ none instantiate in legacy mode.
- **C4** — CNEInstance `kubectl_manifest` carries `wait_for { condition { type
  = "Available" status = "True" } }` + `depends_on` on the FLO helm_release
  (via `var.flo_deployment_dependency`); License `kubectl_manifest` carries
  `wait_for { field { key = "status.state" value = "Verification Complete" } }`
  + `depends_on` on `var.cneinstance_dependency` (which itself depends on the
  FLO chart). Matches the architect spike's confirmed signals.
- **C5** — legacy baseline intact: the legacy curl `null_resource`s
  (`cneinstance`, `bnk_license`) and the CRD-wait `time_sleep`s
  (`wait_for_cneinstance_crd`, `wait_for_license_crd`) are still present (not
  deleted). `git diff main...HEAD` confirms the legacy resource bodies changed
  ONLY in their `count`/`for_each` gating line (`var.enabled ? N : 0` →
  `local.use_legacy ? N : 0`) + comments — provisioner/curl/trigger bodies are
  byte-intact, so the benchmark baseline is the unchanged original mechanism.

**Go render test** — `internal/tf/vars_crmode_test.go` (new; mirrors
`vars_test.go`): asserts `BNKCfg.CRMode` renders `bnk_cr_mode` correctly —
unset ⇒ **no** line (upstream `kubectl` default, older configs byte-identical),
`"kubectl"` ⇒ `bnk_cr_mode = "kubectl"`, `"legacy_curl"` ⇒
`bnk_cr_mode = "legacy_curl"`; emitted exactly once; flows through BOTH the
sparse and the prefix-driven (full) render paths; api_key never rendered. PASS.

### Issue 2 — gated-live driver — written, `bash -n` clean, ready for integrator

`scripts/e2e-bnk-native.sh` (new; mirrors `e2e-init-var-file.sh` gating /
`redact()` / `DRY_RUN` / EXIT-trap / sentinel-leak-proof). Operator-run,
cluster-mutating, against an EXISTING cluster (the cluster phase is NOT
provisioned by the driver — too slow/costly; it exercises only the BNK trial
layer). `bash -n` clean; `DRY_RUN=1` renders every step with zero
cloud/cluster calls (verified). Gated on `IBMCLOUD_API_KEY` +
`WORKSPACE_KUBECTL` (a workspace already attached to a cluster).

Gated-live sub-case map:

| Case | What it asserts | Fail condition |
|------|-----------------|----------------|
| **S1** correctness | `bnk up` kubectl-mode clean apply (= readiness, since `wait_for` gates it) + `kubectl get` CNEInstance `.status.conditions[Available]==True` | apply non-zero, or Available != True |
| **S2** License literal | `kubectl get licenses.k8s.f5net.com -n f5-utils -o jsonpath='{.status.state}'` == `"Verification Complete"` | mismatch → prints the REAL value LOUDLY + the exact main.tf field to pin + FAILs |
| **S3** speed benchmark | times kubectl-mode vs `--legacy-bnk` cold up; prints both wall-clocks + delta | kubectl not faster by `>= SPEED_MIN_DELTA_S` (default 60s; ~210s legacy `time_sleep` is the floor) |
| **S4** fast re-deploy | bump `f5_bigip_k8s_manifest_version` via a `--var-file`, re-`up`; assert re-up faster than cold | re-up `>=` cold (no delta-only speedup) — skipped with a warning if `MANIFEST_BUMP_VERSION` unset |
| **S5** teardown | `bnk down`; assert no orphaned CNEInstance/License + `f5-utils`/`f5-bnk` namespaces not stuck `Terminating` | any CR survives, or a namespace stuck on finalizers |

Knobs: `WORKSPACE_KUBECTL` (required), `WORKSPACE_LEGACY` (optional 2nd
cluster for a parallel S3 leg; if unset, S3 runs the legacy leg sequentially in
the same workspace — valid but serial), `MANIFEST_BUMP_VERSION` (S4),
`SPEED_MIN_DELTA_S`, `LICENSE_NS`/`LICENSE_NAME`/`FLO_NS`, `EXPECT_LICENSE_STATE`.

**The live benchmark wall-clocks + the License `status.state` literal confirm
are the INTEGRATOR's run** (cluster-mutating; no live cluster here). When the
integrator runs it, record: (a) the measured kubectl-vs-legacy seconds + delta,
and (b) the confirmed live `.status.state` literal — if it is NOT
`"Verification Complete"`, S2 fails loudly with the real value and staff must
pin the `kubectl_manifest.bnk_license` `wait_for.field.value` (or switch to a
`status.conditions[]` matcher).

### Real bugs found

**None.** Staff's terraform + Go match the architect spike and the staff
closure exactly: every `time_sleep`/kube-curl is `use_legacy`-gated, every new
resource is `use_kubectl`-gated, both CRs carry the correct `wait_for` +
`depends_on`, the node-labeler Job has the stable name + `ttlSecondsAfterFinished`
+ `wait_for Complete`, and the render/flag wiring is correct. The only
deviations are staff's documented ones (helm pinned `~> 2.12` to stay on the
v2 nested-`kubernetes{}` block; `kubernetes_*_v1` suffixed names) — both benign.
The single open item is the **License literal**, which is a live-value lookup
the spike already flagged and S2 of the driver closes — not a code bug.

### New files
- `internal/tf/vars_crmode_test.go` — install-mode render test.
- `scripts/check-bnk-tf-modes.sh` — hermetic HCL static checker (C1–C5).
- `scripts/e2e-bnk-native.sh` — gated-live correctness/speed/License driver.
