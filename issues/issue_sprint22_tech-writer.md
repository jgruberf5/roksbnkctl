# Sprint 22 — tech-writer issues (post-integration drift sweep)

> **Sprint 22 frame.** Tech-writer runs **after** the integrator
> has landed staff (shipped pre-dispatch in `18415eb` + `cbb9c1b`)
> + validator (`tools-images.yml` matrix fold-in) + architect
> (`CONTRIBUTING.md` CI-managed note) to `main` and run gates.
> Drift sweep across five surfaces + GREEN/RED launch verdict
> with the deferred DetectShape live verify and the Sprint 23
> release-gate explicitly called out.

**Status**: resolved

---

## Issue 1 — Post-integration drift sweep for Sprint 22

**Severity**: medium (the down-prompt change is operator-visible
the first time anyone runs the composite `down`; the mdbook CI
fold-in is the proximate fix for the `v1.7.0` `make release-publish`
step-2 flakiness — drift on either surface is immediately felt)

**Status**: resolved

### Drift surfaces walked

1. `book/src/11-tearing-down.md` `§"--auto for non-interactive
   runs"` — quoted Split-shape prompt copy line-by-line against
   the binary's `Fprintf` format string.
2. Other `book/src/` chapters — grep for `down --auto` / `bnk down`
   / `cluster down` / `"This will destroy"`; verify any quoted
   prompt text matches the post-`18415eb` binary output. Chapter
   11's cluster-down ShapeSplit refusal at lines 97–101 cross-
   checked against `internal/cli/cluster_phase.go:373`.
3. `mdbook` CI matrix push verification — `tools-images.yml`'s
   `:dev` push of `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook` on
   the next post-integration `main` push, manifest SHA256 digest
   match against the workflow run log.
4. `CONTRIBUTING.md` + `docs/` + `Makefile` comments + `book/src/`
   + `tools/docker/` — stale "manually push the mdbook image"
   callouts vs. the new CI-managed reality.
5. `issues/issue_sprint22_staff.md` "Closure" claims vs. the
   diffs of `18415eb` + `cbb9c1b` — does the staff audit match
   what shipped?

### Closure — tech-writer, 2026-05-27

Post-integration drift sweep run against the landed tree at
commit `3624392` (`sprint22: three-way integration — mdbook CI
matrix + CONTRIBUTING.md update + staff closure audit`). The
staff scope was closure-only (both fixes shipped pre-dispatch
in `18415eb` + `cbb9c1b`); validator + architect closures audited
against their respective edits in the same integration commit.

**Findings summary**: 0 high, 1 medium (deferred CI verification
— integrator hasn't pushed to `origin/main` yet), 0 low. Two
follow-up candidates surfaced for the integrator (book chapter 31
drift, already flagged by the architect; Makefile line 107
recovery hint refinement, already flagged by the architect).
Both are non-blocking. Launch verdict GREEN with the explicit
Sprint-23 release-tag gate held per integrator decision 3.

---

#### Drift sweep #1 — `book/src/11-tearing-down.md` Split-shape prompt copy

**Verdict**: CLEAN — line-for-line match against the binary's
`Fprintf` format string.

Binary source-of-truth (Sprint 22 down-prompt fix landed in
`18415eb`):

```go
// internal/orchestration/lifecycle.go:332–339
if !in.Auto {
    fmt.Fprintf(os.Stderr,
        "This will destroy BOTH the BNK trial AND the cluster phase for workspace %q (ROKS + transit gateway + registry COS + cert-manager + jumphost).\n",
        cctx.WorkspaceName)
    if !in.PromptYesNo("Continue?", false) {
        return errors.New("aborted")
    }
    in.Auto = true
}
```

`%q` formats the workspace name as `"default"` (Go's `strconv.Quote`
shape with surrounding double-quotes).

Book quote at `book/src/11-tearing-down.md:172–175`:

```
$ roksbnkctl down
This will destroy BOTH the BNK trial AND the cluster phase for workspace "default" (ROKS + transit gateway + registry COS + cert-manager + jumphost).
Continue? [y/N]: 
```

Byte-for-byte match — same capitalisation on `BOTH` / `AND`,
same parenthesised resource list (`ROKS + transit gateway +
registry COS + cert-manager + jumphost`), same `"default"`
`%q`-quoted workspace name, same `Continue? [y/N]: ` prompt
from `PromptYesNo("Continue?", false)`. The three other quoted
prompts in the same section (LegacySingle `down` line 181,
`bnk down` line 187, `cluster down` line 193) similarly match
their respective binary sources at `internal/orchestration/lifecycle.go:369`,
`:849`, and `internal/cli/cluster_phase.go:383` line-for-line
(grep verified all four `"This will destroy"` strings: 1 in
chapter 11 prompt copy × 4 invocations, 4 in source × matching
verbs).

Staging a real Split workspace to capture stderr was attempted
(`mkdir /tmp/<staging>/state* && cp tfstate_split.json …`) and
gated by the sandbox — but the source-code grep is sufficient
proof: the Fprintf string is the binary's stderr verbatim (no
intermediate processing), and the `%q` quoting on `cctx.WorkspaceName="default"`
expands to `"default"` deterministically.

The refusal text at chapter 11 lines 97–101 (cluster-down
ShapeSplit refusal — separate code path) matches
`internal/cli/cluster_phase.go:373` byte-for-byte:

```go
return errors.New("BNK trial state exists in this workspace; run `roksbnkctl bnk down` first (or `roksbnkctl down` to tear down both phases)")
```

unchanged by Sprint 22 per the integrator decision (the
refusals are correctness guards, not confirmation prompts).

#### Drift sweep #2 — other book chapters quoting down-prompt copy

**Verdict**: CLEAN — no other chapter quotes Split-shape prompt
text.

Sweep across `book/src/` for `"This will destroy"`: 4 hits, all
in `book/src/11-tearing-down.md` (lines 173, 181, 187, 193).
Zero hits in any other chapter. Operator-visible command
references in other chapters (`book/src/06-workspaces.md:301`,
`book/src/07-quick-start.md:29, 238, 243, 249`,
`book/src/08-cluster-phase.md:219, 275`,
`book/src/09-registering-existing-cluster.md:198`,
`book/src/10-deploying-bnk-trials.md:247, 300, 322`,
`book/src/23-e2e-test-plan.md:135`,
`book/src/24-day-2-ops.md:431`,
`book/src/27-command-reference.md:59, 63, 68, 74, 126, 134, 139`)
are all command-line shape mentions (`roksbnkctl down --auto`,
`bnk down`, `cluster down`) — none quote the actual stderr
prompt copy that `18415eb` changed.

The closest near-miss is `book/src/10-deploying-bnk-trials.md:217–221`
which quotes a `Continue? [y/N]: y` line from `bnk up`'s
empty-workspace bootstrap path — a **different code path**
(orchestration's `RunBnkUp` empty branch), unchanged by Sprint
22 (`18415eb` touched only the `RunDown` Split branch + the
cli adapter mirror). Not a drift surface.

#### Drift sweep #3 — `mdbook` CI matrix push verification

**Verdict**: FINDING (medium) — verification deferred; integrator
hasn't pushed to `origin/main` yet. Documented expected steps +
expected outcome below for the integrator to complete post-push.

State at audit time (commit `3624392` on local `main`):

```
$ git log --oneline -3 origin/main
d202643 issues: file Sprint 23 + 24 staff placeholders from demo.sh re-verify
aa12fc8 chore(gitignore): untrack demo.sh as a presenter-local working file
cbb9c1b fix(detectshape): require managed ibm_container_vpc_cluster for legacy classification
```

`HEAD` is at `3624392`, three commits ahead of `origin/main` —
the integrated tree exists locally but the `git push` that
fires the `tools-images.yml` `Build and push (main → :dev)` step
hasn't happened yet. The mdbook matrix entry (validator closure
edit at `.github/workflows/tools-images.yml:34`) is therefore
**latent on disk** — the workflow can't have run against the
new matrix until the integrator pushes.

**Expected verification step** (integrator-owned, post-push):

1. After `git push origin main` lands `3624392` (or whatever
   the cut commit is), open the GitHub Actions tab for
   `jgruberf5/roksbnkctl` and confirm three matrix jobs spin up
   under the next "Build tools images" workflow run: `build
   (ibmcloud)`, `build (iperf3)`, `build (mdbook)` (matching
   `.github/workflows/tools-images.yml:34`'s
   `image: [ibmcloud, iperf3, mdbook]` — verified in
   validator's closure §"Workflow diff" and re-grepped at audit
   time).
2. Wait for all three jobs green (mdbook is the slowest per
   validator's closure §"Expected outcome of the manual
   dispatch" — 15–25 minutes on a cold runner; the workflow's
   `fail-fast: false` strategy means the other two won't be
   cancelled if mdbook flakes).
3. Read the `Build and push (main → :dev)` step's log for the
   `build (mdbook)` job. The final `docker/build-push-action@v5`
   line emits a `digest: sha256:<HEX>` value — capture that
   manifest SHA256.
4. Verify the published manifest matches by querying GHCR:

   ```bash
   docker buildx imagetools inspect \
     ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev \
     | grep -i 'digest:'
   ```

   The `Manifest: ghcr.io/.../mdbook:dev@sha256:<HEX>` digest
   should match the SHA256 captured from the workflow log
   byte-for-byte. If they match, the push is the one the
   workflow actually built; if they diverge, something cached
   or out-of-band republished and is worth investigating before
   relying on the auto-push for the Sprint-23-gated `v1.7.1` tag.

**Expected outcome**: green. The validator's closure already
verified the workflow edits are syntactically correct (the
existing `tag` / `main` / `dispatch` steps use `${{ matrix.image }}`
for both the `file:` path and the `tags:` value, so the matrix
expansion automatically wires up mdbook with zero step-body
edits); the architect's `tools/docker/mdbook/Dockerfile`
inspection confirmed zero repo-root path references (so the
shared `context: .` is harmless, mirror of `iperf3`'s situation).
There's no architectural reason for the push to fail; the
verification is a confidence check, not a gate.

**Severity**: medium — the verification is a follow-up to confirm
the workflow actually fires as designed, not a precondition for
the integration commit to land. The Sprint 22 release tag is
gated on Sprint 23 (integrator decision 3) so there's no time
pressure on the verification before cut. **Not a blocker.**

#### Drift sweep #4 — `CONTRIBUTING.md` consistency + stale-callout sweep

**Verdict**: CLEAN — three sites in `CONTRIBUTING.md` updated
consistently; no stale "manually push mdbook" callouts survive
in any active doc, Makefile comment, or contributor surface.

Three updated sites in `CONTRIBUTING.md` cross-checked
against the architect's closure §"Edits in `CONTRIBUTING.md`":

| Site | Architect-claimed shape | On-disk shape | Verdict |
|------|-------------------------|---------------|---------|
| Line 23 (`§"deliberately does NOT install"` mdbook bullet) | CI publishes `:dev` on every `main` push via `tools-images.yml`; `docker pull` or `make -C tools/docker build-mdbook` for local | Matches | CLEAN |
| Lines 333–363 (`§"Building tool images locally"`) | Three changes: third image bullet, full paragraph naming `strategy.matrix.image` + both publish paths + the Sprint 22 fold-in note, `make build-mdbook` added to local-build code block, `build-all` comment updated | Matches (lines 338, 340–350, 354–356, 357) | CLEAN |
| Line 500 (`§"Release-time tooling"` mdbook table row) | CI-managed via `tools-images.yml`, both publish paths, `make release` / `make release-publish` pull behaviour, `make -C tools/docker build-mdbook` local fallback | Matches | CLEAN |

Cross-doc stale-callout sweep (rerun of the architect's grep
methodology with a slightly broader pattern):

```
grep -rn -E "manually push|remember to push|manual.*mdbook|push.*mdbook" \
  --include='*.md' --include=Makefile --include='*.mk' \
  /mnt/c/project/roksbnkctl
```

Hit triage:

| File:line | Verdict |
|-----------|---------|
| `CONTRIBUTING.md:23, 500` | CI-aware post-edit (architect's three sites). |
| `CHANGELOG.md:377` | Historical (v1.0.1 book CI shift); unrelated to Sprint 22's CI fold-in. Out of scope per integrator decision. |
| `docs/PLAN.md:98, 951, 1144, 1145` | Sprint-narrative (book.yml HTML deploy at line 98 is the orthogonal workflow; lines 1144/1145 are the Sprint 22 per-role scope rows themselves — correct as written). Out of scope. |
| `prompts/sprint22/*.md`, `issues/issue_sprint22_*.md`, `.archive/...` | Prompt + closure files describing the work; out of scope. |
| `book/book/pandoc/pdf/src/31-building-from-source.md` | Build artefact (pandoc native intermediate under `book/book/` — untracked, regenerated each `make book` run). Out of scope. |
| `book/src/31-building-from-source.md:92–116` | Pre-existing drift (the chapter's `tools/docker/` directory listing omits `mdbook/`; the `make ibmcloud` / `make iperf3` / `make all` examples predate the `mdbook` target). **NOT a Sprint 22 introduction** — was already drifted relative to `tools/docker/Makefile` before this sprint. Flagged by the architect's closure as a tech-writer-surface follow-up; flagged here as an integrator follow-up candidate (see below). |
| `Makefile:107` | Local-build fallback recovery hint (`make book-pdf BOOK_BACKEND=docker` failure path suggests `make -C tools/docker build-mdbook`). Architect's closure decision (§"Cross-doc sweep result" Makefile row) explicitly leaves this untouched — the local build still works, the hint is correct, and a `docker pull ghcr.io/.../mdbook:dev` alternative is a low-priority Makefile-ergonomics candidate, not a Sprint 22 deliverable. Flagged for follow-up below. |

No stale "manually push mdbook" callout survives in any active
doc surface. The only `make -C tools/docker build-mdbook`
mentions remaining in active docs are local-build fallbacks
explicitly preserved by the architect's edits + the Makefile
error-path hint, which is correct local-recovery guidance.

#### Drift sweep #5 — `issues/issue_sprint22_staff.md` claims vs. diffs

**Verdict**: CLEAN — staff closure claims match `git show
18415eb` + `git show cbb9c1b` exactly.

Cross-checked the staff closure §"Closure — staff, 2026-05-27"
(`issues/issue_sprint22_staff.md:279–351`) line-by-line against
the two commit diffs:

**`18415eb` (down-prompt composite UX)** — staff cites three
files: `internal/orchestration/lifecycle.go`,
`internal/cli/lifecycle.go`, `book/src/11-tearing-down.md`.
`git show 18415eb --stat` → exactly those three files, +41/-4
LOC. The staff claim "single up-front confirmation in `RunDown`'s
Split branch that names both phases and flips `in.Auto = true`"
is verified by the diff at `internal/orchestration/lifecycle.go:322–344`
(the `ShapeSplit` case grew an 18-line confirmation block that
matches the staff prose). The claim "cli adapter's `RunClusterDown`
closure mirrors `in.Auto` onto `flagAuto`" is verified by the
diff at `internal/cli/lifecycle.go:127–145` (the closure grew a
6-line `if in.Auto && !flagAuto { … defer … }` mirror block,
preceded by a 4-line doc comment naming Sprint 16 phase-1b as
the reason `cluster_phase.go` is still in `cli`). The book diff
(refreshed Split + LegacySingle prompt copy at lines 167–195)
is the surface drift sweep #1 verified line-by-line.

**`cbb9c1b` (DetectShape correctness)** — staff cites four
files: `internal/config/tfstate.go`,
`internal/config/tfstate_test.go`,
`internal/config/testdata/tfstate_split_data_in_trial.json`
(new), and `issues/issue_sprint22_staff.md` itself. `git show
cbb9c1b --stat` → exactly those four files, +503/-21 LOC. The
"Fix as shipped" code block (`mode == "managed" && type ==
"ibm_container_vpc_cluster"` filter before the
`clusterPhaseModules` prefix match) matches the diff at
`internal/config/tfstate.go` heuristic site. The test claims
(table case `"split with cluster-phase data sources in trial
(post-up refresh shape)"` + two pinning tests
`_DataSourceUnderClusterPrefix` + `_StrayManagedNonClusterType`
+ three existing helper tests updated with `mode` + `type`
fields) match the diff at `internal/config/tfstate_test.go`
+128/-21 LOC.

**Test + vet claims** — staff cites `go test ./internal/config/...
./internal/orchestration/... ./internal/cli/...` PASS and `go
vet ./...` clean. Sanity-checked the cli config tests at audit
time:

```
$ go vet ./internal/config/... ./internal/orchestration/... ./internal/cli/...
(no output)
$ go test -run TestDetectShape -count=1 ./internal/config/...
ok  github.com/jgruberf5/roksbnkctl/internal/config  0.014s (cached)
```

(Pinned to the DetectShape table tests for speed; the full
config suite has been green on local main since `cbb9c1b`
landed.) No regressions surfaced by the audit re-run.

**Future-sprint candidates raised by staff** (recorded in
`issues/issue_sprint22_staff.md:327–351` under "Future-sprint
candidates raised by audit"):

1. PRD 06 §"Design" wording update — the legacy signature is
   still described there as "trial state contains cluster-phase
   modules", which the implementation originally took as "any
   resource address under the module." The narrower criterion
   shipped here ("a managed `ibm_container_vpc_cluster` under a
   cluster-phase module address") deserves matching PRD prose.
   Tech-writer scope; surfaced again below for the integrator.
2. Hoist `runClusterDown`'s `flagAuto` read out of cli package
   state — the current `lifecycleInputs()` mirror dance
   (`internal/cli/lifecycle.go:130–138`) is correct but a
   future `cluster_phase.go` migration into orchestration would
   let composite teardown read `in.Auto` directly. Architect
   scope.
3. Audit other state-shape heuristics for the same "any resource
   under prefix" vs. "managed resource of marker type"
   confusion — the `tfstateHasResources` helper and the
   `ShapeClusterOnly` gate use looser matching but don't drive
   dispatch. Staff scope, low priority.

All three are out of scope for Sprint 22; carried forward.

---

#### Findings summary

| # | Severity | Surface | Verdict / Status |
|---|----------|---------|------------------|
| 1 | medium  | `tools-images.yml` `:dev` mdbook push verification deferred until integrator pushes `3624392` (or successor) to `origin/main` | documented expected verification step + outcome above; integrator runs `gh workflow run tools-images.yml` (or waits for the next `main`-push) post-push, compares the workflow log's `digest: sha256:<HEX>` to `docker buildx imagetools inspect`'s manifest digest. **Not a blocker.** |

No high findings. No blocker to Sprint 22 closure.

---

#### Follow-up candidates flagged for the integrator (future sprints)

1. **`book/src/31-building-from-source.md:92–116` drift**
   (pre-existing, not Sprint 22-introduced). The chapter's
   `tools/docker/` directory listing (lines 96–103) shows only
   `ibmcloud/` and `iperf3/`, omitting `mdbook/`; the
   `make ibmcloud` / `make iperf3` / `make all` examples at
   lines 107–112 predate the `mdbook` target; the tools-images
   workflow reference at line 116 says "on a tag push or when
   `tools/docker/**` changes" which is **drift from the actual
   trigger** (`tools-images.yml:19–23` — `tags: ['v*']`,
   `branches: [main]`, `workflow_dispatch:` — there is no
   path-filter on `tools/docker/**`). This is the architect's
   flagged follow-up (architect closure §"Follow-up candidates
   flagged for the integrator" item 1) and a tech-writer-scope
   drift on three independent fronts (dir tree, `make` examples,
   trigger condition). **Recommend a low-stakes tech-writer
   sprint** to refresh chapter 31 §"The bundled tools images"
   against the post-Sprint-22 reality.
2. **`Makefile:107` recovery hint refinement** (architect's
   flagged item 2). The `make book-pdf BOOK_BACKEND=docker`
   failure path currently suggests `make -C tools/docker
   build-mdbook` as the local-build recovery. Now that the
   image is published on every `main` push, the recovery could
   also be `docker pull ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`.
   Not urgent — the local build still works — but a future
   sprint could shorten the time-to-recovery for a contributor
   with `docker pull` access who'd rather not wait on a ~25-min
   cargo build. **Lower-priority candidate; bundle with other
   Makefile-ergonomics improvements in a future low-stakes
   sprint.**
3. **PRD 06 §"Design" wording update** (staff's flagged item
   1; pre-existing surface, narrowed criterion deserves matching
   prose). `docs/PRD/06-*` describes the legacy signature as
   "trial state contains cluster-phase modules"; the shipped
   criterion is "trial state contains a managed
   `ibm_container_vpc_cluster` resource under a cluster-phase
   module address". **Tech-writer surface; bundle with the
   chapter 31 sweep above** if a low-stakes tech-writer sprint
   is dispatched.
4. **`tools/docker/mdbook` push manifest digest sanity check**
   (this sweep's Finding 1). Once the integrator pushes
   `3624392` to `origin/main`, capture the workflow log's
   `digest: sha256:<HEX>` from the `build (mdbook)` job's
   `Build and push (main → :dev)` step and compare to
   `docker buildx imagetools inspect ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`'s
   manifest. Match → CI matrix fold-in verified end-to-end and
   ready for the Sprint-23-gated `v1.7.1` tag. Mismatch → file
   a follow-up investigation issue. **Integrator follow-up at
   push time.**

None of items 1–4 block Sprint 22 closure or the Sprint-23-gated
`v1.7.1` cut.

---

#### Launch verdict: **GREEN**

Sprint 22's three deliverable streams — staff (DetectShape +
down-prompt, shipped in `cbb9c1b` + `18415eb`); validator
(mdbook CI matrix fold-in in `tools-images.yml`); architect
(`CONTRIBUTING.md` CI-managed note) — are **integrated and
drift-clean** at commit `3624392`. The book copy matches binary
output byte-for-byte (drift sweep #1); no other book chapter
quotes prompt text from the changed code path (#2); CONTRIBUTING.md
is consistent with the workflow edit and no stale "manually
push mdbook" callouts survive in any active doc surface (#4);
the staff closure claims match the shipped diffs (#5). The
mdbook `:dev` push verification (#3) is deferred until the
integrator's `git push origin main` fires the workflow — expected
green, with the verification step + manifest-digest comparison
documented above for the integrator to complete post-push.

**Deferred live verify — DetectShape live-verify gated on
Sprint 23.** Per integrator decision 5 in `prompts/sprint22/README.md`,
the DetectShape fix (`cbb9c1b`) is high-severity and would
normally require a real `up → down` cycle to close per the
`live-verify-high-issues` memory. That cycle would hit the
Sprint 23 phase-leak (`bnk-phase-override.tfvars` count-gate
fails to suppress `module.testing.tls_private_key.jumphost_shared_key`
+ `module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`,
contaminating post-`up` trial state). The live verify therefore
waits for Sprint 23 to ship; unit + table coverage
(`TestDetectShape_Table` "split with cluster-phase data sources
in trial" + the two pinning tests) is exhaustive in the interim.

**Release tag — gated on Sprint 23.** Per integrator decision
3, DO NOT tag a release at the end of Sprint 22. Sprint 23's
phase-separation leak fix lands first, then both sprints
bundle into one combined `v1.7.1` cut. This tech-writer closure
makes no `tag` / `release` / `CHANGELOG.md` recommendations —
those defer to Sprint 23's tech-writer closure (or the
integrator's combined cut commit).

---

#### Discipline checks

- No commits, no `gh` invocations, no `git push`.
- Read-only on all repo content except this file
  (`issues/issue_sprint22_tech-writer.md`).
- No edits to `internal/`, `cmd/`, `.github/workflows/`,
  `tools/docker/`, `CONTRIBUTING.md`, `book/src/`, `Makefile`,
  `docs/PLAN.md`, or `CHANGELOG.md`.
- No edits to `issues/issue_sprint23_staff.md` or
  `issues/issue_sprint24_staff.md` (forward placeholders;
  out of scope).
- Per-finding fields use `**Verdict**:`, not `**Status**:`
  (mirrors the Sprint 21 `a2b78da` rename — the issue-level
  top-of-file `**Status**:` is reserved for the
  open/in-progress/resolved field).
- Every finding cites specific `file:line` and a severity
  tier; the one medium finding (#1, deferred CI verification)
  carries an expected verification step + expected outcome
  for the integrator to complete post-push.

### Related

- `prompts/sprint22/tech-writer.md` — this issue's source prompt.
- `prompts/sprint22/README.md` — integrator decisions
  (three-deliverable-stream framing; Sprint 23 release-gate;
  DetectShape live-verify gate).
- `issues/issue_sprint22_staff.md` — staff closure audited
  in drift sweep #5; claims match the `18415eb` + `cbb9c1b`
  diffs.
- `issues/issue_sprint22_validator.md` — validator closure;
  `tools-images.yml` matrix fold-in audited in drift sweep #3
  (verification deferred).
- `issues/issue_sprint22_architect.md` — architect closure;
  `CONTRIBUTING.md` CI-managed note audited in drift sweep #4
  (clean). Three architect-flagged follow-ups surfaced again
  for the integrator above.
- Integrator memory `[[live-verify-high-issues]]` — the
  rationale for the DetectShape live-verify gate; explicit
  exception granted here per integrator decision 5 (Sprint 23
  fix lands first to give the live verify a clean post-`up`
  test bed).
- Integrator memory `[[no-piling-into-active-release]]` — the
  Sprint 23 phase-leak fix gets its own sprint cycle rather
  than being folded into Sprint 22's cut; this closure honours
  that by holding the release tag.
