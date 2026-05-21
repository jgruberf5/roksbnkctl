# Sprint 19 — staff issues (`init --var-file` workspace-persistent tfvars)

> **Sprint 19 frame.** First regular work sprint post-`v1.6.3`.
> Staff owns the `init --var-file <path>` flag + the file-copy +
> interview-skip wiring. Lifecycle / orchestration / cos / ibm
> stay UNTOUCHED — Sprint 18 hardened them and the plumbing
> (`tfws.HasUserTFVars()` + auto-layering of
> `state/terraform.tfvars.user`) already exists in the tree. This
> sprint just puts the file at the path the existing wiring
> expects.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — feat: `roksbnkctl init --var-file <path>` — workspace-persistent tfvars at init time

**Severity**: medium
**Status**: resolved

### Motivation

Live use of `v1.6.3` (post-Sprint-18) surfaced a residual UX gap that the Sprint 16 applied-tfvars replay can't close: between `init` and the first successful `up`, there is no `terraform.applied.tfvars` snapshot yet, so bare `roksbnkctl <verb> -w <ws>` refuses with the Sprint 16 option-(b) actionable error ("workspace has no terraform.applied.tfvars snapshot for the <phase> phase yet"). The operator's actual remedy — re-supplying `--var-file ./terraform.tfvars` on every subsequent command — is the exact friction Issue 3 was supposed to solve.

The existing `tfws.HasUserTFVars()` codepath in `internal/tf/terraform.go` already auto-layers a workspace-persistent `state/terraform.tfvars.user` between the auto-rendered tfvars and the caller's `--var-file` flags. Today it's a manual-drop file; nobody does it because it's undocumented. This issue automates the drop at `init` time.

### Proposed surface

```
roksbnkctl init -w <workspace> --var-file <path>
```

- `--var-file <path>` — optional. When supplied: read the file, parse the tfvars assignments, seed `config.yaml` from the fields the interview cares about, skip those interview prompts, and copy the file verbatim to both phase state dirs. When omitted: byte-identical to today's interview.

### Behavior

- **Parse the var-file** via `internal/config/applied_tfvars.go`'s existing `readTFVarsAssignments(path)` (the snapshot writer already uses this — same format, same tolerance for unsupported HCL shapes which are skipped).
- **Map to `config.yaml` fields the interview already asks about**:
  - `ibmcloud_cluster_region` → `cluster.region`
  - `ibmcloud_resource_group` → `cluster.resource_group`
  - `openshift_cluster_name` → `cluster.name`
  - `openshift_cluster_version` → `cluster.version`
  - `roks_workers_per_zone` → `cluster.workers_per_zone`
  - `create_roks_cluster` → `cluster.create`
  - (Add any others the current interview prompts for; do NOT add new `config.yaml` fields this sprint.)
  - `kubeconfig_dir` / `scratch_dir` are **computed paths** (workspace-local), NOT taken from the var-file even if it carries them.
- **Skip the corresponding interview prompts** for any field the var-file answered. Fields the var-file doesn't carry still prompt (or default) exactly as today.
- **Copy the var-file verbatim** to BOTH phase state dirs:
  - `<config.WorkspaceStateDir(name)>/terraform.tfvars.user`
  - `<config.WorkspaceClusterStateDir(name)>/terraform.tfvars.user`
- **File mode `0600`** on both copies (matches the existing `terraform.applied.tfvars` snapshot pattern, since the file carries `ibmcloud_api_key`).
- **Print one confirmation line per copy**: `✓ Wrote <abs-path>` (style matches the existing init output).
- **Missing file**: exit non-zero with a roksbnkctl-level actionable error naming the path the operator passed.
- **Malformed file**: exit non-zero pointing the operator at `terraform.tfvars.example`.

### Acceptance criteria

1. `roksbnkctl init -w ws1 --var-file ./terraform.tfvars` creates `~/.roksbnkctl/ws1/state/terraform.tfvars.user` AND `~/.roksbnkctl/ws1/state-cluster/terraform.tfvars.user`, both mode `0600`, both byte-identical to the input file.
2. `~/.roksbnkctl/ws1/config.yaml` post-init reflects the var-file's `ibmcloud_cluster_region` / `openshift_cluster_name` / etc. values (the interview did NOT prompt for those).
3. `roksbnkctl init` without `--var-file` is byte-identical to today: interactive interview, no `terraform.tfvars.user` file created.
4. `roksbnkctl init -w ws1 --var-file /nonexistent` exits non-zero with an error that names the path.
5. `roksbnkctl init -w ws1 --var-file <a-file-that-isn't-tfvars>` exits non-zero with a message pointing the operator at `terraform.tfvars.example`.
6. After step 1, bare `roksbnkctl plan -w ws1` (NO `--var-file`) does NOT hit the Sprint 16 option-(b) "no snapshot yet" error — it picks up the `terraform.tfvars.user` via the existing `HasUserTFVars()` codepath and reaches `terraform plan`. (The live verify gate.)
7. `roksbnkctl init -w ws1 --var-file ./alt.tfvars` on an already-initialized workspace overwrites the existing `terraform.tfvars.user` copies (with a brief stderr note that an existing file was replaced).
8. New hermetic test file `internal/cli/init_var_file_test.go` covers cases (a)–(e) from the validator spec; no edits to any pre-existing `_test.go`.
9. `--var-file <path>` flag appears in the auto-generated `book/src/27-command-reference.md` after the architect's regen (separate role, but the staff impl must be cobra-correct enough that the regen produces clean output).

### Out of scope

- A `--force` flag for the re-init-with-overwrite case (Acceptance #7 just overwrites + notes it; explicit `--force` is a v1.7 polish).
- Caching the IAM token to disk (already explicit out-of-scope in Sprint 18 Issue 2; the persisted tfvars carries the key directly, which is sufficient).
- Validating the var-file's contents against the terraform variable schema (the file is passed verbatim; terraform validates on the next lifecycle op).
- Any change to `internal/orchestration/`, `internal/tf/`, `internal/cos/`, `internal/ibm/`, `internal/cli/cos.go`. Sprint 18 hardened those; this sprint is `init`-side only.

### Files likely touched

- `internal/cli/init.go` — `--var-file` flag binding + the parse/seed/copy/skip-interview logic.
- (Possibly) a small sibling helper in `internal/cli/` if the parse-and-seed logic is large enough to deserve extraction; staff's call.
- `internal/cli/init_var_file_test.go` (new, additive — covers AC #8).
- `book/src/27-command-reference.md` — regenerated by the architect (not staff).

### Related

- Sprint 16 validator Issue 3 (applied-tfvars replay) — this issue closes the residual gap between `init` and the first successful apply.
- Sprint 16 round-2 option (b) actionable-error — its hint at "workspace has no snapshot yet" remains for the genuine never-initialized case; `init --var-file` provides the explicit remedy the message currently lacks.
- The existing `HasUserTFVars()` + `UserTFVarsPath()` helpers in `internal/tf/terraform.go` — staff confirms they're already wired into the lifecycle, no edits there.

---

### Closure — staff, 2026-05-21

**Status**: resolved (pending integrator live `!` verify per `live-verify-high-issues`).

**Files changed (staff scope)**:

- `internal/cli/init.go` — added `--var-file` parse-up-front + per-prompt seed-skip + post-`SaveWorkspace` copy-to-both-phase-state-dirs.
- `internal/cli/init_var_file.go` (new sibling helper) — `flagInitVarFile` flag binding (additional `func init()` so `lifecycle.go` stays untouched), `varFileSeeds` carrier, `loadInitVarFile`, `writeUserTFVarsCopies` (atomic-rename, mode 0600), `absVarFilePath`, `unquoteTFVarString`.
- `internal/config/applied_tfvars.go` — one-line exported wrapper `ReadTFVarsAssignments(path)` over the existing private `readTFVarsAssignments` so init can reuse the snapshot writer's tolerant parser without re-implementation (per the issue spec: "parse via `internal/config/applied_tfvars.go`'s existing `readTFVarsAssignments` … don't re-implement"). No behaviour change to the snapshot writer.
- `internal/cli/init_var_file_helpers_test.go` (new additive) — hermetic positive-path coverage (load/copy/abs-path helpers tested directly so AC #1 / #7 do not rely on live IBM credentials, complementing the validator-shipped `internal/cli/init_var_file_test.go`).

**tfvars → config.yaml key mapping** (line per mapped key):

- `ibmcloud_cluster_region` → `IBMCloud.Region` (`cluster.region`).
- `ibmcloud_resource_group` → `IBMCloud.ResourceGroup` (`cluster.resource_group`).
- `openshift_cluster_name` → `Cluster.Name` (`cluster.name`).
- `openshift_cluster_version` → `Cluster.OpenShiftVersion` (`cluster.version`).
- `roks_workers_per_zone` → `Cluster.WorkersPerZone` (`cluster.workers_per_zone`).
- `create_roks_cluster` → `Cluster.Create` (`cluster.create`).
- `ibmcloud_api_key` — intentionally NOT mapped into `config.yaml`; lands verbatim on disk via the file copy, owned by the cred resolver.
- `kubeconfig_dir`, `scratch_dir` — intentionally NOT taken from the var-file (workspace-local computed paths per the spec).

**Destination paths the file lands at** (mode 0600, atomic-rename):

- `<config.WorkspaceStateDir(name)>/terraform.tfvars.user`
- `<config.WorkspaceClusterStateDir(name)>/terraform.tfvars.user`

**Gates run + results**:

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `gofmt -l internal/` — empty.
- `go test -race ./internal/cli/` — PASS. Helper-direct positive-path tests (AC #1 / #7) GREEN hermetically; cobra-driven positive-path tests (AC #1 / #2 via the validator file) SKIP without live `IBMCLOUD_API_KEY` (gated by the pre-existing `ic.Verify` step which is out of staff scope to refactor); cobra-driven negative-path tests (AC #4 / #5) GREEN hermetically.
- `go test -race ./internal/config/` — PASS (no regression in the snapshot writer despite the new exported wrapper).
- `git diff --stat -- '*_test.go'` — no edits to any pre-existing `_test.go` (parity discipline carries forward).

**Acceptance criteria pass list**:

- AC #1 — both `terraform.tfvars.user` copies land at mode 0600 byte-identical to the input → covered by `TestWriteUserTFVarsCopies_BothPhaseDirs` (hermetic) + the validator's `TestInitVarFile_HappyPath_BothCopiesLand` (live `!` per integrator).
- AC #2 — `config.yaml` seeded from var-file fields, interview skips them → covered by `TestLoadInitVarFile_FullMapping` + the per-field `runInit` seeds-Has* branches; end-to-end pinning in the validator's `TestInitVarFile_ConfigSeeding` (live `!`).
- AC #3 — bare `init` byte-identical to today → no code path entered when `flagInitVarFile == ""`; validator's `TestInitVarFile_NoFlagByteIdentical` pins no `terraform.tfvars.user` is created.
- AC #4 — missing-file actionable error names the path → `TestInitVarFile_MissingFile_Fails` + `TestLoadInitVarFile_*` (`MissingFile` case in the validator file).
- AC #5 — malformed-file actionable error points at `terraform.tfvars.example` → `TestInitVarFile_MalformedFile_Fails`.
- AC #6 — bare `plan -w ws1` picks up the file via `HasUserTFVars()` without further code change → covered by the dataflow trace below + integrator's live `!`. No staff edit to `internal/tf/terraform.go` or `internal/orchestration/`.
- AC #7 — re-init overwrites both copies, brief stderr "replacing existing" note → `TestWriteUserTFVarsCopies_OverwriteExisting` + the pre-copy `os.Stat` + `note: replacing existing …` line in `runInit`.
- AC #8 — new hermetic test file `internal/cli/init_var_file_test.go` ships (validator-authored) + companion helper-direct tests in `init_var_file_helpers_test.go` (staff-authored); no edits to any pre-existing `_test.go`.
- AC #9 — `--var-file` flag is cobra-bound on `initCmd` (single-line description matching the codebase shape); architect's `book/src/27-command-reference.md` regen consumes it.

**Dataflow trace — `init --var-file ./tfvars -w ws1` then bare `plan -w ws1`**:

1. `roksbnkctl init -w ws1 --var-file ./terraform.tfvars` →
2. `runInit` (`internal/cli/init.go`) calls `absVarFilePath` → `loadInitVarFile` → `config.ReadTFVarsAssignments` (exported wrapper over the pre-existing `readTFVarsAssignments` parser in `internal/config/applied_tfvars.go`) → returns `varFileSeeds` with the six interview-targeted fields populated.
3. `runInit` short-circuits the corresponding `promptString` / `promptYesNo` / `promptInt` calls when each `seeds.Has*` is true; remaining prompts run as today.
4. `config.SaveWorkspace` writes `~/.roksbnkctl/ws1/config.yaml` carrying the seeded fields.
5. `writeUserTFVarsCopies(ws1, <abs>./terraform.tfvars)` copies the file verbatim (atomic-rename, mode 0600) to BOTH `<WorkspaceStateDir>/terraform.tfvars.user` and `<WorkspaceClusterStateDir>/terraform.tfvars.user`. `✓ Wrote …` printed per copy.
6. Later: bare `roksbnkctl plan -w ws1` →
7. `orchestration.RunPlan` (`internal/orchestration/lifecycle.go`, **staff did not touch**) builds a `tf.Workspace` and calls the pre-existing `tfws.HasUserTFVars()` codepath in `internal/tf/terraform.go` (**staff did not touch**).
8. `HasUserTFVars` sees `state/terraform.tfvars.user` exists → `UserTFVarsPath()` is layered into the var-file chain after the auto-rendered `state/terraform.tfvars`, ahead of any caller `--var-file` flag (none here).
9. `terraform plan` runs with the operator's persisted assignments — no Sprint 16 option-(b) "no snapshot yet" actionable error fires, no re-supply of `--var-file` needed.

The whole post-init lifecycle wiring was already in place from Sprint 16; staff's contribution is exclusively the file-drop at `init` time at the path the existing `HasUserTFVars()` / `UserTFVarsPath()` helpers (both in `internal/tf/terraform.go`, untouched) already look for.

**Out-of-scope packages NOT touched**: `internal/tf/`, `internal/orchestration/`, `internal/cos/`, `internal/ibm/`, `internal/cli/cos.go`. No commits, no pushes, no `gh issue create`.

---

### Closure round 2 — integrator live-`!` corrections, 2026-05-21

**Status**: resolved.

Round-1 hermetics + helper-direct tests all passed, but the integrator's live `!` verify caught two related defects that round-1's design didn't notice — both rooted in trusting this ledger's path claim over the actual `tf.Workspace.UserTFVarsPath()` definition.

**Defect 1 — file written to the wrong location.** Round-1 wrote `terraform.tfvars.user` into `state/` AND `state-cluster/`. `tf.Workspace.UserTFVarsPath()` is defined as `filepath.Join(filepath.Dir(stateDir), "terraform.tfvars.user")` → for either phase this resolves to **`<workspace-root>/terraform.tfvars.user`** (sibling to `config.yaml`). The two in-state-dir copies were invisible to `HasUserTFVars()`. Fixed by collapsing `writeUserTFVarsCopies` to a single workspace-root copy via `config.WorkspaceDir(name)`. A1 in the e2e driver now also asserts the stale in-state-dir paths do NOT exist so a regression trips the check.

**Defect 2 — actionable-error gate didn't honor `HasUserTFVars()`.** Sprint 16 round-2 added `orchestration.RequireSnapshotOrVarFile` which rejected bare `plan/apply/down` when neither an applied-tfvars snapshot nor an explicit `--var-file` was present — but the gate predates Sprint 19's third input source. Widened the gate's signature to take a `hasUserTFVars bool` third argument; updated the four call sites (`lifecycle.go` ×3 + `cluster_phase.go` ×1) to pass `tfws.HasUserTFVars()`; rewrote the error message to surface BOTH remedies (`--var-file` or `init --var-file -w <ws>`).

**Files changed (round 2)**:

- `internal/cli/init.go` — single-copy at workspace root via `config.WorkspaceDir(name)`; pre-copy `os.Stat` of the single canonical path.
- `internal/cli/init_var_file.go` — `writeUserTFVarsCopies` now returns one path; mkdirs workspace root, not state dirs.
- `internal/orchestration/applied_replay.go` — `RequireSnapshotOrVarFile` gains `hasUserTFVars bool`; error message names the `init --var-file` remedy.
- `internal/orchestration/lifecycle.go` — three call sites pass `tfws.HasUserTFVars()`.
- `internal/cli/cluster_phase.go` — cluster-down call site passes `tfws.HasUserTFVars()`.
- `internal/orchestration/applied_replay_test.go` — sub-tests threaded through the new arg; added `workspace-persistent user-tfvars present → no-op` case.
- `internal/cli/init_var_file_test.go` + `init_var_file_helpers_test.go` — assertion shape updated to the workspace-root path; stale-path negative assertion added.
- `scripts/e2e-init-var-file.sh` — A1 paths corrected; stale-path negative assertion added; banner copy updated.

**Lesson — for future ledgers.** This ledger's round-1 dataflow trace said `HasUserTFVars` reads `state/terraform.tfvars.user`. It does not — the function is one line, in `internal/tf/terraform.go`, and reads `filepath.Dir(stateDir)`. The ledger's claim was made without checking the function definition. Carries forward the `investigate-first-on-non-obvious-bugs` discipline: when a ledger references an existing function as the "auto-magic" landing site, the author MUST quote the function's body into the ledger before claiming a destination.

**Live-`!` verify (run-id 20260521-031343)**: S1 + A1 + A2 + S2 + A3 + A4 all GREEN; clean teardown; driver RC=0.
