You are the **architect** agent for Sprint 21 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first

1. `prompts/sprint21/README.md` — integrator decisions; the
   parser strictness contract.
2. `issues/issue_sprint21_architect.md` Issue 1.
3. `book/src/SUMMARY.md` — verify the chapter the new paragraph
   lands in. Likely candidate: `book/src/03-quick-tour.md`
   (the "first run" / command-line tour), but verify the actual
   structure.
4. `book/src/27-command-reference.md` — auto-generated reference;
   you regen it ONCE at the end after staff lands, via
   `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md`.

## Tasks

1. **Short paragraph in the first-run / command-line-basics
   chapter** (verify path):
   - Names the parser strictness contract: short flags accept
     `-f value` (space) OR `-f=value` (equals); the stuck-
     together form is rejected.
   - Cross-links to `--workspace` long form: long flags accept
     `--workspace value` OR `--workspace=value`.
   - Includes one concrete example: the operator typo
     `-ws canada-roks` errors out with the actionable message
     naming the canonical form. Quote the actual error text the
     binary produces (run the binary against the typo to capture
     it — your paragraph should match exactly).
   - Tone matches the rest of the book: practical, terse,
     names the recovery.
2. **Regen `book/src/27-command-reference.md`** if any staff
   `Args:` change affects the per-command flag tables. Run
   `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md`
   from the repo root.
3. **Cross-chapter sweep**: grep the existing book for any
   example that uses stuck-together short-flag-values
   (`-wcanada`, `-vfpath`, etc.) — these now error. If any
   examples exist, rewrite them to the canonical
   space-separated form. If you find none, say so in your
   closure (no edits is a valid outcome).

## Out of scope

- `internal/`, `cmd/` — that's staff's surface.
- `docs/PRD/` — no PRD covers argv parsing.
- The CHANGELOG — integrator-owned at cut time.

## Acceptance criteria

1. The first-run chapter carries the new paragraph; it
   includes one concrete typo example with the verbatim
   error text the binary produces.
2. `book/src/27-command-reference.md` is regen'd against the
   current cobra tree.
3. No existing book example demonstrates stuck-together
   short-flag-value usage (or the sweep result is documented
   as "none found").

## Closure

Write your closure to
`issues/issue_sprint21_architect.md` §"Closure — architect,
<date>". Include the affected book chapter + line numbers, the
exact paragraph you added, the regen invocation + summary, and
the cross-chapter sweep result. Flip status `open` →
`resolved`.
