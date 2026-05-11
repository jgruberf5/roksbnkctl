# Sprint 7 — validator issues, resolution notes

Eight issues filed: 4 high (non-existent CLI flags / commands in chapters 11/12/17/28), 3 medium (stale `state/terraform.tfvars.user` path; broken cross-link anchors; search-index miss-routes), 1 low (`--streams` flag formatting ambiguity in chapter 22). **Zero build / test / vet / gofmt regressions** filed; **zero DRY_RUN walkthrough regressions** filed — all 8 are chapter-prose divergences from the shipped binary surface, **first-impression failures for a dogfooding user** but **none block the `v1.0` tag on technical grounds**.

All 8 issues folded by the architect agent in a second dispatch (after this validator issue file was filed in parallel). Per-issue resolution notes are in `issue_sprint7_architect.md` §"Sprint 7 validator fold — resolution notes" (lines 24-163); summarised below for the resolved-file mirror.

## Issue 1 (HIGH — chapter 11 `--keep-cluster` flag) — resolved by architect fold

**File**: `book/src/11-tearing-down.md:237`

Architect rewrote the "destroy only the BNK overlay" paragraph to remove the non-existent `--keep-cluster` flag. The cluster-vs-trial separation is enforced by the `roksbnkctl cluster up` / `cluster down` split (Sprint 3), not a flag on `down`. New prose makes "leave the cluster up" the **default** behaviour when `cluster up` was used separately. Cross-link to chapter 8 for the two-command split.

**Status**: ✅ resolved (chapter 11 ↔ shipped binary consistent)

## Issue 2 (HIGH — chapter 12 `--api-key-stdin` flag) — resolved by architect fold; `--auto` re-folded by integrator

**File**: `book/src/12-workspace-config.md:333`

Architect replaced the `op read | roksbnkctl init --api-key-stdin` example with `IBMCLOUD_API_KEY=$(op read 'op://...') roksbnkctl init -w dev --auto`. **However**, tech-writer's review caught that `--auto` doesn't exist on `roksbnkctl init` either (only `--tf-source` and `--upgrade-tf`). Integrator re-folded: dropped `--auto`, added a sentence acknowledging `init` still prompts interactively for remaining metadata, noted fully-non-interactive bootstrap is on the v1.x roadmap. Also folded tech-writer Issue 8 in the same chapter-12 pass: prepended a one-sentence framing of `op` as the 1Password CLI for inclusivity (other password-manager CLIs work the same way).

**Status**: ✅ resolved (chapter 12 ↔ shipped binary consistent post-integrator-refold)

## Issue 3 (HIGH — chapter 17 `roksbnkctl destroy` + `--auto-approve` flag) — resolved by architect fold

**File**: `book/src/17-execution-backends.md:329-338`

Architect replaced `roksbnkctl destroy` with `roksbnkctl down` and `--auto-approve` with `--auto` in the "Supported commands" block + the follow-on prose. Added a parenthetical explaining `--auto` is roksbnkctl's shorthand for terraform's `-auto-approve`. Repo-wide grep confirms zero `roksbnkctl destroy` or `--auto-approve` references remain (other matches of `auto-approve` are legitimate: narrative `auto-approved` participle, terraform CLI demos showing terraform's own `-auto-approve` flag, and the shorthand-explanation paragraph itself).

**Status**: ✅ resolved (chapter 17 ↔ shipped binary consistent)

## Issue 4 (HIGH — chapter 28 `--refresh-kubeconfig` flag) — resolved by architect fold

**File**: `book/src/28-configuration-reference.md:16`

Architect replaced `roksbnkctl init --refresh-kubeconfig` with `roksbnkctl kubeconfig --download` in the workspace-config metadata table's "Updated by" cell. Repo-wide grep confirms zero `refresh-kubeconfig` references remain in `book/src/`.

**Status**: ✅ resolved (chapter 28 ↔ shipped CLI surface consistent; Sprint 6 chapter-26 resolution now also holds for chapter 28)

## Issue 5 (MEDIUM — stale `state/terraform.tfvars.user` path) — resolved by architect fold

**Files**: `book/src/10-deploying-bnk-trials.md:153`, `book/src/12-workspace-config.md:267`, `book/src/14-credentials-resolver.md:210`

Architect search-and-replaced `state/terraform.tfvars.user` → `terraform.tfvars.user` in all three chapters. The file is a sibling of `config.yaml`, not inside `state/`, per `internal/tf/terraform.go::UserTFVarsPath`. Repo-wide grep confirms zero `state/terraform.tfvars.user` references remain.

**Status**: ✅ resolved (chapters 10/12/14 ↔ `internal/tf/terraform.go::UserTFVarsPath` consistent)

## Issue 6 (MEDIUM — 5 broken cross-link anchors) — resolved by architect fold

**Files**: `book/src/17-execution-backends.md:243` (→ ch.14), `book/src/26-troubleshooting.md:196` (→ ch.21), `book/src/26-troubleshooting.md:241` (→ ch.25), `book/src/30-glossary.md:208` (→ ch.26), `book/src/preface.md:48` (boilerplate placeholder)

Architect fixed all 5: stripped the `-forward-look` suffix from the ch.14 link; updated the ch.21 link to `#the---gslb-compare-workflow` (three dashes per GFM slugger); updated the ch.25 link to `#worked-example-rotating-cos-supply-chain-assets`; extended the ch.26 link to the full slug ending `…-resources-lbs-security-groups-vpes`; replaced the preface boilerplate `./NN-slug.md` with a concrete `./07-quick-start.md` example.

**Status**: ✅ resolved (5 of 5 anchors resolve post-fold)

## Issue 7 (MEDIUM — search-index canonical-query miss-routes) — partially resolved by architect fold; rest deferred to v1.x

11 of 15 canonical queries miss-route (mostly to auto-generated reference chapters 27/29). Architect folded 5 cheap lead-in tweaks for v1.0:

- Chapter 22 — added `iperf3 north-south` to the lead-in
- Chapter 25 — added `cos object put` to the lead-in
- Chapter 17 — added `terraform via docker` to the lead-in
- Chapter 16 — strengthened `--on jumphost` framing in the lead
- Chapter 30 — confirmed `TOFU` is the entry header (no change needed)

6 structural miss-routes (`kubeconfig`, `cluster register`, `init --upgrade-tf`, etc.) deferred to v1.x — lunr's title-weighted ranking won't shift without chapter-title renames, which is a heavier-than-launch lift. CHANGELOG.md §"Deferred (v1.x roadmap)" lists "search-index canonical-query relevance polish" per tech-writer Issue 9 recommendation.

**Status**: ✅ resolved cheap subset; v1.x roadmap for structural rest

## Issue 8 (LOW — chapter 22 `--streams` ambiguity) — resolved by architect fold

**File**: `book/src/22-throughput-testing.md:186`

Architect reworded the table row to make clear the stream count is a server-pod knob (iperf3's own), not a roksbnkctl CLI flag.

**Status**: ✅ resolved (chapter 22 prose accurate; no flag promise the binary doesn't deliver)

## Verification of validator's deliverables

- **Phase I + Phase M + Phase N coverage**: Sprint 6 wired full coverage; Sprint 7 validator re-ran DRY_RUN walkthroughs against the integrated state. Both `scripts/e2e-test-backends.sh` and `scripts/e2e-test-full.sh` emit cleanly with all phases listed; final green-line emitted in both.
- **`e2e-full.yml` preflight fail-fast** (Sprint 6 Issue 5 carry-over): landed. Documented in `docs/E2E_TEST.md` §"CI preflight". Fails-fast on missing `IBMCLOUD_API_KEY` / `E2E_TFVARS_CONTENT` secrets; optional `E2E_SSH_*` secrets stay optional.
- **`cspell.json` Sprint 7 vocabulary**: added foreword/preface/swimlane/yak-shaving/VPEs and other Sprint 7 polish-pass terms.
- **Full test suite as v1.0 regression gate**: `go build / test / vet / gofmt ./...` all green; 14 Go packages pass tests including `TestProbe_TruncatedFlag` (Sprint 6 carry-over) and the new `TestVersionCmd_OutputShape` + `TestDocsURL_Value`.
- **Cross-link audit**: 353 internal links scanned; 5 broken anchors found and filed (Issue 6); architect folded all 5.
- **Search-index spot-check**: 15 canonical queries run against `book/book/searchindex.json`; results filed in Issue 7.

## Integrator additions

- Verified post-architect-fold grep returns zero hits for all 6 stale tokens (`--keep-cluster`, `--api-key-stdin`, `--auto-approve`, `--refresh-kubeconfig`, `state/terraform.tfvars.user`, `roksbnkctl destroy`).
- Re-folded `--auto` on `roksbnkctl init` in chapters 12 and 26 after tech-writer's review caught it (the architect's fold of Issue 2 had introduced it as a substitute for `--api-key-stdin`, but `--auto` doesn't exist on `init` either — only on `up` / `apply` / `down`).
- Re-ran `go build / test / vet / gofmt ./...` post-fold-pass — all green.

## Summary

8 issues filed; 7 fully resolved (4 high non-existent-flag fixes + 3 medium path/anchor/lead-in fixes), 1 partially resolved (Issue 7 search-index — cheap subset folded, structural rest deferred to v1.x). Zero build/test regressions; zero DRY_RUN regressions. `e2e-full.yml` preflight fail-fast landed (Sprint 6 carry-over closed).

**v1.0 regression-gate verdict**: no blockers. Integrator can cut the tag after running the pre-tag-cut steps documented in `resolved_sprint7_staff.md` (CHANGELOG date fill, `goreleaser check`, `mdbook-mermaid install book/`).
