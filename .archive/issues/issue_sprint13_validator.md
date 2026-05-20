# Sprint 13 — validator issues

> **Sprint 13 frame.** Feature cycle, `v1.5.0`. Validator scope:
> seven-step regression sweep (unchanged from Sprints 10–12; kind step
> skipped with the exit-2 short-circuit if `kind` is absent); Issue-1
> (KUBECONFIG leak) symptom reproduction + fix confirmation at unit
> level (live `--on jumphost kubectl` is the user's out-of-band
> action); PRD 08 + PRD 09 feature-acceptance matrices; doc/code
> lockstep audit (architect chapter 15/16 vs as-landed
> auto-registration); continued analogous-gotcha sweep; `mdbook build
> book/` HTML gate.
>
> See `prompts/sprint13/validator.md` for the task breakdown and
> `issues/issue_sprint13_staff.md` Issues 1/2/3 §"Acceptance criteria"
> / §"Reproduce" for the verify lists.

`Status: open | in-progress | resolved | wontfix | accepted`.

Note: the three staff code deliverables + the architect docs landed in
the **uncommitted working tree** (same posture as Sprint 12 —
`git status` shows modified `internal/cli/{cluster,cluster_phase,
lifecycle,remote}.go`, `internal/tf/terraform.go`, new
`internal/cli/{terraform,env_split,auto_cluster_jumphosts,
terraform_test}.go`, `internal/tf/readonly_test.go`, CHANGELOG,
chapters 15/16, PRD 08/09). Verified against the working tree, not git
history.

---

## Issue 1: Regression sweep — seven gates

**Severity**: medium (gate)
**Status**: resolved

| Step | Command | Result | Notes |
|---|---|---|---|
| 1 | `go build ./...` | clean (exit 0) | no output |
| 2 | `go test ./...` | green (exit 0) | whole module PASS; changed pkgs force-rerun `go test -count=1 ./internal/cli/... ./internal/tf/...` → `internal/cli` 1.565s, `internal/tf` 0.103s (not cache) |
| 3 | `go vet ./...` | clean (exit 0) | no output |
| 4 | `gofmt -d -l .` | clean (exit 0) | no output (all new + edited files formatted) |
| 5 | `make staticcheck` | clean (exit 0) | dispatched via `make` so `GOPATH/bin/staticcheck` resolves; no network |
| 6 | `make build-integration-tags` (`go build -tags integration ./...`) | clean (exit 0) | |
| 7 | `go test -tags integration ./internal/exec/... ./internal/remote/...` | `internal/remote` green (exit 0, 50.4s fresh); `internal/exec` one host-env FAIL (see below) | `kind` absent (`which kind` exit 1) → full `scripts/integration-test.sh` kind-bring-up skipped per Sprint 10–12 precedent |

### Step 7 — `internal/exec` host-environment failure (not a code regression)

`go test -count=1 -tags integration ./internal/exec/...` →
`TestIntegration_K8sBackend_JobMode_Echo` FAIL:
`Configuration error: mkdir /home/runner/.bluemix: permission denied`.
The sandbox `$HOME` (`/home/runner`) is not writable, so the
container-side `ibmcloud` CLI cannot create `~/.bluemix`. `git status
--porcelain internal/exec/` is **empty** — no v1.5.0 working-tree
change touches `internal/exec`; the env-split fix lives entirely in
`internal/cli`. This is the same host-environment class as the
absent-`kind` / Docker-daemon-flaky limits noted in Sprint 12
(`issue_sprint12_validator.md` Issues 1 & 6), not a v1.5.0 regression.
The SSH-boundary-relevant suite for Issue 1 — `internal/remote` —
passed clean fresh (50.4s).

### Verdict

GREEN. Six gates clean; step-7 `internal/remote` green; the lone
`internal/exec` failure is a sandbox FS-permission limit on an
unmodified package, consistent with prior-sprint kindless/Docker host
precedent. No v1.5.0 code regression.

---

## Issue 2: Issue-1 (KUBECONFIG leak) reproduction + fix confirmation

**Severity**: high (headline bugfix)
**Status**: resolved (in-tree, unit-level + architect live-verify on
record); live `--on jumphost kubectl` is the user's out-of-band action

### Fix shape (read out of the landed tree)

`workspaceEnv()` (`internal/cli/cluster.go:595`) refactored into:

- `workspaceEnvCore()` (`cluster.go:618`) — machine-portable subset
  only (`IBMCLOUD_API_KEY` / `IC_API_KEY` / `IBMCLOUD_REGION` /
  `IBMCLOUD_VERSION_CHECK`). Crucially it also scrubs an
  inherited-from-shell `KUBECONFIG` out of `os.Environ()` via
  `remoteSafeEnv` (`cluster.go:642`) — correctness is "never send a
  local path", including inherited ones.
- `workspaceEnv()` — calls the core, then appends the **local-only**
  `KUBECONFIG` addendum (`cluster.go:603-605`). Local exec unchanged.

Every `dispatchRemote(` call site on an `on != ""` branch sources
`workspaceEnvCore()`: `runExec` (`cluster.go:115,119`),
`runIBMCloudPassthrough` (`cluster.go:339,343`), `runPassthrough`
(`cluster.go:559,563`). The local-exec branches keep full
`workspaceEnv()`. `dispatchRemoteShell` (`remote.go:113`) injects no
env at all (`client.Shell` carries none). `test.go:713` uses
`workspaceEnv()` but never dispatches remotely.

Layer-2 defense-in-depth: `dispatchRemote` (`remote.go:55`) runs
`envExtra = remoteSafeEnv(envExtra)` so a future caller that forgets
the core cannot reintroduce the leak. `localPathEnvKeys` =
`{KUBECONFIG}` (`cluster.go:661`).

**Independent of target sshd `AcceptEnv`**: the var is never *sent*
(not merely dropped by the peer) — verified by the env-split + the
backstop scrub. The `ssh.go` `AcceptEnv` comment is referenced in the
new doc comments but the fix does not rely on it.

### Post-fix verify — literal trace

`go test -run 'WorkspaceEnv|RemoteSafeEnv' -count=1 -v ./internal/cli/`:

```
=== RUN   TestWorkspaceEnvCore_OmitsKubeconfig_KeepsIBMCloud
--- PASS: TestWorkspaceEnvCore_OmitsKubeconfig_KeepsIBMCloud (0.01s)
=== RUN   TestWorkspaceEnv_LocalKeepsKubeconfig
--- PASS: TestWorkspaceEnv_LocalKeepsKubeconfig (0.01s)
=== RUN   TestRemoteSafeEnv_StripsLocalPathVars
--- PASS: TestRemoteSafeEnv_StripsLocalPathVars (0.00s)
=== RUN   TestRemoteSafeEnv_NilAndMalformed
--- PASS: TestRemoteSafeEnv_NilAndMalformed (0.00s)
PASS
ok  	github.com/jgruberf5/roksbnkctl/internal/cli	0.110s
```

4/4 PASS. Acceptance-criteria coverage:
`TestWorkspaceEnvCore_OmitsKubeconfig_KeepsIBMCloud` (IBMCLOUD_* present,
KUBECONFIG absent on the --on path),
`TestWorkspaceEnv_LocalKeepsKubeconfig` (local path unchanged),
`TestRemoteSafeEnv_StripsLocalPathVars` /
`_NilAndMalformed` (defense-in-depth backstop).

### Out-of-band live verify — user action, on record

The agent shell cannot drive a live `--on jumphost kubectl` against a
real jumphost (same hand-off shape as Sprint 11 Issue 2 / Sprint 12).
**This was already PERFORMED by the user 2026-05-18 14:54** and is
recorded in `issues/issue_sprint13_staff.md` Issue 1 §"Closure":
`roksbnkctl exec --on jumphost …` returned `KUBECONFIG=[]` on the wire
(pre-fix it carried `/home/jgruber/.kube/config`). The env-split fix is
confirmed correct end-to-end; this issue is **not** reopened. The
residual `localhost:8080` the user still sees is the separate
architect Issue 2 (cloud-init provisioning failure), not this leak —
see validator Issue 6 below.

**Verdict**: env-leak fix VERIFIED (unit + cited user live-verify).

---

## Issue 3: PRD 08 feature-acceptance matrix (read-only `terraform`)

**Severity**: medium (feature gate)
**Status**: resolved

Driven via `internal/cli/terraform_test.go` +
`internal/tf/readonly_test.go` and code read of
`internal/cli/terraform.go` / `internal/tf/terraform.go`.

| Acceptance criterion | Mechanism | Verdict |
|---|---|---|
| Allowlisted (`output`,`show`,`state list`,`state show`,`state pull`,`providers`,`version`,`graph`,`validate`,`fmt -check`) accepted | `terraformReadOnlyTop` + `TestValidateTerraformReadOnly_AllowlistMatrix` accept set (13 cases) | PASS |
| `apply`/`destroy`/`init`/`plan`/`import`/`taint`/`-auto-approve` rejected **before** terraform runs, msg points at lifecycle verbs | `validateTerraformReadOnly` called before `tf.OpenReadOnly`; reject set asserts `err` contains "roksbnkctl up" | PASS |
| Sub-verb guard: `terraform state rm <addr>` rejected though top-level `state` allowlisted | `terraformReadOnlyStateSub` = {list,show,pull}; reject set covers `state rm/mv/replace-provider`, bare `state` | PASS |
| Never-applied workspace → clean "run `roksbnkctl up` first", no source-fetch/`init` side effect, non-zero exit | `tf.OpenReadOnly` stats `<stateDir>/terraform.tfstate`, returns `ErrNoState` before delegating to `Open`; `TestOpenReadOnly_NeverApplied_NoStateNoSideEffects` asserts no `tf-source`/`terraform` dir created | PASS |
| `--on jumphost terraform output` → rejected with workstation-local-state pointer | `runTerraformPassthrough` rejects `on != ""` with "workstation-local"; `TestRunTerraformPassthrough_RejectsOn` | PASS |
| `--phase cluster` routes to `state-cluster/` | `terraformReadOnlyStateDir` → `config.WorkspaceClusterStateDir`; `TestTerraformReadOnlyStateDir_RoutesByPhase` + `TestExtractPhaseFlag` | PASS |
| Help text states read-only; mutations via up/plan/apply/down | `terraformCmd.Long` (terraform.go:31-57) explicit | PASS |

Additional design checks: cwd + `TF_DATA_DIR` reused from `tf.Open`
plumbing — `RunReadOnly` (`terraform.go:381`) runs `cmd.Dir =
w.sourceDir`, `cmd.Env = os.Environ()` (TF_DATA_DIR set by Open); the
CLI layer does **not** re-derive them (the bug class this cycle
addresses). `OpenReadOnly` never calls `Init()`. Registered in
`cluster.go` init() alongside the other passthroughs; alias `tf`.

### Literal trace

`go test -run 'TerraformReadOnly|ExtractPhase|RunTerraformPassthrough|
OpenReadOnly|RunReadOnly' -count=1 -v ./internal/cli/ ./internal/tf/`:

```
--- PASS: TestValidateTerraformReadOnly_AllowlistMatrix (0.00s)
--- PASS: TestExtractPhaseFlag (0.00s)
--- PASS: TestTerraformReadOnlyStateDir_RoutesByPhase (0.00s)
--- PASS: TestRunTerraformPassthrough_RejectsOn (0.00s)
--- PASS: TestOpenReadOnly_NeverApplied_NoStateNoSideEffects (0.00s)
--- PASS: TestOpenReadOnly_NilConfig (0.00s)
--- PASS: TestRunReadOnly_NotOpened (0.00s)
```

7/7 PASS. **PRD 08 acceptance: GREEN.**

---

## Issue 4: PRD 09 feature-acceptance matrix (per-AZ auto-registration)

**Severity**: medium (feature gate)
**Status**: resolved

Driven via `internal/cli/auto_cluster_jumphosts_test.go` + code read
of `tryAutoClusterJumphosts` (`lifecycle.go:595`) and `mapOutput`
(`cluster_phase.go:493`).

| Acceptance criterion | Mechanism | Verdict |
|---|---|---|
| `testing_cluster_jumphost_ips` absent / `[]` / `false` → only `jumphost` seeded, no error, no spurious targets, no warning noise | `mapOutput` returns nil for absent / `[]`-default / `{}` / scalar / bad JSON; `tryAutoClusterJumphosts` silent-returns on `len(fips)==0`; `TestMapOutput_ParseMatrix` (6 skip cases) | PASS |
| Multi-zone map → N `jumphost-<zone>` upserts via idempotent `SetTarget` | per-zone `remote.SetTarget(ws, "jumphost-"+zone, cfg)`; `TestSetTarget_PerZoneUpsertIdempotent` (3 zones → 3 targets; FIP rotation upserts in place, no dup) | PASS |
| key-PEM-missing → skip | `keyPEM := stringOutput(outputs,"jumphost_shared_key")`; `if keyPEM == "" { return }` (no warning noise) | PASS |
| Parse failure → single `warning:`, `up` not failed (parity with `tryAutoJumphost`) | best-effort: `tfws.Output` err → silent return; per-target write err → one `warning:` + continue; never returns/fails `up`; `TestTryAutoClusterJumphosts_GuardsAreNonFatal` | PASS |
| **Option (a) only** — no reconcile/orphan-removal, no `auto:` schema marker | code read: `tryAutoClusterJumphosts` is a pure upsert loop; no prefix-sweep, no `targets.go` reconcile helper, no `config.TargetCfg` schema change; doc comment (lifecycle.go:579-584) states option (a) explicitly | PASS — no scope-creep |

Output-name correctness (architect Issue 4): code reads
`testing_cluster_jumphost_ips` first (lifecycle.go:611), with the
legacy `…_public_ips` as a defensive fallback (lifecycle.go:613).
Matches PRD 09 / CHANGELOG / chapters 15-16. Wired into **all** post-`up`
hook sites that call `tryAutoJumphost` (lifecycle.go:259,272,328,852 —
top-level `up`, trial, cluster up, bnk up).

### Literal trace

```
--- PASS: TestMapOutput_ParseMatrix (0.00s)
--- PASS: TestTryAutoClusterJumphosts_GuardsAreNonFatal (0.00s)
--- PASS: TestSetTarget_PerZoneUpsertIdempotent (0.01s)
```

3/3 PASS. **PRD 09 acceptance: GREEN. Option (a) upsert-only honoured;
no out-of-scope reconcile/orphan-removal code.**

---

## Issue 5: Doc/code lockstep audit

**Severity**: low (doc-coupling consistency)
**Status**: resolved

| Architect claim (chapter / CHANGELOG) | As-landed behaviour | Verdict |
|---|---|---|
| Ch15 §"Per-AZ cluster jumphosts (`jumphost-<zone>`)" — auto-registration since v1.5.0, reads `testing_cluster_jumphost_ips`, target name `jumphost-<zone>`, shared-key reuse | `tryAutoClusterJumphosts` reads `testing_cluster_jumphost_ips`, upserts `jumphost-<zone>` with `KeySource: tf-output:jumphost_shared_key` | match |
| Ch15 orphan caveat (option (a) upsert-only, manual `targets remove`) | code is upsert-only, no prune; matches caveat verbatim | match |
| Ch15 §"What is *not* auto-discovered" — no top-level private-IP output; private-IP hop is a documented technique not a target | confirmed: no `testing_cluster_jumphost_private_ips` top-level output (architect Issue 4) | match |
| Ch15 pre-v1.5.0 fallback uses `roksbnkctl terraform output …` (PRD 08, shipped) with raw-`terraform` only as older-release aside | PRD 08 shipped this cycle; fallback correctly marked pre-v1.5.0 | match |
| Ch16 §"Per-AZ cluster jumphosts" — auto-registered since v1.5.0, first-class `--on`, no hop | matches; `targets list` shows `jumphost` + `jumphost-<zone>` | match |
| Ch16 §"What `--on` doesn't do (yet)" — no longer claims per-AZ jumphosts are *not* auto-registered (line 253 states they *are* since v1.5.0) | no stale "not auto-registered" claim remains | match |
| Ch16 §"Environment passthrough" — KUBECONFIG **not** forwarded; only machine-portable IBMCLOUD_* cross the boundary; pre-v1.5.0 history noted | matches staff Issue 1 as-landed env-split exactly | match |
| CHANGELOG `v1.5.0 §Added` — read-only `terraform` allowlist + per-AZ auto-registration; `§Fixed` — KUBECONFIG leak | match the landed allowlist / hook / env-split | match |
| CHANGELOG `v1.4.1 §Deferred` known-issue note re-pointed `v1.4.2 → v1.5.0` (not deleted) | CHANGELOG.md:44 retains the note, re-points to "Unreleased (v1.5.0) §Fixed" | match |
| CHANGELOG `§Deferred (post-v1.5.0)` carries option (b) reconcile + prior items forward | line 24 adds the option (b) follow-up; lines 25-26 carry v1.4.1/v1.4.0/v1.3.0 deferred items | match |
| Architect Issue 3 (`--tf-source` help) — 2-line diff applied to lifecycle.go:87,90 | both help strings now state relative-path resolution (verified) | match |

No manual-`targets-add`-as-headline drift; the manual path is a clearly
version-gated pre-v1.5.0 aside in both chapters. **Doc/code lockstep:
PASS.**

---

## Issue 6: architect Issue 2 (cloud-init kubeconfig provisioning failure) — documented known-issue carried to Sprint 14

**Severity**: high (but OUT of v1.5.0 scope — not a v1.5.0 regression)
**Status**: accepted (tracked as the Sprint 14 / get-well headline;
NOT a v1.5.0 blocker)

Recorded here for completeness so the carried-forward known-issue is
not lost (analogous to the v1.4.1 §Deferred known-issue pattern). This
is `issues/issue_sprint13_architect.md` Issue 2: the jumphost has no
`/home/ubuntu/.kube/config` because the cloud-init `ibmcloud ks cluster
config --admin || true` provisioning fails silently. Live testing
2026-05-18 14:54 escalated it from low to high.

It is a **separate, independent root cause** from the staff Issue 1
env-leak (which is RESOLVED and live-verified — see validator Issue 2).
It is explicitly OUT of v1.5.0 scope, is NOT introduced or regressed by
any v1.5.0 code, and is tracked as the headline deliverable of the next
(get-well / Sprint 14) cycle. The CHANGELOG and chapters 15/16 honestly
represent the as-shipped behaviour (the v1.5.0 env-leak fix is
described accurately; the cloud-init failure is its own tracked issue,
not conflated). **This does NOT make the v1.5.0 tag RED** — the three
in-scope deliverables stand on their own gates (validator Issues 2/3/4
all GREEN).

---

## Issue 7: Continued analogous-gotcha sweep

**Severity**: low (preventive)
**Status**: resolved — no new findings

`grep -rn "Flags().String.*[Ff]ile\|...[Pp]ath\|StringArrayVar.*[Ff]ile"
internal/cli/` and `grep -n "dispatchRemote(\|workspaceEnv(\|
workspaceEnvCore(\|dispatchRemoteShell(" internal/cli/`:

- **Path-shaped flags**: `--var-file` (fixed v1.4.1), `--tf-source`
  (fixed v1.4.1), `targets add --key-path` (read at command time, same
  CWD — OK), `tfvars -o` (relative to invoking CWD only, no state-dir
  hand-off — OK), `ops install --trusted-profile` (not a path), `init`
  has no `--backend-config`. **No new path-shaped flag introduced this
  cycle**; the Sprint 12 Issue 5 sweep conclusion stands unchanged.
- **SSH-boundary env**: every `dispatchRemote(` call site
  (`cluster.go:119,343,563`) sources `workspaceEnvCore()` — never the
  KUBECONFIG-bearing `workspaceEnv()`. `dispatchRemoteShell`
  (`remote.go:113`) injects no env. `dispatchRemote` itself applies
  `remoteSafeEnv()` as a backstop. `test.go:713` uses `workspaceEnv()`
  but does not dispatch remotely. **No other local-path-valued var
  leaks across the SSH boundary; no other path-shaped flag flows
  verbatim into a different-CWD subprocess.** Nothing suspect to file.

---

## Issue 8: `mdbook build book/` — HTML backend gate

**Severity**: low
**Status**: resolved

**Command**: `PATH="$HOME/.cargo/bin:$PATH" mdbook build book/`

**HTML backend**: exit 0 for the HTML renderer —
`INFO HTML book written to /mnt/c/project/roksbnkctl/book/book/html`.
`book/book/html/15-ssh-targets.html` (50685 bytes) +
`16-on-flag-ssh-jumphosts.html` (45282 bytes) both regenerated this
session (15:13).

New anchors reachable:

- ch15: `id="auto-discovery-from-terraform-outputs"`,
  `id="per-az-cluster-jumphosts-jumphost-zone"`,
  `id="what-is-not-auto-discovered"`
- ch16: `id="per-az-cluster-jumphosts"`

All cross-link targets between the two chapters resolve.

**PRD cross-link grep** (Sprint 11 published-book-404 fix still in
place):

```
$ grep -c 'href="https://github.com/jgruberf5/roksbnkctl/blob/main/docs/prd' \
    book/book/html/15-ssh-targets.html book/book/html/16-on-flag-ssh-jumphosts.html
15-ssh-targets.html:3
16-on-flag-ssh-jumphosts.html:2
$ grep -c 'href="\.\./\.\./docs/prd\|href="\.\./docs/prd' \
    book/book/html/15-ssh-targets.html book/book/html/16-on-flag-ssh-jumphosts.html
15-ssh-targets.html:0
16-on-flag-ssh-jumphosts.html:0
```

5 absolute GitHub PRD URLs, 0 relative `../docs/prd/` paths — Sprint 11
404-fix intact.

**Pandoc backend**: exit 101,
`cannot open /opt/render-mermaid.lua: No such file or directory`.
Identical to Sprint 11 Issue 6 / Sprint 12 Issue 4 — a known
orthogonal host-config issue (hardcoded container lua-filter path
absent on this host), NOT a gate failure. The HTML backend is what
GitHub Pages serves.

**Verdict**: HTML backend gate PASS.

---

## Final report

**Headline**: Sprint 13 closure is **GREEN** for the `v1.5.0` tag,
scoped to the three in-scope deliverables.

- **Seven-step sweep**: build / vet / gofmt / staticcheck /
  integration-build / `internal/remote` integration all clean; `go
  test ./...` green (changed pkgs force-rerun). Lone `internal/exec`
  step-7 failure is a sandbox `mkdir /home/runner/.bluemix` permission
  limit on an *unmodified* package (`git status internal/exec/` empty)
  — host-env, same class as the Sprint 12 kindless/Docker precedent,
  not a v1.5.0 regression. `kind` absent → kind bring-up skipped per
  precedent.
- **Issue-1 (KUBECONFIG leak) fix**: VERIFIED.
  `TestWorkspaceEnvCore_OmitsKubeconfig_KeepsIBMCloud` + 3 siblings →
  4/4 PASS; env-split + `remoteSafeEnv` backstop independent of target
  sshd `AcceptEnv`; cited user live-verify (`KUBECONFIG=[]` on the
  wire, 2026-05-18 14:54) on record. Not reopened.
- **PRD 08** (read-only `terraform`): GREEN — 7/7 acceptance tests
  PASS; allowlist + sub-verb guard + mutation-flag scrub + no-state
  side-effect-free open all confirmed.
- **PRD 09** (per-AZ auto-registration): GREEN — 3/3 tests PASS;
  option (a) upsert-only confirmed, **no out-of-scope
  reconcile/orphan-removal code**; reads `testing_cluster_jumphost_ips`.
- **Doc/code lockstep**: PASS — chapters 15/16 + CHANGELOG match
  as-landed behaviour; `v1.4.1 §Deferred` note re-pointed
  `v1.4.2 → v1.5.0` (not deleted); architect Issue 3 confirmed applied.
- **Analogous-gotcha sweep**: no new findings; no other
  local-path-valued var crosses the SSH boundary, no new path-shaped
  flag.
- **mdbook HTML gate**: PASS (exit 0; chapter 15/16 HTML + new anchors
  render; 5 absolute / 0 relative PRD cross-links). Pandoc backend
  fails on the known orthogonal `/opt/render-mermaid.lua` host issue
  (not a gate).

**Issues filed**: 8 — 1 high in-tree-resolved (Issue 2, live-verify
on record), 1 high OUT-of-scope tracked-to-Sprint-14 (Issue 6,
architect Issue 2 — NOT a v1.5.0 blocker), 3 medium gate (Issues 1/3/4
resolved), 3 low (Issues 5/7/8 resolved).

**Gate verdict for `v1.5.0` tag**: **GREEN**, scoped to the three
in-scope deliverables (KUBECONFIG-leak fix + read-only `terraform` /
PRD 08 + per-AZ auto-registration / PRD 09), conditional only on the
already-performed user out-of-band live verify. Architect Issue 2
(cloud-init kubeconfig provisioning failure) is the tracked Sprint 14
get-well headline, NOT a v1.5.0 blocker — v1.5.0 correctly ships the
env-leak fix + read-only `terraform` + per-AZ auto-registration and is
neither coupled to nor regressed by that separate root cause.

---

## Sprint 13 ledger closeout — `v1.5.0` shipped 2026-05-18

**Status: CLOSED.** All 8 issues terminal — Issues 1–5,7,8 resolved; Issue 6 accepted → carried to Sprint 14 and resolved there.

`v1.5.0` is cut, released, and published:

- **Tag:** annotated `v1.5.0` on `5113b74` (the `chore: prep v1.5.0 release` commit — matches the `v1.4.1`/`v1.3.0`/`v1.2.1` tag-placement convention).
- **CI gate green pre-tag:** `ci.yml` (vet/fmt/staticcheck/test, ubuntu+macos, goreleaser-check) ✅; `book.yml` (`mdbook build`) ✅ on the book-touching commit `d6c8bf8`; plus the integrator's live `roksbnkctl --on jumphost kubectl` verify 2026-05-18 16:33 (self-healed attempt 1, `localhost:8080` gone, exit 0, no redeploy).
- **GitHub Release:** live, not draft — 8 assets (6 platform archives + `checksums.txt` + `roksbnkctl-book-v1.5.0.pdf`); `release.yml`/goreleaser run completed success.
- **Book:** HTML → `gh-pages` live at <https://jgruberf5.github.io/roksbnkctl/book/> (HTTP 200); PDF attached to the Release.
- **Release notes:** curated `v1.5.0` announcement published (headline = the end-to-end `--on jumphost` fix; Fixed / Added / Install).

All Sprint 13 + Sprint 14 gate criteria met (Sprint 13 §"Gate to `v1.5.0`" + Sprint 14 §"Gate to the (finally tag-ready) `v1.5.0`"). The post-v1.4.0 jumphost thread is closed end-to-end. The only forward items are the explicitly-tracked post-`v1.5.0` follow-ups: per-AZ stale-target reconcile option (b), and the path/env chokepoint + `cli` consolidation (`docs/PLAN.md` §"Sprint 15"). **Ledger closed 2026-05-18.**
