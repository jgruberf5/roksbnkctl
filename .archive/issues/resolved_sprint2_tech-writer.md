# Sprint 2 — tech writer issues, resolution notes

10 issues filed by the tech-writer review. **All 10 fixed in this integration pass.** Tech-writer caught real PRD-vs-implementation drift (chapter 11's bogus `cluster down` guard claim), doctor-output drift between chapter 5 and `internal/doctor/doctor.go`, README highlight gap (analogous to Sprint 1 Issue 10), and several smaller xref/wording slips.

## Issue 1 (chapter 11 documents non-existent `cluster down` trial-state guard) — fixed via Path 1

The chapter claimed `cluster down` refused to run with non-empty trial state and quoted a specific error message. Implementation in `cluster_phase.go:319-344` has no such guard — it shows a stderr warning + interactive prompt without `--auto`, and silently proceeds with `--auto`.

Rewrote chapter 11 § "Order matters" to describe what's actually shipped (warning text, no hard guard, `--auto` skips the warning). Removed the quoted error message and the "`--auto` does not override this" claim. Added a note that the hard guard is tracked as a future improvement.

**Status**: ✅ resolved (doc-side fix per Path 1)
**Files**: `book/src/11-tearing-down.md`

## Issue 2 (chapter 5 doctor sample output mismatch on kubectl/oc rows) — fixed

Sample showed `(informational; roksbnkctl k get/apply/... covers the happy path natively)`. Actual `doctor.go:73-74` blurb is `(internalised in roksbnkctl k *; passthrough still works if installed)`. Changed sample to match the binary's output verbatim.

**Status**: ✅ resolved
**Files**: `book/src/05-doctor.md`

## Issue 3 (README missing Sprint 2 highlight bullet for k commands) — fixed

Added a new highlight bullet between the Sprint 1 `--on jumphost` bullet and the closing `---`:

> **Internalised kubectl verbs (v0.8)** — `roksbnkctl k get/apply/describe/delete/logs/exec/port-forward` run natively in-process via `client-go`; no host `kubectl` required for the everyday workflow. Top-level `roksbnkctl get` / `logs` for muscle-memory parity. Host `kubectl` / `oc` are now informational on `roksbnkctl doctor`. See [chapter 24](https://jgruberf5.github.io/roksbnkctl/book/24-day-2-ops.html).

(Skipped the cluster-ops table additions and the doctor-description softening — those are larger surgery and the highlight bullet covers the discoverability gap.)

**Status**: ✅ resolved
**Files**: `README.md`

## Issue 4 (PLAN.md still claims top-level `apply` alias was shipped) — fixed

Lines 230 and 245 updated to reflect actual aliasing (`get` and `logs` only; `apply` deliberately not aliased to avoid shadowing the lifecycle `roksbnkctl apply` / terraform apply).

**Status**: ✅ resolved
**Files**: `docs/PLAN.md`

## Issue 5 (chapter 24 OpenShift section more pessimistic than reality) — fixed

The section claimed Phase 2.1 was needed for `roksbnkctl k get projects/routes/imagestreams` to work. In fact the dynamic client + RESTMapper path in `internal/k8s/get.go` discovers OpenShift CRDs today; Phase 2.1 only adds typed-client output for nicer printing.

Rewrote the section to:
- State that `k get projects/routes/imagestreams` works **today** via dynamic-client discovery
- Reframe Phase 2.1 as "adds typed clients for prettier printing and `describe` integration" (deferrable to Sprint 5 polish per PLAN.md, accurately quoted)
- Update the "fall back to passthrough" guidance: only fall back if you specifically want typed-client output
- Same fix in the kubectl Rosetta-stone table at line 330

**Status**: ✅ resolved (also covers Issue 10 — the bad PLAN.md quote was inside this section)
**Files**: `book/src/24-day-2-ops.md`

## Issue 6 (chapter 24 exec cross-reference points at wrong chapter) — fixed

Changed `[Chapter 6](./06-workspaces.md)` to `[Chapter 16](./16-on-flag-ssh-jumphosts.md)` on the host-exec line. Chapter 16 is the canonical reference for host-side `exec` and `shell` behavior; chapter 6 covers workspaces only.

**Status**: ✅ resolved
**Files**: `book/src/24-day-2-ops.md`

## Issue 7 (chapter 6 + 11 mis-attribute `roksbnkctl down` to e2e Phase H) — fixed

The destroy commands run in Phase D (D8); Phase H is the parking-lot cleanup only.

- Chapter 6 line 176: changed `# Phase H of scripts/e2e-test.sh: tear-down + cleanup` to `# End-to-end test cleanup (e2e-test.sh: Phase D destroys; Phase H runs the parking-lot dance below)`
- Chapter 11 line 146: changed `from scripts/e2e-test.sh Phase H` to `from scripts/e2e-test.sh (Phase D destroys; Phase H parks and deletes)`

**Status**: ✅ resolved
**Files**: `book/src/06-workspaces.md`, `book/src/11-tearing-down.md`

## Issue 8 (doctor.go kubeconfig hint recommends host `ibmcloud` instead of native `roksbnkctl kubeconfig --download`) — fixed

The chapter's recommendation is the better one — `roksbnkctl kubeconfig --download` doesn't require `ibmcloud` on PATH. Updated `doctor.go:179` from:

```
$KUBECONFIG and ~/.kube/config both missing — fetch with `ibmcloud ks cluster config --admin`
```

to:

```
$KUBECONFIG and ~/.kube/config both missing — fetch with `roksbnkctl kubeconfig --download`
```

This brings the doctor's inline hint and chapter 5's recommendation into sync.

**Status**: ✅ resolved
**Files**: `internal/doctor/doctor.go`

## Issue 9 (chapter 5's chapter 26 forward-ref missing "lands in Sprint 6" annotation) — fixed

Other forward-refs to upcoming chapters use `(lands in Sprint N)` consistently. Chapter 5 line 123 was the outlier. Updated to match.

**Status**: ✅ resolved
**Files**: `book/src/05-doctor.md`

## Issue 10 (chapter 24 mis-quotes PLAN.md "Phase 2.1 which may slip") — fixed alongside Issue 5

The "may slip" quote isn't in PLAN.md anywhere. Issue 5's rewrite of the OpenShift section uses an accurate paraphrase: "PLAN.md flags this as deferrable to Sprint 5 polish".

**Status**: ✅ resolved (folded into Issue 5's rewrite)

## Verification post-fix

- `go build ./...` clean
- `go test ./...` clean
- `go vet ./...` clean
- `gofmt -d -l .` clean
- All 10 issues addressed (9 fixed in source, 1 — Issue 10 — folded into another fix)
