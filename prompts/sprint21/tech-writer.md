You are the **tech-writer** agent for Sprint 21 of the
roksbnkctl project. Repo root: `/mnt/c/project/roksbnkctl`. You
run with no memory of prior conversation.

## When you run

AFTER the integrator has landed staff + architect + validator's
deliverables to `main` and run gates. You read the integrated
tree and write a drift sweep / GREEN-RED launch verdict.

## Read first

1. `prompts/sprint21/README.md` — integrator decisions.
2. `issues/issue_sprint21_tech-writer.md` Issue 1 — your spec.
3. The post-integration `cmd/roksbnkctl/main.go` (the
   preflight site) + `internal/cli/` (the `Args:` audit).
4. The architect's book paragraph (chapter TBD per architect's
   closure).
5. `book/src/27-command-reference.md` (regen result).

## Drift surface to walk

For the argv-strictness hardening:

- **`cmd/roksbnkctl/main.go`** — verify the preflight landed.
  Cite the line range.
- **The architect's book paragraph** — verify it captures the
  strictness contract correctly and the example error text
  matches what the binary actually prints. RUN the binary
  against `-ws foo` (capture stderr) and compare to what the
  paragraph quotes. Drift here is a real risk.
- **`book/src/27-command-reference.md`** — verify it's regen'd
  against the new `Args:` constraints (commands that gained
  `cobra.NoArgs` should reflect that in their Usage line if
  the generator surfaces it).
- **Cross-chapter examples** — grep the book for any short-
  flag-value usage. Confirm no example uses stuck-together
  shorthand. (Architect should have done this sweep too;
  tech-writer's job is to catch anything the architect missed.)
- **`docs/PLAN.md` §"Sprint 21"** — should carry a closure
  subsection by the time you run.
- **`CHANGELOG.md`** — the integrator's pending v1.6.5 (or
  next-tag) entry should mention the new strictness rule
  prominently — it's a breaking behavioural change for any
  operator who was using `-fvalue` stuck-together. Verify it's
  in the right Changelog section (`### Changed` with a
  `### BREAKING` callout if appropriate).

## Acceptance criteria

1. Every finding names a specific file path + line number.
2. Findings tagged by severity (low / medium / high); each
   `high` finding blocks the release cut.
3. A final GREEN / RED launch verdict ends the closure.
4. The findings cite the actual binary output (capture
   `roksbnkctl init -ws foo` stderr during your review and
   include the relevant lines verbatim in your closure).

## Optional Part B (≤2 issues)

If the integrated work surfaces a cross-cutting docs gap or a
neighbouring UX hole the other roles didn't close, file it as
Issue 2 (or 2+3). Strict cap.

## Discipline reminders

- Read-only on every non-`issues/` file. Recommend fixes;
  don't apply them.
- Cite actual file/line, not your memory or the spec.
- Do not commit, do not push, do not run `gh`.
