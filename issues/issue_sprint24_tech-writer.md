# Sprint 24 — tech-writer issues (post-integration drift sweep, two-sprint coverage)

> **Sprint 24 frame.** Tech-writer runs **after** the integrator
> has landed staff + architect + validator to `main` at commit
> `c197d20` (sprint24: three-way integration — `roksbnkctl test
> hosts {list,add,remove,clear}` CLI). This pass covers **two
> sprints in one verdict** — the deferred Sprint 23 round-2
> tech-writer re-sweep folds in alongside the Sprint 24 surface.
> The Sprint 23 round-1 tech-writer GREEN at commit `158ae55`
> predated the round-2 architectural fix at `e4b7f7b` (`sprint23:
> round-2 architectural fix — close 4 catastrophic leaks
> live-verified GREEN`); rather than re-dispatching for Sprint 23
> alone, the re-sweep lands here. Five drift sweeps total + a
> single GREEN/RED launch verdict gating the combined `v1.7.1`
> release cut (Sprint 22 + 23 round-1 + 23 round-2 + 24 ship
> together). Mirrors the shape of `issues/issue_sprint23_tech-writer.md`
> (commit `158ae55`) — same section headers, same
> Verdict-not-Status convention per Sprint 21's `a2b78da` rename.

**Status**: resolved

---

## Issue 1 — Post-integration drift sweep for Sprint 24 (+ Sprint 23 round-2 re-sweep)

**Severity**: medium (the `test hosts` CLI is the most
operator-visible v1.7.1 addition; drift between chapter 20's
worked example and the binary's stdout/stderr would be the
first thing an operator hits searching for the "no hosts
configured" error. The Sprint 23 round-2 re-sweep also covers
the cert_manager destroy-provisioner leak class — already
plugged at source by the round-2 fix at `e4b7f7b` and
live-verified GREEN with 2 inert residuals documented).

**Status**: resolved

### Drift surfaces walked

#### Sprint 24 surfaces

1. New `test hosts` CLI surface in `book/src/20-connectivity-testing.md`
   §"Managing test hosts via the CLI" vs. the binary's stdout/stderr
   captured from the source-of-truth in `internal/cli/test_hosts.go`.
2. `internal/cli/test.go:803`'s "no hosts configured" error
   message — the staff edit from the YAML-editing prompt to the
   new `roksbnkctl test hosts add <url>` CLI pointer.

#### Sprint 23 round-2 re-sweep surfaces (deferred from the round-1 pass)

3. PRD 06 §"Design" wording vs. the post-round-2
   `trialStateHasClusterModules` heuristic in
   `internal/config/tfstate.go`. Round-2 staff only updated a
   doc-comment on the function (not the heuristic itself); the
   Sprint 23 round-1 architect rewrote the PRD §"Design" prose.
   This sweep re-confirms the round-1 rewrite still matches
   filter-by-filter against the shipped heuristic.
4. `book/src/31-building-from-source.md` chapter 31 §"The
   bundled tools images" vs. the on-disk `tools/docker/` tree +
   the post-Sprint-22 `.github/workflows/tools-images.yml` matrix.
   Round-2 didn't touch chapter 31; this sweep re-confirms the
   Sprint 23 round-1 architect's chapter-31 rewrite still matches
   on-disk reality.
5. `internal/orchestration/second_phase_reuse.go` header comment
   + override content vs. validator's pinning test
   (`TestWriteBnkPhaseOverride_Sprint23ByteIdenticalBlock`),
   including the round-2 `deploy_cert_manager = false` addition
   that closed the cert_manager destroy-provisioner cluster-damage
   hazard.

### Closure — tech-writer, 2026-05-27

Five drift sweeps run against the integrated tree at commit
`c197d20`. **Findings summary**: 0 high, 1 medium (sweep #1 —
the architect-flagged "illustrative" worked-example re-capture),
1 low (sweep #5 — stale `writeAndInitSecondPhase` function-level
docstring), 0 blocker. Both findings were anticipated by upstream
roles' closures and are non-blocking against the v1.7.1 cut.
Three future-sprint candidates surfaced for the integrator
(non-blocking). Launch verdict GREEN; combined `v1.7.1` cut
unblocked.

---

#### Drift sweep #1 — `test hosts` CLI worked example vs. binary stdout/stderr

**Verdict**: FINDING (medium) — the architect-flagged "illustrative"
worked-example output in `book/src/20-connectivity-testing.md`
lines 73–113 does not match the binary's actual log-line wording
byte-for-byte. The architect's lines 92–93 blockquote explicitly
called this out as the tech-writer's re-capture trigger; the
operator-impacting *shape* (empty list → zero bytes + exit 0;
idempotent `already present` log on duplicate add; confirmation
prompt on `clear`; `--auto` skips) is correct. The literal
prose-vs-binary wording deltas are the recommended edits.

**Authoritative source-of-truth**: `internal/cli/test_hosts.go`
(landed in `c197d20`). The `RunE` family writes to
`os.Stdout`/`os.Stderr` via `fmt.Fprintln` / `fmt.Fprintf`. The
output strings hard-coded into the binary:

- `runTestHostsAdd` (line 177): `test hosts: %q already present; no-op` to stderr (`%q` quotes the URL)
- `runTestHostsAdd` (line 181): `✓ added %q to test.connectivity.extra_hosts` to stderr
- `runTestHostsRemove` (line 198): `test hosts: %q not present; no-op` to stderr
- `runTestHostsRemove` (line 210): `✓ removed %q from test.connectivity.extra_hosts` to stderr
- `runTestHostsClear` prompt label (line 222): `Clear ALL test.connectivity.extra_hosts?` via `promptYesNo(..., false)`
- `runTestHostsClear` decline (line 223): `test hosts clear: declined; no changes` to stderr
- `runTestHostsClear` success (line 231): `✓ cleared test.connectivity.extra_hosts` to stderr
- `runTestHostsList` (line 150-152): `json.NewEncoder(os.Stdout)` + `enc.SetIndent("", "  ")` → multi-line pretty-printed JSON, not flat one-line

Chapter 20 lines 73–113 wording (illustrative, per the
architect's explicit lines 92–93 blockquote):

| Site | Chapter 20 prose | Binary actual |
|---|---|---|
| Line 79 | `added https://docs.f5.com` | `✓ added "https://docs.f5.com" to test.connectivity.extra_hosts` |
| Line 82 | `already present: https://docs.f5.com` | `test hosts: "https://docs.f5.com" already present; no-op` |
| Line 99 | flat single-line `["https://docs.f5.com","https://bigip-next-admin.example.com:8443"]` | pretty-printed multi-line (SetIndent("", "  ")) |
| Line 108 | `This will remove ALL configured test hosts (2 entries). Continue? [y/N]: y` | `Clear ALL test.connectivity.extra_hosts? [y/N]: ` (no entry-count preview) |
| Line 109 | `cleared 2 entries` | `✓ cleared test.connectivity.extra_hosts` |
| Line 112 | `cleared 0 entries` | `✓ cleared test.connectivity.extra_hosts` |

The architect's closure §"Edit 1" item 2 and §"Follow-up
candidates" item 1 both explicitly anticipated this re-capture
work. The chapter's blockquote at lines 92–93 reads:

> "the byte-for-byte output above is illustrative — it reflects
> the surface the Sprint 24 CLI ships and is what tech-writer's
> drift sweep will re-capture against the built binary before
> the `v1.7.1` cut. The shape is stable … minor wording deltas
> in the log lines may surface in the GREEN-verdict re-capture."

**Why this is FINDING, not blocker for v1.7.1**: the shape the
chapter pins is correct (the operator-impacting properties —
empty list → zero bytes + exit 0; idempotent already-present
log; confirmation defaults to No; `--auto` skip; insertion-order
stable JSON for non-empty; backed by `test.connectivity.extra_hosts`
in workspace YAML — all hold). The wording deltas are cosmetic
prose drift; they don't mis-document any behaviour. The
architect's blockquote primes the operator-reader that the
output may differ from a live run.

**Recommended integrator action**: a small post-v1.7.1 follow-up
sprint (or a docs-only commit before the cut, integrator's call)
swaps the illustrative log lines for the binary's actual output
and drops the lines 92–93 blockquote — the
architect's §"Follow-up candidates" item 1 names this path
explicitly. Not gating the GREEN verdict; the binary's surface
itself is correct.

Validator's hermetic test suite
(`internal/cli/test_hosts_test.go`, 12 sub-cases, all PASS at
`c197d20`) pins the shape the chapter's blockquote promises is
stable, so this finding is documentation-prose-only — the
operator-correctness path is solid.

#### Drift sweep #2 — `internal/cli/test.go:803` error message points at the new CLI

**Verdict**: CLEAN — the pre-Sprint-24 YAML-editing prompt has
been replaced with the post-Sprint-24 CLI pointer per the
integrator decision in `prompts/sprint24/README.md` decision 4.

Pre-Sprint-24 source (per `issues/issue_sprint24_staff.md`
§"Out of scope" and §"Files affected" → "the YAML path"):

```
no hosts configured to probe; add to test.connectivity.extra_hosts in config.yaml
```

Post-Sprint-24 source-of-truth (`internal/cli/test.go:803`):

```go
return nil, nil, fmt.Errorf("no hosts configured to probe; add via `roksbnkctl test hosts add <url>`")
```

Match the required substring `roksbnkctl test hosts add` — ✓.
Backticks around the command match the existing in-error style
convention one line above (`internal/cli/test.go:799`):
`"workspace %q is not initialised; run `+"`"+`roksbnkctl init`+"`"+` first"`.
Operator hitting the error from `test connectivity` or no-flag
`test dns` sees a direct CLI path, not a YAML-editing breadcrumb.

Cross-references that quote the new error message verbatim:

- `book/src/21-dns-testing-gslb.md:466` (architect's Edit 4) —
  cross-link paragraph in §"Integration with `extra_hosts`"
  quoting `no hosts configured to probe; add via roksbnkctl test
  hosts add <url>` literally. Matches the binary's format
  string (modulo the backticks the binary emits and the prose
  doesn't render — operator-search-friendly substring still
  matches).
- `book/src/20-connectivity-testing.md:71` (architect's Edit 1
  lead paragraph) — quotes the error string verbatim with the
  backticks around `roksbnkctl test hosts add <url>`. Matches
  the binary byte-for-byte.

Staff closure (`issues/issue_sprint24_staff.md` §"Files touched"
item 3) documents the edit and the in-error style precedent. No
drift.

#### Drift sweep #3 — PRD 06 §"Design" alignment with post-round-2 DetectShape

**Verdict**: CLEAN — PRD 06 wording matches the shipped
`trialStateHasClusterModules` doc-comment and filter logic
filter-by-filter. The Sprint 23 round-1 architect's rewrite still
holds; round-2's staff comment-update on the heuristic did not
change the criterion.

Source-of-truth (`internal/config/tfstate.go:180–235`):
`trialStateHasClusterModules` requires all three filters:

1. `r.Mode == "managed"` (line 225) — discards data-source refreshes.
2. `r.Type == "ibm_container_vpc_cluster"` (line 225) — pins to
   the singular ROKS cluster marker.
3. `r.Module == prefix || strings.HasPrefix(r.Module, prefix+".")`
   (line 229) — the trailing-dot guard prevents
   `module.roks_cluster_extras` from false-matching.

PRD 06 re-audit (`docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md`):

- Signals-table row at line 83 names the signal "Trial state owns
  the ROKS cluster" with all three filters spelled out in the
  "How" cell: `mode == "managed"` AND `type ==
  "ibm_container_vpc_cluster"` AND module address matches one of
  `module.roks_cluster`, `module.cert_manager`, `module.testing`.
- Authoritative-implementation pointer at line 87:
  "`trialStateHasClusterModules` in `internal/config/tfstate.go`".
- Three post-table bullets (lines 89, 91, 93) — one per filter,
  each naming why the filter is necessary and what would
  over-classify if it were dropped. Bullet 3 (line 93) names the
  trailing-dot guard explicitly and uses `module.roks_cluster_extras`
  as the example — matches the doc-comment example in
  `tfstate.go:201–203` verbatim.
- Empirical-verification paragraph at line 95 names the
  canada-roks workspace (135 resources) and the module addresses
  observed — provenance preserved.

The `clusterPhaseModules` list itself (`tfstate.go:85–89` —
three entries: `module.roks_cluster`, `module.cert_manager`,
`module.testing`) appears verbatim in the PRD signals row and
the bullet rationale. No drift.

Round-2's staff comment-update touched the heuristic's
doc-comment (`tfstate.go:180–205`) only — added the
benign-managed-resource example (the registry COS at
`module.roks_cluster.module.cluster.ibm_resource_instance.cos_instance`
and the testing TLS key at
`module.testing.tls_private_key.jumphost_shared_key`) under
bullet 2's rationale. PRD 06's bullet 2 (line 91) names BOTH
benign-managed-resource examples verbatim, so the comment-update
is consistent with the PRD prose. No re-write needed.

#### Drift sweep #4 — chapter 31 §"The bundled tools images" alignment with on-disk tree

**Verdict**: CLEAN — chapter 31 tree listing, `make` examples,
and CI trigger description all still match the on-disk
`tools/docker/` tree + `.github/workflows/tools-images.yml`
matrix. The Sprint 23 round-1 architect's chapter-31 rewrite
stands; round-2 didn't touch chapter 31.

On-disk verification:

```
$ ls /mnt/c/project/roksbnkctl/tools/docker/
Makefile
ibmcloud
iperf3
mdbook
```

Four entries (Makefile + three image dirs). Chapter 31 tree
listing at lines 96–105 shows all three image dirs (ibmcloud/,
iperf3/, mdbook/) with the `# roksbnkctl-tools-mdbook
(release-time book builder)` comment matching the other entries'
style.

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
to the corresponding `CONTRIBUTING.md` callout (per the Sprint 22
architect's closure §"Cross-doc consistency check"); a reader
hitting both surfaces gets a consistent story.

No round-2 changes touched chapter 31 — Sprint 23 round-2
focused on `terraform/modules/cert_manager/`, `terraform/main.tf`,
`internal/orchestration/second_phase_reuse.go`, and validator's
pinning test. Chapter 31 is unchanged from the Sprint 23 round-1
rewrite; the round-1 architect's verdict still holds.

#### Drift sweep #5 — `second_phase_reuse.go` header + override content vs. validator's pinning test (round-2 incl. `deploy_cert_manager`)

**Verdict**: CLEAN with one low-severity nit — override content
matches validator's pinning test byte-for-byte (10 lines, exact
adjacency); file-level header comment + override-file-embedded
comment both document `deploy_cert_manager` thoroughly. The nit
is a stale function-level docstring on `writeAndInitSecondPhase`
(lines 125–134) that mentions only Sprint 23 round-1's
registry-COS gate + count gate, not the round-2 `deploy_cert_manager`
addition. Non-blocking — the operator-facing surface (override
content, embedded provenance comment, file-level header) is
correct.

**Override content byte-parity** (`internal/orchestration/second_phase_reuse.go:222–231`):

```
create_roks_cluster = false
roks_cluster_id_or_name = %q
use_existing_cluster_vpc = true
existing_cluster_vpc_id = %q
create_roks_transit_gateway = false
create_roks_registry_cos_instance = false
deploy_cert_manager = false
testing_create_cluster_jumphosts = false
testing_create_tgw_jumphost = false
testing_create_client_vpc = false
```

Validator's `wantBlock`
(`internal/orchestration/second_phase_reuse_test.go:262–271`) is
this exact 10-line sequence with single-LF separators and the
two `%q` slots substituted with `"crt-cluster-id"` and
`"r038-ef6305af-vpc"` from the fixture. Adjacency check at lines
287–290 pins the new `deploy_cert_manager = false` line between
`create_roks_registry_cos_instance = false` and the
`testing_create_*` block. Leak-signature guard at lines 302–310
flags `deploy_cert_manager = true` (with and without spaces
around `=`) as a regression.

`go test ./internal/orchestration/... -run TestWriteBnkPhaseOverride
-count=1`: all three tests PASS (TurnsAllClusterSharedOff,
SuppressesRegistryCOSAndJumphostKey, Sprint23ByteIdenticalBlock).

**Header comment audit** (file-level, `second_phase_reuse.go:1–106`):

- Lines 3–6 cite Sprint 16 + Sprint 23 explicitly.
- Lines 51–62 (round-1) — the `create_roks_registry_cos_instance
  = false` rationale (defense-in-depth two-count-half + 2026-05-27
  live evidence).
- Lines 63–76 (round-2 NEW) — the `deploy_cert_manager = false`
  rationale: the inner cert_manager submodule's
  `count = var.enabled ? 1 : 0`, the pre-round-2 outer-wrapper
  hardcode (`enabled = true` → `enabled = var.deploy_cert_manager`),
  the cluster-damage hazard (`kubectl delete namespace cert-manager`
  destroy provisioner), the 2026-05-27 canada-roks live verify
  trigger, and the structural-safety claim that
  flo/cne_instance/license's `cert_manager_dependency_id`
  consumers gate on `!= null` (line 75 cross-link to
  `flo/providers.tf:42` "direct-apply" fallback).
- Lines 77–90 — the three `testing_create_*` flags + Sprint 23
  round-1's count-gate cross-link + round-2's
  `jumphost_user_data` locals `length(...) > 0 ? ... : ""` guard
  cross-link (`terraform/modules/testing/main.tf` lines
  39/40/188/193/194).
- Lines 92–96 — corrected net-effect claim. The pre-round-2
  wording listed `cert_manager / flo / cne_instance / license`
  as bnk-layer; the post-round-2 wording (line 93) lists
  `flo / cne_instance / license` only (cert_manager moved off
  bnk-layer in round-2). Matches the round-2 architectural shift.

**Override-file-embedded comment audit** (`second_phase_reuse.go:202–221`,
the comment block emitted INSIDE the generated tfvars file):

- Cites `issues/issue_sprint16_validator.md` Issue 2 round 2 +
  Sprint 23 round-1 + Sprint 23 round-2 (lines 203–208).
- Names the cluster-shared resources by class (cluster VPC,
  subnets, public gateways, transit gateway, registry COS,
  testing client VPC, jumphost subnets, jumphost SG, shared SSH
  key) AND the new round-2 class — cert_manager helm release +
  namespace (lines 209–213).
- Names the cluster-damage hazard (`kubectl delete namespace
  cert-manager` on subsequent `bnk down` — line 207–208).
- Names the consumption pattern for downstream modules — flo /
  cne_instance / license's `direct-apply` fallback on null
  `cert_manager_dependency_id` (lines 217–219).
- Names the forced-override precedence (line 219–221).

Operator-visible provenance for anyone reading the generated
tfvars file is complete.

**Low-severity nit** (`writeAndInitSecondPhase` function-level
docstring, `second_phase_reuse.go:125–134`):

```
// writeAndInitSecondPhase is the second/trial-phase preamble. It renders
// the normal terraform.tfvars (unchanged create-path render — the round-1
// per-toggle renderer is gone), then, when this workspace already has a
// cluster-outputs.json (the cluster phase completed → we are the SECOND
// phase), writes a bnk-phase-override.tfvars that forces every
// cluster-shared CREATE off (cluster + cluster VPC reuse + transit
// gateway + registry COS + client VPC + both jumphost classes; Sprint 23
// added the explicit registry-COS flag and a count gate on the testing
// module's shared TLS key) and returns its path so the caller appends it
// to the plan/apply var-file chain.
```

The parenthetical "Sprint 23 added the explicit registry-COS
flag and a count gate on the testing module's shared TLS key"
captures round-1 only — it doesn't mention `deploy_cert_manager
= false` (round-2's addition that closed the cert_manager
destroy-provisioner cluster-damage hazard). Reader-impact is
low: anyone reading this docstring also has the file-level
header (which is exhaustive) and the override-file-embedded
comment (which is exhaustive). A future-sprint nit edit could
update the parenthetical to "Sprint 23 round-1 added the
explicit registry-COS flag and a count gate on the testing
module's shared TLS key; Sprint 23 round-2 added
`deploy_cert_manager = false` to close the cert_manager
destroy-provisioner hazard". Not blocking.

The round-2 staff closure (`issues/issue_sprint23_staff.md` line
488–489) reads "Header + function-level comments updated with
the round-2 rationale" — the file-level header IS updated;
the function-level docstring on `writeAndInitSecondPhase` was
not. Verifying against the file at `c197d20` confirms the
omission. Cosmetic.

---

#### Findings summary

| # | Severity | Surface | Verdict |
|---|----------|---------|---------|
| 1 | medium | `book/src/20-connectivity-testing.md` lines 73–113 worked-example log lines | FINDING — architect-flagged illustrative output drift; shape correct, wording differs from binary (recommended post-cut docs-only edit per architect §"Follow-up candidates" item 1) |
| 2 | low | `internal/orchestration/second_phase_reuse.go:125–134` function-level docstring | FINDING — stale parenthetical omits round-2's `deploy_cert_manager` addition; file-level header + override-embedded comment are exhaustive (cosmetic) |

Both findings are non-blocking against the combined `v1.7.1`
cut. Neither involves a behavioural mis-document — the binary's
surface is correct, the override-emitted tfvars file is correct,
the file-level provenance comment is correct, the validator's
pinning test is byte-identical against the generator. The
drifted surfaces are prose-only and were anticipated by the
upstream architect / staff closures.

---

#### Follow-up candidates flagged for the integrator (future sprints)

Three candidates surfaced from this two-sprint pass. All
non-blocking; the integrator decides whether to fold any into a
post-v1.7.1 sprint or land as a small docs-only commit before
the cut.

1. **Re-capture `book/src/20-connectivity-testing.md` worked
   example against the built binary** (sweep #1 FINDING; architect
   §"Follow-up candidates" item 1 explicitly anticipated). Swap
   the illustrative log lines (lines 79, 82, 99, 108, 109, 112)
   for the binary's actual `✓ added "<url>" to test.connectivity.extra_hosts`
   / `test hosts: "<url>" already present; no-op` / pretty-printed
   JSON / `Clear ALL test.connectivity.extra_hosts? [y/N]:` /
   `✓ cleared test.connectivity.extra_hosts` wording, and drop
   the lines 92–93 "illustrative" blockquote. Operator-friendliness
   gain: matching the literal log lines makes copy-paste-then-grep
   from the book work against a real terminal session. Small
   single-file docs commit; could land before the cut if the
   integrator wants a clean `v1.7.1` doc surface, or post-cut as
   the first item of the next sprint.
2. **Update `writeAndInitSecondPhase` function-level docstring**
   (sweep #5 low nit). Add the round-2 `deploy_cert_manager`
   sentence to the parenthetical at
   `internal/orchestration/second_phase_reuse.go:131–132`. Single
   docstring edit; the file-level header and the
   override-file-embedded comment are already exhaustive, so the
   nit is purely consistency-for-the-next-reader. Could fold
   into either follow-up #1's docs commit or a Sprint 25
   round-up commit.
3. **Tighten the two bootstrap `null_resource.roks_cluster_gate`
   declarations** (carried forward from
   `issues/issue_sprint23_staff.md` round-2 future-sprint
   candidate item 4). The 2 inert residual entries the round-3
   live verify reported (`module.cert_manager.null_resource.roks_cluster_gate`
   + `module.testing.null_resource.roks_cluster_gate`) are
   framework-bookkeeping null_resources with no destroy
   provisioner — zero cloud impact, zero cluster impact. Adding
   `count = var.create_roks_cluster ? 1 : 0` to both would close
   the strict criterion ("zero managed cluster-phase entries in
   trial state") and let the live-verify recipe's jq filter
   return empty. Cosmetic, not safety-critical. Also overlaps
   with `issues/issue_sprint23_tech-writer.md` §"Follow-up
   candidates" item 6 (`terraform validate` CI smoke gate) — both
   would benefit from a small "phase-leak hardening" Sprint 25
   that bundles the two-line HCL gate + a CI gate.

None of items 1–3 block Sprint 24 closure or the combined
`v1.7.1` cut. Items 1 and 2 are docs-cosmetic; item 3 is HCL
cosmetic.

---

#### Launch verdict: **GREEN** — combined `v1.7.1` cut unblocked

Sprint 22 (down-prompt + DetectShape) + Sprint 23 round-1
(phase-separation leak) + Sprint 23 round-2 (cert_manager
destroy-provisioner leak + jumphost_user_data locals +
flo ca_certificate fix) + Sprint 24 (`test hosts` CLI surface)
are integrated and drift-clean at commit `c197d20`. Five
drift sweeps walked:

- Sweep #1: FINDING (medium, architect-anticipated illustrative
  worked-example wording drift; shape correct; non-blocking).
- Sweep #2: CLEAN (`test.go:803` error message points at new CLI;
  backtick style matches the in-error convention; book
  cross-links quote the new wording).
- Sweep #3: CLEAN (PRD 06 §"Design" still matches the
  `trialStateHasClusterModules` heuristic filter-by-filter
  post-round-2).
- Sweep #4: CLEAN (chapter 31 §"The bundled tools images" tree +
  Makefile targets + workflow triggers still match on-disk
  post-round-2; round-2 didn't touch chapter 31).
- Sweep #5: CLEAN with one low-severity docstring nit (override
  content byte-identical against validator's 10-line pinning test
  including the round-2 `deploy_cert_manager = false` addition;
  header comment + override-embedded comment exhaustive; the
  `writeAndInitSecondPhase` function-level docstring's
  parenthetical is missing the round-2 mention — cosmetic).

**`v1.7.1` release-cut unblocked.** Per the integrator's
combined-cut framing (`prompts/sprint24/README.md` integrator
decision 5 + `prompts/sprint23/README.md` integrator decision 4),
all four sprints ship together under one combined `v1.7.1` tag
+ one CHANGELOG entry. The CHANGELOG entry references all four
sprints; the test hosts CLI is the most operator-visible
addition. This tech-writer closure makes no specific `tag` /
`release` / `CHANGELOG.md` recommendations — those defer to the
integrator's combined-cut commit.

**Live verify status.** Per the
[[live-verify-high-issues]] memory:

- Sprint 23 round-2 was live-verified GREEN on 2026-05-27 against
  canada-roks (round-3 live verify; jq leak filter reported 2
  inert residual entries — both
  `null_resource.roks_cluster_gate` bookkeeping resources, no
  destroy provisioner, zero cloud impact, zero cluster impact;
  the catastrophic leak class is closed). Documented in
  `issues/issue_sprint23_staff.md` lines 519–551.
- Sprint 22's live verify (DetectShape against canada-roks) was
  carried forward through the Sprint 23 verification cycle.
- Sprint 24 is UX-only, hermetic test class sufficient per
  `prompts/sprint24/README.md` integrator decision 6
  (`live-verify-high-issues` does NOT apply to this sprint — UX
  feature, no resource-damage class). Validator's 12-sub-case
  hermetic suite (PASS at `c197d20`) is the closure gate;
  binary surface confirmed via direct source-of-truth read of
  `internal/cli/test_hosts.go`.

The hermetic completeness this verdict certifies + the documented
2026-05-27 round-3 live verify GREEN on the only resource-damage-class
deliverable in the combined cut (Sprint 23) is the full closure
gate for `v1.7.1`. No further live verify needed before the
integrator's tag cut.

---

#### Discipline checks

- No commits, no `gh` invocations, no `git push`, no tag
  proposal. Integrator-owned per `prompts/sprint24/README.md`
  §"Constraints (binding on every role)".
- Read-only on all repo content except this file
  (`issues/issue_sprint24_tech-writer.md`, new file).
- No edits to `internal/`, `cmd/`, `.github/workflows/`,
  `tools/docker/`, `CONTRIBUTING.md`, `book/src/`, `Makefile`,
  `docs/prd/`, `docs/PLAN.md`, `CHANGELOG.md`, or `terraform/`.
- No edits to `issues/issue_sprint23_staff.md`,
  `issues/issue_sprint23_architect.md`,
  `issues/issue_sprint23_validator.md`,
  `issues/issue_sprint23_tech-writer.md`,
  `issues/issue_sprint24_staff.md`,
  `issues/issue_sprint24_architect.md`, or
  `issues/issue_sprint24_validator.md` (other roles' closures /
  other sprint files; out of scope per the prompt's "Critical
  constraints").
- Per-finding fields use `**Verdict**:`, not `**Status**:`
  (mirrors Sprint 21 `a2b78da` rename + Sprint 22 + Sprint 23
  tech-writer file convention — the issue-level top-of-file
  `**Status**:` is reserved for `resolved/open` per the
  `sprint-ledger-status-convention` memory).
- Every drift sweep cites specific `file:line` ranges and a
  CLEAN / FINDING verdict. Two findings logged (sweep #1
  medium-cosmetic, sweep #5 low-cosmetic); both anticipated by
  upstream role closures; neither blocks the v1.7.1 cut.
- GREEN/RED launch verdict explicitly mentions the `v1.7.1`
  combined-cut unblock state and the live-verify status (Sprint
  23 round-2 GREEN with 2 inert residuals; Sprint 24 UX-only
  hermetic-class).

### Related

- `prompts/sprint24/tech-writer.md` — this issue's source prompt.
- `prompts/sprint24/README.md` — integrator decisions
  (tight-scope `test.connectivity.extra_hosts`; mirror the
  `targets` ergonomic; staff updates `:803` error message; the
  tech-writer covers Sprint 23 round-2 + Sprint 24 in ONE pass
  and unblocks `v1.7.1`).
- `issues/issue_sprint24_staff.md` — staff closure audited in
  drift sweeps #1 (output strings in `test_hosts.go`) and #2
  (`:803` error message edit).
- `issues/issue_sprint24_architect.md` — architect closure
  audited in drift sweep #1 (chapter 20 + 21 cross-links; the
  "illustrative" blockquote explicitly anticipated this sweep's
  finding).
- `issues/issue_sprint24_validator.md` — validator closure
  audited in drift sweep #1 (12-sub-case hermetic harness pins
  the shape contract).
- `issues/issue_sprint23_staff.md` — round-2 closure
  (`§"Closure — staff, 2026-05-27 (round 2 — live verify GREEN
  with residual)"`) audited in drift sweep #5; the round-3 live
  verify GREEN with 2 inert residuals documented at lines
  519–551.
- `issues/issue_sprint23_tech-writer.md` (commit `158ae55`) —
  the shape this closure mirrors; the round-1 verdict (sweeps
  #3 and #4 were CLEAN there too) carried forward through this
  re-confirmation.
- `issues/issue_sprint22_tech-writer.md` (commit `00f1e0f`) —
  the same shape one rung back; chapter 31 + CONTRIBUTING.md
  parity the round-1 Sprint 23 sweep #4 cross-checked.
- `internal/cli/test_hosts.go` — Sprint 24 staff's new CLI
  source; the byte-level output strings sweep #1 audited.
- `internal/cli/test.go:803` — staff's one-line error-message
  edit; sweep #2.
- `internal/config/tfstate.go:180–235` — `trialStateHasClusterModules`
  heuristic; sweep #3.
- `internal/orchestration/second_phase_reuse.go` — header
  comment + override generator; sweep #5.
- `internal/orchestration/second_phase_reuse_test.go` —
  validator's 10-line byte-identical pinning test; sweep #5
  cross-check.
- `tools/docker/` + `.github/workflows/tools-images.yml` —
  on-disk tools-images tree + CI matrix; sweep #4.
- `book/src/20-connectivity-testing.md` — architect's Edit 1
  (§"Managing test hosts via the CLI") + Edit 2 + Edit 3; sweep
  #1.
- `book/src/21-dns-testing-gslb.md` — architect's Edit 4 + Edit
  5; sweep #2 cross-link verification.
- `book/src/31-building-from-source.md` — Sprint 23 round-1
  architect's chapter rewrite; sweep #4.
- `docs/prd/06-CLUSTER-TRIAL-PHASE-SPLIT.md` — Sprint 23 round-1
  architect's PRD §"Design" rewrite; sweep #3.
- Integrator memory [[live-verify-high-issues]] — applies to
  Sprint 23 round-2 (resource-damage class, live-verified GREEN
  with 2 inert residuals on 2026-05-27); does NOT apply to
  Sprint 24 (UX-only, hermetic test class sufficient per
  `prompts/sprint24/README.md` integrator decision 6).
- Integrator memory [[no-piling-into-active-release]] — applies
  to the Sprint 24 surfacing chain: the `test hosts` CLI gap was
  surfaced 2026-05-27 during a demo.sh re-verify and promoted
  into Sprint 24 (a new sprint scoped behind Sprint 23 in the
  release cycle), not folded into Sprint 23 mid-cycle. Discipline
  preserved.
- Integrator memory [[argv-strictness-prevents-resource-damage]]
  — applies in spirit. The new `test hosts` subcommands declare
  `cobra.NoArgs` (`list`, `clear`) or `cobra.MinimumNArgs(1)`
  (`add`, `remove`) per the Sprint 21 strictness contract.
  Validator's hermetic suite pins the argv-parse-time rejection
  for both classes (sub-cases k1, k2, l1, l2).
- Integrator memory [[sprint-ledger-status-convention]] —
  applies to the top-of-file `**Status**: resolved` field on this
  issue (not the per-finding `**Verdict**:` fields, which follow
  the `a2b78da` rename).
- Integrator memory [[investigate-first-on-non-obvious-bugs]] —
  applies to the round-2 work this re-sweep covers. Round-1's
  hermetic fix passed all tests but missed the broader leak
  class until the 2026-05-27 live verify surfaced four more
  catastrophic entries; the round-2 staff closure documents the
  instrument-and-measure path that led to the
  `deploy_cert_manager` + jumphost_user_data + flo ca_certificate
  fixes — exactly the discipline this memory codifies.
