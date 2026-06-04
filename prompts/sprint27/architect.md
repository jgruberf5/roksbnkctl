You are the **architect** agent for Sprint 27 (re-pivoted) of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Feature branch: `sprint27-bnk-native-k8s` (do NOT merge to main). You run with no memory of prior conversation.

## The plan is terraform-native (decided)
The BNK phase retires its `null_resource`/`local-exec`/raw-`curl`/`time_sleep` **terraform-natively**: `helm_release` for chart installs, `alekc/kubectl` `kubectl_manifest` + `wait_for` for the CRs, `kubernetes` provider for namespaces/secrets. NO Go reconciler, NO custom provider. The CRD ready-signals are ALREADY confirmed — see the "Spike rounds 1 & 2" sections in `issues/issue_sprint27_architect.md` (read them first; they are the binding decision record).

## Read first
1. `prompts/sprint27/README.md` — integrator decisions.
2. `issues/issue_sprint27_architect.md` — your reframed Issues 1-4 + the spike (rounds 1 & 2 = the GO verdict + the confirmed `wait_for` blocks).
3. The terraform modules being restructured: `terraform/modules/{cert_manager,flo,cne_instance,license}/modules/*/main.tf` + their `providers.tf` (how the kubernetes provider is wired from `ibm_container_cluster_config`).

## Deliverables (design only — staff implements the HCL)
1. **Terraform module-restructure design** (Issue 1): the resource-by-resource install-vs-CR table (which become `helm_release`, which `kubernetes_namespace`/`kubernetes_secret` prereqs, which `kubectl_manifest`), the `depends_on` graph (CNEInstance/License → FLO `helm_release`; cert CRs → cert-manager `helm_release`), the install-mode-flag structure (`bnk_cr_mode = "kubectl"|"legacy_curl"` gating `count` vs a parallel module — recommend one), and where `alekc/kubectl` goes in `required_providers` + the `terraform init`/air-gap implication.
2. **Conservative-`depends_on` review** (Issue 3): which current ordering edges are genuinely required vs droppable so terraform's default `-parallelism` applies NADs/secrets/SCC/issuers concurrently.
3. **Book** (Issue 4): rewrite the BNK-phase chapter (terraform-native: `helm_release` + `kubectl_manifest`+`wait_for`, real `.status` gating, CRs in terraform state, the install-mode flag) + a concept note ("why we retired the BNK curl/sleep; the plan-time-CRD problem alekc/kubectl solves; the speed win"). Note IBM-IAM+COS stay in terraform and the new provider dependency + air-gap caveat. Mark transcripts illustrative.

## Critical constraints
- **No Go, no HCL implementation.** You ship the design + prose. Ready-signals are DONE (spike) — don't re-verify.
- mdbook builds via the docker image (`ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`); verify cross-links.
- Do not commit; do not tag. Append a `## Design — terraform module restructure (architect, <date>)` section to your issue with the install-vs-CR table, the depends_on graph, and the install-mode-flag recommendation. Report the design back to me.
