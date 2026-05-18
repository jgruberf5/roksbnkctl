# Sprint 8

**Theme:** Cluster/trial phase split — `bnk` command group + composite lifecycle (PRD 06)

_Drafted from `docs/PLAN.md` Sprint 8 section. Sprint 8 is the **first post-v1.0 feature cycle**: it makes the two-phase lifecycle (durable cluster + short-lived BNK trial) the default for new workspaces, adds `roksbnkctl bnk up/down` so a trial can be torn down without destroying its cluster, and converts the unscoped `roksbnkctl up`/`down` into shape-aware composites that preserve v1.0.x behavior for legacy single-state workspaces._

The cluster-phase split was opt-in in v1.0.x (`roksbnkctl cluster up/down/register/show`) but discoverability was poor — most users defaulted to the unscoped `up` and ended up with cluster + trial in one state file, so trial-only teardowns destroyed the cluster too. Sprint 8 fixes this by making the split the default and adding a first-class `bnk` group.

Reference spike: `spike/bnk-phase-split` branch (commit `00181d0`) — a proof-of-concept that the shape detector correctly identifies the real `canada-roks` legacy workspace (135 resources, both phases sharing one tfstate). The branch is reference-only; the **staff agent re-implements from PRD 06**, not from the spike. The spike is the empirical evidence the design works, not the deliverable.

Sprint 8 closes the `v1.1.0` tag. Cycle is approximately one week; this is a tight feature sprint, not a multi-week cross-cutting one.

The four-agent dispatch shape is the same as Sprints 1-7:

- **Architect** — PRD 06 polish (the draft already lives at `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md`; this sprint refines if validator/staff surface gaps); chapter 8 reframe (cluster phase is now the default, not opt-in); chapter 10 `bnk up/down` section + dispatch matrix; chapter 11 phase-aware decision matrix; CHANGELOG `v1.1.0` entry under the existing `Unreleased (v1.x)` section.
- **Staff engineer** — implement the dispatch from PRD 06: `internal/config/tfstate.go` (new), `internal/cli/bnk_phase.go` (new), refactor `lifecycle.go` (rename `runUp`/`runDown` bodies to `runTrialUp`/`runTrialDown`, add composite dispatchers), add shape refusals to `cluster_phase.go`. Unit tests for `DetectShape` against synthetic tfstate fixtures + bnk dispatch tests.
- **Validator** — full regression sweep (`go build/test/vet/gofmt`); manual live verification against the existing `canada-roks` legacy workspace (refusals fire as expected, monolithic `down` still works to the confirm prompt); cross-link audit on the touched chapters; optional e2e patch adding a `cluster up` → `bnk up` → `bnk down` → `cluster down` cycle phase to `scripts/e2e-test.sh`.
- **Tech-writer** — read-only review at end of sprint; dogfooding loop on the new chapter content from a first-time-reader perspective ("I want to keep my cluster and redeploy BNK — which command do I run?"); drift sweep between PRD 06, PLAN.md Sprint 8, the chapters, and the CHANGELOG entry; launch-readiness audit against PLAN.md §"Gate to `v1.1.0` tag".

The release tag itself (`v1.1.0`) is **integrator-owned** — Sprint 8 lands all the prep; the integrator cuts the tag, kicks off goreleaser, and pushes after the four agents' work merges.
