You are the staff engineer agent for Sprint 12 of the roksbnkctl project. Sprint 12 is a **patch cycle** — `v1.4.1` — landing a single bug fix for `--var-file` relative-path resolution that surfaced during v1.4.0 user-side live verify. Your scope is `internal/cli/lifecycle.go`, the matching wire-ups in `internal/cli/cluster_phase.go` and `internal/cli/bnk_phase.go`, and the supporting unit test in `internal/cli/lifecycle_test.go`. **Do not touch `book/`, `CHANGELOG.md`, `docs/`, `Makefile`, `scripts/`** — those are architect / validator surfaces.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

## Read first

- `issues/issue_sprint12_staff.md` — **the design surface for this sprint**. Issue 1 §"Root cause", §"Proposed fix", §"Files affected", and §"Acceptance criteria" spell out the work. Don't deviate from the acceptance criteria without surfacing why in your closure block.
- `internal/cli/lifecycle.go:42` — `flagVarFiles` declaration.
- `internal/cli/lifecycle.go:96-101` — `--var-file` flag registration on the `up`/`plan`/`apply`/`down` command set.
- `internal/cli/lifecycle.go:178, 198, 222, 243, 319` — the five `tfws.Plan` / `applyWithRetry` / `tfws.Destroy` call sites that consume `flagVarFiles`.
- `internal/cli/lifecycle.go:712-721` — existing docker-backend handling that rejects relative paths with a clear error. Your fix should make this branch redundant for the common case (every reachable path is already absolute by the time it's reached); leave the explicit reject in place as a defensive guard.
- `internal/cli/cluster_phase.go:106-108, 271-278` — `clusterUpCmd` / `clusterDownCmd` registration and `varFiles := append([]string{}, flagVarFiles...)` consumption.
- `internal/cli/bnk_phase.go:66-68` — `bnkUpCmd` / `bnkDownCmd` registration of the same flag.
- `prompts/sprint11/staff.md` — prior-sprint prompt structure; the build-verify-test loop is the same.

## Coordinate with parallel agents

An **architect** agent is updating CHANGELOG `v1.4.1` entry, PLAN.md Sprint 12 section, and two chapter 6 polish nudges. **Do not touch `book/`, `CHANGELOG.md`, `docs/`.**

A **validator** agent will do the seven-step regression sweep, reproduce the bug per `issues/issue_sprint12_staff.md` §"Reproduce", and confirm your fix makes it pass. They need your `resolveVarFiles` helper landed and the wire-ups complete before they can verify.

A **tech-writer** agent does read-only review at end of sprint (after staff/architect/validator return).

## Tasks (priority order)

### 1. `resolveVarFiles` helper

Land the helper exactly as sketched in `issues/issue_sprint12_staff.md` §"Proposed fix" (the code block there), with one judgment call:

- **Where the helper lives**: `internal/cli/lifecycle.go` is fine if it's the smallest surface. If you find a natural shared-helpers file under `internal/cli/` already, prefer placing it there. Don't create a new package or file for one helper.
- **Error message shape**: when `os.Stat` fails on the resolved absolute, the error must name *both* the user-supplied input *and* the resolved absolute, so users can distinguish "I typoed the filename" from "I'm in the wrong directory". The issue file calls this out explicitly.

### 2. Wire into the five consumption sites

Walk the `flagVarFiles` slice through `resolveVarFiles` at the earliest convenient point — the RunE function (or a `preRun` helper if one exists for this command set). One normalization pass per command invocation; downstream callers (`tfws.Plan`, `applyWithRetry`, `tfws.Destroy`, the cluster-phase / bnk-phase variants) receive the already-absolute slice.

Five call sites the issue lists:
- `internal/cli/lifecycle.go:178` (`tfws.Plan` in plan flow)
- `internal/cli/lifecycle.go:198` (`applyWithRetry` in up flow)
- `internal/cli/lifecycle.go:222` (`tfws.Plan` in apply flow)
- `internal/cli/lifecycle.go:243` (`applyWithRetry` in apply flow)
- `internal/cli/lifecycle.go:319` (`tfws.Destroy` in down flow)

Plus the analogous cluster-phase / bnk-phase paths that share `flagVarFiles`. Decide whether one normalization-at-top-of-RunE pattern serves all, or whether each command needs its own — keep the duplication minimal.

### 3. Unit test in `internal/cli/lifecycle_test.go`

Match `issues/issue_sprint12_staff.md` §"Acceptance criteria":

- absolute pass-through (input `/abs/path/foo.tfvars` → output unchanged)
- relative resolved against CWD (input `./foo.tfvars` from `t.TempDir()` → output `<tempdir>/foo.tfvars`)
- missing-file error message names both the input and resolved path (`grep` the error string for both substrings)
- `~`-expansion: check the project convention first (`grep -rn '"~/' internal/cli/` to see if anything else handles it) — if the project's `filepath.Abs` is sufficient elsewhere, keep this fix consistent; if there's an `os.ExpandEnv` / `~`-expansion helper, route through it.

If the test file doesn't exist, create it. If it does, append the new tests rather than overwriting.

### 4. Close `issues/issue_sprint12_staff.md` Issue 1

Flip `**Status**: open` → `**Status**: resolved`. Add a `### Closure` block recording:
- The helper's actual location (file + function name).
- The five wire-up sites and any pattern you chose for them (single top-of-RunE pass vs. per-command).
- The unit-test names and pass count.
- Whether you found `~`-expansion handling elsewhere in the project and how that informed your test coverage.
- Build/test sweep results: `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./internal/cli/...`, `go test ./...`, `make staticcheck`.

## Build/test loop

After each meaningful edit, run at minimum:

- `go build ./...` — must return clean.
- `go vet ./...` — clean.
- `gofmt -l .` — empty.
- `go test ./internal/cli/...` — green; the new tests pass and no existing test regresses.
- `go test ./...` — green across the whole module.
- `make staticcheck` — clean.

If `staticcheck` is unavailable (unlikely on this host — Sprint 11 ran it), skip that gate and note it in your closure block.

## Scope guardrails

- Do NOT touch `book/`, `CHANGELOG.md`, `docs/`, `Makefile`, `scripts/`, `internal/tf/`, `internal/config/` (except passing through `flagVarFiles` to existing exported APIs).
- Do NOT modify `prompts/`.
- Do NOT commit. Do NOT push.
- The fix is intentionally small. If you're tempted to refactor `flagVarFiles` into a struct or split commands — don't; out of scope.

## Verification before reporting done

- Reproduce recipe from `issues/issue_sprint12_staff.md` §"Reproduce" walks through cleanly *in unit-test form* (you can't run the binary against a real workspace from the agent shell).
- All five wire-up sites use the normalized slice; `grep -n "flagVarFiles" internal/cli/` shows the call sites and you can trace each one to a normalization point upstream.
- Docker-backend's `lifecycle.go:718-720` reject path is now reachable only on a programming error (every entry passed in is already absolute) — leave the reject in place; note this in the closure block.

## Final report

Under 200 words. Cover: the helper's location + signature, the wire-up pattern you chose, the unit-test names and pass/fail count, the build/test sweep results, any unexpected surface (e.g., `~`-expansion handling elsewhere in the project), and the Issue 1 status flip. If anything in the proposed fix didn't fit the codebase as imagined (e.g., the call sites had a wrapper that made the top-of-RunE normalization awkward), surface the deviation and why.
