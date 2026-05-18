# Sprint 7 — architect issues, resolution notes

Three issues filed: 1 medium (mdbook-mermaid integrator-install step), 2 low (chapter-23 disk-size carry-over conditional, search-index spot-check pending validator). All three closed cleanly — Issues 2 and 3 by the parallel dispatch (validator's findings folded; tech-writer's dogfood number didn't materially diverge); Issue 1 is an integrator pre-tag-cut handoff that's also tracked in `resolved_sprint7_tech-writer.md` Issue 12.

The architect was re-dispatched mid-sprint to **fold the parallel validator agent's 8 chapter findings** before tech-writer review; resolution notes for those 8 are in the same `issue_sprint7_architect.md` file under §"Sprint 7 validator fold — resolution notes" (lines 24-163). All 8 validator findings folded cleanly; repo-wide grep for the 6 stale tokens returns zero hits.

## Issue 1 (MEDIUM — mdbook-mermaid preprocessor install) — handed off to integrator

`book/book.toml` declares the `[preprocessor.mermaid]` block and references `additional-js = ["mermaid.min.js", "mermaid-init.js"]`. The JS asset files don't exist in the repo yet; the integrator runs `cargo install mdbook-mermaid && mdbook-mermaid install book/` once during the v1.0 dispatch so the assets land alongside `book.toml`. Architect Issue 1 + tech-writer Issue 12 + the CHANGELOG §"Deferred (v1.x roadmap)" section all reference this same integrator step.

**Status**: ✅ handed off (integrator runs the install command once at tag-cut time)

## Issue 2 (LOW — chapter-23 disk-size estimate carry-over) — resolved (no edit needed)

Sprint 6 architect Issue 11 deferred refinement of the chapter-23 "~200 MB workspace state" line to v1.0 sign-off. Tech-writer's dogfood-simulation walkthrough (per `issue_sprint7_tech-writer.md` §"Dogfooding-simulation walkthrough") didn't surface a materially different number — the 200 MB framing is honest about its "approximately, allow more for `.terraform/` cache" hedging. No edit applied; carry-over closed.

**Status**: ✅ resolved (200 MB estimate stands; tech-writer-dogfood validated)

## Issue 3 (LOW — search-index canonical-query spot-check) — resolved (cheap subset folded; rest deferred)

Validator's Issue 7 ran the spot-check; 11 of 15 canonical queries miss-route. The architect's fold pass landed 5 cheap lead-in tweaks (chapters 22, 25, 17, 16; chapter 30 already correct) and deferred 6 structural ones (`kubeconfig`, `cluster register`, `init --upgrade-tf`, etc.) to v1.x — those need chapter-title renames, which is a heavier-than-launch lift. Tech-writer's Issue 9 confirmed the deferral; CHANGELOG.md §"Deferred (v1.x roadmap)" now lists "search-index canonical-query relevance polish".

**Status**: ✅ resolved (cheap subset folded; structural rest deferred to v1.x with CHANGELOG entry)

## Sprint 7 validator fold — all 8 resolved (details in issue file lines 24-163)

Summary: Validator Issues 1-4 (HIGH non-existent flags `--keep-cluster`, `--api-key-stdin`, `roksbnkctl destroy` + `--auto-approve`, `--refresh-kubeconfig`) — resolved by rewriting the affected chapters' prose. Issue 5 (MEDIUM stale `state/terraform.tfvars.user` path) — search-and-replaced across chapters 10/12/14. Issue 6 (MEDIUM 5 broken anchors) — all 5 anchors fixed (chapter-14, 21, 25, 26, preface targets). Issue 7 (MEDIUM search miss-routes) — see Issue 3 above. Issue 8 (LOW `--streams` ambiguity) — reworded in chapter 22.

Post-fold grep `grep -nrE '(--keep-cluster|--api-key-stdin|--auto-approve|--refresh-kubeconfig|state/terraform\.tfvars\.user|roksbnkctl destroy)' book/src/` returns zero hits.

## Integrator additions (post-architect-fold)

Tech-writer's review surfaced 3 HIGH findings against the architect's fold (and other agents' work):

- **Tech-writer Issue 1** — `--auto` token leaked into the architect's fold of validator Issue 2 (chapter 12). Integrator re-folded: dropped `--auto` from `roksbnkctl init` calls in chapter 12 line 333 + chapter 26 line 22; added a sentence acknowledging `init` still prompts interactively for remaining workspace metadata and noting fully non-interactive bootstrap is on the v1.x roadmap.
- **Tech-writer Issue 3** — chapter 4 line 134 documented the version-output prefix as `Book:` but the shipped binary emits `Docs:`. Integrator changed `Book:` → `Docs:` and updated the follow-on prose explaining `DocsURL` is a compile-time constant (not ldflags-injected).
- **Tech-writer Issue 6** — "3-command happy path" framing across 5 surfaces (chapter 7, README, CHANGELOG, root.go `Long:`, chapter 3, chapter 4, chapter 6) didn't match the actual 4-command lifecycle (`init` → `up` → `test` → `down`) the chapters teach. Integrator aligned all 7 surfaces to "4-command lifecycle" and regenerated chapter 27 from the updated cobra tree.

Plus 3 MEDIUM tech-writer findings (Issues 4, 5, 8) folded by the integrator:
- **TW Issue 4** — chapter 11 worked-example terminal step rewrote "destroys BNK AND the cluster" + the literal "77 resources destroyed" count to reflect the `cluster register` discovery-only contract (registered cluster survives `down`; destroy count is ~30-40, not 77).
- **TW Issue 5** — CHANGELOG.md:25 `up/plan/apply/destroy --backend docker` → `up/plan/apply/down --backend docker`.
- **TW Issue 8** — chapter 12 line 333 added a one-sentence framing of `op` as the 1Password CLI (folded together with the `--auto` removal in the same chapter).

## Summary

3 architect-filed issues + 8 validator-fold-pass resolutions documented in `issue_sprint7_architect.md`. Integrator added 6 tech-writer-finding folds (3 HIGH + 3 MEDIUM) post-review. `go build / test / vet / gofmt` all green on the integrated state. Zero stale tokens (`--keep-cluster`, `--api-key-stdin`, `--auto-approve`, `--refresh-kubeconfig`, `roksbnkctl destroy`, `07-first-deploy`, `3-command happy path`) anywhere in book / README / CHANGELOG / root.go.

The codebase is in clean v1.0-launch shape entering the tag-cut.
