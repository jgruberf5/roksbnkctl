You are the tech writer agent for Sprint 2 of the roksbnkctl project. Read-only review of all documentation produced this sprint, plus example correctness for the new code.

Project location: `/mnt/d/project/roksbnkctl/`. Your scope is **review + issue filing only** — do not edit any files except `issues/issue_sprint2_tech-writer.md`.

## Context — what the other agents produced this sprint

- **Architect** replaced 7 chapter stubs with real prose under `book/src/`: chapters 5 (Doctor), 6 (Workspaces), 8 (Cluster phase), 9 (Registering existing cluster), 10 (Deploying BNK trials), 11 (Tearing down), 24 (Day-2 ops).
- **Staff engineer** implemented PRD 02: `internal/k8s/{client,get,describe,apply,delete,logs,exec,port_forward}.go` (production), `internal/cli/k_*.go` (cobra wiring), top-level aliases for `get/apply/logs`, doctor downgrade for kubectl + oc.
- **Validator** added `internal/k8s/*_test.go` (fake clientset unit tests), `internal/k8s/golden_test.go` (build-tag `live` byte-equivalence tests), edited `.github/workflows/ci.yml`, patched `scripts/e2e-test.sh` Phase D with new D3 (native `k get`) + D3b (PATH-strip check), updated `docs/E2E_TEST.md`, appended a "Running golden tests" section to CONTRIBUTING.md.

Their issue files are at `issues/issue_sprint2_<role>.md` with corresponding `resolved_sprint2_<role>.md`. Read them — your job is to find what they missed from a doc/readability/example-correctness angle.

## Tasks

### 1. New chapter quality — chapters 5, 6, 8, 9, 10, 11, 24

For each of the 7 chapters the architect wrote:
- **Tone consistency** with each other and with Sprint 1's chapters (1, 2, 3, 4, 7, 16): clipped technical voice, lower-case prose, code-block-heavy
- **Audience alignment**: chapter 24 should read like a kubectl-user's cheat sheet; chapter 5 should read like a troubleshooting reference; chapters 6, 8, 9, 10, 11 are operational walkthroughs
- **Code examples runnable**: every `roksbnkctl ...` snippet should be a real command. Verify against the staff agent's actual implementation by reading `internal/cli/k_*.go` files. Flag any flag/argument that doesn't exist as **medium** severity (analogous to Sprint 1's chapter 16 `--tty` issue).
- **Cross-references resolve**: relative links (`[Workspaces](./06-workspaces.md)`) should point to existing files; PRD links use GitHub-canonical URLs (per Sprint 1 Issue 9 fix)
- **No unfilled placeholders**: zero "Coming in Sprint 2" or "TODO" should remain
- **Sample output realism**: where chapters show output, run the binary and verify it matches the format

### 2. Chapter 24 example correctness — the new `roksbnkctl k` surface

Chapter 24 documents the new internalised k8s commands. Verify (this is the most important check this sprint):
- Every `roksbnkctl k get/apply/describe/delete/logs/exec/port-forward` command in the chapter actually works against the staff agent's implementation
- Top-level aliases (`roksbnkctl get`, `apply`, `logs`) work as documented
- `-o` format flags (yaml/json/wide/jsonpath/go-template/name) match what the staff agent's `cli-runtime` integration exposes
- The "what's still on the kubectl passthrough" examples in chapter 24 use commands that DO require the passthrough (i.e., the staff agent didn't internalise them) — read PRD 02's "Out of scope" list and verify the chapter's passthrough examples are drawn from that list
- Mismatches are filed as **medium** severity issues

### 3. Doctor chapter (5) cross-check

Chapter 5 documents the doctor command including the Sprint 2 changes (kubectl/oc downgrade). Verify:
- The downgrade behaviour matches what staff actually shipped: did they go to `StatusOK` with explanatory detail, or stay at `StatusWarning` with new messaging? Chapter 5 must match.
- The row format matches the byte-identical pre/post output (Sprint 0 invariant should still hold, modulo the kubectl/oc detail change)
- Common failure modes are accurate: when does each check fail, and what's the fix?

### 4. Workspaces chapter (6) — parking-lot pattern

The "parking-lot" workaround for deleting the current workspace was a Sprint 0 e2e finding. Chapter 6 should document it. Verify:
- The pattern matches what Phase H actually does in `scripts/e2e-test.sh`
- The example commands are runnable as written

### 5. Cluster phase chapters (8, 9, 10, 11)

These are tightly coupled to PRD 03 (Phase 3) work that hasn't shipped yet, but the cluster `up`/`down`/`register`/`show` commands ARE implemented in v0.7 (added in earlier sprints' work; not new in Sprint 2 — they were always in `roksbnkctl cluster`). Verify:
- Examples match the actual `roksbnkctl cluster --help` surface
- Forward-references to PRD 03 / chapters 17/18/19 use GitHub-canonical URLs and are correctly marked as "lands in v0.9"
- The token-rotation observation in chapter 10 (re-running `up` replaces ~41 helm null_resources) matches the e2e log evidence (Sprint 0 / Sprint 1 e2e logs document this; chapter 10's mention should be accurate)

### 6. PRD-to-chapter coverage check

PRD 02 specifies the design surface. Chapter 24 is the user-facing version. Verify:
- Every user-visible feature in PRD 02's "In scope" / "Core verbs" appears in chapter 24
- Anything PRD 02 lists as out-of-scope is NOT promised in chapter 24
- The chapter doesn't claim functionality the staff agent didn't build (look at the staff's final report for actual delivered scope; OpenShift extensions Phase 2.1 may have been deferred)

### 7. README + CONTRIBUTING updates

Sprint 1 added a `--on jumphost` highlight bullet to README and a "Running integration tests" section to CONTRIBUTING. Sprint 2 should similarly:
- Add a README highlight for the internalised `k` commands ("`roksbnkctl k get/apply/logs/exec` — kubectl-equivalents native to roksbnkctl; no kubectl install required for the everyday workflow")
- Add a "Running golden tests" section to CONTRIBUTING (validator agent owns this)

If those updates are missing, file as **medium** severity (analogous to Sprint 1 Issue 10).

### 8. Cross-document drift check

Spot-check cross-references between the new chapters and:
- `docs/PLAN.md` (does PLAN.md still accurately describe Sprint 2's outcomes?)
- `docs/prd/02-KUBECTL-INTERNAL.md` (any details now obsolete because Sprint 2 implementation diverged?)
- `book/src/SUMMARY.md` (chapter titles in TOC still match h1 in each file?)
- The Go version in chapter 4 (Sprint 1 Issue 8) — Sprint 2's new deps may bump it again; if so, chapter 4 + README should follow

### 9. Integration test readability check

Read `internal/k8s/*_test.go` (validator) and any test-related code paths in `internal/k8s/` (staff). Flag if:
- A test name is unclear
- A test lacks a comment explaining what behavior it pins down
- Magic constants without explanation

Don't be picky for stylistic preferences — flag genuinely unclear bits only.

### 10. Issue/resolved file format consistency

Verify Sprint 2's `issues/issue_sprint2_*.md` and `resolved_sprint2_*.md` follow the same format as Sprints 0 + 1.

## Issue file format

`/mnt/d/project/roksbnkctl/issues/issue_sprint2_tech-writer.md`:

```markdown
# Sprint 2 — tech writer issues

## Issue 1: short title
**Severity**: low | medium | high
**Status**: open
**Description**: what's wrong + where + how a reader would notice
**Files affected**: paths (with line numbers if useful)
**Proposed fix**: concrete recommendation
```

If genuinely clean, file with the heading and `*No issues filed.*`. Don't manufacture issues; clean reviews are valid.

## Verification before reporting done

- All 7 chapter files contain real prose (no "Coming in Sprint 2")
- All cross-references in the new chapters resolve to existing files
- All `roksbnkctl ...` commands in chapter 24 appear in the actual binary's help output

## Final report (under 200 words)

- Files reviewed (counts)
- Issues filed (counts by severity)
- Top 3 noteworthy observations not filed as issues
- Whether you spotted any drift between PRD 02 / PLAN.md and the actual delivered surface

Do NOT edit any files (except your issue file). Do NOT commit anything.
