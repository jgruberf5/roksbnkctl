You are the **staff** agent for Sprint 28 of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Branch: `sprint28-three-phase-split` (stacked on `sprint27-bnk-native-k8s`; do NOT merge to main, do NOT commit/tag). No memory of prior conversation.

## Read first
1. `prompts/sprint28/README.md` — integrator decisions.
2. `issues/issue_sprint28_staff.md` — your Issues 1-4 + acceptance criteria + files affected.
3. `issues/issue_sprint28_architect.md` — the architect's **Design** section (the state layout + migration, the override blocks, the per-phase presence model, the parallelism/teardown ordering, the CLI naming). Follow it.
4. The current 2-phase code you're extending: `internal/config/{paths,tfstate,cluster_outputs}.go`, `internal/orchestration/{lifecycle,second_phase_reuse}.go`, `internal/cli/{cluster_phase,bnk_phase,root}.go`, and `terraform/modules/testing/` (reused standalone).

## Tasks (per the architect design)
1. **Split state** (Issue 1): add the `state-testing/` dir (`internal/config/paths.go`); move the testing jumphosts out of the cluster phase (cluster-phase-override gains `testing_create_*=false`) into the Testing phase; per architect's migration decision, keep BNK in `state/` or introduce `state-bnk/` (+ migration). Write a `testing-phase-override.tfvars` writer (mirror `writeBnkPhaseOverrideAt`): `create_roks_cluster=false`, `use_existing_cluster_vpc=true`, `existing_cluster_vpc_id`, `deploy_bnk=false`, `deploy_cert_manager=false`, `testing_create_*=true`. Ensure `cluster-outputs.json` carries what Testing needs (cluster VPC id + TGW).
2. **Presence model** (Issue 2): generalize `DetectShape` to per-phase presence (cluster?/bnk?/testing?); branch `RunUp`/`RunDown` + the `cluster|bnk|testing up|down` dispatch on it; keep `ShapeLegacySingle`.
3. **Parallel up + testing phase** (Issue 3): `RunTestingUp`/`RunTestingDown` (mirror RunTrialUp/Down against `state-testing/` with the override + handoff; move the `tryAutoJumphost`/`tryAutoClusterJumphosts` SSH-target seeding here). In `RunUp`: Cluster serial-first → BNK ∥ Testing via `golang.org/x/sync/errgroup`, with readable concurrent stderr (architect's approach). New `internal/cli/testing_phase.go` (`testing up/down`) wired in `root.go`; `bnk up/down` → BNK-state only (drop the jumphosts).
4. **Teardown + guards** (Issue 4): `bnk down` (BNK only), `testing down` (testing only); extend the `cluster down` guard to refuse while BNK OR Testing state has resources; bare `down` → composite confirm → BNK ∥ Testing → Cluster; delete `cluster-outputs.json` only on cluster down.

## Constraints
- Orchestration/state/CLI + the testing split only — NO Sprint 27 BNK module-body changes. The `testing` module is reused as-is (pure IBM VPC; no k8s providers in the Testing phase).
- Keep `up`/`down`/`bnk`/`cluster` backward-compatible; don't strand pre-Sprint-28 workspaces.
- No `_test.go` (validator's surface). `go build`/`go vet`/`staticcheck` clean before close.
- Do not commit/tag. Append a `## Closure — staff, <date>` to your issue (the state split, presence model, parallel dispatch, CLI, migration, gate results). Report back.
