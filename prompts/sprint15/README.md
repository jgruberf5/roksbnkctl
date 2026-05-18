# Sprint 15

**Theme:** consolidation: root-cause the boundary-bug class + decompose the `cli` god-package (consolidation cycle, post-`v1.5.0`)

_Consolidation / debt-paydown cycle targeting `v1.6.0` (the integrator may re-designate `v1.5.1` under strict SemVer — **no user-visible behavior change** this cycle; tag/version is integrator-owned at cut). The design surface is `docs/PLAN.md` §"Sprint 15" — there is **no PRD** (zero new features) and **no book surface** (internal-only). Run at the **consolidation tier** per `NEW_PROJECT_STARTING_POINT.md` §"Tiering the sprint process by change size": full staff + validator, light architect + tech-writer._

Integrator decisions baked in (see `docs/PLAN.md` §"Sprint 15"):

1. **Zero behavior change.** This is strictly internal hardening. The headline gate is **behavior parity**: the entire pre-existing unit + integration suite — *including the Sprint 14 e2e + `--on` integration suite* — must pass **unchanged, zero test-file diffs**. A test that needs editing to pass is a drift signal and fails the gate, not a test fix.
2. **The Sprint 14 e2e/`--on` suite is the parity harness, not a deliverable.** The blind-spot test was pulled forward into Sprint 14 and already landed. Sprint 15 *consumes* it as the refactor's behavior-parity gate — do **not** rebuild or modify it.
3. **`cli` decomposition is phased.** Phase 1 scope is *exactly* `internal/cli/lifecycle.go` + `internal/cli/cluster.go` extracted into a new `internal/orchestration` service layer. The remaining ~27 `cli` files are an explicitly deferred, tracked follow-up — do **not** touch them.
4. **Out of scope, must not regress:** the Sprint 14 cloud-init + `--on` self-heal kubeconfig fix (option C) — its e2e/`--on` guard is part of the parity gate; per-AZ stale-target reconcile option (b) — still a post-`v1.5.0` follow-up.

Why now (evidence, from the Sprint-13-close health review): Sprint 12 Issues 1+2 (`--var-file`, `--tf-source`) and Sprint 13 Issue 1 (KUBECONFIG leak) are the *same* defect shape — a value correct in the invocation context is consumed in a different context — each patched as an instance (`resolveVarFiles` at 8+ RunE sites; `--tf-source` normalized separately; `workspaceEnv()` split into `workspaceEnvCore`/`remoteSafeEnv` + `localPathEnvKeys` scrub). No single chokepoint → the next path/env flag re-opens the class. And `internal/cli` is 61% of internal LOC (`lifecycle.go` 1058, `cluster.go` 739). Consolidation, not a greenfield restart, is the correct response.

Four-agent dispatch (consolidation tier):

- **Staff (full)** — code deliverable 1 (single path/env normalization chokepoint, `cli.ResolvedFlags`), code deliverable 2 (`internal/cli` → `internal/orchestration` phase-1 decomposition of `lifecycle.go` + `cluster.go`), code deliverable 3 (chokepoint-invariant guard test + `internal/cos` coverage). Closes `issues/issue_sprint15_staff.md` Issue 1.
- **Validator (full)** — seven-step regression sweep with the **behavior-parity assertion as the headline gate** (zero test-file diffs, Sprint 14 guards green & unedited), plus the chokepoint-invariant `grep` audit and the `cli`-phase-1-boundary import audit. Closes `issues/issue_sprint15_validator.md`.
- **Architect (light)** — `CHANGELOG.md`: a `v1.6.0` block (`### Changed` = internal consolidation, explicitly "no user-visible behavior change"; `### Removed` = the obviated `remoteSafeEnv`/`localPathEnvKeys` scrub). Confirm `docs/PLAN.md` §"Sprint 15" and `NEW_PROJECT_STARTING_POINT.md` §"Tiering the sprint process by change size" are final/consistent (both already landed — verify, do not rewrite). **No PRD, no book** — light cycle; file a one-line "no surface" record if nothing else.
- **Tech-writer (light, read-only)** — drift sweep confirming **zero user-visible / doc drift** from an internal-only refactor (CHANGELOG `v1.6.0` ↔ PLAN §"Sprint 15" ↔ the as-landed code; the "no behavior change" claim is true); GREEN/RED launch verdict for `v1.6.0`. Files only `issues/issue_sprint15_tech-writer.md`.

The `v1.6.0` (or `v1.5.1`) tag and version designation remain integrator-owned, cut only after the gate: behavior parity (zero diffs) + single-chokepoint proven + `cli` phase-1 boundary clean + all four ledgers terminal.

## Carry-over considerations

- Design surface = `docs/PLAN.md` §"Sprint 15" (integrator-authored — staff/validator build from it; architect does **not** rewrite it).
- The Sprint 14 e2e/`--on` suite (`internal/cli/lifecycle_e2e_test.go`) is the parity harness — green-and-unedited through the refactor is a gate criterion.
- Enumerate every current `resolveVarFiles` / `workspaceEnv*` / `dispatchRemote` site **before** deleting any of it (chokepoint must subsume every special case a scattered site handled).
- Sprint 15 process deliverable is largely already landed (`NEW_PROJECT_STARTING_POINT.md` tiering section committed `f38f171`; PLAN §"Sprint 15" committed) — this cycle finalizes/verifies consistency, it does not re-author them.
- Prior-session untracked files (`.archive/*`, `make_PM_Guide_book_pdf.sh`) remain the integrator's call; out of scope.
