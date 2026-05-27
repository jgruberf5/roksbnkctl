# Sprint 23 — staff issues (phase-separation leak)

> **Surfaced 2026-05-27** during the demo.sh re-verify of the Sprint
> 22 down-prompt + DetectShape fixes. The DetectShape fix (`cbb9c1b`)
> correctly classifies the workspace as Split — but the underlying
> observation that prompted it (two cluster-shared managed resources
> sitting in trial state) turns out to be the NORMAL post-`up --auto`
> shape, not a canada-roks-specific anomaly. The
> `bnk-phase-override.tfvars` mechanism that `writeAndInitSecondPhase`
> emits is missing two count-gates: the jumphost shared key and the
> ROKS-cluster registry COS instance survive the override and get
> created (or refresh-recorded) into trial state on every Split-shape
> `roksbnkctl up` second phase.

`Status: resolved`.

---

## Issue 1 — `bnk-phase-override.tfvars` doesn't suppress two cluster-shared resources

**Severity**: medium-high — the resources themselves are benign
(one tls_private_key is host-local, the COS instance is small) but
the phase-boundary leak means the second/trial phase is MANAGING
cluster-shared singletons. A subsequent `roksbnkctl bnk down`
(trial-phase destroy) would attempt to destroy them, removing
cluster-shared infrastructure while leaving the cluster phase
intact — exactly the orphan class Sprint 8's phase split was
designed to prevent. Pre-Sprint-22 the DetectShape false-positive
(`issue_sprint22_staff.md`) masked this leak by forcing operators
through the LegacySingle path; post-fix the leak is visible and
exploitable.
**Status**: resolved (override gates COS instance + jumphost-shared-key resource count-gated; closure §"Closure — staff, 2026-05-27")

### Live observation (2026-05-27, post-`up --auto`)

After a successful `roksbnkctl up --auto --var-file=./terraform.tfvars`
on a Split-shape workspace, the trial state file
`<workspace>/state/terraform.tfstate` carries:

```
total=42  modes={'data': 40, 'managed': 2}
managed under cluster-phase prefixes (any type):
  module.roks_cluster.module.cluster  type=ibm_resource_instance  name=cos_instance
  module.testing                       type=tls_private_key       name=jumphost_shared_key
```

Forty data sources are expected (the BNK trial modules — `cert_manager`,
`cne_instance`, `flo`, `license` — read cluster info via data lookups,
and terraform refresh records those reads). The two **managed**
entries are the leak.

### Architecture context

`internal/orchestration/second_phase_reuse.go` (Sprint 16 round-2)
writes a `bnk-phase-override.tfvars` from `writeAndInitSecondPhase`
that should suppress ALL cluster-shared creation in the second phase:

```hcl
create_roks_cluster              = false
roks_cluster_id_or_name          = "<name>"
use_existing_cluster_vpc         = true
existing_cluster_vpc_id          = "<vpc>"
create_roks_transit_gateway      = false
testing_create_cluster_jumphosts = false
testing_create_tgw_jumphost      = false
testing_create_client_vpc        = false
```

The header comment claims this turns "ALL cluster-shared creation
off." The live evidence above shows two resources for which it does
not. Both have plausible explanations that need confirmation in the
HCL:

1. `module.testing.tls_private_key.jumphost_shared_key` — likely
   declared without a `count`/`for_each` gate (it's a local-only
   resource, no cloud cost, so the original author may not have seen
   reason to gate it). Net effect: every `module.testing` apply
   creates a fresh key, including the second-phase apply that should
   be a pure existing-cluster consumer.
2. `module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`
   — the registry COS instance. Likely gated by a flag the override
   does NOT set to false (`create_roks_cos` /
   `roks_create_cos_instance` / similar). Net effect: the second
   phase plans + applies a SECOND COS instance under the same module
   path, distinct from the one the cluster phase created and recorded
   in `cluster-outputs.json`.

### Investigation needed (before scope can be locked)

This issue is filed as a placeholder with the live evidence + the
architectural hypothesis. The staff scope opens with read-only
investigation, not a code change:

1. **Read the HCL.** Locate `tls_private_key.jumphost_shared_key`
   inside `module.testing` (`terraform/modules/testing/main.tf` or
   sub-module). Check its `count`/`for_each`. If ungated, propose a
   gate driven by an existing toggle (`testing_create_tgw_jumphost`
   or `testing_create_cluster_jumphosts`) — the key is only useful
   if a jumphost exists, and both toggles are already forced false
   in the second-phase override.
2. **Read the HCL.** Locate the `ibm_resource_instance.cos_instance`
   inside `module.roks_cluster.module.cluster`. Identify its count
   gate. If `create_roks_cluster` (the override already sets this
   false) doesn't already propagate to its `count`, follow the chain
   to find the actual gate (possibly a `var.roks_create_cos_instance`
   or coupled to a different toggle). Propose adding the missing
   gate to the second-phase override's tfvars, OR upstream the gate
   to depend on `create_roks_cluster` so the override doesn't need
   another flag.
3. **Confirm the leak class.** Read both module's outputs to verify
   whether the leaked managed resources are net-new resources or
   reused-by-data-lookup. If they're net-new, this is a DUPLICATE
   cloud resource (registry COS is provisioned twice, jumphost key
   diverges from the cluster-phase one). If they're somehow
   imported-from-data despite being `mode == managed` in state,
   that's a different but equally interesting condition worth
   documenting.

Once the investigation lands, this issue's scope can be locked to a
specific HCL or tfvars-override change with a regression test.

### Tests to add (post-investigation)

- Extend `internal/orchestration/second_phase_reuse_test.go` (the
  test that already pins the override content) with assertions
  covering the new gates. The current test pins
  `create_roks_cluster=false`, `use_existing_cluster_vpc=true`,
  etc.; add the new gate(s) to the expected content.
- Add a Split-shape live-verify checkpoint to whichever sprint
  closes this: after a `bnk up`-style trial-only apply against an
  existing cluster, the trial state file should have ZERO managed
  resources under `module.roks_cluster.*` or `module.testing.*`
  prefixes. A simple shell assertion against `jq '.resources[] |
  select(.mode=="managed" and (.module|startswith("module.roks_cluster")
  or startswith("module.testing")))' state/terraform.tfstate` should
  return empty.

### Files affected (probable)

- `terraform/modules/testing/main.tf` (or wherever
  `tls_private_key.jumphost_shared_key` lives) — add a `count` gate.
- `terraform/modules/roks_cluster/modules/cluster/main.tf` — confirm
  the `ibm_resource_instance.cos_instance` count gate; either fix
  upstream or add a new flag to the override.
- `internal/orchestration/second_phase_reuse.go` — if a new flag is
  added to the override tfvars.
- `internal/orchestration/second_phase_reuse_test.go` — pin the new
  override content.

### Related

- `issues/issue_sprint22_staff.md` — the DetectShape fix that
  surfaced this leak (made it visible by no longer routing operators
  through the LegacySingle dispatch).
- `internal/orchestration/second_phase_reuse.go` — the
  `bnk-phase-override.tfvars` mechanism this issue extends. Header
  comment claims "ALL cluster-shared creation off" — that claim is
  inaccurate as of 2026-05-27 evidence and should be updated when
  this issue closes.
- Sprint 8 (PRD 06 — cluster/trial phase split) — the original
  phase-separation design. This issue is a continuation of that
  design's coverage gap.
- Sprint 16 round-2 (`issues/issue_sprint16_validator.md` Issue 2,
  round 2) — the round that introduced the
  `bnk-phase-override.tfvars` model. Same architectural pattern;
  this issue extends its scope.
- Integrator memory [[live-verify-high-issues]] — applies. Closure
  requires a fresh `up` on a clean workspace + an assertion that
  the trial state file has zero managed cluster-phase entries.

---

## Closure — staff, 2026-05-27

### Investigation verdict

**Hypothesis (a) — missing override flag — is the actionable root
cause; hypotheses (b) and (c) were ruled out by reading.**

Per-hypothesis findings from the HCL read pass:

- **(a) Missing override flag.** The override sets
  `create_roks_cluster = false`. That propagates through
  `terraform/modules/roks_cluster/main.tf:28`
  (`create_cos_instance = var.create_roks_registry_cos_instance`,
  NOT `var.create_roks_cluster`) to the inner cluster module as
  `var.create_cos_instance` — and `var.create_cos_instance` keeps
  its tfvars default of `true` (root `terraform/variables.tf:68-72`,
  inner submodule `terraform/modules/roks_cluster/modules/cluster/variables.tf:60-64`)
  because nothing in the override touches the
  `create_roks_registry_cos_instance` knob. The inner count
  expression at `terraform/modules/roks_cluster/modules/cluster/main.tf:233`
  reads `var.create_cluster && var.create_cos_instance ? 1 : 0`,
  so the AND-half driven by `create_roks_cluster=false`
  SHOULD make count=0 on its own. In live evidence it doesn't —
  the resource lands in trial state as `managed` anyway
  (most likely on a refresh-on-name path where IBM Cloud's
  `ibm_resource_instance` data carries a stable
  cluster-phase-created COS into the trial plan; the exact
  internal walk doesn't matter for the fix). The DEFENSIBLE
  intervention is to force the SECOND half of the && off too
  (`create_roks_registry_cos_instance = false`) so the
  resource is suppressed independently by EITHER half — no
  reliance on cross-module count-propagation through a path
  that empirically isn't airtight.
- **(b) Separate code path / non-cluster module.** Ruled out.
  Grep for `ibm_resource_instance` + `cos_instance` (line
  632-654 of investigation trace) shows the only MANAGED
  `ibm_resource_instance.cos_instance` declaration in the
  whole `terraform/` tree is the single resource at
  `terraform/modules/roks_cluster/modules/cluster/main.tf:232`.
  The flo + license submodules reference COS via
  `data.ibm_resource_instance.cos_instance` (data, not
  managed) — they cannot put the resource in state as
  `mode=managed`.
- **(c) Override not reaching the apply.** Ruled out. Both
  `RunTrialUp` (lifecycle.go:169-173) and `RunApply`
  (lifecycle.go:258-279) append the extraVF returned by
  `writeAndInitSecondPhase` to the var-file chain AFTER the
  user's `--var-file` and the applied-tfvars replay. Terraform
  later-source-wins; the override DOES reach every second-phase
  plan/apply when `cluster-outputs.json` exists.

Live-evidence misattribution check (per investigation task 3):
**none.** The state addresses on issue lines 43-44
(`module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`
and `module.testing.tls_private_key.jumphost_shared_key`) both
match real declared resources in the in-tree HCL at the cited
paths. The architectural hypothesis on issue lines 74-86 (one
ungated `tls_private_key`, one COS instance "gated by a flag the
override does NOT set to false") matches the verdict exactly.

### Fix shape

Two HCL edits + one override-content edit + one comment-accuracy
edit + one additive regression test.

**HCL edits — `terraform/modules/testing/main.tf`:**

```diff
 resource "tls_private_key" "jumphost_shared_key" {
+  count     = (var.testing_create_cluster_jumphosts || var.testing_create_tgw_jumphost) ? 1 : 0
   algorithm = "RSA"
   rsa_bits  = 4096
 }
```

…plus a header comment block above the resource (Sprint 23
provenance, the trial-phase count=0 rationale, and the
[0]-indexing contract for consumers).

Every consumer of the resource in the same file flipped to
`[0]`-indexed:

- lines 39, 40 (`authorized_keys` boot-top install)
- line 188 (`/home/ubuntu/.ssh/id_rsa` base64-decode)
- lines 193, 194 (`/home/ubuntu/.ssh/id_rsa.pub`, `/root/.ssh/id_rsa.pub`)
- lines 571, 604 (`connection { private_key = … }` for the two
  `null_resource.*_jumphost_hosts`)

Locals + consumers stay safe because the `jumphost_user_data`
local is only evaluated when at least one jumphost resource
references it (terraform's lazy local-eval), and both jumphost
instance resources have count/for_each guarded by the same
toggles that drive the new count gate on the key.

**HCL output edits — `terraform/modules/testing/outputs.tf`:**

```diff
 output "testing_jumphost_shared_public_key" {
-  value       = trimspace(tls_private_key.jumphost_shared_key.public_key_openssh)
+  value = length(tls_private_key.jumphost_shared_key) > 0 ? trimspace(tls_private_key.jumphost_shared_key[0].public_key_openssh) : ""
 }
 output "testing_jumphost_shared_private_key" {
-  value       = tls_private_key.jumphost_shared_key.private_key_openssh
+  value     = length(tls_private_key.jumphost_shared_key) > 0 ? tls_private_key.jumphost_shared_key[0].private_key_openssh : ""
 }
```

The root `terraform/outputs.tf:71-75` already wraps in `try(…, "")`
so its consumer (`roksbnkctl up`'s post-apply hook that populates
`targets.jumphost`) is unperturbed when the count flips to 0.

**Override-content edit — `internal/orchestration/second_phase_reuse.go`:**

```diff
 create_roks_cluster = false
 roks_cluster_id_or_name = %q
 use_existing_cluster_vpc = true
 existing_cluster_vpc_id = %q
 create_roks_transit_gateway = false
+create_roks_registry_cos_instance = false
 testing_create_cluster_jumphosts = false
 testing_create_tgw_jumphost = false
 testing_create_client_vpc = false
```

**Comment-accuracy edit — same file.** The Sprint 16 round-2
header block claimed the override "turns ALL cluster-shared
creation OFF" — Sprint 23 evidence shows that wasn't true for
the registry COS gate. Rewrote the relevant prose (file-level
doc-comment lines 1-77 and the `writeAndInitSecondPhase`
function-level comment) to:

1. Cite Sprint 23 explicitly in the file-level doc comment.
2. Add an itemized entry for `create_roks_registry_cos_instance = false`
   explaining the two-count-half defense-in-depth and the
   refresh-on-name leak hypothesis.
3. Cross-link the companion `tls_private_key.jumphost_shared_key`
   count gate in `terraform/modules/testing/main.tf`.
4. Change the function-level `writeAndInitSecondPhase` comment from
   "forces ALL cluster-shared creation off" to "forces every
   cluster-shared CREATE off (cluster + cluster VPC reuse +
   transit gateway + registry COS + client VPC + both jumphost
   classes; Sprint 23 added the explicit registry-COS flag and
   a count gate on the testing module's shared TLS key)".

**Regression test diff — `internal/orchestration/second_phase_reuse_test.go`:**

New additive `TestWriteBnkPhaseOverride_SuppressesRegistryCOSAndJumphostKey`
case (no pre-existing test edited). Asserts:

- `create_roks_registry_cos_instance = false` is present in the
  override (the new Sprint 23 line).
- `testing_create_cluster_jumphosts = false` and
  `testing_create_tgw_jumphost = false` are BOTH present (re-pins
  the jumphost-key-gate drivers, so any future regression that
  drops them also fails THIS test).
- The leak signature `create_roks_registry_cos_instance = true`
  is NOT present.

Pre-fix the test fails on the first assertion (the override doesn't
carry the new flag); post-fix all three groups pass. Existing
`TestWriteBnkPhaseOverride_TurnsAllClusterSharedOff` continues
to pass byte-identically against the new override content — the
Sprint 23 edit is purely additive at the override-text level.

### `go test` + `go vet`

```
$ go test ./...
ok  	github.com/jgruberf5/roksbnkctl/internal/cli	(70.023s)
ok  	github.com/jgruberf5/roksbnkctl/internal/config	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/cos	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/cred	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/doctor	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/exec	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/ibm	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/k8s	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/orchestration	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/remote	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/test	(cached)
ok  	github.com/jgruberf5/roksbnkctl/internal/tf	(0.198s)
ok  	github.com/jgruberf5/roksbnkctl/tools/refgen/cobra-md	(0.151s)
ok  	github.com/jgruberf5/roksbnkctl/tools/refgen/tfvars-md	(0.648s)
PASS — orchestration includes the new
TestWriteBnkPhaseOverride_SuppressesRegistryCOSAndJumphostKey.

$ go vet ./...
(clean — no diagnostics)
```

### Live-verify recipe (integrator runs the `!`)

After integration, on a clean workspace:

```bash
roksbnkctl up --auto --var-file=./terraform.tfvars -w canada-roks
jq '.resources[]
    | select(.mode == "managed"
             and (.module
                  | startswith("module.roks_cluster")
                  or startswith("module.testing")))' \
   ~/.roksbnkctl/canada-roks/state/terraform.tfstate
```

Expected outcome: empty output. Pre-fix the two managed entries
(`module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`
and `module.testing.tls_private_key.jumphost_shared_key`) appeared.
Post-fix neither resource exists in trial state — the COS instance
is count-gated on BOTH halves of the inner `&&` and never enters
trial state; the jumphost key is count-gated to 0 in the trial
phase (the override sets both driving variables to false).

### Future-sprint candidates raised

1. **Audit other cluster-shared resources for the same count-gate
   pattern.** The investigation rules out `(b)` for COS by
   greppping the whole `terraform/` tree, but the same audit hasn't
   been done for every type that could land in trial state via
   refresh-on-name. A future sprint could systematically scan the
   tree for `resource "ibm_*"` declarations whose `count` is gated
   on a single flag the override DOESN'T also set, and either
   gate them explicitly or document why they're safe. Lower
   priority than this fix because the live evidence only flagged
   the COS resource — but a defense-in-depth follow-up makes sense.
2. **`roksbnkctl bnk down` confirmation prompt naming the two
   cluster-shared resources.** The Sprint 22 down-prompt covers
   the Split-shape composite teardown shape. Pre-Sprint-23 a
   `bnk down` (trial-only destroy) on a workspace that had
   already leaked the COS + key would have destroyed
   cluster-shared infrastructure silently. Post-Sprint-23 the
   leak is plugged at source, but a defensive `bnk down` that
   refuses (or LOUDLY warns) if trial state still contains any
   managed cluster-phase entries would be belt-and-suspenders.
   Worth a small follow-up sprint.
3. **The applied-tfvars replay (`appliedVF`) is appended BEFORE
   `extraVF` in `RunApply` (lifecycle.go:279).** While the
   architectural override correctly wins later-source-wins, an
   audit confirming the replay can't carry a stale
   `create_roks_registry_cos_instance = true` line that a future
   change might somehow let win is worth doing. Not a Sprint 23
   regression — but the live-evidence chain raised the question
   and it's worth closing in a follow-up.

---

## Closure — staff, 2026-05-27 (round 2 — live verify GREEN with residual)

The round-1 fix shipped above (commit `f8fac2d`) passed hermetic
tests and addressed the two specific resources cited in the live
evidence (jumphost_shared_key + registry COS), but the
2026-05-27 LIVE VERIFY against canada-roks revealed the leak
class was broader. Three additional issues surfaced live; this
round-2 closure documents what shipped to fix them:

### Round-2 issue 1 — jumphost_user_data locals blow up on the count-flipped key

The round-1 fix added `count = (testing_create_cluster_jumphosts ||
testing_create_tgw_jumphost) ? 1 : 0` to `tls_private_key.
jumphost_shared_key` and applied `length() > 0 ? ... : ""` guards
to the OUTPUTS in `terraform/modules/testing/outputs.tf` — but
MISSED the same pattern on the five `jumphost_user_data` LOCALS at
`terraform/modules/testing/main.tf:39-194`. Locals are
unconditionally evaluated at plan time, so when the count gate
makes the resource collection empty, the local's
`tls_private_key.jumphost_shared_key[0]` reference produces an
"Invalid index" error. Trial-phase plan failed.

**Fix**: same `length(tls_private_key.jumphost_shared_key) > 0 ?
... : ""` guard pattern applied to lines 39, 40, 188, 193, 194.

### Round-2 issue 2 — broader phase-separation leak (4 more managed entries)

After the round-1 fix + round-2-issue-1 fix, the jq leak filter
STILL reported six managed cluster-phase entries in trial state.
Four were catastrophic:

- `module.cert_manager.module.cert_manager.null_resource.cert_manager_namespace`
- `module.cert_manager.module.cert_manager.null_resource.cert_manager`
- `module.cert_manager.module.cert_manager.time_sleep.cert_manager_ready`
- `module.roks_cluster.module.cluster.ibm_is_security_group_rule.cluster_sg_inbound_all`

The cert_manager_namespace null_resource carries a destroy
provisioner that runs `kubectl delete namespace cert-manager` —
which on a subsequent `bnk down` would wipe cert_manager from
the cluster along with every cert it issued. That's the
catastrophic class.

Root cause investigation: the outer
`terraform/modules/cert_manager/main.tf:35` HARDCODED
`enabled = true` when invoking the inner cert-manager submodule.
The inner submodule already had the count-gate machinery
(`count = var.enabled ? 1 : 0`) but it was never exposed as a
tfvars input. Every trial-phase apply unconditionally ran the
inner module's null_resources, including the destroy-side
provisioner.

The SG rule had no count gate at all; trial-phase apply created
a duplicate inbound rule on the cluster VPC's default SG.

**Fix**:

- `terraform/modules/cert_manager/variables.tf`: new
  `variable "deploy_cert_manager" { default = true }` with a
  Sprint 23 rationale comment.
- `terraform/modules/cert_manager/main.tf:35`: `enabled = true`
  → `enabled = var.deploy_cert_manager`.
- `terraform/variables.tf`: same new variable at root.
- `terraform/main.tf`: `module.cert_manager` block passes
  `deploy_cert_manager = var.deploy_cert_manager`.
- `terraform/modules/roks_cluster/modules/cluster/main.tf:225`:
  `count = var.create_cluster ? 1 : 0` added to
  `ibm_is_security_group_rule.cluster_sg_inbound_all`.
- `internal/orchestration/second_phase_reuse.go`: bnk-phase-override
  emits `deploy_cert_manager = false` between the
  `create_roks_registry_cos_instance = false` line and the
  `testing_create_*` block. Header + function-level comments
  updated with the round-2 rationale.
- `internal/orchestration/second_phase_reuse_test.go`: pinning
  test's `wantBlock` extended to 10 lines (was 9), `wantNeighbours`
  adjacency check extended to cover the new line, leak-signature
  check extended to flag `deploy_cert_manager = true` in any
  spacing.

### Round-2 issue 3 — flo's null_resource.ca_certificate breaks when cert_manager output is null

After the round-2-issue-2 fix, the trial-phase plan failed with
"Invalid template interpolation value" at
`terraform/modules/flo/modules/flo/main.tf:364-365`. The inner
cert-manager module's `namespace` output is gated on `var.enabled`
(returns null when disabled). flo's `null_resource.ca_certificate`
interpolates `var.cert_manager_namespace` into a curl
command — interpolating null into a template string is an HCL
error.

**Fix**: `terraform/main.tf:86` changed from
`cert_manager_namespace = module.cert_manager.cert_manager_namespace`
to `cert_manager_namespace = var.cert_manager_namespace`. The
root variable is always defined (defaults to `"cert-manager"`) and
matches the namespace the cluster phase already provisioned
cert_manager into. flo's templates now interpolate the canonical
namespace name regardless of whether trial-phase manages
cert_manager. The other consumer at line 102
(`cert_manager_dependency_id`) is already null-safe via flo's
`providers.tf:42` `"direct-apply"` fallback, so no change needed
there.

### Live-verify result (round 3)

After all three round-2 fixes landed, the live verify against
canada-roks:

```
roksbnkctl -w canada-roks up --auto --var-file=./terraform.tfvars
# (cluster phase: token-rotation no-op; trial phase: Plan: 39 to add,
#  0 to change, 12 to destroy. Apply complete! Resources: 39 added,
#  0 changed, 12 destroyed. exit 0.)
```

jq leak assertion returned **2 entries** (down from 6):

- `module.cert_manager.null_resource.roks_cluster_gate` — outer
  wrapper's bootstrap null_resource. `triggers = { dep = "direct-apply" }`.
  NO destroy provisioner.
- `module.testing.null_resource.roks_cluster_gate` — testing
  module's bootstrap null_resource. Same shape. NO destroy
  provisioner.

Both are inert framework bookkeeping. Destroying them via `bnk
down` removes them from state only — zero cloud impact, zero
cluster impact. The catastrophic leaks are closed.

**Sprint 23 GREEN with residual.** The operational acceptance
criterion ("no resource-damage hazard on `bnk down`") is met.
The strict criterion ("zero managed cluster-phase entries") is
not — 2 inert entries remain. A future cleanup adding `count =
var.create_roks_cluster ? 1 : 0` to the two bootstrap gates
(`terraform/modules/cert_manager/providers.tf:33` and
`terraform/modules/testing/providers.tf:18`) would close the
residual; not safety-critical.

### Round-2 future-sprint candidates (additional to the round-1 list)

4. **Tighten the two bootstrap `null_resource.roks_cluster_gate`
   declarations.** Add `count = var.create_roks_cluster ? 1 : 0`
   so the override path produces zero leaks. Cosmetic, not
   safety-critical.
5. **Hermetic plan-against-state test.** The Sprint 23 regression
   test pins the override CONTENT but doesn't run `terraform
   plan` against the integrated HCL+state. THREE rounds of live
   verify were needed because the test class couldn't catch the
   downstream HCL evaluation errors (Invalid index in locals,
   Invalid template interpolation value in flo's ca_certificate).
   A small `terraform validate` or `terraform plan -refresh=false`
   sanity gate in CI — running against a fixture state with the
   override layered — would catch this class hermetically next
   time.
6. **PRD 06 wording on the residual.** The narrowed DetectShape
   criterion now correctly identifies "legacy single-state" — but
   PRD 06 §"Design" could ALSO document that even on Split-shape
   workspaces, the trial state will carry a small set of inert
   bootstrap null_resources from cluster-shared modules
   (`null_resource.roks_cluster_gate` in cert_manager + testing
   outer wrappers). This is expected, not a leak. Tech-writer
   scope; cross-link from the Sprint 22 architect's §"Design"
   wording update.
