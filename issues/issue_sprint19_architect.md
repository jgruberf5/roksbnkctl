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
**Status**: resolved

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

---

### Closure (architect, 2026-05-20)

**Status**: resolved (doc deliverables landed; awaiting integrator commit + post-staff regen)

**Chapter updated**: `book/src/06-workspaces.md` — new §"Skip the interview: `init --var-file`" subsection added between "The everyday workspace routine" and "The full command tree" (the natural deepening of the `init` story, adjacent to the on-disk-layout that names the two `state/` and `state-cluster/` dirs the flag writes to). Covers all five required bullets: when-to-use, single-command flow, two-copy persistence (with explanation of why two and not one), why-it-matters (bare `-w <ws>` Just Works for `up` / `plan` / `apply` / `down`; later `--var-file` still wins), secrets-on-disk posture (mode `0600`, same as repo-root `./terraform.tfvars`, with omit-key-for-resolver-fallback option), and a two-state diagnostics paragraph keyed off `No value for required variable …` on bare `down -w <ws>`.

**Cross-references touched**: three chapters, one sentence each (no chapter rewritten):
- `book/src/10-deploying-bnk-trials.md` — extended the var-file chain bullet list (point 2 now names the `state/terraform.tfvars.user` path explicitly and cross-links to Chapter 6 §); added a closing sentence pointing at `init --var-file` as the persistence path for users who'd otherwise pass the same `--var-file` on every command.
- `book/src/12-workspace-config.md` — added a paragraph after "The `terraform.tfvars.user` middle layer is for…" pointing at the new flow as the canonical way to seed both phase copies from an existing `./terraform.tfvars`.
- `book/src/13-terraform-variables.md` — added a new row to the "When to edit `config.yaml` vs `.tfvars.user` vs `--var-file`" decision matrix for the "I have a complete `./terraform.tfvars` already" case, cross-linking to Chapter 6 §.

**Regen verification**: `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md` ran clean (exit 0). Initial pass during this work the regen output was byte-identical (staff's flag not yet wired to the cobra command); staff's work landed on the working tree mid-pass, so I re-ran the regen. The second run captured the new flag — `book/src/27-command-reference.md` line 529 now reads ``| `--var-file` | `string` | — | path to a tfvars file (shaped like terraform.tfvars.example); seeds config.yaml and is copied verbatim to both phase state dirs as terraform.tfvars.user |`` under the `## roksbnkctl init` section. Acceptance criterion 2 met.

**Files touched**: `book/src/06-workspaces.md`, `book/src/10-deploying-bnk-trials.md`, `book/src/12-workspace-config.md`, `book/src/13-terraform-variables.md`, `book/src/27-command-reference.md` (regenerated). The architect did not author any prose in `27-command-reference.md`; the file is fully regen-produced.

**Did not touch**: any Go file, any script, any workflow, `book.toml`. Did not commit. Did not run `gh issue create`.

**Judgement calls**:
1. **Chapter choice**: the prompt named "likely chapter 04, 05, or 06". Chapter 04 (Installation) and 05 (Doctor) don't cover `init`; Chapter 06 (Workspaces) is where `init` mechanics actually live and where the workspace's on-disk layout (including the `state/` and `state-cluster/` dirs the new flag writes to) is documented. Chapter 07 (Quick Start) is a "fresh laptop" walkthrough — adding the `--var-file` subsection there would have crowded the linear narrative for a flow that's optional on the first run. Chapter 06 is the right home.
2. **Cross-sweep scope**: no chapter literally said "supply `--var-file` on every command" verbatim, so I targeted the three chapters that talk about the `--var-file` flag's role and persistence story (10, 12, 13). I did **not** edit Chapter 11 (Tearing down) — its worked example is a `cluster register` path that doesn't require `--var-file` on the destroy, so it doesn't carry the drift the sweep is fixing. I did **not** edit Chapter 25 (COS supply chain) — it has no `--var-file` mentions to drift in the first place.
3. **No Chapter 26 (Troubleshooting) entry added**. The new Chapter 6 §Diagnostics paragraph is the canonical home for the `No value for required variable …` recovery; a Chapter 26 cross-link would have been gold-plating beyond the prompt's "add or fix a sentence where the existing prose is now incomplete" guidance.
