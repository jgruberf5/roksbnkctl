You are the **validator** agent for Sprint 27 of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Feature branch: `sprint27-bnk-native-k8s` (do NOT merge to main). You run with no memory of prior conversation, AFTER staff has landed the reconciler.

## Read first (in this order)
1. `prompts/sprint27/README.md` — integrator decisions (SPEED is the primary success metric).
2. `issues/issue_sprint27_validator.md` — your full issue (Issue 1 hermetic + Issue 2 gated-live speed benchmark).
3. Staff's landed code + closure in `issues/issue_sprint27_staff.md` — the EXACT API you test against: `internal/k8s/wait.go` (`WaitCRDEstablished`, `WaitDeploymentReady`, `WaitResourceCondition`, `WaitResourceJSONPath`, `WaitJobComplete`), `internal/bnk` (`Reconciler`, `ProgressReporter`), the `bnk status`/`--native` CLI.
4. The architect's pinned CRD ready-signals + parallelism DAG in `issues/issue_sprint27_architect.md` — so your tests assert the right `.status` and the right ordering invariants.
5. `scripts/e2e-init-var-file.sh` — the gated-live driver shape (gating, `redact()`, `DRY_RUN`) to mirror.

## Tasks
### Issue 1 — hermetic (client-go fake clients; drive `.status` via the tracker)
- `internal/k8s/wait_test.go`: each wait helper returns on the real condition, short-circuits when already satisfied, and returns an **actionable timeout naming the resource + last status** on ctx deadline (assert the message shape — acceptance-critical). Use `k8s.io/client-go/dynamic/fake` + `kubernetes/fake`; flip `.status` mid-watch via reactors / `Tracker()`.
- `internal/bnk/*_test.go`: reconcile against fake clients applies the expected GVK set in valid order + emits expected `ProgressReporter` events (with non-zero `duration`); idempotent short-circuit on a second run; **ordering invariants** on hard serial edges (cert-manager CRDs → issuers; FLO → CNEInstance → License) hold while independent steps may interleave; reverse-order `Destroy`; failure path surfaces the actionable timeout.

### Issue 2 — gated-live `scripts/e2e-bnk-native.sh`
- Correctness: `bnk up --native` against a real cluster → cert-manager/FLO/CNEInstance/License all reach ready (query live `.status`).
- **Speed benchmark (headline)**: time native `bnk up` vs a terraform-path baseline; report both wall-clocks + delta; **fail if native is not materially faster** (the ~210s terraform `time_sleep` is the floor). Capture the reconciler's per-phase timings.
- Fast re-deploy: bump the manifest version (or re-run) and time it — assert markedly faster than cold `up`, delta-only.
- Timeout behavior (short `--timeout` → actionable message + non-zero exit); `bnk down --native` removes everything.
- Gated on `IBMCLOUD_API_KEY` + existing cluster; honors `DRY_RUN`; redacts secrets; non-zero exit on any miss OR if native isn't faster.

## Critical constraints
- New test files only; no edits to pre-existing `_test.go`. Hermetic tests use fake clients (no live cluster).
- If a test reveals a real production bug, document it in your closure for the integrator — do NOT fix staff's code yourself.
- `go test ./...` PASS; `go vet ./...` + `staticcheck ./...` clean before you close.
- Do not commit to main; do not tag. Append a `## Closure — validator, <date>` with the sub-case → assertion map, the `go test` output, and (when the integrator runs it) the measured native-vs-terraform wall-clock numbers.
