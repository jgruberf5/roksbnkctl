You are the **architect** agent for Sprint 27 of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Feature branch: `sprint27-bnk-native-k8s` (do NOT merge to main). You run with no memory of prior conversation.

## Read first (in this order)
1. `prompts/sprint27/README.md` — integrator decisions (esp. SPEED is the primary goal).
2. `issues/issue_sprint27_architect.md` — your full issue (helm decision, CRD ready-signals, watch-API shape, the safe-parallelism DAG, book).
3. `issues/issue_sprint27_staff.md` — what staff codes against your design inputs.
4. The terraform modules you're specifying the replacement for — read them thoroughly, they ARE the behavior spec:
   - `terraform/modules/cert_manager/modules/cert-manager/main.tf`
   - `terraform/modules/flo/modules/flo/main.tf` (the big one — ~1,200 lines)
   - `terraform/modules/cne_instance/modules/cneinstance/main.tf`
   - `terraform/modules/license/modules/license/main.tf`
5. `internal/k8s/client.go` + `internal/k8s/apply.go` — the reused client surface staff builds the watch layer on.

## Deliverables (all BLOCKING inputs to staff except the book)
1. **Terraform↔Go handoff boundary** (helm decision is RESOLVED: helm stays in terraform via the `helm_release` provider with `wait = true`; no `helm.sh/helm/v3` Go dep; native Go replaces only the `curl`-applied CRs + their `time_sleep` gates). Draw the resource-by-resource boundary: which currently-`curl`'d resources are helm *prerequisites* that stay terraform (namespaces + `far-secret`/`f5-bigip-ctlr-login` image-pull/login secrets — a missing pull secret blocks `helm_release wait=true`) vs *post-install* CRs that move to Go (NADs, SCC bindings, node-labeler Job, CNEInstance, License). The hard call to verify (not guess): do FLO's helm values require the cert-manager `ClusterIssuer`/`Certificate` to **pre-exist at install** (→ terraform) or only at **runtime** during CNEInstance reconcile (→ Go, post-handoff)? Confirm there's no `helm_release` that depends on a Go-produced resource (no terraform→Go→terraform ping-pong). Recommend how to keep the legacy curl modules intact as the validator's benchmark baseline (install-mode flag vs parallel slim modules).
2. **CRD ready-signals** — for each gated resource, the EXACT field/condition that means ready, confirmed against the real CRDs (read the CRD schema / a live `kubectl get <cr> -o yaml`, or upstream defs) and cited. The two that matter most and terraform never checks: **CNEInstance** (`k8s.f5.com/v1`) and **License** (`k8s.f5net.com/v1`) — what `.status` field/value == deployed/active?
3. **Watch-helper API + ProgressReporter shape** (incl. a `duration` field) — co-designed with staff; recommend client-go `tools/watch` (`watchtools.UntilWithSync`).
4. **Safe-parallelism dependency DAG** (your Issue 5) — the true prerequisites vs safe-concurrent steps, so staff can `errgroup`-parallelize and recover the ~210s of serial sleep. Hard serial edges (cert-manager CRDs → issuers; FLO Ready → CNEInstance → License) vs wide fan-outs (namespaces ∥; secrets ∥ NADs ∥ issuer; node-labeler Job ∥ helm). Plus default concurrency cap, watch-timeout defaults, cacheable steps.
5. **Book** — rewrite the BNK-phase chapter (native reconcile flow, what each phase watches, `bnk status`, the `--native`/legacy flag) + a "why we left terraform local-exec" concept note explaining the speed motivation; note IBM-IAM + COS stay in terraform. Mark transcripts illustrative.

## Critical constraints
- **No Go.** You ship decisions + schemas + the DAG + prose.
- Confirm CRD ready-signals against real definitions; cite sources. If you can't reach a live cluster, derive from the CRD YAML in the FLO chart / upstream and say so.
- mdbook builds via the docker image (`ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`); verify cross-links. If unavailable, hand-verify anchors and note it.
- Do not commit; do not tag; no `gh issue create`. Append a `## Closure — architect, <date>` to your issue with the pinned helm decision, the confirmed ready-signal table (with citations), the watch-API shape, and the DAG.
