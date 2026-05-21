# Sprint 21 — tech-writer issues (argv strictness drift sweep)

> **Sprint 21 frame.** Tech-writer runs **after** the
> integrator has landed staff + architect + validator's
> deliverables to `main` and run gates. Drift sweep + GREEN/RED
> launch verdict.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Post-integration drift sweep for argv strictness

**Severity**: medium (argv strictness is a behavioural change
visible to every operator; drift here surfaces immediately in
the next person's shell)
**Status**: open

### Motivation

Sprint 21 introduces a parser strictness contract that's a
small but real behavioural change. The drift surface includes:
the architect's book paragraph (must match verbatim binary
output), the regen'd chapter 27 (must reflect any `Args:`
change), cross-chapter examples (no stuck-together shorthand
survives), the CHANGELOG entry the integrator will write at
cut (must call out the BREAKING aspect for any operator using
the pre-strictness form), and `docs/PLAN.md`'s closure
subsection.

### Drift surface to walk

1. **`cmd/roksbnkctl/main.go`** — verify the preflight landed.
   Cite line range. Confirm it walks the cobra tree (no
   hand-maintained typo list).
2. **The architect's book paragraph** — find via grep for the
   new strictness language. Capture the binary's actual error
   output by running it against `-ws foo` (with
   `ROKSBNKCTL_HOME=/tmp/probe-tw` or similar). Compare to
   what the paragraph quotes. ANY drift here is a high finding.
3. **`book/src/27-command-reference.md`** — verify it's
   regen'd against the new `Args:` constraints. Sample a
   handful of commands staff added `cobra.NoArgs` to — their
   per-command sections should reflect the updated Usage.
4. **Cross-chapter sweep** — grep the book for stuck-together
   short-flag-value examples. If any survive, file as findings
   (severity: medium per example — operator confusion risk).
5. **`docs/PLAN.md` §"Sprint 21"** — should carry a closure
   subsection. Verify the live-`!`-not-applicable rationale
   appears (this sprint's gate is hermetic, not live).
6. **`CHANGELOG.md`** — the integrator's pending entry for
   the next tag should call out the strictness change
   prominently. Is it in `### Changed`? Does it warn operators
   who were using `-fvalue`? IF the CHANGELOG isn't written
   yet, recommend the wording the integrator should use.

### Acceptance criteria

1. Every finding names a specific file path + line number.
2. Findings tagged by severity (low / medium / high); each
   `high` finding blocks the release cut.
3. A final GREEN / RED launch verdict ends the closure.
4. The findings cite the actual binary output (capture
   `roksbnkctl init -ws foo` stderr during your review and
   include the relevant lines verbatim).

### Out of scope

- Restyling chapters; rewriting flow descriptions. Drift
  sweep only — recommend fixes, don't apply them.
- Touching any non-`issues/` file. Read-only on existing
  repo content.

### Optional Part B (≤2 issues)

If the integrated work surfaces a cross-cutting docs gap or
neighbouring UX hole the other roles didn't close, file it as
Issue 2 (or 2+3). Strict cap.

### Files affected

- `issues/issue_sprint21_tech-writer.md` (this file's
  Closure section). Read-only on the integrated tree.

### Related

- Sprint 21 staff/architect/validator Issue 1 — all reviewed
  for drift.
- `argv-strictness-prevents-resource-damage` memory — context
  for why the BREAKING callout in CHANGELOG matters.
