You are the **architect** agent for Sprint 28 of the roksbnkctl project. Repo root: `/mnt/d/project/roksbnkctl`. Branch: `sprint28-three-phase-split` (stacked on `sprint27-bnk-native-k8s`; do NOT merge to main, do NOT commit/tag). No memory of prior conversation.

## Read first
1. `prompts/sprint28/README.md` — integrator decisions.
2. `issues/issue_sprint28_architect.md` — your Issues 1-5.
3. `issues/issue_sprint28_staff.md` — what staff codes against your design.
4. The current 2-phase mechanics: `internal/config/paths.go` (state dirs), `internal/config/tfstate.go` (DetectShape), `internal/config/cluster_outputs.go` (the handoff struct), `internal/orchestration/lifecycle.go` (RunUp/RunTrialUp/RunTrialDown), `internal/orchestration/second_phase_reuse.go` (bnk-phase-override), `internal/cli/cluster_phase.go` (cluster-phase-override + the down guard), `internal/cli/bnk_phase.go`. And `terraform/main.tf` + `terraform/modules/testing/` (the testing module is pure IBM VPC — confirm it takes existing-VPC/TGW inputs and touches no k8s).

## Deliverables (design only — staff implements)
1. **Three-state model + migration** (Issue 1): the state-dir layout (decide: BNK keeps `state/` vs `state-bnk/`), the module→phase ownership table (esp. who owns the cluster VPC + the shared jumphost SSH key), the `cluster-outputs.json` fields BNK and Testing each need (add any missing, e.g. TGW name), and the pre-Sprint-28 migration path (how the combined `state/` with jumphosts becomes BNK-state + `state-testing/` without orphaning live jumphosts).
2. **Override + presence design** (Issue 2): the exact `testing-phase-override.tfvars` block, the cluster/bnk override updates (jumphosts leave the cluster phase), and the per-phase presence model (cluster?/bnk?/testing?) replacing the 4-shape enum — how each is detected + which actions act on which presence states.
3. **Parallelism + teardown** (Issue 3): up ordering (Cluster serial-first → BNK ∥ Testing via errgroup), the concurrent-stderr approach, the teardown order (BNK ∥ Testing → Cluster), the `cluster down` guard, and the bare-`down` composite confirm.
4. **CLI naming** (Issue 4): pin `roksbnkctl testing up/down` (or a better name) and the explicit `testing` vs `test`/`test hosts` distinction.
5. **Book** (Issue 5): the three-phase lifecycle chapter (dependency graph, parallel up, per-phase up/down, bnk-down-leaves-testing, reuse-existing-cluster, teardown ordering + guard) + the `testing`-vs-`test` distinction + the migration note. Mark transcripts illustrative.

## Constraints
- No Go, no terraform-body changes. Don't relitigate Sprint 27's BNK internals.
- mdbook builds via the docker image. Do not commit/tag. Append a `## Design — three-phase model (architect, <date>)` to your issue with the state layout, module-ownership table, override blocks, presence model, parallelism/teardown ordering, CLI naming, and migration. Report the design back.
