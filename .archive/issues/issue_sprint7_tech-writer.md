# Sprint 7 — tech-writer issues

Sprint 7 is the **v1.0 launch-readiness sign-off**. Tech-writer is read-only;
issues below are filed for the integrator (or the architect, where the file
is in `book/src/`) to resolve before the v1.0 tag is cut.

Scope walked end-to-end: all 32 chapters + preface read for voice / audience
/ placeholders; 7 worked-example walkthroughs (ch 7, 11, 12, 18, 21, 25, 32)
quality-assessed; 3 Mermaid diagrams (ch 7 lifecycle, ch 17 architecture,
ch 21 GSLB) + ch 18 backend matrix (markdown table) reviewed; README +
CHANGELOG + version-output read-verified against `internal/cli/meta.go` and
`internal/cli/root.go`; PRD 05 §"Phase I" (I0-I11) + §"Phase N" (N1-N6)
cross-checked against `scripts/e2e-test-backends.sh`; dogfooding-simulation
walkthrough of `preface → ch.7` done from a first-time-reader perspective;
v1.0 audit run against PLAN.md §"v1.0 (M4)" gate criteria; cross-document
drift sweep across README ↔ book ↔ CHANGELOG ↔ PLAN ↔ MIGRATING.

Verification baseline (read-only, integrated-state):

- `go build ./...`, `go vet ./...`, `gofmt -d -l .`, `go test ./...` — all green (cached). All 14 packages pass including the new `TestVersionCmd_OutputShape` + `TestDocsURL_Value`.
- `grep -nrE "coming in sprint|coming soon|TBD|TODO" book/src/` — zero hits (placeholder-free).
- `grep -nrE '(--keep-cluster|--api-key-stdin|--auto-approve|--refresh-kubeconfig|state/terraform\.tfvars\.user|roksbnkctl destroy)' book/src/` — zero hits (architect's validator-fold landed).
- 32 chapters listed in `SUMMARY.md`; 34 .md under `book/src/` (32 chapter files + `preface.md` + `SUMMARY.md`).
- `book/book.toml` has `[preprocessor.mermaid]` + `additional-js = ["mermaid.min.js", "mermaid-init.js"]`; `git-repository-url` points at the repo.

## Issue 1 (HIGH — `roksbnkctl init --auto` flag does not exist; documented in ch.12 + ch.26)

**Severity**: high (chapter documents a flag the binary doesn't expose; same class as architect-folded validator Issue 2)

**Status**: resolved by integrator fold (see `resolved_sprint7_tech-writer.md`)

**Files**:
- `book/src/12-workspace-config.md:333` — `IBMCLOUD_API_KEY=$(op read 'op://...') roksbnkctl init -w dev --auto`
- `book/src/26-troubleshooting.md:22` — `IBMCLOUD_API_KEY=$(cat /path/to/secret) roksbnkctl init --auto -w my-workspace`

**Description**: Two worked-example / troubleshooting snippets pass `--auto` to `roksbnkctl init`. Per `book/src/27-command-reference.md:407-427` (auto-generated from cobra), `init` only exposes `--tf-source` and `--upgrade-tf`. Running either documented command fails with `Error: unknown flag: --auto`.

The ch.12 instance was introduced by the architect's fold of validator Issue 2 (the env-var-prefixed replacement for the non-existent `--api-key-stdin` form). The architect's resolution prose for validator Issue 2 in `issue_sprint7_architect.md` shows the intended replacement command without `--auto`, suggesting the `--auto` token slipped in at edit time rather than by design.

The ch.26 instance has been in the chapter from earlier sprints; validator's chapter sweep missed it because the prose looks plausible at a glance (`init` reads like it should support `--auto`).

**Fix**: drop the `--auto` tokens. The non-interactive bootstrap is already gated on `IBMCLOUD_API_KEY` being set — when the env var is populated, `init` resolves the key from env and only prompts for the remaining workspace metadata (region, RG, cluster name, etc.). For a fully non-interactive CI bootstrap, the architect / integrator should either (a) flag this as a v1.x feature to add an `--auto` flag to `init`, or (b) pre-populate every prompt's answer via env vars / a `--config` flag. Recommend (a) since the CI use case is real, but for v1.0 the chapters must not advertise a flag that doesn't exist — strip the `--auto` and add a note that fully-non-interactive init lands in v1.x.

## Issue 2 (HIGH — README pointer link to chapter 7 uses stale slug `07-first-deploy.html`)

**Severity**: high (broken cross-link from the README — the canonical entry point — to the book's flagship quick-start chapter; a 404 is a first-impression failure for the v1.0 launch)

**Status**: resolved by integrator (see `resolved_sprint7_tech-writer.md` Issue 2)

**File**: `README.md:76`

**Description**: The "Pointers" block reads:

> **Book** — <https://jgruberf5.github.io/roksbnkctl/book/> — canonical user documentation; start at the preface or jump to [Chapter 7](https://jgruberf5.github.io/roksbnkctl/book/07-first-deploy.html) for the deploy walkthrough.

The slug `07-first-deploy.html` is stale — the actual filename (per `book/src/SUMMARY.md:16`) is `07-quick-start.md`, which mdBook renders to `07-quick-start.html`. Clicking the README link from the GitHub repo page lands the reader on a 404.

The earlier README mention on line 33 already uses the correct slug (`07-quick-start.html`), so this is a single-line drift rather than a systematic issue. Likely a copy-paste leftover from an earlier draft where the chapter was titled "First deploy" before being renamed.

**Fix**: replace `07-first-deploy.html` with `07-quick-start.html` on README line 76.

## Issue 3 (HIGH — chapter 4 version-output sample uses `Book:` prefix; binary emits `Docs:`)

**Severity**: high (the chapter documents the user-visible output shape of a v1.0 launch feature; the literal string mismatch is a first-impression failure when a reader runs `roksbnkctl version` to confirm the install)

**Status**: resolved by integrator fold (see `resolved_sprint7_tech-writer.md`)

**File**: `book/src/04-installation.md:134`

**Description**: The "Verifying the install" section reads:

```
roksbnkctl v1.0.0 (commit abc1234, built 2026-05-10T14:22:08Z)
Book: https://jgruberf5.github.io/roksbnkctl/book/
```

The actual binary output (per `internal/cli/meta.go:31`, pinned by `internal/cli/meta_test.go::TestVersionCmd_OutputShape`) emits `Docs: ` as the prefix, not `Book: `. The constant of record is `internal/cli/meta.go::DocsURL`; a reader copy-pasting the chapter-4 sample as the expected output spec would see a mismatch.

Staff agent picked `Docs:` over `Book:` for the constant + test name (DocsURL, not BookURL); architect's chapter 4 polish used `Book:` — the two agents diverged on the literal label. Source-of-truth is the shipped binary.

**Fix**: change `Book:` to `Docs:` in `book/src/04-installation.md:134`. The line should read `Docs: https://jgruberf5.github.io/roksbnkctl/book/`.

## Issue 4 (MEDIUM — chapter 11 worked-example claims `down` destroys cluster after `cluster register`; logically contradictory)

**Severity**: medium (the walkthrough's terminal step is logically inconsistent with its setup; a reader following the example end-to-end would either (a) see a different terraform-destroy plan than the chapter shows or (b) accidentally tear down a cluster they didn't provision)

**Status**: resolved by integrator fold (see `resolved_sprint7_tech-writer.md`)

**File**: `book/src/11-tearing-down.md:201-235`

**Description**: The "register an existing cluster, deploy BNK, tear down" walkthrough sets up the scenario at step 2 with `roksbnkctl cluster register existing-bnk-cluster -w preexisting` — the cluster was provisioned outside roksbnkctl, only the registration metadata enters the workspace. Step 6 then runs `roksbnkctl down --auto -w preexisting` and the expected-output comment reads:

```
# 6. Tear down — destroys BNK AND the cluster
roksbnkctl down --auto -w preexisting
# Expected:
#   → terraform destroy (auto-approved)
#   Destroy complete! Resources: 77 destroyed.
```

Two issues:
1. **"destroys BNK AND the cluster"** contradicts the setup. `cluster register` is the discovery-only path — terraform state holds the BNK overlay modules (`cert_manager`, `flo`, `cne_instance`, `license`) and the `testing` jumphost, but **not** the `roks_cluster` module (the cluster pre-existed). `roksbnkctl down` over this state destroys only what terraform knows about. The pre-existing cluster (not in state) survives.
2. **"77 resources destroyed"** is the from-scratch count (cluster + VPC + jumphost + overlay). The register-then-up path leaves the cluster out of state, so the destroy resource count is closer to ~30-40 (overlay modules + jumphost only). The literal "77" is wrong for this scenario.

The post-walkthrough prose at line 237 (architect's fold of validator Issue 1) now correctly describes the cluster-vs-trial separation as `down`'s default behaviour. The walkthrough's terminal-step comment-block needs to align with that — `down` here destroys the trial + jumphost only, not the registered cluster.

**Fix**: rewrite the step-6 comment block so the "destroys BNK AND the cluster" line says "destroys the BNK overlay; the registered cluster (provisioned outside roksbnkctl) survives", and bring the resource count down from 77 to a representative number (or drop the literal count and use "Destroy complete! Resources: N destroyed" so the example doesn't pin a misleading specific count). Add one follow-on sentence pointing readers at chapter 8 (`roksbnkctl cluster down` is what tears down a registered cluster's underlying terraform state, IF the user wants that — but in the register-then-deploy scenario the cluster pre-existed, so it's not under roksbnkctl's destroy scope at all).

## Issue 5 (MEDIUM — CHANGELOG v0.9 §"Sprint 5" documents non-existent `roksbnkctl destroy` command and `--backend docker` for it)

**Severity**: medium (the CHANGELOG is a v1.0-launch surface that ships with the GitHub Release page; documenting a flag/command that doesn't exist is a first-impression failure for a reader checking what changed since v0.7)

**Status**: resolved by integrator (see `resolved_sprint7_tech-writer.md` Issue 5)

**File**: `CHANGELOG.md:25`

**Description**: The Sprint 5 entry under v0.9 reads:

```
- **Terraform via docker** (`roksbnkctl up/plan/apply/destroy --backend docker`)
```

`roksbnkctl destroy` is not a command — the destroy verb is `roksbnkctl down` (the same divergence validator Issue 3 caught in chapter 17, which architect folded). The CHANGELOG entry was written before the chapter-17 fold and didn't get swept.

The follow-on bullets in the same Sprint 5 block don't repeat the `destroy` token, so this is one literal occurrence. Cross-checked: README + book/src/* are clean post-architect-fold; only CHANGELOG carries the stale form.

**Fix**: replace `up/plan/apply/destroy` with `up/plan/apply/down` on CHANGELOG.md:25. Optional consistency tweak — confirm the architect's fold of validator Issue 3 ("--auto-approve" → "--auto") didn't leak the same drift elsewhere in CHANGELOG (spot-checked — no other instances).

## Issue 6 (MEDIUM — chapter 7 framing "3-command happy path" inconsistent with the chapter's own 4-command lifecycle diagram + 7-step body)

**Severity**: medium (a reader's first impression of the flagship quick-start chapter is that the introduction undercounts the steps it then proceeds to walk through; the framing "3-command happy path" is also repeated in README.md, internal/cli/root.go's `Long:` description, and the CHANGELOG's v1.0 intro paragraph — so the inconsistency surfaces across multiple v1.0 launch surfaces)

**Status**: resolved by integrator fold (see `resolved_sprint7_tech-writer.md` Issue 6) — aligned 7 surfaces to "4-command lifecycle"

**Files**:
- `book/src/07-quick-start.md:3` — "walks the 3-command happy path end-to-end"
- `book/src/07-quick-start.md:7-33` — lifecycle Mermaid diagram explicitly shows `init / up / test / down` (four commands)
- `book/src/07-quick-start.md:55-247` — chapter body has 7 numbered steps (1. set API key; 2. init; 3. up; 4. status; 5. test; 6. explore; 7. down)
- `README.md:33` — "That's the 3-command happy path"
- `CHANGELOG.md:87` — "a 3-command happy path (`init` → `up` → `test`)"
- `internal/cli/root.go:47` — `Long:` description: "The 3-command happy path: roksbnkctl init / up / test"

**Description**: The "3-command" framing across the v1.0 launch surfaces names `init` / `up` / `test`. But the canonical lifecycle the rest of the book teaches (and the chapter-7 Mermaid diagram visualises) is `init` / `up` / `test` / `down` — four commands. Dropping `down` from the framing makes a reader who follows the chapter feel like they've already "done it" after step 5, but the chapter then introduces `down` as step 7 without explaining why it wasn't in the count.

This isn't a bug — `init/up/test` is the *deployment* happy path; `down` is the cleanup verb that the user runs once they're done evaluating. But the framing should match what the chapter actually teaches end-to-end. Three options:

1. **Stay with "3-command happy path"** — explicitly explain that `down` is the cleanup verb that closes the loop, not a separate step in the deploy walkthrough. Then the framing of `down` as step 7 needs a callout (e.g., "Step 7 — `roksbnkctl down --auto` *(when you're done evaluating)*").
2. **Flip to "4-command lifecycle"** everywhere — chapter 7 intro, README, CHANGELOG, root.go's `Long:`. Matches the Mermaid diagram and the chapter body's actual surface.
3. **Keep the 3-command framing AND drop `down` from chapter 7** entirely — move tear-down into a follow-on call-out box. Probably wrong: a reader who finishes chapter 7 without knowing how to clean up the IBM Cloud spend they just incurred is a real-money problem.

Recommend option (2) for the launch — "4-command lifecycle" matches the Mermaid diagram, the chapter body, and the actual user mental model.

**Fix**: pick one of the three options above; update all 5 surfaces consistently. If option (2), the changes are 1-token edits in each file.

## Issue 7 (LOW — preface line 48 cross-reference example "[Chapter 7 — Quick start](./07-quick-start.md)" uses a different inline-link shape than the rest of the book teaches)

**Severity**: low (the convention-example in the preface uses an em-dash; many other inline cross-references in the chapters use a colon or just the chapter title, so the "convention" line is descriptive of one option rather than the universally-followed pattern)

**Status**: resolved by integrator fold (see `resolved_sprint7_tech-writer.md`)

**File**: `book/src/preface.md:48`

**Description**: The "Book conventions" section reads:

> **Cross-references**: every chapter ends with a "Cross-references" section linking related chapters. Inline links use the form `[Chapter 7 — Quick start](./07-quick-start.md)` — a chapter number, an em-dash, the chapter title, and the relative path to the chapter source.

A repo-wide check of inline links shows the actual pattern is mixed:

- `[Chapter N — Title](./NN-slug.md)` — used by most chapters (matches the preface convention).
- `[Chapter N §"Section"](./NN-slug.md#anchor)` — used heavily in chapters 14, 17, 21 where sub-section links are common.
- `[Chapter N](./NN-slug.md)` — chapter-only inline link without the title; used in ~30% of cross-references where the chapter has been named earlier in the paragraph.

The preface convention covers the "long form" but doesn't acknowledge the other two patterns. A first-time reader scanning the conventions and then hitting a `[Chapter 14 §"…"](…)` reference in chapter 12 might wonder if the convention has been violated. It hasn't — the third form is the section-anchor extension and the second form is the contextual short-form.

**Fix**: extend the convention paragraph to call out the three shapes explicitly:

> Inline cross-references use one of three shapes, depending on what the surrounding prose needs:
>
> - `[Chapter 7 — Quick start](./07-quick-start.md)` — chapter-level link with the full title (used when introducing a chapter for the first time in the paragraph).
> - `[Chapter 14 §"The IBMCLOUD_API_KEY resolver chain"](./14-credentials-resolver.md#the-ibmcloud_api_key-resolver-chain)` — section-anchor link to a specific heading within a chapter (used when the section is the relevant pointer, not the whole chapter).
> - `[Chapter 7](./07-quick-start.md)` — bare chapter-number link (used when the chapter has been named earlier in the same paragraph).

This is a polish item, not a v1.0 blocker.

## Issue 8 (LOW — chapter 12 line 333 worked-example uses 1Password syntax `op read 'op://...'` without mentioning that `op` is the 1Password CLI)

**Severity**: low (a reader who isn't a 1Password customer would assume `op` is a roksbnkctl-defined verb and try to find it; the example would be more inclusive if it explicitly mentioned the tool)

**Status**: resolved by integrator fold (see `resolved_sprint7_tech-writer.md`)

**File**: `book/src/12-workspace-config.md:333`

**Description**: The worked-example shows:

```
IBMCLOUD_API_KEY=$(op read 'op://Private/IBM Cloud/api-key') roksbnkctl init -w dev --auto
```

`op` is the 1Password CLI (`brew install 1password-cli`); the `op://...` URI is 1Password's secret-reference scheme. Neither is roksbnkctl-specific and neither is introduced in the preface or in chapter 12's prose. A reader who uses a different password manager (Bitwarden's `bw`, LastPass's `lpass`, AWS Secrets Manager's `aws secretsmanager`, gopass, etc.) would have to mentally translate the example. A reader who's never seen `op` would briefly think it's a roksbnkctl command.

The framing is salvageable with one sentence: "(1Password CLI users can fetch the key via …; Bitwarden / gopass / Doppler users adapt similarly — the env-var is what roksbnkctl reads, not the source command)".

**Fix**: prepend a one-sentence framing to the code block explaining what `op` is and that the pattern works with any password-manager CLI that can print a secret to stdout. Three-line tweak; no structural change.

Note: this is the same code block that carries Issue 1's stale `--auto` flag. The architect can fold both edits together in one chapter-12 pass.

## Issue 9 (ROADMAP — search-index canonical-query miss-routes deferred to v1.x; flag at tag-cut)

**Severity**: roadmap (validator Issue 7 ran the canonical-query spot-check; 11/15 queries miss-route, mostly to ref chapters 27/29; architect folded 5 of the cheap subset via lead-in tweaks but deferred 6 structural ones)

**Status**: deferred to v1.x (CHANGELOG entry in place; see `resolved_sprint7_tech-writer.md` Issue 9)

**Files**: applies across chapters 5/6/11/14/27/29 via prose distribution

**Description**: Validator Issue 7 identified 11 search-index miss-routes (canonical queries like `kubeconfig`, `init --upgrade-tf`, `cluster register`, `cos object put`, `iperf3 north-south`, `terraform via docker`, `on jumphost`, `OpenShift SCC`, etc.). Architect folded 5 cheap fixes via lead-in prose tweaks. The remaining 6 are structural — lunr's title-weighted ranking won't shift without renaming chapter titles, which is a heavier-than-launch lift.

A v1.x effort to re-rank could either:

1. **Rename chapter titles** to surface canonical search terms (e.g., chapter 22 "Throughput testing" → "Throughput testing (iperf3 north-south + east-west)"); cheap edits, lunr title-weight wins immediately.
2. **Add a custom `book.toml::output.html.search.boost`** map per term — not natively supported by mdBook; would need a fork or a post-build re-index step.
3. **Add per-chapter `<meta>` keywords** via mdBook's HTML preprocessor extension — feasible but speculative ROI.

Recommend (1) as the v1.x effort: chapter-title renames are reversible, immediate-effect, and align titles with the search query space dogfooders actually use.

**Fix**: defer to v1.x; document in CHANGELOG.md §"Deferred (v1.x roadmap)" if not already present (spot-check: it isn't — only signing/PDF/Homebrew/state-backends/Truncated/cluster-sharing/apt-bootstrap/Windows/F5-theming are listed; add a "Search-index canonical-query relevance polish" bullet).

## Issue 10 (ROADMAP — `--truncated` user-facing CLI flag carry-over from Sprint 6 validator Issue 6)

**Severity**: roadmap

**Status**: deferred to v1.x (CHANGELOG entry in place; see `resolved_sprint7_tech-writer.md` Issue 10)

**File**: `internal/test/dns.go` + the DNS-probe chapter (21) reference table

**Description**: The DNS probe internally distinguishes UDP-truncated answers (TC=1) from TCP-completed answers; the test `TestProbe_TruncatedFlag` (`internal/test/dns_test.go`) pins the TC=1 → TCP-retry projection through the schema. A user-facing CLI flag (e.g. `roksbnkctl test dns --require-untruncated`) was scoped in Sprint 6 validator Issue 6 as the natural follow-on for users who want to assert "the answer fit in one UDP packet" as part of a CI check.

Not landing in v1.0. The internal behaviour is already correct; the user-facing surface is the deferred polish.

**Fix**: defer to v1.x; already implicitly in PLAN.md §"What's deliberately deferred to post-v1.0". CHANGELOG.md does call out `--truncated` in its "Deferred (v1.x roadmap)" block (line 133). No tech-writer action.

## Issue 11 (ROADMAP — cross-driver cluster-sharing for `e2e-test-full.sh` carry-over from Sprint 6 validator Issue 4)

**Severity**: roadmap

**Status**: deferred to v1.x (CHANGELOG entry in place; see `resolved_sprint7_tech-writer.md` Issue 11)

**File**: `scripts/e2e-test-full.sh`, `docs/prd/05-E2E-TEST-PLAN.md` §"Test infrastructure"

**Description**: `e2e-test-full.sh` currently chains the baseline driver and the backends driver; each driver does its own cluster apply. The PRD-envisioned design has them share a single cluster apply (~50 min saved per wall-time-budgeted CI run). Chapter 23 line 17 explicitly calls out this deferral; PRD 05 §"Test infrastructure" similarly. Already in CHANGELOG.md "Deferred (v1.x roadmap)" (line 134).

**Fix**: defer to v1.x; no tech-writer action.

## Issue 12 (ROADMAP — mdbook-mermaid integrator step gated on `mdbook-mermaid install book/`)

**Severity**: roadmap (handoff to integrator at v1.0 dispatch time)

**Status**: handed off to integrator (see `resolved_sprint7_tech-writer.md` Issue 12)

**File**: `book/book.toml`, `.github/workflows/book.yml`

**Description**: `book/book.toml` declares the `[preprocessor.mermaid]` block + `additional-js = ["mermaid.min.js", "mermaid-init.js"]` but the `mermaid.min.js` / `mermaid-init.js` assets aren't committed yet. The integrator runs `cargo install mdbook-mermaid && mdbook-mermaid install book/` once during the v1.0 dispatch so the assets land alongside `book.toml`; the assets then commit alongside the v1.0 tag. After the one-time install, no further action is needed.

Tech-writer could not verify locally — `mdbook` is not on the sprint VM PATH. Architect already filed this as Issue 1; restating here so it surfaces in the v1.0-readiness audit.

**Fix**: integrator runs the install command once during the v1.0 dispatch.

## Dogfooding-simulation walkthrough

Read `book/src/preface.md` → `book/src/07-quick-start.md` linearly, taking the first-time-reader perspective. The simulation is read-only — no IBM Cloud account on the sprint VM — but the surface every chapter-7 step shells out to (the `roksbnkctl` binary itself) was cross-verified against the source code.

**Stuck-points surfaced**:

- **Issue 6** (chapter 7 intro frames "3-command happy path" but the chapter walks 7 numbered steps + a 4-command lifecycle diagram) — a first-time reader will pause at step 4 (`status`) wondering "wait, was I supposed to count this one?" and again at step 6 (optional `explore`). Real friction; would survive a real dogfood.
- **Issue 3** (chapter 4 version-output spec uses `Book:` but binary emits `Docs:`) — a reader who reads chapter 4 first (the install verification step), then runs the binary, sees a literal mismatch. Would file a "the docs are out-of-date" bug rather than push through.
- **Issue 1** (chapter 12 + 26 use `roksbnkctl init --auto` which errors `unknown flag`) — chapter 26 specifically is the troubleshooting chapter; a CI user reaching for the documented non-interactive form gets stuck immediately. Real-money problem if the user is on a CI clock.

**Stuck-points NOT surfaced** (good news):

- Chapter 7 prerequisites (line 35-41) honest about what's required (terraform on PATH + IBM Cloud API key + doctor green); no over-promising.
- Chapter 7 each step's expected-output blocks are realistic (ROKS cluster creation is correctly characterised as ~30-40 minutes per line 134, not "instant"; the abridged-log framing at line 97 is honest).
- Cross-link from preface to chapter 7 (preface.md:27) is present and resolves.
- Chapter 7 step 7 (`down`) closes the cost-incurring loop; a reader won't accidentally leave a $$$ ROKS cluster running.
- Install paths (chapter 4) are honest about each option's prerequisites; the Docker-build fallback for older-Go hosts is concretely walked through.

**Disk-size estimate carry-over** (architect Issue 2): the tech-writer dogfood didn't surface a different number than 200 MB. Architect's chapter 23 line 69 estimate stays unchanged; no integrator action.

## v1.0 launch-readiness audit against PLAN.md §"v1.0 (M4)" gate criteria

Seven gate criteria from `docs/PLAN.md:524-532`:

| # | Criterion | Verdict | Notes |
|---|---|---|---|
| 1 | All E2E phases (A-H + I-N + L-DNS) pass on a clean test host | **TBD by integrator** | The live e2e run is integrator-scope at tag-cut time; validator's `DRY_RUN=1` walkthroughs are clean (per `issue_sprint7_validator.md` baseline). |
| 2 | All previous sprints' acceptance criteria still hold (no regressions) | **met** | `go build` / `vet` / `gofmt` / `test ./...` clean (14 packages); `TestHasFailures_StockDevBoxGreen` cached green; `TestVersionCmd_OutputShape` cached green. Zero validator regression flags. |
| 3 | Cred audit clean (Phase M) | **TBD by integrator** | The live run hasn't happened. M1-M7 step matrix in PRD 05 is the integrator's gate. |
| 4 | Doctor green-by-default on a stock dev box (terraform only required) | **met** | Pinned by `internal/doctor/doctor_test.go::TestHasFailures_StockDevBoxGreen` (line 128) — cached green. |
| 5 | Book published, all 32+ chapters complete, dogfooded by ≥1 external user, no placeholders, code examples verified | **partially met** — book content is launch-ready (32 chapters + preface + 11 worked-example sections + 3 Mermaid diagrams + ch.18 backend matrix + zero placeholders); validator's chapter-sweep verified the code examples; tech-writer's dogfood simulation surfaced 6 issues (3 high, 2 medium, 1 low) for architect to fold. **The "dogfooded by ≥1 external user" requirement is TBD by integrator** — the simulation here is not a substitute for a real-IBM-Cloud-account run. **The GitHub Pages publish is TBD by integrator at tag-cut**. |
| 6 | Release artifacts (binaries, checksums, optional PDF book) attached to the GitHub release | **TBD by integrator** | `.goreleaser.yml` is finalised (staff agent); `goreleaser check` deferred to integrator (staff Issue 2); PDF book deferred to v1.x (staff Issue 4). Signing also deferred to v1.x (staff Issue 3). |
| 7 | README links to the book; book links back to the repo | **met** (modulo Issue 2 above) — README links to the book at the top of README.md (line 3) and in the Pointers block (line 76, but with the broken `07-first-deploy.html` slug — Issue 2 above). The book's `book.toml::git-repository-url` points back at the repo. Fix Issue 2 and this criterion is fully met. |

**Overall verdict**: **TBD by integrator at tag-cut**. Two criteria (1, 3) require live runs that only the integrator can execute. One criterion (6) requires `goreleaser release --snapshot` against the final pre-tag branch. The remaining four (2, 4, 5, 7) are met or partially met — and the partial-met for (5) is resolved by architect folding the 3 high-severity issues from this file (Issues 1, 2, 3 in particular). No **blocker**-severity issue prevents the integrator from cutting the v1.0 tag, but the 3 high-severity issues (1, 2, 3) should land before the tag fires to avoid first-impression failures.

## Cross-document drift sweep

Spot-checked across the seven v1.0 launch surfaces:

- **`docs/PLAN.md` §"Sprint 7 Documentation deliverables" rows 1-8** — match what landed (polish pass; diagrams; foreword; worked-example walkthroughs; internal cross-linking review; search-index spot-check via validator; dogfooding loop via this file; launch-announcement prep via README + CHANGELOG).
- **`docs/PLAN.md` §"Per-sprint book chapters" Sprint 7 row** — "(polish only — diagrams, cross-links, foreword) → **all chapters launch-ready**" — accurate.
- **`docs/prd/05-E2E-TEST-PLAN.md` §"Phase I"** — lists I0-I11 (12 steps), matches `scripts/e2e-test-backends.sh::phase_I`. Architect refresh closed Sprint 6 tech-writer Issue 12 carry-over. **Verified**.
- **`docs/prd/05-E2E-TEST-PLAN.md` §"Phase N"** — lists N1-N6 (6 steps), matches `scripts/e2e-test-backends.sh::phase_N`. Architect refresh complete. **Verified**.
- **`book/src/SUMMARY.md`** — 32 chapters listed; h1 titles match SUMMARY entries (spot-checked ch.1, 7, 17, 21, 27, 32).
- **README ↔ book ↔ CHANGELOG ↔ PLAN ↔ MIGRATING** — consistent on the v1.0 narrative (terraform-only prereq, book-as-canonical-docs framing, 32 chapters, sprint-6+7 close-out). Drift surfaced separately in Issues 2 (README chapter-7 slug), 3 (chapter-4 version label), 5 (CHANGELOG `destroy` token), 6 ("3-command" vs "4-command" framing).
- **`MIGRATING.md`** — v0.7/v0.8/v0.9/v1.0 column matches; spot-check confirms no drift since Sprint 6's pinning (per staff's Issue 7-equivalent check in `issue_sprint7_staff.md`).

## Summary

- **Files reviewed**: 32 book chapters + preface; README; CHANGELOG; MIGRATING; PRD 05; PLAN.md §"v1.0 (M4)"; `internal/cli/{meta,root}.go` + `meta_test.go`; `book/book.toml`; the four `issue_sprint7_*.md` files. ~50 files end-to-end.
- **Issues filed**: 12 total — **3 high** (Issues 1-3: invalid `init --auto` flag in ch.12+26, broken README slug, version-output label drift), **3 medium** (Issues 4-6: walkthrough logic, CHANGELOG `destroy`, "3-command" framing), **2 low** (Issues 7-8: preface convention nuance, password-manager framing), **4 roadmap** (Issues 9-12: search-index polish, `--truncated` flag, cluster-sharing, mdbook-mermaid install).
- **Top 3 noteworthy observations not filed as issues** —
  1. Voice consistency between Sprint-2-3 chapters (5-15) and Sprint-5-6 chapters (20-32) holds well after architect's polish pass; tone is uniformly clipped, code-block-heavy, sentence-case-headers. Gold-standard chapters 17 and 21 still set the bar.
  2. The architect's fold of validator's 8 chapter issues landed cleanly — repo-wide grep for the 6 non-existent flag/command tokens returns zero hits.
  3. Worked-example walkthroughs (11 across chapters 8/9/11/12/13/18/20/21/24/25/32) are stylistically consistent (each uses an "End-to-end Part X scenario:" framing followed by a numbered bash block) — readers should be able to recognise a walkthrough section at a glance.
- **v1.0-readiness verdict**: **TBD by integrator at tag-cut**. Four of seven PLAN.md §"v1.0 (M4)" gate criteria are met (2, 4, 7, partially 5). Three are integrator-scope (1, 3, 6). **No blocker-severity issues** prevent the integrator from cutting the v1.0 tag, but the 3 high-severity issues filed here (Issues 1, 2, 3) should be folded before the tag fires — they're first-impression failures on the v1.0 launch surface.
