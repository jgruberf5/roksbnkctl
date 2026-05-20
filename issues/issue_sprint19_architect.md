# Sprint 19 — architect issues (`init --var-file` doc surface)

> **Sprint 19 frame.** First regular work sprint post-`v1.6.3`.
> Architect owns the book chapter that introduces `init` + the
> auto-generated CLI reference, plus a cross-chapter sweep to point
> existing "supply `--var-file` on every command" prose at the new
> `init --var-file` flow.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — doc: introduce `init --var-file` in the init chapter + regen the CLI reference

**Severity**: low (docs-only follow-up; the high-sev work is staff Issue 1's code)
**Status**: open

### Motivation

Staff Issue 1 adds `roksbnkctl init --var-file <path>` to skip the interactive interview and persist the operator's tfvars to workspace state. Without the book pinning this flow, users won't know it exists; the existing "supply `--var-file` on every command" prose in other chapters will misroute them; and `book/src/27-command-reference.md` (auto-generated) goes stale exactly the way Sprint 18 architect Issue 1's missing `cos bucket get` subsection did.

### Tasks

1. **Find the init chapter** in `book/src/SUMMARY.md` (likely `book/src/04-…` / `05-…` / `06-…`; verify) and add a §"Skip the interview: `init --var-file`" subsection covering:
   - When to use it (existing `terraform.tfvars` in hand, scripted runs, multi-workspace operator).
   - The single-command flow:
     `roksbnkctl init -w myws --var-file ./terraform.tfvars`.
   - What gets persisted: `config.yaml` seeded from the tfvars's fields **and** a copy of the var-file at `~/.roksbnkctl/myws/state/terraform.tfvars.user` and `~/.roksbnkctl/myws/state-cluster/terraform.tfvars.user`, mode `0600`.
   - Why it matters: subsequent `up` / `plan` / `apply` / `down` against `-w myws` alone Just Work; `--var-file <path>` on later commands still overrides.
   - **Secrets-on-disk note** (one short paragraph): the `ibmcloud_api_key` from the operator's `./terraform.tfvars` lands at `~/.roksbnkctl/<ws>/state/terraform.tfvars.user` (and the cluster sibling), mode `0600`. Same posture as the repo-root file the operator copied from; just relocated under workspace state.
   - **Diagnostics paragraph** at the end: if `down -w <ws>` (no `--var-file`) errors with `No value for required variable …`, the workspace was either `init`-ed without `--var-file` (re-run with the flag) or the `terraform.tfvars.user` was removed (re-init or pass `--var-file` on the destroy).

2. **Regenerate `book/src/27-command-reference.md`**:
   ```
   go run ./tools/refgen/cobra-md > book/src/27-command-reference.md
   ```
   Run from repo root. Verify `--var-file` now appears under the `init` subsection.

3. **Cross-chapter sweep**: grep `book/src/**.md` for prose that says "supply `--var-file` on every command" / "you must pass `--var-file` to" / "remember to include `--var-file`" — fix each to recommend the new `init --var-file` flow. Don't rewrite chapters; add or amend a single sentence per drift point.

### Acceptance criteria

1. The init chapter has a new §"Skip the interview: `init --var-file`" subsection covering all five bullet points above (when-to-use, flow, what's persisted, why-it-matters, secrets-on-disk, diagnostics).
2. `book/src/27-command-reference.md` shows `--var-file` under `### roksbnkctl init` after the regen.
3. The cross-chapter sweep finds every "pass `--var-file` on every command" instance and amends it; no stale advice remains.
4. No Go code, scripts, workflows, or `book.toml` touched.

### Out of scope

- Restyling the init chapter or moving it in `SUMMARY.md` — additive only.
- A new chapter on workspace persistence — the existing init chapter is the right home.
- PDF rebuild — Sprint 18 already validated the mermaid pipeline; the integrator rebuilds during release prep.

### Files likely touched

- `book/src/0[4-6]-…md` (the init chapter — exact filename verified via SUMMARY.md).
- `book/src/27-command-reference.md` (regenerated, not hand-edited).
- 1–3 other `book/src/*.md` files if the cross-sweep finds drift.

### Related

- Sprint 18 architect Issue 1 (mermaid PDF) — same regen discipline applies; the v1.6.3 cycle caught a missed regen and added it post-integration. Sprint 19 architect lands the regen up front to skip that round.
- Staff Issue 1 (this sprint) — the code-side companion; this doc work is meaningless without it.
