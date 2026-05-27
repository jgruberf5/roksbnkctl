# Sprint 23 — tech-writer issues (post-integration drift sweep)

> **Sprint 23 frame.** Tech-writer runs **after** the integrator
> has landed staff + architect + validator to `main` at commit
> `f8fac2d` (sprint23: three-way integration — phase-separation
> leak closed). Six drift sweeps across the integrated tree plus
> GREEN/RED launch verdict with the deferred live verify and the
> `v1.7.1` combined release-cut framing explicitly called out.
> Mirrors the shape of `issues/issue_sprint22_tech-writer.md`
> (commit `00f1e0f`) — same section headers, same
> Verdict-not-Status convention per Sprint 21's `a2b78da` rename.

**Status**: resolved

---

## Issue 1 — Post-integration drift sweep for Sprint 23

**Severity**: medium-high (mirrors the staff issue — the
phase-separation leak is a `roksbnkctl bnk down` resource-damage
hazard; drift in the override surface or its tests would mask a
silent regression of exactly the orphan class Sprint 8's phase
split was designed to prevent).

**Status**: resolved

### Drift surfaces walked

1. Override content (`writeBnkPhaseOverrideAt` in
   `internal/orchestration/second_phase_reuse.go`) vs.
   validator's `TestWriteBnkPhaseOverride_Sprint23ByteIdenticalBlock`
   regression test — byte-identical parity.
2. `second_phase_reuse.go` header-comment accuracy — the old
   "ALL cluster-shared creation off" claim is no longer in the
   file; new prose explicitly itemises the registry-COS gate
   and the testing-module shared-TLS-key count gate.
3. `docs/PRD/06-CLUSTER-TRIAL-PHASE-SPLIT.md` §"Design"
   wording vs. shipped DetectShape heuristic in
   `internal/config/tfstate.go` lines 78–235.
4. `book/src/31-building-from-source.md` §"The bundled tools
   images" vs. on-disk `tools/docker/` tree + `tools/docker/Makefile`
   targets + `.github/workflows/tools-images.yml` matrix and
   triggers.
5. Staff's HCL count gate downstream-reference audit —
   `terraform/modules/testing/main.tf` references to
   `tls_private_key.jumphost_shared_key` (seven sites) +
   `terraform/modules/testing/outputs.tf` (two sites) +
   the `module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`
   site (one `[0]` reference at `terraform/modules/roks_cluster/modules/cluster/main.tf:257`).
6. Live-verify recipe in validator's closure file
   (`issues/issue_sprint23_validator.md`) for the integrator's
   `!` invocation.

### Closure — tech-writer, 2026-05-27

Six drift sweeps run against the integrated tree at commit
`f8fac2d`. Findings summary: **0 high, 0 medium, 0 low**. All
six sweeps CLEAN. Three future-sprint candidates surfaced for
the integrator (carried forward from validator's closure;
non-blocking). Launch verdict GREEN; `v1.7.1` combined cut is
now unblocked (Sprint 22 + 23 ship together) pending the
integrator's `!` live-verify invocation post-merge.

---

#### Drift sweep #1 — override content vs. validator's byte-identical block

**Verdict**: CLEAN — the override emitted by
`writeBnkPhaseOverrideAt` matches validator's pinned 9-line
block byte-for-byte.

Source-of-truth (override generator at
`internal/orchestration/second_phase_reuse.go:196–204`):

```
create_roks_cluster = false
roks_cluster_id_or_name = %q
use_existing_cluster_vpc = true
existing_cluster_vpc_id = %q
create_roks_transit_gateway = false
create_roks_registry_cos_instance = false
testing_create_cluster_jumphosts = false
testing_create_tgw_jumphost = false
testing_create_client_vpc = false
```

The `%q` slots take `clusterIdentity(co)` (cluster ID or name)
and `co.VPCID`. With validator's fixture
(`ClusterID: "crt-cluster-id"`, `VPCID: "r038-ef6305af-vpc"`)
the rendered substitution becomes:

```
roks_cluster_id_or_name = "crt-cluster-id"
existing_cluster_vpc_id = "r038-ef6305af-vpc"
```

Validator's `wantBlock` constant at
`internal/orchestration/second_phase_reuse_test.go:260–268` is
this exact 9-line sequence with single-LF separators. Adjacency
check at lines 280–282 pins the new Sprint 23 line between
`create_roks_transit_gateway = false` (line 200 of the generator)
and `testing_create_cluster_jumphosts = false` (line 202),
matching the generator's emit order exactly.

Staff's companion test
`TestWriteBnkPhaseOverride_SuppressesRegistryCOSAndJumphostKey`
(lines 145–204) uses per-line `strings.Contains` and asserts:
the new `create_roks_registry_cos_instance = false` line is
present; the two jumphost-key-driver lines
(`testing_create_cluster_jumphosts = false` and
`testing_create_tgw_jumphost = false`) are present; the leak
signature `create_roks_registry_cos_instance = true` is absent.

Pre-existing `TestWriteBnkPhaseOverride_TurnsAllClusterSharedOff`
(lines 59–117) uses `strings.Contains` on each of the eight
pre-Sprint-23 lines; the additive Sprint 23 line is purely
additive at the override-text level, so the pre-existing test
continues to pass byte-identically against the new content
(parity discipline preserved).

#### Drift sweep #2 — header-comment accuracy on `second_phase_reuse.go`

**Verdict**: CLEAN — the inaccurate "ALL cluster-shared
creation off" claim is gone; the new prose accurately enumerates
the registry-COS gate + the testing-module shared-TLS-key gate.

File-level doc-comment audit
(`internal/orchestration/second_phase_reuse.go:1–88`):

- Line 3 names Sprint 23 explicitly:
  "Sprint 23 phase-separation-leak follow-up in
  issues/issue_sprint23_staff.md".
- Lines 51–62 add the itemised
  `create_roks_registry_cos_instance = false` entry explaining
  the defense-in-depth two-count-half rationale and the
  2026-05-27 live-evidence trigger.
- Lines 63–72 add the count-gate cross-link for
  `tls_private_key.jumphost_shared_key` in
  `terraform/modules/testing/main.tf` (the
  `(testing_create_cluster_jumphosts || testing_create_tgw_jumphost)`
  driving expression).
- Lines 74–78 close with the corrected net-effect claim — "the
  second/bnk phase plan contains the bnk-layer modules
  (cert_manager / flo / cne_instance / license) + existing-cluster
  DATA lookups ONLY — no module.roks_cluster / module.testing
  cluster-shared CREATE at all." No surviving "ALL cluster-shared
  creation off" wording.

Function-level audit at `writeAndInitSecondPhase`
(`internal/orchestration/second_phase_reuse.go:107–116`):

- The function-level comment has been rewritten from "forces ALL
  cluster-shared creation off" to "forces every cluster-shared
  CREATE off (cluster + cluster VPC reuse + transit gateway +
  registry COS + client VPC + both jumphost classes; Sprint 23
  added the explicit registry-COS flag and a count gate on the
  testing module's shared TLS key)".

Function-level audit at `writeBnkPhaseOverrideAt`
(`internal/orchestration/second_phase_reuse.go:178–210`) — the
header block at lines 184–195 (embedded in the generated
override file itself) cites Sprint 23 by name: "Second/bnk-phase
override (issues/issue_sprint16_validator.md Issue 2, round 2;
Sprint 23 phase-separation-leak follow-up — added the explicit
create_roks_registry_cos_instance gate)." Operator-visible
provenance for anyone reading the generated tfvars.

Grep for stale "ALL cluster-shared creation" wording across the
repo: zero hits in active source — only historical mentions in
prior sprint closure files (`issues/issue_sprint23_staff.md:56`
quoting the pre-fix claim it tracked, and the prior tech-writer
closure under `.archive/` if any). No stale callout in any
in-tree code/comment surface.

#### Drift sweep #3 — PRD 06 §"Design" alignment with shipped DetectShape

**Verdict**: CLEAN — PRD 06 wording matches the shipped
`trialStateHasClusterModules` doc-comment and filter logic
filter-by-filter.

Source-of-truth (`internal/config/tfstate.go:180–235`):
`trialStateHasClusterModules` requires all three filters:

1. `r.Mode == "managed"` (line 225) — discards data-source
   refreshes.
2. `r.Type == "ibm_container_vpc_cluster"` (line 225) — pins to
   the singular ROKS cluster marker.
3. `r.Module == prefix || strings.HasPrefix(r.Module, prefix+".")`
   (line 229) — the trailing-dot guard prevents
   `module.roks_cluster_extras` from false-matching.

PRD 06 audit:

- Site 1 — signals table at line 83 names the signal "Trial state
  owns the ROKS cluster" (narrower than the pre-edit "contains
  cluster modules") with the inline three-AND filter spelled out
  in the "How" cell: `mode == "managed"` AND
  `type == "ibm_container_vpc_cluster"` AND module address
  matches one of `module.roks_cluster`, `module.cert_manager`,
  `module.testing`. Matches the shipped filter list exactly.
- Site 2 — post-table prose at lines 87–93 expands into three
  bullets, one per filter, each explaining *why* the filter is
  necessary and what would over-classify if it were dropped. The
  third bullet (line 93) names the trailing-dot guard explicitly
  and uses `module.roks_cluster_extras` as the example — matches
  the doc-comment example in `tfstate.go:202–203` verbatim.
- The authoritative-implementation pointer at line 85
  ("`trialStateHasClusterModules` in `internal/config/tfstate.go`")
  matches the function name and file path one-to-one.

The `clusterPhaseModules` list itself
(`tfstate.go:85–89` — three entries: `module.roks_cluster`,
`module.cert_manager`, `module.testing`) appears verbatim in the
PRD signals row and the bullet rationale. No drift.

The closing canada-roks paragraph (PRD lines downstream of the
filter rationale) was preserved verbatim per the architect's
closure §"Sections deliberately left untouched", maintaining the
"we ran this against real data" provenance.

#### Drift sweep #4 — chapter 31 §"The bundled tools images" alignment with on-disk tree

**Verdict**: CLEAN — chapter 31 tree listing, `make` examples,
and CI trigger description all match the post-Sprint-22 state of
`tools/docker/` + `.github/workflows/tools-images.yml`.

On-disk verification:

```
$ ls /mnt/c/project/roksbnkctl/tools/docker/
Makefile
ibmcloud
iperf3
mdbook
```

Four entries (Makefile + three image dirs). Chapter 31 tree
listing at lines 97–105 shows all three image dirs (ibmcloud/,
iperf3/, mdbook/) with `# roksbnkctl-tools-mdbook (release-time
book builder)` comment matching the other entries' style.

Makefile target verification:

```
$ grep -E "^(build|clean)" /mnt/c/project/roksbnkctl/tools/docker/Makefile
build-ibmcloud:
build-iperf3:
build-mdbook:
build-all: build-ibmcloud build-iperf3 build-mdbook
clean:
```

Chapter 31 `make` examples at lines 111–114 use
`make build-ibmcloud` / `make build-iperf3` / `make build-mdbook`
/ `make build-all` — each target exists in the Makefile
verbatim. The pre-Sprint-23 chapter wording (`make ibmcloud` /
`make iperf3` / `make all` — which would have failed with "No
rule to make target") is gone.

Workflow trigger verification
(`.github/workflows/tools-images.yml:19–34`):

```
on:
  push:
    tags: ['v*']
    branches: [main]
  workflow_dispatch:
...
    strategy:
      fail-fast: false
      matrix:
        image: [ibmcloud, iperf3, mdbook]
```

Chapter 31 paragraph at line 119 names the matrix
(`strategy.matrix.image` over `[ibmcloud, iperf3, mdbook]`) and
the three trigger paths (main push → `:dev`, `v*` tag push →
`:<tagname>` + `:latest`, `workflow_dispatch` → `:dev`). All
three match the workflow shape. The phrase "`mdbook` image was
folded into the matrix in Sprint 22" is verbatim-style-matched
to `CONTRIBUTING.md:344–350` (per architect's closure
§"Cross-doc consistency check"); a reader hitting both surfaces
gets a consistent story.

The chapter's pre-Sprint-23 single-sentence trigger description
("on a tag push or when `tools/docker/**` changes" — factually
wrong on both axes: no `paths:` filter in the workflow; elided
`:dev` on main push; elided `workflow_dispatch`) is gone.

#### Drift sweep #5 — staff's HCL count gates handle downstream references

**Verdict**: CLEAN — every downstream reference to
`tls_private_key.jumphost_shared_key` is `[0]`-indexed and gated
either by a `length(...) > 0` guard (outputs) or by an upstream
count/for_each on the consumer that pins the consumer to
count=0 when the same drivers are false. The
`ibm_resource_instance.cos_instance` site already had its `[0]`
indexing pre-Sprint-23 (staff did NOT touch its upstream gate;
they added the override flag instead).

`tls_private_key.jumphost_shared_key` references —
seven sites in `terraform/modules/testing/main.tf`:

| Line | Context | Gate / guard |
|------|---------|--------------|
| 39   | `local.jumphost_user_data` boot-top `authorized_keys` install for `/home/ubuntu/.ssh` | `local` only evaluated when a jumphost resource references it; both jumphost classes (`null_resource.cluster_jumphost_hosts` for_each and `null_resource.tgw_jumphost_hosts` count) are gated on the same drivers as the key. Safe. |
| 40   | Same local, `/root/.ssh/authorized_keys` install | As above. |
| 188  | Same local, `/home/ubuntu/.ssh/id_rsa` base64-decode | As above. |
| 193  | Same local, `/home/ubuntu/.ssh/id_rsa.pub` write | As above. |
| 194  | Same local, `/root/.ssh/id_rsa.pub` write | As above. |
| 571  | `null_resource.cluster_jumphost_hosts.connection.private_key` | Parent uses `for_each = ibm_is_floating_ip.cluster_jumphost_fip` (line 560); the FIP itself is gated by `testing_create_cluster_jumphosts` upstream. When the override forces that var false, the for_each map is empty → zero null_resource instances → `[0]` reference never evaluated. Safe. |
| 604  | `null_resource.tgw_jumphost_hosts.connection.private_key` | Parent gated by `count = var.testing_create_tgw_jumphost ? 1 : 0` (line 593). When the override forces that var false, count=0 → `[0]` reference never evaluated. Safe. |

Two sites in `terraform/modules/testing/outputs.tf`:

| Line | Context | Guard |
|------|---------|-------|
| 16   | `output "testing_jumphost_shared_public_key"` | `length(tls_private_key.jumphost_shared_key) > 0 ? trimspace(...[0]...) : ""`. Defensive guard handles count=0. Safe. |
| 23   | `output "testing_jumphost_shared_private_key"` | `length(tls_private_key.jumphost_shared_key) > 0 ? ...[0]... : ""`. As above. |

Root-level wrap at `terraform/outputs.tf:71–75` already used
`try(..., "")` pre-Sprint-23, so the consumer
(`roksbnkctl up`'s post-apply hook that populates
`targets.jumphost`) is unperturbed when the count flips to 0.

`module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`
references — staff explicitly did NOT touch the upstream count
gate per the closure §"Investigation verdict" hypothesis (a).
Instead they added `create_roks_registry_cos_instance = false`
to the override so the resource is suppressed independently by
EITHER half of the inner `&&` count gate at
`terraform/modules/roks_cluster/modules/cluster/main.tf:233`.
The single downstream `[0]` reference at
`terraform/modules/roks_cluster/modules/cluster/main.tf:257`
(`cos_instance_crn = var.create_cos_instance ? ibm_resource_instance.cos_instance[0].crn : null`)
is itself ternary-guarded on `var.create_cos_instance` — when
the override forces that var false, the `[0]` reference is
short-circuited away by the ternary. Safe.

No HCL count-gate consumer drift. Staff's investigation chain
holds end-to-end.

#### Drift sweep #6 — live-verify recipe documented in validator's closure

**Verdict**: CLEAN — validator's closure documents the `jq` +
expected outcome verbatim for the integrator's `!` invocation.

Recipe location in `issues/issue_sprint23_validator.md`:

- Primary copy at §"Live-verify recipe (integrator runs via `!`
  — DO NOT execute here)" (lines 111–128 of the closure file).
- Closure summary copy at §"Closure — validator, 2026-05-27"
  (lines 266–273) — the same recipe, byte-identical, so the
  integrator can copy from either site.

Recipe shape (both copies, byte-identical):

```bash
roksbnkctl -w canada-roks up --auto --var-file=./terraform.tfvars
jq '.resources[] | select(.mode == "managed" and (.module | startswith("module.roks_cluster") or startswith("module.testing")))' \
  ~/.roksbnkctl/canada-roks/state/terraform.tfstate
```

Expected outcome: zero output (empty jq result) — documented
explicitly at validator closure line 130–138 and 266–273. The
expected-outcome prose names BOTH leak sites by full address
(`module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`
and `module.testing.tls_private_key.jumphost_shared_key`), so
non-empty output would identify which leak class survived the
fix — the operator-debuggability the staff issue called for.

Rollback path (validator closure §"Rollback path (if live verify
surfaces a NEW leak class)", lines 148–179):

- Step 1 names the `bnk down` hazard explicitly and refuses it
  (`bnk down` would destroy leaked managed cluster-shared
  resources alongside the BNK trial; the resource-damage class
  this sprint exists to prevent).
- Step 2 mandates `roksbnkctl down` (full teardown) as the safe
  path because it owns both states.
- Step 3 defers `v1.7.1` to a Sprint 24 staff issue with the
  verbatim jq output, matching the
  [[no-piling-into-active-release]] memory.
- Step 4 names the investigation chain (read count gates →
  propagation → either upstream fix or new override flag).

Staff's own §"Live-verify recipe (integrator runs the `!`)"
(`issues/issue_sprint23_staff.md:363–384`) mirrors the same
recipe and expected outcome — two copies on independent surfaces
guard against either closure drifting. Cross-checked
byte-for-byte: same `jq` query, same expected outcome (empty),
same workspace fixture (canada-roks). Consistent.

---

#### Findings summary

| # | Severity | Surface | Verdict |
|---|----------|---------|---------|
| — | — | — | No findings; all six sweeps CLEAN. |

No high / medium / low findings. No blocker to Sprint 23
closure or the combined `v1.7.1` cut.

---

#### Follow-up candidates flagged for the integrator (future sprints)

Carried forward from the staff + validator closures (the
architect closure raised no future-sprint candidates beyond the
already-Sprint-23-scoped chapter 31 + PRD 06 follow-ups). All
non-blocking; the integrator decides whether to dispatch any of
these as Sprint 24 scope.

1. **Defense-in-depth count-gate audit across the
   `terraform/` tree** (staff §"Future-sprint candidates raised"
   item 1). The Sprint 23 investigation ruled out a second
   COS-instance code path by greppping the whole tree, but
   didn't systematically scan every `resource "ibm_*"`
   declaration for the same "count gated on a single flag the
   override doesn't also set" pattern. A future sprint could
   scan + either explicitly gate or document why each is safe.
   Lower-priority than this sprint's fix (live evidence only
   flagged COS + the TLS key) but a defensive follow-up matches
   the [[argv-strictness-prevents-resource-damage]] discipline
   pattern.
2. **`roksbnkctl bnk down` defensive guard** (staff item 2).
   Pre-Sprint-23 a `bnk down` on a workspace that had already
   leaked the COS + key would have destroyed cluster-shared
   infrastructure silently. Post-Sprint-23 the leak is plugged
   at source, but a defensive `bnk down` that REFUSES (or
   LOUDLY warns) if trial state still contains any managed
   cluster-phase entries would be belt-and-suspenders. Worth
   a small follow-up sprint — same defensive class as Sprint
   22's down-prompt fix.
3. **Applied-tfvars replay precedence audit** (staff item 3).
   `RunApply` (`internal/cli/lifecycle.go:279` per staff's
   investigation trace) appends `appliedVF` BEFORE `extraVF` in
   the var-file chain — architecturally correct
   (later-source-wins gives the override precedence) but worth
   an audit confirming `appliedVF` can't carry a stale
   `create_roks_registry_cos_instance = true` line that a
   future change might let win. Not a Sprint 23 regression but
   the investigation chain raised the question.
4. **`roksbnkctl doctor --phase-leak-check` subcommand**
   (validator §"Future-sprint candidates" item 1). The closure's
   `jq` invocation is documented prose; a future sprint could
   add a native subcommand that runs the equivalent on the
   active workspace's trial state file and exits non-zero on
   any managed match. That would let the tech-writer's drift
   sweep + integrator's `!` invocation converge on a single
   command. Sprint 23 explicitly out-of-scope per `internal/cli/`
   being settled in Sprint 22.
5. **Three-test fixture helper consolidation** (validator item
   2). The three Sprint 23 orchestration tests
   (`TurnsAllClusterSharedOff`,
   `SuppressesRegistryCOSAndJumphostKey`,
   `Sprint23ByteIdenticalBlock`) all stub the same
   `config.ClusterOutputs` fixture (`canada-roks` /
   `crt-cluster-id` / `r038-ef6305af-vpc`). A private helper
   (`fixtureCanadaROKSOutputs()`) would DRY them. Deliberately
   not done in Sprint 23 (parity discipline forbade touching
   pre-existing tests). Sprint 24+ micro-refactor.
6. **`terraform validate` CI smoke gate** (validator item 3).
   Staff added `[0]` indexes to seven `module.testing`
   references + two outputs. A `terraform validate` smoke test
   in CI (against the embedded `terraform/` tree) would catch
   a missed `[0]` regression cheaply. Today validation only
   happens at `up` time on live cloud. Sprint 24+ architect
   candidate.

None of items 1–6 block Sprint 23 closure or the combined
`v1.7.1` cut.

---

#### Launch verdict: **GREEN** — `v1.7.1` combined cut unblocked

Sprint 23's three deliverable streams — staff (HCL count gate
on `tls_private_key.jumphost_shared_key` +
`create_roks_registry_cos_instance = false` override flag +
header-comment accuracy edit + additive regression test);
architect (PRD 06 §"Design" narrowed-criterion wording +
chapter 31 §"The bundled tools images" drift sweep); validator
(byte-identical-block regression test + documented live-verify
recipe + rollback path) — are **integrated and drift-clean** at
commit `f8fac2d`. All six sweeps CLEAN: override + test byte
parity (#1); header comment now accurate (#2); PRD 06 matches
DetectShape doc comment filter-by-filter (#3); chapter 31 tree
+ Makefile targets + workflow triggers all match on-disk (#4);
every `tls_private_key.jumphost_shared_key` downstream
reference is `[0]`-indexed and either guarded by `length() > 0`
or pinned to count=0 via an upstream consumer gate (#5);
validator's live-verify recipe documented verbatim with the
rollback path naming the `bnk down` hazard (#6).

**`v1.7.1` release-cut unblocked.** Sprint 22's staff fixes
(`18415eb` down-prompt + `cbb9c1b` DetectShape) shipped before
this sprint; Sprint 23's phase-separation-leak fix was the
final gate. Per the integrator's combined-cut framing
(`prompts/sprint23/README.md` integrator decision 4), both
sprints ship together under one combined `v1.7.1` tag + one
CHANGELOG entry covering down-prompt + DetectShape +
phase-separation fixes. This tech-writer closure makes no
specific `tag` / `release` / `CHANGELOG.md` recommendations —
those defer to the integrator's combined cut commit.

**Live verify deferred to integrator.** Per the
[[live-verify-high-issues]] memory and integrator decision 3 in
`prompts/sprint23/README.md`, the closure gate is a real-cloud
`up` + the `jq` assertion in validator's recipe. This
tech-writer verdict CERTIFIES HERMETIC COMPLETENESS — the
integrated tree is internally consistent, the documentation
matches the shipped code, the tests pin the shipped behaviour
— but it CANNOT certify the live verify pre-emptively. The
integrator runs the `!` invocation (validator's recipe at
`issues/issue_sprint23_validator.md:266–273`); a GREEN live
verify is the final precondition for the combined `v1.7.1` tag
cut. A non-empty `jq` output during live verify triggers the
rollback path in validator's closure §"Rollback path" (full
`roksbnkctl down`, NOT `bnk down`; file a Sprint 24 issue;
hold `v1.7.1`).

---

#### Discipline checks

- No commits, no `gh` invocations, no `git push`, no tag
  proposal. Integrator-owned per `prompts/sprint23/README.md`
  integrator decision 4 and the Sprint 23 constraints section.
- Read-only on all repo content except this file
  (`issues/issue_sprint23_tech-writer.md`).
- No edits to `internal/`, `cmd/`, `.github/workflows/`,
  `tools/docker/`, `CONTRIBUTING.md`, `book/src/`, `Makefile`,
  `docs/PRD/`, `docs/PLAN.md`, `CHANGELOG.md`, or `terraform/`.
- No edits to `issues/issue_sprint23_staff.md`,
  `issues/issue_sprint23_architect.md`, or
  `issues/issue_sprint23_validator.md` (other roles' closures;
  out of scope).
- No edits to `issues/issue_sprint24_staff.md` (forward
  placeholder; out of scope).
- Per-finding fields use `**Verdict**:`, not `**Status**:`
  (mirrors Sprint 21 `a2b78da` rename + Sprint 22 tech-writer
  file convention — the issue-level top-of-file `**Status**:`
  is reserved for `resolved/open`).
- Every drift sweep cites specific `file:line` ranges and a
  CLEAN / FINDING verdict; no sweep produced a finding so no
  severity tier escalation needed.
- GREEN/RED launch verdict explicitly mentions the `v1.7.1`
  combined-cut unblock state and the live-verify deferral per
  acceptance criterion 2.

### Related

- `prompts/sprint23/tech-writer.md` — this issue's source
  prompt.
- `prompts/sprint23/README.md` — integrator decisions
  (investigation-first staff scope; architect's two Sprint 22
  follow-ups; release tag is post-tech-writer + live-verify
  GREEN; `v1.7.1` combined cut unblocks here).
- `issues/issue_sprint23_staff.md` — staff closure audited in
  drift sweeps #1, #2, #5. Override content + header comment +
  HCL count-gate consumers all match the closure's
  §"Investigation verdict" + §"Fix shape" exactly.
- `issues/issue_sprint23_architect.md` — architect closure
  audited in drift sweeps #3, #4. PRD 06 + chapter 31 edits
  match the closure's §"Edits in `docs/PRD/06-…`" + §"Drift
  sites corrected" exactly.
- `issues/issue_sprint23_validator.md` — validator closure
  audited in drift sweeps #1, #6. Regression test + live-verify
  recipe + rollback path all match the closure exactly.
- `issues/issue_sprint22_tech-writer.md` (commit `00f1e0f`) —
  the shape this closure mirrors per `prompts/sprint23/tech-writer.md`
  §"Closure".
- Integrator memory [[live-verify-high-issues]] — applies. The
  hermetic completeness this verdict certifies is necessary
  but not sufficient; the `!` invocation is the closure gate.
- Integrator memory [[no-piling-into-active-release]] — applies
  to the rollback path documented in validator's closure (don't
  extend Sprint 23 scope mid-cycle if a new leak class
  surfaces during the live verify).
- Integrator memory [[argv-strictness-prevents-resource-damage]]
  — applies in spirit. The pre-Sprint-23 leak made
  `roksbnkctl bnk down` a silent resource-damage hazard against
  cluster-shared singletons; Sprint 23 plugs the leak at source
  so no `bnk down` can ever destroy the registry COS or the
  jumphost shared key.
- Integrator memory [[sprint-ledger-status-convention]] —
  applies to the top-of-file `**Status**: resolved` field on
  this issue (not the per-finding `**Verdict**:` fields, which
  follow the `a2b78da` rename).
