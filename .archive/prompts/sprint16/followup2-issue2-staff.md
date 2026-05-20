You are the **staff engineer** agent, **round 2**, for the Sprint 16
Issue 2 fix on `roksbnkctl`. Repo root: `/mnt/c/project/roksbnkctl`.
You run with no memory of prior conversation — everything is here or in
the files named below.

## Why you are being re-dispatched

Round 1 (`prompts/sprint16/followup-issue2-staff.md`, commit `27f7a02`)
added an existing-resource handoff (`use_existing_cluster_vpc` /
`existing_cluster_vpc_id` / `create_roks_transit_gateway=false` /
`testing_create_client_vpc=false` from `cluster-outputs.json`). It
passed the hermetic test but the **live `!` verify came back RED**.
That fix is necessary-but-insufficient and the per-resource-toggle
approach is the wrong model. **Do not just add more toggles.**

Read first, in order:
1. `issues/issue_sprint16_validator.md` — §"Issue 2 — live `!` verify
   result: RED — reopened & expanded" (the authoritative live evidence
   + corrected scope) and the original Issue 2 + the round-1 closure
   section above it. Also new **Issue 4** (e2e-driver teardown) in the
   same file — you must fix that too (see Task B).
2. `prompts/sprint16/followup-issue2-staff.md` — what round 1 did
   (the code is on `main`; keep what helps, change the model).
3. `prompts/sprint16/followup-issue2-README.md` — decisions 3/4/5 still
   bind (live-`!`-gated close; no pre-existing `_test.go` edits;
   `internal/orchestration` ⊄ `internal/cli`).

## The live evidence (run-id `20260519-181511`)

Cluster phase succeeded (`Apply complete! Resources: 72 added`). The
round-1 Go handoff fired (`→ Second-phase handoff: reusing
cluster-phase VPC … (cluster-outputs.json)`). `use_existing_cluster_vpc`
worked — `…cluster.ibm_is_vpc.cluster_vpc[0]` did **not** recreate. But
the second/bnk phase apply still failed re-creating the whole
cluster-shared network:

- `module.roks_cluster.module.cluster.ibm_is_subnet.cluster_subnet_zone{1,2,3}[0]`
- `module.roks_cluster.module.cluster.ibm_is_public_gateway.cluster_gateway_zone{1,2,3}[0]`
- `module.roks_cluster.module.cluster.ibm_tg_gateway.transit_gateway[0]` (the `create_roks_transit_gateway=false` toggle did **not** suppress it)
- `module.testing.ibm_is_vpc.client_vpc[0]` (the `testing_create_client_vpc=false` toggle did **not** suppress it)
- `module.testing.ibm_is_subnet.cluster_jumphost_subnet["ca-tor-{1,2,3}"]`
- `module.testing.ibm_is_security_group.cluster_jumphost_sg[0]`

**Conclusion:** the `up` second phase re-runs the *entire* root
terraform config — including `module.roks_cluster` + `module.testing`
cluster-shared resources — against its own separate state, so it
duplicates the cluster-shared network the cluster phase already built.
Chasing per-resource "use existing" flags across two modules is a
losing game.

## Task A — the corrected fix (design, don't toggle-chase)

Investigate the actual two-phase architecture and fix it at the right
level. Key code to read and reason about:
- The root terraform: `terraform/main.tf` (how `module.roks_cluster`,
  `module.testing`, and the bnk modules — `cert_manager`/`flo`/
  `cne_instance`/`license` — are wired; what `deploy_bnk` /
  `create_roks_cluster` gate).
- The phase split in Go: how the cluster phase forces
  `deploy_bnk=false` (grep `deploy_bnk`, `cluster phase`,
  `forced`), and how the second/bnk phase invokes terraform
  (`internal/orchestration/{lifecycle,cluster}.go`,
  `internal/cli/{cluster_phase,bnk_phase}.go`, `internal/tf/*`).
- What `cluster-outputs.json` already carries
  (`internal/config/cluster_outputs.go`: `cluster_id`, `vpc_id`,
  `region`, `resource_group_id`, `registry_cos_*`, `master_url`, …).

The intended end state: **the second/bnk phase must not create or
manage the cluster-shared network at all** (cluster VPC, cluster
subnets, cluster public gateways, transit gateway, and the
`module.testing` client VPC / jumphost subnets / jumphost SG). It should
deploy only the bnk-trial layer onto the already-provisioned cluster,
consuming the cluster's identity from `cluster-outputs.json` (and/or
terraform `data` sources keyed off it). Evaluate and pick among, e.g.:

- (preferred if viable) the second phase **targets only the bnk-layer
  modules** / sets the gates so `module.roks_cluster` + the
  cluster-network parts of `module.testing` are not in its plan at all,
  consuming cluster identity via data sources from `cluster-outputs.json`;
- a single shared state across both phases (no second independent
  state to diverge);
- if some `module.testing` jumphost layer legitimately belongs to the
  bnk phase, make it reference the existing cluster network by
  data-source, never create it.

Pick the lowest-risk design that makes a full `roksbnkctl up` (cluster
phase then bnk phase, fresh workspace) apply cleanly with **zero
duplicate-name collisions**, and keep the cluster-only and bnk-only
sub-flows working. Round 1's `use_existing_cluster_vpc` plumbing may be
kept if it still serves the chosen design, or removed if the design
makes it moot — your call; document it.

In your issue-file writeup, state the architecture you chose, the
options rejected and why, and trace: fresh `up` → cluster phase creates
the cluster-shared network → bnk phase plan contains **no**
`module.roks_cluster`/cluster-network create, only bnk-layer + data
lookups → no collision.

## Task B — fix the e2e-driver teardown (Issue 4) — REQUIRED

The verify loop is unusable until this is fixed: a live verify that
gets past the cluster phase strands a billing ROKS cluster, because
`scripts/e2e-phase-handoff.sh`'s `teardown()` runs only `roksbnkctl
down` (trial phase). Patch `teardown()` to also run `roksbnkctl
cluster down --auto -w "$WORKSPACE" --var-file "$TFVARS"` (after the
trial down), tolerate either being a no-op, and add a post-teardown
assertion that **no** `canada-*` VPC / `canada-roks-tgw` /
`canada-roks` cluster remains (fail loudly otherwise). This file is
normally validator-owned; for this round you own this one function
(coordination note — touch only `teardown()` + its helpers).

## Constraints

- No pre-existing `_test.go` edits (additive new tests welcome — e.g.
  assert the bnk-phase plan/tfvars contains no `module.roks_cluster`
  cluster-network create when `cluster-outputs.json` exists).
- `internal/orchestration` must not import `internal/cli`.
- Cluster-only and bnk-only sub-flows must stay working; fresh-workspace
  `up` unchanged except that the bnk phase no longer re-declares
  cluster-shared infra.
- **Do not commit.** Integrator commits. Do not tag anything — `v1.6.2`
  was reverted; closure is gated on a fresh live `!` verify (hermetic
  GREEN is proven insufficient for this issue).

## Verify before reporting

- `go build ./...`, `go vet ./...`, `gofmt -l internal/` → 0,
  `go test ./...` green (record exact denied command if sandboxed —
  do not fake).
- `bash -n scripts/e2e-phase-handoff.sh` clean; show the new
  `teardown()` does both phase downs + the residual assertion.
- Static dataflow trace for Task A as described above.

## Issue file

Append to `issues/issue_sprint16_staff.md` a `## Issue 2 (round 2) —
corrected phase-architecture fix` section: chosen design + rejected
options + files changed + the Task B teardown fix + verification (or
denied-command record). Do not delete prior content. Note closure stays
gated on the integrator's fresh live `!` verify.

## Final report

≤250 words: the architecture you chose and why (vs round-1 toggles),
the exact code/terraform changes, the Task B teardown fix, test
results (or denied record), and the static trace showing the bnk phase
no longer plans any cluster-shared create. State you did not commit.
