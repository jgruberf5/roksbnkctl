# Sprint 27 — tech-writer issues (post-integration drift sweep)

> **Sprint 27 frame (re-pivoted 2026-06-04).** Light drift sweep AFTER the
> three-way integration lands the **terraform-native** BNK phase
> (`helm_release` installs + `alekc/kubectl` `kubectl_manifest` + `wait_for`
> for the CRs — no Go reconciler, no custom provider). Verifies the integrated
> tree's operator-facing surface (BNK-phase chapter, command/config reference,
> CHANGELOG) matches the built binary + the new terraform behavior, and that
> any speed claim traces to the validator's measured benchmark. Ends on a
> GREEN/RED verdict.

`Status`: open (re-pivoted)

---

## Issue 1 — Drift sweep over the integrated terraform-native BNK tree

**Severity**: low
**Status**: open

Build the integrated binary, then sweep:

1. **Command / config reference**: the install-mode surface — whatever staff
   exposed (`--legacy-bnk` flag and/or a `bnk_cr_mode` config/tfvar) — matches
   the binary; any optional `bnk status` matches if it landed. If `bnk up`/`down`
   help text changed, reflect it.
2. **BNK-phase chapter** (architect-authored): reconcile prose vs reality — the
   phase is now `helm_release` installs + `kubectl_manifest` + `wait_for` (real
   `.status` gating: CNEInstance `Available`, License `state`), the CRs are real
   terraform state (`plan`/`destroy`/drift), and the install-mode flag keeps the
   legacy curl path for the transition. Re-capture any illustrative transcript
   (`terraform apply` / `bnk up` output) byte-for-byte; drop the illustrative
   note.
3. **Speed claim**: any "faster"/timing numbers must match the validator's
   measured **kubectl-vs-legacy** benchmark — do NOT ship an unverified "Nx
   faster"; quote the measured delta or phrase qualitatively if no number is
   pinned.
4. **Stale sweep**: update prose that calls the BNK phase `null_resource`/`curl`/
   `time_sleep`-driven; confirm the "IBM IAM trusted-profile + COS reads stay in
   terraform" note is present and the new `alekc/kubectl` provider dependency
   (+ air-gap mirror caveat) is documented.
5. **CHANGELOG**: user-facing — leads with the faster, in-state, watch-gated BNK
   phase (real `plan`/`destroy` for the CRs); notes the legacy curl path remains
   behind the install-mode flag this release; notes the new provider dependency;
   notes (per integrator) it's on a feature branch / not yet default if
   applicable.

### Acceptance criteria
1. Install-mode surface (+ any `bnk status`) in the reference matches the binary.
2. BNK-phase chapter matches the terraform-native reality; transcript captured.
3. Speed numbers trace to the validator's measured kubectl-vs-legacy benchmark.
4. No stale "`null_resource`/`curl`/`time_sleep`-driven BNK" prose survives; the
   new provider dependency + air-gap caveat documented.
5. CHANGELOG user-facing + notes the install-mode flag. mdbook builds clean.
   GREEN/RED verdict recorded.

### Files affected (probable)
- `book/src/**` (BNK-phase chapter, command/config reference), `CHANGELOG.md`.
- No `internal/`, `cmd/`, `terraform/` code edits (reflected-doc reconciliation).

### Related
- `issues/issue_sprint27_architect.md` (incl. Spike rounds 1 & 2) + `..._staff.md`
  closures — the prose + terraform behavior to reconcile.
- `issues/issue_sprint27_validator.md` — the measured benchmark + License-literal
  confirmation any claim must trace to.
- Per-finding fields use `**Verdict**:` (not `**Status**:`) per the `a2b78da`
  convention.

---

## Closure — tech-writer, 2026-06-04

Post-integration drift sweep over the terraform-native BNK landing on branch
`sprint27-bnk-native-k8s` (not merged, not tagged, no commit). Built the binary
(`go build -o /tmp/roksbnkctl-s27 ./cmd/roksbnkctl` → OK) and reconciled the
operator-facing docs against it + the landed terraform + the architect/staff/
validator closures. Doc edits only (`book/src/**` + `CHANGELOG.md` + this
closure); no `internal/`/`cmd/`/`terraform/` code touched.

### Finding 1 — install-mode surface vs the binary

**Verdict**: GREEN (drift fixed).
`bnk up --help` / `bnk down --help` confirm a **`--legacy-bnk`** bool flag on
BOTH subcommands (help text byte-captured into the docs). No `bnk status`
landed (staff deferred it — live CR status is queryable via terraform state /
`k get`; the chapter does not claim one exists). The command reference
(ch27) documented the `bnk up`/`bnk down` flag tables but was **missing
`--legacy-bnk`** — added to both, with the binary's exact help strings. Added
the `bnk.cr_mode` config key to the ch28 `bnk:` block and the `bnk_cr_mode`
tfvar (default `"kubectl"`, validated) to the ch29 root-variable table, both
traced to `internal/config/workspace.go` (`yaml:"cr_mode,omitempty"`),
`internal/tf/vars.go` (`bnk_cr_mode = %q`), and `terraform/variables.tf:217`.

### Finding 2 — BNK-phase chapter (ch10) vs the terraform-native reality

**Verdict**: GREEN. The architect-authored chapter already matched the landed
resource shapes (`helm_release` / `kubernetes_*_v1` / `kubectl_manifest` +
`wait_for`), the CRs-as-state model, the `bnk_cr_mode` flag, the `wait_for`
HCL (`server_side_apply`, `field_manager="roksbnkctl"`, CNEInstance
`condition Available=True`, License `field status.state="Verification
Complete"`), the IBM-IAM+COS-stay-in-terraform note, and the `alekc/kubectl`
+ air-gap caveat. Reconciliation edits: tied the `--legacy-bnk` flag into the
install-mode section (mode-match-at-down note + cross-links to ch27/ch28);
corrected the License-literal sentence (it is **pending** the integrator's
live confirm, was worded as already-confirmed-by-validator); and re-scoped the
illustrative note (see Finding 3). Resource counts/timings left as illustrative
and labelled so.

### Finding 3 — speed claim (NO measured number yet)

**Verdict**: GREEN (qualitative + placeholder, per the re-pivot). The gated-live
kubectl-vs-legacy benchmark has NOT been run (no cluster on this host). Grep
confirmed the chapter ships **no** "N× faster" anywhere. Softened the one line
that implied a measured delta existed ("the validator measures … this
paragraph's numbers trace to that benchmark") to an explicit
**pending-the-live-benchmark** placeholder, and re-scoped the chapter's closing
illustrative note to say the transcripts/timings could not be re-captured here
(cluster-mutating) and are filled by the integrator from
`scripts/e2e-bnk-native.sh`. Both spots carry an `<!-- INTEGRATOR: … -->`
marker. CHANGELOG phrases the speedup purely qualitatively (≈210s of fixed
sleep removed + real-readiness gating; "no N× faster number this release").

### Finding 4 — stale `null_resource`/`curl`/`time_sleep` sweep

**Verdict**: GREEN (drift fixed). Grepped all of `book/`. `time_sleep` and
`alekc` and `--legacy-bnk` appeared only in ch10 (correct, historical/legacy-
mode context). Stale BNK-is-shell-driven prose found and fixed in:
- **ch04 (installation)** — 4 sites claiming host `helm` is required *because*
  the modules shell out to `helm upgrade --install`. Re-scoped: that is now the
  **legacy `--legacy-bnk` path only**; the default path installs via the
  `helm_release` provider (in-process, no host-`helm` shell-out). Kept "helm
  flagged required" to stay **consistent with the binary** — `internal/doctor/
  doctor.go:106` still `checkBinary("helm", true, …)` (staff did not relax the
  doctor check, and I cannot edit `internal/`); the legacy path genuinely still
  needs it.
- **ch05 (doctor)** — same re-scope on the `helm` check prose + replaced the
  stale "a v1.x refactor onto `helm_release` *would* eliminate the requirement"
  note with "the default path now uses `helm_release`; doctor still flags helm
  required for the legacy path".
- **ch11 (tearing down)** — `bnk down` description said it removes
  "null_resources that bootstrap admin tokens"; rewritten to the terraform-
  native teardown (`helm_release`/`kubernetes_*`/`kubectl_manifest` destroy,
  finalizer-aware, no destroy-time `curl`; legacy parenthetical kept).
IBM-IAM+COS-stay-in-terraform note: **present** (ch10 §"What stays in
Terraform"). `alekc/kubectl` provider dependency + air-gap mirror caveat:
**present** (ch10 §"The one new dependency" + CHANGELOG Added).

### Finding 5 — CHANGELOG

**Verdict**: GREEN. Added an **"Unreleased — feature branch
`sprint27-bnk-native-k8s`"** section above v1.8.0 (this is unmerged/untagged, so
not a versioned release). User-facing: terraform-native BNK (`helm_release` +
`kubectl_manifest` + `wait_for`); CRs as real state (`plan`/`destroy`/drift,
no destroy-time `curl`); ≈210s of fixed sleeps removed (qualitative, **no
measured number**); legacy curl behind `bnk_cr_mode`/`--legacy-bnk`; new
`alekc/kubectl` provider dependency + air-gap mirror note; doctor-still-needs-
helm note; and an explicit **feature-branch / not-yet-merged, integrator-gated**
note.

### mdbook

**Verdict**: GREEN. `make book BOOK_BACKEND=docker` builds clean (HTML + pandoc
PDF) — only the pre-existing benign `mdbook-mermaid` version-skew warning, no
link/anchor errors. New cross-reference anchors hand-verified against the
mdbook slug rule (`#bnk-block`, `#the-terraform-native-deployment-model`,
`#the-install-mode-flag-bnk_cr_mode`) and confirmed by the clean build.

### Residual / integrator gates (NOT doc issues)

1. The measured kubectl-vs-legacy wall-clock + delta — fill the two
   `<!-- INTEGRATOR -->` markers in ch10 from the `e2e-bnk-native.sh` run.
2. The live License `status.state` literal confirm (`"Verification Complete"`
   vs the real value) — ch10 + the `wait_for` are written to that literal with
   the `conditions[]` fallback called out.
3. Re-capture the ch10 transcripts byte-for-byte once a cluster is available
   (currently labelled illustrative).

### Files changed
- `CHANGELOG.md`
- `book/src/04-installation.md`, `book/src/05-doctor.md`,
  `book/src/10-deploying-bnk-trials.md`, `book/src/11-tearing-down.md`,
  `book/src/27-command-reference.md`, `book/src/28-configuration-reference.md`,
  `book/src/29-terraform-variable-reference.md`
- `issues/issue_sprint27_tech-writer.md` (this closure)

### Verdict — **GREEN** (doc-complete)

The operator-facing surface matches the built binary + the landed terraform-
native BNK phase; stale shell-driven prose is gone; the speed claim is
qualitative with integrator placeholders (no unverified number shipped); mdbook
builds clean. The integrator still gates **merge** on the gated-live
correctness + speed verify + the live License-literal confirm (all three
flagged above and marked in-doc). Not committed, not tagged, branch unmerged
per constraint.
