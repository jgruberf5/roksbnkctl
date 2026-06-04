# Sprint 26 — validator issues (naming / render / interview hermetic tests + gated-live prefix e2e)

> **Sprint 26 frame.** Validator owns the test surface proving the
> prefix-driven naming refactor staff lands
> (`issues/issue_sprint26_staff.md`) behaves per spec: name derivation +
> length validation in `internal/naming`, the full prefix render (and the
> preserved legacy sparse path) in `internal/tf`, the rewritten `init`
> interview (prefix loop, create toggles, existing-resource discovery,
> non-TTY error path), backward compatibility for empty-`Prefix` configs,
> and a gated-live driver that proves two workspaces with distinct
> prefixes no longer collide.

`Status`: open

---

## Issue 1 — Hermetic coverage for `internal/naming`, the full render, and the interview

**Severity**: medium
**Status**: open

### Scope

Additive new test files only; do not edit pre-existing `_test.go` (parity
discipline carried from Sprints 18/21/22/23/24) **except** the two
deliberate updates called out below (the render tests change shape, and the
vestigial handoff test is deleted — both are intended by the staff issue).

**1. `internal/naming/naming_test.go`** (new) — table tests:
- `Derive(prefix)` produces the exact suffix scheme from the architect's
  table (`<prefix>`, `<prefix>-cluster-vpc`, `<prefix>-tgw`,
  `<prefix>-registry-cos`, `<prefix>-client-vpc`, `<prefix>-jh-tgw`,
  `<prefix>-jh`).
- `ValidatePrefix` accepts a normal prefix; rejects (a) uppercase, (b)
  leading digit/hyphen, (c) trailing hyphen, (d) illegal chars, (e) a
  prefix long enough to overflow the cluster-name limit — and the overflow
  error names the offending resource + the max prefix length.
- `ValidatePrefix` accounts for the appended `-<zone>` on the cluster
  jumphost prefix (the longest zone must still fit 63).
- `SanitizeToPrefix` lowercases, maps `_`/`.`→`-`, strips a leading
  non-letter, trims trailing `-`, caps length, and is **idempotent**
  (`Sanitize(Sanitize(x)) == Sanitize(x)`).

**2. `internal/tf/vars_test.go`** (update — intended shape change):
- New cases asserting the **full prefix render**: given a `Workspace` with
  `Prefix` + a `Resources` block, the output contains every derived name +
  `create_*` toggle, each **exactly once** (assert no duplicate variable
  lines), and no `tf-*` upstream defaults leak.
- Keep a **legacy** case (`Prefix == ""`) asserting the old sparse output
  (the create/attach/omit-empty behavior the current
  `TestRenderTFVars_*` pin) is byte-unchanged.
- Existing-resource cases: `create_roks_transit_gateway = false` +
  `roks_transit_gateway_name = "<existing>"`; same for client VPC and COS.
- Continue to assert the api key is never rendered.

**3. Delete `internal/tf/secondphase_handoff_test.go`** — it pins the
vestigial `RenderTFVarsWithClusterOutputs` staff removes. Confirm no other
test references the deleted functions.

**4. `internal/cli` init tests** (new additive file, e.g.
`init_prefix_test.go`) — drive the interview hermetically (temp
`ROKSBNKCTL_HOME`, seeded/scripted stdin, mocked IBM verify where the
existing init tests already stub it):
- Default-accept run persists `prefix` + a `resources:` block and the
  expected `ClusterCfg`.
- An over-long prefix on a TTY re-prompts; in **non-TTY** with an invalid
  default it returns a clear non-nil error (CI contract — mirror the
  existing non-TTY init test pattern).
- Declining a create toggle with a live dependent captures the
  existing-resource name into the right `ResourceToggle.Existing` /
  `Cluster.Name`.
- `--var-file` path still seeds config + copies `terraform.tfvars.user`,
  **and** now also sets a sanitized `Prefix` (update the Sprint 19
  `init_var_file*_test.go` expectations for the added field — this is an
  intended, minimal expectation update, not a behavior regression).

### Acceptance criteria (sub-case → assertion map in the closure)

1. `internal/naming` derive/validate/sanitize cases all PASS, including the
   overflow-message and zone-suffix cases.
2. Full-render case asserts presence + single-occurrence of every derived
   variable and absence of `tf-*` defaults; legacy case asserts
   byte-unchanged sparse output.
3. Init interview cases pin: prefix persistence, non-TTY invalid-prefix
   error, existing-resource capture, `--var-file` prefix-derivation.
4. `go test ./...` PASS; `go vet ./...` clean. Parity: `git diff --stat --
   '**/*_test.go'` shows only the new files + the two intended updates
   (`internal/tf/vars_test.go`, `internal/cli/init_var_file*_test.go`) + the
   deletion (`internal/tf/secondphase_handoff_test.go`).

---

## Issue 2 — Gated-live e2e: prefix-driven generation + no-collision proof

**Severity**: medium (operator-run; the rendered-tfvars change is
`up`-affecting per [[live-verify-high-issues]]).
**Status**: open

### Scope

`scripts/e2e-init-prefix.sh` (new; mirrors `scripts/e2e-init-var-file.sh`'s
gating + `redact()` + `DRY_RUN` shape):

1. `roksbnkctl init -w e2e-prefix-a --prefix e2e-prefix-a` (or scripted
   interview) then `roksbnkctl plan -w e2e-prefix-a` and assert the
   rendered `state*/terraform.tfvars` carries
   `openshift_cluster_name = "e2e-prefix-a"`,
   `roks_cluster_vpc_name = "e2e-prefix-a-cluster-vpc"`,
   `roks_transit_gateway_name = "e2e-prefix-a-tgw"`, … with **no** `tf-*`
   default names.
2. **No-collision proof**: a second workspace `e2e-prefix-b` plans against
   distinct names (`e2e-prefix-b-*`) — the two `plan`s reference disjoint
   resource names (the collision class this sprint closes). `plan`-level
   assertion is sufficient; a full dual `up` is optional/DRY_RUN-gated
   given cost.
3. **Override proof**: drop a `terraform.tfvars.user` overriding
   `openshift_cluster_name` and confirm the `→ Layering user tfvars …`
   line + that the override wins in the plan.

Exit non-zero on any assertion miss so CI / the integrator's live `!`
verify can gate on it.

### Acceptance criteria

1. The driver asserts the full generated name set in the rendered tfvars
   for two distinct prefixes and proves disjoint names.
2. Override layering verified.
3. Driver is gated (requires `IBMCLOUD_API_KEY`, honors `DRY_RUN`) and
   redacts secrets in echoed commands.

### Files affected

- **New**: `internal/naming/naming_test.go`,
  `internal/cli/init_prefix_test.go`, `scripts/e2e-init-prefix.sh`.
- **Update**: `internal/tf/vars_test.go`,
  `internal/cli/init_var_file*_test.go` (added `Prefix` expectation).
- **Delete**: `internal/tf/secondphase_handoff_test.go`.

### Related

- `issues/issue_sprint26_staff.md` — the surface under test.
- `scripts/e2e-init-var-file.sh` — the gated-live driver shape to mirror.
- Integrator memory [[live-verify-high-issues]] — integrator runs the
  live `init → plan` (and optional dual-`up`) before flipping staff Issue 1
  to `resolved`.

---

## Closure — validator, 2026-06-04

`Status`: Issue 1 **resolved** (hermetic, all gates green). Issue 2
**ready for operator/integrator run** (gated-live driver shipped; not run
here — no cloud creds, per the hermetic-only constraint).

### Gate results

- `go test ./...` — **PASS** (every package; no failures, no skipped
  assertions beyond the two documented live-creds / vacuous-table skips).
- `go vet ./...` — **clean, exit 0** (the deletion of
  `internal/tf/secondphase_handoff_test.go` cleared the one remaining
  failure staff flagged; it referenced the now-deleted
  `RenderTFVarsWithClusterOutputs`).
- `git diff --stat -- '**/*_test.go'` shows **only** the three intended
  test-file changes:
  - `internal/tf/vars_test.go` (update — full-render + byte-stable legacy cases)
  - `internal/cli/init_var_file_test.go` (update — added `Prefix` expectation)
  - `internal/tf/secondphase_handoff_test.go` (deletion)
  New additive files (`internal/naming/naming_test.go`,
  `internal/cli/init_prefix_test.go`) + the gated-live driver
  (`scripts/e2e-init-prefix.sh`) are untracked, as expected.

### Files changed (validator-owned only)

- **New** `internal/naming/naming_test.go` — Derive / ValidatePrefix /
  SanitizeToPrefix table tests.
- **New** `internal/cli/init_prefix_test.go` — interview-helper + render +
  persistence coverage for the prefix flow.
- **New** `scripts/e2e-init-prefix.sh` — gated-live prefix driver.
- **Update** `internal/tf/vars_test.go` — full prefix render +
  existing-resource + byte-unchanged legacy (create + attach) cases.
- **Update** `internal/cli/init_var_file_test.go` — added the `Prefix` +
  all-create-`Resources` expectation to the `ConfigSeeding` (AC2) case.
- **Delete** `internal/tf/secondphase_handoff_test.go` — vestigial; confirmed
  no other file references `RenderTFVarsWithClusterOutputs` /
  `WriteTFVarsWithClusterOutputs` (grep clean).

### Sub-case → assertion map

**Issue 1 — naming (`internal/naming/naming_test.go`)**

| Sub-case | Assertion |
|---|---|
| `TestDerive` | exact suffix scheme: cluster==prefix, `-cluster-vpc`, `-registry-cos`, `-tgw`, `-client-vpc`, `-jh-tgw`, `-jh` |
| `TestDerive_ClusterNameIsBarePrefix` | cluster name has NO suffix (the prefix-length invariant) |
| `TestValidatePrefix_Accept` | single-letter, normal, digits, hyphens, and a **MaxPrefixLen()-length** prefix all validate |
| `TestValidatePrefix_RejectLabel` | empty / uppercase / leading digit / leading hyphen / trailing hyphen / illegal `_`,`.`,space,`/` all rejected |
| `TestValidatePrefix_Overflow` | over-long (MaxPrefixLen()+1) rejected; message names the offending resource (cluster) + the max prefix length |
| `TestValidatePrefix_ZoneSuffixBudget` | cluster-jumphost validation budget includes `-<zone>`; **skips (documented)** because the cluster limit (35) binds before the jumphost zone budget (49) in staff's table — the zone-inclusive fit is also exercised by the max-length accept case |
| `TestSanitizeToPrefix` | lowercase, `_`/`.`→`-`, illegal-char strip, hyphen-run collapse, leading-non-letter strip, trailing-`-` trim |
| `TestSanitizeToPrefix_ProducesValidLabel` | non-empty result satisfies the label charset rule |
| `TestSanitizeToPrefix_Idempotent` | `Sanitize(Sanitize(x)) == Sanitize(x)`, incl. the length-cap re-trim edge |
| `TestMaxPrefixLen_Sane` | bound is positive, ≤ cluster limit, and the exact-max prefix validates |

**Issue 1 — full render (`internal/tf/vars_test.go`)**

| Sub-case | Assertion |
|---|---|
| `TestRenderTFVars_FullPrefixRender_AllCreate` | every derived name (from `naming.Derive`) + every `create_*` toggle present; **each variable EXACTLY ONCE** (`assertEachVarOnce`); no `tf-*` default leak; `api_key` never rendered |
| `TestRenderTFVars_FullPrefixRender_ExistingResources` | declined TGW/COS/client-VPC → `create_* = false` + `*_name = "<existing>"`; the derived names for the declined resources do NOT appear; each-var-once + no api key |
| `TestRenderTFVars_LegacySparse_ByteUnchanged` | empty-`Prefix` create-path render is **byte-for-byte** the old sparse body (literal baseline); no prefix-only variables leak |
| `TestRenderTFVars_LegacyAttach_ByteUnchanged` | empty-`Prefix` attach-path render byte-for-byte unchanged |

**Issue 1 — interview (`internal/cli/init_prefix_test.go`, `init_var_file_test.go`)**

| Sub-case | Assertion |
|---|---|
| `TestPromptPrefix_NonTTY_ValidDefault` | non-TTY returns the sanitized-workspace-name default (validated) |
| `TestPromptPrefix_NonTTY_ReInitUsesExistingPrefix` | re-init default = the existing workspace prefix |
| `TestPromptPrefix_NonTTY_InvalidDefaultErrors` | **CI contract**: non-TTY + invalid (over-long) default → clear non-nil error flagging the non-interactive path |
| `TestRunPrefixInterview_NonTTY_DefaultAccept` | default-accept builds + persists `prefix` + `resources:` block + the derived `ClusterCfg`; round-trips through real `SaveWorkspace`/`LoadWorkspace`; on-disk YAML carries `prefix:` + `resources:` + `registry_cos:` |
| `TestSeedVarFileInterview_SetsSanitizedPrefix` | `--var-file` path sets a **sanitized** `Prefix` (from the file's cluster name) + all-create `Resources` |
| `TestSeedVarFileInterview_PrefixFallsBackToWorkspaceName` | no file cluster name ⇒ prefix seeded from the workspace name |
| `TestSeedVarFileInterview_DeclinedClusterCapturesExisting` | declined cluster captures the existing name into `Cluster.Name` |
| `TestRenderFullBody_DeclinedToggleCapturesExistingName` | declined toggle's `Existing` name routes into the matching `*_name` var (the interview→render wiring) |
| `TestPrintNamePlan_ShowsDerivedNames` / `_LegacyEmptyPrefix_PrintsNothing` | name plan prints derived + annotated-existing names; legacy empty-prefix prints nothing |
| `TestInitPrefix_CobraDefaultAccept_PersistsPrefix` | end-to-end cobra `init` persists a non-empty `Prefix` + `Resources` — **skip-guarded** on live IBM creds (runInit verifies creds before the interview; no in-process stub seam — same gap as the Sprint 19 var-file positive cases; the gated-live driver covers it) |
| `init_var_file_test.go` `TestInitVarFile_ConfigSeeding` (updated) | now also asserts the persisted `Prefix` (sanitized `openshift_cluster_name`) + all-create `Resources` — skip-guarded on live creds as before |

**Issue 2 — gated-live (`scripts/e2e-init-prefix.sh`)**

| Check | Assertion |
|---|---|
| G1 | `init`→`plan` for `e2e-prefix-a` and `e2e-prefix-b`; each `state/terraform.tfvars` carries the full `<prefix>-*` generated name set (cluster, cluster-vpc, registry-cos, tgw, client-vpc, jh-tgw) with **no `tf-*` defaults** |
| G2 | no-collision proof: every `*_name` value across the two rendered files is **disjoint** (`comm -12` intersection empty) |
| G3 | a `terraform.tfvars.user` overriding `openshift_cluster_name` triggers the `→ Layering user tfvars from …` line and the override value wins in the plan output |
| G4 | planted-sentinel + API-key-head leak scan over the run log = 0 hits |
| gating | requires `IBMCLOUD_API_KEY`; honors `DRY_RUN` (walk-through, no cloud); `redact()` scrubs every echoed command; EXIT-trap tears down both throwaway workspaces; exits non-zero on the first assertion miss. `bash -n` syntax-clean; `DRY_RUN=1` walk-through verified. |

### Notes for the integrator (no production code changed by validator)

- **No production bug found.** Every staff-shipped behavior I exercised
  matched the issue + staff closure. Two test expectations I initially
  drafted were *my* wrong assumptions, not staff bugs, and were corrected
  to match the (correct) shipped behavior:
  - `seedVarFileInterview` seeds the prefix from the workspace name when the
    var-file carries **no** `openshift_cluster_name`; a *defaulted* cluster
    name does NOT seed the prefix (the seed is keyed on `seeds.HasClusterName`).
    This is sensible and is now pinned by
    `TestSeedVarFileInterview_PrefixFallsBackToWorkspaceName`.
- **Zone-suffix case is honestly a skip** under the current constraint table
  (cluster 35 binds before the IS jumphost zone budget 49), so the scenario
  is vacuous to *isolate*. The zone-inclusive fit is still positively
  exercised by the max-length accept case. If the architect's reconciled
  table ever loosens the cluster limit relative to IS, the test goes
  assertive automatically (it reads `MaxPrefixLen()`, not a literal).
- **Live verify still required** per [[live-verify-high-issues]]: run
  `IBMCLOUD_API_KEY=… scripts/e2e-init-prefix.sh` (and, if desired, the
  skip-guarded cobra cases via the same key) before flipping staff Issue 1
  to `resolved`.
