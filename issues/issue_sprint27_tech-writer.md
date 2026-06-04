# Sprint 27 — tech-writer issues (post-integration drift sweep)

> **Sprint 27 frame.** Light drift sweep AFTER the three-way integration
> lands the native watch-driven BNK reconciler. Verifies the integrated
> tree's operator-facing surface (BNK-phase chapter, command reference,
> CHANGELOG) matches the built binary's actual behavior — the `bnk up
> --native` flow, `bnk status`, the per-phase timings, and the legacy-flag
> transition — and re-captures illustrative output byte-for-byte. Ends on a
> GREEN/RED launch verdict.

`Status`: open

---

## Issue 1 — Drift sweep over the integrated native-BNK tree

**Severity**: low
**Status**: open

Build the integrated binary first, then sweep:

1. **Command reference** — `roksbnkctl bnk up --help`, `bnk down --help`, and
   the new `bnk status` match the binary; the `--native` / legacy-terraform
   flag and any `--timeout`/`--timings` flags are reflected.
2. **BNK-phase chapter** (architect-authored) — reconcile the prose against
   the binary: the cert-manager → FLO → CNEInstance → License flow, what each
   phase watches on, and the `bnk status` output. Re-capture any illustrative
   transcript byte-for-byte; drop the "illustrative" note.
3. **Speed claim** — the chapter/CHANGELOG should describe the speedup
   concretely. Confirm any quoted timing/benchmark numbers match what the
   validator's gated-live run actually measured (don't ship an unverified
   "Nx faster" — quote the measured native-vs-terraform delta or phrase it
   qualitatively if the integrator hasn't pinned a number).
4. **Stale-reference sweep** — find prose that says the BNK phase is
   terraform-driven / uses `time_sleep` waits and update it (the native path
   is now the story; note the legacy flag still exists during transition).
   Confirm the "IBM IAM trusted-profile + COS reads stay in terraform" note
   is present and correct.
5. **CHANGELOG** — user-facing entry: leads with the faster, watch-driven BNK
   phase + live `bnk status`, notes the legacy terraform path remains behind
   a flag this release, and that this is on a feature branch / not yet the
   default if that's the integrator's call at cut time.

### Acceptance criteria
1. `bnk up/down/status --help` in the command reference match the binary.
2. BNK-phase chapter matches the binary; transcript captured (no illustrative
   placeholder).
3. Any speedup numbers trace to the validator's measured benchmark.
4. No stale "terraform-driven / `time_sleep`" BNK prose survives un-updated.
5. CHANGELOG user-facing + notes the legacy flag. mdbook builds clean.
   GREEN/RED verdict recorded.

### Files affected (probable)
- `book/src/**` (BNK-phase chapter, command/config reference), `CHANGELOG.md`.
- No `internal/`, `cmd/`, `terraform/` code edits (reflected-doc
  reconciliation against the binary).

### Related
- `issues/issue_sprint27_architect.md` + `..._staff.md` closures — the prose
  + binary surface to reconcile.
- `issues/issue_sprint27_validator.md` — the measured benchmark numbers any
  speed claim must trace to.
- Per-finding fields use `**Verdict**:` (not `**Status**:`) per the `a2b78da`
  convention.
