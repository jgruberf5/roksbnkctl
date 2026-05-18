# Sprint 2 — tech writer issues

Findings cover the 7 new chapters (5, 6, 8, 9, 10, 11, 24), the staff
agent's `internal/k8s/*` and `internal/cli/k_*.go` implementation, the
validator's tests, and CONTRIBUTING / README / PLAN.md / PRD 02 drift.
All findings are doc-and-example-correctness only — no code changes
proposed.

## Issue 1: chapter 11 documents a `cluster down` trial-state-empty check that does not exist in the implementation

**Severity**: medium
**Status**: open
**Description**: Chapter 11 § "Order matters: trial first, then cluster"
(lines 50-59) tells the reader that `roksbnkctl cluster down`
**refuses to run** if the workspace's BNK trial state still lists
resources, and quotes a specific error message:

> `Error: workspace "default" has BNK trial state with N resources still present;`
> `       run `roksbnkctl down` first to destroy the trial before destroying the cluster`

It then doubles down at line 59: "`--auto` does **not** override this;
the order is correctness, not preference."

The actual `runClusterDown` in
`/mnt/d/project/roksbnkctl/internal/cli/cluster_phase.go` (lines
319-344) implements no such check. When `--auto` is unset the user
sees a *warning text* on stderr ("Any BNK trial state on top of this
cluster will be orphaned — run `roksbnkctl down` first if needed.")
followed by an interactive `Continue? [y/N]` prompt. With `--auto`
the warning and prompt are *both* skipped — `cluster down --auto` will
happily start `terraform destroy` against a cluster that still hosts
trial state. There is no inspection of `state/terraform.tfstate`'s
resource count anywhere in the cluster-down path.

A reader following chapter 11's clean-teardown sequence (lines 62-72)
will get the right outcome by accident — they call `down --auto` then
`cluster down --auto` in order. A reader who skips `down` and just
runs `cluster down --auto` (perhaps because the chapter promised a
guard) will get a half-destroyed cluster with orphaned trial pods,
not a clean error.

**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/11-tearing-down.md` (lines 50-59,
specifically the "refuses to run" claim and the quoted error
message);
`/mnt/d/project/roksbnkctl/internal/cli/cluster_phase.go` (lines
319-344) for the actual behavior.

**Proposed fix**: pick one of two paths:

1. **Doc-side fix** (smaller change): rewrite chapter 11 § "Order
   matters" to describe what's actually shipped — a textual warning
   on the prompt, no hard guard, `--auto` skips the warning. Drop the
   quoted error message and the "`--auto` does not override this"
   sentence. Then file a follow-up issue for staff to add the real
   check in a later sprint.
2. **Code-side fix** (larger change): file a staff issue to implement
   the check chapter 11 documents (read `state/terraform.tfstate` via
   `tfws.Show`, count resources, refuse on non-zero unless an
   explicit `--orphan-trial` flag is set). The chapter then becomes
   accurate as written.

Path 1 is the right Sprint 2 outcome (the ordering correctness check
isn't in scope for this sprint); Path 2 is for a future sprint.

## Issue 2: chapter 5 sample doctor output for kubectl/oc rows does not match what the binary actually prints

**Severity**: medium
**Status**: open
**Description**: Chapter 5 § "What `doctor` checks" (lines 11-22)
shows a sample `roksbnkctl doctor` output. The kubectl and oc rows are
documented as:

```
✓  kubectl           /usr/local/bin/kubectl (clientVersion:)                                  (informational; `roksbnkctl k get/apply/...` covers the happy path natively)
✓  oc                /usr/local/bin/oc (Client Version: 4.21.10)                              (informational; `roksbnkctl k ...` covers the happy path natively)
```

Staff agent's `runWithWhy` in
`/mnt/d/project/roksbnkctl/internal/doctor/doctor.go` (lines 73-74)
attaches a different "why" blurb:

```go
out = append(out, checkBinaryInformational("kubectl", "internalised in roksbnkctl k *; passthrough still works if installed"))
out = append(out, checkBinaryInformational("oc", "internalised in roksbnkctl k *; passthrough still works if installed"))
```

So the actual rendered rows will read `(internalised in roksbnkctl k
*; passthrough still works if installed)` — different wording in both
the message and the punctuation. The chapter 5 sample also shows
`clientVersion:` with no value as the kubectl detail line; the actual
detail comes from `versionLine("kubectl")` which runs `kubectl
version --client=true --output=yaml` and returns the first non-empty
line of that output, which for current kubectl is something like
`clientVersion:` followed by indented YAML on subsequent lines. The
first non-empty line *is* `clientVersion:` so that part is accidentally
correct, but the chapter doesn't explain that the detail is the first
line of YAML output.

A reader who runs `roksbnkctl doctor` after reading chapter 5 sees
output that doesn't match the chapter's sample for those two rows,
which mildly undermines the chapter's "this is what doctor prints"
contract.

The other rows in chapter 5's sample (terraform, iperf3, ibmcloud,
kubeconfig, workspace, ibmcloud api key, ibm cloud auth) match the
actual blurbs in `doctor.go` exactly.

**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/05-doctor.md` (lines 15-16, the
kubectl + oc rows in the sample-output block).

**Proposed fix**: replace the "why we care" parentheticals on lines
15-16 with the actual blurbs the binary prints:

```
✓  kubectl           /usr/local/bin/kubectl (clientVersion:)                                  (internalised in roksbnkctl k *; passthrough still works if installed)
✓  oc                /usr/local/bin/oc (Client Version: 4.21.10)                              (internalised in roksbnkctl k *; passthrough still works if installed)
```

Either change the chapter to match the code, or file a staff
follow-up to change `doctor.go` lines 73-74 to match the chapter
prose. The chapter prose is more readable (`covers the happy path
natively` is friendlier than `internalised in roksbnkctl k *`); if
someone has a preference, the chapter wins and `doctor.go` should
follow.

## Issue 3: README `Highlights` section missing a Sprint 2 bullet for the internalised `k` commands

**Severity**: medium
**Status**: open
**Description**: Sprint 1 added a `--on jumphost` highlight bullet
(README line 35) when that feature shipped. Sprint 2's equivalent —
the internalised `roksbnkctl k get/apply/logs/exec/port-forward`
verbs that remove the `kubectl`-on-PATH requirement for the everyday
workflow — has not been mirrored into the README's `Highlights`
section (lines 27-35). The Sprint 2 brief for tech writer flagged
this as analogous to Sprint 1 Issue 10 (the README + CONTRIBUTING
parallel update).

The `Cluster ops (post-deploy)` table (lines 271-285) still lists
`roksbnkctl kubectl <args...>` and `roksbnkctl oc <args...>` as
passthroughs but does **not** mention `roksbnkctl k get/apply/...`.
A reader scanning the README to decide whether to install
`roksbnkctl` won't learn that it now ships kubectl-equivalent verbs
natively — they'd have to find the book's chapter 24.

The `doctor` row in the `Operations + meta` table (line 301) also
still says: "Eight-check prereq + credentials report: `terraform` /
`iperf3` / `kubectl` / `oc` / `ibmcloud` on PATH" — implying all five
are required-ish prereqs, which is no longer true post-Sprint 2
(kubectl + oc are informational).

**Files affected**:
`/mnt/d/project/roksbnkctl/README.md` (lines 27-35 highlights,
271-285 cluster-ops table, line 301 doctor description).

**Proposed fix**: three small README edits.

1. Add a new highlight bullet between the existing `--on jumphost`
   bullet (line 35) and the closing `---`:

   - **Internalised kubectl verbs (v0.8)** — `roksbnkctl k
     get/apply/describe/delete/logs/exec/port-forward` run natively
     in-process via `client-go`; no host `kubectl` required for the
     everyday workflow. Top-level `roksbnkctl get` / `logs` for
     muscle-memory parity. Host `kubectl` / `oc` are now
     informational on `roksbnkctl doctor`. See [chapter
     24](https://jgruberf5.github.io/roksbnkctl/book/24-day-2-ops.html).

2. Add `k get/apply/describe/delete/logs/exec/port-forward` rows to
   the `Cluster ops (post-deploy)` table, marking the top-level
   aliases (`get`, `logs`) where they exist.

3. Soften the line 301 `doctor` description: "`terraform` / `iperf3`
   on PATH; `kubectl` / `oc` / `ibmcloud` informational; …".

## Issue 4: PLAN.md Sprint 2 deliverable list still claims top-level `apply` alias was shipped

**Severity**: low
**Status**: open
**Description**: PLAN.md § "Sprint 2 — kubectl internalization" §
"Code deliverables" line 245 lists item 10 as:

> `internal/cli/k_*.go` — wire `roksbnkctl k get/apply/describe/delete/exec/logs/port-forward` plus top-level aliases for `get/apply/logs`

Staff agent's actual delivery — documented in
`/mnt/d/project/roksbnkctl/internal/cli/k_aliases.go` lines 19-24 and
captured in `issues/issue_sprint2_staff.md` Issue 1 — only ships
top-level aliases for `get` and `logs`. `apply` was deliberately not
aliased because the existing top-level `apply` runs `terraform apply`
(Sprint 0/1 lifecycle verb) and adding a second `apply` would shadow
it. Chapter 24 (lines 32-41) accurately documents this divergence.
PLAN.md does not.

The drift is small and PLAN.md is a planning doc, not user-facing.
But the same goal section (line 230) repeats the misclaim:

> `roksbnkctl get/apply/logs/exec/port-forward` works without `kubectl` on PATH

This phrasing reads as if those five are top-level commands — only
`get` and `logs` are; `apply`, `exec`, `port-forward` are reachable
only through `roksbnkctl k <verb>`.

**Files affected**:
`/mnt/d/project/roksbnkctl/docs/PLAN.md` (lines 230, 245).

**Proposed fix**: small textual update on both lines:

- Line 230: change to `… `roksbnkctl k get/apply/logs/exec/port-forward` works without `kubectl` on PATH (top-level aliases for `get` and `logs`)`.
- Line 245: change `top-level aliases for `get/apply/logs`` to `top-level aliases for `get` and `logs`; `apply` deliberately not aliased to avoid shadowing the lifecycle `apply` (see issues/issue_sprint2_staff.md)`.

## Issue 5: chapter 24 OpenShift extensions section is more pessimistic than the as-shipped behaviour

**Severity**: low
**Status**: open
**Description**: Chapter 24 § "OpenShift extensions (Phase 2.1)"
(lines 282-299) frames `roksbnkctl k get projects` /
`get routes` / `get imagestreams` as a Phase 2.1 future-feature that
"may not have shipped" in v0.8, and tells readers to fall back to
`roksbnkctl oc get projects` if it didn't.

In fact `roksbnkctl k get` already discovers OpenShift CRDs **today**
via the dynamic client + RESTMapper path in
`/mnt/d/project/roksbnkctl/internal/k8s/get.go` (lines 67-78,
`resource.Builder`'s `.Unstructured().ResourceTypeOrNameArgs(...)`
chain). Staff documents this explicitly in
`/mnt/d/project/roksbnkctl/internal/k8s/openshift.go` (lines 15-19):

> Phase 2.0 ships without this — the dynamic-client + RESTMapper path
> in get.go already discovers OpenShift CRDs late-bound (the cluster
> advertises them via the API discovery doc and our DeferredDiscovery
> mapper picks them up). The typed-client path is purely an
> optimisation for Phase 2.1.

So `roksbnkctl k get projects` / `routes` / `imagestreams` will work
against any ROKS cluster running v0.8 today — Phase 2.1 only adds the
*typed* client (`openshift/client-go`) for nicer printing of those
resources, not the ability to fetch them.

A reader following chapter 24 will see the "Phase 2.1 may not have
shipped" caveat and unnecessarily fall back to the `oc` passthrough,
when the native path actually works.

**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/24-day-2-ops.md` (lines 282-299).

**Proposed fix**: rewrite the section to reflect what's actually
shipped:

- The first paragraph should say something like "ROKS clusters
  surface OpenShift-specific resource types alongside core
  Kubernetes resources. `roksbnkctl k get` discovers these via the
  dynamic client + RESTMapper, so commands like `get projects` work
  against any ROKS cluster today without typed-client support.
  Phase 2.1 (deferred to a later sprint) adds typed clients for
  prettier printing and `describe` integration."
- The "If 2.1 isn't there yet, fall back to the passthrough" line
  (lines 295-298) should be deleted or rewritten as "if you want
  typed-client output for an OpenShift resource, fall back to
  `roksbnkctl oc`".

## Issue 6: chapter 24 `roksbnkctl exec` cross-reference points at chapter 6 but should point at chapter 16

**Severity**: low
**Status**: open
**Description**: Chapter 24 line 41:

> **`roksbnkctl exec`** runs a command on the **host** with the
> workspace's env loaded (Sprint 1's host-exec verb — see [Chapter
> 6](./06-workspaces.md)). `roksbnkctl k exec` runs in a pod.

Chapter 6 ("Workspaces") covers `roksbnkctl shell` and the
`-w`/`--workspace` flag, but does **not** document `roksbnkctl exec`
host-side. The verb's actual deep-dive — including the `--on
jumphost` interaction that's the main reason it exists — is in
chapter 16 (`The --on flag and SSH jumphosts`), specifically the
"Working examples" and "Behaviour details" sections.

A reader who clicks the chapter 6 link looking for `exec`
documentation will not find it.

**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/24-day-2-ops.md` (line 41).

**Proposed fix**: change `[Chapter 6](./06-workspaces.md)` to
`[Chapter 16](./16-on-flag-ssh-jumphosts.md)` on line 41. Chapter 16
is the canonical reference for host-side `exec` and `shell`
behaviour.

## Issue 7: chapter 6 parking-lot example mis-attributes `roksbnkctl down` to e2e Phase H

**Severity**: low
**Status**: open
**Description**: Chapter 6 § "The parking-lot pattern" (lines 175-193)
introduces the example with:

> ```bash
> # Phase H of scripts/e2e-test.sh: tear-down + cleanup
>
> # Run the destroy against "default" (still current at this point)
> roksbnkctl down --auto
> ```

Looking at the actual e2e-test.sh, Phase H is **cleanup-only**: it
creates the parking-lot workspace, switches to it, and deletes the
original. The `roksbnkctl down --auto` in the chapter's example does
not appear in Phase H — it's in **Phase D (D8)** at line 370 of
`scripts/e2e-test.sh`. Phase H starts at line 455 and contains only
the parking-lot dance plus the post-delete dir-existence check.

Chapter 11 § "The full clean-as-you-go pattern from
`scripts/e2e-test.sh` Phase H" (lines 151-169) makes the same misclaim
— it shows `roksbnkctl down --auto` followed by `roksbnkctl cluster
down --auto` followed by the parking-lot dance, all attributed to
"Phase H".

A reader cross-referencing the chapters with the script will be
briefly confused; this is a low-severity accuracy slip.

**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/06-workspaces.md` (lines 175-193);
`/mnt/d/project/roksbnkctl/book/src/11-tearing-down.md` (lines 151-169).

**Proposed fix**: in chapter 6 line 176 change the comment to
something accurate: e.g. `# End-to-end test cleanup (e2e-test.sh
Phase D destroys; Phase H runs the parking-lot dance below)`. In
chapter 11 line 151 change "from `scripts/e2e-test.sh` Phase H" to
"from `scripts/e2e-test.sh` (Phase D destroys, Phase H parks and
deletes)". Either correction restores the chapter's accuracy without
losing the e2e cross-reference.

## Issue 8: chapter 5 "Common failures" table fix-recommendation diverges from doctor.go's hint

**Severity**: low
**Status**: open
**Description**: Chapter 5 § "Common failures and how to fix them"
table (line 116) maps the `kubeconfig: $KUBECONFIG and ~/.kube/config
both missing` symptom to the fix:

> `roksbnkctl kubeconfig --download` or run `roksbnkctl up`

The actual doctor output for the same failure (from
`/mnt/d/project/roksbnkctl/internal/doctor/doctor.go` line 179)
suggests:

> `$KUBECONFIG and ~/.kube/config both missing — fetch with `ibmcloud ks cluster config --admin``

So the doctor row tells the user to use `ibmcloud ks cluster config
--admin` (the legacy host-binary path) and the chapter tells them to
use `roksbnkctl kubeconfig --download` (the in-process path that
already exists today).

The chapter's recommendation is the better one — `roksbnkctl
kubeconfig --download` doesn't require the `ibmcloud` CLI on PATH
and is the canonical roksbnkctl-native way to get an admin
kubeconfig. The doctor's inline hint is left over from before
`kubeconfig --download` was internalised.

**Files affected**:
`/mnt/d/project/roksbnkctl/internal/doctor/doctor.go` (line 179, the
inline fix-hint).

**Proposed fix**: a small staff follow-up to update doctor.go line
179 to match the chapter:

```go
c.Detail = "$KUBECONFIG and ~/.kube/config both missing — fetch with `roksbnkctl kubeconfig --download`"
```

Chapter 5 is correct and shouldn't change. The doctor message and
the chapter should both surface the in-process recommendation.

## Issue 9: chapter 11 "lands in Sprint 6" annotation on chapter 26 is inconsistent with chapter 5

**Severity**: low
**Status**: open
**Description**: Chapter 11 line 207:

> [Chapter 26 — Troubleshooting](./26-troubleshooting.md) (lands in
> Sprint 6) — recovery from partial-destroy and orphan-state
> scenarios.

Chapter 5 line 123 references the same chapter 26 with no "lands in
Sprint X" annotation:

> If a fix isn't here, [Chapter 26 — Troubleshooting](./26-troubleshooting.md) covers the longer tail.

These chapters are inconsistent about whether chapter 26 is treated
as forthcoming or already-existing. (`book/src/26-troubleshooting.md`
exists but is a stub today.) Other forward-references in the new
chapters use the "lands in Sprint N" pattern consistently:

- chapter 5 line 186: chapter 14 "(lands in Sprint 3)"
- chapter 6 line 236: chapter 12 "lands in Sprint 3"
- chapter 9 line 232: chapter 25 "(lands in Sprint 6)"
- chapter 10 line 204: chapter 13 "(lands in Sprint 3)"
- chapter 11 line 207: chapter 26 "(lands in Sprint 6)"

So chapter 5's reference to chapter 26 (line 123) is the outlier — it
should also say "(lands in Sprint 6)" to match the rest.

**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/05-doctor.md` (line 123).

**Proposed fix**: change line 123 to: `If a fix isn't here, [Chapter
26 — Troubleshooting](./26-troubleshooting.md) (lands in Sprint 6)
covers the longer tail.`

## Issue 10: chapter 24's reference to PLAN.md "Phase 2.1 which may slip" mis-quotes PLAN.md

**Severity**: low
**Status**: open
**Description**: Chapter 24 line 293:

> Whether Phase 2.1 lands for v0.8 depends on staff agent budget —
> PLAN.md scopes it as "Phase 2.1 which may slip".

PLAN.md line 274 actually says:

> OpenShift CRDs (Phase 2.1) require `openshift/client-go` which has
> its own version dance — defer to Sprint 5 polish if not clean by
> sprint end

The "which may slip" wording isn't in PLAN.md anywhere. The chapter
is invoking PLAN.md's authority for a quote that doesn't exist. (The
spirit is correct — PLAN.md does flag Phase 2.1 as deferrable —
but the verbatim quote misleads anyone who searches PLAN.md for the
phrase.)

This is closely tied to Issue 5 above (which proposes rewriting that
whole section anyway). If Issue 5 is accepted and the section is
rewritten, this issue resolves itself.

**Files affected**:
`/mnt/d/project/roksbnkctl/book/src/24-day-2-ops.md` (line 293).

**Proposed fix**: either fold into the Issue 5 rewrite, or change the
quoted phrase to a paraphrase: `PLAN.md flags it as deferrable to
Sprint 5 polish if not clean by sprint end`. The quoted-string form
should not survive — only paraphrase or a copy-paste of PLAN.md's
actual line.
