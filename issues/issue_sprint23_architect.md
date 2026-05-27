# Sprint 23 — architect issues (PRD 06 narrowed-criterion wording + Chapter 31 drift sweep)

> **Sprint 23 frame.** Two Sprint 22 architect-/staff-flagged
> follow-ups folded into this sprint's architect scope per
> integrator decision (`prompts/sprint23/README.md` §"Integrator
> decisions baked in" item 5). Both are documentation-only,
> reconciling the PRD + book against the post-Sprint-22 shipped
> state. Neither touches code, workflows, or any
> tech-writer/staff/validator-owned surface.

**Status**: resolved

---

## Issue 1 — PRD 06 §"Design" narrowed-criterion wording

**Severity**: low (documentation; no behavior change).
**Status**: resolved

### Motivation

Sprint 22's commit `cbb9c1b` narrowed the legacy-single-state
DetectShape criterion from "trial state contains any resource
under one of the cluster-phase module prefixes" to "trial state
owns a managed `ibm_container_vpc_cluster` resource under one of
the cluster-phase module prefixes." Authoritative implementation:
`trialStateHasClusterModules` in `internal/config/tfstate.go`
(lines 206–235), with the three required filters spelled out in
the doc comment (lines 187–225).

The PRD 06 §"Design" prose still described the legacy classifier
as the over-broad "trial state contains cluster-phase modules"
check — that wording predates the Sprint 22 tightening and would
mislead a reader inferring the heuristic from the PRD alone. A
reader unfamiliar with the implementation should be able to
understand from the PRD why `mode == "managed"` AND `type ==
"ibm_container_vpc_cluster"` AND a cluster-phase module prefix
are all required, and why each filter on its own would
over-classify.

### Closure — architect, 2026-05-27

**Files edited**: `docs/PRD/06-CLUSTER-TRIAL-PHASE-SPLIT.md` only.
No edits to `internal/config/tfstate.go` (staff-territory; Sprint
22 settled), no edits to `internal/cli/lifecycle.go` or
`cluster_phase.go`, no edits to the CHANGELOG (integrator-owned
at cut time per the sprint README).

**Edits in `docs/PRD/06-CLUSTER-TRIAL-PHASE-SPLIT.md`**:

**Site 1** — §"Shape detection" signals table, third row (line
83 in the pre-edit file). The pre-edit cell text was:

> | Trial state contains cluster modules | walk
> `state.resources[]`, match `module` field against
> `module.roks_cluster`, `module.cert_manager`, `module.testing`
> (the modules that `deploy_bnk=false` in `cluster_phase.go`
> provisions) | Legacy single-state — cluster and trial share
> one tfstate |

Rewritten to:

> | Trial state owns the ROKS cluster | walk
> `state.resources[]`, find an entry whose `mode == "managed"`
> AND `type == "ibm_container_vpc_cluster"` AND whose `module`
> address matches one of `module.roks_cluster`,
> `module.cert_manager`, `module.testing` (the modules that
> `deploy_bnk=false` in `cluster_phase.go` provisions) | Legacy
> single-state — cluster and trial share one tfstate |

The signal name shifts from the broad "contains cluster modules"
to the narrower "owns the ROKS cluster" to match the resource-type
pin. The "How" cell now spells the three-AND filter inline.

**Site 2** — the post-table prose explaining the cluster-module
match (lines 85–87 in the pre-edit file). The pre-edit paragraph
was a single sentence about `strings.HasPrefix` plus exact
equality. Rewritten and expanded into a three-bullet rationale
explaining *why each filter is necessary*, with a top-line
pointer at the authoritative implementation:

> The legacy-single-state classifier requires all three filters
> together; any one of them on its own would over-classify.
> Authoritative implementation: `trialStateHasClusterModules`
> in `internal/config/tfstate.go`.
>
> 1. **`mode == "managed"`.** A normal post-`up` split trial
>    state legitimately *reads* data sources under cluster-phase
>    module prefixes — `module.cert_manager` and
>    `module.roks_cluster` propagate cluster identity into the
>    BNK trial via data-source lookups, and those entries appear
>    in the trial tfstate with `mode == "data"`. Treating data
>    refreshes as ownership would mis-flag every healthy split
>    workspace as legacy. The filter discards them.
> 2. **`type == "ibm_container_vpc_cluster"`.** The ROKS cluster
>    resource itself is the singular unambiguous v1.0.x marker —
>    if a trial-phase state file owns that resource, the cluster
>    lives in the trial state and the workspace is shared. Other
>    managed resources can appear under cluster-phase module
>    addresses for benign reasons (a stray
>    `tls_private_key.jumphost_shared_key` under `module.testing`,
>    a `module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`
>    for the registry COS, etc., observed during normal post-`up`
>    split applies); none of those imply shared-state ownership,
>    so they must not trip the classifier on their own. Pinning
>    to the cluster resource type is what makes the heuristic
>    robust against the routine cluster-shared resources that
>    propagate into the BNK trial's plan graph.
> 3. **Cluster-phase module prefix match.** Even an
>    `ibm_container_vpc_cluster` resource needs to live under
>    one of the cluster-phase module addresses for the legacy
>    classification to fire. Match logic is exact-equality OR
>    `strings.HasPrefix(r.Module, prefix+".")` — the trailing-dot
>    guard covers nested sub-addresses (e.g.
>    `module.roks_cluster.module.cluster`) while preventing false
>    matches against unrelated module names that happen to share
>    a prefix (e.g. `module.roks_cluster_extras` does not match
>    `module.roks_cluster`).

The closing paragraph naming the canada-roks workspace
(135 resources, the empirical fixture) was preserved verbatim
to keep the "we ran this against real data" provenance
intact.

**Sections deliberately left untouched**: §"Refusal messages"
(lines 136–151 in the post-edit file, unchanged), §"Dispatch
table" (lines 89–102, unchanged), §"`status` command
integration" (lines 153–168, unchanged), §"Implementation tasks"
(unchanged), §"Acceptance criteria" (unchanged), §"Open
questions" (unchanged). Per the architect prompt the narrowed
criterion is the only Sprint 22 drift in PRD 06; the rest of
the document still describes the shipped surface accurately.

**Discipline checks**:

- Only `docs/PRD/06-CLUSTER-TRIAL-PHASE-SPLIT.md` touched for
  Issue 1.
- No edits to `internal/config/tfstate.go` (staff-territory;
  Sprint 22's `cbb9c1b` is the authority and the PRD now cites
  it rather than restating it).
- No edits to the surrounding refusal-message / dispatch-table
  prose — the Sprint 22 tightening did not change command
  behavior, only the detector signal, so those sections still
  describe the shipped surface correctly.

---

## Issue 2 — Chapter 31 drift sweep (tools-images section)

**Severity**: low (documentation; user-visible book content).
**Status**: resolved

### Motivation

`book/src/31-building-from-source.md` §"The bundled tools
images" (verified the section title — line 92, exact heading is
"The bundled tools images") predated three Sprint 22 changes:

1. The on-disk `tools/docker/` tree gained `mdbook/` (verified
   `ls tools/docker/` returns `Makefile`, `ibmcloud/`, `iperf3/`,
   `mdbook/` — four entries).
2. `tools/docker/Makefile` exposes `build-ibmcloud`,
   `build-iperf3`, `build-mdbook`, `build-all`, `clean` —
   *not* the bare `ibmcloud` / `iperf3` / `all` target names the
   chapter quoted (those don't exist in the Makefile;
   `make ibmcloud` would fail with "No rule to make target
   ibmcloud").
3. `.github/workflows/tools-images.yml` runs a
   `strategy.matrix.image: [ibmcloud, iperf3, mdbook]` matrix
   (lines 31–34 of the workflow) over three trigger paths: tag
   push (publishes `:<tagname>` + `:latest`), main push
   (publishes `:dev`), and `workflow_dispatch` (publishes
   `:dev`). The chapter's old single-sentence trigger
   description ("on a tag push or when `tools/docker/**`
   changes") was inaccurate on both axes — there is no `paths:`
   filter on `tools/docker/**`, and it elided the `:dev`
   publish-on-main behavior entirely.

The chapter was the last drift surface flagged for Sprint 22 by
the prior architect closure
(`issues/issue_sprint22_architect.md` §"Follow-up candidates",
item 1) and folded into this sprint's architect scope per
`prompts/sprint23/README.md` integrator decision 5.

### Closure — architect, 2026-05-27

**Files edited**: `book/src/31-building-from-source.md` only.
No edits to `CONTRIBUTING.md` (Sprint 22 already CI-aware; the
chapter-31 rewrite did not surface any inconsistency requiring
re-edit there — the framing the new chapter prose uses
"`mdbook` was folded into the matrix in Sprint 22 — routine
edits…" is verbatim-style-matched to `CONTRIBUTING.md` lines
344–348, intentionally consistent). No edits to the workflow
file (validator's surface; the workflow is the source of truth
the chapter now reconciles to). No edits to the Dockerfiles
under `tools/docker/*/`.

**Drift sites corrected** — three sites, surgical edits, no
chapter rewrite. The before/after summary by site:

| Site | Pre-edit lines | Drift | Post-edit lines | Fix |
|------|----------------|-------|------------------|-----|
| Intro sentence | 94 | "Dockerfiles for the images the `docker` and `k8s` backends use" — omits the book-builder image | 94 | "Dockerfiles for the images the `docker` and `k8s` backends use, plus the release-time book builder" — names `mdbook`'s release-time role |
| Tree listing | 96–103 | Two-entry tree (`ibmcloud/`, `iperf3/`); missing `mdbook/` | 96–105 | Three-entry tree adds `mdbook/` with `# roksbnkctl-tools-mdbook (release-time book builder)` comment matching the other entries' style |
| `make` example block | 105–112 | `make ibmcloud` / `make iperf3` / `make all` — none of these targets exist in `tools/docker/Makefile`. The Makefile's actual targets are `build-ibmcloud`, `build-iperf3`, `build-mdbook`, `build-all` | 107–114 | Rewrote the four lines to `make build-ibmcloud` / `make build-iperf3` / `make build-mdbook` / `make build-all`, each with the matching comment ("builds roksbnkctl-tools-<image>:dev" + "all three"). Also rewrote the lead-in sentence from "builds both images locally as `:dev`" to "builds the three images locally as `:dev`" |
| Workflow trigger description | 116 | Single sentence: "builds and pushes the published images on a tag push or when `tools/docker/**` changes" — factually wrong on both axes (no `paths:` filter; elides `:dev` on main push; elides `workflow_dispatch`; elides matrix shape) | 118 | Replaced with a multi-sentence paragraph: names the `strategy.matrix.image` over `[ibmcloud, iperf3, mdbook]`, names the three trigger paths (main push → `:dev`; `v*` tag push → `:<tagname>` + `:latest`; `workflow_dispatch` → `:dev`), explicitly states `mdbook` was folded into the matrix in Sprint 22 and that routine `tools/docker/mdbook/Dockerfile` edits no longer require a manual `make -C tools/docker build-mdbook` + `docker push` step on the release-cut host. Matches `CONTRIBUTING.md` lines 340–350's framing verbatim-style |

**Verbatim post-edit text for the workflow-trigger paragraph**
(the most substantive single-site change):

> The GitHub Actions workflow
> [`tools-images.yml`](https://github.com/jgruberf5/roksbnkctl/blob/main/.github/workflows/tools-images.yml)
> builds and pushes the published images via a
> `strategy.matrix.image` over `[ibmcloud, iperf3, mdbook]`.
> Every push to `main` republishes the `:dev` tag for each
> image; every `v*` tag push publishes `:<tagname>` + `:latest`.
> `workflow_dispatch` is also wired for manual runs. The
> `mdbook` image was folded into the matrix in Sprint 22 —
> routine edits to `tools/docker/mdbook/Dockerfile` no longer
> require a manual `make -C tools/docker build-mdbook` +
> `docker push` step on the release-cut host; the next push to
> `main` republishes `ghcr.io/jgruberf5/roksbnkctl-tools-mdbook:dev`
> automatically.

**Sections deliberately left untouched** in chapter 31:
§"Go version requirement", §"Quick build", §"Build via the
Makefile", §"The embedded HCL", §"The book build", §"The
auto-generated chapters", §"Cross-compile matrix", §"Release
process", §"Cross-references" — none of those carried Sprint 22
drift. The §"Release process" mention at line 207 of "Build the
matching tools images and push to
`ghcr.io/jgruberf5/roksbnkctl-tools-*:<tag>`" is glob-correct
post-Sprint-22 (the `-*` covers all three matrix images), so no
edit needed.

**Cross-doc consistency check**:

- `CONTRIBUTING.md` lines 340–350 already carry the Sprint 22
  CI-aware framing for the same workflow. The new chapter 31
  paragraph is style-matched to `CONTRIBUTING.md`'s phrasing
  (the "`mdbook` was folded into the matrix in Sprint 22 —
  routine edits…" formulation is shared verbatim), so a reader
  hitting both surfaces gets a consistent story. No re-edit of
  `CONTRIBUTING.md` required.
- `CONTRIBUTING.md` lines 23 (the install-script section's
  `mdbook` bullet) and 500 (the release-time tooling table row)
  also already name `tools-images.yml` and the `:dev`-on-`main`
  publish path. Consistent with the new chapter 31 prose; no
  re-edit.
- No "manually built and pushed" framing for `mdbook` survives
  in chapter 31 post-edit. The old single-sentence trigger
  description didn't carry that framing explicitly, but the
  paired-with-omission-from-tree effect implicitly excluded
  `mdbook` from the CI flow; that exclusion is now fixed.

**Discipline checks**:

- Only `book/src/31-building-from-source.md` touched for Issue
  2.
- No edits to `.github/workflows/tools-images.yml` (validator's
  surface; the workflow is the authority the chapter
  reconciles to).
- No edits to `tools/docker/Makefile` (out-of-tree per the
  prompt's "Out of scope" — and the Makefile is already
  correct; the chapter was drifting from it, not the other way
  around).
- No edits to `tools/docker/*/Dockerfile` (out-of-tree per the
  prompt).
- No edits to `CONTRIBUTING.md` (Sprint 22 already updated; the
  chapter-31 rewrite did not surface an inconsistency).
- No edits to `internal/`, `cmd/`, `terraform/`, or any Go
  code.
- No structural rewrite of the chapter — three surgical
  drift-site edits, all under §"The bundled tools images". The
  chapter's overall organization, cross-references, and other
  sections are unchanged.
- No commits; no `gh issue create`; no tag proposal. Sprint 23
  release-cut is integrator-owned post-tech-writer + live-verify
  GREEN.

### Follow-up candidates flagged for the integrator

1. **Tech-writer drift-sweep cross-check.** The Sprint 23
   tech-writer (per `prompts/sprint23/README.md`'s per-role
   scope table) will sweep architect + staff + validator
   deliverables, including PRD 06's §"Design" prose and chapter
   31's tools-images section. Both edits in this closure are
   intentionally tone-matched to surrounding prose (PRD voice
   for PRD 06; book chapter voice for chapter 31). Tech-writer
   should still confirm consistency with the shipped
   `tfstate.go` doc comment + the `CONTRIBUTING.md` framing —
   no expected drift, but the cross-doc check is the
   tech-writer's surface, not the architect's.
2. **No PRD 03 update needed.** `docs/PRD/03-EXECUTION-BACKENDS.md`
   documents the docker backend's runtime image-pull contract
   (binary version → image tag resolution), which is unchanged
   by Sprint 22's CI-flow shift or by Sprint 23's documentation
   reconciliation. Verified at the Sprint 22 closure;
   re-verified here implicitly (chapter 31's §"The bundled
   tools images" links to chapter 17 §":dev tag resolution" for
   the runtime-resolution contract, which is PRD-03 territory
   and untouched).
3. **No second-pass needed on the §"Refusal messages" /
   §"Dispatch table" sections of PRD 06.** Sprint 22's
   tightening only changed the detector signal; the four-shape
   dispatch behavior is unchanged. The narrowing of "any
   resource under a cluster-phase prefix" → "a managed
   `ibm_container_vpc_cluster` under a cluster-phase prefix"
   means *fewer* false-positive Legacy classifications, never
   misroutes a real Legacy workspace; the refusal-message text
   and dispatch table both stay valid for both signal shapes.
4. **Future low-stakes sprint candidate** — PRD 06 §"Open
   questions" entry on `roksbnkctl migrate` (line 206) is still
   accurate as written: no real legacy user has asked for the
   one-shot state-surgery migration. Worth re-checking each
   sprint that touches PRD 06; not addressed here.

---

## Related

- `prompts/sprint23/architect.md` — this issue's source prompt
  (two Sprint 22 follow-ups folded into Sprint 23 architect
  scope).
- `prompts/sprint23/README.md` — integrator decisions
  (investigation-first staff scope; the architect's two
  Sprint-22 follow-ups; release tag is post-tech-writer +
  live-verify GREEN; v1.7.1 unblocks here).
- `issues/issue_sprint22_architect.md` — prior closure flagging
  the chapter-31 drift (Follow-up candidate 1, the immediate
  predecessor of Issue 2 here) and confirming the
  `CONTRIBUTING.md` Sprint 22 update.
- `internal/config/tfstate.go` (lines 70–89 + 180–235) — the
  authoritative narrowed-criterion implementation that PRD 06
  §"Design" now cites and accurately describes.
- `.github/workflows/tools-images.yml` — the matrix the chapter
  31 trigger paragraph now reconciles to.
- `tools/docker/Makefile` — source of the `build-ibmcloud` /
  `build-iperf3` / `build-mdbook` / `build-all` target names
  the chapter 31 examples now use.
