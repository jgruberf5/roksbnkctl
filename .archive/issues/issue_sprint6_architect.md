# Sprint 6 — architect issues

## Issue 1: chapter 27 (command reference) is a placeholder; staff's `tools/refgen/cobra-md` generator was not landed at architect-pass end
**Severity**: medium
**Status**: open
**Description**: Chapter 27 is auto-generated per the Sprint 6 plan — staff was to ship `tools/refgen/cobra-md/main.go` which walks the cobra command tree and emits one section per command. At the time the architect agent finished its pass, `tools/refgen/` did not exist in the tree (`ls /mnt/d/project/roksbnkctl/tools/` returned only `docker/` and `sprintwatch/`). Architect committed a placeholder chapter explaining the auto-generation pattern + how to re-run, listing what the rendered chapter will cover, and filed this issue so the integrator runs the generator at integration time.

**Files affected**: `book/src/27-command-reference.md`
**Proposed fix**: integrator confirms staff landed `tools/refgen/cobra-md/main.go`. If yes: `go run ./tools/refgen/cobra-md > book/src/27-command-reference.md` and commit the rendered output, replacing the placeholder. If staff deferred the generator: leave the placeholder, re-open as a v0.9.1 / v1.0 follow-up.

## Issue 2: chapter 29 (terraform variable reference) is a placeholder; staff's `tools/refgen/tfvars-md` generator was not landed at architect-pass end
**Severity**: medium
**Status**: open
**Description**: Same shape as issue 1, applied to chapter 29 and the HCL-to-markdown generator. `tools/refgen/tfvars-md/main.go` was to parse `terraform/variables.tf` (and any submodule `variables.tf` files) and emit a sorted table of every variable with name, type, default, description, sensitive flag, and the module path. At architect-pass end the generator was not in the tree.

**Files affected**: `book/src/29-terraform-variable-reference.md`
**Proposed fix**: integrator confirms staff landed `tools/refgen/tfvars-md/main.go`. If yes: `go run ./tools/refgen/tfvars-md > book/src/29-terraform-variable-reference.md` and commit the rendered output. If staff deferred the generator: leave the placeholder, re-open as a v0.9.1 / v1.0 follow-up.

## Issue 3: chapter 23 forward-references staff's validator-CI workflow and the doctor green-by-default behaviour
**Severity**: low
**Status**: open
**Description**: Chapter 23 §"How CI runs it" describes the manual-trigger workflow for `scripts/e2e-test-full.sh` — "see the validator agent's e2e CI workflow file (landed in Sprint 6) for the concrete YAML." Architect didn't know the validator agent's eventual filename for the workflow. Same shape for chapter 23 §"What each phase validates" §"Phase A" — references doctor's green-by-default behaviour on a stock dev box, which is the staff agent's Sprint 6 deliverable.

**Files affected**: `book/src/23-e2e-test-plan.md` §"How CI runs it"
**Proposed fix**: integrator confirms (a) the validator's CI workflow filename (likely `.github/workflows/e2e.yml` or similar) and updates the cross-link to a direct GitHub URL, and (b) the doctor refresh actually landed and reports green when only `terraform` is on PATH.

## Issue 4: chapter 26 entry "Workspace-delete current-workspace gotcha" assumes `ROKSBNKCTL_WORKSPACE` env var semantics; verify against shipped code
**Severity**: low
**Status**: open
**Description**: Chapter 26 §"Symptom: `roksbnkctl ws delete <name>` succeeds but subsequent commands still use the deleted workspace" claims workspace context is set by the `ROKSBNKCTL_WORKSPACE` env var. The codebase uses `flagWorkspace` (cobra `--workspace` / `-w`) and `config.New(flagWorkspace)` reads from there; whether there's a backing env var on the persistent flag wasn't directly verified.

**Files affected**: `book/src/26-troubleshooting.md` §"Workspaces"
**Proposed fix**: integrator greps `internal/cli/root.go` and `internal/config/context.go` for `ROKSBNKCTL_WORKSPACE` / `os.Getenv` interactions. If the env var is wired, chapter is correct. If only `-w`/`--workspace` is supported, chapter prose softens to "set by the `-w` flag" — the gotcha shape remains valid either way (deleting a workspace doesn't clear the user's intent to use it).

## Issue 5: chapter 23 cites a per-phase log path `/tmp/roksbnkctl-e2e-backends/` that may not match validator's landed driver
**Severity**: low
**Status**: open
**Description**: Chapter 23 §"Per-phase logs" and §"Re-runnability" cite the per-phase log directory as `/tmp/roksbnkctl-e2e-backends/<phase>-<ts>.log` (and `/tmp/roksbnkctl-e2e/` for the baseline). This matches PRD 05 §"Test infrastructure" and what the existing `scripts/e2e-test-backends.sh` produces today, but validator's Sprint 6 work may consolidate or rename. If validator picked a different log root (e.g., `~/.roksbnkctl/e2e-logs/`), the chapter's path is wrong.

**Files affected**: `book/src/23-e2e-test-plan.md` §"Per-phase logs"
**Proposed fix**: integrator greps validator's landed `scripts/e2e-test-backends.sh` and `scripts/e2e-test-full.sh` for the log root and updates the chapter's literal path to match.

## Issue 6: chapter 25 `cos object put --multipart` flag is forward-statement; the v1.0 binary auto-multiparts based on file size
**Severity**: low
**Status**: open
**Description**: Earlier drafts of the architect prompt mentioned a `cos object put --multipart` and `cos object get --stream` flag surface. Reading `internal/cli/cos.go`, neither flag exists — `cos object put` always uses `PutObjectFromFile` (which the IBM COS SDK auto-multiparts when the file size exceeds the SDK's threshold) and `cos object get` always streams via `GetObjectToFile`. Chapter 25 documents the actual v1.0 behaviour ("transparent — there's no `--multipart` flag to set") rather than the aspirational flag set. If staff lands explicit flags during sprint integration, the chapter's prose needs a one-paragraph addition.

**Files affected**: `book/src/25-cos-supply-chain.md` §"Multipart upload and streaming download"
**Proposed fix**: integrator confirms `roksbnkctl cos object put --help` against the landed binary doesn't show `--multipart`. If correct: chapter is fine. If staff added the flag: add a flag-table row.

## Issue 7: chapter 31 references `goreleaser.yml` at repo root; verify the file's path and shape
**Severity**: low
**Status**: open
**Description**: Chapter 31 §"Quick build" and §"Release process" reference `goreleaser.yml` as the canonical multi-platform build config. I did not verify the file exists in the tree — it may live at `.goreleaser.yml` (the conventional dotfile location) or not at all (release.yml could use a direct goreleaser invocation without a config file). Same chapter §"Build via the Makefile" cross-references the Makefile's actual targets, which I did verify against the source.

**Files affected**: `book/src/31-building-from-source.md` §"Quick build"; §"Release process"
**Proposed fix**: integrator `ls`'s the repo root for `goreleaser.yml` / `.goreleaser.yml`. If neither exists, the chapter's cross-link gets dropped or repointed at `release.yml`'s direct goreleaser command. If `.goreleaser.yml` exists, fix the chapter's path to match.

## Issue 8: chapter 22 had a flow-reorder per Sprint 5 tech-writer Issue 14; verify the reorder doesn't break any inbound anchor links
**Severity**: low
**Status**: open
**Description**: Per Sprint 5 tech-writer Issue 14, the §"OpenShift SCC failure mode" section in chapter 22 was moved from its original position (after §"Reading the output", around line 194 of the pre-reorder file) to immediately after §"The bundled image and the `runAsNonRoot` constraint" so a user reading top-to-bottom hits the full SCC story before being shown sample iperf3 output. Two of the other Sprint 5 chapters (and potentially the new chapter 26) may anchor-link to the moved section. The slug `#openshift-scc-failure-mode` is stable (it's derived from the header text), so the link target remains valid — but the relative position of nearby anchors changed.

**Files affected**: `book/src/22-throughput-testing.md` §"OpenShift SCC failure mode" (now repositioned before §"Reading the output")
**Proposed fix**: integrator greps the rest of the book for `22-throughput-testing.md#openshift-scc-failure-mode` and confirms the anchor still resolves under the new section ordering. mdbook's default slug algorithm is GitHub-Flavored Markdown-compatible, so the slug derives from the header text alone — moving the section shouldn't change the slug.

## Issue 9: chapter 26 troubleshooting entry "doctor refresh" green-by-default behaviour is staff's Sprint 6 deliverable; chapter assumes it's landed
**Severity**: low
**Status**: open
**Description**: Chapter 26's §"Symptom: doctor reports `terraform: not found` on a fresh dev box" entry and chapter 23's §"Phase A — `init`" both assume the staff agent's Sprint 6 doctor refresh has landed — i.e., that doctor reports green when only `terraform` is on PATH and informational (not warning) for kubectl/oc absence. If staff deferred the refresh, doctor still treats kubectl/oc as warnings and the chapter prose overstates the green-by-default story.

**Files affected**: `book/src/23-e2e-test-plan.md` §"Phase A — `init`"; `book/src/26-troubleshooting.md` §"Symptom: doctor reports `terraform: not found`…"
**Proposed fix**: integrator runs `roksbnkctl doctor` on a stock dev box (or grep `internal/cli/meta.go` / `internal/cli/doctor_backend.go` for the kubectl/oc check severity). If green-by-default is in: chapters are correct. If not: one-line edits softening "green" to "informational" until the refresh lands in a follow-up.

## Issue 10: glossary entry for "Cell" is stub-shaped — confirm whether the term is actually used elsewhere in the book
**Severity**: low
**Status**: open
**Description**: Chapter 30 has an entry for "Cell (k8s)" defined as "a worker-node grouping concept used by some OpenShift extensions; not BNK-specific. Not used directly by `roksbnkctl`." I added it on the assumption it might appear in BNK-related OpenShift docs, but a grep of the book turns up only `Cell` in capitalised-word noise (it's a common English word, so my acronym-grep flagged false positives). If no other chapter uses "Cell" as a k8s term, the entry is dead weight and should be removed.

**Files affected**: `book/src/30-glossary.md` §"Cell"
**Proposed fix**: integrator greps the book for word-boundary `\bCell\b` in a k8s context. If not used: drop the entry. If used: confirm the definition matches the context.

## Issue 11: chapter 26 "Adequate disk for terraform plan output" pre-req item in chapter 23 may understate disk needs
**Severity**: low
**Status**: open
**Description**: Chapter 23 §"Pre-requisites" claims terraform plan output needs ~200 MB of disk for the embedded module's state — this is an estimate based on similar IBM provider state sizes, not a measured number. The actual state file (post-apply, for the full ROKS + BNK install) may be larger (>500 MB is not uncommon for 77-resource states) or smaller. A user on a small CI runner who hits "disk full" will get a much less obvious error than the chapter implies.

**Files affected**: `book/src/23-e2e-test-plan.md` §"Pre-requisites"
**Proposed fix**: integrator measures `du -sh ~/.roksbnkctl/<ws>/state/` on a freshly-applied workspace and updates the chapter's number to the actual measurement. If the number is materially larger, also bump the implied "disk for the workspace" requirement to a more honest figure.
