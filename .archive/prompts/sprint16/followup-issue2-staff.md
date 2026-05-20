You are the **staff engineer** agent for a Sprint 16 follow-up dispatch
on the `roksbnkctl` project. Repo root: `/mnt/c/project/roksbnkctl`.
You run with no memory of prior conversation — everything you need is
here or in the files named below.

## Read first (in order)

1. `issues/issue_sprint16_validator.md` — **Issue 2** is the bug you
   are fixing. Read its full root cause + evidence + "Files affected"
   + "Proposed fix".
2. `prompts/sprint16/followup-issue2-README.md` — the integrator
   decisions for this dispatch. Decisions 1, 4, 5 bind you.
3. The code: `terraform/modules/roks_cluster/main.tf` (the
   `module "cluster"` block, ~line 19), `terraform/main.tf` (the
   `module "roks_cluster"` block ~line 32 and `module "testing"`
   ~line 179), `terraform/variables.tf`,
   `terraform/modules/roks_cluster/modules/cluster/{main,variables,outputs}.tf`,
   `terraform/modules/testing/{main,data,variables}.tf`,
   `internal/tf/vars.go` (the second-phase tfvars renderer — see
   `create_roks_cluster`/`openshift_cluster_name` writes ~line 60),
   `internal/orchestration/lifecycle.go` (the `up` phase
   orchestration; grep `RunUp`, `ClusterOutputs`, `writeAndInit`,
   tfvars), `internal/cli/cluster_phase.go` (writes
   `cluster-outputs.json` via `config.WriteClusterOutputs`),
   `internal/config/cluster_outputs.go` (`ReadClusterOutputs`,
   `ClusterOutputs` has `VPCID`).

## The fix — complete the existing-resource handoff (two halves)

Per README decision 5, prefer wiring the existing reuse plumbing over a
new mechanism. There are two gaps; both must be closed or the bug
persists:

**Half A — terraform module passthrough.** The cluster submodule has
`use_existing_cluster_vpc` (default `false`) and
`existing_cluster_vpc_id`, with a `data.ibm_is_vpc.existing_cluster_vpc`
lookup and `count` toggles already implemented in
`modules/roks_cluster/modules/cluster/main.tf`. But the
`module "cluster"` block in `terraform/modules/roks_cluster/main.tf`
never passes them, and there is no root variable feeding them. Add the
root variables (`use_existing_cluster_vpc`, `existing_cluster_vpc_id`)
to `terraform/variables.tf`, pass them `module "roks_cluster"` →
`module "cluster"` (`terraform/main.tf` + `roks_cluster/main.tf`), and
verify the transit-gateway path has an equivalent reuse toggle (the
error also hit `ibm_tg_gateway.transit_gateway[0]` — if there is no
existing-TG path, add one symmetric to the VPC one, or make the second
phase not manage the TG; pick the smaller-surface option and document
it). Defaults must keep the **first/cluster** phase behavior identical
(create), i.e. default `use_existing_cluster_vpc = false`.

**Half B — Go phase handoff.** The second (bnk/testing) phase must, when
the workspace already has a `cluster-outputs.json`
(`config.ReadClusterOutputs` succeeds), render its tfvars with the
reuse toggles set: `use_existing_cluster_vpc = true`,
`existing_cluster_vpc_id = <outputs.VPCID>`, the transit-gateway reuse
equivalent, and `testing_create_client_vpc = false` +
`testing_client_vpc_name = <the client VPC name>` so
`module.testing` looks up the existing client VPC instead of creating
`client_vpc[0]`. Wire this in the tfvars render path
(`internal/tf/vars.go`) driven from the orchestration `up` second-phase
step (`internal/orchestration/lifecycle.go`), reading the outputs the
same way `internal/cli/cluster_phase.go` writes them. The first/cluster
phase must be unaffected (it creates; only the *second* phase, run when
cluster outputs already exist, reuses).

Evaluate the alternative — second phase does not re-apply the
infra-creating modules at all — and in your issue file state in 2–3
sentences why you chose the handoff approach (or the alternative if it
is genuinely smaller and safe).

## Constraints

- README decisions 1, 4, 5 bind you. **Do not edit any pre-existing
  `_test.go`** (parity guardrail). New unit tests are additive and
  welcome (e.g. assert the second-phase tfvars render contains
  `use_existing_cluster_vpc = true` + the `existing_cluster_vpc_id`
  when a `ClusterOutputs` is present, and does **not** when absent).
- `internal/orchestration` must never import `internal/cli`
  (one-directional boundary, asserted in Sprint 16). Pass any
  cli-resident collaborator as a function field / param, matching the
  existing `LifecycleInputs` shape.
- Keep first-phase (cluster) behavior byte-identical. The change is
  scoped to the *second* phase's tfvars when cluster outputs exist.
- Do **not** commit. The integrator commits. Do not push.

## Verify before reporting done

- `go build ./...`, `go vet ./...`, `gofmt -l internal/ | wc -l` → 0,
  `go test ./...` green (run from repo root; if `go test` is
  sandbox-denied, say so explicitly with the exact denied command — do
  not fake results, the Sprint 15/16 precedent is documented).
- `terraform validate` in `terraform/` if the toolchain is available;
  otherwise eyeball the module wiring for arity/type correctness.
- Trace the dataflow end to end in your report:
  `cluster-outputs.json.vpc_id` → `ReadClusterOutputs` →
  second-phase tfvars (`use_existing_cluster_vpc=true`,
  `existing_cluster_vpc_id=…`) → `module.roks_cluster` →
  `module.cluster` `data.ibm_is_vpc.existing_cluster_vpc` (count=1, no
  `ibm_is_vpc.cluster_vpc[0]` create) → no duplicate-name collision.

## Issue file

Write `issues/issue_sprint16_staff.md` (append a new
`## Issue 2 — phase-handoff fix` section if the file exists; do not
delete prior content). Schema: `**Severity**`, `**Status**`,
`**Description**` (what you changed, both halves), `**Files affected**`,
`**Approach chosen + why** (vs the no-re-apply alternative)`,
`**Verification**` (commands run + results, or denied-command record),
`**Related**` (links Issue 2 in the validator file).

## Final report

≤200 words: the two-half change, files touched, the end-to-end
dataflow trace, test results (or denied-command record), and any
risk/edge you want the integrator to know (e.g. the transit-gateway
reuse decision). State explicitly that you did not commit.
