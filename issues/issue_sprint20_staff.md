# Sprint 20 — staff issues (`make release-publish` stale-PDF hardening)

> **Sprint 20 frame.** First regular work sprint post-`v1.6.4`.
> Release-tooling hardening only; no `internal/` or `book/`
> touches. Surface: `Makefile`'s `release-publish` target.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — `make release-publish` must not upload stale PDF artifacts

**Severity**: medium
**Status**: resolved (pending integrator gate)

### Motivation

The Sprint 19 v1.6.4 cut (2026-05-21) caught a real defect in the
`release-publish` target: it uploaded a `book.pdf` left over from
the prior cycle (v1.6.3 vintage) under the v1.6.4 asset name,
because the current cycle's `book-pdf` build had failed silently
(missing `ulem.sty` in the docker image — fixed in commit `a46fb53`,
unrelated to this issue) and the publish target's prereq check
was `[ ! -f book/book/pandoc/pdf/book.pdf ]` — only "exists?", not
"current?". The integrator caught it manually via `grep` against
the gh-pages-published HTML; the PDF was re-built + re-uploaded
~30 minutes after the bad upload. The next cycle's integrator must
not have to catch this manually.

### Acceptance criteria

1. **Rebuild contract**: `make release-publish VERSION=…` removes
   `book/book/pandoc/` (so a prior cycle's `book.pdf` cannot
   survive into the new cycle's upload), then invokes
   `$(MAKE) book-pdf BOOK_BACKEND=docker` and fails
   `release-publish` if that rebuild exits non-zero.
2. **Symmetric HTML check**: confirm `make book-publish` either
   (a) already rebuilds, or (b) has the same staleness risk —
   if (b), apply the same `rm -rf book/book/html/` +
   re-build-then-publish shape. The v1.6.4 cycle's HTML
   publish reported "nothing to push" because the local
   `book/book/html/` carried v1.6.3 content; the integrator
   had to manually re-run `make book BOOK_BACKEND=docker` and
   re-`make book-publish`. The HTML side likely has the same
   shape; verify and harden.
3. **Docstring**: the existing `# release-publish:` block above
   the target gains one line naming the Sprint 19 v1.6.4
   stale-upload event so future maintainers reading the source
   understand why the `rm -rf` is there.
4. **No behaviour change on clean trees**: a fresh checkout +
   `make release` + `make release-publish VERSION=v1.6.x`
   sequence behaves byte-identically to today (build runs;
   upload runs). The hardening only changes behaviour when
   `book/book/pandoc/` is present from a prior cycle.
5. **No edits to** `book-pdf` / `book` / `book-publish` recipe
   bodies beyond the symmetric HTML hardening in AC #2. The
   build targets' contracts stay as-is.

### Out of scope

- Re-architecting the docker-backend interaction. The image is
  fine as of `a46fb53`; this issue is about the publish target,
  not the build chain.
- Adding a CI workflow to run `release-publish` automatically.
  The Makefile's existing docstring says `release-publish` is
  integrator-on-host post-tag-cut; Sprint 20 preserves that
  posture.
- Hardening `goreleaser` itself. The binary release is fine.

### Files affected

- `Makefile` — `release-publish` target body + docstring; possibly
  `book-publish` if AC #2 turns up the same gotcha.

### Related

- Sprint 19 v1.6.4 cut, integrator commit `a46fb53` (the
  Dockerfile `texlive-plain-generic` fix that surfaced the
  underlying stale-PDF defect).
- The `live-verify-high-issues` discipline — this issue's
  hermetic test (validator Issue 1) is sufficient closure
  because the publish target's risk is artifact-shaped, not
  cloud-integration-shaped.

---

## Closure — staff, 2026-05-21

### Files changed

| File | Diff stat | Range |
|---|---|---|
| `Makefile` | +39 / -10 (net +29) | `release-publish` docstring block (lines 384-?) + recipe body |

No other files touched in the staff edit. The closure block below
this line is the only edit to `issues/issue_sprint20_staff.md`.

### Behavioural delta — before/after

**Before** (v1.6.4 cycle's shape):

```make
release-publish:
    ...
    @if [ ! -f book/book/pandoc/pdf/book.pdf ]; then \
        echo "... missing ..." >&2; exit 2; \
    fi
    ...
    @echo "==> [1/2] Pushing HTML book to gh-pages"
    @$(MAKE) book-publish
    ...
    @echo "==> [2/2] Uploading PDF book to GitHub Release $(VERSION)"
    @tmp=... && cp book/book/pandoc/pdf/book.pdf ... && gh release upload ...
```

The prereq check is "does the file exist?". A `book.pdf` from any prior
cycle satisfies it. The HTML side defers to `book-publish`, which has
no rebuild and reports "nothing to push" on a stale-but-present tree.

**After** (Sprint 20):

```make
release-publish:
    ...
    @if ! gh release view $(VERSION) >/dev/null 2>&1; then ... fi
    @echo "==> [1/2] Rebuilding + pushing HTML book to gh-pages"
    rm -rf book/book/html/
    $(MAKE) book BOOK_BACKEND=docker
    @$(MAKE) book-publish
    ...
    @echo "==> [2/2] Rebuilding + uploading PDF book to GitHub Release $(VERSION)"
    rm -rf book/book/pandoc/
    $(MAKE) book-pdf BOOK_BACKEND=docker
    @tmp=... && cp book/book/pandoc/pdf/book.pdf ... && gh release upload ...
```

The `[ -f book.pdf ]` existence check is gone — it could never
distinguish "stale" from "current". Each upload step is now preceded by
`rm -rf` of its own output tree + a `$(MAKE) book…` invocation whose
exit code propagates (recursive-make default; a non-zero from the
rebuild aborts `release-publish` before any `gh release upload` runs).
The `rm -rf` and `$(MAKE)` lines are deliberately un-`@`-prefixed so
the destructive cleanup is visible in the live recipe echo.

### `make -n release-publish VERSION=v1.6.4` — relevant excerpt

```
echo "==> [1/2] Rebuilding + pushing HTML book to gh-pages"
# Sprint 20 hardening: clean book/book/html/ first so a stale tree
# from a prior cycle (the v1.6.4 "nothing to push" event) cannot
# survive into this cycle's gh-pages push. `book` rebuilds via the
# docker backend; failure here propagates and aborts the publish.
rm -rf book/book/html/
make book BOOK_BACKEND=docker
...
make book-publish
...
echo "==> [2/2] Rebuilding + uploading PDF book to GitHub Release v1.6.4"
# Sprint 20 hardening: clean book/book/pandoc/ first so a stale
# book.pdf from a prior cycle (the v1.6.4 stale-PDF-upload event)
# cannot survive into this cycle's GitHub Release asset. `book-pdf`
# rebuilds via the docker backend; failure here propagates and
# aborts the publish before any `gh release upload` runs.
rm -rf book/book/pandoc/
make book-pdf BOOK_BACKEND=docker
...
tmp=$(mktemp -d -t roksbnkctl-pdf.XXXXXX) && \
    trap "rm -rf $tmp" EXIT && \
    cp book/book/pandoc/pdf/book.pdf "$tmp/roksbnkctl-book-v1.6.4.pdf" && \
    gh release upload v1.6.4 "$tmp/roksbnkctl-book-v1.6.4.pdf" --clobber
```

The two `rm -rf` + `$(MAKE) book…` pairs are the AC #1 + AC #2
hardening. Both rebuilds execute before any `gh release upload`, and a
non-zero exit from either aborts the target.

### Per-target audit table

| Target | Changed? | Rationale |
|---|---|---|
| `release-publish` | **Yes** — docstring expanded (publish-contract framing + Sprint 19 v1.6.4 event narrative) and recipe body gained `rm -rf book/book/html/` + `$(MAKE) book BOOK_BACKEND=docker` before the HTML push, and `rm -rf book/book/pandoc/` + `$(MAKE) book-pdf BOOK_BACKEND=docker` before the PDF upload. Removed the now-redundant `[ -f book.pdf ]` existence prereq (the rebuild step replaces it; a rebuild that fails to produce the file will exit non-zero anyway). | This is the publish-contract gate per AC #1, AC #2, AC #3. |
| `book-publish` | **No** body change. | `book-publish` is also a standalone contributor entrypoint (a contributor iterating on the book may want to push the current `book/book/html/` without an unconditional docker rebuild). The symmetric HTML hardening (AC #2) lives in `release-publish`'s call site instead: it `rm -rf book/book/html/` + invokes `$(MAKE) book BOOK_BACKEND=docker` before delegating to `book-publish`. That preserves `book-publish`'s usability standalone while giving `release-publish` its symmetric "matches tagged tree" guarantee. README §"Per-role scope" Staff row scopes the edit to `release-publish`; the issue spec AC #5 explicitly forbids edits to `book-publish`'s recipe body beyond the symmetric hardening — putting the hardening in `release-publish` honours both. |
| `book` / `book-pdf` | **No.** | Their contracts (build + exit non-zero on failure) are already correct; the v1.6.4 cycle's `book-pdf` correctly returned exit 101. AC #5 forbids touching them. |
| All other Makefile targets | **No.** | Out of staff scope per the issue spec and README §"Per-role scope". |

### Clean-tree byte-identity argument (AC #4)

On a clean tree after `make release`, both `book/book/html/` and
`book/book/pandoc/pdf/book.pdf` exist. The new `release-publish` will
`rm -rf` them and rebuild — but rebuilding the same source tree
produces the same artifacts (the docker image is pinned via
`$(BOOK_IMAGE)`, the source is the committed working tree), so the
bytes pushed to gh-pages and uploaded as the GitHub Release asset are
identical to today's. The only observable difference on a clean tree
is "the publish step takes a few minutes longer because docker is
re-invoked" — which is the intended cost of the clean-then-rebuild
contract per integrator decision #3.

### Discipline checks

- [x] No `git commit` run by this agent — integrator commits.
- [x] No `gh` invocations run by this agent.
- [x] No edits to `internal/`, `book/src/`, `docs/PRD/`, the
      Dockerfile, or `tools/docker/Makefile`.
- [x] No pre-existing `_test.go` touched. (No `_test.go` touched at
      all; this is a Makefile-only edit.)
- [x] No new files created at staff scope. (Validator ships the
      hermetic test file per README §"Per-role scope" Validator row.)
- [x] `make -n release-publish VERSION=v1.6.4` renders the expected
      shape — `rm -rf` + `$(MAKE) book…` pair visible before each
      upload step (excerpt above).
- [x] `make -n book-publish` standalone is byte-identical to its
      pre-edit rendering — confirmed by direct comparison.
- [x] Docstring above `release-publish` now names the Sprint 19
      v1.6.4 stale-upload event and frames the target as the
      publish-contract gate (not the build-contract gate), per
      AC #3 and the README §"Per-role scope" Staff-row deliverable.

### Status flip

`**Status**: open` → `**Status**: resolved (pending integrator gate)`.
Integrator flips to plain `resolved` after the validator's hermetic
test lands and the gates run green against this edit.
