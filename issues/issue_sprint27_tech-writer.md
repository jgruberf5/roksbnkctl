# Sprint 27 — tech-writer issues (post-integration drift sweep)

> **Sprint 27 frame (re-pivoted 2026-06-04).** Light drift sweep AFTER the
> three-way integration lands the **terraform-native** BNK phase
> (`helm_release` installs + `alekc/kubectl` `kubectl_manifest` + `wait_for`
> for the CRs — no Go reconciler, no custom provider). Verifies the integrated
> tree's operator-facing surface (BNK-phase chapter, command/config reference,
> CHANGELOG) matches the built binary + the new terraform behavior, and that
> any speed claim traces to the validator's measured benchmark. Ends on a
> GREEN/RED verdict.

`Status`: open (re-pivoted)

---

## Issue 1 — Drift sweep over the integrated terraform-native BNK tree

**Severity**: low
**Status**: open

Build the integrated binary, then sweep:

1. **Command / config reference**: the install-mode surface — whatever staff
   exposed (`--legacy-bnk` flag and/or a `bnk_cr_mode` config/tfvar) — matches
   the binary; any optional `bnk status` matches if it landed. If `bnk up`/`down`
   help text changed, reflect it.
2. **BNK-phase chapter** (architect-authored): reconcile prose vs reality — the
   phase is now `helm_release` installs + `kubectl_manifest` + `wait_for` (real
   `.status` gating: CNEInstance `Available`, License `state`), the CRs are real
   terraform state (`plan`/`destroy`/drift), and the install-mode flag keeps the
   legacy curl path for the transition. Re-capture any illustrative transcript
   (`terraform apply` / `bnk up` output) byte-for-byte; drop the illustrative
   note.
3. **Speed claim**: any "faster"/timing numbers must match the validator's
   measured **kubectl-vs-legacy** benchmark — do NOT ship an unverified "Nx
   faster"; quote the measured delta or phrase qualitatively if no number is
   pinned.
4. **Stale sweep**: update prose that calls the BNK phase `null_resource`/`curl`/
   `time_sleep`-driven; confirm the "IBM IAM trusted-profile + COS reads stay in
   terraform" note is present and the new `alekc/kubectl` provider dependency
   (+ air-gap mirror caveat) is documented.
5. **CHANGELOG**: user-facing — leads with the faster, in-state, watch-gated BNK
   phase (real `plan`/`destroy` for the CRs); notes the legacy curl path remains
   behind the install-mode flag this release; notes the new provider dependency;
   notes (per integrator) it's on a feature branch / not yet default if
   applicable.

### Acceptance criteria
1. Install-mode surface (+ any `bnk status`) in the reference matches the binary.
2. BNK-phase chapter matches the terraform-native reality; transcript captured.
3. Speed numbers trace to the validator's measured kubectl-vs-legacy benchmark.
4. No stale "`null_resource`/`curl`/`time_sleep`-driven BNK" prose survives; the
   new provider dependency + air-gap caveat documented.
5. CHANGELOG user-facing + notes the install-mode flag. mdbook builds clean.
   GREEN/RED verdict recorded.

### Files affected (probable)
- `book/src/**` (BNK-phase chapter, command/config reference), `CHANGELOG.md`.
- No `internal/`, `cmd/`, `terraform/` code edits (reflected-doc reconciliation).

### Related
- `issues/issue_sprint27_architect.md` (incl. Spike rounds 1 & 2) + `..._staff.md`
  closures — the prose + terraform behavior to reconcile.
- `issues/issue_sprint27_validator.md` — the measured benchmark + License-literal
  confirmation any claim must trace to.
- Per-finding fields use `**Verdict**:` (not `**Status**:`) per the `a2b78da`
  convention.
