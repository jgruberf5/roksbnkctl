# Sprint 16 — validator issues (consolidation phase-1b, post-v1.6.0)

> **Sprint 16 frame.** See `prompts/sprint16/README.md` + `docs/PLAN.md`
> §"Sprint 16". Headline gate = behavior parity (zero pre-existing
> test-file diffs vs the `v1.6.0` tag; Sprint 14 e2e/`--on` +
> Sprint 15 chokepoint guards green & **unedited**); full hermetic
> `go test -race ./...`; `cli` phase-1b boundary/import audit. If this
> agent's session is toolchain-denied (Sprint 15 validator Issue 1
> precedent), record a `blocker` with the exact denied commands and
> hand the gate to the integrator — do not fake results.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Behavior-parity gate + boundary audit — **integrator-run (validator/staff sessions toolchain-denied)**

`Status: resolved` — the Sprint 16 staff session had `go test` execution sandbox-denied (recorded in `issues/issue_sprint16_staff.md` §Closure, the documented Sprint 15 precedent); a separate validator agent would hit the same wall, so the integrator ran the full gate directly. **2026-05-19, all green:**

- **Behavior parity (HEADLINE):** `git diff --stat v1.6.0 -- <every _test.go tracked at v1.6.0>` → **empty** (zero pre-existing test-file diffs); Sprint 14 `lifecycle_e2e_test.go`/`lifecycle_e2e_integration_test.go`/`env_split_test.go` + Sprint 15 `chokepoint_guard_test.go` **byte-identical**. The only new test is the pre-existing `internal/orchestration/chokepoint_test.go` (already in v1.6.0). No pre-existing test edited.
- **Full hermetic `go test -race ./...`** (CI's exact command, `HOME`=empty, `KUBECONFIG` unset) → **all 14 packages `ok`, RACE_EXIT=0**, incl. `internal/cli` (thinned adapter), `internal/orchestration` (new home), and the Sprint 14/15 guards. `internal/test::TestProbe_TruncatedFlag` (pre-existing full-`-race` flake, refactor-untouched) did not recur.
- `go build ./...` / `go vet ./...` clean; `gofmt -l internal/` empty.
- **Boundary/import audit:** `internal/orchestration` does **not** import `internal/cli` (grep-clean — one-directional boundary held under the function-field dependency-injection shape); `internal/cli/lifecycle.go`+`cluster.go` are thin cobra adapters; `internal/orchestration/{lifecycle,cluster}.go` (≈64 KB) hold the moved RunE orchestration.
- Sprint 14 kubeconfig fix not regressed (`selfheal.go` untouched; e2e/`--on` guards green); Sprint 15 chokepoint guard green & unedited.

**Verdict: GREEN.** The phase-1b move is behavior-parity-proven at the test level, not just statically. Tag/version (`v1.6.1` strict-SemVer vs `v1.7.0`) is integrator-owned at cut.

---

## Issue 2 — `up` second phase recreates cluster VPC / transit gateway / client VPC → IBM Cloud duplicate-name failure (phase handoff incomplete)

**Severity**: high
**Status**: open

**Description.** Live `roksbnkctl up` on the `canada-roks` workspace
(2026-05-19, IBM provider 1.89.0) failed at the end with:

```
CreateVPCWithContext failed: Provided Name (canada-roks-vpc) is not unique   (module.roks_cluster.module.cluster.ibm_is_vpc.cluster_vpc[0])
A gateway with the same name already exists.                                 (module.roks_cluster.module.cluster.ibm_tg_gateway.transit_gateway[0])
CreateVPCWithContext failed: Provided Name (canada-j-vpc) is not unique      (module.testing.ibm_is_vpc.client_vpc[0])
```

The behavior-parity gate (Issue 1) is GREEN because the regression is
**not test-observable** — it only manifests against a workspace that has
already completed the cluster phase, which the hermetic suite never
exercises. This is the live-verify gap the memory note
(`live-verify-high-issues`) warns about.

Root cause — `up` applies the **same** `roks_cluster`/`testing`
terraform across two independent state files with **no existing-resource
handoff between them**:

| `~/.roksbnkctl/canada-roks/` state | serial | resources | cluster_vpc / transit_gateway / client_vpc tracked? |
|---|---|---|---|
| `state-cluster/terraform.tfstate` | 91 | 49 | **yes** (1 instance each — cluster phase created them, completed ✓) |
| `state/terraform.tfstate` | 6 | 2 | **no** (only `existing_*` data lookups, `instances=0`) |

The cluster phase created `canada-roks-vpc`, the `canada-roks` transit
gateway, and `canada-j-vpc` and tracks them. The second (bnk/testing)
phase runs the same modules against its own near-empty `state/`, plans
to **create** those same-named resources, and IBM Cloud rejects the
duplicates.

The reuse plumbing exists but is **not wired end to end**:
- `terraform/modules/roks_cluster/main.tf:19` — the `module "cluster"`
  block never passes `use_existing_cluster_vpc` /
  `existing_cluster_vpc_id`; submodule default is `false`
  (`modules/roks_cluster/modules/cluster/variables.tf:38`), so the
  module **always** plans to create the VPC + TG.
- No Go emits `use_existing_cluster_vpc` /
  `existing_cluster_vpc_id` / `testing_create_client_vpc` into the
  second phase's tfvars (zero non-test refs across `internal/**.go`;
  confirmed absent from `state/terraform.tfvars`).
- `cluster-outputs.json` **does** record `vpc_id`, so the handoff data
  is present but never consumed by the bnk phase.

Correlates with the sprint16 phase-1b lifecycle/cluster split (staff
Issue 1): the phase boundary was introduced without completing the
existing-VPC/TG/client-VPC handoff into the second phase.

**Files affected**:
- `terraform/modules/roks_cluster/main.tf` (≈line 19 — `module "cluster"` missing `use_existing_cluster_vpc`/`existing_cluster_vpc_id` passthrough)
- `terraform/modules/roks_cluster/modules/cluster/main.tf` (lines 41–88 — count toggle / data-vs-resource branch, correct but never reached)
- `terraform/modules/testing/{main.tf,data.tf}` (`client_vpc` create vs `existing_client_vpc` lookup; `testing_create_client_vpc`)
- `internal/orchestration/lifecycle.go` (the `up` phase orchestration that must emit the reuse toggles for the second phase)
- second-phase tfvars writer (`internal/cli/tfvars.go` / `internal/config/applied_tfvars.go`)

**Proposed fix.** Complete the handoff: after the cluster phase, the
bnk/testing phase must consume `cluster-outputs.json` and apply with
`use_existing_cluster_vpc=true` + `existing_cluster_vpc_id=<vpc_id>`
(and the equivalent transit-gateway + `testing_create_client_vpc=false`
/ `testing_client_vpc_name` reuse), **and** `roks_cluster/main.tf` must
actually pass those variables down into `module "cluster"`. Alternative
to weigh: the bnk phase should not re-run the infra terraform at all
when `cluster-outputs.json` already exists (single-state / skip-infra).
**Must be live-`!`-verified on `canada-roks` before close** — unit
parity will stay green regardless (`live-verify-high-issues`).

**Recovery for the stuck workspace** (separate from the fix):
`terraform import` the existing VPC / transit gateway / `canada-j-vpc`
into `state/terraform.tfstate` so the second phase stops planning their
creation.

**Related**: staff Issue 1 (phase-1b lifecycle+cluster move, commits
`e7cc7e7`/`99b45cc`/`ce35f09`); validator Issue 1 (parity GREEN — by
design blind to this); memory `live-verify-high-issues`.

### Issue 2 — validator closure (Sprint 16 follow-up dispatch)

`Status: open — pending live \`!\` verify`

Per README decision 3 and the `live-verify-high-issues` memory, a `high`
issue cannot be closed on unit/hermetic tests alone. The validator
delivered the regression test + the gated live-verify driver; the live
`!` run and the final flip to `resolved` are integrator/operator-owned.
**Do not mark Issue 2 `resolved` on the hermetic GREEN.**

**Hermetic regression test that would have caught it.**
`internal/tf/secondphase_handoff_test.go` (new, additive — no
pre-existing `_test.go` edited; `git diff --stat -- '*_test.go'` shows
only this untracked file; `vars_test.go` + the Sprint 14/15 guards
byte-identical to `v1.6.0`). It is the cross-agent seam named in
`tf.RenderTFVarsWithClusterOutputs`'s doc comment (staff owns the
renderer; validator owns the test). It asserts the second-phase tfvars
contract:

- `co != nil, VPCID != ""` → output carries `use_existing_cluster_vpc =
  true`, `existing_cluster_vpc_id = "<vpc id>"`,
  `create_roks_transit_gateway = false`, `testing_create_client_vpc =
  false`; explicitly fails if `use_existing_cluster_vpc = false` slips
  through (the duplicate-create signature); asserts no `api_key` leak.
- `co == nil` → byte-identical to `RenderTFVars` (first/cluster-phase
  create-path parity — keeps validator Issue 1's gate GREEN).
- `co != nil, VPCID == ""` → defensive: byte-identical create path (a
  half-written `cluster-outputs.json` must not flip reuse on with an
  empty id that would fail the submodule's `data.ibm_is_vpc` lookup).

Verified: `go test ./internal/tf/` PASS (3/3); full hermetic
`HOME=<tmp> go test -race ./...` → **all packages `ok`, RACE_EXIT=0**
(parity gate not regressed). `go test` was **not** sandbox-denied in
this session.

**Gated live-verify e2e driver.** `scripts/e2e-phase-handoff.sh` (new;
mirrors `e2e-test.sh` style — `set -euo pipefail`, colored
`log/green/red`, `DRY_RUN=1`, `LOG_DIR`, exits non-zero on first failed
assertion). Operator-run via `!`, **NOT CI** — no `.github/workflows`
added or modified (`git status -- .github/` clean), no
`workflow_dispatch`. Runs the real reproduction (`roksbnkctl up <ws>` —
cluster phase then bnk/testing phase) and asserts: A1/A1b cluster phase
created + tracked the VPC/TG and `cluster-outputs.json` carries
`vpc_id`; **A2** second-phase state does not *manage* a duplicate
`module.roks_cluster.module.cluster.ibm_is_vpc.cluster_vpc` /
`ibm_tg_gateway.transit_gateway` / `module.testing.ibm_is_vpc.client_vpc`
(a `data` lookup is fine); **A3** rendered second-phase
`terraform.tfvars` carries `use_existing_cluster_vpc = true`; **A4**
`up` exits 0 and the run log has no `is not unique` / `already exists`.
Self-tears-down via an EXIT trap (loud, best-effort). The API key is
never echoed/logged and is required from the env (not scraped from
`./terraform.tfvars`, whose contents are never printed).

Operator invocation (after the fix lands, before integrator closes):

```bash
DRY_RUN=1 ./scripts/e2e-phase-handoff.sh                 # plan-only self-check
IBMCLOUD_API_KEY=... ./scripts/e2e-phase-handoff.sh      # live; ~$5-8, ~70+ min
```

Verified by the validator: `bash -n scripts/e2e-phase-handoff.sh`
clean; `DRY_RUN=1` run prints the intended steps + assertions, exits 0,
makes no cloud call, and leaks no key (0 occurrences of a planted key
sentinel in stdout/stderr and in the run log). The validator did **not**
run the live driver (real spend; integrator/operator-owned) and did
**not** commit. `docs/E2E_TEST.md` gained a §"Phase-handoff regression
(Issue 2)" describing how/when an operator runs it and what GREEN means.

**Verdict: hermetic GREEN; Issue 2 stays `open — pending live \`!\`
verify`.** The integrator runs `scripts/e2e-phase-handoff.sh` against a
real account and only then flips Issue 2 to `resolved`.

### Issue 2 — live `!` verify result: **RED — reopened & expanded** (2026-05-19)

`Status: open (reopened — first fix attempt necessary-but-insufficient;
high)`

The integrator ran the live driver against a real account. **Run-id
`20260519-181511` came back RED.** The hermetic-GREEN first fix
(`27f7a02`) is broken in practice — exactly what the
`live-verify-high-issues` gate exists to catch.

**What the live run proved.** The cluster phase fully succeeded
(`Apply complete! Resources: 72 added`). The fix's Go path fired
(`→ Second-phase handoff: reusing cluster-phase VPC r038-ef6305af… +
transit gateway + client VPC (cluster-outputs.json)`). The VPC-reuse
part worked — `module.roks_cluster.module.cluster.ibm_is_vpc.cluster_vpc[0]`
is **absent** from the second-phase failure set. **But the second-phase
apply still failed**, re-creating everything the handoff model does not
cover:

| Re-created by 2nd phase → "is not unique" / "already exists" | Covered by `27f7a02`? |
|---|---|
| `…cluster.ibm_is_subnet.cluster_subnet_zone{1,2,3}` | ❌ not gated by any toggle |
| `…cluster.ibm_is_public_gateway.cluster_gateway_zone{1,2,3}` | ❌ not gated |
| `…cluster.ibm_tg_gateway.transit_gateway[0]` | ⚠️ `create_roks_transit_gateway=false` did **not** suppress it |
| `module.testing.ibm_is_vpc.client_vpc[0]` | ⚠️ `testing_create_client_vpc=false` did **not** suppress it |
| `module.testing.ibm_is_subnet.cluster_jumphost_subnet["ca-tor-{1,2,3}"]` | ❌ never in scope |
| `module.testing.ibm_is_security_group.cluster_jumphost_sg[0]` | ❌ never in scope |

**Corrected root cause / scope.** Issue 2's original analysis
under-scoped the defect. The second/bnk phase does not merely duplicate
the cluster *VPC* — it re-runs the **entire cluster-shared network**
(cluster subnets + cluster public gateways + transit gateway +
`module.testing` client VPC + jumphost subnets + jumphost SG) against
its own state. Per-resource "use existing" toggles are the wrong model:
`use_existing_cluster_vpc` only suppresses `ibm_is_vpc.cluster_vpc`;
the other two toggles staff added did **not** suppress their resources
(wiring or module-gating gap, live-confirmed). The correct fix direction
is for the second phase to **not manage the cluster-shared modules at
all** (consume the cluster identity from `cluster-outputs.json` via data
sources / a single shared state), not to chase a growing list of
per-resource reuse flags.

**The committed fix is invalid as a fix.** `27f7a02`'s code is retained
as a partial foundation (VPC reuse works), but the `v1.6.2`
CHANGELOG `### Fixed` claim was **reverted as premature** and the
PLAN §"Sprint 16" follow-up corrected. No `v1.6.2` tagged. Issue 2
**reopened/expanded**; staff **re-dispatched** for the corrected
(not-per-toggle) fix.

**Secondary defect found — e2e-driver teardown (filed as Issue 4).**
`scripts/e2e-phase-handoff.sh`'s EXIT trap runs `roksbnkctl down`
(trial phase only) and never `roksbnkctl cluster down`, so a run that
gets past the cluster phase **strands the entire cluster phase**
(this run left a running ROKS cluster + VPCs + TGW billing; the
integrator cleaned it via the intact `state-cluster` state). The driver
must `roksbnkctl cluster down` (and the trial down) in teardown before
it is safe to re-run for the corrected-fix verify.

---

## Issue 4 — `scripts/e2e-phase-handoff.sh` teardown only destroys the trial phase, strands the cluster phase

**Severity**: high
**Status**: open

**Description.** The Issue 2 live driver's self-teardown (`teardown()`
EXIT trap) runs `roksbnkctl down --auto -w <ws> --var-file <tfvars>`.
`roksbnkctl down` destroys only the **trial/bnk** phase (`state/`).
The cluster phase (`state-cluster/`, ~49 managed resources incl. the
ROKS cluster, both VPCs, the transit gateway) is destroyed by the
separate `roksbnkctl cluster down`, which the driver never calls. On the
2026-05-19 Issue 2 verify (run `20260519-181511`) the cluster phase
created 72 resources; the trap destroyed 3; a running ROKS cluster +
networking were left billing until the integrator manually tore them
down via the (luckily intact) `state-cluster` state. Any e2e run that
progresses past the cluster phase strands billable infra — directly
contrary to the driver's "self-tears-down so no billable infra is
stranded" guarantee.

**Files affected**: `scripts/e2e-phase-handoff.sh` (`teardown()` —
must also `roksbnkctl cluster down --auto -w <ws> --var-file <tfvars>`,
after the trial `down`, and verify both phases gone before declaring
teardown complete).

**Proposed fix.** In `teardown()`: run `roksbnkctl bnk down` (or the
existing `down`) then `roksbnkctl cluster down`, both `--auto`, tolerate
either being a no-op, and add a post-teardown assertion that no
`canada-*` VPC / `canada-roks-tgw` / `canada-roks` cluster remains
(fail loudly if so). Must be in place before the corrected Issue 2 fix
is live-verified, or every verify attempt strands a cluster.

**Related**: Issue 2 (its verify loop depends on this); the broader
`down` phase-awareness gap is the same family as Issue 3 (lifecycle
commands not doing what the operator reasonably expects).

---

## Issue 3 — `terraform.applied.tfvars` snapshot is write-only; `down`/`plan`/`apply -w <ws>` don't replay it, so they fail on required vars without `--var-file`

**Severity**: medium
**Status**: open

**Description.** Found in live use (2026-05-19) while tearing down the
`canada-roks` workspace. `roksbnkctl down -w canada-roks` failed; the
same command with `--var-file ./terraform.tfvars` succeeded.

Expected (operator mental model): once a workspace's cluster + bnk
phases are applied, the full terraform variable environment is
preserved in the workspace so later lifecycle ops (`down`, `plan`,
`apply`) against `-w <ws>` match the stored state without re-supplying
the original `--var-file`.

Actual: roksbnkctl writes `terraform.applied.tfvars` — a snapshot of
the var-file inputs terraform actually consumed on a *successful* apply
(`internal/config/applied_tfvars.go`, PRD 07 / Sprint 11) — but the
snapshot is **write-only**. `WriteAppliedTFVars` is called post-apply
(`internal/tf/terraform.go:291`); there is **no reader** anywhere in
the codebase (grep-confirmed — only the writer, the version-stamp
setter, and `internal/tf/terraform.go:325` which documents that destroy
deliberately does *not* touch it). `RunDown` →
`tfws.Destroy(ctx, in.VarFiles...)` feeds terraform only (a) the small
`config.yaml`-derived auto-rendered `state/terraform.tfvars` (~11 lines:
region, RG, `create_roks_cluster`, cluster name/version, workers, dirs),
(b) `terraform.tfvars.user` if present, and (c) the `--var-file` flags
passed *on that invocation*. `terraform destroy` still evaluates the
whole config, which has required no-default variables
(`ibmcloud_api_key`, plus the `testing_*` / `roks_*` / `f5_*` set) that
the auto-rendered subset does not contain → "No value for required
variable" failures. Supplying `--var-file ./terraform.tfvars` restores
the full set, so destroy proceeds.

Compounding (workspace-specific): only `state-cluster/` had a
`terraform.applied.tfvars` here; the bnk/testing phase never got one
because its apply failed on the Issue 2 duplicate-name error — so even
a hypothetical replay would have been incomplete for the second phase.

This is the designed contract today, not a crash bug: `down`/`plan`/
`apply -w <ws>` require the *same* `--var-file` used for `up`, and
`ibmcloud_api_key` is intentionally never persisted (secret; env /
`--var-file` only). But the tool already has a near-complete applied
snapshot on disk and the operator expectation that `-w <ws>` "just
works" is reasonable — PRD 07's snapshot appears to have been building
toward replay but the destroy/plan path was never wired to consume it.
Recurring-confusion-class UX gap in the same phase-lifecycle family as
Issue 2.

**Files affected**:
- `internal/config/applied_tfvars.go` (snapshot is written; no
  read/replay API)
- `internal/tf/terraform.go` (`:291` writes snapshot post-apply;
  `:325` destroy explicitly does not consume it)
- `internal/orchestration/lifecycle.go` (`RunDown`/`RunTrialDown` →
  `Destroy(in.VarFiles...)`; no applied-snapshot layering)
- `docs/prd/` PRD 07 (deployed-tfvars snapshot — intent vs the
  write-only reality)

**Proposed fix (not for the Issue 2 release — log only).** Either
(a) auto-layer the phase's `terraform.applied.tfvars` as a lowest-
precedence var-file in `down`/`plan`/`apply` when no explicit
`--var-file` is given (secret still required via env/flag), with a clear
"replaying applied snapshot from <path>" stderr line and a guard for the
missing-snapshot / failed-prior-apply case; or (b) if replay is
explicitly out of scope, make the bare `-w <ws>` failure a roksbnkctl-
level actionable error ("this workspace was applied with a --var-file;
re-supply it or run with --var-file <…>") instead of a raw terraform
"No value for required variable", and document the contract in
`docs/E2E_TEST.md` / the `down` help text. Decide (a) vs (b) as a
scoped follow-up; **must be live-`!`-verified** on a real
multi-var-file workspace before close (`live-verify-high-issues`) —
unit tests will not surface the required-var gap.

**Related**: Issue 2 (same phase-lifecycle/handoff family; the failed
bnk-phase apply is why no second-phase applied snapshot existed here);
PRD 07 (deployed-tfvars snapshot, Sprint 11); memory
`live-verify-high-issues`.
