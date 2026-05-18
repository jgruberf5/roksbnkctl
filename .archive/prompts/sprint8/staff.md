You are the staff engineer agent for Sprint 8 of the roksbnkctl project. Sprint 8 ships the cluster/trial phase split as a first-class command surface (PRD 06) and cuts `v1.1.0` at the end. Your scope is the implementation: new shape detection, new `bnk` command group, refactor of `lifecycle.go` to make `runUp`/`runDown` shape-aware composite dispatchers, and refusals on the `cluster up`/`cluster down` commands.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

A **reference spike** exists on the `spike/bnk-phase-split` branch (commit `00181d0`). The spike validates the approach end-to-end against the real `canada-roks` legacy workspace, but you should re-implement from PRD 06 — the spike is reference only, not for cherry-picking. Reading the spike is fine (it's the empirical evidence the design works); copying it wholesale is not (you bypass the PRD-driven discipline this sprint exists to exercise). Diff between your final code and the spike is fair game during your verification pass — if you diverge, document why in an issue.

## Read first

- `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` — your authoritative source for what to build. Specifically §"Design" (the shape detector + dispatch table), §"Implementation tasks" (the file-by-file work plan), and §"Acceptance criteria" (what your code must satisfy).
- `docs/PLAN.md` §"Sprint 8" — your authoritative deliverables list (the code-deliverables table).
- `internal/cli/cluster_phase.go` — current `runClusterUp`/`runClusterDown` bodies; your refusal logic gates them.
- `internal/cli/lifecycle.go` — current `runUp`/`runDown` bodies; you rename to `runTrialUp`/`runTrialDown` and add composite dispatchers.
- `internal/config/workspace.go` — currently contains a private `tfstateHasResources`; you relocate it to the new `tfstate.go` and remove from here.
- `internal/config/paths.go` — `WorkspaceStateDir` and `WorkspaceClusterStateDir` are what `DetectShape` calls; understand the naming convention before adding sibling helpers.
- `internal/cli/prompt.go` — `promptYesNo` is the helper you use for the `bnk up` bootstrap confirmation.
- `internal/cli/root.go` — `rootCmd.AddCommand` is where new top-level command groups register; mirror `cluster_phase.go`'s init() pattern.
- `prompts/sprint7/staff.md` — prior-sprint prompt structure; the verification block is worth borrowing verbatim.

## Coordinate with parallel agents

An **architect** agent is reframing chapter 8 (cluster phase is the default, not opt-in), adding a `bnk` section to chapter 10 with the dispatch matrix, adding a phase-aware decision tree to chapter 11, and writing the CHANGELOG `v1.1.0` entry under `## Unreleased (v1.x)`. **Do not touch `book/src/`, `CHANGELOG.md`, `docs/`, or `README.md`.**

A **validator** agent is running the regression sweep (`go build/test/vet/gofmt`), doing the cross-link audit on the architect's chapters, and optionally patching `scripts/e2e-test.sh` with a new phase that exercises the `cluster up` → `bnk up` → `bnk down` → `cluster down` cycle. **Do not touch `scripts/` or `.github/workflows/`.** They'll file issues against your code if examples in the chapters diverge from your actual implementation — fold those into your work.

A **tech-writer** agent does read-only review at the end of the sprint.

**Your scope** is `internal/config/tfstate.go` (new), `internal/config/workspace.go` (edit), `internal/cli/bnk_phase.go` (new), `internal/cli/lifecycle.go` (edit), `internal/cli/cluster_phase.go` (edit), and their accompanying `_test.go` files plus any `testdata/` fixtures.

## Tasks (priority order)

### 1. Shape detection (`internal/config/tfstate.go`, new)

Implement `WorkspaceShape` enum (`ShapeUnknown`, `ShapeEmpty`, `ShapeClusterOnly`, `ShapeSplit`, `ShapeLegacySingle`) with a `String()` method, `DetectShape(workspace string) (WorkspaceShape, error)`, the relocated `tfstateHasResources(path string)`, and the new `trialStateHasClusterModules(path string)`. The cluster-module prefix list is `["module.roks_cluster", "module.cert_manager", "module.testing"]` per PRD 06 §"Design". Match `r.Module == prefix || strings.HasPrefix(r.Module, prefix+".")` to cover root + nested addresses.

Missing tfstate files must surface as "no resources" (not an error) — workspaces aren't necessarily applied yet. Malformed JSON must surface as an error so dispatch doesn't silently misroute.

Remove `tfstateHasResources` from `internal/config/workspace.go` (it now lives in `tfstate.go`). The existing call site in `workspace.go` (`DeleteWorkspace` → `tfstateHasResources` at line 300-ish) continues to work because it's the same package.

### 2. `bnk` command group (`internal/cli/bnk_phase.go`, new)

Mirror the structure of `internal/cli/cluster_phase.go`. The cobra command tree:

- `bnkCmd` — group; `Use: "bnk"`, short = "BNK trial lifecycle (sits on top of a cluster)".
- `bnkUpCmd` — `Use: "up"`, RunE = `runBnkUp`.
- `bnkDownCmd` — `Use: "down"`, RunE = `runBnkDown`.

Flags match `cluster up`/`down`: `--auto`, `--var-file`, `--no-kubeconfig` on `bnk up`; `--auto`, `--var-file` on `bnk down`. Register via `rootCmd.AddCommand(bnkCmd)` in `init()`.

`runBnkUp` semantics per PRD 06 §"Dispatch table":
- `ShapeLegacySingle` → refuse with the message in §"Refusal messages".
- `ShapeEmpty` → print the bootstrap notice, `promptYesNo` (skipped on `--auto`), call `runClusterUp(cmd, nil)`, then `runTrialUp(cmd, nil)`.
- `ShapeClusterOnly` or `ShapeSplit` → `runTrialUp(cmd, nil)`.

`runBnkDown` semantics:
- `ShapeLegacySingle` → refuse.
- `ShapeEmpty` / `ShapeClusterOnly` → refuse with "no BNK trial state to destroy".
- `ShapeSplit` → `runTrialDown(cmd, nil)`.

### 3. Composite `runUp`/`runDown` (`internal/cli/lifecycle.go`, edit)

Rename the existing `func runUp(cmd, args)` body to `func runTrialUp(cmd, args)` (same signature, same body, same behavior — including the existing docker-backend dispatch at the top). Same rename for `runDown` → `runTrialDown`.

Add new `runUp` and `runDown` as composite dispatchers per PRD 06 §"Dispatch table". The composites:

- Detect shape via `config.DetectShape(cctx.WorkspaceName)`.
- Switch on shape: legacy → `runTrialUp`/`runTrialDown` (preserves v1.0.x byte-for-byte); empty/split → `runClusterUp` then `runTrialUp` (for up) or `runTrialDown` then `runClusterDown` (for down); cluster-only → `runTrialUp` (for up) or `runClusterDown` (for down).
- Return an error for `down` on `ShapeEmpty` ("nothing to destroy in this workspace").

The cobra wiring stays: `upCmd.RunE = runUp` and `downCmd.RunE = runDown` (now pointing at the composites).

### 4. Refusals on `cluster up`/`cluster down` (`internal/cli/cluster_phase.go`, edit)

`runClusterUp`: at the top, detect shape; refuse on `ShapeLegacySingle` with the exact text from PRD 06 §"Refusal messages".

`runClusterDown`: at the top, detect shape; refuse on `ShapeLegacySingle`, `ShapeSplit`, and `ShapeEmpty` with the corresponding messages from §"Refusal messages". Remove the existing "Any BNK trial state ... will be orphaned" warning text — it's replaced by the hard refusal on `ShapeSplit`. Keep the "This will destroy ..." prompt (and `promptYesNo` gate) for the `ShapeClusterOnly` happy path.

### 5. Tests

- `internal/config/tfstate_test.go` (new): table test for `DetectShape` against synthetic tfstate fixtures. Use `internal/config/testdata/tfstate_{empty,cluster_only,split,legacy_single}.json`. Cover missing-file (returns `ShapeEmpty`), malformed-json (returns error), each shape's correct classification, the `r.Module == prefix` exact-match case, and the nested-prefix case.
- `internal/cli/bnk_phase_test.go` (new): cover the refusal matrix from §"Dispatch table". Use `ROKSBNKCTL_HOME` set to a `t.TempDir()`; populate `<home>/<workspace>/state/terraform.tfstate` and `<home>/<workspace>/state-cluster/terraform.tfstate` from the same fixtures. Mock or skip the actual `runTrialUp`/`runClusterUp` calls (assertion: a refusal returns the right error; a non-refusal would dispatch to terraform-exec which the test doesn't exercise).

The cluster-phase refusal logic should be similarly tested, but if test wiring gets thorny, prioritise the bnk and shape-detection tests; file an issue for any deferred cluster-phase test coverage.

### 6. Smoke verify

- `go build ./...` clean.
- `go test ./internal/config/... ./internal/cli/...` green.
- `go vet ./...` clean.
- `gofmt -d -l .` empty.
- `go run ./cmd/roksbnkctl --help` lists `bnk` alongside `cluster`.
- `go run ./cmd/roksbnkctl bnk --help` lists `up` and `down`.
- `go run ./cmd/roksbnkctl -w canada-roks bnk down` (if the workspace still exists locally) refuses with the legacy-single-state error.

## Issue tracking

File at `issues/issue_sprint8_staff.md`. One issue per finding. Severity: `low | medium | high | blocker`. Status: `open | in-progress | resolved | wontfix`.

If you find a chapter or PRD inconsistency while implementing, file the issue against architect's surface — don't edit prose yourself.

## Verification before reporting done

- All six items in §"Smoke verify" pass.
- Test coverage is meaningful, not perfunctory: shape detection table-test has at least one case per shape plus the two error edges; bnk dispatch tests cover every cell of the refusal matrix.
- Docker-backend short-circuit in `runTrialUp`/`runTrialDown` is preserved (the new composite must not break the existing docker dispatch for legacy workspaces).
- No edit under `book/src/`, `CHANGELOG.md`, `docs/`, `README.md`, `scripts/`, or `.github/`.

## Final report

Under 200 words. Include: files created, files edited (full list), line counts (rough), test counts + pass/fail, smoke-check status, issues filed (counts by severity), deferred-to-integrator items. Do NOT commit.
