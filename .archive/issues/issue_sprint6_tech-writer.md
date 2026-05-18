# Sprint 6 — tech writer issues

Sprint 6 is the **v1.0 dress rehearsal** — last sprint before the
release tag. Findings cover the 9 new chapters (23, 25, 26, 27, 28,
29, 30, 31, 32), the EDNS / `tools/docker/ibmcloud` Dockerfile carry-
overs, the Phase I/M/N implementation against PRD 05, the doctor
refresh, the `MIGRATING.md` content, and the v1.0 release-readiness
preview.

Reviewed: 9 new / refreshed chapters, 1 reordered chapter (22),
`MIGRATING.md`, `CHANGELOG.md`, `README.md`, `docs/PLAN.md` §Sprint 6,
`docs/prd/05-E2E-TEST-PLAN.md` §I/M/N, `scripts/e2e-test-backends.sh`,
`scripts/e2e-test-full.sh`, `.github/workflows/e2e-full.yml`,
`internal/doctor/doctor.go`, `internal/exec/docker.go`,
`internal/exec/ssh.go`, `internal/test/dns.go`. Cross-checked book
slugs + cross-references.

12 issues filed: **1 blocker** (chapter 23 `--use-existing-cluster`
flag reference that doesn't exist in the binary, fed into the
release-checklist as a "supported invocation"), 6 medium, 4 low, 1
roadmap. The blocker is doc-only (no code regression) but it goes to
the v1.0 release narrative.

The Sprint 6 gate criteria from `docs/PLAN.md` §"Gate to Sprint 7"
(all 32 chapters drafted; doctor green-by-default; previous-sprint
acceptance criteria still hold; all E2E phases pass on a clean host):
**three of four are clean — see Issue 11 for the e2e-coverage gate
nuance**. Sprint 6 is otherwise releasable to Sprint 7.

## Issue 1 (BLOCKER — chapter 23 documents a non-existent `--use-existing-cluster` flag on `scripts/e2e-test-backends.sh`)

**Severity**: blocker (v1.0 release narrative)
**Status**: open

**Description**: Chapter 23 §"How to run it locally" line 49 documents
the invocation:

```bash
./scripts/e2e-test-backends.sh --use-existing-cluster
```

and again at line 77 + line 232 as the "intended workflow on a flake"
path. The flag **does not exist** in `scripts/e2e-test-backends.sh` —
`grep -n "use-existing-cluster\|use_existing" scripts/e2e-test-backends.sh`
returns zero hits. The script's config block (lines 37-44) shows the
only knobs are `WORKSPACE`, `TFVARS`, `PHASE_FROM`, `DRY_RUN`, `LOG_DIR`,
`ROKSBNKCTL`, `RUN_K6` — no cluster-reuse flag. The preflight (lines
167-188) implicitly assumes a cluster from a prior `scripts/e2e-test.sh`
Phase D run is up and reachable; there's no "skip the up" knob.

A user running `./scripts/e2e-test-backends.sh --use-existing-cluster`
will see Bash's `set -u` ignore the flag (it's just an unused positional
arg) and the script will run normally — but the flag-table format and
the three repeated mentions across the chapter set the expectation that
this is a real, documented invocation. A reader copy-pasting from the
book will be confused when other "documented" flags don't work.

Chapter 23 is the user-facing E2E reference; for v1.0 release-gate
correctness this MUST match the shipped surface.

**Files affected**: `book/src/23-e2e-test-plan.md` §"How to run it
locally" (line 49), §"Resuming a partial run" (line 77), §"Re-runnability"
(line 232).

**Proposed fix**: choice of two:

1. **Drop the flag from chapter 23** — the implicit "the cluster from
   the prior `e2e-test.sh` Phase D is up" contract is what the script
   actually enforces. Rewrite the invocation as a bare
   `./scripts/e2e-test-backends.sh` and add a one-line note ("requires
   a live cluster — either run after Phase D of `e2e-test.sh`, or
   provision via `roksbnkctl up` in a separate workspace first").
2. **Land the flag in the script** — add `--use-existing-cluster`
   parsing to `scripts/e2e-test-backends.sh::preflight` (currently a
   no-op; the script already assumes the cluster exists). The flag
   becomes documentation-as-code: present-and-no-op for now,
   semantically meaningful for the integrator's mental model.

Option (1) is the smaller surface for v1.0 polish; option (2) is the
"document-the-actual-flag" path if Sprint 7 wants to land the knob.

## Issue 2 (HIGH — chapter 23 cites a per-phase log path that doesn't match the script's actual log emission)

**Severity**: medium (correctness of a release-checklist artefact)
**Status**: open

**Description**: Chapter 23 §"Per-phase logs" (line 84) says:

> Each phase logs to `/tmp/roksbnkctl-e2e-backends/<phase>-<timestamp>.log`
> (and the baseline driver writes the equivalent under
> `/tmp/roksbnkctl-e2e/`). Failures preserve the log; success deletes
> everything but the last summary line.

The actual `scripts/e2e-test-backends.sh` (lines 66-68) writes a
**single combined** log:

```bash
mkdir -p "$LOG_DIR"          # /tmp/roksbnkctl-e2e-backends/
RUN_TS=$(date +%Y%m%d-%H%M%S)
RUN_LOG="$LOG_DIR/run-$RUN_TS.log"
```

Every `step`, `capture`, and `log` invocation appends to `RUN_LOG`
unconditionally — there are no per-phase log files. The architect-pass
issue file (Issue 5) said this would be the case and asked the
integrator to verify; the integrator's resolution note claimed
"validator's path matches the chapter's prose", but that's incorrect.

Additionally there's no "success deletes everything but the last
summary line" behaviour — the script keeps the full run log on both
success and failure.

**Files affected**: `book/src/23-e2e-test-plan.md` §"Per-phase logs"
(line 84).

**Proposed fix**: rewrite §"Per-phase logs" to reflect the actual
emission shape:

> Each driver writes one combined log per run at
> `/tmp/roksbnkctl-e2e-backends/run-<timestamp>.log` (and the baseline
> driver writes the equivalent at `/tmp/roksbnkctl-e2e/run-<ts>.log`).
> The combined runner adds a third layer at
> `/tmp/roksbnkctl-e2e-full/run-<ts>.log` with the two child driver
> logs nested under `baseline/` and `backends/` subdirectories. Logs
> are preserved on both success and failure; clean them up manually
> when disk pressure warrants.

Per-phase log splitting is a sensible v1.x request — file a follow-up.

## Issue 3 (HIGH — chapter 23 lists Phase J in the "Backends + extras" tier table but no `phase_J` exists in `scripts/e2e-test-backends.sh`)

**Severity**: medium (chapter 23 misrepresents the shipped e2e surface)
**Status**: open

**Description**: Chapter 23's at-a-glance table (lines 20-36) places
Phase J ("kubectl internalisation") in the **Backends + extras** tier
alongside I, K, L, L-DNS, M, N — implying it's driven by
`scripts/e2e-test-backends.sh`. The chapter also lists "14 phases" in
the prose at line 9 (actually 15 distinct labels when you count L-DNS
as a phase).

Grepping `scripts/e2e-test-backends.sh` for `phase_J` returns zero
hits. The script jumps from `phase_I` straight to `phase_K` —
deliberately, because PRD 05 §J requires `sudo mv kubectl
$KUBECTL_PATH.hidden` to PATH-strip kubectl, which can't be safely
automated inside the e2e driver (it mutates the host environment for
all other tools). PRD 05 §J even has it labelled as needing sudo and
expects the integrator's manual sign-off, not an automated phase.

So Phase J is **not** part of the automated coverage — it's a manual
release-checklist item. The at-a-glance table misleads the reader.

**Files affected**: `book/src/23-e2e-test-plan.md` lines 9, 20-36
(at-a-glance table), line 130 (§"Phase J — kubectl internalisation"
implies it's run by the driver).

**Proposed fix**:

- Update line 9 prose to "14 phases organised into two tiers, plus
  Phase J as a manual integrator step" — or drop the count entirely
  and let the table speak for itself.
- In the at-a-glance table, move the Phase J row out of the backends
  tier with a "manual" annotation in the driver column:
  `| J | manual | kubectl internalisation (PATH-stripped) | 02 |`.
- In §"Phase J — kubectl internalisation" add a one-paragraph callout
  at the top: "Phase J is a manual integrator step; it requires
  `sudo mv` of the kubectl/oc binaries which the automated driver
  doesn't perform. The integrator runs it during the per-release
  checklist (`docs/E2E_TEST.md` §6 once added)."
- (Optional polish) Add an entry to `docs/E2E_TEST.md` §"Per-release
  checklist" between items 4 and 5 covering the manual Phase J path.

## Issue 4 (MEDIUM — chapter 26 references a non-existent `--refresh-kubeconfig` flag on `init` and `cluster register`)

**Severity**: medium (broken-as-documented user workaround)
**Status**: open

**Description**: Chapter 26 §"Symptom: `roksbnkctl up` post-apply hook
fails fetching the admin kubeconfig with a 404" (line 49) says:

> run `roksbnkctl init --refresh-kubeconfig -w <workspace>` (or just
> `roksbnkctl kubeconfig --download` once that command lands) to retry
> just the fetch without re-applying.

and again at §"Symptom: `register` succeeds but `roksbnkctl k get
nodes` immediately errors `Unauthorized`" (line 221):

> ```bash
> roksbnkctl kubeconfig --download
> # or, force a refresh via the registration flow
> roksbnkctl cluster register <name> --refresh-kubeconfig
> ```

Two of these three commands don't exist:

- `roksbnkctl init --refresh-kubeconfig` — chapter 27 shows `init` has
  only `--tf-source` and `--upgrade-tf` flags. `grep -rn
  "refresh-kubeconfig" internal/cli/` returns zero hits.
- `roksbnkctl cluster register --refresh-kubeconfig` — chapter 27 shows
  `cluster register` has only `--prompt` and `--registry-cos-name`.
  Same grep returns zero hits.
- `roksbnkctl kubeconfig --download` — chapter 27 confirms this **does**
  exist (line 725). Good.

The chapter is also internally inconsistent — line 49 hedges
"`kubeconfig --download` once that command lands" implying the command
doesn't exist yet, but line 219 says to run it. The command does exist;
the hedge is stale.

**Files affected**: `book/src/26-troubleshooting.md` §"Workspace-delete…
404" entry (line 49), §"register succeeds but Unauthorized" entry
(line 221).

**Proposed fix**:

- Line 49: drop the `init --refresh-kubeconfig` reference; drop the
  "once that command lands" hedge. The correct fix is simply
  `roksbnkctl kubeconfig --download -w <workspace>`.
- Line 221: drop the `cluster register --refresh-kubeconfig` line.
  The correct fix is `roksbnkctl kubeconfig --download --cluster
  <name>` (chapter 27 shows the `--cluster` flag on `kubeconfig`
  for exactly this case).

## Issue 5 (MEDIUM — chapter 13 + `MIGRATING.md` document `terraform.tfvars.user` at the wrong path)

**Severity**: medium (cross-document drift carried from Sprint 3)
**Status**: open

**Description**: `internal/tf/terraform.go::UserTFVarsPath` (line 169-
171) puts the file at the workspace root:

```go
func (w *Workspace) UserTFVarsPath() string {
    return filepath.Join(filepath.Dir(w.stateDir), "terraform.tfvars.user")
}
```

i.e., `~/.roksbnkctl/<ws>/terraform.tfvars.user` — a sibling of
`config.yaml`, **outside** the `state/` directory.

Chapter 28 §"How `--var-file` interacts with `config.yaml`" (line 288)
gets this right: `~/.roksbnkctl/<ws>/terraform.tfvars.user`.

But Chapter 13 and the new `MIGRATING.md` both say it lives inside
`state/`:

- `book/src/13-terraform-variables.md` line 31: `~/.roksbnkctl/dev/state/terraform.tfvars.user`
- `book/src/13-terraform-variables.md` lines 45, 84, 150 — same.
- `MIGRATING.md` line 34: `~/.roksbnkctl/<workspace>/state/terraform.tfvars.user`
- `MIGRATING.md` lines 113-122 (workspace layout diagram) puts
  `terraform.tfvars.user` under `state/`.

A user copy-pasting from either of these will create a file the binary
doesn't look at — `HasUserTFVars()` returns false, so the layering
silently skips them.

**Files affected**: `book/src/13-terraform-variables.md` (4 references);
`MIGRATING.md` lines 34 + 113-122 (workspace layout diagram).

**Proposed fix**: bulk find/replace `state/terraform.tfvars.user` →
`terraform.tfvars.user` (i.e., one level up) in chapter 13 and
MIGRATING.md. Update the workspace-layout diagram in MIGRATING.md to
show `terraform.tfvars.user` as a sibling of `config.yaml`, not under
`state/`. Chapter 28 is the source of truth.

## Issue 6 (MEDIUM — chapter 23 cross-link to chapter 16's "Auto-discovery from terraform outputs" — section title doesn't match + no anchor)

**Severity**: low (broken cross-reference)
**Status**: open

**Description**: Chapter 23 §"Phase D — `up` lifecycle" (line 108):

> the phase auto-registers the `jumphost` target (per [Chapter 16 §"Auto-discovery from terraform outputs"](./16-on-flag-ssh-jumphosts.md)) so subsequent phases can `--on jumphost` without manual config.

Two problems:

1. The link has no `#anchor` — it just points at `./16-on-flag-ssh-jumphosts.md`.
2. The cited section title "Auto-discovery from terraform outputs"
   doesn't exist in chapter 16. The actual section header is
   `## Auto-discovery from \`roksbnkctl up\`` (line 46).

**Files affected**: `book/src/23-e2e-test-plan.md` line 108.

**Proposed fix**: rewrite the link as `[Chapter 16 §"Auto-discovery
from `roksbnkctl up`"](./16-on-flag-ssh-jumphosts.md#auto-discovery-from-roksbnkctl-up)`.
Verify the slug renders correctly under mdbook (GFM slug strips the
backticks, so `auto-discovery-from-roksbnkctl-up` should resolve).

## Issue 7 (MEDIUM — chapter 27 auto-generator omits cobra `Aliases`, hiding the `ws` short form documented elsewhere)

**Severity**: medium (auto-generator surface gap)
**Status**: open

**Description**: Chapter 27 is generated by `tools/refgen/cobra-md` and
renders the canonical command name from `cmd.Use`. For the workspaces
subtree, that's:

```
## `roksbnkctl workspaces`
### `roksbnkctl workspaces current`
### `roksbnkctl workspaces delete`
…
```

But `internal/cli/workspaces.go:18` declares `Aliases: []string{"ws"}`
on the parent command, and the rest of the book uses `roksbnkctl ws`
universally (chapters 6, 23, 26, 28, 30, 32, MIGRATING.md, CHANGELOG.md
— every prose mention is `ws`, not `workspaces`).

A reader landing on chapter 27 looking for `roksbnkctl ws` won't find
the entry; readers cross-referencing from elsewhere in the book will
get table-of-contents anchor mismatches. `grep -rn "Aliases" tools/refgen/cobra-md/main.go`
shows the generator never reads the `Aliases` slice.

Similar (single-instance) for `k port_forward` (chapter 27 emits
`k port-forward` — kebab — but the `Aliases` slice has `port_forward`
underscore form; less impactful since chapter 27's primary form is
already what users type).

**Files affected**: `tools/refgen/cobra-md/main.go` (generator code,
not a chapter edit) — but the chapter 27 output is the visible artefact.

**Proposed fix**: extend the generator to emit a one-line "**Aliases**:
`ws`" callout under any command whose `Aliases` slice is non-empty.
Place it directly under the per-command synopsis paragraph (before
the flag table) so readers can find the short form without leaving
the section. Re-run `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md`
to refresh the chapter content; commit both the generator change and
the regenerated chapter. Sprint 7 polish window is the right place.

## Issue 8 (LOW — chapter 23's "Backends + extras" driver column references the wrong script for Phase I-N)

**Severity**: low (factual inaccuracy that mirrors Issue 3's J row)
**Status**: open

**Description**: Chapter 23's at-a-glance table (lines 20-36) attributes
the backends tier to `scripts/e2e-test-backends.sh` — correct. But the
prose at line 16 says the combined driver runs "A-H first to bring
infrastructure up, then I-N (which reuse the cluster from D) before
D's destroy." This is **not** what `scripts/e2e-test-full.sh`
actually does — the staff-written combined runner (lines 134-172 of
`e2e-test-full.sh`) explicitly notes the design tradeoff:

> the simpler path is: just let the baseline driver complete D (which
> is the `up + tests + down` cycle), then the backends driver brings
> up a SEPARATE workspace.

i.e., the baseline driver A-H runs to completion (Phase H destroys),
THEN the backends driver provisions its own cluster via Phase N's N1
step. Chapter 23's "reuse the cluster from D before D's destroy"
narrative doesn't match the implementation; it describes the
PRD 05-envisioned design, which the v1.0 build deliberately doesn't
implement (validator's Sprint 6 Issue 4 documents the design choice).

**Files affected**: `book/src/23-e2e-test-plan.md` line 16; cost +
time table (~5 hours total — partially correct since the cluster does
get re-provisioned).

**Proposed fix**: rewrite line 16 to match the actual design:

> A combined driver, `scripts/e2e-test-full.sh`, runs both tiers in
> sequence: A-H first to bring up + exercise + tear down the baseline
> cluster, then I-N which provisions a fresh cluster via Phase N's
> mixed-mode lifecycle step. The two drivers stay decoupled — each
> can be run standalone — at the cost of an extra cluster apply
> (~70min wall-time). Cluster-sharing across the two drivers (the
> PRD-envisioned design) is queued for v1.x; see PRD 05 §"Test
> infrastructure".

Also bump the cost+time table's wall-time estimate — the actual
combined run is closer to 5-7 hours, not 4-6, because of the second
cluster apply.

## Issue 9 (LOW — chapter 23 carries a stale forward-reference to "the validator agent's e2e CI workflow file")

**Severity**: low (architect placeholder the integrator didn't update)
**Status**: open

**Description**: Chapter 23 §"How CI runs it" (line 179):

> See the validator agent's e2e CI workflow file (landed in Sprint 6)
> for the concrete YAML.

The file is `.github/workflows/e2e-full.yml` (landed; see Issue 1 of
the architect's open list). The integrator should have replaced this
stale "ask the validator agent" placeholder with a direct link.

**Files affected**: `book/src/23-e2e-test-plan.md` line 179.

**Proposed fix**: replace the sentence with:

> See [`.github/workflows/e2e-full.yml`](https://github.com/jgruberf5/roksbnkctl/blob/main/.github/workflows/e2e-full.yml)
> for the workflow YAML; the workflow is manually triggered from the
> Actions tab (with optional `cluster_region` + `teardown_on_success`
> inputs) and runs automatically on every `release/**` branch push.

## Issue 10 (LOW — chapter 32 `internal/exec/registry.go` reference doesn't exist; `ResolveBackend` lives in `backend.go`)

**Severity**: low (contributor-facing chapter has a misnamed file path)
**Status**: open

**Description**: Chapter 32 §"Adding a new execution backend" step 2
(line 15):

> The `ResolveBackend(spec string)` function in
> `internal/exec/registry.go` dispatches `--backend <name>` to your
> constructor.

There's no `internal/exec/registry.go` — `ResolveBackend` lives at
`internal/exec/backend.go:127`. Same paragraph references "Add an
entry to the registry — either via the package's `init()` block or
via a `Register(name string, factory func() Backend)` call". The
`Register` function exists at `backend.go:166` but takes
`(name string, b Backend)` — not `(name string, factory func() Backend)`.

Also chapter 32 §"Adding a new tool to an existing backend" (line 80):

```go
var toolPackages = map[string][]string{
    "ibmcloud": {"ibmcloud-cli"},
    "iperf3":   {"iperf3"},
    "<your>":   {"<deb-package>"},
}
```

The actual type is `map[string]toolPackage` (a struct), not
`map[string][]string` — see `internal/exec/ssh.go:35`. Minor; the
shape is illustrative.

**Files affected**: `book/src/32-extending-roksbnkctl.md` lines 15, 80.

**Proposed fix**: change `registry.go` → `backend.go`. Fix the
`Register` signature to match the actual `func Register(name string,
b Backend)`. Fix the `toolPackages` example to reflect the actual
struct type — either show the real struct shape or rewrite the
example as `map[string]string{"<tool>": "<package-or-spec>"}` with a
"the real type is a struct with apt repo + package fields; see
`internal/exec/ssh.go` for the production form" note.

## Issue 11 (LOW — MIGRATING.md introduces a "v0.10" version label that nothing else in the project uses)

**Severity**: low (versioning surface inconsistency)
**Status**: open

**Description**: `MIGRATING.md` §"From roksbnkctl v0.7 / v0.8 → v0.9
→ v0.10" (line 78) and §"v0.10 (current — Sprint 6)" (line 82) label
Sprint 6 as **v0.10**. The rest of the project disagrees:

- `CHANGELOG.md` §"Unreleased — Sprint 6 (v1.0 prep)" (line 85) —
  labels Sprint 6 as v1.0 prep, not v0.10.
- `docs/PLAN.md` §"M4 / v1.0" — Sprint 6 + Sprint 7 together produce
  the v1.0 tag; no intermediate v0.10.
- `README.md` line 7 — "v0.9 release candidate" status, which per
  the prompt should flip to "v1.0 release candidate" in Sprint 7
  (not "v0.10").
- The resolved-issue file `resolved_sprint6_staff.md` mentions
  "v0.10" only as a Sprint 6 staff Issue 1 follow-up (chapter 21
  EDNS doc drift); the staff issue itself doesn't say v0.10 will
  be tagged.

So MIGRATING.md introduces a v0.10 label that no other artefact uses
and no plan-of-record commits to. Either the project does tag v0.10
between Sprint 6 + Sprint 7 (in which case PLAN.md, CHANGELOG.md, and
README all need a one-line update), or MIGRATING.md is the outlier and
its "v0.10" label should be revised to "Sprint 6 / v1.0 prep" to match
CHANGELOG's framing.

**Files affected**: `MIGRATING.md` lines 78, 82 (heading + section
intro).

**Proposed fix**: pick one of:

1. **Drop v0.10 from MIGRATING.md** — relabel the section as
   "Sprint 6 → v1.0 prep" or just "v1.0 (Sprint 7 cut)" and merge
   the v0.10 content into the v1.0 entry. Cleanest for the v1.0
   release narrative.
2. **Land v0.10 properly** — add a v0.10 entry to `CHANGELOG.md`
   between the existing v0.9.0 and Unreleased sections, update
   `README.md` status to "v0.10" (interim between v0.9 RC and v1.0
   tag), and add a v0.10 row to `docs/PLAN.md` §"Milestones". This
   is the bigger surface change.

Option (1) is the recommended path for v1.0 polish.

## Issue 12 (ROADMAP — chapter 23 + PRD 05 drift on Phase I/M/N step labels)

**Severity**: roadmap (PRD vs implementation divergence — pick one
source of truth in v1.x)
**Status**: filed for v1.x

**Description**: PRD 05 §"Phase I" defines I0-I7 (8 steps); the shipped
`scripts/e2e-test-backends.sh::phase_I` implements I0-I11 (12 steps —
validator's Sprint 6 build-out added I6 wrong-fingerprint, I7-I9
purpose-built-target steps, I10 context-cancel, I11 doctor). Chapter
23's prose for Phase I is consistent with the script (12 steps), but
PRD 05 is not.

PRD 05 §"Phase N" defines N0-N10 (11 steps); the script implements
N1-N6 (6 steps, restructured). Chapter 23's prose summarises Phase N
without enumerating, so it's silent on the count.

PRD 05 §"Phase M" defines M1-M7 (7 steps); the script implements
M1-M7 (matches).

The CHANGELOG.md line "12 steps I0-I11" + "N1-N6" matches the
implementation but doesn't match PRD 05's step numbering. Per chapter
32 §"The PRD process" line 128 — "When the implementation diverges
from the PRD, the PRD gets updated to match — never the other way
around" — PRD 05 §I and §N should be refreshed to match the shipped
step matrix.

**Files affected**: `docs/prd/05-E2E-TEST-PLAN.md` §"Phase I" (8
steps; should be 12), §"Phase N" (11 steps; should be 6 + a note on
N7-N10 being deferred to manual integrator scope per the simpler
chained design).

**Proposed fix**: out-of-scope for tech-writer pass; file as a v1.0
sign-off action item or v1.1 PRD-refresh task. Adding the explicit
"PRD 05 §I/§N updated to match Sprint 6 implementation" item to
PLAN.md §"Sprint 7" would track it cleanly.

## Spot-check: non-issues confirmed clean

- **Chapter 22 reorder**: `## OpenShift SCC failure mode` now precedes
  `## Reading the output` (lines 120 + 132). Anchor `#openshift-scc-failure-mode`
  resolves unchanged (mdbook GFM slug). Inbound references from
  chapters 26 + 30 still land correctly.
- **Chapter 21 EDNS Client Subnet update**: line 289 matches `internal/test/dns.go`'s
  `EDNSClientSubnet` struct (family / source_netmask / scope_netmask /
  address) including the `omitempty` framing. Schema-version statement
  is honest ("v0.10 added an optional `edns_client_subnet` object").
- **Chapter 30 glossary**: all required acronyms (BNK, ROKS, FAR, FLO,
  CIS, GSLB, SCC, TOFU, SPDY, RBAC) present. "Cell" entry dropped per
  architect Issue 10. Cross-link coverage is good. Anchor on "Licence
  rotation" resolves (chapter 25 has the `### Licence rotation` heading).
- **Chapter 26 entry count**: 29 entries (well above the 15-entry
  threshold); all entries follow symptom → root cause → fix. Coverage
  is comprehensive (SCC, terraform retries, kubeconfig 404, workspace-
  delete current-workspace, Docker daemon, k8s ops pod, SSH backend
  tool-not-found are all covered).
- **Chapter 26 workspace-delete current-workspace gotcha**: integrator
  correctly rewrote the prose to drop the `ROKSBNKCTL_WORKSPACE` env
  var reference (env var doesn't exist; `ROKSBNKCTL_HOME` does, but
  not `_WORKSPACE`). The corrected prose reads naturally.
- **Chapter 27 auto-generator output**: 1151 lines covering all top-
  level commands. Markdown valid, code fences balanced, anchor
  backlinks work. Top-of-chapter callout documents the regeneration
  command. (Aliases gap noted in Issue 7.)
- **Chapter 29 auto-generator output**: 205 lines covering root module
  + 6 submodules. Sensitive flag honored on `ibmcloud_api_key` +
  `bigip_password`. Markdown valid. Generator command documented at top.
- **Chapter 31 `.goreleaser.yml` path**: integrator fixed
  `goreleaser.yml` → `.goreleaser.yml` per architect Issue 7.
- **Doctor refresh**: `internal/doctor/doctor.go::runWithWhy` (line 84
  onward) treats `terraform` as the only required tool;
  `checkBinaryInformational` for kubectl/oc/ibmcloud/iperf3/dig;
  `checkKubeconfigInformational` replaces the warning path. Chapter 5
  + chapter 26 + chapter 23 §"Phase A" prose all match.
- **PRD 05 cross-link from chapter 23**: every PRD URL uses the
  GitHub-canonical form per chapter 32's style guide.
- **`MIGRATING.md` workspace-migration content**: minus the
  terraform.tfvars.user path bug (Issue 5) and the v0.10 label (Issue
  11), the content is comprehensive — covers `bnk`-to-roksbnkctl,
  manual-deploy-to-roksbnkctl, per-version notes, workspace layout +
  what's preserved across upgrades + cross-host transfer.
- **`scripts/e2e-test-full.sh`**: present, executable (`-rwxr-xr-x`),
  runs cleanly under DRY_RUN (resolved per validator Issue 1).
- **`.github/workflows/e2e-full.yml`**: workflow valid, secret-driven
  tfvars, optional SSH-target env keys for purpose-built-target steps.
  Matches chapter 23's "manual-trigger workflow" framing.

## v1.0 readiness verdict

PLAN.md §"Sprint 6 — Gate to Sprint 7" (line 477) defines four gate
items:

1. ✅ **All E2E phases pass on a clean test host** — Phase A-H
   (baseline driver), Phase I (12 steps), K, L, L-DNS, M (7 steps),
   N (6 steps) all wired and verified under DRY_RUN. Live run is
   integrator scope per validator's Issue 1 resolution.
2. ✅ **All previous sprints' acceptance criteria still hold** —
   Sprint 5 EDNS deferral closed (Sprint 6 staff Issue 1 resolved);
   Sprint 5 chapter 22 reorder confirmed (Sprint 5 tech-writer
   Issue 14 closed); Sprint 5 TruncatedFlag re-added (Sprint 5
   validator Issue 4 closed).
3. ✅ **Doctor green-by-default on a stock dev box** — refactored
   in `internal/doctor/doctor.go`; pinned by
   `TestHasFailures_StockDevBoxGreen`; live integrator sign-off per
   `docs/E2E_TEST.md` §5.
4. ⚠ **All 32 chapters drafted (some still rough — Sprint 7 polishes)**
   — 32 chapters present, but the inaccuracies surfaced in Issues 1-
   10 represent more than "rough polish" for some chapters:
   - Issue 1 (`--use-existing-cluster` flag that doesn't exist) is a
     **release-blocker** — it goes to the front of the book's user-
     facing E2E surface and a user running the documented command
     will be confused.
   - Issues 2 + 3 + 4 are **medium** — they break documented user
     workarounds + misrepresent the e2e coverage. Each one is a
     ~5-line chapter edit; Sprint 7's polish window has time.
   - Issues 5 + 6 + 8 are **medium** — drift between book and
     code/scripts; cumulatively they erode user trust in the docs.

**Verdict**: **Sprint 6 gate criteria met with one blocker carry-over
(Issue 1) for Sprint 7 polish**. The v1.0 tag should not be cut until
at least Issues 1-5 are resolved. Issues 6-10 are Sprint 7 polish
items; Issue 11 is a labelling decision; Issue 12 is v1.x scope.

Overall, the sprint is in materially good shape — chapter 22 reorder,
chapter 21 EDNS update, doctor refresh, MIGRATING.md, the two auto-
generators, the `.github/workflows/e2e-full.yml`, the cred-resolver
invariance test, the dropped `ENTRYPOINT ["ibmcloud"]` shim, and all
9 hand-written chapters land cleanly. The findings here are the
expected "second-pass tech-writer review" surface: cross-document
drift + a handful of forward-references that didn't get pruned during
integration. Nothing v1.0-blocking that Sprint 7's polish pass can't
close in a single PR.
