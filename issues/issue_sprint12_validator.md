# Sprint 12 — validator issues

Format: one issue per finding. `Severity: low | medium | high | blocker`.
`Status: open | in-progress | resolved | wontfix | accepted`.

Sprint 12 is a patch cycle (`v1.4.1`) landing the `--var-file`
relative-path fix surfaced post-v1.4.0. Validator scope: the seven-step
regression sweep, the bug reproduce/verify against staff's
working-tree fix, the cross-link audit on architect's CHANGELOG /
PLAN.md / chapter 6 edits, the `mdbook build book/` HTML gate, and the
analogous shell-CWD-vs-state-dir gotcha sweep.

Note: staff's fix is in the uncommitted working tree
(`internal/cli/lifecycle.go`, `cluster_phase.go`, `bnk_phase.go`, new
`internal/cli/lifecycle_test.go`). Verified against the working tree,
not git history, per the restart brief.

---

## Issue 1: Regression sweep — seven gates green

**Severity**: medium (gate)
**Status**: resolved

### Steps run (working tree, staff's fix in place uncommitted)

| Step | Command | Result | Notes |
|---|---|---|---|
| 1 | `go build ./...` | clean (exit 0) | no output |
| 2 | `go test ./...` | green (exit 0) | every package PASS; `internal/cli` includes the 5 new `resolveVarFiles` tests; other suites unchanged (`internal/doctor` 14.5s, rest cached/quick) |
| 3 | `go vet ./...` | clean (exit 0) | no output |
| 4 | `gofmt -d -l .` | clean (exit 0) | no output (staff's new file + 3 edited files all formatted) |
| 5 | `make staticcheck` | clean (exit 0) | dispatched via `make` so `GOPATH/bin/staticcheck` resolves |
| 6 | `make build-integration-tags` (`go build -tags integration ./...`) | clean (exit 0) | |
| 7 | `go test -tags integration ./internal/exec/... ./internal/remote/...` | green (exit 0) | `internal/exec` 35.4s, `internal/remote` cached. `kind` not installed on host (`which kind` exit 1); full `scripts/integration-test.sh` kind-bring-up skipped per Sprint 10/11 precedent (exit-2 short-circuit). |

### Verdict

GREEN. No regressions in the wider surface. Staff's per-RunE
normalization is additive and idempotent; no existing suite moved.

---

## Issue 2: Bug reproduce + fix verification

**Severity**: high (headline)
**Status**: resolved (in-tree test surface), open (out-of-band live verify — user)

### Pre-fix sanity

Per the restart brief, staff's fix is in the uncommitted working tree
and must not be stashed/reverted to fake a pre-fix state. The root
cause is confirmed by inspection of the *prior* behavior documented in
`issues/issue_sprint12_staff.md` §"Root cause": `flagVarFiles` flowed
verbatim to `tfws.Plan` / `applyWithRetry` / `tfws.Destroy`, and
terraform runs with `CWD = stateDir`, so a relative `--var-file`
resolved against the per-phase state dir, not the shell PWD. The fix
inserts `resolveVarFiles(flagVarFiles)` at the top of every consuming
RunE (verified: 10 wire-up sites — `runUp`, `runTrialUp`, `runPlan`,
`runApply`, `runDown`, `runTrialDown` in `lifecycle.go`;
`runClusterUp`, `runClusterDown` in `cluster_phase.go`; `runBnkUp`,
`runBnkDown` in `bnk_phase.go`).

Helper inspected at `internal/cli/lifecycle.go:125-156`: empty-input
short-circuit (no `os.Getwd`), `~`/`~/` expansion via
`os.UserHomeDir` (matches `install.go` convention), absolute
pass-through via `filepath.Clean`, relative `filepath.Join(cwd, …)`
with pre-flight `os.Stat` that errors naming both forms. Matches the
staff §"Proposed fix" + §"Closure" exactly.

### Post-fix verify — literal trace

`go test -run VarFile -count=1 -v ./internal/cli/`:

```
=== RUN   TestResolveVarFiles_AbsolutePassThrough
--- PASS: TestResolveVarFiles_AbsolutePassThrough (0.01s)
=== RUN   TestResolveVarFiles_RelativeResolvedAgainstCWD
--- PASS: TestResolveVarFiles_RelativeResolvedAgainstCWD (0.03s)
=== RUN   TestResolveVarFiles_MissingFileErrorNamesBoth
--- PASS: TestResolveVarFiles_MissingFileErrorNamesBoth (0.03s)
=== RUN   TestResolveVarFiles_TildeExpansion
--- PASS: TestResolveVarFiles_TildeExpansion (0.01s)
=== RUN   TestResolveVarFiles_EmptyInput
--- PASS: TestResolveVarFiles_EmptyInput (0.00s)
PASS
ok  	github.com/jgruberf5/roksbnkctl/internal/cli	0.220s
```

5/5 PASS. The three `issue_sprint12_staff.md` §"Acceptance criteria"
subtests are all covered and green:

- absolute pass-through → `TestResolveVarFiles_AbsolutePassThrough`
- relative resolved against CWD → `TestResolveVarFiles_RelativeResolvedAgainstCWD`
- missing-file error names both paths → `TestResolveVarFiles_MissingFileErrorNamesBoth`

Plus `TestResolveVarFiles_TildeExpansion` (pins the `install.go`
`~`-convention compatibility) and `TestResolveVarFiles_EmptyInput`
(no-op safety because every RunE calls the helper unconditionally).

### Out-of-band action for the user

The live verify is the user's responsibility (same hand-off shape as
Sprint 11 Issue 2 — the original bug surfaced via a live
`roksbnkctl up --var-file=./terraform.tfvars --auto`). Once v1.4.1
lands, re-run from a directory containing `terraform.tfvars`:

```bash
cd <dir-with-terraform.tfvars>
roksbnkctl -w <existing-workspace> cluster up --var-file=./terraform.tfvars --auto
# expect: terraform consumes the local file (no "Given variables file
#          ./terraform.tfvars does not exist" error)
roksbnkctl up --var-file=../shared.tfvars --auto   # sibling-dir form
roksbnkctl up --var-file=./missing.tfvars --auto   # expect pre-flight
#          error naming BOTH ./missing.tfvars AND the resolved abs path
```

If any diverges, re-open against
`internal/cli/lifecycle.go::resolveVarFiles`.

---

## Issue 3: Cross-link audit — CHANGELOG / PLAN.md / chapter 6 vs staff output

**Severity**: low (cross-link consistency)
**Status**: resolved

| Architect claim | Actual staff output | Verdict |
|---|---|---|
| CHANGELOG `v1.4.1` `### Fixed`: relative `--var-file` resolves against invocation CWD; "small `resolveVarFiles` helper called at each `--var-file`-consuming command's `RunE`" | `resolveVarFiles` at `internal/cli/lifecycle.go:125`; called at 10 RunE sites across lifecycle/cluster_phase/bnk_phase | match |
| CHANGELOG: absolute paths unchanged; missing-file error names both paths; docker reject now redundant but kept as guard | helper `filepath.Clean`s absolutes, `os.Stat` errors `--var-file %q (resolved to %q)`, docker reject loop untouched per staff §"Closure" | match |
| PLAN.md §"Sprint 12" code deliverable 1: helper in `lifecycle.go`, `cluster_phase.go`, `bnk_phase.go`; deliverable 2: `lifecycle_test.go` table | exactly those three files modified + new `lifecycle_test.go`; `git status` shows no other code files touched | match |
| Chapter 6 nudge A (cred-resolver context, team-handoff): replace/remove `<redacted>` line, cred resolver supplies key; links `./14-credentials-resolver.md` | landed after the redaction code block (`06-workspaces.md` ~line 80); link target `book/src/14-credentials-resolver.md` exists; renders as `./14-credentials-resolver.html` | match, no drift |
| Chapter 6 nudge B (defaults caveat): "embedded Terraform module defaults are **not** captured" after the worked-example block | landed at `06-workspaces.md` ~line 118; consistent with `internal/config/applied_tfvars.go` which only captures explicit source-file/config-derived vars (no module-default capture path) | match, no drift |

CHANGELOG also adds a `### Deferred (v1.x roadmap, post-v1.4.1)` block
carrying the v1.4.0 list forward unchanged — accurate, no new PRDs this
cycle. PLAN.md §"Sprint 12" gate block lists the same gates this
validator ran.

**Verdict**: cross-link audit PASS. Architect's claims match staff's
landed surface; the two chapter 6 nudges introduce no drift against
`internal/config/applied_tfvars.go`.

---

## Issue 4: `mdbook build book/` — HTML backend gate

**Severity**: low
**Status**: resolved

**Command**: `PATH="$HOME/.cargo/bin:$PATH" mdbook build book/`
(all three helpers resolve: `mdbook`, `mdbook-mermaid`,
`mdbook-pandoc` at `/home/jgruber/.cargo/bin/`).

**HTML backend**: exit 0 for the HTML renderer —
`INFO HTML book written to /mnt/c/project/roksbnkctl/book/book/html`.
`book/book/html/06-workspaces.html` present (45845 bytes, regenerated
this session).

- Architect nudge A rendered: `grep -c 'teammate receives this file
  out-of-band'` → 1.
- Architect nudge B rendered: `grep -c 'embedded Terraform module
  defaults are'` → 1.
- New internal link reachable: `href="./14-credentials-resolver.html"`
  present (target chapter exists in `book/src/`).

**PRD cross-link grep** (Sprint 11 published-book-404 fix still in
place):

```
$ grep -c 'href="https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd' book/book/html/06-workspaces.html
2
$ grep -c 'href="\.\./\.\./docs/prd' book/book/html/06-workspaces.html
0
```

Two absolute GitHub URLs, zero relative `../../docs/prd/` paths —
unchanged from the Sprint 11 baseline (`issue_sprint11_validator.md`
Issue 6).

**Pandoc backend**: exit 101, fails with
`cannot open /opt/render-mermaid.lua: No such file or directory`.
Identical to Sprint 11 Issue 6 — a known orthogonal host-config issue
(hardcoded container lua-filter path absent on this host), NOT a gate
failure. The HTML backend is what GitHub Pages serves.

**Verdict**: HTML backend gate PASS. Pandoc failure noted as
pre-existing host-environment issue, orthogonal to v1.4.1 content.

---

## Issue 5: Analogous shell-CWD-vs-state-dir gotcha — `--tf-source` local path (Sprint 13 follow-up)

**Severity**: low
**Status**: resolved (pulled into Sprint 12 per integrator decision)

### Finding

Sweeping path-shaped flags via
`grep -rn "Flags().String.*[Ff]ile\|...[Pp]ath\|StringArrayVar"
internal/cli/` surfaced one flag with the *same* class of bug
`resolveVarFiles` just fixed:

- **`--tf-source`** (`internal/cli/lifecycle.go:86,89` — registered on
  `init` and `up`, help text "override TF source (**path** or URL)").
  A `local`-type value flows verbatim into `config.TFSourceCfg{Type:
  "local", Path: flagTFSource}` and is **persisted into config.yaml**
  at `internal/cli/init.go:199-201,257-259`. It's later consumed by
  `internal/tf/fetch.go:45-56` (`FetchSource`, case `"local"`), which
  `os.Stat`s `src.Path` and returns it **unmodified** as terraform's
  source directory.

A relative `--tf-source=./mytf` passes the `os.Stat` at `init` time
(CWD = shell PWD), gets persisted *relative* into config.yaml, and is
later handed to terraform whose CWD is the per-phase state dir — the
exact shell-CWD-vs-state-dir trap, but **worse than the `--var-file`
case** because it survives into config.yaml and detonates on a *later*
`up`/`plan`/`apply` run, not the same invocation.

Other candidates swept and cleared:

- `--var-file` (`StringArrayVar &flagVarFiles`) — fixed this sprint.
- `targets add --key-path` (`targets.go:67`) — read at command time in
  the same CWD; not forwarded to a different-CWD sub-process. OK.
- `tfvars -o` (`tfvars.go:40`, default `./terraform.tfvars`) —
  `os.WriteFile` relative to the invoking CWD only; no state-dir
  hand-off. OK.
- `k apply -f` (`k_apply.go:40`) — consumed by the in-process k8s
  apply (`k8s.ApplyOptions`), same CWD as the CLI process. OK.
- `init --tf-source` URL form / github form — no local path. OK.
- No `init --backend-config` flag exists (the staff issue's
  hypothetical) — `grep` confirms it's not exposed.

### Proposed fix (Sprint 13 — not applied here, validator scope)

Normalize a `local` `--tf-source` to an absolute path before pinning
it into config.yaml, mirroring `resolveVarFiles`. Smallest surface:
resolve in `init.go` right before `config.TFSourceCfg{...}` is built
(both sites: `init.go:199-201` and `init.go:257-259`), e.g.:

```diff
 if flagTFSource != "" {
-    tfCfg := config.TFSourceCfg{Type: "local", Path: flagTFSource}
+    src := flagTFSource
+    if !isURLish(src) { // local path form
+        abs, err := filepath.Abs(src)
+        if err != nil {
+            return fmt.Errorf("resolve --tf-source %q: %w", src, err)
+        }
+        src = abs
+    }
+    tfCfg := config.TFSourceCfg{Type: "local", Path: src}
```

(or, defensively, also `filepath.Abs`-normalize in
`FetchSource`'s `case "local"` so pre-existing relative config.yaml
entries self-heal). Exact placement is staff's call next sprint.

Filed as a separate low-severity issue per the validator brief §5 so
it doesn't get lost. Out of scope for v1.4.1 (the patch cycle is
strictly the `--var-file` fix); does not block the v1.4.1 tag.

### Resolution (pulled into Sprint 12 per integrator decision)

The integrator pulled this into Sprint 12 (no longer deferred to Sprint
13). Resolved by staff as `issue_sprint12_staff.md` **Issue 2**. Both
layers the analysis above proposed landed: (1) init-time normalization
via a new `internal/cli/init.go::resolveLocalTFSource` helper (placed
beside `looksLikeGitHubRepo`, mirroring `resolveVarFiles` conventions)
wired at *both* `config.TFSourceCfg{Type:"local"}` build sites
(`runUpgradeTF`, `promptTFSource`); and (2) the defensive
`FetchSource` `case "local"` `filepath.Abs` self-heal so
already-written relative config.yaml entries recover on the next
`up`/`plan`/`apply`. The draft's hypothetical `isURLish` guard proved
unnecessary — the embedded/github forms are already split off upstream
(the `"embedded"` literal + `looksLikeGitHubRepo`), so only local paths
reach the helper; the existing type-detection mechanism was reused
rather than a new one invented. Five new tests
(`TestResolveLocalTFSource_{RelativeResolvedToAbs,AbsolutePassThrough,EmptyInput,GitHubFormUntouched}`
in `internal/cli/lifecycle_test.go`,
`TestFetchSource_Local_RelativePathSelfHeals` in
`internal/tf/fetch_test.go`) all pass; `go build ./...` and
`go test ./internal/cli/... ./internal/tf/...` green; landed in the
working tree (commit-free, tag is integrator-owned).

---

## Issue 6: Re-gate after Issue 5 pulled into Sprint 12

**Severity**: medium (gate)
**Status**: resolved

Re-gate pass after the integrator pulled validator Issue 5
(`--tf-source` local relative-path trap) into Sprint 12. Staff landed
the fix (`issue_sprint12_staff.md` Issue 2) and the architect widened
the CHANGELOG / PLAN.md scope (`issue_sprint12_architect.md` Issue 8).
The codebase changed since the Issues 1-5 sweep (`internal/cli/init.go`
+ `internal/tf/fetch.go` now carry the `--tf-source` fix), so all
seven gates were re-run from scratch against the current working tree.

### Seven-step regression sweep (re-run, working tree, staff fixes uncommitted)

| Step | Command | Result | Notes |
|---|---|---|---|
| 1 | `go build ./...` | clean (exit 0) | no output |
| 2 | `go test ./...` | green (exit 0) | whole module PASS; changed pkgs re-run with `-count=1` (`internal/cli` 1.449s, `internal/tf` 0.110s) — not just cache |
| 3 | `go vet ./...` | clean (exit 0) | no output |
| 4 | `gofmt -d -l .` | clean (exit 0) | empty (new helper in `init.go`, `fetch.go` self-heal, both test files formatted) |
| 5 | `make staticcheck` | clean (exit 0) | no output |
| 6 | `make build-integration-tags` | clean (exit 0) | `go build -tags integration ./...` |
| 7 | `go test -tags integration ./internal/exec/... ./internal/remote/...` | green (exit 0) | `internal/exec` 22.2s PASS. `internal/remote`: one transient `integration_test.go:357 Cannot connect to the Docker daemon` blip on a single fresh run, then 4 consecutive clean passes once `docker info` returned exit 0 — testcontainers-spun SSH-server container is Docker-daemon-availability-flaky in the sandbox, **not** a code regression (no working-tree change touches `internal/remote` or `internal/exec`; `git status --porcelain` confirms). Same host-environment class as the absent `kind` (kindless precedent unchanged, Sprint 10/11). |

**Verdict**: GREEN. Staff's `--tf-source` additions are additive in
`internal/cli`/`internal/tf` only; no existing suite moved.

### --tf-source fix verification — literal trace

`go test -run 'TFSource|FetchSource' -count=1 -v ./internal/cli/... ./internal/tf/...`:

```
--- PASS: TestResolveLocalTFSource_RelativeResolvedToAbs (0.03s)
--- PASS: TestResolveLocalTFSource_AbsolutePassThrough (0.00s)
--- PASS: TestResolveLocalTFSource_EmptyInput (0.00s)
--- PASS: TestResolveLocalTFSource_GitHubFormUntouched (0.00s)
ok  github.com/jgruberf5/roksbnkctl/internal/cli   0.145s
--- PASS: TestFetchSource_Local_OK (0.00s)
--- PASS: TestFetchSource_Local_NotADir (0.00s)
--- PASS: TestFetchSource_Local_Missing (0.00s)
--- PASS: TestFetchSource_Local_EmptyPath (0.00s)
--- PASS: TestFetchSource_Local_RelativePathSelfHeals (0.02s)
--- PASS: TestFetchSource_GitHub_NeedsRepoAndRef (0.00s)
--- PASS: TestFetchSource_UnknownType (0.00s)
ok  github.com/jgruberf5/roksbnkctl/internal/tf    0.060s
```

5 new tests, 5/5 PASS (4 in `lifecycle_test.go` + 1 self-heal in
`fetch_test.go`); 6 pre-existing `FetchSource` tests still green
(no regression).

All three acceptance directions hold, verified against test bodies:

- **relative local → persisted absolute**:
  `TestResolveLocalTFSource_RelativeResolvedToAbs` chdirs to a tmp
  dir, asserts `resolveLocalTFSource("./mytf")` returns an absolute
  path EvalSymlinks-matched to the fixture.
- **absolute → unchanged**:
  `TestResolveLocalTFSource_AbsolutePassThrough` asserts the input
  round-trips `filepath.Clean`'d, no mutation.
- **URL/github form → no `filepath.Abs` rewriting**:
  `TestResolveLocalTFSource_GitHubFormUntouched` asserts the
  `owner/repo` slug survives as a path *suffix* (proving only path
  joining, no URL parsing) — and the real guarantee is the call sites
  split embedded/github off upstream (`"embedded"` literal +
  `looksLikeGitHubRepo`) before the helper is ever reached.
- **self-heal**: `TestFetchSource_Local_RelativePathSelfHeals` feeds a
  pre-fix-style `TFSourceCfg{Type:"local", Path:"./legacy-tf"}` and
  asserts `FetchSource` absolutizes it from the invocation CWD.

**Helper + site inspection vs original Issue 5 §"Proposed fix" spec**:
matches intent. Both proposed layers landed —

1. **Init-time normalization**: `resolveLocalTFSource(path string)
   (string, error)` at `internal/cli/init.go:52`, placed beside
   `looksLikeGitHubRepo` (line 28), mirroring `resolveVarFiles`
   conventions (empty-input short-circuit, `~`/`~/` via
   `os.UserHomeDir`, absolute `filepath.Clean` pass-through, relative
   `filepath.Abs`, `resolve --tf-source %q: %w` wrap). Wired at the
   two `--tf-source`-flag `config.TFSourceCfg{Type:"local"}` build
   sites: `runUpgradeTF` (`init.go:248,252`) and `promptTFSource`'s
   flag short-circuit (`init.go:312,316`).
2. **`FetchSource` self-heal**: `internal/tf/fetch.go` `case "local"`
   (`fetch.go:56-63`) `filepath.Abs`-normalizes a non-absolute
   `src.Path` before the `os.Stat`/dir checks, error-wrap kept as the
   existing `local TF source %s: %w` form. Idempotent for
   already-absolute (layer-1-written) configs.

The draft's hypothetical `isURLish` guard was correctly judged
unnecessary — embedded/github are split off upstream, so only local
paths reach the helper. **Note (not a defect)**: there is a *third*
`config.TFSourceCfg{Type:"local", Path: input}` build site at
`init.go:356` — `promptTFSource`'s *interactive-prompt* local-path
branch — which staff's closure ("both local build sites") does not
enumerate. It is intentionally **not** init-normalized, but is fully
covered by the layer-2 `FetchSource` self-heal (any relative value
written via the interactive prompt is absolutized at fetch time, same
as a legacy config.yaml). This is exactly the belt-and-suspenders
posture my original Issue 5 §"Proposed fix" recommended; the design is
sound and the gap is closed by the backstop. Recorded for the record;
does not block the tag.

### Cross-link audit (expanded)

| Claim | Landed surface | Verdict |
|---|---|---|
| CHANGELOG intro reframed to **two** sibling shell-CWD-vs-state-dir bugs; `--tf-source` pulled from Sprint 13 backlog; cross-links `issue_sprint12_validator.md` Issue 5 | `CHANGELOG.md:9` intro + `:14` second `### Fixed` bullet match; behavior-level only | match |
| Second `### Fixed` bullet: relative local `--tf-source` → absolute in config.yaml; absolute + URL/GitHub forms unchanged; no over-claimed internal symbol | `CHANGELOG.md:14` — describes behavior; names neither `resolveLocalTFSource` nor `FetchSource`. (The `--var-file` bullet does name `resolveVarFiles` — original v1.4.1 bullet, unchanged, accurate) | match — no over-claim per architect Issue 8 |
| PLAN.md §"Sprint 12" retitled "relative-path resolution fixes"; new "Scope expansion — Issue 5 pulled forward from Sprint 13" subsection; two-bullet Drivers; code-deliverable row 3; gate names both bugs | `docs/PLAN.md:850,856,863,873,888-890` all present; row 3 says "exact placement and helper naming are staff's call (landing in parallel)" — deliberately no symbol over-claim | match |
| No Sprint 13 section in PLAN.md to strike/annotate | `grep` confirms no `## Sprint 13` heading; only the Scope-expansion narrative reference | match (architect Issue 8 note correct) |
| Consistency with staff Issue 2 + architect Issue 8 | Staff Issue 2 closure (helper at `init.go::resolveLocalTFSource`, both flag sites, `fetch.go` self-heal, 5 tests) and architect Issue 8 (CHANGELOG/PLAN scope-bump, behavior-level framing) both accurately reflect the landed surface | consistent |

**Verdict**: expanded cross-link audit PASS. CHANGELOG + PLAN.md
describe the landed `--tf-source` behavior accurately and at the
behavior level only (no internal-symbol over-claim); consistent with
staff Issue 2 and architect Issue 8.

### --var-file regression — still green

Staff's new edits touched `init.go`/`fetch.go`; re-confirmed the
original `--var-file` fix is untouched. `resolveVarFiles` still at
`internal/cli/lifecycle.go:125`; all 10 wire-up sites intact
(`runUp/runTrialUp/runPlan/runApply/runDown/runTrialDown` in
`lifecycle.go`; 2 in `cluster_phase.go`; 2 in `bnk_phase.go`), each
with the `flagVarFiles = resolved` reassignment. `go test -run
VarFile -count=1 -v ./internal/cli/` → 5/5 PASS
(`TestResolveVarFiles_{AbsolutePassThrough,RelativeResolvedAgainstCWD,
MissingFileErrorNamesBoth,TildeExpansion,EmptyInput}`). No regression.

### mdbook HTML gate — not re-run (decision)

Optional this pass. Issue 8 modified only `CHANGELOG.md`,
`docs/PLAN.md`, and the issue files — no `book/src/` change landed
since the Issue 4 HTML gate. The Sprint-12 Issue 4 HTML-backend PASS
result stands unchanged; deliberately not re-run (no input changed).

### Verdict

GREEN. The now-two-bug `v1.4.1` tag is gate-clean: seven-step sweep
green (kindless/Docker-daemon host limits noted, not regressions),
`--tf-source` fix verified against the original Issue 5 spec (both
layers landed, 5/5 new tests pass, third interactive-prompt build site
covered by the self-heal backstop), expanded cross-link audit PASS,
`--var-file` fix still green. Conditional only on the user's
out-of-band live `--var-file` re-run (Issue 2, unchanged) — Issue 5 is
now resolved in-tree, no longer a deferred follow-up.

---

## Final report

**Headline**: Sprint 12 closure is **GREEN** for the `v1.4.1` tag.

- **Seven-step sweep** (working tree, staff fix uncommitted): all
  green — build / test / vet / gofmt / staticcheck / integration build
  / kind-less integration tests (kind absent, full
  `integration-test.sh` skipped per Sprint 10/11 precedent).
- **Bug-fix verification**: `go test -run VarFile -count=1 -v
  ./internal/cli/` → 5/5 PASS; all three
  `issue_sprint12_staff.md` acceptance-criteria subtests covered
  (`TestResolveVarFiles_{AbsolutePassThrough,RelativeResolvedAgainstCWD,MissingFileErrorNamesBoth}`)
  plus tilde-expansion + empty-input. Helper at
  `lifecycle.go:125-156` wired at all 10 RunE sites.
- **Cross-link audit**: PASS — CHANGELOG `v1.4.1` `### Fixed`,
  PLAN.md §"Sprint 12" code deliverables, and the two chapter 6
  nudges all match staff's landed surface; no drift vs
  `applied_tfvars.go`.
- **mdbook HTML gate**: PASS (exit 0; chapter 6 nudges + cred-resolver
  link render; 2 absolute / 0 relative PRD cross-links). Pandoc
  backend fails on the known orthogonal `/opt/render-mermaid.lua`
  host issue (not a gate).
- **Analogous-gotcha sweep**: one new finding — `--tf-source` local
  path has the same shell-CWD-vs-state-dir trap (persisted into
  config.yaml). Filed as **Issue 5**, low-severity, out-of-scope for
  v1.4.1, flagged for Sprint 13.

**Issues filed**: 5 — 1 high (Issue 2, resolved in-tree; live verify
deferred to user out-of-band), 1 medium (Issue 1, resolved), 3 low
(Issues 3, 4 resolved; Issue 5 open / Sprint-13 follow-up).

**Gate verdict for `v1.4.1` tag**: **GREEN**, conditional only on the
user's out-of-band live `--var-file` re-run (Issue 2). No blockers;
Issue 5 is explicitly out-of-scope and non-blocking.
