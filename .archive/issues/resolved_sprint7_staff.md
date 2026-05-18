# Sprint 7 — staff engineer issues, resolution notes

Five issues filed, all **informational** — 3 v1.x deferrals (cosign signing, PDF book artifact, Homebrew tap) and 2 integrator handoffs (CHANGELOG date placeholder, `goreleaser check` run). All accepted as the right v1.0-launch scope per the Sprint 7 staff prompt's "Defer if signing infrastructure isn't in place; flag as v1.x roadmap (and don't ship a half-working signs: block)" guidance.

## Issue 1 (INFORMATIONAL — CHANGELOG.md v1.0.0 date placeholder) — handed off to integrator

The `## v1.0.0 — 2026-MM-DD (M4 milestone)` header at the top of `CHANGELOG.md` carries the placeholder. Integrator fills the actual date at tag-cut (single-line edit). Goreleaser's release-page header surfaces `{{ .Tag }}` directly from the git tag, so the placeholder doesn't cascade.

**Status**: ✅ handed off (integrator pre-tag-cut step)

## Issue 2 (INFORMATIONAL — `goreleaser check` deferred to integrator) — accepted

Goreleaser not on the sprint VM PATH. Staff verified `.goreleaser.yml` YAML syntax via `python3 -c "import yaml; yaml.safe_load(open('.goreleaser.yml'))"` (clean). Integrator runs at tag-cut:

```bash
goreleaser check                       # config lint
goreleaser release --snapshot --clean  # multi-platform binary dry-run
```

Expected binary names + checksums.txt land in `dist/`; `internal/cli/self.go::assetName` shape pinned by the existing self-update test.

**Status**: ✅ accepted (integrator pre-tag-cut step)

## Issue 3 (INFORMATIONAL — Cosign / sigstore signing deferred to v1.x) — accepted

`.goreleaser.yml` does not wire a `signs:` block. Per the staff prompt's "don't ship half-wired" guidance: cosign isn't installed on the sprint VM; `.github/workflows/release.yml` doesn't have `COSIGN_EXPERIMENTAL=1` / `COSIGN_KEY` secrets wired; the standard `checksums.txt` SHA256 surface in `.goreleaser.yml::checksum:` already provides the integrity check that `roksbnkctl self update` relies on.

V1.x effort: `cosign generate-key-pair` → store private key as `COSIGN_KEY` GitHub Actions secret → wire `signs:` block in `.goreleaser.yml` → document signature verification in chapter 4 (Installation). CHANGELOG.md §"Deferred (v1.x roadmap)" calls this out explicitly.

**Status**: ✅ accepted (v1.x roadmap; CHANGELOG entry in place)

## Issue 4 (INFORMATIONAL — PDF book artifact deferred to v1.x) — accepted

`.goreleaser.yml` does not wire `mdbook-pdf` or `extra_files:` for a PDF. Per PLAN.md §"Sprint 7 Risks" classification ("PDF is a nice-to-have; HTML book is the canonical surface"). HTML book at <https://jgruberf5.github.io/roksbnkctl/book/> is the canonical user-documentation surface; v1.0 GitHub Release page header (`release.header` in `.goreleaser.yml`) links to it.

**Status**: ✅ accepted (v1.x consideration; CHANGELOG entry in place)

## Issue 5 (INFORMATIONAL — Homebrew formula stub deferred to v1.x) — accepted

`.goreleaser.yml` includes a commented-out `brews:` block as a marker. No `github.com/jgruberf5/homebrew-tap` repo exists yet; wiring the block would cause `goreleaser release` to fail when it tries to push the formula. When the tap repo lands, uncomment the block and add a Homebrew row to the README install table.

**Status**: ✅ accepted (v1.x roadmap when tap repo exists; CHANGELOG entry in place)

## Integrator additions

- **Tech-writer Issue 2** (HIGH) — README.md:76 pointer link used stale slug `07-first-deploy.html`. Integrator replaced with `07-quick-start.html` (the earlier mention on README line 33 already used the correct slug; this was a copy-paste leftover).
- **Tech-writer Issue 5** (MEDIUM) — CHANGELOG.md:25 documented non-existent `roksbnkctl destroy` token under v0.9 Sprint 5 entry. Integrator replaced `up/plan/apply/destroy --backend docker` → `up/plan/apply/down --backend docker`.
- **Tech-writer Issue 6** (MEDIUM) — `3-command happy path` framing across README + CHANGELOG + internal/cli/root.go didn't match the 4-command lifecycle (`init` → `up` → `test` → `down`) the chapters teach. Integrator aligned all 3 staff-owned surfaces to "4-command lifecycle"; chapter 27 then regenerated from the updated cobra tree.

## Verification of staff's Priority 1-5 deliverables

- **Priority 1 (README rewrite)**: README dropped from 735 lines (the v0.9 candidate framing) to ~92 lines (v1.0 framing — terraform-only prereq, book-as-canonical-docs framing, install table, pointer block). Post-integrator-fold the README is internally consistent across all chapter-7 references.
- **Priority 2 (`roksbnkctl version` book URL)**: `DocsURL` constant declared in `internal/cli/meta.go`; both `versionCmd.RunE` and `rootCmd.Version` reference it. Two unit tests pin the contract (`TestVersionCmd_OutputShape`, `TestDocsURL_Value`). Post-integrator-fold of tech-writer Issue 3, the chapter 4 sample output (`Docs: ...`) matches the binary's emission exactly.
- **Priority 3 (`.goreleaser.yml` finalisation)**: `release.header` / `release.footer` link the book + MIGRATING + CHANGELOG; archives now bundle LICENSE / README / CHANGELOG / MIGRATING; commented-out `brews:` scaffold present. Signing / PDF / Homebrew explicitly deferred to v1.x with CHANGELOG entries.
- **Priority 4 (`CHANGELOG.md` v1.0.0 rollup)**: section header renamed, v1.0 intro paragraph added (post-integrator-fold of TW Issue 6 the framing reads "4-command lifecycle"), Sprint 7 launch additions documented, Documentation subsection added, Deferred (v1.x roadmap) subsection added, `## Unreleased (v1.x)` stub at bottom.
- **Priority 5 (smoke verification)**: `go build / test / vet / gofmt` all green on the integrated state. 14 Go packages pass tests. Version-output smoke confirmed post-fold (both `version` and `--version` emit the `Docs:` URL).

## Summary

5 issues filed; 5 closed cleanly (2 handed off to integrator at tag-cut, 3 deferred to v1.x with CHANGELOG entries). Integrator folded 3 tech-writer findings against staff-owned surfaces (README slug, CHANGELOG `destroy` token, 4-command framing alignment). Build, vet, gofmt, full test suite, manual version-output smoke all green.

The `.goreleaser.yml` is finalised for the v1.0 tag-cut; integrator runs `goreleaser check` + `goreleaser release --snapshot --clean` against the pre-tag branch to confirm multi-platform builds.
