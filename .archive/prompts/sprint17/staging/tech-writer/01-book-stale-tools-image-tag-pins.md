---
name: Bug report
about: Something roksbnkctl does that it shouldn't, or doesn't do that it should
title: 'bug: book chapters carry stale `v0.9.0` / `v0.10.0` ghcr.io image tag examples'
labels: []
assignees: ''
---

## Symptom

Five book chapters ship illustrative pod / manifest examples that pin
the vendored tools images at `ghcr.io/jgruberf5/roksbnkctl-tools-*` tags
that pre-date the project's current `v1.6.2` release. A reader who
copy-pastes the example verbatim either (a) pulls a release that
doesn't match their installed binary's image-tag resolver (a
release-built `v1.6.x` binary resolves `:v1.6.x`, not `:v0.9.0`), or
(b) tries to pull a tag that may have been pruned from ghcr.io
entirely.

Concrete drift instances (paths relative to repo root):

| file:line | image reference |
|---|---|
| `book/src/17-execution-backends.md:257` | "release-built binary like `v0.10.0` pulls `:v0.10.0`" (illustrative prose, but the example tag is far behind current) |
| `book/src/19-in-cluster-ops-pod.md:314` | `image: ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v0.9.0` |
| `book/src/22-throughput-testing.md:111` | `image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:v0.9.0` |
| `book/src/22-throughput-testing.md:212` | `image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:v0.9.0` |
| `book/src/26-troubleshooting.md:149` | `image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:v0.9.0` |
| `book/src/28-configuration-reference.md:91` | `image: ghcr.io/jgruberf5/roksbnkctl-tools-iperf3:v0.9.0` |

The image-tag resolver itself (`internal/exec/docker.go::toolImageTag()`)
is correct — it reads `internal/version.Version` at runtime and pulls
the matching tag. The bug is in the book: examples that should track
the current minor (`v1.6.x` as of the v1.6.2 cut, or `<current>` /
`<your-roksbnkctl-version>` placeholder text) instead pin `v0.9.0` /
`v0.10.0` literals that are five+ minor cycles behind.

## Reproduction

```
# 1. grep the book tree for the stale pins:
grep -rn 'roksbnkctl-tools-[a-z]*:v0\.' book/src/
# Expected (post-fix): empty (or only patterns naming `<your-version>` /
#                      `<current>` placeholders).
# Actual (pre-fix):    5 hits in chapters 19, 22 (×2), 26, 28.

# 2. confirm chapter 17 §"the <tag> for the vendored per-tool images" prose
#    illustrates with `v0.10.0` as well:
grep -n 'v0\.10\.0' book/src/17-execution-backends.md
# Expected (post-fix): the illustrative example uses the current minor
#                      (e.g. `v1.6.0`) or a `<your-roksbnkctl-version>`
#                      placeholder.
# Actual (pre-fix):    one hit, naming `v0.10.0` as the example.

# 3. user impact reproduction — a reader following chapter 19 §"Manual
#    pod manifest if you'd rather not use `ops install`":
cat book/src/19-in-cluster-ops-pod.md | sed -n '310,316p'
# shows `image: ghcr.io/jgruberf5/roksbnkctl-tools-ibmcloud:v0.9.0`.
# Pulling that tag from ghcr.io may 404 (if the tag has been pruned)
# or pull a binary built against the v0.9.0 SDK surface, which the
# v1.6.x `ops install` path no longer expects.
```

## Expected behavior

The five (and the chapter-17 prose example) read either as
`<your-roksbnkctl-version>` placeholders (preferred — they stay
correct forever) or pin the current minor (`v1.6.0` or whatever's
current at the next minor cut). A reader pasting the example into
their own manifest pulls an image consistent with the binary they're
running.

## Actual behavior

The examples pin `v0.9.0` (4 hits) and `v0.10.0` (1 hit). A reader
running `roksbnkctl v1.6.2` who pastes the chapter 19 manifest pulls
a tag that's either gone or carries a pre-Sprint-9 codebase.

## Environment

- `roksbnkctl version`: N/A — this is a book-content drift bug.
- OS / arch: N/A (the affected text reads the same on every render).
- IBM Cloud region: N/A.
- Backend: N/A.

## Suspect pipeline / hypotheses (optional)

1. **Most likely:** the chapters were authored during the v0.9.x /
   v0.10.x window (Sprint 4-5 era, when `:v<version>` tag pinning
   first landed per `book/src/17-execution-backends.md:257`'s
   prose); the literal example tags weren't bumped on subsequent
   minor cuts because nothing forces a sweep of example pins per
   release. Five minor cycles + two patch cycles later, the literals
   are far enough behind to confuse a copy-pasting reader.
2. Less likely: the maintainer wanted the examples to read as a
   specific pre-release pin for "stable" documentation. The chapter
   17 prose explicitly names the tag-pinning *mechanism* (matches
   binary version at runtime); a literal example pin doesn't serve
   the explanation and doesn't track the project.

## Acceptance criteria

1. `grep -rn 'roksbnkctl-tools-[a-z]*:v0\.' book/src/` returns empty
   on the fixed tree.
2. Each of the five image-tag references is replaced with either
   `<your-roksbnkctl-version>` (a literal placeholder string the
   reader edits before applying — preferred for chapters where the
   example is meant as a paste-ready manifest) or the current minor
   pin at the time of the fix (e.g. `v1.6.0`). Integrator picks per
   chapter; consistency within a chapter is required.
3. `book/src/17-execution-backends.md:257`'s illustrative prose example
   (`v0.10.0`) updates in lockstep — the explanation reads
   coherently with the literal examples in chapters 19, 22, 26, 28.
4. `mdbook build book/` exits 0 with no new warnings (the fix is
   markdown-only — no chapter headings or anchors change).
5. Regression check: a CHANGELOG release-checklist item (or the
   `release-precheck` script the validator filed separately) gains
   a one-line `grep` ensuring the next release cut doesn't ship a
   stale `roksbnkctl-tools-*:v<old>` pin. Stretch goal — not blocking
   this issue's PR if the script is co-landing with the validator's
   pre-tag-gate issue.

## Out of scope (deliberately)

- The image-tag *resolver* (`internal/exec/docker.go::toolImageTag()`)
  — it's correct; the bug is in the book examples, not the runtime
  path.
- Auditing every other "version-pinned literal" in the book (e.g.
  the `hashicorp/terraform:1.5.7` pin in chapter 17:257 — that's
  an upstream image with deliberate stability semantics, not a
  drift). Different defect class; file separately if it bites.
- A book-rendering placeholder syntax (`{{ .Version }}` etc.) that
  expands at `mdbook build` time. mdBook doesn't ship one; adding
  one is its own project. Use literal placeholders + a release-cut
  sweep instead.
- Updating PRD or `docs/`-tree examples that name `v0.x` tags
  (PRDs are historical artefacts that intentionally pin their
  contemporary versions).

## Notes

- The chapter 17:257 prose is the single most-likely-read site (it
  explains the tag-pinning mechanism). The other four are manifest
  examples in §"Manual pod manifest" / §"Tuning knobs" sections —
  less prominent but more dangerous (a reader who pastes them gets
  a broken pull or a stale binary inside their cluster).
- The fix is markdown-only; ≤30-line PR. The follow-up release-cut
  sweep is the more durable shape, but landing the immediate fix
  first un-blocks the v1.7+ documentation from carrying the same
  drift forward.
