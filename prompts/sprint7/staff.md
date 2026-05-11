You are the staff engineer agent for Sprint 7 of the roksbnkctl project. Sprint 7 cuts the **`v1.0` release tag** — your code lands the release-readiness pieces: the **README rewrite for v1.0**, the **book URL in `roksbnkctl --version`**, the **`.goreleaser.yml` finalisation** (multi-platform binaries, checksums, optional PDF book artifact attached to the GitHub release), the **`CHANGELOG.md` §"v1.0.0" rollup** (the existing "Unreleased — Sprint 6" section plus a v0.7 → v1.0 summary), and a small **`--version`-output test** to pin the new shape.

Project location: `/mnt/c/project/roksbnkctl/`. Note path change from Sprint 6 (was `/mnt/d/...`) — confirm by `pwd`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25.

The actual `git tag v1.0` + `git push --tags` + `goreleaser release --clean` + GitHub Pages publish are **integrator-owned**. Your scope is the prep work that gets committed before the tag cuts.

## Read first

- `docs/PLAN.md` Sprint 7 §"Code / config deliverables" — rows 1-5: README rewrite, `--version` book URL, release notes, goreleaser finalisation, Homebrew stub if scope permits.
- `docs/PLAN.md` §"v1.0 (M4)" — the gate criteria your code must satisfy.
- `README.md` — currently 200+ lines; the "Status" line at row 5 says `v0.9 release candidate`. Sprint 7 flips that to `v1.0` (or `v1.0 release candidate` if the tag hasn't cut yet — integrator's call; prose works either way).
- `internal/cli/meta.go` — the `versionCmd` flow. Today prints `roksbnkctl <version> (commit <c>, built <date>)`. Sprint 7 extends to include the book URL.
- `internal/cli/root.go` — where `Version`, `Commit`, `BuildDate` are declared. No edit expected here; reference for the build-time variable shape.
- `.goreleaser.yml` — current 52-line config covers Linux/macOS/Windows × amd64/arm64 with checksums + snapshot template. Sprint 7 polishes for v1.0: signing if scope permits, the PDF book artifact attached to the release (optional — PDF is "nice to have" per PLAN.md §"Sprint 7 Risks").
- `CHANGELOG.md` — the existing "Unreleased — Sprint 6 (v1.0 prep)" section needs renaming to `v1.0.0 — <date> (M4 milestone)` at tag time; you draft the rollup prose so the integrator's tag-cut is mechanical.
- `MIGRATING.md` — already covers v0.6.x → v0.9 → v1.0; spot-check that the v1.0 column matches the actual binary's v1.0 surface (no edits expected; flag if drift).
- `prompts/sprint6/staff.md` for prompt-structure reference.
- `issues/resolved_sprint6_staff.md` — Sprint 6 staff carry-overs (1 resolved, 2 accepted as v1.x roadmap). Nothing in here is a Sprint 7 blocker for you; just context.

## Coordinate with parallel agents

A **architect** agent is doing the polish pass on all 32 book chapters, landing Mermaid diagrams, rewriting the preface, adding worked-example walkthroughs per Part, and refreshing PRD 05 §"Phase I" + §"Phase N" step matrices. **Do not touch any file under `book/src/`, `docs/prd/`, or `docs/PLAN.md`.**

A **validator** agent is re-verifying every chapter's code examples against a fresh workspace + real cluster where possible, doing the cross-link audit, spot-checking mdbook search, and optionally polishing the `e2e-full.yml` preflight fail-fast. **Do not touch `scripts/e2e-test*.sh`, `.github/workflows/*.yml`, or `cspell.json`.**

A **tech-writer** agent does read-only review at the end of the sprint, including the dogfooding loop. They file issues; the integrator folds.

**Your scope** is `README.md`, `CHANGELOG.md`, `.goreleaser.yml`, `internal/cli/meta.go`, `internal/cli/meta_test.go` (or a new test file for the version-output assertion), and `.github/workflows/release.yml` if the goreleaser invocation needs adjusting.

## Tasks (priority order)

If you run out of token budget, stop at a priority boundary and file an issue for what's deferred.

### Priority 1 — `README.md` rewrite for v1.0

Lift the framing from "v0.9 release candidate / four-backend tour" to "v1.0 / single-binary CLI for deploying BNK on ROKS". Keep the existing structure (badge / status / what's in this repo / quick start / etc.) but tighten the prose for the launch narrative:

- **Top banner** — keep the "Read the book" badge linking to `https://jgruberf5.github.io/roksbnkctl/book/`.
- **Status line** — flip `> **Status:** v0.9 release candidate.` to `> **Status:** v1.0 — first stable release.` (Or `v1.0 release candidate` if the tag hasn't cut yet by integration time; tech-writer's dogfooding loop will determine which.) Drop the "four execution backends (local, docker, k8s, ssh), GSLB-aware DNS probe..." enumeration — at v1.0 those are background facts, not novelty. Replace with a one-line "what this is for" framing.
- **"What's in this repo" tree** — keep current; spot-check that the file paths match today's tree (`internal/`, `cmd/roksbnkctl/`, `terraform/`, `tools/`, `book/`, `docs/`, `scripts/`).
- **Quick start** — collapse to a 5-command happy path (`go install` or `brew install` → `roksbnkctl init` → set API key → `roksbnkctl up` → `roksbnkctl test`). Cross-link to chapter 7 for the full walkthrough.
- **Install paths** — keep the `go install` + `brew install` (if Homebrew tap lands; integrator-owned) + binary download options. Update goreleaser-produced binary names if `.goreleaser.yml` changed their shape.
- **Prerequisites** — single line: `terraform` only (the doctor green-by-default refactor from Sprint 6 made this true).
- **Pointers** — book URL (canonical user docs), `docs/PLAN.md` (sprint history), `docs/prd/` (design rationale), `MIGRATING.md` (upgrade notes), `CONTRIBUTING.md` (how to contribute).
- **Drop** — anything that says "coming soon", "planned for vX.Y", "Sprint N delivers...". v1.0 is the present tense.
- **Length** — aim for ~150 lines (currently 200+; trim aggressively, the book is the canonical documentation surface).

### Priority 2 — `roksbnkctl --version` / `roksbnkctl version` includes the book URL

PLAN.md row 2 says `roksbnkctl --version` should include the book URL. The current shape (`internal/cli/meta.go::versionCmd`):

```go
fmt.Printf("roksbnkctl %s (commit %s, built %s)\n", Version, Commit, BuildDate)
```

Extend to:

```
roksbnkctl <version> (commit <commit>, built <date>)
Docs: https://jgruberf5.github.io/roksbnkctl/book/
```

A second-line print is fine; the existing single-line shape is too packed for the URL. Keep the first line byte-identical to today's output so any scripts parsing it (unlikely but possible) don't break — append the second line.

Also wire the same URL via `--version` (the cobra root command's auto-generated `--version` flag). Cobra wires `--version` from `rootCmd.Version`; today this is probably unset, so `--version` doesn't work at all. Set `rootCmd.Version` to `Version + "\n" + bookURL` in `internal/cli/root.go::init()` (or wherever rootCmd is built) so both code paths emit the URL.

Add a tiny test at `internal/cli/meta_test.go` (or extend an existing one) that:

- Captures stdout from `versionCmd.RunE` with `Version="v1.0.0", Commit="abc1234", BuildDate="2026-05-24"`
- Asserts the output contains `roksbnkctl v1.0.0`
- Asserts the output contains `https://jgruberf5.github.io/roksbnkctl/book/`

Constant for the URL: declare `const DocsURL = "https://jgruberf5.github.io/roksbnkctl/book/"` somewhere in `internal/cli/` (`meta.go` is fine) so it's a single source of truth.

If `internal/cli/self.go` (the self-update flow) also surfaces a "current version" line, extend that too for consistency.

### Priority 3 — `.goreleaser.yml` finalisation

The current 52-line config covers the multi-platform sweep. v1.0 polish:

- **Signing** — add a `signs:` block using `cosign` (sigstore) for the binaries + checksums. Sigstore is the lowest-friction option; the integrator can set `COSIGN_EXPERIMENTAL=1` + `COSIGN_PASSWORD` secrets in the GitHub release workflow. Defer if signing infrastructure isn't in place; flag as v1.x roadmap (and don't ship a half-working signs: block).
- **PDF book artifact** — optional per PLAN.md §"Sprint 7 Risks" ("PDF is a nice-to-have, HTML book is the canonical surface"). If `mdbook-pdf` is available, wire a `before:` hook that runs `mdbook build -o book/book.pdf book/` or similar, and add an `extra_files:` entry pointing at the PDF so it lands on the GitHub release. If `mdbook-pdf` doesn't have a clean WSL2/Linux story, drop this — flag as v1.x roadmap.
- **Release notes** — wire `release.header` and `release.footer` so the GitHub release page links to the book + CHANGELOG. Concrete:
  ```yaml
  release:
    header: |
      ## roksbnkctl {{ .Tag }}

      Single-binary CLI for deploying F5 BIG-IP Next for Kubernetes on IBM Cloud ROKS.

      📖 [Read the book](https://jgruberf5.github.io/roksbnkctl/book/) — _Deploying and Testing BIG-IP Next for Kubernetes with roksbnkctl_

      📋 [Migration guide](https://github.com/jgruberf5/roksbnkctl/blob/main/MIGRATING.md)
    footer: |
      ---
      See [`CHANGELOG.md`](https://github.com/jgruberf5/roksbnkctl/blob/main/CHANGELOG.md) for the full per-version change log.
  ```
- **Homebrew tap stub** — PLAN.md row 5 ("optional Homebrew formula stub"). If the integrator has a tap repo ready, wire the `brews:` block; otherwise leave for v1.x. Don't ship a half-wired brew block that points at a nonexistent tap.
- **Archive name template** — current `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}` is fine; flag if v1.0 should follow a different naming convention. Sprint 4's `internal/cli/self.go::archiveNameTemplate` references this shape — keep alignment.

Run `goreleaser check` (the integrator can confirm; you flag if it's not available in your sandbox) to lint the config before reporting done. Run `goreleaser release --snapshot --clean` if you can to verify the multi-platform build produces binaries; document the binaries' relative-path landing places in your final report so the integrator can confirm them at tag-cut time.

### Priority 4 — `CHANGELOG.md` §"v1.0.0" rollup

The existing CHANGELOG has an "Unreleased — Sprint 6 (v1.0 prep)" section. Sprint 7 prepares the rollup:

1. Rename the section header from `## Unreleased — Sprint 6 (v1.0 prep)` to `## v1.0.0 — <YYYY-MM-DD> (M4 milestone)`. The integrator fills the date at tag-cut time; leave a placeholder like `## v1.0.0 — 2026-MM-DD (M4 milestone)` and call out the date-fill task in your final report.
2. Add a 3-5 sentence intro paragraph framing v1.0: "The first stable release. Bundles seven sprints of work (M1 → M4) into a single-binary CLI..."; cite v0.7 (M1: --on jumphost), v0.8 (M2: kubectl internalisation), v0.9 (M3: four backends + DNS probe), v1.0 (M4: book launch + e2e gate). Keep the Keep-a-Changelog category headers (Added / Changed / Removed / Deprecated / Fixed / Security as needed).
3. Roll up Sprint 6's added/changed list verbatim — that section already has the content; you just renormalise the section header + add the v1.0 intro paragraph + add the Sprint 7 launch additions (book launch, Mermaid diagrams, worked-example walkthroughs, foreword rewrite, release artifacts).
4. Add a `### Documentation` subsection noting the book at `https://jgruberf5.github.io/roksbnkctl/book/` is now 32 chapters + preface + worked-example walkthroughs + Mermaid diagrams, dogfooded by ≥1 external user (per PLAN.md §"v1.0 (M4)" gate).
5. Update the v0.9-section "Documentation" sub-section's forward-looking line ("Sprint 6 will land chapters 23-32...") to past-tense ("Sprint 6 landed chapters 23-32; Sprint 7 launched the polished book alongside the v1.0 tag.").
6. Add a stub `## Unreleased (v1.x)` section at the bottom referencing PLAN.md §"What's deliberately deferred to post-v1.0". This is where the next dev cycle's CHANGELOG entries will land.

### Priority 5 — Smoke verification

Run the local build + lint sweep before reporting done:

- `go build ./...` clean
- `go test ./...` clean (incl. the new version-output test)
- `go vet ./...` clean
- `gofmt -d -l .` clean for any Go file you touched
- `./roksbnkctl version` post-build emits the new two-line shape with the book URL
- `./roksbnkctl --version` post-build emits the new shape via cobra's auto-generated flag
- `goreleaser check` clean (if `goreleaser` is on PATH; flag if not)
- `goreleaser release --snapshot --clean` produces binaries in `dist/` for at least linux/amd64 + darwin/amd64 (if `goreleaser` is on PATH; flag if not — the integrator can confirm at tag-cut time)

## Issue tracking

`/mnt/c/project/roksbnkctl/issues/issue_sprint7_staff.md`. Same format as Sprint 6.

## Final report (under 200 words)

- Files created
- Files edited
- Build / test / vet / gofmt status
- Goreleaser dry-run result (snapshot build produces N binaries; or "deferred to integrator" if goreleaser isn't on the sandbox PATH)
- Version-output smoke: `./roksbnkctl version` and `./roksbnkctl --version` both emit the book URL (yes/no)
- Issues filed (counts by severity)
- Anything the integrator should know — especially the placeholder date in `CHANGELOG.md` §"v1.0.0" that needs filling at tag-cut time, plus whether signing / PDF / Homebrew were wired vs deferred

Do NOT commit. The integrator commits the aggregated work and cuts the `v1.0` tag.
