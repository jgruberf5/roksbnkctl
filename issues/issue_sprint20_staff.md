# Sprint 20 — staff issues (`make release-publish` stale-PDF hardening)

> **Sprint 20 frame.** First regular work sprint post-`v1.6.4`.
> Release-tooling hardening only; no `internal/` or `book/`
> touches. Surface: `Makefile`'s `release-publish` target.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — `make release-publish` must not upload stale PDF artifacts

**Severity**: medium
**Status**: open

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
