# Sprint 22 — staff issues (DetectShape false-positive)

> **Surfaced 2026-05-27** during live verify of the down-prompt fix
> (commit `18415eb` — single combined confirm on Split-shape composite
> teardown). The verify path against canada-roks exposed a second,
> independent bug in `internal/config/tfstate.go` that recreates the
> exact same operator symptom the prompt fix was meant to close:
> `roksbnkctl down` exits 0 after destroying the trial phase only,
> leaving the cluster phase stranded. The prompt fix is correct — but
> the dispatcher never reaches the Split branch because shape detection
> misclassifies the workspace as `ShapeLegacySingle`.

`Status: resolved` — fix landed during the same conversation that
filed this issue (no separate sprint dispatch). The proposed
"mode == managed" filter described below was, on closer inspection of
the canada-roks repro trial state, too lax — that state also held
two managed cluster-phase resources (`module.testing.tls_private_key.jumphost_shared_key`
and `module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`),
so a mode-only filter would not have rescued the canada-roks case.
The shipped fix is stricter: legacy classification requires a managed
`ibm_container_vpc_cluster` resource (the ROKS cluster itself, the
unambiguous v1.0.x marker) under a cluster-phase module prefix. Data
sources, stray `tls_private_key`s, and stray COS instances under
those prefixes no longer trip the classifier. See **§"Fix as
shipped"** below the original proposal for the actual code and tests.

---

## Issue 1 — `trialStateHasClusterModules` false-positives on refreshed data sources

**Severity**: high (silent misclassification routes the composite
`down` to the LegacySingle branch which does not chain cluster destroy;
operator sees exit 0 and assumes both phases gone — same class as
[[argv-strictness-prevents-resource-damage]], silently driving a
resource-heavy command against the wrong dispatch path)
**Status**: resolved (shipped in `cbb9c1b`; companion down-prompt fix in `18415eb`)

### Motivation

`internal/config/tfstate.go:80-84` defines the legacy-single-state
signature as any resource under one of these TF module addresses
appearing in the **trial** state file:

```go
var clusterPhaseModules = []string{
    "module.roks_cluster",
    "module.cert_manager",
    "module.testing",
}
```

`trialStateHasClusterModules` matches each `state.resources[].module`
against this list with `r.Module == prefix || HasPrefix(r.Module,
prefix+".")` — and **does not filter on resource mode**.

In normal Split-state operation, the BNK trial phase legitimately
holds data sources under `module.cert_manager` (the trial's flo /
cne_instance / license modules read `ibm_container_cluster_config`,
`ibm_container_vpc_cluster`, resource group lookups, etc. via the
cert_manager module path for cert/secret provisioning context). After
any `terraform refresh` — triggered by `up`, `apply`, `plan`, or
`destroy`'s plan phase — the trial state file is rewritten with those
data sources populated.

A refreshed trial state therefore looks like a legacy single-state to
the heuristic, even though no managed cluster-phase resources live in
it. `DetectShape` returns `ShapeLegacySingle`. The Sprint 22
composite-down dispatcher takes the LegacySingle branch
(`return RunTrialDown(ctx, in)`) — the cluster phase is never touched.

### Live reproduction (2026-05-27, canada-roks)

1. `roksbnkctl up --auto` against canada-roks failed mid-flight on
   IBM Cloud name collisions (orphan VPCs `canada-roks-vpc` /
   `canada-j-vpc` + the `canada-roks-tgw` from a prior incomplete
   destroy — itself a downstream symptom of the same prompt-bug
   commit `18415eb` closed).
2. Post-failure state:
   ```
   state/terraform.tfstate          serial=44   42 resources (40 data + 2 managed)
   state-cluster/terraform.tfstate  serial=129  88 resources (39 data + 49 managed)
   ```
   The trial state's 40 data sources include refreshed reads under
   `module.cert_manager.data.ibm_container_cluster_config`,
   `module.cert_manager.data.ibm_container_vpc_cluster`,
   `module.cert_manager.data.ibm_resource_group`, etc.
3. `roksbnkctl -w canada-roks down --auto` (built from commit
   `18415eb` — the post-prompt-fix binary):
   - `DetectShape` saw `module.cert_manager` data sources in trial
     state → `trialStateHasClusterModules` returned true →
     `ShapeLegacySingle`.
   - `RunDown` dispatched to `RunTrialDown` (LegacySingle branch).
   - Terraform destroy plan: `Plan: 0 to add, 0 to change, 2 to destroy`
     (the two trial-phase managed resources — `tls_private_key.jumphost_shared_key`
     and the license module's COS instance — destroyed cleanly).
   - Exit 0. No chain to cluster down. 49 cluster-phase managed
     resources stranded in `state-cluster/terraform.tfstate`.
4. Re-running `down --auto` with the trial state now empty made
   `DetectShape` return `ShapeClusterOnly` (the trialHas check failed
   the legacy gate at line 119 of tfstate.go), the dispatcher took
   the `ShapeClusterOnly` branch, and the cluster destroy proceeded
   normally — confirming the heuristic, not the dispatch, is the
   defect.

### Proposed fix

Tighten `trialStateHasClusterModules` to match on **managed**
resources only. Data sources are external lookups that legitimately
appear under any module address — they're refreshed reads, not
provisioning ownership. Only a `mode == "managed"` resource under one
of the legacy-signature module prefixes is diagnostic of pre-split
single-state ownership.

Concretely, change the JSON shape and the match loop in
`trialStateHasClusterModules`:

```go
var s struct {
    Resources []struct {
        Mode   string `json:"mode"`
        Module string `json:"module"`
    } `json:"resources"`
}
...
for _, r := range s.Resources {
    if r.Mode != "managed" {
        continue
    }
    for _, prefix := range clusterPhaseModules {
        if r.Module == prefix || strings.HasPrefix(r.Module, prefix+".") {
            return true, nil
        }
    }
}
```

The `mode` filter is the entire fix — the prefix-match logic stays.

### Acceptance criteria

1. **Fixture**: add `internal/config/testdata/tfstate_split_data_in_trial.json`
   mirroring the canada-roks 2026-05-27 case — a Split-shape trial
   state where the only resources under `module.cert_manager` /
   `module.testing` / `module.roks_cluster` are data sources
   (`"mode": "data"`), no managed entries. Pair it with a cluster
   state fixture that has cluster-phase managed resources (existing
   `tfstate_cluster_only.json` suffices).
2. **Test**: extend `internal/config/tfstate_test.go` with a case
   that loads the new trial fixture + the existing cluster-only
   fixture and asserts `DetectShape == ShapeSplit`. Pre-fix this
   test fails with `ShapeLegacySingle`; post-fix it passes.
3. **Heuristic change**: `trialStateHasClusterModules` skips
   resources with `r.Mode != "managed"` before the prefix match.
   The existing `tfstate_legacy_single.json` fixture's managed
   cluster-phase resources still trigger legacy detection (no
   regression — verified by the existing
   `TestDetectShape_LegacySingle*` cases passing unchanged).
4. **Live verify** (required per [[live-verify-high-issues]]): on a
   fresh canada-roks workspace, run `up` → partial-fail (or run a
   clean `up` then `down`) → confirm `down --auto` destroys BOTH
   phases in one invocation and the cluster state file ends with
   0 resources. The new combined prompt copy (commit `18415eb`) must
   also visibly fire when run without `--auto` — that's the
   end-to-end proof that DetectShape now reaches the Split branch.

### Files affected

- `internal/config/tfstate.go` — the heuristic (one-loop change in
  `trialStateHasClusterModules`)
- `internal/config/tfstate_test.go` — new case + new fixture wiring
- `internal/config/testdata/tfstate_split_data_in_trial.json` — new
  fixture

No changes to `internal/orchestration/lifecycle.go` or the cli
adapters — the dispatcher logic is already correct; only the shape
classifier feeding it is wrong.

### Fix as shipped

The mode-only filter described above was insufficient — the canada-roks
trial state held two managed cluster-phase resources alongside the
data sources (`tls_private_key.jumphost_shared_key` under
`module.testing`, `ibm_resource_instance.cos_instance` under
`module.roks_cluster.module.cluster`), both observed in the
2026-05-27 destroy plan. Filtering only on `mode == "managed"` would
have classified that state as LegacySingle and stranded the cluster
phase identically.

The shipped heuristic adds a TYPE filter: legacy classification
requires a managed `ibm_container_vpc_cluster` resource (the ROKS
cluster itself) under a `clusterPhaseModules` prefix. That type is
unique to cluster-phase ownership — no other terraform path
provisions the ROKS cluster — so it's the unambiguous v1.0.x marker.
Stray managed resources of other types under cluster-phase prefixes
are treated as benign (the dispatcher takes the Split branch and
destroys whatever's actually in each state).

```go
for _, r := range s.Resources {
    if r.Mode != "managed" || r.Type != "ibm_container_vpc_cluster" {
        continue
    }
    for _, prefix := range clusterPhaseModules {
        if r.Module == prefix || strings.HasPrefix(r.Module, prefix+".") {
            return true, nil
        }
    }
}
```

Test coverage added:

- `tfstate_split_data_in_trial.json` — realistic post-up trial state
  fixture: managed BNK resources under `module.flo` /
  `module.cne_instance` / `module.license` + data-source refreshes
  under `module.cert_manager`, `module.cne_instance`, `module.flo`.
  Paired with `tfstate_cluster_only.json` in a new
  `TestDetectShape_Table` case that asserts `ShapeSplit`.
- `TestTrialStateHasClusterModules_DataSourceUnderClusterPrefix` —
  pins the mode filter (data sources under cluster prefix → not
  legacy).
- `TestTrialStateHasClusterModules_StrayManagedNonClusterType` —
  pins the type filter (managed `tls_private_key` /
  `ibm_resource_instance` under cluster prefix → not legacy). Mirrors
  the canada-roks 2026-05-27 trial-state shape.
- The three existing helper tests (`_ExactMatch`, `_NestedPrefix`,
  `_DotGuard`) had their inline JSON fixtures updated to include the
  required `mode: "managed"` + `type: "ibm_container_vpc_cluster"`
  fields so the positive cases still trip the classifier.

Files modified:

- `internal/config/tfstate.go` (heuristic + comment)
- `internal/config/tfstate_test.go` (3 helper tests updated, 2 new,
  1 new dispatcher case)
- `internal/config/testdata/tfstate_split_data_in_trial.json` (new)

### Related

- Commit `18415eb` — Sprint 22 down-prompt fix. Correct in isolation;
  surfaced this defect during live verify because the operator could
  not reach the Split branch of `RunDown` to exercise the new prompt
  copy. The prompt fix and this fix are complementary — neither
  obsoletes the other.
- PRD 06 §"Design" — authoritative classification logic. The legacy
  signature there is described as "trial state contains cluster-phase
  *modules*" — the implementation took that as "any resource address
  under the module" rather than "any managed resource provisioned by
  the module." Worth updating the PRD wording too if the heuristic
  changes (tech-writer scope, follow-on).
- Integrator memory [[live-verify-high-issues]] — this bug class is
  exactly why unit tests alone aren't sufficient closure for
  high-sev defects; the test fixtures shipped with the original
  Sprint 8 split work didn't include the "refreshed trial state with
  cert_manager data sources" case.
- Integrator memory [[argv-strictness-prevents-resource-damage]] —
  same class of "silent misclassification routes a resource-heavy
  command down the wrong path." Different mechanism (DetectShape
  heuristic vs. cobra argv parser), same operator-visible failure
  mode (exit 0, stranded resources, no error to read).
