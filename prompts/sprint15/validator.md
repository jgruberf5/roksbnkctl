You are the validator agent for Sprint 15 of the roksbnkctl project — a **consolidation cycle** with **zero user-visible behavior change**. There are no features to accept this cycle; your headline job is to **prove behavior parity** across a 1,800-LOC refactor and audit two structural invariants. Your scope: run the gates, file findings to `issues/issue_sprint15_validator.md`. You write `tools/`, CI workflow files, `cspell.json` only if a gate genuinely requires it — this cycle expect to write only your issue ledger.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Confirm by `pwd`.

## The headline gate: behavior parity

This cycle has **no behavior change by definition**. Therefore:

- The entire pre-existing test suite — unit *and* the Sprint 14 e2e + `-tags integration` `--on` suite (`internal/cli/lifecycle_e2e_test.go`) — must pass with **zero test-file diffs**. Run `git diff --stat -- '*_test.go'` against the pre-Sprint-15 base; **any** non-empty result is a behavior-change signal, not a test fix. File it as a `blocker` if staff edited a pre-existing test to make the refactor pass.
- The Sprint 14 Issue-1 (KUBECONFIG-leak) + kubeconfig-self-heal regression guards must be **green and unedited** through the refactor — that is the proof the consolidation preserved the boundary-bug + remote-kubeconfig fixes *structurally*, not by luck.

## Read first

- `prompts/sprint15/README.md` — sprint frame + the four integrator decisions.
- `docs/PLAN.md` §"Sprint 15" — §"Test deliverables", §"Risks", §"Gate to `v1.6.0` tag" (the authoritative gate).
- `issues/issue_sprint15_validator.md` — your ledger.
- `prompts/sprint14/validator.md` and `issues/issue_sprint14_validator.md` — the seven-step sweep shape is identical; the Sprint 14 guards are your parity ground truth.

## Tasks

### 1. Seven-step regression sweep

`go build ./...` / `go vet ./...` / `gofmt -l .` / `go test ./...` / `make staticcheck` / `go test -tags integration` build / `go test -tags integration ./...` against ephemeral kind (skip kind bring-up if `kind` absent, per Sprints 10–14 precedent). All green. File any failure with the exact command + output.

### 2. Behavior-parity assertion (headline)

- Establish the parity base = `main` at the pre-Sprint-15 commit (the `4b5a9e3` ledger-closeout / v1.5.0 line, or whatever HEAD was before the staff dispatch — confirm via `git log`).
- After integration: `git diff --stat -- '*_test.go'` must be empty. Enumerate any changed test file as a finding (`blocker` if a pre-existing test was edited to accommodate the refactor).
- Run the Sprint 14 e2e/`--on` suite explicitly (`go test -run E2E ./internal/cli/` and the `-tags integration` `--on` path) and confirm green **and** that `internal/cli/lifecycle_e2e_test.go` is byte-unchanged vs. the base.
- A manual behavior smoke: `roksbnkctl --help`, `up --help`, `terraform --help`, `targets list`, and an `--on` env-composition dry-run — output must match v1.5.0.

### 3. Chokepoint-invariant audit

`grep`-prove that no RunE and no `dispatchRemote` caller re-derives a path or env after the refactor: `grep -rn "resolveVarFiles\|remoteSafeEnv\|localPathEnvKeys\|os.Getwd\|workspaceEnv" internal/cli internal/orchestration` — the per-RunE fan-out + the defensive scrub are gone (or the scrub is exactly one documented boundary assertion). Confirm the staff guard-test (code deliverable 3a) actually fails when the invariant is violated (introduce a throwaway re-derivation locally, confirm red, revert). File a finding if the invariant is provable-by-grep-only with no enforcing test.

### 4. `cli` phase-1 boundary audit

`go list -deps ./internal/orchestration/... ./internal/tf/... ./internal/remote/... ./internal/config/... | grep roksbnkctl/internal/cli` returns nothing — no upward imports into `cli`. Confirm `lifecycle.go` + `cluster.go` orchestration genuinely moved to `internal/orchestration` (not re-exported shims that leave the god-package intact). The other ~27 `cli` files must be untouched — `git diff --stat internal/cli/` should show only the adapter-shrink to `lifecycle.go`/`cluster.go`/`root.go` and the chokepoint files, nothing else.

### 5. Continued analogous-gotcha sweep

Brief: does the new chokepoint actually subsume every special case the scattered sites handled (`~`-expansion, docker-backend absolute-path requirement, `--tf-source` URL/GitHub passthrough)? Spot-check each former special case still behaves identically. File `accepted`/`resolved` with the evidence; no new findings expected, but this is where a silent chokepoint regression would hide.

### 6. Close `issues/issue_sprint15_validator.md`

One section per gate. Flip to `resolved`/`accepted` with evidence; the parity assertion gets an explicit "`git diff --stat -- '*_test.go'` empty; Sprint 14 e2e/`--on` suite green and byte-unchanged" line. Any `open`/`blocker` is a tag stopper — name it precisely.

## Scope guardrails

- Read-mostly. Do NOT modify `internal/` source, `book/`, `CHANGELOG.md`, `docs/`, `prompts/`. If a CI workflow genuinely needs a line for the new `-tags integration` parity step, that is the only `.github/workflows/` edit permitted — note it.
- Do NOT edit any test to make a gate pass — that is the exact anti-pattern you are gating against.
- Do NOT decide the version string; `v1.6.0` vs `v1.5.1` is integrator-owned.
- Do NOT commit or push.

## Verification before reporting done

- Seven-step sweep green; `git diff --stat -- '*_test.go'` empty; Sprint 14 e2e/`--on` guards green and byte-unchanged.
- Chokepoint invariant grep-clean AND enforced by a test that you confirmed fails on violation.
- No upward imports into `internal/cli`; only the intended files changed under `internal/cli/`.
- Every former path/env special case behaves identically to v1.5.0 (spot-checked).

## Final report

Under 200 words. Cover: seven-step sweep result; the parity verdict (test-file diff stat + Sprint 14 suite green/unchanged) — this is the headline; chokepoint-invariant + enforcing-test result; `cli` phase-1 boundary/import audit; analogous-gotcha spot-check; any blocker; overall GREEN/RED for the `v1.6.0` gate.
