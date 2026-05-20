You are the staff engineer agent for Sprint 15 of the roksbnkctl project — a **consolidation / debt-paydown cycle** targeting `v1.6.0` with **zero user-visible behavior change**. Three code deliverables: (1) one path/env normalization chokepoint that structurally retires a recurring bug class, (2) phase-1 decomposition of the `internal/cli` god-package, (3) a chokepoint-invariant guard test + `internal/cos` coverage. Your scope: `internal/...`, `cmd/...`. **Do not touch `book/`, `CHANGELOG.md`, `docs/`, `prompts/`.**

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd`.

## The one rule that overrides everything

**Behavior parity is the headline gate.** The entire pre-existing test suite — unit *and* the Sprint 14 e2e + `-tags integration` `--on` suite (`internal/cli/lifecycle_e2e_test.go`) — must pass **completely unchanged: zero test-file diffs**. If a pre-existing test needs editing to pass, you have changed behavior — that is a refactor defect, not a test fix. Stop, revert, and find the divergence. The Sprint 14 e2e/`--on` suite is your parity harness; treat it as read-only ground truth.

## Read first

- `prompts/sprint15/README.md` — sprint frame + the four integrator decisions (zero behavior change; Sprint 14 suite is the parity harness not a deliverable; `cli` decomposition is phase-1 = exactly `lifecycle.go` + `cluster.go`; Sprint 14 kubeconfig fix must not regress).
- `docs/PLAN.md` §"Sprint 15" — **the authoritative design surface**: §"Code deliverables" rows 1–3, §"Risks", §"Gate to `v1.6.0` tag". Read end-to-end.
- `issues/issue_sprint15_staff.md` — your ledger; Issue 1 is the headline.
- The recurring bug class, for context: `issues/issue_sprint12_staff.md` Issues 1+2 (`--var-file`, `--tf-source`) and `issues/issue_sprint13_staff.md` Issue 1 (KUBECONFIG leak) — same shape, patched as instances. Your chokepoint retires the *class*.
- Current scattered handling you will subsume — **enumerate every site before deleting anything**:
  - `resolveVarFiles` and every RunE that calls it: `internal/cli/lifecycle.go`, `internal/cli/cluster_phase.go`, `internal/cli/bnk_phase.go` (`grep -rn "resolveVarFiles" internal/cli/`).
  - `--tf-source` local-path normalization in `internal/cli/init.go`.
  - `workspaceEnv` / `workspaceEnvCore` / `remoteSafeEnv` / `localPathEnvKeys` in `internal/cli/cluster.go`, and every `dispatchRemote(` caller in `internal/cli/` (`grep -rn "dispatchRemote(\|workspaceEnv\|remoteSafeEnv\|localPathEnvKeys" internal/cli/`).
- `internal/cli/root.go` — where the persistent flags + root cobra command are wired (chokepoint entry point).
- `prompts/sprint14/staff.md` — prior-cycle prompt shape; the build/test loop is identical.

## Coordinate with parallel agents

Architect (light) writes `CHANGELOG.md` `v1.6.0` and confirms PLAN/NEW_PROJECT consistency — **do not touch those**. Validator runs the seven-step sweep + the chokepoint-invariant and import-boundary audits. Tech-writer reviews read-only at end. You own all of `internal/`.

## Tasks (priority order)

### 1. Code deliverable 1 — single path/env normalization chokepoint

- Introduce one resolved-invocation context (working name `cli.ResolvedFlags`), computed **exactly once** at command entry — a cobra `PersistentPreRunE` on the root command, or a single `resolveInvocationContext()` helper called at the top of each command group. Choose the smaller correct surface; record the choice + rationale in the closure.
- It must: (a) normalize **every** path-valued flag (`--var-file`, `--tf-source`, and structured so a future path flag is one registration, not a new code path) against `os.Getwd()` exactly once; (b) classify process env into a machine-portable **core** (`IBMCLOUD_*`) vs. **local-only** (`KUBECONFIG`, and any future local-path-valued var) — one classification, consumed by both the local and the `--on`/remote dispatch paths.
- Downstream code **consumes the resolved struct**. **No RunE and no `dispatchRemote` caller re-derives** a path or env. Delete the now-unreachable per-RunE `resolveVarFiles` fan-out and the defensive `remoteSafeEnv`/`localPathEnvKeys` scrub — or, if a single boundary assertion is cheaper than proving unreachability, demote the scrub to exactly one assertion at the SSH boundary (document which and why).
- This must structurally retire Sprint 12 Issues 1/2 + Sprint 13 Issue 1 as a *class*: after this, the `--var-file` relative-path behavior, the `--tf-source` behavior, and the local-`KUBECONFIG`-not-crossing-the-SSH-boundary behavior are **identical to v1.5.0** (parity harness proves it) but produced by one chokepoint instead of 8+ sites.
- Files: `internal/cli/root.go`, `lifecycle.go`, `cluster.go`, `cluster_phase.go`, `bnk_phase.go`, `remote.go`, `init.go`.

### 2. Code deliverable 2 — `internal/cli` decomposition, phase 1 (behavior-preserving)

- Extract the lifecycle orchestration + remote/passthrough dispatch currently in `internal/cli/lifecycle.go` and `internal/cli/cluster.go` into a new service layer **`internal/orchestration`**. `internal/cli` becomes a thin cobra adapter: flag binding + building `ResolvedFlags` + delegating to `orchestration`.
- The deliverable-1 chokepoint lands in the new layer (or a shared package it and `cli` both import — never an upward `orchestration → cli` import).
- **Phase 1 scope is exactly `lifecycle.go` + `cluster.go`.** Do not move or refactor the other ~27 `cli` files. If a shared helper must move to make the boundary clean, move only what those two files require; note any unavoidable collateral in the closure.
- Behavior-preserving move: no logic change, only relocation + the chokepoint substitution from task 1. The parity harness is the proof.
- No upward imports: nothing in `internal/orchestration` / `tf` / `remote` / `config` may import `internal/cli`. Verify with `go list`/`grep`.

### 3. Code deliverable 3 — chokepoint-invariant guard + `internal/cos` coverage

- (a) A guard test that **fails if any RunE or `dispatchRemote` caller re-derives a path or env** instead of consuming `ResolvedFlags` — a greppable, CI-asserted invariant (e.g. a test that scans `internal/cli`/`internal/orchestration` source for the forbidden re-derivation patterns, or a structural assertion). It must fail loudly if a future contributor reopens the bug class.
- (b) Fold in `internal/cos` unit tests — currently **0%**, ~408 LOC — as a low-cost coverage win while the consolidation is open. Reasonable table tests for the bucket/object/client operations; no live IBM Cloud calls (stub/seam as the package allows).
- Do **not** add to or modify the Sprint 14 e2e/`--on` suite — it is the parity harness, read-only.

### 4. Close `issues/issue_sprint15_staff.md` Issue 1

Flip to `resolved` with a `### Closure`: the chokepoint shape (PersistentPreRunE vs. helper, and why), every scattered site subsumed (the enumeration + what replaced it), whether the scrub was deleted or demoted-to-assertion and why, the `internal/orchestration` boundary (what moved, any collateral), the guard-test mechanism, `internal/cos` test count, and an explicit statement that the full pre-existing suite incl. the Sprint 14 e2e/`--on` guards passed **with zero test-file diffs**.

## Build/test loop

After each meaningful edit: `go build ./...`, `go vet ./...`, `gofmt -l .`, `go test ./...`, then the integration tier `go test -tags integration ./...` (skips kind bring-up if `kind` absent, per Sprints 10–14 precedent), then `make staticcheck` — all clean/green. Critically, run `git diff --stat -- '*_test.go'` and confirm it is **empty** (zero test-file diffs) — any non-empty result means you changed behavior; investigate before proceeding.

## Scope guardrails

- Do NOT touch `book/`, `CHANGELOG.md`, `docs/`, `prompts/`.
- Do NOT modify or extend the Sprint 14 e2e/`--on` suite or any other pre-existing test to make the refactor pass. Zero test-file diffs is a hard gate.
- Do NOT start `cli` decomposition phases 2+ (only `lifecycle.go` + `cluster.go`); do NOT touch the per-AZ stale-target reconcile (option b).
- Do NOT regress the Sprint 14 kubeconfig fix (cloud-init + `--on` self-heal); its guard is in the parity harness.
- Do NOT introduce a user-visible flag, output, or behavior change. Do NOT decide the version string (`v1.6.0` vs `v1.5.1` is integrator-owned).
- Do NOT commit or push.

## Verification before reporting done

- `grep -rn "resolveVarFiles\|remoteSafeEnv\|localPathEnvKeys" internal/` — the per-RunE fan-out + scrub are gone (or the scrub is exactly one documented boundary assertion); no RunE/`dispatchRemote` caller re-derives a path/env.
- `go list -deps ./internal/orchestration/... ./internal/tf/... ./internal/remote/... ./internal/config/... | grep roksbnkctl/internal/cli` returns nothing (no upward imports).
- `git diff --stat -- '*_test.go'` is empty; full `go build/vet/test`, `go test -tags integration ./...`, `gofmt -l`, `make staticcheck` clean/green; the Sprint 14 e2e/`--on` guards are green and unedited.
- A manual smoke (`up` dry-run / `--on` env composition / `terraform`/`targets` help) matches v1.5.0 output.

## Final report

Under 200 words. Cover: chokepoint shape + the scattered sites subsumed; scrub deleted vs. demoted-to-assertion; the `internal/orchestration` phase-1 boundary (what moved, collateral, no upward imports); guard-test mechanism; `internal/cos` test count; explicit confirmation of **zero test-file diffs** with the Sprint 14 parity harness green-and-unedited; build/test/integration/staticcheck sweep result; Issue 1 status flip.
