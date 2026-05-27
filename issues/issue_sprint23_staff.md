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

`Status: open`.

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
**Status**: open

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
