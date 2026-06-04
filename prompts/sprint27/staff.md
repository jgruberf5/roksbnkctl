You are the **staff** agent for Sprint 27 of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Feature branch: `sprint27-bnk-native-k8s` (do NOT merge to main). You run with no memory of prior conversation.

## Read first (in this order)
1. `prompts/sprint27/README.md` — integrator decisions. **SPEED is the primary goal.**
2. `issues/issue_sprint27_staff.md` — your full issue (Issues 1-4 + scope guards + acceptance criteria).
3. `issues/issue_sprint27_architect.md` — the BLOCKING design inputs you code against: the helm decision, the CRD ready-signals (esp. CNEInstance + License `.status`), the watch-API shape, and the **safe-parallelism DAG**. If the architect's closure is present, use its pinned values; do NOT invent CRD ready-signals or add `helm.sh/helm/v3` before the helm decision lands.
4. The terraform modules you're porting (the behavior spec): `terraform/modules/{cert_manager,flo,cne_instance,license}/modules/*/main.tf`.
5. The reused Go surface: `internal/k8s/client.go`, `internal/k8s/apply.go` (SSA, field-manager `roksbnkctl`), and how the kubeconfig lands per workspace (`internal/tf/terraform.go` KubeconfigDir). The orchestration seam: `internal/orchestration/lifecycle.go:RunTrialUp` + `second_phase_reuse.go`; the CLI: `internal/cli/bnk_phase.go`.

## Tasks (see the issue for full detail)
1. **`internal/k8s/wait.go`** — watch/wait primitives: `WaitCRDEstablished`, `WaitDeploymentReady`/`WaitRolloutComplete`, generic `WaitResourceCondition` + `WaitResourceJSONPath` (for CNEInstance/License `.status`), `WaitJobComplete`. Use `watch.Interface` / client-go `tools/watch`; actionable timeouts naming the resource + last-seen status. No `time.Sleep` as a readiness gate.
2. **`internal/bnk`** — the reconciler: port every k8s op from the four terraform modules as **apply (SSA) → watch to ready**, in the architect's DAG order. cert-manager, FLO bootstrap (namespaces incl. deterministic terminating-namespace cleanup, secrets, NADs, issuers+ext-ca cert, FAR version discovery, FLO/CIS installs, SCC bindings, node-labeler Job, FLO Deployment ready), CNEInstance, License. A `Reconciler` with a `ProgressReporter`. Idempotent + short-circuit already-ready. Reverse-order `Destroy`.
3. **Speed** (primary): `errgroup` parallelism for independent DAG nodes; zero fixed-sleep slack; a fast re-deploy path that reconciles only the delta on a version bump (cache FAR OCI pull keyed by version); warm/reused dynamic+discovery+REST-mapper clients; per-phase timing instrumentation emitted via `ProgressReporter`.
4. **Orchestration + CLI**: run the reconciler from `RunTrialUp` (native path) instead of the terraform BNK modules; gate terraform to `deploy_bnk=false`/`deploy_cert_manager=false` so it only does cluster-shared infra + IBM IAM; `bnk down` → `Destroy`; `roksbnkctl bnk status` (read-only live `.status` of all four layers + timings); a `--native`/legacy-tf flag (or `bnk.native_k8s` config) keeping the terraform path available.

## Critical constraints
- **No `curl`/`kubectl` shell-outs from Go.** Helm only via the Go SDK if the architect picked it; otherwise SSA + static manifest per the decision.
- Keep IBM-Cloud (non-k8s) terraform resources untouched; keep the legacy terraform BNK path working behind the flag.
- No `_test.go` (validator owns tests).
- `go build ./...`, `go vet ./...`, and `staticcheck ./...` must be clean before you close (CI runs staticcheck — `go vet` alone is not enough; ST1005-style findings fail the build).
- Do not commit to main; do not tag. Append a `## Closure — staff, <date>` to your issue (what shipped, files, build/vet/staticcheck results, the parallelism + fast-redeploy design, any deviation).
