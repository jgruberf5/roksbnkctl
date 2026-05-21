# Sprint 20 — validator issues (release-publish stale-PDF gate)

> **Sprint 20 frame.** First regular work sprint post-`v1.6.4`.
> Validator owns the hermetic shell-level test surface that
> proves staff Issue 1's rebuild-then-upload contract holds
> against the planted-stale-PDF scenario the Sprint 19 v1.6.4
> integrator cut hit live.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Hermetic test for `release-publish` stale-artifact gate

**Severity**: medium
**Status**: resolved (pending integrator gate)

### Motivation

Staff Issue 1 hardens `make release-publish` so a stale
`book/book/pandoc/pdf/book.pdf` from a prior cycle cannot be
uploaded into the current cycle's GitHub Release. The proof
that the hardening holds is a hermetic test that walks the
target with a planted stale artifact + a stubbed `gh` binary,
and asserts (a) the planted bytes do not survive the rebuild
preamble, and (b) `gh release upload` is never invoked with
stale content. This is the artifact-shape analog of Sprint 16
round-2's `live-verify-high-issues` discipline: the high-risk
publish-step gate gets its own pin.

### Acceptance criteria

1. **Test file**: one new test file ships
   (e.g. `scripts/test-release-publish-staleness.sh`, mode +x,
   `set -euo pipefail`), or one new bats test under
   `tests/scripts/` if bats is already in the tree. Pick one
   shape per the prompt; do NOT ship both.
2. **Planted-bytes assertion**: a sentinel byte string written
   to `book/book/pandoc/pdf/book.pdf` before the test runs
   `make release-publish` (or the equivalent recipe shape)
   does NOT survive the rebuild step.
3. **gh-stub assertion**: a `gh` stub on `PATH` intercepts every
   `gh release upload …` invocation; the test asserts the stub
   was either NOT called (the rebuild failed first) OR was
   called with a freshly-built `book.pdf` (validated by
   sentinel-byte absence in the uploaded path).
4. **Hermetic**: the test never invokes the real `gh` binary,
   never `docker pull`s, never renders an actual PDF. If the
   test environment can't satisfy any of those (e.g. docker
   isn't installed on the test host), the test must SKIP with
   a clear message, NOT fail.
5. **`bash -n` clean** on the test file.
6. **Failure shape pinning**: run the test against the
   pre-staff-edit tree (e.g. `git stash` the Makefile edits)
   and confirm it FAILS with an assertion message that names
   what was wrong. Capture that output in the closure section.
7. **No edits to any pre-existing `_test.go` or `_test.sh`** —
   strict additive parity discipline carries forward.

### Out of scope

- A full live `release-publish` invocation against a real
  GitHub Release. Live gating remains the integrator's manual
  invocation at cut time, per the existing Makefile docstring.
- Testing the `goreleaser` half of the release pipeline. That
  side is fine.

### Files affected

- The one new test file (path TBD by the implementer's choice
  between bats / plain bash).

### Related

- Sprint 20 staff Issue 1 — the code-side; this issue's test
  proves it.
- Sprint 16 round-2 `live-verify-high-issues` discipline —
  this sprint's hermetic test is the artifact-shape equivalent
  (publish-step gates don't have a "live cloud account" surface
  to verify against, so the hermetic shape is sufficient).

---

## Closure — validator, 2026-05-21

### Files changed

| File | Type | LOC |
|---|---|---|
| `scripts/test-release-publish-staleness.sh` | new (mode 0755, `set -euo pipefail`, `bash -n` clean) | 394 |

No other files touched in this validator edit aside from this issue
ledger's status flip + this Closure block. Strict additive-only
discipline: no pre-existing `_test.go` or `_test.sh` was modified.

### Test shape

Plain `bash` driver, not bats — `find . -name '*.bats'` returned
empty and the existing project test surface under `scripts/` is all
plain bash (`scripts/e2e-*.sh` family). Mirrors the style + EXIT-trap
discipline of `scripts/e2e-init-var-file.sh` (the Sprint 19 reference
shape the prompt called out).

### What the test pins

The staff edit (see `issues/issue_sprint20_staff.md` §Closure —
staff, 2026-05-21) added two `rm -rf` + `$(MAKE) book…` pairs to
the `release-publish` recipe at `Makefile` lines 420–458, with the
HTML-side hardening at lines 429–435 and the PDF-side hardening at
lines 438–445. This test pins both pairs.

### Two scenarios

The hardening is symmetric (HTML at [1/2], PDF at [2/2]). Each side
gets its own scenario so a regression that removes one but not the
other still trips the test:

| Scenario | Forced failure | Sentinel-wiped target | Expected outcome |
|---|---|---|---|
| **S1** HTML-side gate | `docker` stub fails on **invocation #1** (the `$(MAKE) book BOOK_BACKEND=docker` rebuild) | `book/book/html/index.html` | release-publish aborts before `gh release upload`; HTML sentinel does not survive; exit non-zero. |
| **S2** PDF-side gate | `docker` stub succeeds on #1 (HTML rebuild simulates clean output), fails on **invocation #2** (the `$(MAKE) book-pdf BOOK_BACKEND=docker` rebuild) | `book/book/pandoc/pdf/book.pdf` | release-publish aborts before `gh release upload`; PDF sentinel does not survive; exit non-zero. |

### Assertion-by-assertion breakdown

Per scenario (so 8 assertions total — 4 per scenario):

- **A1 — rebuild-step-reached pin.** `grep -qF` the staff-edit echo
  line ("==> [1/2] Rebuilding + pushing HTML book to gh-pages" for
  S1; "==> [2/2] Rebuilding + uploading PDF book to GitHub Release"
  for S2) appears in the captured make output. Pinning the echo line
  catches regressions that quietly remove the rebuild preamble but
  leave the `gh release upload` step intact.
- **A2 — planted-bytes wiped.** The pre-run sentinel string written
  to the stale-artifact path must not be present at that path after
  `make release-publish` returns. This is the core "the `rm -rf` ran"
  assertion (AC #2).
- **A3 — gh release upload either NOT called OR called against a
  freshly-built path.** The `gh` stub appends every `gh release
  upload …` argv to a recorder file. If the file is empty → "never
  called" branch (which is what S1 + S2 hit, because the rebuild
  fails first and aborts the target). If non-empty → walk every
  path token in the argv and `grep -F` for the sentinel; absence =
  fresh artifact (AC #3).
- **A4 — make exited non-zero.** Pins the recursive-make exit-code
  propagation. A zero exit here would mean the recipe swallowed the
  rebuild's failure, which is the original v1.6.4 defect shape.

### Hermetic discipline (AC #4)

- **No real `gh`.** A PATH-prefix stub at `<sandbox>/stub-bin/gh`
  handles `release view` (returns 0 + a stub URL so the prereq
  check passes) and `release upload` (records argv to a recorder
  file, returns 0). Real `gh` is never invoked, never authed.
- **No real docker pull.** A PATH-prefix stub at
  `<sandbox>/stub-bin/docker` counts invocations and either fails
  with exit 99 or simulates success by creating the expected output
  paths (`book/book/html/index.html` + `book/book/pandoc/pdf/book.pdf`)
  inside the mounted-volume source — never actually pulls or runs
  the `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev` image.
- **No real PDF render.** The `check-pdf-mermaid-labels.sh` shim in
  the sandbox is a no-op so `book-pdf`'s post-build pdftotext check
  doesn't try to run against the stubbed-empty PDF.
- **No `git` network touches.** `book-publish` (which release-publish
  invokes between the HTML rebuild and the PDF rebuild) shells out
  to `git fetch origin gh-pages` + `git worktree add` + `git push`.
  Rather than stub `git` (which would mask future regressions in
  release-publish that add new git calls), the sandbox Makefile's
  `book-publish` target is replaced via `awk` with a one-line no-op
  before the test runs. The hardening under test lives in
  `release-publish` itself, not in `book-publish`, so this
  neutralization is sound.
- **Skip-not-fail on missing prereqs.** Preflight `command -v` checks
  for `make`, `mktemp`, `grep`, `rm`, `cp`, `mkdir`, `tail` — any
  missing tool yields a `SKIP:` line + exit 0, NOT a failure (AC #4
  hermetic-skip contract).
- **Self-cleanup on EXIT trap.** Successful runs delete the sandbox.
  Failed runs preserve it for post-mortem (path printed) so the
  next test invocation gets a fresh sandbox.

### PASS output transcript (against the post-staff tree)

Ran `bash scripts/test-release-publish-staleness.sh` on the current
working tree (staff Makefile edits landed, uncommitted). All 8
assertions pass; exit 0.

```
release-publish stale-artifact gate — hermetic test — run-id 20260521-141601
(validator Sprint 20 Issue 1 — pins staff Issue 1's rebuild-before-upload contract)
[14:16:01] log: /tmp/roksbnkctl-test-release-publish-staleness/run-20260521-141601.log
preflight
[14:16:01] preflight OK — REPO_ROOT=/mnt/c/project/roksbnkctl ... sentinel=STALE_ARTIFACT_SENTINEL_a34e7fb4b9d31a7357a68619e55fa64a
scenario S1 — HTML rebuild fails → HTML sentinel must be wiped, no upload
[14:16:02]   make exit code:   2
  PASS S1/A1: recipe reached the 'Rebuilding + pushing HTML book to gh-pages' rebuild step
  PASS S1/A2: planted sentinel wiped from .../s1-html-gate/book/book/html/index.html
  PASS S1/A3: gh release upload was never called (rebuild failure aborted release-publish before upload)
  PASS S1/A4: make release-publish exited 2 (non-zero) — rebuild failure correctly aborted the target
scenario S2 — HTML rebuild succeeds, PDF rebuild fails → PDF sentinel must be wiped, no upload
[14:16:02]   make exit code:   2
  PASS S2/A1: recipe reached the 'Rebuilding + uploading PDF book to GitHub Release' rebuild step
  PASS S2/A2: planted sentinel wiped from .../s2-pdf-gate/book/book/pandoc/pdf/book.pdf
  PASS S2/A3: gh release upload was never called (rebuild failure aborted release-publish before upload)
  PASS S2/A4: make release-publish exited 2 (non-zero) — rebuild failure correctly aborted the target

════════════════════════════════════════════════════════════
PASS — release-publish stale-artifact gate verified hermetically:
  S1 HTML-side: sentinel wiped + no upload + non-zero exit
  S2 PDF-side:  sentinel wiped + no upload + non-zero exit
run-id 20260521-141601
════════════════════════════════════════════════════════════
[14:16:02] teardown: removed sandbox /tmp/.../sandbox-20260521-141601
```

Exit code: 0.

### FAIL output transcript (against the pre-staff tree — `git stash` pin, AC #6)

Stashed the Makefile edits via `git stash push -m sprint20-validator-fail-pin -- Makefile`, leaving the issue-ledger edits in place, then re-ran the test. The pre-staff `release-publish` recipe (lines 400–429 of the pre-staff tree) uses `[ ! -f book/book/pandoc/pdf/book.pdf ]` as its only prereq check + invokes `@$(MAKE) book-publish` + `gh release upload …` directly, with no rm-rf and no rebuild. The test's first assertion (S1/A1) names the missing rebuild echo line — naming exactly what's wrong, not just "something failed":

```
release-publish stale-artifact gate — hermetic test — run-id 20260521-141950
preflight
scenario S1 — HTML rebuild fails → HTML sentinel must be wiped, no upload
[14:19:51]   make exit code:   0
  make log tail:
make: Entering directory '.../s1-html-gate'
==> [1/2] Pushing HTML book to gh-pages
(book-publish stub: hermetic test no-op)
==> [2/2] Uploading PDF book to GitHub Release v0.0.0-test
==> Published:
    HTML: https://jgruberf5.github.io/roksbnkctl/book/
    PDF:  https://example.invalid/stub
make: Leaving directory '.../s1-html-gate'
  FAIL: S1/A1: recipe never echoed 'Rebuilding + pushing HTML book to gh-pages' — the staff-edit rebuild step did not run
  full log: /tmp/roksbnkctl-test-release-publish-staleness/run-20260521-141950.log
  sandbox preserved at: /tmp/roksbnkctl-test-release-publish-staleness/sandbox-20260521-141950
Test run FAILED (exit 2) — see /tmp/roksbnkctl-test-release-publish-staleness/run-20260521-141950.log
```

Exit code: 2. Failure message: **`S1/A1: recipe never echoed
'Rebuilding + pushing HTML book to gh-pages' — the staff-edit rebuild
step did not run`** — directly names the missing hardening.

Note: in the captured pre-staff make log above, the recipe walked
all the way through to `==> Published:` (exit 0) because the
existence check `[ ! -f book.pdf ]` was satisfied by the planted
sentinel file — exactly the v1.6.4 defect shape the staff edit
closes. Had the test reached A2/A3, those would also have failed
(PDF sentinel would have survived; gh-upload-argv recorder would
contain `v0.0.0-test .../roksbnkctl-book-v0.0.0-test.pdf --clobber`).
First-failure-name-the-cause is the right discipline here — naming
"missing rebuild echo" is more diagnostic than "sentinel survived".

After capturing the FAIL output: `git stash pop` to restore the
staff Makefile edits; working-tree status now shows
`Makefile + issues/issue_sprint20_staff.md + this validator ledger
edit + the new test file`.

### Discipline checks

- [x] No `git commit` run by this agent — integrator commits.
- [x] No `gh` invocation by this agent (PATH stub only; real `gh`
      binary never touched).
- [x] No `docker pull` or `docker run` against any real image (PATH
      stub only).
- [x] No actual PDF render (no `mdbook` / `pandoc` / `XeLaTeX`
      invocation; the docker stub simulates output by `touch`-style
      file creation).
- [x] No edits to any pre-existing `_test.go` or `_test.sh` (strict
      additive-only parity discipline; one new file:
      `scripts/test-release-publish-staleness.sh`).
- [x] `bash -n scripts/test-release-publish-staleness.sh` exits 0
      (syntax-clean shell file, per AC #5).
- [x] Mode 0755 + shebang `#!/usr/bin/env bash` + `set -euo pipefail`
      at top.
- [x] EXIT-trap self-cleanup so a successful run leaves nothing in
      the repo tree or `/tmp`; failed runs preserve the sandbox for
      post-mortem (path printed to stderr).
- [x] Real working-tree `Makefile` never modified by the test (the
      test copies `Makefile` into a sandbox tmpdir and runs
      `make -C $sandbox` there — never `make` from the repo root).
- [x] Failure-shape pin (AC #6) captured against the pre-staff tree
      via `git stash push -- Makefile` cycle; FAIL output included
      above.

### Invocation

The test is operator-run, not CI:

```
./scripts/test-release-publish-staleness.sh
```

There is no project-level "run all hermetic shell tests" make target
to wire it into (the existing `scripts/e2e-*.sh` family is also
operator-run, per the Sprint 19 closure). The integrator runs this
test once at the next release-cut as the artifact-shape equivalent of
the `live-verify-high-issues` discipline (per the §Related row of
this issue's Acceptance criteria).

### Status flip

`**Status**: open` → `**Status**: resolved (pending integrator gate)`.
Integrator flips to plain `resolved` after running the test once
against the staff edit and confirming the PASS shape above
reproduces on their host.
