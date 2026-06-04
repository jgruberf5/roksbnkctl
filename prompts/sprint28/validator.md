You are the **validator** agent for Sprint 28 of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Branch: `sprint28-three-phase-split` (do NOT merge to main, do NOT commit/tag). You run AFTER staff lands the three-phase split. No memory of prior conversation.

## Read first
1. `prompts/sprint28/README.md` — integrator decisions.
2. `issues/issue_sprint28_validator.md` — your Issue 1 (hermetic) + Issue 2 (gated-live).
3. Staff's `## Closure` in `issues/issue_sprint28_staff.md` — the exact surface: the presence model, the `testing-phase-override` writer, `RunTestingUp/Down`, the errgroup parallel dispatch, the `testing` CLI, the `cluster down` guard.
4. The architect's pinned presence model + override blocks + teardown ordering in `issues/issue_sprint28_architect.md`.
5. `internal/orchestration/second_phase_reuse_test.go` + `internal/config/tfstate_test.go` (existing patterns), `scripts/e2e-init-var-file.sh` (gated-live shape).

## Tasks
### Issue 1 — hermetic
- Presence/shape detection over fabricated state dirs (cluster/bnk/testing present or empty; ShapeLegacySingle; data-source-only no false-positive).
- Byte-exact `testing-phase-override.tfvars` block + the cluster/bnk override updates (cluster-phase-override now has `testing_create_*=false`).
- Dispatch-decision table: presence state → which phases `up`/`down`/`cluster|bnk|testing up|down` act on; the `cluster down` guard refuses when BNK or Testing state has resources.
- Parallel dispatch: `RunUp` launches BNK ∥ Testing after Cluster (recorded order / barrier with injected fakes); errgroup error propagation; ordering invariant (Cluster before both; teardown BNK∥Testing before Cluster).
- `go test ./...` PASS; `go vet` + `staticcheck` clean.

### Issue 2 — gated-live `scripts/e2e-three-phase.sh`
- Parallel up: `up` → Cluster then BNK ∥ Testing; both land; assert concurrency.
- Independent teardown: `bnk down` → BNK gone, **jumphosts still present + SSH targets work**; `bnk up` redeploys BNK reusing the same jumphosts. Symmetric `testing down` leaves BNK.
- `cluster down` guard: refused while BNK/Testing exist; succeeds (and removes cluster-outputs.json) after both are down.
- Reuse-existing-cluster: skip Cluster, deploy BNK + Testing against an existing cluster.
- Migration: a 2-phase-created workspace driven by the 3-phase binary still up/downs.
- Gated on `IBMCLOUD_API_KEY`; honors `DRY_RUN`; redacts; non-zero on any miss; `bash -n` clean.

## Constraints
- New test files only (+ the intended `tfstate_test.go` / `second_phase_reuse_test.go` updates). Hermetic tests use fabricated state dirs / `t.TempDir()`.
- If a check reveals a real bug, document it for the integrator — don't fix staff's code.
- Do not commit/tag. Append a `## Closure — validator, <date>` with the sub-case map + gate results. Report back.
