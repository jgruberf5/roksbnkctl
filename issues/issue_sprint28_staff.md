# Sprint 28 — staff issues (three-phase split: Cluster / BNK / Testing — parallel + independent lifecycle)

> **Surfaced 2026-06-04** as an integrator architecture request during the
> Sprint 27 live verify. roksbnkctl is two phases today — **cluster**
> (`state-cluster/`) and **trial** (`state/`), where "trial" lumps BNK
> (cert-manager + FLO + CNEInstance + License) AND the testing jumphosts into
> one state. That coupling means you can't tear down BNK while keeping the
> jumphosts, and the two unrelated workloads can't deploy in parallel. The
> live verify made the seams obvious (cert-manager phase placement, the
> jumphost capacity blip sinking the whole apply).
>
> **Split into THREE independent phases:**
> ```
>            ┌──────────── 1. CLUSTER ────────────┐  (create OR reuse existing)
>            │  ROKS cluster + cluster VPC + TGW   │  → cluster-outputs.json
>            └──────────────────┬──────────────────┘
>                      ┌────────┴────────┐
>                      ▼                 ▼
>            ┌──────────────────┐  ┌──────────────────┐
>            │ 2. BNK           │  │ 3. TESTING       │
>            │ cert-manager,    │  │ jumphosts (pure  │
>            │ FLO, CNE, License│  │ IBM VPC, no k8s) │
>            │ needs: full      │  │ needs: cluster   │
>            │ cluster (k8s)    │  │ VPC + TGW only   │
>            └──────────────────┘  └──────────────────┘
>                 └──── 2 ⊥ 3: parallel; independent up/down ────┘
> ```
> BNK depends fully on Cluster; Testing depends on Cluster only for the
> network (cluster VPC + TGW). BNK and Testing are independent → run in
> **parallel** after Cluster, and **tear down independently** (`bnk down`
> leaves the jumphosts for reuse, and vice versa).

`Status: open` (not yet dispatched).

### Locked decisions (integrator; recommended where noted — confirm before dispatch)
- **Three states**: `state-cluster/`, BNK state, `state-testing/`. (Architect
  pins whether BNK keeps `state/` or moves to `state-bnk/` + the migration
  path for pre-Sprint-28 workspaces — `issue_sprint28_architect.md`.)
- **Builds on Sprint 27**: real terraform state per phase makes independent
  `destroy` clean. Sprint 28 must land AFTER Sprint 27 merges (it stacks on
  the `sprint27-bnk-native-k8s` branch).
- **Parallel BNK ∥ Testing** after the Cluster phase completes (recommended:
  3 phases, NOT a 4th VPC-only phase — Testing starts after Cluster finishes;
  the VPC-only dependency is what makes its *lifecycle* independent, not its
  start time).
- **Create OR reuse cluster**: skip the Cluster phase and point BNK + Testing
  at an existing cluster (the `create_roks_cluster=false` + cluster-outputs
  handoff already exists).
- **`roksbnkctl testing up/down`** as the new phase command (recommended name;
  distinct from the existing `test` / `test hosts` group, which RUNS probes).
- **Teardown**: `bnk down` ∥ `testing down` leave the cluster; bare
  `roksbnkctl down` destroys BNK ∥ Testing then Cluster (one composite
  confirm); `cluster down` refuses while BNK or Testing state exists.

---

## Issue 1 — Split the trial phase into BNK + Testing states

**Severity**: high
**Status**: open

Today `state/` = trial = BNK + testing (the `testing` module's jumphosts run
in the cluster phase via config; the trial phase forces `testing_create_*=false`
— see `second_phase_reuse.go`). Re-allocate so each runs in its own state:

- **Cluster phase** (`state-cluster/`): `roks_cluster` only (cluster + cluster
  VPC + TGW + registry COS). Drop the testing jumphosts from here. Continues
  writing `cluster-outputs.json` (already carries VPCID, ClusterID/Name, TGW,
  subnets — everything Testing needs).
- **BNK phase**: cert-manager + FLO + CNEInstance + License (Sprint 27's
  provider path). `create_roks_cluster=false`, `deploy_bnk=true`,
  `deploy_cert_manager=true`, `testing_create_*=false`.
- **Testing phase** (`state-testing/`): the `testing` module (jumphosts).
  `create_roks_cluster=false`, `deploy_bnk=false`, `deploy_cert_manager=false`,
  `testing_create_tgw_jumphost`/`cluster_jumphosts`/`client_vpc` per the user's
  config. Consumes the cluster VPC + TGW from `cluster-outputs.json`.

Phase overrides (mirror `cluster-phase-override.tfvars` / `bnk-phase-override.tfvars`):
- a **`testing-phase-override.tfvars`** writer (new, in `second_phase_reuse.go`
  or a sibling): `create_roks_cluster=false`, `use_existing_cluster_vpc=true`,
  `existing_cluster_vpc_id=<from cluster-outputs>`, `create_roks_transit_gateway=false`,
  `deploy_bnk=false`, `deploy_cert_manager=false`, `testing_create_*=true`.
- the **bnk-phase-override** keeps forcing `testing_create_*=false` (Testing is
  now its own phase, not BNK's).
- the **cluster-phase-override** keeps `deploy_bnk=false`/`deploy_cert_manager=false`
  and now also `testing_create_*=false` (jumphosts move out of the cluster phase).

**Migration**: pre-Sprint-28 workspaces have a combined `state/`. Per the
architect's decision, either keep `state/` as the BNK state (jumphosts already
there get adopted/migrated) or introduce `state-bnk/`. Whichever — don't strand
existing workspaces; document the path.

## Issue 2 — Expand shape detection + phase model for three phases

**Severity**: high
**Status**: open

`config.DetectShape` (`internal/config/tfstate.go`) returns 4 shapes today
(Empty/ClusterOnly/Split/LegacySingle) by inspecting `state-cluster/` and
`state/`. Generalize to **per-phase presence** (cluster?, bnk?, testing?) — a
small struct/bitmask is cleaner than a combinatorial enum. `RunUp`/`RunDown`
and the `cluster`/`bnk`/`testing` CLI dispatch branch on per-phase presence
(e.g. "cluster present + bnk absent + testing present" → bring up BNK only).
Keep `ShapeLegacySingle` handling (v1.0.x). Preserve the
managed-`ibm_container_vpc_cluster` signal for the cluster-present check.

## Issue 3 — Parallel up + the `testing` phase, in orchestration + CLI

**Severity**: high
**Status**: open

- **`RunTestingUp` / `RunTestingDown`** (`internal/orchestration/`): mirror
  `RunTrialUp`/`RunTrialDown` but for `state-testing/` with the
  testing-phase-override + the cluster-outputs handoff. Post-apply: seed SSH
  targets from the jumphost outputs (the existing `tryAutoJumphost` /
  `tryAutoClusterJumphosts` hooks — they currently run after the trial apply;
  move them to the Testing phase).
- **Parallel dispatch** in `RunUp`: after `RunClusterUp` completes, run BNK and
  Testing concurrently via `golang.org/x/sync/errgroup` (only the phases that
  need bringing up, per the presence model). Interleave their stderr cleanly
  (prefix lines per phase, or serialize the prints) so concurrent output is
  readable. The cluster phase stays serial-first (both depend on it).
- **CLI**: a new `roksbnkctl testing` command group (`up`/`down`, mirroring
  `cluster`/`bnk`), wired in `root.go`. `roksbnkctl bnk up/down` now targets the
  BNK state only (no jumphosts). Update `bnk up`'s dispatch (it currently calls
  cluster-up then trial-up) to cluster-up then BNK-up (no testing).

## Issue 4 — Independent teardown + guards

**Severity**: high
**Status**: open

- `roksbnkctl bnk down` → BNK state only (cluster + testing untouched).
- `roksbnkctl testing down` → testing state only (cluster + BNK untouched) —
  the "leave testing hosts for reuse" requirement is the inverse: `bnk down`
  must NOT touch testing.
- `roksbnkctl cluster down` → refuse while BNK OR Testing state has resources
  (they reference the cluster VPC/TGW); extend the existing trial-exists guard
  (`cluster_phase.go` ~375-382) to check both.
- bare `roksbnkctl down` → composite confirm naming all present phases, then
  destroy **BNK ∥ Testing** (parallel), then **Cluster** (reverse-dependency
  order). Reuse the existing single-confirm-then-`in.Auto=true` pattern so the
  user isn't re-prompted per phase.
- `cluster-outputs.json` is deleted only on `cluster down` (not bnk/testing
  down) — it's the cluster's identity.

### Scope guards
- Don't change the Sprint 27 terraform-native BNK internals (helm_release /
  kubectl_manifest) — this sprint is orchestration/state/CLI + the
  testing-phase split, not the module bodies.
- The `testing` module already takes existing-VPC/TGW inputs (it's pure IBM
  VPC) — wire it to run standalone in `state-testing/`; no k8s providers in
  the testing phase.
- Keep `roksbnkctl up`/`down`/`bnk`/`cluster` backward-compatible in spirit;
  add `testing` alongside.
- Tests are validator's surface.

### Acceptance criteria
1. A fresh `roksbnkctl up` does Cluster → (BNK ∥ Testing) with the two
   running concurrently; the jumphosts land in `state-testing/`, BNK in its
   own state.
2. `bnk down` removes BNK and leaves the jumphosts; `testing down` removes the
   jumphosts and leaves BNK; `cluster down` refuses while either exists.
3. `roksbnkctl testing up/down` works standalone against an existing cluster.
4. Reuse-existing-cluster: skip the Cluster phase, BNK + Testing deploy against
   a provided cluster.
5. Pre-Sprint-28 workspaces still `up`/`down` (migration path works).
6. `go build`/`go vet`/`staticcheck` clean.

### Files affected
- `internal/config/paths.go` (state-testing dir), `internal/config/tfstate.go`
  (per-phase presence), `internal/config/cluster_outputs.go` (ensure TGW
  name/id + VPC id present for Testing).
- `internal/orchestration/lifecycle.go` (parallel RunUp/RunDown,
  RunTestingUp/Down), `internal/orchestration/second_phase_reuse.go`
  (testing-phase-override + the cluster-phase-override jumphost gate),
  `internal/cli/cluster_phase.go` (down guard), `internal/cli/bnk_phase.go`
  (BNK-only), new `internal/cli/testing_phase.go`, `internal/cli/root.go`.
- `go.mod` — `golang.org/x/sync/errgroup` (confirm present).
- terraform: no module-body changes expected; verify the `testing` module
  runs standalone with existing-VPC/TGW inputs.

### Related
- `issues/issue_sprint28_architect.md` — the state model, override design,
  shape model, parallelism/teardown ordering, migration, CLI naming, book.
- `issues/issue_sprint28_validator.md` — hermetic shape/override/dispatch tests
  + gated-live parallel-up / independent-down e2e.
- Sprint 27 (`sprint27-bnk-native-k8s`) — the real-state prerequisite; this
  branch stacks on it.
- Integrator memory [[live-verify-high-issues]] — cluster-mutating; the live
  parallel-up + independent-down verify gates closure.
