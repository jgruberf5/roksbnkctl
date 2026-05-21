# Sprint 21 — architect issues (argv strictness docs)

> **Sprint 21 frame.** First regular work sprint post-`v1.6.4`.
> Architect ships the one-paragraph addition to the
> first-run / command-line chapter, regens the auto-generated
> CLI reference if any `Args:` change surfaces, and sweeps the
> existing book for stuck-together-shorthand examples that the
> new strictness contract would break.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Doc the parser strictness contract; sweep stale stuck-together examples

**Severity**: medium (the contract is a small change but
operator-facing; a stale example in the book would confuse
any operator who tried to follow it)
**Status**: open

### Motivation

Staff Issue 1 adds parser strictness — short flags accept
`-f value` or `-f=value`; the stuck-together `-fvalue` form
is rejected. The book must (a) name the rule once in the
first-run chapter so operators know about it, (b) include the
verbatim error text the binary produces so the error feels
familiar when an operator hits it in the wild, and (c) survive
a cross-chapter sweep that catches any existing example that
demonstrates the now-rejected form.

### Tasks

1. **Find the first-run / command-line chapter** in
   `book/src/SUMMARY.md` (likely `book/src/03-quick-tour.md`;
   verify the actual structure).
2. **Add one short paragraph** that:
   - Names the strictness contract: short flags accept
     `-f value` (space) or `-f=value` (equals); the
     stuck-together form is rejected.
   - Cross-links the long-flag forms: `--workspace value` or
     `--workspace=value`.
   - Quotes the verbatim error the binary produces for the
     typo `-ws canada-roks`. Run the binary to capture this
     exactly; do NOT paraphrase from the staff issue spec.
   - Names the recovery succinctly.
3. **Regen `book/src/27-command-reference.md`** if staff's
   `Args: cobra.NoArgs` audit changes any per-command Usage
   text. Invocation:
   `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md`.
4. **Cross-chapter sweep** — grep the existing book for any
   short-flag-value usage in stuck-together form:
   `grep -rn '\-[a-zA-Z][a-zA-Z]' book/src/` (rough — refine
   per false-positive shape). Each hit must be rewritten to
   space-separated form. If no hits, document "none found" in
   the closure.

### Acceptance criteria

1. The first-run chapter carries the new paragraph; the error
   text inside it matches the binary's verbatim output (NOT
   paraphrased from the spec).
2. `book/src/27-command-reference.md` is regen'd against the
   post-staff cobra tree.
3. No stuck-together short-flag-value example survives in the
   book (or the sweep result is documented as "none found").
4. No `docs/PRD/` touched; no `CHANGELOG.md` touched
   (integrator-owned at cut).

### Out of scope

- `internal/`, `cmd/`. That's staff's surface.
- Restructuring the first-run chapter beyond the new
  paragraph. Drift-sweep / additive only.

### Files affected

- `book/src/03-quick-tour.md` (or the actual first-run chapter
  per `SUMMARY.md`) — one new paragraph.
- `book/src/27-command-reference.md` — auto-regen.
- Possibly other `book/src/*.md` per the cross-chapter sweep
  (if any stuck-together example is found).

### Related

- Sprint 21 staff Issue 1 — the code-side contract this docs
  surface introduces.
- `argv-strictness-prevents-resource-damage` memory — the
  user-priority context that motivates surfacing this in the
  book (so operators understand the rule, not just hit the
  error).
