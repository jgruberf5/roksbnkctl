# Sprint 18 — tech-writer resolution log

## Issue 1 — post-integration drift / consistency review → **resolved; verdict flipped to GREEN; shipped in `v1.6.3`**

Tech-writer drift sweep over the integrated tree (HEAD `420eac9`, end of Sprint 18 round-6) returned **RED with 3 findings** (1 high, 1 medium, 1 low). All three addressed in the integrator's doc-integration commit `e357025`:

- **High** — `book/src/27-command-reference.md` was stale (auto-generated CLI reference file with no `#### roksbnkctl cos bucket get` subsection, despite the file's header explicitly requiring re-running `go run ./tools/refgen/cobra-md` on every CLI surface change). **Fix**: regenerated. The new `### roksbnkctl cos bucket get` subsection lives at line 267 with the positional `<bucket> <local-dir>` args and the `--no-clobber` flag.
- **Medium** — `book/src/25-cos-supply-chain.md` missing the new verb's pinning. **Fix**: three drift points addressed — line 29's three-command-levels block now reads `{create|delete|list|get}`; a snapshot-before-rotate `cos bucket get` example added between the `delete` example and the flags table (mirroring `cos object get`'s narrative shape); `--no-clobber` row appended to the flags table. The Worked-example section's optional snapshot-before-rotate paragraph was deferred (tech-writer flagged it as integrator judgement-call).
- **Low** — five SVG→PNG comment drifts (sibling files to the architect's Lua filter — `book/book.toml:35`, `Makefile:78`, `tools/docker/mdbook/Dockerfile:8/69/110`) + one stale `lua-filter` → `filters` TOML-key reference at `Dockerfile:112`. **Fix**: six mechanical one-line swaps. `librsvg2-bin` apt install kept in place per the tech-writer note (transitively needed for non-mermaid SVG content in the HTML pipeline).

**Verdict flipped GREEN** at the bottom of `issues/issue_sprint18_tech-writer.md`'s Closure section; v1.6.3 release-cut was unblocked on that basis.

## Notes

No Part-B cross-cutting issues filed (cap was ≤2; all findings fit cleanly under Issue 1).

## Status

Issue 1: **resolved**. No live-`!`-verify gate (read-only doc review).
