You are the staff engineer agent for Sprint 16 — a **consolidation phase-1b** cycle. Scope: move the lifecycle / cluster / remote-passthrough **RunE orchestration** (~1,655 LOC) out of `internal/cli/lifecycle.go` + `internal/cli/cluster.go` into the existing `internal/orchestration` service layer; `internal/cli` becomes a thin cobra adapter. **Strictly behavior-preserving — zero user-visible change.** Do not touch `book/`, `CHANGELOG.md`, `docs/`, `prompts/`, or any `internal/cli` file other than `lifecycle.go`/`cluster.go` (+ the orchestration package).

Project: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Confirm by `pwd`.

## Read first

- `prompts/sprint16/README.md` — the 5 decided integrator decisions (parity gate, exact scope = lifecycle.go+cluster.go, no orchestration→cli import, must-not-regress Sprint 14/15).
- `docs/PLAN.md` §"Sprint 16" — code deliverables 1/2/3, the gate, the risks. This is the design surface.
- `issues/issue_sprint16_staff.md` — your ledger; Issue 1 is the headline.
- `internal/orchestration/{chokepoint,env}.go` — the existing layer + its conventions (package doc, one-directional boundary, no `internal/cli` import). New orchestration files follow the same conventions.
- `internal/cli/lifecycle.go` (991 LOC, 22 funcs) + `internal/cli/cluster.go` (664, 21 funcs) — the move source. `internal/cli/root.go` (the `PersistentPreRunE` chokepoint + `resolvedFlags`), `internal/cli/selfheal.go` (Sprint 14 — **stays in cli**, do not move).
- `prompts/sprint15/staff.md` + `issues/issue_sprint15_staff.md` §Closure — the phase-1a precedent (chokepoint shape, delegators, parity discipline). Same posture, larger scope.

## Coordinate with parallel agents

Validator runs the parity gate; architect writes the light CHANGELOG block; tech-writer reviews read-only. **Do not touch their surfaces.**

## Tasks (priority order — TWO staged commits)

### 1. Move lifecycle orchestration → `internal/orchestration`

`runUp`/`runTrialUp`/`runPlan`/`runApply`/`runDown`/`runTrialDown` + helpers (`openTF`, `writeAndInit`, `applyWithRetry`, `tryAutoKubeconfig`/`tryAutoJumphost`/`tryAutoClusterJumphosts`, `resolveClusterIdentity`, `terraformBackendSpec`, `runTerraformLifecycleDocker`, `dockerTerraform*`, `hostUIDGID`, `looksTransient`) move into new `internal/orchestration/*.go`. `internal/cli/lifecycle.go` shrinks to thin cobra `RunE` shims: bind/read the `flag*` globals + `resolvedFlags`, pass them as explicit parameters/an inputs struct into `orchestration`, return its error. **No `orchestration`→`internal/cli` import.** The thin delegating wrappers (`resolveVarFiles` etc.) stay as-is (pinned by pre-existing tests — zero test diffs). Run the **parity gate** (below); commit when green.

### 2. Move cluster / remote-passthrough orchestration → `internal/orchestration`

`runShell`/`runExec`/`runKubeconfig*`/`runKubectlPassthrough`/`runOCPassthrough`/`runIBMCloudPassthrough`/`runPassthrough`/`dispatchBackend`/`resolveBackendSpecWith`/`ensureIBMCloudLoggedIn`/`envValue`/`runWithEnv`/`clusterFromTFOutput` + `extractWorkspaceFlag`/`extractOnFlag`/`extractBackendFlag` move into `internal/orchestration`. `internal/cli/cluster.go` → thin shims. **`selfheal.go` and the chokepoint/env layer stay where they are.** Re-run the parity gate; commit when green.

### 3. Orchestration test additions only

If a newly-exported orchestration entry point warrants direct coverage, add `internal/orchestration/*_test.go` (a NEW file = an addition, allowed). **Never edit a pre-existing `*_test.go`** — if a moved symbol was referenced by an existing in-`cli` test, keep a thin `cli` shim so that test compiles and passes byte-unchanged. An edited pre-existing test fails the parity gate.

### 4. Close `issues/issue_sprint16_staff.md` Issue 1

`Status: resolved` + a `### Closure`: what moved, the inputs-struct/param shape replacing the flag globals, the two staged commits, parity-gate results after each, confirmation that `orchestration` does not import `cli` and no pre-existing test was edited.

## Parity gate (run after EACH staged commit, before reporting done)

```
B=v1.6.0
git diff --stat $B -- $(git ls-tree -r $B --name-only | grep '_test\.go$')   # MUST be empty
git diff --stat $B -- internal/cli/lifecycle_e2e_test.go internal/cli/lifecycle_e2e_integration_test.go internal/cli/env_split_test.go internal/cli/chokepoint_guard_test.go  # MUST be empty
grep -rq 'internal/cli"' internal/orchestration/ && echo VIOLATION || echo boundary-clean   # must be boundary-clean
go build ./... && go vet ./... && gofmt -l internal/   # clean
HOME="$(mktemp -d)" KUBECONFIG= go test -race ./...    # all green (CI's exact command, hermetic)
go test -run TestChokepointInvariant ./internal/cli/   # green & the test file unedited
```

Any pre-existing `*_test.go` diff, any `orchestration`→`cli` import, or any non-green package = stop and fix; do not paper over with a test edit.

## Scope guardrails

- Touch ONLY `internal/cli/lifecycle.go`, `internal/cli/cluster.go`, and `internal/orchestration/` (new files). Do NOT touch the other ~27 `cli` files, `selfheal.go`, `root.go` chokepoint logic, `book/`, `CHANGELOG.md`, `docs/`, `prompts/`.
- Do NOT change behavior, flags, output, or error text. This is a move, not a rewrite.
- Do NOT edit any pre-existing test. Do NOT commit the prior-session `.archive`/PM-guide artifacts.
- Do NOT tag or push tags (integrator-owned). Commit on the working branch only.

## Final report

Under 200 words: what moved (lifecycle / cluster), the flag-global→param shape, the two staged commits + parity-gate result after each, boundary-clean confirmation, zero-test-edit confirmation, Issue 1 status.
