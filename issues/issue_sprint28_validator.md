# Sprint 28 — validator issues (three-phase: hermetic shape/override/dispatch + gated-live parallel-up / independent-down)

> **Sprint 28 frame.** Validator proves the three-phase split (Cluster / BNK /
> Testing) staff lands: the per-phase presence model, the testing-phase
> override, the parallel BNK ∥ Testing dispatch, the independent teardown
> (`bnk down` leaves the jumphosts; `testing down` leaves BNK), the
> `cluster down` guard, reuse-existing-cluster, and the pre-Sprint-28 migration.

`Status`: open

---

## Issue 1 — Hermetic tests (no live cluster)

**Severity**: high
**Status**: open

- **Presence/shape model** (`internal/config/tfstate_test.go` additive): given
  fabricated state dirs (`state-cluster/`/`state-testing/`/the BNK state) with
  or without managed resources, the presence detection reports cluster?/bnk?/
  testing? correctly; `ShapeLegacySingle` still detected; data-source-only
  refresh doesn't false-positive.
- **Override generation**: the new `testing-phase-override.tfvars` writer emits
  the exact forced block (`create_roks_cluster=false`, `use_existing_cluster_vpc=true`,
  `existing_cluster_vpc_id=<vpc>`, `deploy_bnk=false`, `deploy_cert_manager=false`,
  `testing_create_*=true`); the cluster-phase-override now includes
  `testing_create_*=false`; the bnk-phase-override keeps `testing_create_*=false`.
  Pin byte-exact (mirror the existing `second_phase_reuse_test.go` style).
- **Dispatch decisions**: a table test over the presence states → which phases
  `up`/`down`/`cluster|bnk|testing up|down` act on (e.g. cluster-present +
  testing-present + bnk-absent → `up` brings up BNK only). Assert the
  `cluster down` guard refuses when BNK or Testing state has resources.
- **Parallel dispatch**: assert `RunUp` launches BNK and Testing concurrently
  after Cluster (e.g. via injected fakes/recorded order with a barrier) and
  that a failure in one is surfaced (errgroup error propagation) without
  corrupting the other's state path. Ordering invariant: Cluster before both;
  teardown BNK∥Testing before Cluster.
- `go test ./...` PASS; `go vet` + `staticcheck` clean. Additive test files;
  the `second_phase_reuse_test.go` / `tfstate_test.go` updates are intended.

## Issue 2 — Gated-live e2e: parallel up + independent lifecycle

**Severity**: high
**Status**: open

`scripts/e2e-three-phase.sh` (new; mirror the gating + `redact()` + `DRY_RUN`
shape of `scripts/e2e-init-var-file.sh`), against a real account:

1. **Parallel up**: `roksbnkctl up` → Cluster, then BNK ∥ Testing; assert both
   land (BNK CRs ready per Sprint 27; jumphosts reachable) and that they ran
   concurrently (timing overlap, or both phases' state dirs populated from one
   `up`).
2. **Independent teardown**: `roksbnkctl bnk down` → BNK gone, **jumphosts still
   present** (`state-testing/` intact, SSH targets still work); then
   `roksbnkctl bnk up` redeploys BNK against the same cluster + reuses the same
   jumphosts. Symmetric: `testing down` leaves BNK.
3. **cluster-down guard**: `roksbnkctl cluster down` while BNK/Testing exist →
   refused with the actionable message; after `bnk down` + `testing down`,
   `cluster down` succeeds and removes `cluster-outputs.json`.
4. **Reuse-existing-cluster**: against a pre-existing cluster, skip the Cluster
   phase; BNK + Testing deploy.
5. **Migration**: a workspace created on the 2-phase code, then driven by the
   3-phase binary, still `up`/`down`s (or follows the documented migration).
6. Gated on `IBMCLOUD_API_KEY`; honors `DRY_RUN`; redacts secrets; exits
   non-zero on any miss. `bash -n` clean.

### Acceptance criteria
1. Hermetic presence/override/dispatch/parallel tests PASS; gates green.
2. Gated-live proves parallel up, `bnk down`-leaves-testing (and inverse),
   the cluster-down guard, reuse-existing-cluster, and migration.

### Files affected
- **New**: `scripts/e2e-three-phase.sh`; additive `internal/orchestration/*_test.go`
  for dispatch/parallel; updates to `internal/config/tfstate_test.go` +
  `internal/orchestration/second_phase_reuse_test.go`.

### Related
- `issues/issue_sprint28_staff.md` — the surface under test.
- `issues/issue_sprint28_architect.md` — the pinned presence model + override
  blocks + teardown ordering the tests assert.
- Integrator memory [[live-verify-high-issues]] — cluster-mutating; the live
  parallel-up + independent-down verify gates closure.
