# Handoff Plan — preparing roksbnkctl for a new owning organization

**Status:** proposed, targeted for the release *after* the next one.
**This document is transitional.** Delete it when the work it describes is complete.

The goal: an engineer (or an AI agent) with no prior contact with this project
should be able to open the repo, understand what it does and why it is shaped
the way it is, and safely make a change — without asking anyone who worked on
it, and without encountering a single reference to how it got here.

---

## 1. What the measurements say

The starting position is better than the request assumes. This is not a
documentation-*writing* project. It is a **decontamination and reorientation**
project, with a smaller genuine-authoring component.

| Surface | Size | Historical contamination |
|---|---|---|
| Go (non-test) | 40,333 LOC · 9,599 comment lines · 331 files | see below |
| Go (test) | 25,071 LOC | sprint refs in test file headers |
| Terraform | 11,515 LOC · 1,728 comment lines · 72 files | heavy in `variables.tf`, `flo/main.tf` |
| CI workflows | 7 files | 6 sprint · 8 PRD refs · **1 provably stale claim** |
| Makefile | 526 lines | 25 sprint refs |
| CHANGELOG.md | 236 KB · 1,211 lines | 89 sprint · 47 PRD |
| `book/src` (**published publicly**) | 46 files | 59 sprint · 97 PRD |
| `docs/` + `docs/prd/` | 23 files · 5,836 lines | this is the *source* of the PRD refs |
| CONTRIBUTING.md | 554 lines | 18 sprint · 11 PRD |
| .gitignore | 54 lines | narrates its own history |

Raw marker counts across Go + Terraform: **406** `Sprint N`, **310** `PRD N`,
**214** `legacy`, **51** version references, 27 `used to`, 18 `no longer`.

**The number that sizes the job: 872 comment lines carry at least one
historical marker** — out of ~11,300 total comment lines. That is 7.7%. The
hard-strip set is bounded and tractable.

### What is already good — do not disturb it

- **95% of exported declarations in `internal/` carry doc comments** (499
  declarations, 24 undocumented). The godoc contract layer largely exists.
- **Every package but one has a package doc.** Only `cmd/roksbnkctl` lacks one.
- **All tests pass.** 25,071 lines of test against 40,333 of source.
- **Only 4 TODOs, 0 FIXMEs.** No abandoned-work debris.
- CI already enforces `vet`, `gofmt`, `staticcheck`, `-race`, plus three
  integration tiers (testcontainers, docker backend, kind).

The problem is not absence. It is **orientation**: the comments are written
outward from the decision that produced them ("Sprint 23: pass the ROOT
variable directly, not the cert_manager one") rather than inward toward a
reader who needs to know what is true now.

### One finding that shapes the method

`.github/workflows/ci.yml:60` describes carrying a `k8sinternal` build tag
"temporarily during staff+validator parallel work," and points at a removal
plan. **That tag does not exist anywhere in the codebase** — zero matches.

The comment is not merely dated; it is *false*. This is proof that comment
drift is already present, which means the sweep must **verify every claim it
rewrites against the code**, not just reword it. A pass that polishes prose
without checking truth will launder stale assertions into confident-looking
documentation — worse than what is there now.

---

## 2. Comment doctrine

331 Go files and 72 Terraform files cannot be made consistent by taste. The
doctrine is written first, applied to a pilot package, adjusted, then rolled
out. It becomes a permanent `CONTRIBUTING.md` section.

### The four tiers

**Tier 1 — Package doc (`doc.go`), one per package, 22 packages.**
What this package owns · its boundary (what it deliberately does *not* do) ·
who calls it · invariants a caller must respect · anything that will surprise
someone. Target 15–40 lines. This is the highest-leverage artifact in the
whole effort — it is what an agent reads first.

**Tier 2 — File header.** Required for files >200 lines or where the grouping
is not self-evident from the filename. Answers: what is in here, and why do
these things belong together? Not required for small, obviously-named files.

**Tier 3 — Exported declaration doc.** Standard godoc contract: inputs,
outputs, error conditions, side effects, concurrency expectations, and any
resource the caller must release. Mostly already present; needs auditing for
truth and for history-free phrasing.

**Tier 4 — Inline.** Permitted *only* for non-obvious invariants and external
constraints — an IBM Cloud API quirk, a Helm OCI behavior, an OpenShift
admission rule, a Windows path constraint. Never to narrate a change.

### Strip rules (mechanical, enforceable)

Delete or rewrite any comment containing:

- `Sprint N`, `PRD N`, `Issue N`, `§"..."` cross-references into planning docs
- Version-relative claims — "as of v1.27", "since v1.2", "the v1.1.x cycle"
- Change narration — "used to", "previously", "no longer", "retired",
  "replaced the", "carried temporarily", "round-2", "historically"
- Team and process references — "staff+validator parallel work", "the
  integrator", "the validator"
- References to documents that will not ship (`docs/PLAN.md`, `docs/prd/*`)

### The rewrite rule — this is where the value is protected

**Every stripped comment that encoded a real constraint must be re-expressed
in the present tense as a constraint.** A naive strip destroys hard-won
operational knowledge; that is the single biggest risk in this plan.

Worked example, from `terraform/main.tf:100`:

> **Before:** `# Sprint 23 round-2: pass the ROOT variable directly, not the cert_manager one`
>
> **After:** `# Takes the root variable rather than the module-scoped copy: the module-scoped value is not populated until cert-manager has applied, which is too late for this reference.`

Same information, no archaeology, and — critically — the *reason* survives so
the next person does not "simplify" it back into a bug.

Where the original comment's rationale cannot be recovered from the code, that
is a finding to escalate, not a line to quietly delete. Track these; they are
the places where institutional knowledge is genuinely at risk.

### Anti-goal

Do not inflate. A 23% comment ratio in Go is healthy. The target after this
work is roughly the same ratio with different content — not 40%.

---

## 3. Disposition decisions (need sign-off before work starts)

These are the judgment calls that block the sweep. **310 code comments point
into `docs/prd/`**, so the sweep cannot start until the destination of that
directory is decided.

### 3.1 `docs/prd/` — 18 numbered PRDs, ~5,800 lines

Recommendation: **extract, then delete.**

Roughly six PRDs still describe live architecture (registry mirror, execution
backends, credentials, declarative CLI, remote state, cluster/trial phase
split). Their durable content should be lifted into `ARCHITECTURE.md` and the
relevant package `doc.go` files, expressed as *how the system works* rather
than *what we decided to build*. The rest is superseded process. Once
extracted, delete the directory and strip all 310 references.

Alternative if that is too aggressive: rename to `docs/design/` with
topic-based filenames (no numbers) and rewrite references to point at topics.
Costs more and preserves a document class the new org has not signed up to
maintain.

### 3.2 `CHANGELOG.md` — 236 KB

Recommendation: retain entries from v1.0 forward with sprint/PRD references
stripped; collapse everything pre-1.0 to a single line. Archive the full text
on a tag or in the release notes, not in the shipping tree.

### 3.3 `docs/PLAN.md`, `docs/PRD.md`, `docs/SHAKEOUT.md`, `docs/E2E_TEST.md`

`E2E_TEST.md` likely has ongoing value — verify against `book/src/23-e2e-test-plan.md`
and keep exactly one of the two. The other three are internal process
artifacts; delete.

### 3.4 The published book — 97 PRD and 59 sprint references

`book/src` is deployed to GitHub Pages. Internal PRD numbering is currently
leaking into public documentation. This should be fixed regardless of the
handoff.

---

## 4. Stability and maintainability findings

Independent of the documentation work, the sweep surfaced these. Ranked by
impact on a new owner.

### 4.1 Test coverage is inverted against risk — highest priority

| Package | Coverage | Size |
|---|---|---|
| `internal/orchestration` | **17.3%** | 13 files, incl. `lifecycle.go` at 1,567 lines |
| `internal/ibm` | **22.6%** | 15 files |
| `internal/cli` | **27.7%** | 65 files |
| `internal/cred` | 30.8% | credential handling |
| `internal/doctor` | 34.7% | |
| `internal/k8s` | 40.3% | 16 files |
| — | — | — |
| `internal/flp` | 100% | 1 file |
| `internal/naming` | 96.8% | 1 file |
| `internal/bnkbom` | 88.7% | 1 file |

The small, self-contained packages are well covered. The large, consequential
ones that actually orchestrate cloud resources are not. A new team will make
its first mistakes in `orchestration` and `cli`, and the suite will not catch
them.

**Recommendation:** raise `orchestration`, `cli`, and `ibm` to a 50% floor
before handoff, and add a coverage gate to CI so it cannot regress. This is
the single most valuable thing on this list.

### 4.2 No lint configuration

CI runs `vet` + `gofmt` + `staticcheck`, but there is no `.golangci.yml`. A
pinned `golangci-lint` with an agreed ruleset is how a new team maintains
quality without access to the original reviewers. Add it, pin the version,
wire it into CI and the existing `make lint` target.

### 4.3 God files — the onboarding cliff

| File | Lines |
|---|---|
| `terraform/modules/flo/modules/flo/main.tf` | 1,881 |
| `internal/orchestration/lifecycle.go` | 1,567 |
| `internal/cli/init.go` | 1,305 (incl. a 258-line `runInit`) |
| `internal/cli/test.go` | 923 |
| `internal/cli/registry.go` | 903 |
| `internal/config/workspace.go` | 860 |

Longest functions: `runInit` 258 lines, `renderBNKFields` 206,
`DockerBackend.Run` 202, `tf.Open` 194.

Decomposition is not required for the handoff, but these files are where
documentation alone will fail to make the code approachable. At minimum they
need strong Tier-2 headers with a navigational map. Splitting `lifecycle.go`
and `init.go` would pay for itself.

### 4.4 Terraform module double-nesting

Five modules use a `modules/<name>/modules/<name>/` wrapper-around-inner
pattern (`cert_manager`, `cne_instance`, `flo`, `license`, `roks_cluster`).
The variable declarations are duplicated across both levels — `flo/variables.tf`
is 263 lines and `flo/modules/flo/variables.tf` is 281. Every new variable
must be declared twice and threaded through.

Either flatten it, or document the reason prominently. Right now a newcomer
will assume it is an accident and "fix" it.

### 4.5 Terraform is embedded in the binary

`embedded.go` embeds `./terraform` into the compiled binary via `go:embed`.
This is deliberate and well-reasoned, but it is genuinely surprising: editing
`terraform/*.tf` has no effect until rebuild. This needs to be called out
loudly in `ARCHITECTURE.md`, not left in a root-level file comment.

### 4.6 Dependency surface

138 direct dependencies, Go 1.26. A new team needs a dependency policy and
automated update pressure — enable Dependabot or Renovate, and document the
upgrade cadence for the `k8s.io/*` group specifically, since those constrain
the minimum Go version.

### 4.7 Naming archaeology

`internal/orchestration/second_phase_reuse.go` (+ its test) means something
only to the original team. Rename to describe what it does.

### 4.8 Missing top-level artifacts for a public handoff

No `ARCHITECTURE.md`, `SECURITY.md`, `SUPPORT.md`, or `CODEOWNERS`.
`CONTRIBUTING.md` (554 lines) is written for the current team and references
sprints and PRDs throughout — it needs a rewrite for an external audience.
`cmd/roksbnkctl` has no package doc and no test.

### 4.9 Credential handling deserves an explicit document

The presence of a `test-cred-audit` make target says this was taken seriously.
The invariant being protected should be written down as a stated security
property, not left implicit in a test target — both for the new owners and for
`SECURITY.md`.

---

## 5. Phased plan

### Phase 0 — Foundations *(can start before the next release; small)*

1. Decide the §3 dispositions. **Blocking** — the sweep cannot start without
   the `docs/prd/` decision.
2. Write the comment doctrine (§2) into `CONTRIBUTING.md`.
3. Build `scripts/check-comment-hygiene.sh` — greps for the §2 strip-rule
   patterns, exits non-zero with file:line. This is both the progress meter
   and the permanent regression gate.
4. Baseline it: expect ~872 hits. Record the number.
5. Add `.golangci.yml` (§4.2) now, so the sweep does not fight a moving target.

**Exit:** doctrine agreed, linter runs, baseline recorded.

### Phase 1 — Pilot *(one small package, validates the doctrine)*

Run the full treatment on `internal/registry/{mirror,ocireg,source}` — three
packages, self-contained, already 54–87% covered, and touching the mirror
surface a new owner will need early.

Produce a before/after diff and review it as a team. **Adjust the doctrine
based on what the pilot reveals**, then freeze it.

**Exit:** doctrine frozen; a worked example exists for everyone else to copy.

### Phase 2 — Go sweep *(the bulk)*

Package by package, 22 packages. For each: `doc.go` rewrite (Tier 1) → file
headers (Tier 2) → exported-doc audit for *truth* (Tier 3) → inline strip and
rewrite (Tier 4) → hygiene linter clean → tests still pass.

Suggested order — dependencies first, so later packages can reference
established vocabulary: `naming`, `cred`, `config`, `bnkbom`, `registry/*`,
`cos`, `ibm`, `k8s`, `exec`, `remote`, `tf`, `flp`, `forge`, `sshkey`,
`doctor`, `test`, `orchestration`, `cli`, `ui`, `embedded`, `cmd`.

Do `orchestration` and `cli` **last** — they are the largest and will benefit
most from the vocabulary settled elsewhere.

Track escalations: every comment whose rationale could not be recovered.

### Phase 3 — Terraform sweep

72 files. Same tiers, adapted: a `README.md` per module directory (what this
module provisions, its inputs/outputs, its dependencies on other modules),
then variable-description and inline cleanup. `variables.tf` descriptions are
user-facing — they appear in the book's variable reference — so they get the
same truth audit.

Resolve or document the double-nesting (§4.4) while in here.

### Phase 4 — Non-code surfaces

CI workflows (delete the false `k8sinternal` narration and audit every other
claim the same way), Makefile, `.gitignore`, CHANGELOG truncation, `docs/`
disposition, `book/src` PRD-reference removal, `CONTRIBUTING.md` rewrite.

New artifacts: `ARCHITECTURE.md` (the phase model — cluster → flp → bnk →
gateway → testing, each with its own Terraform state — plus the embedded-HCL
surprise and the registry/credential flows), `SECURITY.md`, `SUPPORT.md`,
`CODEOWNERS`, `cmd/roksbnkctl` package doc.

### Phase 5 — Stability work

Coverage floors on `orchestration` / `cli` / `ibm` with a CI gate; god-file
decomposition if it fits the budget; `second_phase_reuse.go` rename;
Dependabot/Renovate.

This phase is separable — it can be descoped without invalidating Phases 1–4,
and it is the natural place to trim if time runs short.

### Phase 6 — Cold-read acceptance

The only test that actually proves the goal.

Give an engineer with no exposure to this codebase — and separately, an AI
agent with only the repo — a real task. Suggested: *"add a new registry target
backend alongside `icr` and `generic`."* It touches config, the CLI, the
registry packages, Terraform variables, and the book.

Measure: can they do it using only the repo? Every question they have to ask a
current team member is a documentation defect. Fix those, repeat once.

**Exit criteria for the whole effort:**
- `check-comment-hygiene.sh` returns zero hits, and runs in CI
- No reference to sprints, PRDs, or superseded versions anywhere in the tree
- Every package has a `doc.go` that states ownership and boundary
- `ARCHITECTURE.md` explains the phase model and the embedded-Terraform coupling
- The cold-read task completes without external help
- All tests pass; coverage gate holds

---

## 6. Sizing

Ranges, not estimates — the pilot exists to replace these with a measured rate.

| Phase | Rough size | Notes |
|---|---|---|
| 0 — Foundations | 2–3 engineer-days | Mostly the disposition decisions |
| 1 — Pilot | 2–3 days | Includes doctrine revision |
| 2 — Go sweep | 12–20 days | 22 packages; `cli` and `orchestration` are ~⅓ of it |
| 3 — Terraform sweep | 4–6 days | 72 files, 12 modules |
| 4 — Non-code | 4–6 days | `ARCHITECTURE.md` is the long pole |
| 5 — Stability | 8–15 days | Highly variable; coverage work dominates |
| 6 — Cold read | 2–3 days | Plus fix time for whatever it finds |

**Total: roughly 34–56 engineer-days**, of which Phase 5 is the compressible
part. Phases 0–4 and 6 — the actual handoff readiness — are ~26–41 days.

This is well suited to AI-assisted execution: the strip rules are mechanical,
the hygiene linter gives an objective progress signal, and the per-package
structure parallelizes cleanly. The judgment-heavy parts that need human
attention are the §3 dispositions, the pilot review, the Tier-1 package docs,
`ARCHITECTURE.md`, and every escalated comment whose rationale could not be
recovered from the code.

---

## 7. Risks

| Risk | Mitigation |
|---|---|
| **Naive stripping destroys real operational knowledge** — the top risk | The §2 rewrite rule; escalate every unrecoverable rationale rather than deleting it |
| **Laundering stale claims into confident docs** — the `k8sinternal` case proves drift exists | Verify every claim against code; the sweep is an audit, not a copy-edit |
| Sweep collides with active feature work | Run it after the release, package by package, merged continuously — not as one giant branch |
| Cosmetic-only outcome | Phase 6 cold-read is the real acceptance test; do not skip it |
| Doctrine drift across 331 files | Freeze after the pilot; enforce mechanically via the hygiene linter |
| `docs/prd/` deletion loses design rationale | Extract to `ARCHITECTURE.md` and `doc.go` *before* deleting; verify in review |
