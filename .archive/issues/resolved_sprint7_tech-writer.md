# Sprint 7 — tech writer issues, resolution notes

Twelve issues filed: **3 high** (Issues 1-3: invalid `init --auto` flag in ch.12+26; broken README slug; version-output `Book:` vs `Docs:` label drift), **3 medium** (Issues 4-6: chapter-11 walkthrough logic inconsistency; CHANGELOG `destroy` token; "3-command" vs "4-command" framing across 5 surfaces), **2 low** (Issues 7-8: preface convention nuance; password-manager framing), **4 roadmap** (Issues 9-12: search-index polish; `--truncated` user flag; cluster-sharing; mdbook-mermaid install). **None blocker**.

Per the integrator's "Fold HIGH + MEDIUM" directive, 6 issues (1-6) were folded by the integrator post-tech-writer-review. The 2 LOW issues were also folded since they were single-line tweaks bundled with the chapter-12 edit. The 4 roadmap items are documented in CHANGELOG.md §"Deferred (v1.x roadmap)".

## Issue 1 (HIGH — `roksbnkctl init --auto` flag does not exist) — resolved by integrator

**Files**: `book/src/12-workspace-config.md:333`, `book/src/26-troubleshooting.md:22`

The ch.12 instance was introduced by the architect's fold of validator Issue 2 (substituting `--auto` for the non-existent `--api-key-stdin`); the ch.26 instance survived from earlier sprints. Integrator removed `--auto` from both:

- **Ch.12 line 333**: `IBMCLOUD_API_KEY=$(op read 'op://...') roksbnkctl init -w dev` (no `--auto`). Added a follow-on sentence noting `init` still prompts interactively for the remaining workspace metadata (region, RG, cluster name) and that fully-non-interactive bootstrap is on the v1.x roadmap.
- **Ch.26 line 22**: `IBMCLOUD_API_KEY=$(cat /path/to/secret) roksbnkctl init -w my-workspace` (no `--auto`). Same v1.x-roadmap note added.

Post-fold grep `grep -nE 'init.*--auto|init --auto' book/src/` confirms zero remaining instances of the non-existent flag.

**Status**: ✅ resolved (chapters 12 + 26 ↔ shipped `init` flag surface consistent)

## Issue 2 (HIGH — README pointer link stale slug `07-first-deploy.html`) — resolved by integrator

**File**: `README.md:76`

Single-line drift; integrator replaced `07-first-deploy.html` with `07-quick-start.html` (the earlier README mention on line 33 already used the correct slug). v1.0 GitHub-Release `release.header` (`.goreleaser.yml`) also surfaces the book URL but uses the root book URL, not a chapter-specific deep link, so no further fix required there.

**Status**: ✅ resolved (README links to the book's quick-start chapter without 404)

## Issue 3 (HIGH — chapter 4 version-output `Book:` vs `Docs:`) — resolved by integrator

**File**: `book/src/04-installation.md:134`

The chapter documented the version-output second line as `Book: https://...` but the shipped binary emits `Docs: https://...` (per `internal/cli/meta.go::DocsURL` constant, pinned by `internal/cli/meta_test.go::TestVersionCmd_OutputShape`). Integrator changed `Book:` → `Docs:` and corrected the follow-on prose to explain `DocsURL` is a compile-time constant (the original prose claimed it was ldflags-injected, which was inaccurate).

**Status**: ✅ resolved (chapter 4 ↔ `internal/cli/meta.go::DocsURL` consistent)

## Issue 4 (MEDIUM — chapter 11 walkthrough terminal-step logical inconsistency) — resolved by integrator

**File**: `book/src/11-tearing-down.md:201-235`

The "register an existing cluster, deploy BNK, tear down" worked-example terminal step claimed "destroys BNK AND the cluster" and showed `Resources: 77 destroyed`, both inconsistent with the `cluster register` discovery-only contract (the cluster pre-existed roksbnkctl; terraform state holds only the overlay + jumphost). Integrator rewrote the step-6 comment block to:

1. Frame the destroy as "destroys the BNK overlay; the registered cluster survives"
2. Replace the literal `77 destroyed` with `N destroyed` and add explanatory prose that ~30-40 resources is the register-then-deploy destroy count (vs ~77 from-scratch)
3. Add a follow-on paragraph clarifying that releasing the pre-existing cluster requires tearing it down through whatever provisioned it originally (the IBM Cloud console or a separate terraform tree); `roksbnkctl cluster down` only works against clusters `roksbnkctl cluster up` created

Cross-link to chapter 8 for the cluster-phase boundary.

**Status**: ✅ resolved (chapter 11 walkthrough ↔ `cluster register` discovery-only contract consistent)

## Issue 5 (MEDIUM — CHANGELOG v0.9 Sprint 5 entry documents non-existent `roksbnkctl destroy`) — resolved by integrator

**File**: `CHANGELOG.md:25`

Single literal occurrence; integrator replaced `roksbnkctl up/plan/apply/destroy --backend docker` → `roksbnkctl up/plan/apply/down --backend docker`. CHANGELOG cross-checked: no other instances of the stale `destroy` token in the file.

**Status**: ✅ resolved (CHANGELOG v0.9 entry ↔ shipped CLI surface consistent)

## Issue 6 (MEDIUM — "3-command happy path" vs 4-command lifecycle across 5 surfaces) — resolved by integrator

**Files**: `book/src/07-quick-start.md:3` + `:5`, `README.md:33`, `CHANGELOG.md:87`, `internal/cli/root.go:47`, and 2 additional surfaces the tech-writer didn't list but that surfaced during the integrator sweep: `book/src/03-what-roksbnkctl-does.md:7+99` and `book/src/06-workspaces.md:41`.

Per tech-writer's recommendation (option 2: "4-command lifecycle"), integrator aligned all 7 surfaces to `init` → `up` → `test` → `down`. Changes:

- **Chapter 7**: opening sentence and follow-on diagram-intro both reference the 4-command lifecycle explicitly.
- **README**: "That's the 4-command lifecycle..." sentence aligned.
- **CHANGELOG**: v1.0 intro paragraph reads "a 4-command lifecycle (`init` → `up` → `test` → `down`)".
- **`internal/cli/root.go`**: `rootCmd.Long` updated to "The 4-command lifecycle:" + added the `roksbnkctl down` line that was previously missing from the help text. Also updated the trailing pointer from `docs/PRD.md` to the published book URL.
- **Chapter 3**: heading `## The 3-command happy path` → `## The 4-command lifecycle`; body prose and code block now include `down` as the fourth step.
- **Chapter 4**: cross-reference to chapter 7 updated to "walks the 4-command lifecycle end-to-end".
- **Chapter 6**: heading `## The 3-command path` → `## The everyday workspace routine` (the chapter-6 framing is about workspaces, not the lifecycle; the rename is cleaner than forcing a 4-command framing into a workspace-routine section).

Chapter 27 (auto-generated command reference) regenerated via `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md` so the chapter picks up `root.go`'s new `Long:` description. Post-fold grep `grep -nrE '3-command' book/src/ README.md CHANGELOG.md internal/cli/root.go` returns zero hits.

**Status**: ✅ resolved (7 surfaces aligned on "4-command lifecycle")

## Issue 7 (LOW — preface line 48 cross-reference convention nuance) — resolved by integrator

**File**: `book/src/preface.md:48`

This was folded together with the chapter-12 edits since the same code block appeared. Integrator updated the preface to call out the three actual cross-reference shapes (full-form chapter+title, section-anchor, bare chapter number) so readers don't think the second and third forms violate the convention.

**Status**: ✅ resolved (preface convention paragraph ↔ actual chapter usage consistent)

## Issue 8 (LOW — chapter 12 line 333 `op` 1Password CLI framing) — resolved by integrator

**File**: `book/src/12-workspace-config.md:333`

Folded together with Issue 1 in the same chapter-12 pass. Integrator prepended a one-sentence framing explaining `op` is the [1Password CLI](https://developer.1password.com/docs/cli/) and the `op://...` URI is its secret-reference scheme; noted that Bitwarden / gopass / AWS Secrets Manager / Doppler all work the same way since roksbnkctl only cares about `IBMCLOUD_API_KEY` being set in the environment.

**Status**: ✅ resolved (chapter 12 example accessible to non-1Password users)

## Issue 9 (ROADMAP — search-index canonical-query miss-routes) — deferred to v1.x

11 of 15 canonical queries miss-route to auto-generated reference chapters 27/29. Validator Issue 7 + architect's partial fold landed 5 of the cheap subset; the remaining 6 are structural (need chapter-title renames to shift lunr's title-weighted ranking). Per tech-writer's recommendation, deferred to v1.x. CHANGELOG.md §"Deferred (v1.x roadmap)" lists "search-index canonical-query relevance polish".

**Status**: ✅ deferred (v1.x roadmap; CHANGELOG entry in place)

## Issue 10 (ROADMAP — `--require-untruncated` user-facing CLI flag) — deferred to v1.x

Sprint 6 validator Issue 6 carry-over. Internal `TestProbe_TruncatedFlag` already pins the TC=1 → TCP-retry projection; user-facing CLI flag (e.g., `roksbnkctl test dns --require-untruncated` for CI assertions) is a v1.x polish item. PLAN.md §"What's deliberately deferred to post-v1.0" + CHANGELOG.md §"Deferred (v1.x roadmap)" both list this.

**Status**: ✅ deferred (v1.x roadmap; already documented in CHANGELOG)

## Issue 11 (ROADMAP — cross-driver cluster-sharing for `e2e-test-full.sh`) — deferred to v1.x

Sprint 6 validator Issue 4 carry-over. Saves ~50 min per full-e2e run if the baseline driver and the backends driver share a single cluster apply. PRD-envisioned design; for v1.0 ships as serial chained drivers. CHANGELOG.md §"Deferred (v1.x roadmap)" already lists this.

**Status**: ✅ deferred (v1.x roadmap; already documented in CHANGELOG)

## Issue 12 (ROADMAP — mdbook-mermaid install step) — handed off to integrator

`book/book.toml` declares the `[preprocessor.mermaid]` block; the JS asset files don't exist in the repo yet. Integrator runs `cargo install mdbook-mermaid && mdbook-mermaid install book/` once at the v1.0 dispatch. Identical handoff to architect Issue 1 + the CHANGELOG entry.

**Status**: ✅ handed off (integrator runs the install command once during the v1.0 tag-cut sweep)

## Dogfooding-simulation walkthrough verification

Tech-writer's preface→ch.7 read-through surfaced 3 stuck-points (Issues 1, 3, 6 above). All three are now folded; a real external-user dogfood at tag-cut should hit zero of those frictions. Stuck-points NOT surfaced (good news the tech-writer flagged):

- Chapter 7 prerequisites are honest (terraform + IBM Cloud API key + doctor green; no over-promising).
- Each chapter-7 step's expected-output blocks are realistic (ROKS apply correctly characterised as ~30-40 min; abridged log framing honest).
- Cross-link from preface to chapter 7 resolves.
- Chapter-7 step 7 (`down`) closes the cost-incurring loop; no abandoned-cluster scenario possible for a reader who follows the chapter end-to-end.

## v1.0 launch-readiness audit verification (PLAN.md §"v1.0 (M4)" gate)

Tech-writer's audit recapped here; post-fold-pass status:

| # | Criterion | Verdict |
|---|---|---|
| 1 | All E2E phases pass on a clean test host | TBD by integrator (live run at tag-cut) |
| 2 | All previous sprints' acceptance criteria still hold | **met** (full test suite green; zero regressions) |
| 3 | Cred audit clean (Phase M) | TBD by integrator (live run at tag-cut) |
| 4 | Doctor green-by-default on stock dev box | **met** (test pinned) |
| 5 | Book published, 32+ chapters, dogfooded, no placeholders, examples verified | **partially met** (book content launch-ready; 3 HIGH chapter findings now folded; GitHub Pages publish + real external dogfood are integrator-scope at tag-cut) |
| 6 | Release artifacts attached | TBD by integrator (`goreleaser release` at tag-cut) |
| 7 | README ↔ book bidirectional links | **met** (post-fold of Issue 2) |

**No blocker-severity issues** prevent the integrator from cutting the v1.0 tag.

## Integrator additions

- Re-ran `go build / test / vet / gofmt ./...` after folding all 6 HIGH+MEDIUM tech-writer issues — all green.
- Regenerated `book/src/27-command-reference.md` from the updated cobra tree so the auto-generated chapter picks up `internal/cli/root.go`'s new "4-command lifecycle" `Long:` description.
- Verified zero stale tokens (`--keep-cluster`, `--api-key-stdin`, `--auto-approve`, `--refresh-kubeconfig`, `state/terraform.tfvars.user`, `roksbnkctl destroy`, `07-first-deploy`, `3-command happy path`) anywhere in book / README / CHANGELOG / internal/cli/root.go via final repo-wide grep.

## Summary

12 issues filed; 8 resolved by integrator post-review (3 HIGH + 3 MEDIUM directives + 2 LOW bundled fixes); 4 deferred to v1.x with CHANGELOG entries already in place. The dogfooding-simulation stuck-points all now have folded fixes — a real external-user dogfood at tag-cut should hit zero documented-but-non-existent-flag friction.

**v1.0 launch-readiness verdict**: **TBD by integrator at tag-cut**, but **no blockers**. Four gate criteria fully met (2, 4, 5-content-portion, 7); three integrator-scope at tag-cut (1, 3, 6); criterion 5's "dogfooded by ≥1 external user" + "GitHub Pages publish" steps are at-tag-cut work. The codebase is in clean v1.0-launch shape.
