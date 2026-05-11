# Sprint 7

**Theme:** Book launch + `v1.0` cut

_Drafted from `docs/PLAN.md` Sprint 7 section. This is the **launch sprint** — every chapter gets a polish pass, Mermaid diagrams land, the foreword/preface gets a proper rewrite, every code example is re-verified in a fresh workspace, and the `v1.0` tag closes the M4 milestone. After this sprint the binary AND the book ship together at `https://jgruberf5.github.io/roksbnkctl/book/`._

Sprint 6 closed cleanly (all four agents' issues resolved or accepted; `go build/vet/gofmt/test` green; both `DRY_RUN` walkthroughs green). The carry-overs into Sprint 7 are:

1. **PRD 05 §"Phase I" + §"Phase N" step-matrix refresh** (Sprint 6 tech-writer Issue 12) — PRD 05 §"Phase I" lists I0-I7 (8 steps) but the shipped driver implements I0-I11 (12); PRD 05 §"Phase N" lists N0-N10 (11) but the shipped driver implements N1-N6 (6 restructured). PRD prose should match the shipped surface for the v1.0 release narrative. **Architect** owns the PRD edit.
2. **Chapter 23 disk-size estimate refinement** (Sprint 6 architect Issue 11) — the "~200 MB workspace state" line is a rough OOM figure; the integrator's dogfood run is the right place to refine. **Tech-writer** flags if the dogfood number diverges materially; **architect** edits if so.
3. **`e2e-full.yml` workflow secret pre-flight fail-fast** (Sprint 6 validator Issue 5) — currently the workflow surfaces missing-secret failure at `roksbnkctl up` time instead of preflight. Small polish if scope permits. **Validator** owns.
4. **README "Status" line flip** from `v0.9 release candidate` to `v1.0` (or `v1.0 release candidate` if the tag hasn't cut yet). **Staff** owns the README rewrite.
5. **`Truncated` user-facing CLI flag** (Sprint 6 validator Issue 6) — v1.x roadmap; no Sprint 7 action expected, just track in the deferred list.
6. **Cross-driver cluster-sharing for `e2e-test-full.sh`** (Sprint 6 validator Issue 4) — v1.x roadmap; tracked in PLAN.md §"What's deliberately deferred to post-v1.0".

The four-agent dispatch shape is the same as Sprints 1-6:

- **Architect** — book polish pass on all 32 chapters; Mermaid diagrams (architecture, lifecycle, GSLB cross-vantage, execution-backend matrix); foreword/preface rewrite; worked-example walkthroughs in each Part; PRD 05 §I + §N step refresh.
- **Staff engineer** — README rewrite for v1.0; `--version` output includes the book URL; `goreleaser.yml` finalisation for multi-platform binaries + checksums + optional PDF book artifact; ldflags + ldenv polish; the `roksbnkctl self update` smoke against a real `v1.0` tag.
- **Validator** — re-verify every `roksbnkctl ...` code example in every chapter (in a fresh workspace, against a real cluster where possible); cross-link audit (every "see Chapter X" resolves); search-index spot-check; mdbook build/test in CI runs clean on every chapter; optional `e2e-full.yml` preflight fail-fast polish; CHANGELOG `v1.0` section finalisation.
- **Tech-writer** — read-only review of the polish pass; dogfooding loop (at least one external-user-perspective walkthrough of the quick-start chapter against a clean workspace); launch-readiness audit against PLAN.md §"v1.0 (M4)" gate criteria; PRD/PLAN drift sweep one last time before the tag cuts.

The release tag itself (`v1.0`) is **integrator-owned** — Sprint 7 lands all the prep work; the integrator cuts the tag, kicks off goreleaser, and publishes the book to GitHub Pages after the four agents' work merges.
