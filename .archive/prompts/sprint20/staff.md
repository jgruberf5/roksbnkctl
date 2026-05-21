You are the **staff engineer** agent for Sprint 20 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in order)

1. `prompts/sprint20/README.md` — integrator decisions; especially
   §"Integrator decisions baked in" and §"Per-role scope".
2. `issues/issue_sprint20_staff.md` Issue 1 — the **authoritative
   spec** for what to build. Every acceptance criterion in that
   file is binding.
3. `Makefile` — read the `release-publish`, `book-publish`,
   `book`, and `book-pdf` targets in full so you understand the
   current call graph. Pay attention to the existing docstring
   above `release-publish` (lines starting `# release-publish:`).
4. `tools/docker/Makefile` — the `BOOK_IMAGE` definition that
   `make book-pdf BOOK_BACKEND=docker` uses, for context. Do NOT
   touch this file.

## Tasks

1. Edit the `release-publish` target so the PDF-build step:
   - Removes the previous `book/book/pandoc/` tree before
     invoking the build (so a stale `book.pdf` from a prior
     cycle cannot survive to the upload step).
   - Invokes `$(MAKE) book-pdf BOOK_BACKEND=docker` and fails
     `release-publish` if the rebuild exits non-zero (today the
     target's prereq check is `[ ! -f book/book/pandoc/pdf/book.pdf ]`
     which lets a stale file pass).
   - The HTML side already has a similar shape via
     `make book-publish` — verify that target rebuilds when
     needed, OR add a clean-then-rebuild preamble there too if
     it has the same gotcha. Use your read of the current target
     to decide.
2. Update the docstring above `release-publish` so the next
   maintainer reading it sees:
   - Why the `rm -rf book/book/pandoc/` is there (one-line
     reference to the Sprint 19 v1.6.4 stale-upload event).
   - That `release-publish` is the publish-contract gate, NOT
     the build-contract gate (the build's correctness is
     `book-pdf`'s problem; `release-publish` is responsible for
     "this matches the tagged tree").
3. Hermetic verification: run the new `release-publish` target
   in a way that proves the stale-file scenario fails loudly.
   `gh release upload` is impractical to dry-run; the spec is
   that the rebuild must be invoked and its exit code must
   propagate. A `make -n release-publish VERSION=v1.6.4` walk-
   through is enough verification at staff scope — the validator
   ships the executable hermetic test.

## Out of scope

- `internal/`, `book/src/`, `docs/PRD/`. This is a tools/build-
  system sprint.
- Re-architecting how `book-pdf` interacts with the docker
  backend. The fix is downstream of `book-pdf`'s contract.
- Any change that would require the docker image to be rebuilt.
  The Dockerfile is fine as of `a46fb53`.

## Acceptance criteria

1. `git diff Makefile` is the only file in the staff scope (plus
   the issue ledger's §Closure section).
2. `make -n release-publish VERSION=v1.6.4` shows the new
   `rm -rf book/book/pandoc/` and the gated rebuild in the
   rendered recipe.
3. Running `release-publish` against a planted stale
   `book/book/pandoc/pdf/book.pdf` fails the rebuild step (when
   the underlying `book-pdf` would fail) without uploading the
   stale file. Validator's hermetic test pins this.
4. Running `release-publish` against a clean tree behaves byte-
   identically to today (rebuilds, then publishes).
5. Docstring update names the Sprint 19 v1.6.4 event so future
   maintainers have context.

## Closure

Write your closure to `issues/issue_sprint20_staff.md` §"Closure
— staff, <date>". Include: files changed, the relevant `make -n`
output excerpt, and the discipline-checks bullet list (no
commit, no gh, no out-of-scope edits).
