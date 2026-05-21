# Sprint 20

**Theme:** Harden `make release-publish` against silent stale-PDF uploads. First regular work sprint post-`v1.6.4`.

_Drafted from the integrator's observation during the Sprint 19 cut (v1.6.4, 2026-05-21): the `make release-publish` target uploaded a stale `book/book/pandoc/pdf/book.pdf` left over from the prior cycle (v1.6.3 vintage) because the `book-pdf` build had failed silently for the current cycle, and `release-publish` looked at "does the file exist?" not "is the file current?". The stale PDF was uploaded to the v1.6.4 GitHub Release under the `roksbnkctl-book-v1.6.4.pdf` name and sat there for ~30 minutes before the integrator caught it and re-cut the PDF after fixing the underlying `texlive-plain-generic` Dockerfile gap (commit `a46fb53`). The integrator caught it; a less-attentive cycle would have shipped a release whose PDF asset's content lagged the binary release by a full version. This is a release-pipeline hardening issue — small surface, real risk, must close before the next release cycle._

## Integrator decisions baked in (do not relitigate)

1. **Fix `release-publish`, not `book-pdf`.** The `book-pdf` target already exits non-zero on failure (the v1.6.4 cycle's `book-pdf` invocation correctly returned exit 101 with the pandoc error). The bug is downstream: `release-publish` was happy to use a `book.pdf` left over from a previous cycle. Hardening belongs in `release-publish`.
2. **Treat the stale-output check as part of the publish contract, not the build contract.** A developer iterating locally on the book may have a stale `book.pdf` lying around and want it that way; the `release-publish` target is the gate that says "this PDF is going public — it had better match this commit". The check must run there.
3. **Prefer a clean-then-rebuild approach over freshness heuristics.** `find -newer` / mtime comparisons against book sources are fragile (depend on filesystem mtime resolution, clock skew on CI, etc.). The cleaner contract is: `release-publish` removes `book/book/pandoc/` (or shells out to `make clean-book-pdf`) at the start, then re-runs `book-pdf BOOK_BACKEND=docker` against the current tree. If the build fails, `release-publish` fails before any upload attempt.
4. **Keep CI off the publish path.** `release-publish` runs on the integrator's host post-tag-cut (per the existing Makefile docstring) — this sprint does not change that posture.

## Per-role scope

| Role | Scope |
|---|---|
| **Staff** Issue 1 | Edit `Makefile`'s `release-publish` target so it (a) `rm -rf book/book/pandoc/` before invoking `$(MAKE) book-pdf BOOK_BACKEND=docker`, then (b) gates the upload step on the rebuild's exit code. Behavior on a clean tree is byte-identical to today; behavior on a tree with a stale `book.pdf` is now "rebuild, fail loudly if the rebuild fails, never upload stale content". Plus a docstring update naming the gotcha so the next maintainer who reads the target understands why the `rm -rf` is there. |
| **Architect** Issue 1 | None — pure release-tooling hardening. No book chapter touches this surface; no PRD applies; no CHANGELOG bullet (this isn't user-facing). The closure note for this issue is one line: "no architect deliverables for Sprint 20". |
| **Validator** Issue 1 | A small `tests/scripts/release-publish-staleness.bats` (or `.sh` if bats isn't already in the tree) that walks the Makefile target in `DRY_RUN`-ish shape: plants a stale `book/book/pandoc/pdf/book.pdf`, runs the new `release-publish` preamble, asserts the planted bytes don't survive — without ever calling `gh release upload`. This is a hermetic make-target shape test, NOT a full release dry-run. |
| **Tech-writer** Issue 1 (light, runs after) | Drift sweep — confirm the Makefile docstring matches the as-landed shape; confirm the new behaviour is mentioned in `docs/PLAN.md`'s §"Sprint 20" closure; confirm no other Makefile target (e.g. `book-publish` standalone) carries the same staleness risk. GREEN/RED launch verdict. |

## Constraints (binding on every role)

- `internal/`, `book/`, `docs/PRD/` are **out of scope** — this is a tools/build-system sprint. Touches `Makefile` + one test file. Nothing else.
- No edits to any pre-existing `_test.go` (parity discipline carries forward; new test files only).
- Do not commit; integrator commits. Do not run `gh issue create`.

## Reference — the gotcha in concrete terms

The v1.6.4 cycle's sequence was:

1. Integrator ran `make release-publish VERSION=v1.6.4`.
2. The target's first step (`make book-publish` → `make book BOOK_BACKEND=docker` doesn't actually rebuild — see Makefile) found `book/book/html/` from v1.6.3, said "nothing to push", returned 0.
3. The target's second step (PDF upload) found `book/book/pandoc/pdf/book.pdf` from v1.6.3, renamed it to `roksbnkctl-book-v1.6.4.pdf`, uploaded it via `gh release upload`.
4. Integrator's release was "published" with stale assets.

The integrator caught it via `grep -c 'workspace root.*sibling to' book/book/html/06-workspaces.html` (returned 1 locally; `curl -s gh-pages/06-workspaces.html | grep -c …` returned 0) and recovered manually. Sprint 20 makes that manual recovery unnecessary.
