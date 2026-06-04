# Sprint 28

**Theme:** Split roksbnkctl's two-phase model (cluster + trial=BNK+testing) into **three independent phases — Cluster / BNK / Testing** — that deploy in parallel (BNK ∥ Testing after Cluster) and tear down independently (`bnk down` leaves the jumphosts for reuse, and vice versa).

_Surfaced 2026-06-04 during the Sprint 27 live verify. Today "trial" lumps BNK (cert-manager + FLO + CNEInstance + License) and the testing jumphosts into one `state/`, so you can't tear down BNK while keeping the jumphosts, and the two unrelated workloads can't deploy concurrently. Decomposition: BNK depends fully on the Cluster (k8s); Testing depends on the Cluster only for the network (cluster VPC + TGW — it's pure IBM VPC, no k8s API), so it's a safe independent, parallelizable sibling to BNK. Sprint 27's real-terraform-state is the prerequisite that makes independent per-phase `destroy` clean._

```
           ┌──────────── 1. CLUSTER ────────────┐  (create OR reuse)
           │  ROKS cluster + cluster VPC + TGW   │  → cluster-outputs.json
           └──────────────────┬──────────────────┘
                     ┌────────┴────────┐
                     ▼                 ▼
           ┌──────────────────┐  ┌──────────────────┐
           │ 2. BNK (k8s)     │  │ 3. TESTING (VPC) │
           └──────────────────┘  └──────────────────┘
                └──── parallel; independent up/down ────┘
```

## Integrator decisions baked in (recommended where noted — confirm before dispatch)

1. **Three states**: `state-cluster/`, BNK state, `state-testing/`. The architect pins whether BNK keeps `state/` (zero migration) or moves to `state-bnk/` (cleaner; needs migration), and the pre-Sprint-28 migration path.
2. **Builds on Sprint 27** — stacks on the `sprint27-bnk-native-k8s` branch; lands after 27 merges. Real per-phase state makes independent destroy clean.
3. **Parallel BNK ∥ Testing** after the Cluster phase completes (3 phases, NOT a 4th VPC-only phase — Testing's VPC-only dependency makes its *lifecycle* independent, not its start time).
4. **Create OR reuse cluster** — skip the Cluster phase, point BNK + Testing at an existing cluster (the `create_roks_cluster=false` + cluster-outputs handoff already exists).
5. **`roksbnkctl testing up/down`** — the new phase command (recommended name; the architect resolves `testing` vs the existing `test`/`test hosts` probe group).
6. **Teardown**: `bnk down` ∥ `testing down` leave the cluster; bare `roksbnkctl down` destroys BNK ∥ Testing then Cluster (one composite confirm); `cluster down` refuses while BNK or Testing state exists.
7. **`live-verify-high-issues` applies** — the integrator runs the gated-live parallel-up + independent-down verify before closing/merging.

## Per-role scope

See `docs/PLAN.md` Sprint 28 block + `issues/issue_sprint28_<role>.md`.

| Role | Scope |
|---|---|
| **Architect** | The three-state model + the BNK-state migration decision; the testing-phase-override + the per-phase presence/shape model; the parallelism (up) + teardown-ordering + concurrent-output design; the CLI naming (`testing` vs `test`); the lifecycle/phases book chapter + migration note. No Go. |
| **Staff** | Split the trial phase into BNK + Testing states; the `testing-phase-override.tfvars` writer + the cluster/bnk override updates; expand `DetectShape` to per-phase presence; `RunTestingUp`/`Down` + the **errgroup parallel BNK∥Testing dispatch** in `RunUp`/`RunDown`; the new `roksbnkctl testing` CLI group; `bnk` → BNK-only; the `cluster down` guard (refuse while BNK/Testing exist); reuse-existing-cluster; the migration. No Sprint 27 module-body changes. |
| **Validator** | Hermetic: presence/shape model, byte-exact override generation, dispatch-decision table, parallel-dispatch ordering + errgroup error propagation, the cluster-down guard. Gated-live `scripts/e2e-three-phase.sh`: parallel up + `bnk down`-leaves-testing (and inverse) + cluster-down guard + reuse-existing-cluster + migration. |
| **Tech-writer** (light, runs after) | Drift sweep: the new `testing` command + the three-phase lifecycle chapter vs the binary; the **`testing` vs `test` disambiguation** (the likeliest operator confusion); migration note; user-facing CHANGELOG. GREEN/RED verdict. |

## Constraints (binding on every role)

- Repo root: `/mnt/d/project/roksbnkctl`. **Feature branch `sprint28-three-phase-split` (stacked on `sprint27-bnk-native-k8s`) — do NOT merge to main until the integrator's live verify is GREEN, and not before Sprint 27 merges.**
- Orchestration/state/CLI + the testing-phase split only — do NOT change Sprint 27's terraform-native BNK module bodies. The `testing` module is reused as-is (pure IBM VPC; no k8s providers in the Testing phase).
- Keep `up`/`down`/`bnk`/`cluster` backward-compatible; add `testing` alongside; don't strand pre-Sprint-28 workspaces.
- Do NOT tag a release; the integrator cuts. Do not commit to main; the integrator integrates on the feature branch. No `gh issue create`.
- Hermetic tests use fabricated state dirs / `t.TempDir()`; the gated-live driver is operator-run only.
