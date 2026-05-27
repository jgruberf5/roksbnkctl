You are the **architect** agent for Sprint 23 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first

1. `prompts/sprint23/README.md` — integrator decisions; the
   two-Issue architect scope.
2. `docs/PRD/06-CLUSTER-TRIAL-PHASE-SPLIT.md` §"Design" — the
   wording that needs updating (Sprint 22 staff-flagged
   follow-up: the narrowed DetectShape criterion).
3. `internal/config/tfstate.go` lines 78-90 + 187-225 — the
   shipped DetectShape logic. `clusterPhaseModules` slice +
   `trialStateHasClusterModules` is the authority on the
   narrowed criterion ("managed `ibm_container_vpc_cluster`
   under a cluster-phase module address"). Your PRD wording
   must match.
4. `book/src/31-building-from-source.md` §"The bundled tools
   images" — the drift target (Sprint 22 architect-flagged
   follow-up).
5. `.github/workflows/tools-images.yml` — the integrated
   Sprint 22 state of the workflow. Reference for the chapter
   31 reconciliation.
6. `tools/docker/` — the actual on-disk tree. The chapter 31
   listing must match. `ls tools/docker/` shows `Makefile`,
   `ibmcloud/`, `iperf3/`, and `mdbook/`.

## Issue 1 — PRD 06 §"Design" wording update

**Current state** (verify the exact wording in
`docs/PRD/06-CLUSTER-TRIAL-PHASE-SPLIT.md`): the §"Design"
section describes the legacy-single-state detection as
roughly "trial state contains cluster-phase modules" — the
implementation originally took this as "any resource address
under one of the cluster-phase module prefixes
(`module.roks_cluster`, `module.cert_manager`,
`module.testing`)." Sprint 22's `cbb9c1b` narrowed the
criterion to "a managed `ibm_container_vpc_cluster` resource
under a cluster-phase module address" — the ROKS cluster
itself is the unambiguous v1.0.x marker. Data sources and
stray managed resources of other types under cluster-phase
prefixes are benign (e.g. tls_private_key, ibm_resource_instance
of COS).

**Task:** rewrite the §"Design" prose to match the narrower
shipped criterion. Cite the rationale (post-`up` trial
states legitimately carry data-source refreshes under
cluster-phase prefixes; only managed `ibm_container_vpc_cluster`
ownership signals legacy single-state). Optionally link to
`internal/config/tfstate.go` for the canonical implementation.
Keep the surrounding §"Refusal messages" / §"Dispatch
table" sections intact — only the legacy-detection criterion
needs the update.

## Issue 2 — Chapter 31 drift sweep

**Current state**: `book/src/31-building-from-source.md`
§"The bundled tools images" (verify the section title — it
may be slightly different) shows a `tools/docker/` tree
listing that predates Sprint 22 and omits `mdbook/`. The
`make` examples don't reference `build-mdbook`. The
description of when the CI workflow fires is outdated
(predates the Sprint 22 matrix expansion).

**Task:** reconcile against the post-Sprint-22 state of:
- `tools/docker/` (on-disk tree — confirm via `ls`)
- `tools/docker/Makefile` (the targets actually available)
- `.github/workflows/tools-images.yml` (the matrix, the
  trigger conditions, the published image tags)

Update the chapter's tree listing, the `make` examples, and
the trigger description. Do NOT rewrite the chapter from
scratch — find the specific drift sites and surgically
correct them. Match the Sprint 22 `CONTRIBUTING.md` framing
(`mdbook` is now CI-managed via the matrix; routine
Dockerfile edits no longer require manual push).

## Out of scope

- `internal/`, `cmd/`, `tools/` Go code — staff territory.
- `.github/workflows/`, `tools/docker/*/Dockerfile` —
  validator / out-of-tree.
- `CONTRIBUTING.md` — already updated in Sprint 22; don't
  re-edit unless the chapter 31 rewrite surfaces a
  CONTRIBUTING.md inconsistency.
- The CHANGELOG — integrator-owned at cut time (which is
  post-tech-writer GREEN + post-live-verify GREEN).
- `internal/orchestration/second_phase_reuse.go` — staff
  territory for this sprint.

## Acceptance criteria

1. PRD 06 §"Design" wording describes the narrower criterion
   accurately. A reader unfamiliar with the heuristic should
   understand from the PRD why `mode == "managed"` AND
   `type == "ibm_container_vpc_cluster"` AND a cluster-phase
   module prefix are all required.
2. Chapter 31's tools-images section matches the on-disk
   `tools/docker/` tree + the workflow's actual trigger
   behavior. No mention of `mdbook` as "manually built and
   pushed" — that framing is dead post-Sprint-22.

## Closure

Write your closure to
`issues/issue_sprint23_architect.md` §"Closure — architect,
<date>". Include: the PRD 06 diff (line numbers + before/after
of the §"Design" paragraph), the chapter 31 drift sites you
corrected (line numbers + before/after summary), and any
future-sprint candidates raised. Flip the top-of-file
`**Status**:` field to `resolved`. Create the issue file —
it doesn't exist yet.

Reply with a concise summary under 200 words: the PRD 06
update, the chapter 31 drift sites corrected, and any
follow-ups for the integrator.
