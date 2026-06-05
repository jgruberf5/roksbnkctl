# Sprint 28 — architect issues (three-phase state model, parallelism/teardown design, migration, book)

> **Sprint 28 frame.** Split roksbnkctl's two-phase model (cluster + trial=
> BNK+testing) into THREE independent phases — Cluster, BNK, Testing — that
> deploy in parallel (BNK ∥ Testing after Cluster) and tear down
> independently (`bnk down` leaves the jumphosts for reuse, and vice versa).
> Staff (`issue_sprint28_staff.md`) owns the orchestration/state/CLI Go. This
> issue owns the **design decisions staff codes against** + the operator
> prose. The decomposition: BNK depends fully on the Cluster (k8s); Testing
> depends on the Cluster only for the network (cluster VPC + TGW, no k8s).

`Status`: resolved (design + book delivered)

---

## Issue 1 — Three-state model + the BNK-state migration decision (BLOCKING)

**Severity**: high
**Status**: resolved

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
**Status**: resolved

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
**Status**: resolved

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
**Status**: resolved

Pin the command tree: a new `roksbnkctl testing up/down` (phase command,
parallel to `cluster`/`bnk`) — recommend this name and explicitly distinguish
it from the existing `roksbnkctl test` / `test hosts` group (which RUNS
connectivity/DNS/throughput probes, not provisions jumphosts). If `testing` vs
`test` is too close, propose an alternative (`roksbnkctl jumphosts up/down`?
`roksbnkctl test-infra`?) — but recommend ONE. Define what bare `up`/`down`/
`plan`/`apply` do across the three phases.

## Issue 5 — Book authoring

**Severity**: low
**Status**: resolved

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

---

## Design — three-phase model (architect, 2026-06-04)

This is the authoritative design staff codes against. It splits the current
two phases — **cluster** (`state-cluster/`) and **trial** (`state/` =
BNK + jumphosts) — into **three**: **Cluster**, **BNK**, **Testing**. BNK
depends fully on the cluster (k8s); Testing depends on the cluster only for
the network (cluster VPC id + TGW name) and is pure IBM VPC (confirmed: the
`testing` module's `providers.tf` declares only `ibm` + `ibm.vpc_region`,
no helm/kubectl/kubernetes providers). No Go or terraform-body changes are
made here — this is design + prose. Line/file references are to the tree at
the time of writing.

### Issue 1 — Three-state layout, module ownership, handoff, migration

#### 1a. State-dir decision: **BNK keeps `state/` (zero migration). RECOMMENDED.**

Two options were on the table:

| Option | Layout | Migration cost |
|---|---|---|
| **A (chosen)** | cluster→`state-cluster/`, **BNK→`state/`**, testing→`state-testing/` (new) | Only the jumphosts move out of `state/`; BNK resources stay put |
| B | cluster→`state-cluster/`, BNK→`state-bnk/` (new), testing→`state-testing/` (new) | Every existing split workspace needs `terraform state mv`/dir-copy of the BNK modules into a brand-new dir on top of the testing move |

**Rationale for A.** Today `state/` already holds the BNK modules
(`module.flo` / `module.cne_instance` / `module.license` / `module.cert_manager`)
**plus** the jumphosts (`module.testing`). The three-phase split only needs
to *evict the jumphosts*; the BNK modules are already exactly where the BNK
phase wants them. Keeping `state/` as the BNK state means:

- **Pre-Sprint-28 split workspaces** (the common case post-Sprint-27) need
  **only the jumphost eviction**, not a second whole-phase move. One move,
  not two.
- `WorkspaceStateDir` (`internal/config/paths.go:67`) — the most-referenced
  state path in the codebase (`openTF`, `DetectShape` trial leg, the
  docker-backend mounts, `tryAuto*` hooks) — keeps its meaning. Renaming it
  to `state-bnk/` would ripple through every one of those call sites for
  pure cosmetics.
- The `ShapeLegacySingle` v1.0.x path still keys off `state/` (the monolith
  lives there); keeping `state/` as BNK keeps that detection unperturbed.

The only cost of A is a slight naming asymmetry (`state/` vs the explicitly
named `state-cluster/`/`state-testing/`). That is documented (book chapter
+ a one-line comment on `WorkspaceStateDir`) and is strictly cheaper than a
migration that touches live BNK resources. **Staff: add
`WorkspaceTestingStateDir(name) → ~/.roksbnkctl/<name>/state-testing/` (new
`testingStateSubdir = "state-testing"`); leave `WorkspaceStateDir` (=BNK)
and `WorkspaceClusterStateDir` as-is.**

#### 1b. Module → phase ownership table (authoritative)

| TF module / resource | Phase | State dir | Notes |
|---|---|---|---|
| `module.roks_cluster` (cluster + **cluster VPC** + subnets + PGWs + **transit gateway** + registry COS) | **Cluster** | `state-cluster/` | Owns the cluster VPC (`ibm_is_vpc.cluster_vpc`) and the TGW. Both BNK and Testing consume these as existing/data. Unchanged from today. |
| `module.cert_manager` | **BNK** | `state/` | Moved to BNK in Sprint 27 (provider-based; needs a live cluster at plan time). `deploy_cert_manager=true` in the bnk phase. |
| `module.flo` | **BNK** | `state/` | |
| `module.cne_instance` | **BNK** | `state/` | |
| `module.license` | **BNK** | `state/` | |
| `module.testing` (TGW jumphost, per-AZ cluster jumphosts, client VPC, jumphost subnets/SG, **`tls_private_key.jumphost_shared_key`**) | **Testing** | `state-testing/` | Pure IBM VPC. **The shared SSH key now lives here** — it was the Sprint-23 leak resource; with Testing as its own phase the key is owned by exactly one phase, no cross-phase leak. |

The two cross-phase couplings, both **read-only data lookups** in the
consuming phase (never managed):

- **Cluster VPC** — owned by `module.roks_cluster`. Testing consumes it via
  `cluster_vpc_id` (a direct input, `terraform/modules/testing/variables.tf:148`)
  fed from `existing_cluster_vpc_id` + `use_existing_cluster_vpc=true`
  → `module.roks_cluster` flips `ibm_is_vpc.cluster_vpc[0]` to
  `data.ibm_is_vpc.existing_cluster_vpc[0]`. BNK consumes the cluster (not
  the VPC directly) via the existing-cluster data source.
- **Transit gateway** — owned by `module.roks_cluster`. Testing looks it up
  **by name** via `data.ibm_tg_gateway.transit_gateway` (`name =
  var.testing_transit_gateway_name`, `terraform/modules/testing/data.tf:103-107`).
  In the combined flow this name is wired from
  `module.roks_cluster.transit_gateway_name`; standalone, it must come from
  the handoff file (see 1c).

#### 1c. `cluster-outputs.json` handoff — ADD one field (`transit_gateway_name`)

`ClusterOutputs` (`internal/config/cluster_outputs.go:22`) today carries:
`ClusterName`, `ClusterID`, `Region`, `ResourceGroupID`, `VPCID`, `VPCName`,
`SubnetIDs`, `TransitGatewayID`, `RegistryCOSCRN`/`Name`, `MasterURL`,
`OpenShiftVersion`, `Source`, `RecordedAt`.

- **BNK needs**: `ClusterID`/`ClusterName` (existing-cluster data lookup) +
  `VPCID` (the bnk-phase override already reuses it). **All present.**
- **Testing needs**: `VPCID` (→ `existing_cluster_vpc_id` + `cluster_vpc_id`)
  + the cluster identity (`ClusterID`/`Name` → `roks_cluster_name_or_id`,
  which the testing module requires non-empty) + the **TGW name** (the
  module looks the gateway up by name, not id). The struct carries
  `TransitGatewayID` but **not the name** → **gap**.

  **ADD `TransitGatewayName string `json:"transit_gateway_name,omitempty"``**
  to `ClusterOutputs`. The cluster phase already emits the root output
  `roks_transit_gateway_name` (`terraform/outputs.tf:30`, value
  `module.roks_cluster.transit_gateway_name`), so `persistClusterOutputs`
  (`internal/cli/cluster_phase.go:433`) just reads that output and stamps
  it. For `cluster register` (no TGW output), the SDK lookup or a config
  fallback fills it; if empty, the Testing phase falls back to
  `config.yaml`'s `testing_transit_gateway_name`/the rendered tfvars (the
  testing-override leaves the name to the normal render — see 2a), so a
  missing-name register doesn't hard-break, it just relies on the user's
  configured name as today.

  `cluster show` (`cluster_phase.go:224`) gets one extra print line; no
  consumer breaks on the new optional field.

#### 1d. Pre-Sprint-28 migration — evict the jumphosts from `state/`

Three pre-existing shapes, and what each does on first Sprint-28 `up`/down:

1. **Empty / fresh** — no migration. `up` creates Cluster, then BNK (`state/`)
   ∥ Testing (`state-testing/`) cleanly.
2. **Legacy single-state (v1.0.x)** — **unchanged**. The monolith (cluster +
   BNK + jumphosts) stays in `state/`; `up`/`down` stay monolithic;
   `cluster`/`bnk`/`testing` phase verbs all refuse (extend the existing
   `ShapeLegacySingle` refusals to the new `testing` verb). No jumphost
   eviction — the whole point of legacy is "leave it alone."
3. **Split (Sprint-27 era: `state-cluster/` + `state/` where `state/` =
   BNK + jumphosts)** — the **only** shape needing a migration. The
   jumphosts (`module.testing.*`) must leave `state/` and land in
   `state-testing/` **without destroying the live jumphosts**.

   **Recommended path — `terraform state mv` (no cloud churn). RECOMMENDED.**
   Provide a one-shot migration (`roksbnkctl testing migrate`, or folded
   into the first `testing up`/`up` that detects "jumphosts in `state/`"):
   1. Init both `state/` (source) and `state-testing/` (dest, fresh).
   2. For each `module.testing.*` address in `state/`:
      `terraform state mv -state=state/terraform.tfstate
      -state-out=state-testing/terraform.tfstate module.testing.X module.testing.X`.
      (terraform-exec exposes `StateMv`.) The shared SSH key moves with the
      rest of `module.testing`.
   3. Re-run the BNK phase against `state/` so the now-jumphost-free state
      reconciles (the bnk-phase override already forces `testing_create_*=
      false`, so BNK plans *no* jumphost create — the post-move BNK plan is
      clean, no orphan re-create).
   4. Future `testing up` against `state-testing/` adopts the moved resources
      (plan shows in-place/no-op, not create).

   **Fallback — documented down/re-up.** If a workspace's `state/` is in an
   awkward partial-apply shape, the safe manual path is: `bnk down` (leaves
   jumphosts — but jumphosts are in `state/`, so this would also try to
   destroy them) → therefore the clean fallback is `roksbnkctl down`
   (destroy everything) then `roksbnkctl up` (re-create in the new
   three-state layout). This *does* churn the jumphosts (new IPs, known_hosts
   reset) but is bulletproof. The `state mv` path is preferred precisely
   because it preserves the live jumphosts; the down/re-up is the escape
   hatch the book documents for stuck states.

   **Detection** of "needs migration": `state/terraform.tfstate` has managed
   `module.testing.*` resources **and** `state-testing/` is empty/absent.
   This is a new presence combination (see 2b) — staff surfaces it as a
   one-line nudge ("jumphosts still live in the BNK state; run
   `roksbnkctl testing migrate` to split them") rather than auto-running a
   `state mv` silently.

### Issue 2 — Override + presence design

#### 2a. `testing-phase-override.tfvars` (new)

Written into `state-testing/` and appended LAST to the var-file chain
(wins over config.yaml-derived tfvars + `terraform.tfvars.user`), exactly
like `cluster-phase-override.tfvars` and `bnk-phase-override.tfvars`. Only
written when `cluster-outputs.json` exists (the cluster phase completed /
a cluster is registered); on a fresh/legacy workspace it is absent and the
testing phase is byte-identical to the create path. Exact content:

```hcl
# Generated by roksbnkctl. Do not edit by hand.
# Testing-phase override (Sprint 28 three-phase split). cluster-outputs.json
# exists, so the cluster phase already created the cluster VPC + subnets +
# public gateways + transit gateway + registry COS. This phase provisions
# ONLY the testing jumphosts (pure IBM VPC), consuming the cluster VPC + TGW
# from cluster-outputs.json. It must NOT manage the cluster, cert-manager,
# or any BNK module. Forced (wins over config.yaml tfvars +
# terraform.tfvars.user), symmetric with cluster-phase-override /
# bnk-phase-override.
create_roks_cluster = false
roks_cluster_id_or_name = "<cluster-outputs.json cluster_id or cluster_name>"
use_existing_cluster_vpc = true
existing_cluster_vpc_id = "<cluster-outputs.json vpc_id>"
existing_cluster_vpc_id is the cluster VPC; cluster_vpc_id below is the same value
cluster_vpc_id = "<cluster-outputs.json vpc_id>"
create_roks_transit_gateway = false
create_roks_registry_cos_instance = false
deploy_bnk = false
deploy_cert_manager = false
testing_transit_gateway_name = "<cluster-outputs.json transit_gateway_name>"
testing_create_tgw_jumphost = true
testing_create_cluster_jumphosts = true
testing_create_client_vpc = <user's configured value; default false>
```

Notes for staff:
- `roks_cluster_id_or_name` is REQUIRED non-empty by the testing module
  (`variables.tf:37`) → must be stamped from `clusterIdentity(co)` (the
  same helper `second_phase_reuse.go:179` uses).
- `cluster_vpc_id` is the direct input the module prefers
  (`variables.tf:148`); set it = `existing_cluster_vpc_id`. (The line
  above it in the block is a comment; render it as a real `#` comment, not
  a bare line — shown unprefixed here only for readability.)
- `testing_transit_gateway_name` comes from the **new** `TransitGatewayName`
  handoff field (1c). If empty (e.g. a register without a TGW), omit the
  line and let the normal render / config.yaml supply it.
- `testing_create_tgw_jumphost` / `testing_create_cluster_jumphosts` are
  forced **on** here (this is the phase that owns them), **but** respect the
  user's intent: if the workspace config sets one false, the override should
  pass that through rather than hard-force `true`. Cleanest: have the
  override force the *architectural* flags (`create_roks_cluster=false`,
  `deploy_bnk=false`, `deploy_cert_manager=false`,
  `create_roks_transit_gateway=false`, the VPC reuse pair) and let the
  `testing_create_*` toggles flow from the user's rendered tfvars (which
  already carry them). The override then only needs to *un-suppress* nothing
  — it simply doesn't set them false. **Decision: the testing override sets
  the architectural-off flags + the VPC/TGW reuse inputs, and does NOT
  pin `testing_create_*` (they come from the user's config render).** This
  keeps "I only want a TGW jumphost, no cluster jumphosts" working.

#### 2b. cluster-phase-override + bnk-phase-override updates

- **cluster-phase-override** (`internal/cli/cluster_phase.go:236`,
  `clusterPhaseOverrideContent`): today it forces `deploy_bnk=false` +
  `deploy_cert_manager=false` and the comment says "the testing jumphost
  still runs here." **Change:** add
  `testing_create_tgw_jumphost = false` + `testing_create_cluster_jumphosts
  = false` (and, defensively, `testing_create_client_vpc = false`) so the
  jumphosts **leave the cluster phase**. The cluster phase now owns only the
  cluster + cluster VPC + TGW + registry COS. Update the comment block to
  say the jumphosts moved to the Testing phase. With the testing module's
  Sprint-23 count-gating, this also drops `tls_private_key.jumphost_shared_key`
  to count=0 in the cluster state — the key now exists only in `state-testing/`.
- **bnk-phase-override** (`internal/orchestration/second_phase_reuse.go:200`,
  `writeBnkPhaseOverrideAt`): **no functional change** — it already forces
  `testing_create_cluster_jumphosts=false` / `testing_create_tgw_jumphost=false`
  / `testing_create_client_vpc=false` (lines 231-233). Keep them. Only the
  header comment is updated to note Testing is now its own phase (not "BNK's
  jumphosts"). The byte-content of the forced values is unchanged, which
  keeps the validator's byte-exact gate stable on the BNK override.

#### 2c. Per-phase presence model (replaces the 4-shape enum)

Replace the combinatorial `WorkspaceShape` enum
(`internal/config/tfstate.go:22`) with a **per-phase presence struct** —
three independent booleans plus the preserved legacy flag:

```go
type Presence struct {
    Cluster bool // state-cluster/ has managed resources
    BNK     bool // state/ has managed BNK resources (not legacy-monolith)
    Testing bool // state-testing/ has managed resources
    Legacy  bool // state/ carries cluster modules (v1.0.x single-state)
}
```

Detection (pure filesystem + JSON decode, no terraform/cloud — same
contract as today):

- **Cluster** = `state-cluster/terraform.tfstate` has ≥1 managed resource.
  Preserve the **`ibm_container_vpc_cluster` managed-resource signal** for
  the authoritative cluster-present check (Sprint 22 data-source-refresh
  fix — reuse `trialStateHasClusterModules`'s filter, retargeted at the
  cluster state).
- **Legacy** = `state/terraform.tfstate` carries a managed
  `ibm_container_vpc_cluster` under a `clusterPhaseModules` prefix (exactly
  `trialStateHasClusterModules` today). When `Legacy` is true, `BNK` and
  `Testing` are reported false (the monolith is its own world).
- **BNK** = `state/terraform.tfstate` has ≥1 managed resource **and not
  Legacy**. (Post-migration the jumphosts are gone from `state/`, so a
  non-legacy `state/` with resources = BNK.)
- **Testing** = `state-testing/terraform.tfstate` has ≥1 managed resource.
- **Migration-needed** (derived, not a field): `state/` has managed
  `module.testing.*` **and** `state-testing/` is empty (the 1d nudge).

`DetectShape` stays as a thin wrapper returning the old enum for any caller
not yet ported (legacy-single detection in `cluster up`/`bnk up` refusals)
— but the dispatchers move to `Presence`. The `String()`/book examples
keep the legacy names where they still mean something.

#### 2d. Dispatch table (presence → action)

`C`=Cluster present, `B`=BNK present, `T`=Testing present. "absent" omitted.

| Presence | `up` | `down` | `cluster up` | `cluster down` | `bnk up` | `bnk down` | `testing up` | `testing down` |
|---|---|---|---|---|---|---|---|---|
| none (Empty) | Cluster → (BNK ∥ Testing) | err "nothing to destroy" | Cluster up | err nothing | Cluster up → BNK up | err no BNK | Cluster up → Testing up | err no Testing |
| C | (BNK ∥ Testing) | Cluster down | no-op refresh | Cluster down | BNK up | err no BNK | Testing up | err no Testing |
| C+B | Testing up (+BNK refresh) | (BNK ∥ Testing) → Cluster | refresh | **refuse** (BNK exists) | BNK refresh | BNK down (leaves cluster+testing) | Testing up | err no Testing |
| C+T | BNK up (+Testing refresh) | (BNK ∥ Testing) → Cluster | refresh | **refuse** (Testing exists) | BNK up | err no BNK | Testing refresh | Testing down (leaves cluster+BNK) |
| C+B+T | refresh all (BNK ∥ Testing) | (BNK ∥ Testing) → Cluster | refresh | **refuse** (BNK+Testing) | BNK refresh | BNK down only | Testing refresh | Testing down only |
| Legacy | monolithic trial up | monolithic trial down | refuse | refuse | refuse | refuse | **refuse** | **refuse** |

Reuse-existing-cluster (cluster-outputs.json present, **no** `state-cluster/`):
treated as "C present" for BNK/Testing dispatch — the cluster phase is
skipped, BNK ∥ Testing deploy against the registered cluster. `cluster down`
is a no-op/err there (nothing roksbnkctl created), exactly as today.

### Issue 3 — Parallelism + teardown

#### 3a. Up ordering — **3 phases, Cluster-completes-first. RECOMMENDED.**

Bare `up` runs **Cluster serially first (both downstreams need it), then
BNK ∥ Testing concurrently** via `golang.org/x/sync/errgroup` (already in
`go.mod` as indirect — staff promotes it to a direct require). Only the
phases that need bringing up per the presence model are launched into the
group.

**Rejected: a 4th VPC-only phase that starts Testing once the cluster VPC
exists.** The wall-clock case is weak: the ROKS cluster create is ~30-50 min
and dominates; Testing is a handful of VSIs (minutes). Starting Testing
"early" against a half-built cluster VPC would save only minutes while
adding a real 4th phase, a VPC-only state slice, and a partial-cluster
data-source hazard (the testing module's `null_resource.roks_cluster_gate`
exists precisely to defer its cluster/TGW data reads to apply time — racing
it against a not-yet-ready cluster is exactly the fragility Sprint 23/27
fought). **Cluster-completes-first is simplest and correct.**

errgroup contract: first non-nil error cancels the group's context; the
other phase's apply gets a cancelled ctx (terraform-exec honors ctx
cancellation between resources). `up` returns the first error; the partial
other-phase state is left intact (re-run `up` reconciles). The Cluster
phase is **not** in the group — it must fully succeed (and write
cluster-outputs.json) before the group launches, because both downstreams
read that file.

#### 3b. Concurrent stderr — **per-phase line prefixes. RECOMMENDED.**

Two applies writing raw stderr interleave illegibly. Pin **per-phase
line-prefixed writers**: wrap each phase's stderr in a small `io.Writer`
that prefixes every line with `[bnk] ` / `[testing] ` and flushes on
newline, guarded by a shared `sync.Mutex` so a single `Write` (one line)
isn't split across the two goroutines. This beats a coarse
serialize-the-whole-apply mutex (which would defeat the parallelism) and is
simpler than a full multiplexed TUI. The Cluster phase (serial) keeps
unprefixed stderr — only the parallel leg is prefixed. Staff: a
`prefixWriter{w, prefix, mu}` in orchestration; the existing
`fmt.Fprintln(os.Stderr, "→ terraform plan")` lines in the leaf helpers
flow through it unchanged when the leaf is handed a prefixed writer instead
of `os.Stderr`.

#### 3c. Teardown ordering — (BNK ∥ Testing) → Cluster

- **Bare `down`**: ONE composite confirmation naming all present phases
  (extend the Sprint-22 single-confirm-then-`in.Auto=true` pattern,
  `lifecycle.go:337`), then destroy **BNK ∥ Testing in parallel** (errgroup,
  both independent of each other), then **Cluster** after both succeed
  (reverse-dependency order — both reference the cluster VPC/TGW).
- **`cluster down` guard**: extend the existing `ShapeSplit` refusal
  (`cluster_phase.go:375-382`) to refuse while **BNK OR Testing** presence
  is set. New message names whichever phase(s) are present, e.g. "BNK and
  Testing state exist; run `roksbnkctl bnk down` and `roksbnkctl testing
  down` first (or `roksbnkctl down` to tear down all phases)". `--auto` does
  not bypass it (correctness guard, not a prompt).
- **`bnk down`** → `state/` only (cluster + testing untouched). Keep the
  reassurance footer; add that the testing jumphosts are also intact.
- **`testing down`** → `state-testing/` only (cluster + BNK untouched). This
  is the inverse "leave testing for reuse" requirement's sibling: `bnk down`
  must not touch `state-testing/`, and vice versa — guaranteed by the
  separate state dirs.
- **`cluster-outputs.json`** deleted ONLY on `cluster down` (it's the
  cluster's identity) — unchanged; `bnk down`/`testing down` leave it.

### Issue 4 — CLI naming: **`roksbnkctl testing up/down`. RECOMMENDED.**

Pin **`roksbnkctl testing up` / `roksbnkctl testing down`** as the new phase
command group (mirrors `cluster`/`bnk`), wired in `root.go`, new
`internal/cli/testing_phase.go`.

The `testing` vs `test` distinction is the likeliest operator confusion, so
make it explicit everywhere:

- **`roksbnkctl testing`** = a **provisioning phase** (the jumphost
  infrastructure: TGW jumphost, per-AZ cluster jumphosts, client VPC). Verbs
  `up`/`down`. Parallel to `cluster`/`bnk`. State `state-testing/`.
- **`roksbnkctl test`** = **runs validation probes** (connectivity / dns /
  throughput) against an already-deployed environment (`internal/cli/test.go`,
  `Use: "test [suite]"`). Provisions nothing.
- **`roksbnkctl test hosts`** = manages the **list of target hosts** the
  `test` probes run against (`internal/cli/test_hosts.go`). Still under
  `test`, still no provisioning.

Naming was weighed against `jumphosts up/down` and `test-infra`. `testing`
wins because (a) it matches the terraform module name (`module.testing`,
`state-testing/`, `testing_*` tfvars) the operator already sees in plan
output, and (b) the `gerund` "testing" (provisioning the test rig) reads
distinctly from the imperative "test" (run the tests). The mitigation for
the residual `test`/`testing` closeness is documentation, not a rename: the
book chapter leads with a callout box, and both commands' `Short`/`Long`
cross-reference each other ("`testing` provisions the jumphosts; `test`
runs probes against them"). Tech-writer's drift sweep flags this as the
top disambiguation.

Bare verbs across the three phases:
- `up` → Cluster → (BNK ∥ Testing) (3a).
- `down` → (BNK ∥ Testing) → Cluster (3c).
- `plan` / `apply` → operate on the **BNK** phase (`state/`), unchanged from
  today's `RunPlan`/`RunApply` (they already target `WorkspaceStateDir`).
  Phase-specific plan/apply is via the phase groups if needed later; this
  sprint does not add `testing plan`/`cluster plan` (out of scope, and the
  staff issue scopes the phase groups to `up`/`down`).

### Issue 5 — Book

New chapter **`book/src/08a-three-phase-lifecycle.md`** (slots after the
cluster-phase chapter, before "registering an existing cluster"), added to
`SUMMARY.md` under Part III. It covers: the dependency graph, parallel `up`,
per-phase `up`/`down`, `bnk down`-leaves-testing (and the inverse), reuse-
existing-cluster, teardown ordering + the `cluster down` guard, the
`testing`-vs-`test` callout, and the pre-Sprint-28 migration note. Existing
chapters 08/10/11 get light cross-reference + "now three phases" nudges
(tech-writer re-captures the full transcripts; the new chapter marks its
sample output **illustrative**). The book builds clean via the docker
backend (`make book BOOK_BACKEND=docker` — verified GREEN at design time).

