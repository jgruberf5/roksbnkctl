# Sprint 16

**Theme:** consolidation phase-1b — empty `internal/cli/lifecycle.go` + `cluster.go` (~1,655 LOC of RunE orchestration) into `internal/orchestration`; `internal/cli` becomes a thin cobra adapter. The deferred second half of the Sprint 15 `cli` decomposition.

_Consolidation / debt-paydown cycle. **No PRD, no book surface.** Design surface = `docs/PLAN.md` §"Sprint 16" + §"Sprint 15 → Scope decision" (integrator-authored). Consolidation tier per `NEW_PROJECT_STARTING_POINT.md` §"Tiering the sprint process by change size": **full staff + validator, light architect + tech-writer**. Version is integrator-owned at cut: `v1.6.1` under strict SemVer (no behavior change) or `v1.7.0` if judged minor-worthy._

Integrator decisions baked in (decided — do not relitigate):

1. **Zero user-visible behavior change.** Strictly internal. Headline gate = **behavior parity**: the entire pre-existing unit + integration suite, **including the Sprint 14 e2e/`--on` suite**, passes with **zero test-file diffs vs the `v1.6.0` baseline tag**. A pre-existing test edited to accommodate the move is drift → fails the gate, it is not a fix.
2. **The Sprint 14 e2e/`--on` suite is the parity harness, not a deliverable** — consume it unchanged; do not rebuild or modify it. The Sprint 15 `chokepoint_guard_test.go` likewise stays green & unedited.
3. **Phase-1b scope is exactly `lifecycle.go` + `cluster.go`.** The other ~27 `cli` files are a tracked phase-2 follow-up — do **not** touch them. The chokepoint/env layer and `selfheal.go` are already correctly placed — do not move them.
4. **`internal/orchestration` must never import `internal/cli`** (one-directional boundary, asserted). Moved code takes flag values as parameters/an inputs struct, not via `cli` package globals.
5. **Must not regress** the Sprint 14 kubeconfig fix (cloud-init + `--on` self-heal) or the Sprint 15 chokepoint — their guards are part of the parity gate.

Four-agent dispatch (consolidation tier):

- **Staff (full)** — code deliverables 1 + 2: move lifecycle then cluster/remote-passthrough orchestration into `internal/orchestration`; `lifecycle.go`/`cluster.go` → thin cobra `RunE` shims. Two staged commits (lifecycle, then cluster), re-running the parity gate after each. Closes `issues/issue_sprint16_staff.md` Issue 1.
- **Validator (full)** — seven-step regression sweep with the behavior-parity assertion as headline gate (zero test-file diffs vs `v1.6.0`, Sprint 14 + chokepoint guards green & unedited), the full hermetic `go test -race ./...`, and the `cli` phase-1b boundary/import audit. If this agent's session is toolchain-denied (Sprint 15 precedent), record the blocker and the integrator runs the gate. Closes `issues/issue_sprint16_validator.md`.
- **Architect (light)** — `CHANGELOG.md` block (`### Changed` = internal decomposition, explicitly "no user-visible behavior change"). Confirm `docs/PLAN.md` §"Sprint 16" final/consistent (already landed — verify, do not rewrite). No PRD, no book; file a one-line "no surface" record if nothing else.
- **Tech-writer (light, read-only)** — drift sweep confirming zero user-visible/doc drift from an internal-only refactor; GREEN/RED launch verdict. Files only `issues/issue_sprint16_tech-writer.md`.

The tag + version designation are integrator-owned, cut only after the gate: behavior parity (zero diffs) + `cli` phase-1b boundary clean + Sprint 14/15 guards green & unedited + all four ledgers terminal.

## Carry-over considerations

- Design surface = `docs/PLAN.md` §"Sprint 16" (integrator-authored — staff/validator build from it; architect does not rewrite it).
- Parity baseline = the `v1.6.0` tag. The Sprint 14 e2e/`--on` suite + the Sprint 15 `chokepoint_guard_test.go` are the harness — green-and-unedited through the move is a gate criterion.
- Move in two staged commits (lifecycle, then cluster); re-run the parity gate after each so a regression is localized.
- Prior-session `.archive`/PM-guide artifacts remain untracked/out-of-scope — the integrator's call, never folded into sprint commits.
