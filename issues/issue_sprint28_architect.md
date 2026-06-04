# Sprint 28 — architect issues (three-phase state model, parallelism/teardown design, migration, book)

> **Sprint 28 frame.** Split roksbnkctl's two-phase model (cluster + trial=
> BNK+testing) into THREE independent phases — Cluster, BNK, Testing — that
> deploy in parallel (BNK ∥ Testing after Cluster) and tear down
> independently (`bnk down` leaves the jumphosts for reuse, and vice versa).
> Staff (`issue_sprint28_staff.md`) owns the orchestration/state/CLI Go. This
> issue owns the **design decisions staff codes against** + the operator
> prose. The decomposition: BNK depends fully on the Cluster (k8s); Testing
> depends on the Cluster only for the network (cluster VPC + TGW, no k8s).

`Status`: open

---

## Issue 1 — Three-state model + the BNK-state migration decision (BLOCKING)

**Severity**: high
**Status**: open

Today: `state-cluster/` (cluster) + `state/` (trial = BNK + testing). Design
the three-state layout and pin the migration:

1. **State dirs**: `state-cluster/` (cluster), `state-testing/` (jumphosts),
   and BNK — **decide**: keep BNK in `state/` (no rename, zero migration for
   existing workspaces) vs introduce `state-bnk/` (cleaner naming, needs a
   `terraform state mv`/dir-copy migration). Recommend one. Whichever, the
   testing jumphosts that currently live in the cluster phase's view (and the
   trial state) must end up in `state-testing/` — define how a pre-Sprint-28
   workspace (combined `state/` with jumphosts) migrates without orphaning the
   live jumphosts (e.g. `terraform state mv module.testing... ` into
   `state-testing/`, or a documented "down then re-up" for the testing phase).
2. **Which module runs in which phase** (the authoritative table): roks_cluster
   → cluster; cert_manager/flo/cne_instance/license → BNK; testing → Testing.
   Confirm the exact resource ownership — esp. who owns the **cluster VPC**
   (`roks_cluster`, per the cross-module wiring) and the **shared jumphost SSH
   key** (`module.testing.tls_private_key.jumphost_shared_key` per the Sprint 23
   leak finding). The shared SSH key must live with the Testing phase now.
3. **The handoff**: `cluster-outputs.json` already carries VPCID, ClusterID/Name,
   TransitGatewayID, SubnetIDs. Confirm it carries everything **Testing** needs
   (cluster VPC id + TGW name/id) and **BNK** needs (cluster id/name); add any
   missing field (e.g. a TGW *name* if Testing looks the gateway up by name).

## Issue 2 — Phase-override + shape-presence design (BLOCKING)

**Severity**: high
**Status**: open

- **testing-phase-override.tfvars** (new): specify the exact forced tfvars
  (`create_roks_cluster=false`, `use_existing_cluster_vpc=true`,
  `existing_cluster_vpc_id`, `create_roks_transit_gateway=false`,
  `deploy_bnk=false`, `deploy_cert_manager=false`, `testing_create_*=true`).
  And the updated **cluster-phase-override** (add `testing_create_*=false` so
  jumphosts leave the cluster phase) and **bnk-phase-override** (keeps
  `testing_create_*=false`).
- **Shape/presence model**: replace the 4-shape enum with a per-phase presence
  signal (cluster?/bnk?/testing? — a struct or 3-bit set). Define how each is
  detected from its state dir (managed-resource scan, like today's
  `ibm_container_vpc_cluster` check for cluster-present). Map every `up`/`down`/
  `cluster|bnk|testing up|down` action to the presence states it acts on.

## Issue 3 — Parallelism + teardown ordering design

**Severity**: high (the speed + lifecycle goals)
**Status**: open

- **Up ordering**: Cluster (serial, first — both depend on it) → BNK ∥ Testing
  (`errgroup`). Recommend: does the bare `up` block on Cluster fully completing
  before launching BNK+Testing (simplest, correct), or is there value in
  starting Testing once the cluster VPC exists (a 4th network phase)? Recommend
  the 3-phase / Cluster-completes-first model unless the wall-clock case for the
  VPC-early-start is compelling (it usually isn't — Testing is minutes, Cluster
  is ~30 min, and a 4th phase adds real complexity).
- **Concurrent output**: how to render two parallel applies' stderr readably
  (per-phase line prefixes, or a serialize-on-write mutex). Pin the approach.
- **Teardown ordering**: BNK ∥ Testing (parallel, independent) → Cluster
  (after both). `cluster down` guard: refuse while BNK or Testing state has
  resources. Bare `down`: one composite confirmation naming the present phases,
  then the parallel-then-cluster destroy. `cluster-outputs.json` deleted only
  on cluster down.

## Issue 4 — CLI surface + naming

**Severity**: medium
**Status**: open

Pin the command tree: a new `roksbnkctl testing up/down` (phase command,
parallel to `cluster`/`bnk`) — recommend this name and explicitly distinguish
it from the existing `roksbnkctl test` / `test hosts` group (which RUNS
connectivity/DNS/throughput probes, not provisions jumphosts). If `testing` vs
`test` is too close, propose an alternative (`roksbnkctl jumphosts up/down`?
`roksbnkctl test-infra`?) — but recommend ONE. Define what bare `up`/`down`/
`plan`/`apply` do across the three phases.

## Issue 5 — Book authoring

**Severity**: low
**Status**: open

- Rewrite the lifecycle/phases chapter for three phases: the dependency graph,
  parallel `up`, per-phase `up`/`down`, the `bnk down`-leaves-testing capability,
  reuse-existing-cluster, the teardown ordering + the `cluster down` guard.
- Document the new `testing` command + how it differs from `test`/`test hosts`.
- Note the migration for pre-Sprint-28 workspaces.
- Mark transcripts illustrative (tech-writer re-captures).

### Scope guards
- **No Go, no terraform-body changes** — design + prose. (The `testing` module
  is reused as-is; staff wires it standalone.) Don't relitigate Sprint 27's BNK
  internals.
- mdbook builds (docker image) clean.

### Acceptance criteria
1. Three-state model + BNK-state migration decision pinned (state dirs, module
   ownership table, handoff fields).
2. testing-phase-override + the override updates + the per-phase presence model
   specified.
3. Parallelism (up) + teardown ordering + concurrent-output approach designed.
4. CLI naming pinned (`testing` vs `test` resolved).
5. Lifecycle chapter + migration note authored.

### Files affected
- This ledger / a `resolved_sprint28_architect.md` (the design).
- `book/src/**` (lifecycle/phases chapter, the `testing` command), SUMMARY only
  if a new chapter is added.

### Related
- `issues/issue_sprint28_staff.md` — consumes this design.
- `internal/config/{paths,tfstate,cluster_outputs}.go`,
  `internal/orchestration/{lifecycle,second_phase_reuse}.go`,
  `internal/cli/{cluster_phase,bnk_phase}.go` — the current 2-phase mechanics.
- Sprint 23 leak finding — the cluster-shared resources
  (`module.testing.tls_private_key.jumphost_shared_key`,
  `module.roks_cluster...cos_instance`) that pinned the current phase boundary.
