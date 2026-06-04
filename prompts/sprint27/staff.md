You are the **staff** agent for Sprint 27 (re-pivoted) of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Feature branch: `sprint27-bnk-native-k8s` (do NOT merge to main). You run with no memory of prior conversation.

## What you're building: terraform-native, NOT a Go reconciler
Retire the BNK phase's `null_resource`/`local-exec`/raw-`curl`/`time_sleep` by converting the terraform modules to proper providers. NO `internal/bnk`, NO `internal/k8s/wait.go`, NO custom provider, NO `helm.sh/helm/v3`.

## Read first
1. `prompts/sprint27/README.md` — integrator decisions.
2. `issues/issue_sprint27_staff.md` — your Issues 1-4 + acceptance criteria + files affected.
3. `issues/issue_sprint27_architect.md` — the architect's module-restructure design + the **Spike rounds 1 & 2** (the confirmed `wait_for` blocks for CNEInstance + License; read these — they give the exact HCL). If the architect's design section is present, follow it.
4. The modules you're converting: `terraform/modules/{cert_manager,flo,cne_instance,license}/modules/*/main.tf` (+ `providers.tf`).

## Tasks
1. **Install layer** (Issue 1): convert cert-manager / f5-lifecycle-operator / f5-bnk-cis from `null_resource local-exec helm` to **`helm_release`** (`wait = true`; values ported from the existing `--set`/values; FAR version-discovery stays terraform-side feeding `version`). Move the `f5-utils`/`flo` namespaces + `far-secret`/`f5-bigip-ctlr-login` secrets from `curl` to `kubernetes_namespace`/`kubernetes_secret` (ordered before the charts). Wire the `helm`/`kubernetes` providers from `ibm_container_cluster_config`.
2. **CR layer** (Issue 2): add `alekc/kubectl` to `required_providers`; replace each `null_resource`+`curl` CR apply (and its `time_sleep`) with a **`kubectl_manifest`** (`server_side_apply = true`, `yaml_body` from the existing `*_manifest` local — SPECS UNCHANGED) + the spike's `wait_for`: CNEInstance `condition{type="Available" status="True"}`; License `field{key="status.state" value="Verification Complete"}` (validator confirms the literal live); cert `Certificate`/issuers `condition{type="Ready"}`; node-labeler `Job` `condition{type="Complete"}`; NADs + SCC bindings as plain `kubectl_manifest` (no wait). `depends_on` on the FLO `helm_release` for CRD-before-CR. Drop the namespace finalizer-strip curl dance.
3. **Install-mode flag + roksbnkctl glue** (Issue 3): a `bnk_cr_mode = "kubectl"|"legacy_curl"` variable gating new vs legacy resources (legacy curl modules stay intact as the validator baseline). Render it from a workspace-config toggle / `--legacy-bnk` flag (`internal/tf/vars.go`, `internal/cli/bnk_phase.go`). Confirm roksbnkctl's `terraform init` fetches `alekc/kubectl`; note the air-gap mirror implication. Optional light `bnk status` only if cheap.
4. **Speed** (Issue 4): zero `time_sleep` in the kubectl path (grep to confirm); minimal `depends_on` so terraform parallelizes independents (per architect's review); verify a version-bump plan is minimal (no `kubectl_manifest` perpetual-diff — use `server_side_apply` + field-manager correctly).

## Critical constraints
- No Go reconciler / custom provider / shell-outs. CR specs port verbatim into `yaml_body`. Keep IBM-IAM + COS in terraform. Keep legacy curl behind the flag.
- No `_test.go` (validator's surface) beyond what the issue assigns; the render-toggle test is validator's.
- `terraform fmt -check` + `terraform validate` clean on both modes; `go build ./...` / `go vet ./...` / `staticcheck ./...` clean for the render/flag Go change.
- Do not commit to main; do not tag. Append a `## Closure — staff, <date>` to your issue (HCL changes, the install-mode structure, build/validate results, any deviation). Report back.
