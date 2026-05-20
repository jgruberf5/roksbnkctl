# Sprint 12 — tech-writer issues

Read-only end-of-cycle review pass for the `v1.4.1` patch tag (the
`--var-file` relative-path fix + two chapter 6 discoverability nudges
deferred from Sprint 11). Dispatched after staff / architect /
validator. Working tree reviewed (staff fix uncommitted, per the
restart brief). The only file written this cycle is this one.

Format: one issue per finding. `Severity: low | medium | high | blocker`.
`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1: Drift sweep — staff issue ↔ lifecycle.go ↔ CHANGELOG v1.4.1 ↔ PLAN.md Sprint 12

**Severity**: low (verification record — no drift found)
**Status**: accepted

Four-surface agreement table per the prompt. All rows agree.

| Claim | Source 1 (staff issue) | Source 2 (code) | Source 3 (CHANGELOG) | Source 4 (PLAN.md) |
|---|---|---|---|---|
| Relative `--var-file` resolves against invocation CWD | Issue 1 §"Proposed fix" — normalize against `os.Getwd()` before either backend | `internal/cli/lifecycle.go:125-156` `resolveVarFiles`; `filepath.Join(cwd, expanded)` at :149 | `### Fixed` bullet — "resolve … against the user's shell CWD … matching terraform's own `-var-file=./...` semantics" | §"Code deliverables" row 1 — "joins relative entries against `os.Getwd()`" | **agree** |
| Missing-file error names both input + resolved path | §"Acceptance criteria" bullet 3 | :151 `fmt.Errorf("--var-file %q (resolved to %q): %w", vf, abs, err)` | `### Fixed` — "names both the user-supplied path and the absolute path it resolved to" | §"Code deliverables" row 1 — "names both the user-supplied and resolved-absolute forms" | **agree** |
| Absolute paths pass through unchanged | §"Acceptance criteria" bullet 4 | :145-147 `filepath.IsAbs` → `filepath.Clean` branch | `### Fixed` — "Absolute paths continue to work unchanged" | §"Code deliverables" row 1 — "pass-throughs absolute entries via `filepath.Clean`" | **agree** |
| Helper home (`lifecycle.go`) + per-RunE wire-up at every consuming entry point | §"Closure" — 10 RunE sites across `lifecycle.go` / `cluster_phase.go` / `bnk_phase.go` | helper at `lifecycle.go:125`; called at `runUp` (:181), `runTrialUp` (:228), + 8 more per `git status` (only those 3 code files + new test touched) | `### Fixed` — "small `resolveVarFiles` helper called at each `--var-file`-consuming command's `RunE`" | §"Code deliverables" row 1 names same three files | **agree** |
| `~` / `~/` expansion mirrors `install.go` convention | §"Closure" — mirrors `install.go:76` | :136-144 `~`/`~/` → `os.UserHomeDir` join | (n/a — error-message/behaviour shape, not user-visible in CHANGELOG) | §"Risks" notes `~`-expansion decision recorded in test names | **agree** |
| Docker-backend absolute-only reject now redundant, kept as guard | §"Proposed fix" / §"Closure" — leave in place as defensive guard | reject loop untouched (`git status` shows no other code change) | `### Fixed` — "redundant for the common case … remains in place as a defensive guard" | (n/a) | **agree** |

No divergence on any surface. Docs match code; code matches the staff
spec. No proposed-fix patch needed.

---

## Issue 2: Dogfooding loop — `roksbnkctl up --var-file=./terraform.tfvars --auto`

**Severity**: low (discoverability — no stuck-point)
**Status**: accepted

Walked staff §"Reproduce" mentally: `cd` into a dir with
`terraform.tfvars` → `roksbnkctl up --var-file=./terraform.tfvars
--auto`. With the fix, `runUp` calls `resolveVarFiles` first
(`lifecycle.go:181`), the relative path is joined against the shell
CWD and `os.Stat`-passes, the absolute is reassigned into
`flagVarFiles`, and the leaf re-resolution is an idempotent no-op on
the now-absolute slice. No stuck-point in the flow.

Misleading-text check:

- `--var-file` cobra help (`lifecycle.go:100`): `"extra TF var-file
  (repeatable; later files override earlier)"` — says nothing about
  *where* relative paths resolve. Pre-fix this was a latent trap
  (silent state-dir resolution); post-fix the behaviour matches the
  user's terraform mental model, so the terse help text is no longer
  actively misleading. Not worth a separate nudge for v1.4.1.
- Chapter 6 / 7: no prose claims `--var-file` resolves state-dir-
  relative. The only `--var-file` mentions are :86 ("Not an input"
  chain) and :313 (workspace-isolation aside) — neither touches
  relative-path resolution. No correction needed.
- Snapshot source-attribution: `--var-file`-supplied files are not
  captured in `terraform.applied.tfvars` (architect Issue 6 verified
  the snapshot uses hardcoded source labels, not `flagVarFiles`), so
  the dogfooding step 3 expectation ("appears in the snapshot's
  source-attribution chain") is not a documented guarantee and chapter
  6 correctly does not promise it. No drift.

Verdict: dogfooding loop hits no stuck-point.

---

## Issue 3: Chapter 6 nudge review (architect Issues 3 + 4)

**Severity**: low (prose-placement record)
**Status**: accepted

**3a. Defaults caveat (§"Worked example", line 118).** Lands
immediately after the HCL worked-example block and before the
"header records / alphabetic ordering" paragraph. A
disaster-recovery reader who reads only the worked example now sees
the defaults caveat in the same eyeful. Cross-link
`[§"What it's **not**"](#what-its-not)` resolves: mdbook's slugifier
strips Markdown emphasis, so the `### What it's **not**` heading
(line 84) slugs to `what-its-not` — link target matches. Prose flows;
no run-on with the surrounding sentences.

**3b. Cred-resolver / team-handoff sentence (§"Redaction", line 80).**
Lands after the `ibmcloud_api_key = "<redacted>"` HCL block and before
the "file mode is `0600` regardless" paragraph. It gives an
out-of-band reader (teammate who opens the file without reading
chapter 6 top-to-bottom) a concrete action — replace or drop the
redacted line — and surfaces the `./14-credentials-resolver.md`
cross-link a second time, in the reading path where the question
actually arises. The pre-existing §"Redaction" cross-link at line 74
is retained, so the chapter does not read worse post-edit; the new
sentence is additive and scoped to the handoff scenario. Architect's
decision to decline the binary-side inline-comment URL change
(deferred: needs a docs-URL constant + mdbook-URL plumbing not in
this cycle) holds — that rationale is sound for a patch cycle.

No revert warranted on either nudge.

---

## Issue 4: Validator hand-off closures

**Severity**: low (hand-off triage)
**Status**: accepted

Reviewed `issues/issue_sprint12_validator.md` for items handed to
tech-writer. The only `open` item is **validator Issue 5**
(`--tf-source` local path has the same shell-CWD-vs-state-dir trap,
worse because it persists into `config.yaml` and detonates on a later
run). It is explicitly scoped **out of v1.4.1** and flagged for
Sprint 13 — it is a code/design follow-up, not a documentation gap,
so there is nothing for tech-writer to close in the docs surface this
cycle. No documentation gap surfaced by the validator regression
sweep was handed to tech-writer. Recording the no-op explicitly.

For Sprint 13 awareness: when `--tf-source` is fixed, the `init` /
`up --tf-source` cobra help (`lifecycle.go:86,89`, "override TF source
(path or URL)") will want the same scrutiny this issue gave the
`--var-file` help text — currently silent on relative-path
resolution. Not actionable for v1.4.1.

---

## Final verdict

Sprint 12 tech-writer surface is **GREEN** for the `v1.4.1` tag.

- Drift sweep: all six rows agree across staff issue / code /
  CHANGELOG / PLAN.md. No divergence; no proposed-fix patch needed.
- Dogfooding loop: no stuck-point; the `--var-file` cobra help is
  terse but no longer misleading post-fix; chapter 6/7 prose makes no
  contradicting relative-path claim.
- Chapter 6 nudges: both land at sensible spots, prose flows, the
  intra-page `#what-its-not` anchor and the inter-page
  `14-credentials-resolver` cross-link both resolve; no revert.
- Validator hand-off: no documentation gap handed to tech-writer;
  Issue 5 is a non-blocking Sprint-13 code follow-up.
- Validator gates (seven-step sweep, bug-fix verify, cross-link
  audit, mdbook HTML) all green.

No pre-tag conditions. (Validator's user out-of-band live `--var-file`
re-run remains a post-tag confidence check, not a documentation
blocker.)

---

## Issue 5: Re-review after `--tf-source` pulled into Sprint 12

**Severity**: low (verification record — no drift found)
**Status**: accepted

Re-review pass after the integrator pulled validator Issue 5
(`--tf-source` local relative-path trap) into Sprint 12. Staff landed
the fix (`issue_sprint12_staff.md` Issue 2), architect widened the
CHANGELOG/PLAN scope (`issue_sprint12_architect.md` Issue 8), validator
re-gated GREEN (`issue_sprint12_validator.md` Issue 6). v1.4.1 now
closes **two** sibling shell-CWD-vs-state-dir bugs. Issues 1-4 above
are unchanged and still hold (re-confirmed below).

### 5a. Expanded drift sweep — `--tf-source` across four surfaces

Four-surface agreement table for the `--tf-source` fix. All rows agree.

| Claim | Source 1 (staff Issue 2) | Source 2 (code) | Source 3 (CHANGELOG 2nd `### Fixed`) | Source 4 (PLAN.md Sprint 12) |
|---|---|---|---|---|
| Relative local `--tf-source` → persisted **absolute** in `config.TFSourceCfg.Path` | §"Proposed fix" layer 1 + §"Closure" — `resolveLocalTFSource` at both build sites | `init.go:52` helper; `:69` `filepath.Abs` for relative; wired at `runUpgradeTF` (`:248,252`) + `promptTFSource` flag short-circuit (`:312,316`) | `:14` — "records an absolute path in the workspace's `config.yaml`, so the source still resolves on a later `up` / `plan` / `apply`" | `:863,873` Drivers + code-deliverable row 3 — "resolve a relative local `--tf-source` value to an absolute path before it is persisted" | **agree** |
| Absolute `--tf-source` → unchanged (cleaned) | §"Acceptance criteria" bullet 2 | `init.go:66-68` `filepath.IsAbs` → `filepath.Clean` branch | `:14` — "Absolute `--tf-source` paths … are unchanged" | `:873` row 3 — "absolute paths … are pass-through" | **agree** |
| URL / GitHub `owner/repo` form → never routed through helper, structurally untouched | §"Proposed fix" — embedded/github split off upstream (`"embedded"` literal + `looksLikeGitHubRepo`); no `isURLish` guard needed | `init.go:341` embedded short-circuit + `:346` `looksLikeGitHubRepo` branch both `return` before any `Type:"local"` build site | `:14` — "the URL / GitHub source forms, are unchanged" | `:873` row 3 — "the URL / GitHub source forms are pass-through" | **agree** |
| Self-heal of pre-existing relative `config.yaml` entries — accurately, not over-, described in user-facing docs | §"Proposed fix" layer 2 — `FetchSource` `case "local"` absolutizes; idempotent for layer-1-pinned configs | `fetch.go:56-63` `!filepath.IsAbs` → `filepath.Abs` before `os.Stat`/dir checks | (n/a — CHANGELOG describes only the user-action behavior: relative `--tf-source` → absolute in config.yaml; **does not** surface the internal self-heal mechanism, correctly — not over-claimed) | (n/a — PLAN row 3 is behavior-level; no self-heal symbol claimed) | **agree** |

Self-heal-not-over-described check passes: the CHANGELOG 2nd bullet and
PLAN row 3 are strictly behavior-level. Neither names
`resolveLocalTFSource` nor the `FetchSource` self-heal, and neither
promises that legacy relative `config.yaml` entries are silently
rewritten in place — they describe only the user-visible guarantee (a
relative `--tf-source` no longer breaks a later run). The self-heal is
an internal robustness backstop; keeping it out of user docs is
correct, not a gap. No over-claim, no under-claim.

**Original `--var-file` rows re-confirmed (no regression).** Validator
Issue 6 §"--var-file regression" verified `resolveVarFiles` is still at
`lifecycle.go:125` with all 10 wire-ups intact and 5/5 tests green
after the `init.go`/`fetch.go` edits. Issue 1's six rows above are
re-confirmed unchanged: staff's `--tf-source` work touched only
`init.go` + `fetch.go` (+ test files) per `git status`, none of which
is on the `--var-file` path. No drift introduced.

### 5b. Dogfooding loop — `init --tf-source=./mytf` then later `up`

Walked the two-invocation flow mentally. `roksbnkctl init
--tf-source=./mytf` from a project dir: `runInit` → `promptTFSource`
flag short-circuit (`init.go:308`) → `resolveLocalTFSource("./mytf")`
→ `filepath.Abs` against the shell CWD → `config.TFSourceCfg{Type:
"local", Path: <abs>}` persisted into `config.yaml`, and the on-screen
confirmation `✓ TF source: local path <abs>` shows the *resolved
absolute* (so the user sees what was actually pinned). A later
`roksbnkctl up` from any directory → `FetchSource` `case "local"`
gets the already-absolute path (no-op self-heal) → `os.Stat` passes
regardless of `up`'s CWD. No stuck-point; the bug class is closed.

Misleading-text check:

- `--tf-source` cobra help — `init`: `"override TF source (path or
  URL); pinned into config.yaml"` (`lifecycle.go:86`); `up`:
  `"override TF source for this run only"` (`:89`). Neither states
  *where* a relative path resolves. Pre-fix this was a latent trap
  (silent state-dir resolution on a later run). Post-fix the value is
  normalized to absolute at `init` time and the confirmation line
  echoes the resolved absolute, so the terse help is no longer
  *actively* misleading — it never claimed CWD-at-up-time, and the
  resolved-absolute echo removes the ambiguity at the point of use.
  Mirrors the Issue 2 verdict for the `--var-file` help text. Not
  worth a separate v1.4.1 nudge.
- Chapter 12 §"`tf_source:`" (`12-workspace-config.md:170`): the
  `local` row already documents `path: "/abs/path/to/tf-source"` —
  the **absolute** form is the canonical documented shape. It never
  promised relative-path support, and post-fix a relative
  `--tf-source` is normalized to exactly this absolute form before
  persistence, so the doc and behavior are now *more* consistent than
  pre-fix, not less. No correction needed.
- Chapter 27 command reference (`:427,1092`): both `--tf-source`
  entries mirror the cobra help verbatim (auto-generated shape).
  Same terse-but-not-misleading verdict as the help text. Chapter 10
  (`:143`) and chapter 24 (`:36`, `local` renders `local:<Path>`)
  make no relative-path-resolution claim. No drift.

No low-severity discoverability nudge filed: the help text is silent
in a way that is no longer misleading (the trap it could have masked
is now closed at the source), and the canonical doc surface
(chapter 12) already shows the absolute form the fix produces.

### 5c. init.go:356 third build site — self-heal backstop assessment

Validator Issue 6 noted a *third* `config.TFSourceCfg{Type:"local",
Path: input}` build site at `init.go:356` — `promptTFSource`'s
*interactive-prompt* local-path branch — which staff's closure ("both
local build sites") does not enumerate. It is **not** init-time
normalized; a relative value typed at the interactive prompt is
persisted relative, then absolutized by the layer-2 `FetchSource`
self-heal on the next lifecycle run.

Assessment from a user-mental-model standpoint: **sound, no doc
contradiction.** No user-facing surface promises *init-time*
normalization specifically:

- CHANGELOG 2nd bullet promises only the end-state ("records an
  absolute path … so the source still resolves on a later run") — for
  the `--tf-source` *flag* path, which *is* init-normalized. It does
  not make any promise about the interactive prompt, and it does not
  promise *when* normalization happens, only *that* a later run
  resolves correctly.
- PLAN row 3 is scoped to "a relative local `--tf-source` *value*"
  (the flag) and is behavior-level.
- Chapter 12 documents the persisted shape as absolute but does not
  claim the binary rewrites the interactive-prompt input at write
  time.

The user-observable contract is "a relative local source still works
on a later run" — satisfied for the interactive-prompt path by the
self-heal backstop, just one layer later than the flag path. The only
cosmetic asymmetry (the flag path echoes the resolved absolute in its
`✓ TF source: local path <abs>` confirmation at `:317`, while the
interactive path at `:355` echoes the raw `input`) is not a documented
guarantee and not user-blocking. This is exactly the
belt-and-suspenders posture validator Issue 5 §"Proposed fix"
recommended; the design is self-consistent and no doc over-promises
init-time normalization. Recorded as sound; no nudge warranted for
v1.4.1.

### 5d. Launch-readiness verdict for the expanded v1.4.1

**GREEN.** The expanded two-bug v1.4.1 is launch-ready from the
tech-writer surface:

- Expanded drift sweep: all four `--tf-source` rows agree across staff
  Issue 2 / code / CHANGELOG / PLAN; self-heal accurately (not over-)
  described — kept out of user docs, correctly. Original six
  `--var-file` rows re-confirmed; no regression.
- Dogfooding: `init --tf-source=./mytf` → later `up` hits no
  stuck-point; help text terse but no longer misleading post-fix;
  chapter 12 canonical `local` form (absolute) is now *more*
  consistent with behavior than pre-fix.
- init.go:356 self-heal backstop: sound; no user-facing doc promises
  init-time normalization specifically; the interactive-prompt path's
  later-run contract is satisfied by the layer-2 self-heal.

No pre-tag documentation conditions. The only standing condition is
validator Issue 2's user out-of-band live `--var-file` re-run —
unchanged, non-blocking, a post-tag confidence check (not a
documentation blocker). Validator Issue 5 is now resolved in-tree (no
longer a deferred follow-up). Tech-writer surface remains GREEN for
the `v1.4.1` tag.
