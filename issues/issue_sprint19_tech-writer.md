# Sprint 19 — tech-writer issues (`init --var-file` post-integration drift sweep)

> **Sprint 19 frame.** First regular work sprint post-`v1.6.3`.
> Tech-writer runs **after** the three-way integration of staff
> (Issue 1: `init --var-file`) + architect (Issue 1: init chapter
> + 27 regen + cross-chapter sweep) + validator (Issue 1:
> hermetic + live tests). Drift sweep + GREEN/RED launch verdict.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — Post-integration drift / consistency review for `init --var-file`

**Severity**: low
**Status**: resolved

### Motivation

The Sprint 18 cycle taught us that integration-time drift is real even with careful role partitioning: the auto-generated CLI reference was stale; the COS chapter didn't pin the new verb; sibling pipeline-comment prose contradicted the as-shipped code. Sprint 19's surface is smaller (one flag on one command) but the same drift classes apply. The tech-writer's job is to catch them before the v1.6.4 cut.

### Drift surface to walk

For the **`init --var-file` flow**:

- **`roksbnkctl init --help`** output text — the new flag's description matches the style of sibling flags (length, voice, references). Capture the actual `--help` output (run `go run ./cmd/roksbnkctl init --help`) and pin findings against it, not against the staff's prompt language.
- **`book/src/27-command-reference.md`** — the regenerated reference includes the new flag with the right shape. Compare against `init --help` output.
- **The init book chapter** (architect identified the path) — has the new §"Skip the interview: `init --var-file`" subsection with all five components (when, flow, what's persisted, why-it-matters, secrets-on-disk, diagnostics).
- **Cross-chapter sweep verification** — every "supply `--var-file` on every command" instance the architect's task A.3 was supposed to touch is actually amended; no stale advice survives.
- **CHANGELOG / PLAN docstring sweep** — no in-code reference to "see CHANGELOG vX.Y.Z" with a version that doesn't match the integrator's target (likely v1.6.4).
- **Secrets-on-disk consistency** — the book's note about `0600` + the workspace-state path matches what the staff implementation actually does (the validator's hermetic test (a) is the source of truth for mode; the chapter must match).

### Acceptance criteria

1. Every finding in this issue's Closure section names a specific file path + line number.
2. Findings tagged by severity (low / medium / high); each high finding blocks the release.
3. A final GREEN / RED launch verdict ends the closure.
4. The findings cite the actual `init --help` output (captured during the review), not the spec language.

### Out of scope

- Restyling chapters; rewriting flow descriptions; adding new chapters. Drift sweep only.
- Touching any non-`issues/` file. Read-only on existing repo content.

### Optional Part B (≤2 issues)

If the integrated work surfaces a cross-cutting docs gap the other roles didn't close, file it as Issue 2 (or 2+3) here. Strict cap.

### Files affected

- `issues/issue_sprint19_tech-writer.md` (this file's Closure section). Read-only on the integrated tree.

### Related

- Staff Issue 1, architect Issue 1, validator Issue 1 — all reviewed for drift; no edits suggested to those ledgers.
- Sprint 18 tech-writer Issue 1 — the precedent shape for this work (3 findings caught → addressed in integration commit → GREEN).

---

### Closure — tech-writer, 2026-05-21

**Scope walked**:

- `roksbnkctl init --help` captured from `/tmp/roksbnkctl-fixed init --help` (the as-shipped binary post-round-2 fixes).
- `book/src/06-workspaces.md` §"Skip the interview: `init --var-file`" (lines 139-178) — all required components.
- `book/src/27-command-reference.md` line 529 — regenerated init reference.
- Cross-chapter sweep: `book/src/10-deploying-bnk-trials.md` (line 155-158), `book/src/12-workspace-config.md` (line 275), `book/src/13-terraform-variables.md` (line 161).
- `CHANGELOG.md` §"v1.6.4 — 2026-05-21" (lines 7-13) + `docs/PLAN.md` §"Sprint 19" closure (lines 1125-1151).
- In-code reference sweep across `internal/` and `cmd/` for `CHANGELOG vX.Y.Z` / `state/terraform.tfvars.user` / `state-cluster/terraform.tfvars.user` stale tokens.
- Secrets-on-disk consistency: book's `0600` claim vs the staff impl (`internal/cli/init_var_file.go:239`) vs the validator's hermetic test (`internal/cli/init_var_file_test.go:193`).

**Captured `init --help` excerpt** (verbatim, from `/tmp/roksbnkctl-fixed init --help`; the new flag line is what subsequent findings pin against):

```
Flags:
  -h, --help               help for init
      --tf-source string   override TF source (path or URL); relative local paths are resolved to absolute before being pinned into config.yaml
      --upgrade-tf         resolve and pin the latest TF release into config.yaml
      --var-file string    path to a tfvars file (shaped like terraform.tfvars.example); seeds config.yaml and is copied verbatim to the workspace root as terraform.tfvars.user (sibling to config.yaml; serves both phases)
```

The `--var-file` description matches `book/src/27-command-reference.md:529` byte-for-byte. The round-2 correction ("copied verbatim to the workspace root as terraform.tfvars.user (sibling to config.yaml; serves both phases)") landed in both the cobra flag binding and the regenerated reference.

#### Findings

##### Finding 1 — book §"Skip the interview" component coverage (informational pass)

**Severity**: low (informational — no defect; pinning the pass).

**File:line**: `book/src/06-workspaces.md:139-178`.

**Observation**: All five required components are present and ordered for readability:

- When to use: `06-workspaces.md:141` ("If you already have a `terraform.tfvars` in hand …").
- Flow / one-command invocation: `06-workspaces.md:143-145`.
- What gets persisted: `06-workspaces.md:147-150` (config.yaml seed list + workspace-root `terraform.tfvars.user` at mode `0600`).
- Why it matters: `06-workspaces.md:152-165` (subsequent bare-`-w <ws>` commands; precedence-chain note).
- Secrets-on-disk note: `06-workspaces.md:167-169` (`0600`, cross-link to Chapter 13 §"The `IBMCLOUD_API_KEY` exception", cred-resolver opt-out path).
- Diagnostics paragraph: `06-workspaces.md:171-178` (two-state walkthrough + the `ls -l ~/.roksbnkctl/<ws>/terraform.tfvars.user` verification one-liner, with a clean note about stale in-state-dir copies from early-build runs being harmless).

**Recommendation**: none — this is a pass-pin. Document this finding as the section's known-good shape for future drift checks.

##### Finding 2 — chapter-27 reference matches `init --help` byte-for-byte

**Severity**: low (informational).

**File:line**: `book/src/27-command-reference.md:529`.

**Observation**: The chapter-27 row reads

```
| `--var-file` | `string` | — | path to a tfvars file (shaped like terraform.tfvars.example); seeds config.yaml and is copied verbatim to the workspace root as terraform.tfvars.user (sibling to config.yaml; serves both phases) |
```

— byte-identical to the cobra-emitted `init --help` description captured above. The Sprint 18 tech-writer-caught regen-drift class is closed up front this sprint.

**Recommendation**: none.

##### Finding 3 — cross-chapter sweep landed; no stale state-dir paths survive in the book

**Severity**: low (informational).

**File:line**: `book/src/10-deploying-bnk-trials.md:155-158`, `book/src/12-workspace-config.md:275`, `book/src/13-terraform-variables.md:161`.

**Observation**: All three chapters now (a) point at the new `init --var-file` flow as the recommended persistence path, (b) name the **workspace-root** `terraform.tfvars.user` path (not the round-1 `state/` / `state-cluster/` paths), and (c) cross-link to Chapter 6 §"Skip the interview: `init --var-file`". Repo-wide grep for `state/terraform.tfvars.user` or `state-cluster/terraform.tfvars.user` returns **zero hits** in `book/src/` — only hits are (i) the docker container-path bind-mount strings in `internal/orchestration/lifecycle.go:897,963` (those are container paths, not host paths — `/state/` is the container's mount of the workspace root, so `/state/terraform.tfvars.user` resolves correctly to the host workspace-root file via the bind mount at `lifecycle.go:961-965` — not drift), (ii) `.archive/` historical ledgers, (iii) the staff/architect/validator ledger narratives (in `issues/`, scoped out of book-user-facing surface), and (iv) `prompts/sprint19/validator.md` / `docs/PLAN.md` motivation prose which describe what the round-1 plan intended (historical context, not as-shipped behaviour).

**Recommendation**: none.

##### Finding 4 — CHANGELOG `v1.6.4` is coherent with as-shipped behaviour

**Severity**: low (informational).

**File:line**: `CHANGELOG.md:7-13` + `docs/PLAN.md:1125-1151`.

**Observation**: The CHANGELOG entry names the workspace-root single-copy persistence path (`CHANGELOG.md:13`: "persist the file at the workspace root as `terraform.tfvars.user` (mode `0600`, sibling to `config.yaml`)"), the both-phases coverage via `tf.Workspace.UserTFVarsPath()`, the six config-seed fields (matches the staff ledger's mapping table), the `note: replacing existing …` re-init behaviour (matches `internal/cli/init.go:258`), the Sprint 16 actionable-error gate's broadened remedy text, and the live-`!` log line `→ Layering user tfvars from <path>` (matches `internal/orchestration/lifecycle.go:468` + `internal/orchestration/second_phase_reuse.go:107`). `docs/PLAN.md:1144-1149` records the round-2 fix narrative consistent with the staff ledger's "Closure round 2". No in-code reference to `see CHANGELOG vX.Y.Z` with a mismatched version exists in `internal/` or `cmd/` (grep returns zero hits) — nothing to drift.

**Recommendation**: none.

##### Finding 5 — secrets-on-disk `0600` claim matches the source-of-truth tests

**Severity**: low (informational).

**File:line**: `book/src/06-workspaces.md:150,167-169` ↔ `internal/cli/init_var_file.go:239` ↔ `internal/cli/init_var_file_test.go:187-194` ↔ `internal/cli/init_var_file_helpers_test.go:178-…`.

**Observation**: The chapter's "mode `0600`" claim in §"Skip the interview" (`06-workspaces.md:150` for the destination, `:169` for the cross-link to the cred-resolver opt-out) is supported by the staff implementation's explicit `os.Chmod(tmpPath, 0o600)` (`internal/cli/init_var_file.go:239`) and the validator's AC1 hermetic assertion (`internal/cli/init_var_file_test.go:193-195` — `mode != 0o600` is the failure path). The mode the book documents and the mode the binary writes are the same number.

**Recommendation**: none.

##### Finding 6 — stale docstring in `internal/cli/init_var_file.go` (post-round-2)

**Severity**: low.

**File:line**: `internal/cli/init_var_file.go:12` (file-level package comment).

**Observation**: The file-level package comment, item 3, still reads `"copying the file verbatim, mode 0600, to both phase state dirs under the conventional terraform.tfvars.user name — the path the existing tfws.HasUserTFVars() codepath in internal/tf auto-layers on every subsequent lifecycle op"`. This is the round-1 description — the as-shipped function (`writeUserTFVarsCopies` at `init_var_file.go:174-188`) writes one copy at the workspace root, not "both phase state dirs". The function's own docstring (lines 174-188) correctly describes the round-2 single-copy shape; only the file-header summary is stale. No user-visible impact (the comment is internal-only), but it will mislead the next maintainer reading the file top-to-bottom.

**Recommendation**: integrator amends the file-header item 3 to read something like `"copying the file verbatim, mode 0600, to the workspace root as terraform.tfvars.user — the single path the existing tfws.HasUserTFVars() / UserTFVarsPath() codepath in internal/tf auto-layers for both the trial and cluster phases on every subsequent lifecycle op"`. Two-line drive-by edit; defer post-cut is acceptable since it's docstring-only.

##### Finding 7 — architect ledger Closure section narrative is round-1-stale (does not block release)

**Severity**: low.

**File:line**: `issues/issue_sprint19_architect.md:71` and `:78`.

**Observation**: The architect's Closure section (which predates the round-2 cycle the integrator drove for the staff side) describes the chapter's round-1 "two-copy persistence (with explanation of why two and not one)" shape and quotes the chapter-27 round-1 wording for the regen-verification paragraph. The actual book and chapter-27 as-shipped reflect the round-2 single-workspace-root shape (see Findings 1, 2, 3 above). The architect ledger does NOT carry a "Closure round 2" subsection in the way the staff ledger (`issues/issue_sprint19_staff.md:156-180`) and validator ledger (`issues/issue_sprint19_validator.md:111+`) do — instead, `docs/PLAN.md:1149` notes "Architect's docs (chapters 6, 10, 12, 13, 27) were updated round-2 to reflect the workspace-root path. Three integrator-mechanical edits across the architect's round-1 prose".

**Recommendation**: integrator adds a brief "Closure round 2 — integrator-mechanical book edits, 2026-05-21" subsection to `issues/issue_sprint19_architect.md` that records the three round-1 → round-2 edits applied to the architect's deliverables (the chapter-6 single-copy rewrite, the chapter-10/12/13 path corrections, the chapter-27 regen with the corrected flag description), mirroring the shape of staff's and validator's round-2 closures. Pure ledger hygiene — does not affect any user-visible artefact and can be deferred to a post-cut housekeeping commit.

##### Finding 8 — pre-implementation motivation prose in architect Issue 1 retains round-1 path names (informational; pre-impl spec, not user-facing)

**Severity**: low.

**File:line**: `issues/issue_sprint19_architect.md:28` and `:30`.

**Observation**: Architect Issue 1's "Required content" and "Secrets-on-disk note" bullets (motivation prose, predates implementation) still name the round-1 `state/terraform.tfvars.user` and `state-cluster/terraform.tfvars.user` paths as what gets persisted. This is the seed-spec the architect agent was dispatched with — it accurately records what was asked of the role at dispatch time, not what shipped. The shipped chapter content (Finding 1) and shipped CHANGELOG (Finding 4) use the corrected workspace-root path.

**Recommendation**: optional — integrator may add a marginal note at the top of the architect motivation section ("Note: round-2 corrected the persistence path to the workspace-root single copy; see Closure round 2 below") so a future maintainer reading the ledger linearly doesn't take the seed-spec for as-shipped behaviour. Defer-post-cut is fine; pure-ledger hygiene.

#### Final launch verdict

**READY TO CUT v1.6.4 — GREEN.**

All six required surfaces of the `init --var-file` feature ship coherent and round-2-corrected:

- `init --help` (binary), `27-command-reference.md` line 529 (regenerated reference), and `book/src/06-workspaces.md` §"Skip the interview" prose are mutually consistent on the workspace-root single-copy persistence path that serves both phases.
- All five book-chapter components (when, flow, what's persisted, why it matters, secrets-on-disk, diagnostics) are present in `06-workspaces.md:139-178`.
- Cross-chapter sweep landed in 10/12/13; no stale `state/terraform.tfvars.user` or `state-cluster/terraform.tfvars.user` paths survive in `book/src/`.
- `CHANGELOG.md` v1.6.4 + `docs/PLAN.md` §"Sprint 19" closure are coherent with the as-shipped staff/architect/validator changes including the round-2 corrections.
- The `0600` claim in the book matches the staff implementation and the validator's hermetic assertion exactly.
- Live `!` GREEN (run-id `20260521-031343`) per `live-verify-high-issues`.

Eight findings filed; all `low` (six informational pass-pins + two ledger-hygiene items). Zero `high` findings; zero `medium`. The two `low` ledger-hygiene items (Findings 7 + 8) and the one `low` docstring item (Finding 6) are housekeeping the integrator may fold into a post-cut commit at their discretion.

No Part B issues filed — the cross-cutting docs surface for this sprint was small (one flag on one command) and the other three roles + the round-2 corrections covered the user-facing drift completely.

Did not commit. Did not push. Did not run `gh`.
