# Sprint 7 — staff engineer issues

Sprint 7 cuts the v1.0 release tag. Staff scope landed all five Sprint 7 code/config deliverables: README rewrite for v1.0, the `--version` book URL (both code paths), `.goreleaser.yml` finalisation, `CHANGELOG.md` v1.0.0 rollup, and the version-output regression test. Five issues filed: 3 deferred to v1.x with explicit roadmap callouts (signing, PDF book artifact, Homebrew tap), 1 informational handoff to the integrator (placeholder date in CHANGELOG.md needs filling at tag-cut time), 1 informational (`goreleaser check` deferred to integrator — binary not on sandbox PATH).

## Issue 1 (INFORMATIONAL — CHANGELOG.md v1.0.0 date placeholder) — handed off to integrator

**Severity**: informational
**Status**: handed off — single line edit at tag-cut time

The `## v1.0.0 — 2026-MM-DD (M4 milestone)` header at the top of `CHANGELOG.md` carries a `2026-MM-DD` placeholder. The integrator fills the actual date when cutting the v1.0 tag (e.g. `## v1.0.0 — 2026-05-24 (M4 milestone)`). Single line, no other CHANGELOG edits expected.

No code consequence; goreleaser's release-page header surfaces `{{ .Tag }}` (e.g. `v1.0.0`) directly from the git tag, not from the CHANGELOG.

## Issue 2 (INFORMATIONAL — `goreleaser check` deferred to integrator) — accepted

**Severity**: informational
**Status**: accepted (sandbox doesn't have goreleaser on PATH)

`goreleaser` is not on the sprint VM's PATH. The integrator should run before the tag cut:

```
goreleaser check                    # lints the config
goreleaser release --snapshot --clean   # multi-platform binary dry-run, lands at dist/
```

Expected binary names (matching `internal/cli/self.go::assetName`):

- `dist/roksbnkctl_<version>_linux_amd64.tar.gz`
- `dist/roksbnkctl_<version>_linux_arm64.tar.gz`
- `dist/roksbnkctl_<version>_darwin_amd64.tar.gz`
- `dist/roksbnkctl_<version>_darwin_arm64.tar.gz`
- `dist/roksbnkctl_<version>_windows_amd64.zip`
- `dist/roksbnkctl_<version>_windows_arm64.zip`
- `dist/checksums.txt`

YAML syntax was manually verified (`python3 -c "import yaml; yaml.safe_load(open('.goreleaser.yml'))"` → valid).

## Issue 3 (INFORMATIONAL — Cosign / sigstore release signing deferred to v1.x) — accepted

**Severity**: informational
**Status**: ✅ accepted (v1.x roadmap per PLAN.md §"v1.0 (M4)" gate criteria — gate doesn't require signing)

The `.goreleaser.yml` does not wire a `signs:` block. Per the Sprint 7 staff prompt ("Defer if signing infrastructure isn't in place; flag as v1.x roadmap (and don't ship a half-working signs: block)"), this is intentional:

- `cosign` is not on the sprint VM's PATH.
- `.github/workflows/release.yml` does not yet have `COSIGN_EXPERIMENTAL=1` / `COSIGN_PASSWORD` secrets wired.
- The standard `checksums.txt` SHA256 surface in `.goreleaser.yml::checksum:` already provides the integrity check that `roksbnkctl self update` relies on.

A v1.x effort lands:

1. A `cosign generate-key-pair` operation by the integrator; private key stored as `COSIGN_KEY` GitHub Actions secret.
2. A `signs:` block in `.goreleaser.yml` signing the archives + `checksums.txt`.
3. Documentation in the book (Chapter 4 — Installation) explaining how end users verify the signature.

CHANGELOG.md's "Deferred (v1.x roadmap)" section calls this out explicitly.

## Issue 4 (INFORMATIONAL — PDF book artifact deferred to v1.x) — accepted

**Severity**: informational
**Status**: ✅ accepted (PLAN.md §"Sprint 7 Risks" explicitly classifies PDF as "nice-to-have")

The `.goreleaser.yml` does not wire an `mdbook-pdf` `before:` hook or an `extra_files:` entry for the PDF book. Per the prompt's deferral guidance:

- `mdbook` is not on the sprint VM's PATH.
- `mdbook-pdf` has a flaky WSL2/Linux story (per PLAN.md §"Sprint 7 Risks" — "PDF is a nice-to-have, HTML book is the canonical surface").
- The HTML book at `https://jgruberf5.github.io/roksbnkctl/book/` is the canonical user-documentation surface; the v1.0 GitHub Release page header links to it.

A v1.x effort can revisit if user feedback requests an offline PDF copy. The book's mdBook source tree is in-repo; a contributor can render locally any time.

## Issue 5 (INFORMATIONAL — Homebrew formula stub deferred to v1.x) — accepted

**Severity**: informational
**Status**: ✅ accepted (no `homebrew-tap` repo exists yet under the org)

The `.goreleaser.yml` includes a commented-out `brews:` block as a marker for when a Homebrew tap lands. Per the prompt's deferral guidance ("If the integrator has a tap repo ready, wire the `brews:` block; otherwise leave for v1.x. Don't ship a half-wired brew block that points at a nonexistent tap"):

- No `github.com/jgruberf5/homebrew-tap` repo exists.
- Wiring the block would cause `goreleaser release` to fail when it tries to push the formula.

When a tap repo lands, uncomment the block in `.goreleaser.yml` (the structure is already drafted) and add the corresponding "Install via Homebrew" row to the README's install table (currently noted as "A Homebrew tap is on the v1.x roadmap").

## Verification status

- `go build ./...` ✓ clean
- `go vet ./...` ✓ clean
- `gofmt -d -l .` ✓ clean (full tree)
- `go test ./...` ✓ all 14 packages green, including the new `TestVersionCmd_OutputShape` and `TestDocsURL_Value` in `internal/cli/meta_test.go`
- `./bin/roksbnkctl version` ✓ emits two-line output with `Docs: https://jgruberf5.github.io/roksbnkctl/book/`
- `./bin/roksbnkctl --version` ✓ emits the same shape via cobra's auto-generated `--version` flag (custom `VersionTemplate` wired in `internal/cli/root.go::Execute`)
- `goreleaser check` deferred to integrator (binary not on sandbox PATH) — see Issue 2
- `.goreleaser.yml` YAML syntax manually validated via `python3 yaml.safe_load`

## Priorities completed

| Priority | Item | Status |
|---|---|---|
| 1 | README rewrite for v1.0 (terraform-only prereq, book-as-canonical-docs framing, ~90 lines vs 735) | ✓ done |
| 2 | `roksbnkctl version` / `--version` includes the book URL; `DocsURL` constant; regression test | ✓ done |
| 3a | `.goreleaser.yml` `release.header` / `.footer` linking the book + CHANGELOG | ✓ done |
| 3b | `.goreleaser.yml` archives include LICENSE / README / CHANGELOG / MIGRATING | ✓ done |
| 3c | `.goreleaser.yml` cosign signing | ⏸ deferred to v1.x (Issue 3) |
| 3d | `.goreleaser.yml` PDF book artifact | ⏸ deferred to v1.x (Issue 4) |
| 3e | `.goreleaser.yml` Homebrew brews: block | ⏸ deferred to v1.x (Issue 5) |
| 4 | `CHANGELOG.md` §"v1.0.0" rollup — header rename, v1.0 intro paragraph, Sprint 7 launch additions, Documentation subsection, v0.9-Documentation forward-looking line flipped past-tense, `## Unreleased (v1.x)` stub at bottom | ✓ done |
| 5 | Smoke verification (build / test / vet / gofmt / version smoke) | ✓ done |

## Files created

- `internal/cli/meta_test.go` — new test file; `TestVersionCmd_OutputShape` pins the two-line shape (first line byte-identical to pre-v1.0, second line is `Docs: <url>`) and `TestDocsURL_Value` pins the constant.
- `issues/issue_sprint7_staff.md` (this file).

## Files edited

- `README.md` — full rewrite for v1.0 narrative; ~90 lines, terraform-only prereq, book-as-canonical-docs framing, install table, pointer block.
- `CHANGELOG.md` — renamed `## Unreleased — Sprint 6 (v1.0 prep)` to `## v1.0.0 — 2026-MM-DD (M4 milestone)`; added v1.0 intro paragraph, Sprint 7 launch additions, Documentation subsection, Deferred (v1.x roadmap) subsection, `## Unreleased (v1.x)` stub at bottom. Flipped v0.9 §"Documentation" forward-looking line to past-tense.
- `.goreleaser.yml` — added `release.header` / `release.footer`; archives now include `LICENSE` / `README.md` / `CHANGELOG.md` / `MIGRATING.md`; commented-out `brews:` block as v1.x marker; header comment documents the explicit signing/PDF/brew deferrals.
- `internal/cli/meta.go` — added `DocsURL` constant; `versionCmd` now writes via `cmd.OutOrStdout()` (testability) and emits the two-line shape ending with `Docs: <url>`.
- `internal/cli/root.go` — `Execute()` wires `rootCmd.Version = Version` and a custom `VersionTemplate` so `--version` and `roksbnkctl version` emit byte-identical output.

## Items deferred / handed off

- **CHANGELOG.md v1.0.0 date placeholder** (Issue 1) — single line edit by integrator at tag-cut time.
- **`goreleaser check` + `goreleaser release --snapshot`** (Issue 2) — integrator runs against the final pre-tag branch.
- **Cosign / sigstore release signing** (Issue 3) — v1.x roadmap; signing infra in `.github/workflows/release.yml` is the predecessor work.
- **PDF book artifact** (Issue 4) — v1.x roadmap; PLAN.md §"Sprint 7 Risks" already flagged as nice-to-have.
- **Homebrew tap stub** (Issue 5) — v1.x roadmap; brews: block scaffold left commented out for when the tap repo lands.

## Coordination with parallel agents

- **Architect** is doing the polish pass on all 32 book chapters + Mermaid diagrams + preface rewrite + PRD 05 §"Phase I/N" step refresh. No file conflicts — staff doesn't touch `book/src/*`, `docs/prd/*`, or `docs/PLAN.md`.
- **Validator** is re-verifying every chapter's `roksbnkctl ...` code example + cross-link audit + mdbook search spot-check + optional `e2e-full.yml` preflight polish. No file conflicts — staff doesn't touch `scripts/*` or the non-`release.yml` workflows.
- **Tech-writer** does the read-only review at end of sprint, including the dogfooding loop (PLAN.md §"v1.0 (M4)" gate). Will see the new README + CHANGELOG narrative.

The integrator merges the four agents' branches and cuts the v1.0 tag, which fires `release.yml` → goreleaser → GitHub Release with the binaries + checksums + the header/footer linking the book.

## MIGRATING.md spot-check

Staff prompt asked for a "spot-check that the v1.0 column matches" — verified, no drift found. `MIGRATING.md` §"From roksbnkctl v0.7 / v0.8 → v0.9 → v1.0" already lists the Sprint 6 (v1.0 prep) section with the green-by-default doctor refresh, the auto-generated reference chapters, and the top-level MIGRATING.md callout. The Sprint 7 additions (book launch, the `--version` URL append, the goreleaser release-page polish) are user-facing surface but don't break any v0.9 → v1.0 migration step — no MIGRATING.md edit required.

## Summary

5 priorities all green; 3 deferred to v1.x with explicit CHANGELOG callouts (signing, PDF, Homebrew); 1 integrator handoff (placeholder date); 1 integrator handoff (`goreleaser check` not on sandbox PATH). Build / vet / gofmt / test all clean. `./roksbnkctl version` and `./roksbnkctl --version` both emit the book URL. README trimmed from 735 to ~90 lines with the v1.0 launch narrative. `.goreleaser.yml` polished for v1.0 release page with header/footer linking the book + CHANGELOG. CHANGELOG.md §"v1.0.0" rollup ready for tag-cut date fill-in.
