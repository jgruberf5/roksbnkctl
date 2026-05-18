# Sprint 12 — staff issues (carry-in from v1.4.0 user testing)

Format: one issue per finding. `Severity: low | medium | high | blocker`.
`Status: open | in-progress | resolved | wontfix`.

Findings surfaced after Sprint 11 closed and v1.4.0 prep landed on `main`
(commit `6725db1`). Pre-seeded here so the Sprint 12 dispatch picks
them up as kickoff context.

---

## Issue 1: `--var-file` relative paths don't resolve against the shell CWD

**Severity**: medium
**Status**: resolved

### Symptom

```
$ roksbnkctl up --var-file=./terraform.tfvars --auto
…
Error: Failed to read variables file

Given variables file ./terraform.tfvars does not exist.
```

The user invoked `up` from a directory that contains a `terraform.tfvars`
file, expecting `./terraform.tfvars` to resolve against their shell CWD
(the way terraform's own `-var-file=./...` does when terraform is invoked
directly). The path is passed through verbatim, and terraform resolves
it against its own working directory — the per-phase state dir
(`~/.roksbnkctl/<workspace>/state[-cluster]/`) — where no such file
exists.

### Root cause

`flagVarFiles` (defined at `internal/cli/lifecycle.go:42`) is the
repeatable `--var-file` flag shared across `up` / `plan` / `apply` /
`down` / `cluster up`-`down` / `bnk up`-`down`. The user's flag values
flow through to terraform via:

- **Local backend**: `tfws.Plan(ctx, flagVarFiles...)` and
  `applyWithRetry(ctx, tfws, flagVarFiles)` at
  `internal/cli/lifecycle.go:178, 198, 222, 243, 319`. The strings are
  appended to the terraform command line as `-var-file=<vf>` with no
  normalization. Terraform runs with `CWD = stateDir` (the per-phase
  state directory), so relative paths resolve there.

- **Docker backend** (`internal/cli/lifecycle.go:712-721`): already
  rejects non-absolute paths with a clear error:
  *"--var-file %q must be absolute when --backend docker (paths are
  projected into the container at the same location); use absolute
  paths or run with --backend local"*. So the docker path is correct
  about *requiring* absolute, but the user-friendly fix below removes
  the surprise on the local backend AND makes the docker-backend reject
  redundant for the common case.

### Proposed fix

Normalize `--var-file` entries to absolute paths against the invocation
CWD (`os.Getwd()`) before they reach either backend. One small helper,
called from the `flagVarFiles` ingestion point, walks the slice:

```go
func resolveVarFiles(vfs []string) ([]string, error) {
    cwd, err := os.Getwd()
    if err != nil {
        return nil, fmt.Errorf("resolve --var-file: %w", err)
    }
    out := make([]string, len(vfs))
    for i, vf := range vfs {
        if filepath.IsAbs(vf) {
            out[i] = filepath.Clean(vf)
            continue
        }
        abs := filepath.Join(cwd, vf)
        if _, err := os.Stat(abs); err != nil {
            return nil, fmt.Errorf("--var-file %q (resolved to %q): %w", vf, abs, err)
        }
        out[i] = abs
    }
    return out, nil
}
```

Call it once per command — earliest convenient spot is the top of each
RunE in `lifecycle.go` (or a shared `preRun` helper if one already
exists for these commands). Failing the `os.Stat` early surfaces a
clearer error than terraform's "Given variables file … does not exist"
because we can name both the user-supplied path *and* the resolved
absolute, making typos vs. wrong-CWD distinguishable.

Once normalization happens early, the docker-backend reject at
`lifecycle.go:718-720` becomes belt-and-suspenders (every reachable
path is already absolute) — leave it in place as a defensive guard or
delete it; either is fine.

### Files affected

- `internal/cli/lifecycle.go` — `flagVarFiles` declaration (line 42),
  the four `tfws.Plan` / `applyWithRetry` / `tfws.Destroy` call sites
  (lines 178, 198, 222, 243, 319), and the docker-backend loop at
  712-721.
- `internal/cli/cluster_phase.go` — same `flagVarFiles` slice used at
  line 278 (`varFiles := append([]string{}, flagVarFiles...)`).
- `internal/cli/bnk_phase.go` — same pattern at the `bnk up`/`down`
  surface (lines 66, 68 register the flag for that command group).

### Acceptance criteria

- `roksbnkctl up --var-file=./terraform.tfvars --auto` from a directory
  containing `terraform.tfvars` succeeds (path resolves against shell
  CWD). Same for `cluster up` / `bnk up` / `plan` / `apply` / `down`.
- `roksbnkctl up --var-file=../shared.tfvars --auto` from a sibling
  directory of the user's project also resolves correctly.
- `roksbnkctl up --var-file=./missing.tfvars --auto` errors *before*
  terraform runs, with a message naming both the user-supplied path
  and the resolved absolute path.
- Existing absolute-path callers (`--var-file=/abs/path/foo.tfvars`)
  continue to work unchanged.
- The docker backend continues to project absolute paths via the
  container fixture mount as today.
- New unit test in `internal/cli/lifecycle_test.go` covering: absolute
  pass-through, relative-resolved-against-CWD, missing-file error
  message, `~`-expansion if the project handles that elsewhere (verify
  whether `filepath.Abs` is sufficient or whether `os.ExpandEnv` /
  manual `~` handling is needed — quick `grep -rn '"~/' internal/cli/`
  to check existing convention).

### Reproduce

```bash
cd /tmp && mkdir vfrepro && cd vfrepro
cat > terraform.tfvars <<'EOF'
worker_count = 6
EOF
roksbnkctl -w <existing-workspace> cluster up --var-file=./terraform.tfvars --auto
# expected: terraform consumes worker_count = 6 from the local file
# actual:   "Error: Failed to read variables file. Given variables file ./terraform.tfvars does not exist."
```

### Related

- Validator Issue 2 (Sprint 11, `issues/issue_sprint11_validator.md`) —
  the out-of-band live `cluster up` against `canada-roks` is exactly
  the flow that surfaced this.
- The same shell-CWD-vs-state-dir gotcha bites any other path-shaped
  flag that flows verbatim to terraform; sweep for analogous flags
  during the fix (e.g., `--backend-config=<path>` if `init` exposes
  it, plan/apply file targets, etc.).

### Out of scope for this fix

- `~`-expansion conventions outside `--var-file` (separate sweep if
  needed).
- Docker-backend path-projection rework — pre-resolving to absolute is
  enough for v1.4.x; the deeper rework (bind-mount each parent so
  arbitrary host paths flow into the container) stays deferred per the
  existing comment at `lifecycle.go:714-717`.

### Closure

**Helper location & signature**

`internal/cli/lifecycle.go` (just above the lifecycle implementations,
before `runUp`):

```go
func resolveVarFiles(vfs []string) ([]string, error)
```

Kept in `lifecycle.go` because it's the most natural home — that file
owns `flagVarFiles` and all but two consumer RunEs. The cluster/bnk
phase RunEs import it implicitly via the shared `cli` package. No new
file or package was warranted for one helper.

**Wire-up pattern**

Per-RunE normalization at the top of every entry point that consumes
`flagVarFiles` — composites *and* leaves — with the slice reassigned
back into `flagVarFiles` after resolution. Idempotent on already-
absolute slices, so the composite's pass and the leaf's pass don't
fight when `roksbnkctl up` calls into `runClusterUp` → `runTrialUp`.

Sites wired:

- `runUp` (composite) — `internal/cli/lifecycle.go`
- `runTrialUp` (leaf for `up` / `bnk up`)
- `runPlan`
- `runApply`
- `runDown` (composite)
- `runTrialDown` (leaf for `down` / `bnk down`)
- `runClusterUp` — `internal/cli/cluster_phase.go`
- `runClusterDown`
- `runBnkUp` — `internal/cli/bnk_phase.go`
- `runBnkDown`

All five `tfws.Plan` / `applyWithRetry` / `tfws.Destroy` call sites the
issue listed (`lifecycle.go:178, 198, 222, 243, 319` in the original
file) now receive the already-absolute slice via the normalized
package-level `flagVarFiles`. The docker-backend reject loop at
`lifecycle.go:712-721` stays in place as a defensive guard — every
reachable path is now absolute by the time control gets there, but the
reject still catches a programming-error regression cheaply.

**Unit tests** — `internal/cli/lifecycle_test.go` (new file)

Five tests, all passing:

- `TestResolveVarFiles_AbsolutePassThrough` — absolute paths
  round-trip cleaned but otherwise unchanged.
- `TestResolveVarFiles_RelativeResolvedAgainstCWD` — `./foo.tfvars`
  from a `t.TempDir()` CWD resolves to `<tmp>/foo.tfvars`
  (EvalSymlinks-normalized to survive macOS `/var → /private/var`).
- `TestResolveVarFiles_MissingFileErrorNamesBoth` — error message
  contains both the user-supplied input and `"resolved to"` plus the
  filename component of the resolved abs.
- `TestResolveVarFiles_TildeExpansion` — `~/<rel>` expands to
  `<home>/<rel>`. Skipped on Windows.
- `TestResolveVarFiles_EmptyInput` — `nil` / `[]string{}` are no-op
  (no `os.Getwd` call, no error). Important because every RunE calls
  the helper unconditionally.

**`~`-expansion convention found elsewhere**

`grep -rn '"~/' internal/cli/` surfaced `internal/cli/install.go:76`,
which handles `--dir=~/...` for `roksbnkctl install` by checking
`destDir == "~"` and `strings.HasPrefix(destDir, "~/")` then joining
with `os.UserHomeDir()`. `resolveVarFiles` mirrors that pattern
exactly so the two surfaces stay consistent. The `TestTildeExpansion`
test pins that compatibility.

**Build / test sweep**

- `go build ./...` — clean
- `go vet ./...` — clean
- `gofmt -l .` — empty
- `go test ./internal/cli/...` — PASS (existing tests unaffected;
  five new tests green)
- `go test ./...` — PASS across the whole module
- `make staticcheck` — clean (exit 0)

**Surface oddities**

The composite RunEs (`runUp`, `runDown`) and the leaf RunEs
(`runClusterUp`, `runTrialUp`, `runTrialDown`, `runClusterDown`,
`runBnkUp`, `runBnkDown`) are both reachable directly through cobra
dispatch — there's no single `preRun` chain that all paths fall
through. Per-RunE normalization is the smallest correct surface; a
shared `preRun` hook would have required a deeper refactor of how the
flag flows through the phase commands and was explicitly out of scope.
The idempotence of `resolveVarFiles` on absolute inputs makes the
duplication safe.

---

## Issue 2: `--tf-source` local relative paths persist unresolved into config.yaml

**Severity**: low
**Status**: resolved

(Pulled into Sprint 12 by integrator decision — originally deferred to
Sprint 13 as validator Issue 5. Same shell-CWD-vs-state-dir class as
Issue 1, surfaced by the analogous-gotcha sweep.)

### Description

`--tf-source` (`internal/cli/lifecycle.go:86,89`, registered on `init`
and `up`, help text "override TF source (path or URL)") accepts a
local-path override. A local value flows verbatim into
`config.TFSourceCfg{Type: "local", Path: flagTFSource}` and is
**persisted into config.yaml** at the two `init.go` build sites
(`runUpgradeTF` and `promptTFSource`). It's later consumed by
`internal/tf/fetch.go` `FetchSource` `case "local"`, which `os.Stat`s
`src.Path` and hands it to terraform unmodified.

A relative `--tf-source=./mytf` passes the `os.Stat` at `init` time
(CWD = shell PWD), is persisted *relative* into config.yaml, and is
later handed to terraform whose effective CWD is the per-phase state dir
(`~/.roksbnkctl/<workspace>/state[-cluster]/`). Worse than the
`--var-file` case: it survives into config.yaml and detonates on a
*later* `up`/`plan`/`apply`, not the same invocation.

### Root cause

No normalization between the `--tf-source` ingestion in `init.go` and
the persisted `config.TFSourceCfg.Path`. The same shell-CWD-vs-state-dir
trap Issue 1 fixed for `--var-file`, but it's pinned into config.yaml so
it also bites configs written before any fix.

### Proposed fix

Two layers, both landed:

1. **Init-time normalization** — a small `resolveLocalTFSource(path
   string) (string, error)` helper in `internal/cli/init.go` (placed
   beside `looksLikeGitHubRepo`, the existing `--tf-source`
   type-detection sibling), called at *both*
   `config.TFSourceCfg{Type: "local", Path: …}` build sites
   (`runUpgradeTF` ~line 209, `promptTFSource` ~line 265). Mirrors
   `resolveVarFiles` conventions exactly: empty-input short-circuit,
   `~`/`~/` expansion via `os.UserHomeDir` (the `install.go`
   convention), absolute pass-through via `filepath.Clean`, relative
   absolutized via `filepath.Abs`, error-wrapped
   `resolve --tf-source %q: %w`.

   The validator's draft used a hypothetical `isURLish(src)` guard.
   That is unnecessary at these sites: the embedded/github forms are
   already split off *upstream* (the `"embedded"` literal check and
   `looksLikeGitHubRepo` in `promptTFSource`; the `--upgrade-tf`
   branching in `runUpgradeTF`). By the time control reaches a
   `Type: "local"` build site the value is unambiguously a local path,
   so no URL/owner-repo input ever reaches the helper. No new
   type-detection helper invented — the existing mechanism is reused.

2. **`FetchSource` self-heal** — `internal/tf/fetch.go` `case "local"`
   now `filepath.Abs`-normalizes a non-absolute `src.Path` before the
   `os.Stat`/dir checks, so config.yaml files written *before* layer 1
   self-heal on the next `up`/`plan`/`apply`. Idempotent: layer 1 pins
   absolute for freshly-written configs, so this is a no-op for them.

### Acceptance criteria

- Relative `--tf-source` local path → persisted **absolute** in the
  resulting `config.TFSourceCfg.Path`.
- Absolute `--tf-source` local path → unchanged (cleaned).
- github "owner/repo" / URL form → never routed through the helper (no
  `filepath.Abs` applied); structurally untouched if it ever were.
- Pre-existing relative config.yaml `local` Path → self-heals to
  absolute at `FetchSource` time.
- `go build ./...` clean; `go test ./internal/cli/... ./internal/tf/...`
  green; `gofmt`/`go vet` clean.

### Closure

**Helper**: `internal/cli/init.go::resolveLocalTFSource(path string)
(string, error)` — beside `looksLikeGitHubRepo`. Wired at both local
build sites in `runUpgradeTF` and `promptTFSource`. **Self-heal**:
`internal/tf/fetch.go` `FetchSource` `case "local"` absolutizes a
relative `src.Path` (error-wrapping kept as the existing
`local TF source %s: %w` form).

**Tests** — all PASS:

- `internal/cli/lifecycle_test.go` (same package/file as the
  `resolveVarFiles` suite, keeping the `--tf-source` analogue beside
  its `--var-file` sibling):
  `TestResolveLocalTFSource_RelativeResolvedToAbs`,
  `TestResolveLocalTFSource_AbsolutePassThrough`,
  `TestResolveLocalTFSource_EmptyInput`,
  `TestResolveLocalTFSource_GitHubFormUntouched`.
- `internal/tf/fetch_test.go`:
  `TestFetchSource_Local_RelativePathSelfHeals`.

5 new tests, 5/5 PASS (`go test -run
'ResolveLocalTFSource|FetchSource_Local_RelativePathSelfHeals'
-count=1 -v ./internal/cli/ ./internal/tf/`). Full
`go test ./internal/cli/... ./internal/tf/...` green; `go build ./...`,
`gofmt -l`, `go vet` all clean.

---

## Issue 3: local `KUBECONFIG` filesystem path leaks into the `--on <target>` remote environment

**Severity**: high
**Status**: deferred → v1.4.2 fast-follow (integrator decision 2026-05-18)

**Integrator triage (2026-05-18)**: surfaced by live v1.4.0 user
testing *after* v1.4.1's two path-resolution fixes (Issues 1 + 2) were
already committed and gate-green. High-severity and same
"path-crosses-a-boundary" family, but unrelated to and not regressed
by v1.4.1's code. Decision: ship v1.4.1 with Issues 1 + 2; do **not**
block the tag on this. Issue 3 is the headline of an immediate v1.4.2
fast-follow (Sprint 13 dispatch). Disclosed as a known issue in
CHANGELOG `## v1.4.1 — 2026-05-18` §"Deferred".

(Surfaced post-v1.4.0 by user testing, immediately after the Issue 1
`--var-file` flow. Same "a path correct for the local CWD is wrong once
it crosses a machine boundary" family as Issues 1 + 2 — here the boundary
is local-host → SSH target rather than shell-CWD → state-dir.)

### Symptom

```
$ roksbnkctl up --var-file terraform.tfvars --auto      # succeeds
$ roksbnkctl --on jumphost kubectl get pods
Add 163.66.81.28's key (SHA256:…) to /home/jgruber/.roksbnkctl/known_hosts? [y/N]: y
E0518 12:11:22.785133  12372 memcache.go:265] "Unhandled Error" err="couldn't get current server API group list: Get \"http://localhost:8080/api?timeout=32s\": dial tcp 127.0.0.1:8080: connect: connection refused"
… (repeated) …
The connection to the server localhost:8080 was refused - did you specify the right host or port?
```

`localhost:8080` is kubectl's hard-coded fallback when it cannot load a
usable kubeconfig — i.e. on the jumphost kubectl ran with **no valid
kubeconfig**, even though the jumphost cloud-init provisions one at
`/home/ubuntu/.kube/config`.

### Root cause

`runPassthrough` (`internal/cli/cluster.go:539-556`) builds the child
environment with `workspaceEnv()` and forwards the **same** slice into
both the local exec path (`runWithEnv`, line 555) **and** the remote
path (`dispatchRemote(..., env, ...)`, line 549).

`workspaceEnv()` (`internal/cli/cluster.go:566-598`) is composed for
**local** execution. At lines 590-592:

```go
if path := k8s.DefaultKubeconfigPath(); path != "" {
    env = append(env, "KUBECONFIG="+path)
}
```

`k8s.DefaultKubeconfigPath()` (`internal/k8s/client.go:74-92`) returns
the first existing path in the **local** host's lookup chain
(`$KUBECONFIG` entries, then `~/.kube/config`). A successful
`roksbnkctl up` writes the admin kubeconfig to the local
`~/.kube/config` at mode 0600 (documented behavior —
`book/src/03-what-roksbnkctl-does.md:28`,
`book/src/07-quick-start.md:91`). So **after any successful local
`up`**, `DefaultKubeconfigPath()` returns the caller's local path (e.g.
`/home/jgruber/.kube/config`) and `KUBECONFIG=/home/jgruber/.kube/config`
is appended.

That env slice flows verbatim to the remote:
`dispatchRemote` → `client.Run(ctx, argv, RunOpts{Env: envExtra})`
(`internal/cli/remote.go:81-87`) → per-var `sess.Setenv()`
(`internal/remote/ssh.go:171-179`).

`IBMCLOUD_API_KEY` / `IC_API_KEY` / `IBMCLOUD_REGION` /
`IBMCLOUD_VERSION_CHECK` are **values** — forwarding them to the target
is correct and intended. `KUBECONFIG` is categorically different: its
value is a **local filesystem path** that is meaningless on the SSH
target (jumphost user `ubuntu`, home `/home/ubuntu`;
`/home/jgruber/.kube/config` does not exist there). When the target's
sshd honors the var, kubectl is pointed at a nonexistent file → falls
back to `localhost:8080`, **and shadows the working
`/home/ubuntu/.kube/config`** that cloud-init provisions
(`terraform/modules/testing/main.tf:98-104`). Net effect: a successful
local `up` deterministically breaks every subsequent
`--on <target> kubectl|oc` until a kubeconfig path that happens to be
valid on *both* machines exists (rare) or `KUBECONFIG` is unset.

This breaks the advertised flow in
`book/src/07-quick-start.md:222` / Chapter 16 (`--on jumphost` for
passthrough `kubectl`/`oc`/`ibmcloud` from inside the cluster network).

### Why it's `high`, not `medium`

- Deterministic regression on the documented happy path: `up` then
  `--on jumphost kubectl`/`oc` is the canonical private-cluster
  workflow (`book/src/09-registering-existing-cluster.md:208`).
- Silent: the user sees a kube API connection error, not a roksbnkctl
  diagnostic — the local→remote env leak is invisible without reading
  the code.
- Failure-mode coupling: it *masks* the cloud-init-provisioned
  `/home/ubuntu/.kube/config`, so even a fully-booted, correctly
  provisioned jumphost still fails.

### Proposed fix

The env that crosses the SSH boundary must not carry local-only
filesystem paths. Two layers:

1. **Strip `KUBECONFIG` (and any other local-path-valued vars) from the
   env handed to `dispatchRemote`.** Smallest correct surface: have
   `runPassthrough` pass a remote-sanitized copy on the `on != ""`
   branch (line 549) while the local branch (line 555) keeps the full
   `env`. Either filter at the call site or split `workspaceEnv()` into
   a value-only core (`IBMCLOUD_*`) + a local-only addendum
   (`KUBECONFIG`) and only forward the core remotely. `runExec`
   (`cluster.go:100-124`) and any other `dispatchRemote` caller that
   sources `workspaceEnv()` need the same treatment — sweep all
   `dispatchRemote(` call sites.

   With `KUBECONFIG` absent, kubectl/oc on the target fall back to the
   target user's `~/.kube/config` (`/home/ubuntu/.kube/config`), which
   cloud-init provisions — the correct behavior.

2. **(Optional, follow-up) Remote kubeconfig remap.** If a future
   feature wants `--on` to use a *specific* remote kubeconfig, that
   must be a path valid **on the target**, never the inherited local
   one. Out of scope for the v1.4.1 patch unless trivially co-located
   with layer 1; layer 1 alone restores correctness.

Note the existing comment at `internal/remote/ssh.go:176-179` — many
sshd configs reject `Setenv` unless `AcceptEnv` matches, so on a stock
Ubuntu jumphost the leak may be intermittent (depends on the target's
`AcceptEnv`). The fix must not *rely* on sshd rejecting `KUBECONFIG`;
correctness comes from never sending a local path, not from hoping the
peer drops it.

### Files affected

- `internal/cli/cluster.go` — `runPassthrough` (539-556),
  `workspaceEnv` (566-598; specifically the `KUBECONFIG` append at
  590-592), `runExec` / any other `dispatchRemote` caller.
- `internal/cli/remote.go` — `dispatchRemote` (42-96): document/enforce
  that `envExtra` must be machine-portable (values, not local paths);
  consider sanitizing here as a defense-in-depth backstop so every
  caller is covered.
- Tests: `internal/cli/` — assert the remote-dispatch env contains the
  `IBMCLOUD_*` vars and **not** `KUBECONFIG`; assert the local
  passthrough env still contains `KUBECONFIG`.

### Acceptance criteria

- After a successful local `roksbnkctl up` (which writes local
  `~/.kube/config`), `roksbnkctl --on jumphost kubectl get pods`
  succeeds against the cluster (uses the target's
  `/home/ubuntu/.kube/config`), with no `localhost:8080` fallback.
- Local `roksbnkctl kubectl get pods` (no `--on`) is unchanged —
  still resolves `KUBECONFIG` via the local chain.
- `--on <target>` for `oc` and `ibmcloud` passthroughs likewise no
  longer inherit the local `KUBECONFIG` path; `IBMCLOUD_API_KEY` /
  `IC_API_KEY` / `IBMCLOUD_REGION` still forward.
- Behavior is independent of the target sshd's `AcceptEnv` (the var is
  never sent, so it cannot leak even where `AcceptEnv KUBECONFIG`).
- `go build ./...`, `go vet ./...`, `gofmt -l .`, `make staticcheck`,
  `go test ./...` all clean/green.

### Reproduce

```bash
roksbnkctl up --var-file terraform.tfvars --auto      # writes local ~/.kube/config
roksbnkctl --on jumphost kubectl get pods
# actual:   E… "http://localhost:8080/api…: connect: connection refused"
#           The connection to the server localhost:8080 was refused
# expected: pods listed (jumphost uses /home/ubuntu/.kube/config)
```

### Related

- Sibling of Issues 1 + 2 (same "local-context path is wrong across a
  boundary" class; here the boundary is host→SSH-target).
- Contributing fragility (architect/infra surface, file separately if
  pursued): cloud-init writes `/home/ubuntu/.kube/config` via
  `ibmcloud ks cluster config --admin` guarded by `|| true` and runs
  asynchronously (`terraform/modules/testing/main.tf:80-104`), so a
  freshly-booted jumphost can also lack a kubeconfig transiently —
  independent of this env-leak but produces the same `localhost:8080`
  symptom. Layer-1 fix is necessary but, on its own, still subject to
  this boot-timing race; cross-reference when scoping
  `issues/issue_sprint12_architect.md`.
- Surfaces against the documented `--on jumphost` private-cluster
  workflow (`book/src/07-quick-start.md:222`, Chapter 16,
  `book/src/09-registering-existing-cluster.md:208`).

### Out of scope for this fix

- The cloud-init boot-timing race (separate architect/infra issue;
  hardening `ibmcloud ks cluster config --admin` retry / readiness
  gating is its own change).
- Remote-kubeconfig remap as a *feature* (layer 2) — only land if
  trivially co-located with layer 1.
- Generalized "which env vars are machine-portable" policy beyond the
  known set (`KUBECONFIG` is the only local-path-valued var
  `workspaceEnv` currently emits; revisit if more are added).

---

## Issue 4: no read-only `terraform` escape hatch — feature request

**Severity**: low (ergonomic enhancement, not a defect)
**Status**: deferred → post-v1.4.1 backlog (integrator triage 2026-05-18)

> **Scope note (read first).** This is a *feature*, filed into the
> Sprint 12 ledger at user request ("add it to the next sprint"). The
> Sprint 12 cycle is a strict bugfix-only patch (`v1.4.1`) — see
> `issues/issue_sprint12_architect.md` Issue 1 and `docs/PLAN.md:854-858`
> ("No new PRDs; … still patch-scope"). A new user-facing subcommand
> does **not** fit a patch release. Recommendation: schedule for the
> next *minor* (`v1.5.0` / Sprint 13) unless the integrator explicitly
> decides to pull it forward. Logged here so it isn't lost; the
> integrator owns the accept/defer call. Suggested status once triaged:
> `accepted` (defer to Sprint 13) or `wontfix` (close as
> doc-it-instead).

### Motivation

roksbnkctl drives terraform; it does not wrap it. The lifecycle verbs
(`up` / `plan` / `apply` / `down`, plus phase-scoped `cluster`/`bnk`
up/down) are the *mutating* terraform interface and must stay the only
mutation path — running `apply`/`destroy` outside the orchestration
skips the rendered `terraform.tfvars`, the apply-retry wrapper, the
post-apply kubeconfig fetch, the `terraform.applied.tfvars` snapshot,
and the auto-jumphost seeding, and desyncs the managed state.

But there is currently **no** supported way to run *read-only*
terraform against a workspace's managed state. Real cases that hit this
in user testing:

- Looking up the per-zone cluster-jumphost IPs (`terraform output
  testing_cluster_jumphost_ssh_commands`) after discovering only the
  TGW `jumphost` target auto-registers (this session's thread —
  `tryAutoJumphost`, `internal/cli/lifecycle.go:540-565`, only seeds
  the singular TGW jumphost; the per-zone outputs exist but aren't
  surfaced).
- Inspecting state (`terraform state list`, `terraform show`) for
  debugging a partial apply.
- Confirming provider/version (`terraform version`,
  `terraform providers`).

Today the only workaround is the undocumented, fragile
`cd ~/.roksbnkctl/<ws>/state[-cluster] && TF_DATA_DIR=$PWD/terraform
terraform output` — which leaks internal layout, is easy to point at
the wrong phase dir, and one fat-fingered `apply`/`state rm` away from
corrupting managed state. A gated escape hatch removes the foot-gun
*and* the layout leak.

### Proposed feature

A new passthrough-style subcommand, **read-only by allowlist**:

```
roksbnkctl terraform <subcommand> [args...]      # default phase (state/)
roksbnkctl --phase cluster terraform output ...  # state-cluster/
roksbnkctl terraform output testing_cluster_jumphost_ssh_commands
roksbnkctl terraform state list
roksbnkctl terraform show
roksbnkctl terraform version
```

(`tf` as an alias is fine — cobra `Aliases: []string{"tf"}`.)

**Hard requirements**

1. **Allowlist, not denylist.** Only an explicit set of read-only
   subcommands is permitted; everything else is rejected before
   terraform is invoked. Proposed allowlist:
   `output`, `show`, `state list`, `state show`, `providers`,
   `version`, `graph`, `validate`, `fmt -check`, `state pull`.
   Anything not in the set → error:
   *"`roksbnkctl terraform` is read-only; `<sub>` can mutate state.
   Use `roksbnkctl up`/`plan`/`apply`/`down` (or `cluster`/`bnk`
   up/down) for changes."*
2. **Mutation-flag scrub even on allowlisted subs.** Reject
   `-auto-approve`, `-destroy`, `-replace=`, `-target=` *on mutating
   contexts*, `state rm`/`state mv`/`state replace-provider`/`import`/
   `taint`/`untaint`/`apply`/`destroy`/`init`/`plan -out` — i.e. the
   allowlist gates the *subcommand*, and a second guard rejects
   subcommands like `state` whose first arg is a mutating verb
   (`state rm` must not slip through a permitted top-level `state`).
   Implement as: permitted = `{subcommand}` ∪ (for `state`)
   `{state list, state show, state pull}` only.
3. **Phase-correct cwd + env, reusing existing plumbing.** Resolve the
   state dir exactly as the lifecycle does —
   `config.WorkspaceStateDir` (default) /
   `config.WorkspaceClusterStateDir` (`--phase cluster`) — then go
   through `tf.Open(ctx, name, wsCfg, stateDir, apiKey, …)`
   (`internal/tf/terraform.go:39`) so the run gets the same
   `sourceDir` cwd (`<stateDir>/tf-source`), the `TF_DATA_DIR`
   side-effect (`terraform.go:135`), and the configured
   `tfexec.Terraform` (`terraform.go:114`). **Do not** re-implement
   path/env setup at the CLI layer — that's the class of bug Issues
   1-3 are about.
4. **No source re-fetch / no state mutation as a side effect.** `tf.Open`
   currently fetches the TF source into `<stateDir>/tf-source` and can
   run `init`. A read-only invocation must not trigger a fetch or
   `init` if the workspace was never applied — fail with a clear
   *"workspace has no terraform state for phase <p>; run `roksbnkctl
   up` first"* instead. Verify whether `tf.Open` is side-effect-safe
   for a never-applied workspace; if not, add a lighter
   `tf.OpenReadOnly` (or a `tf.Open` option) that skips fetch/init and
   only prepares cwd + `TF_DATA_DIR`.
5. **`DisableFlagParsing: true`** like the other passthroughs
   (`internal/cli/cluster.go:54-72`) so terraform's own flags reach
   terraform; reuse the `extractOnFlag`-style manual parse
   (`cluster.go:165`) for `--phase` / `-w`. Note `--on <target>` is
   **out of scope** (and arguably nonsensical here — the managed state
   lives on the local workstation, not the jumphost); explicitly
   reject `--on` with a pointer to the lifecycle verbs.

**Suggested implementation shape**

- `internal/cli/terraform.go` (new): `terraformCmd` cobra command +
  `runTerraformPassthrough`, mirroring the `kubectl`/`oc` structure in
  `cluster.go` but routing through a new read-only runner instead of
  `runPassthrough` (no SSH dispatch).
- `internal/tf/terraform.go` (new exported method):
  `func (w *Workspace) RunReadOnly(ctx context.Context, argv []string)
  (stdout string, err error)` — argv[0] validated against the
  allowlist by the *caller* (CLI layer owns the policy message), `tf`
  package owns only the safe exec (cwd=`w.sourceDir`, env carrying the
  already-set `TF_DATA_DIR`, stdout/stderr wired through). Prefer
  shelling the prepared `tfBin` over `tfexec`'s typed methods so the
  allowlist can cover `state list`/`graph`/`providers` uniformly.
- Register in `cluster.go` `init()` alongside the existing
  `rootCmd.AddCommand(… kubectlCmd, ocCmd, ibmcloudCmd)`.

### Acceptance criteria

- `roksbnkctl terraform output testing_cluster_jumphost_ssh_commands`
  prints the per-zone map from the default-phase state without the
  user touching `~/.roksbnkctl/...` or `TF_DATA_DIR`.
- `roksbnkctl --phase cluster terraform state list` runs against
  `state-cluster/`.
- `roksbnkctl terraform apply` (and `destroy`, `init`, `state rm`,
  `import`, `taint`, `-auto-approve` anywhere) is **rejected before
  terraform runs**, with the message pointing at the lifecycle verbs.
- `roksbnkctl terraform state rm <addr>` is rejected even though
  top-level `state` is allowlisted (sub-verb guard).
- Against a never-applied workspace phase: clear
  "no state for phase; run `roksbnkctl up` first" error, **no** source
  fetch / `init` side effect, non-zero exit.
- `roksbnkctl --on jumphost terraform output` → rejected with a
  pointer explaining state is local-only.
- Help text states plainly: read-only; mutations go through
  `up`/`plan`/`apply`/`down`.
- `go build ./...`, `go vet ./...`, `gofmt -l .`, `make staticcheck`,
  `go test ./...` clean/green; new unit tests cover the allowlist
  accept/reject matrix and the `state <mutating-subverb>` guard.

### Files affected

- `internal/cli/terraform.go` — new command + read-only policy.
- `internal/cli/cluster.go` — register the command in `init()`.
- `internal/tf/terraform.go` — new `RunReadOnly` (and possibly
  `OpenReadOnly` / a side-effect-free open path for never-applied
  workspaces).
- `internal/cli/<phase resolution>` — reuse
  `config.WorkspaceStateDir` / `WorkspaceClusterStateDir`; wire a
  `--phase` selector if one doesn't already exist for non-lifecycle
  commands (check before adding — `cluster_phase.go:261`,
  `lifecycle.go:440` show the existing resolution).
- Docs: new short section in `book/src/` (the chapter that covers
  passthroughs / execution backends — `book/src/17-execution-backends.md`
  or the passthrough chapter) + a `CHANGELOG.md` `### Added` bullet
  **in whichever release actually ships it** (NOT the `v1.4.1`
  bugfix-only block — see Scope note).

### Related

- This session's `tryAutoJumphost` single-target thread — the feature's
  headline use case. Orthogonal but complementary to **Issue 5** below
  (auto-register `jumphost-<zone>` targets from
  `testing_cluster_jumphost_public_ips`).
- Same "roksbnkctl owns terraform's cwd + `TF_DATA_DIR`, the CLI layer
  must not re-derive them" invariant that Issues 1-3 enforce — the
  implementation note (req. 3) exists specifically so this feature
  doesn't reintroduce that class of bug.
- `internal/tf/terraform.go:39` (`Open`), `:114`
  (`tfexec.NewTerraform`), `:135` (`TF_DATA_DIR` side-effect),
  `:153-160` (`SourceDir`/`StateDir`/`TFVarsPath`) — the plumbing to
  reuse.

### Out of scope

- Any mutating terraform operation (permanently — that is the entire
  point of the gate; mutations are the lifecycle verbs' exclusive
  domain).
- `--on <target>` remote dispatch for `terraform` (state is
  workstation-local; explicitly rejected, not deferred).
- Auto-registering per-zone cluster jumphosts (separate potential
  enhancement; noted under Related, not part of this issue).
- Pulling this into `v1.4.1` (patch scope) absent an explicit
  integrator decision — see Scope note.

---

## Issue 5: auto-register per-AZ cluster jumphosts as `jumphost-<zone>` targets — feature request

**Severity**: low (ergonomic enhancement, not a defect)
**Status**: deferred → post-v1.4.1 backlog (integrator triage 2026-05-18)

> **Scope note (read first).** Feature, filed into the Sprint 12 ledger
> at user request ("file … for the next sprint"). Sprint 12 is a strict
> bugfix-only patch (`v1.4.1`) — see `issues/issue_sprint12_architect.md`
> Issue 1 and `docs/PLAN.md:854-858`. Auto-registering extra targets
> changes user-visible `up` behaviour and `targets list` output — not a
> patch-cycle change. Recommendation: schedule for the next *minor*
> (`v1.5.0` / Sprint 13). Suggested triaged status: `accepted` (defer
> to Sprint 13). **Doc coupling:** `issues/issue_sprint12_architect.md`
> Issue 9 documents the *manual* `targets add` path for these
> jumphosts; if this lands, Issue 9b's manual steps collapse to "verify
> with `targets list`" and that doc must be revised in lockstep — ship
> the two together or sequence Issue 9 to follow this.

### Motivation

`tryAutoJumphost` (`internal/cli/lifecycle.go:540-565`) runs in the
post-`up` hook and seeds exactly one target — `jumphost` — from the
singular `testing_tgw_jumphost_ip` output. When
`testing_create_cluster_jumphosts = true`, the deploy also creates one
cluster jumphost per cluster-VPC AZ (`ibm_is_instance.cluster_jumphost`,
`for_each = local.cluster_zones`, `terraform/modules/testing/main.tf:404`;
per-AZ floating IP at `:430`), each reachable on its own FIP with the
**same** shared key. Today the user must discover these exist, look up
the FIPs, and `targets add` each by hand (the workflow
`issues/issue_sprint12_architect.md` Issue 9b documents). This
enhancement makes the post-`up` hook register them automatically,
matching the convenience the single `jumphost` target already gives.

### Proposed change

Extend `tryAutoJumphost` (or add a sibling `tryAutoClusterJumphosts`
called immediately after it from the same post-`up` hook site) to:

1. Read the `testing_cluster_jumphost_public_ips` output — a
   terraform **map** `{ zone => fip }` (`terraform/outputs.tf:82-84`;
   value is `{}`/`[]` when `testing_create_cluster_jumphosts = false`).
   Add a `mapOutput(outputs, key) map[string]string` helper beside the
   existing `stringOutput` (`internal/cli/lifecycle.go:550-551` use
   site; the `json.Unmarshal(om.Value, &s)` pattern at
   `lifecycle.go:584-588` is the model — unmarshal into
   `map[string]string`, and treat a unmarshal error / empty map / the
   `[]`-default JSON as "no cluster jumphosts, skip" exactly like the
   existing `ip == "" || ip == "TGW jumphost not created"` guard).
2. Reuse the same `keyPEM := stringOutput(outputs,
   "jumphost_shared_key")` presence check already in
   `tryAutoJumphost` (the cluster jumphosts share that key — no new
   output needed; `KeySource: "tf-output:jumphost_shared_key"`).
3. For each `zone => fip`, `remote.SetTarget(cctx.WorkspaceName,
   "jumphost-"+zone, config.TargetCfg{Host: fip, User: "ubuntu",
   KeySource: "tf-output:jumphost_shared_key"})` — same shape as the
   existing TGW seed (lines 555-559), name = `jumphost-<zone>`.
   `SetTarget` is already idempotent/upsert
   (`internal/remote/targets.go`), so re-`up` refreshes rotated FIPs —
   matching the documented "the auto-seeded targets follow IP
   rotation" contract for the TGW `jumphost`.
4. Best-effort, mirroring `tryAutoJumphost`'s existing posture: any
   failure logs `warning:` to stderr and does **not** fail `up`
   (the parent succeeded because terraform succeeded; targets are a
   convenience). One summary line:
   `✓ Auto-registered N per-AZ cluster jumphost targets
   (jumphost-<z1>, jumphost-<z2>, …); use roksbnkctl --on jumphost-<zone> ...`.

**Stale-target handling (call out for design review).** Unlike the
single `jumphost` (always overwritten in place), the *set* of
`jumphost-<zone>` targets can shrink across applies (zone removed,
`testing_create_cluster_jumphosts` flipped to false). An upsert-only
loop leaves orphaned `jumphost-<oldzone>` entries pointing at
destroyed hosts. Options, integrator to choose:
  - (a) **Upsert-only** (simplest; orphans linger until manual
    `targets remove`) — document the caveat.
  - (b) **Reconcile**: remove any existing `jumphost-*` target (by the
    `jumphost-` name prefix) not present in the current output map,
    then upsert. Safer UX but introduces prefix-ownership semantics
    (must not nuke a user's hand-named `jumphost-mybox`). If chosen,
    namespace the auto-managed ones unambiguously (e.g. only reconcile
    names matching `jumphost-<known-zone-pattern>`), or record an
    `auto: true` marker in `config.TargetCfg` (schema change — likely
    out of patch/minor scope; lean (a) for v1.5.0 and revisit (b)
    later).
Recommend **(a)** for the first cut with a documented caveat; (b) is a
follow-up if orphaned-target confusion is reported.

### Acceptance criteria

- After `roksbnkctl up` with `testing_create_cluster_jumphosts = true`
  in a 3-AZ region, `roksbnkctl targets list` shows `jumphost` **and**
  `jumphost-<zone>` for each AZ; `roksbnkctl --on jumphost-<zone>
  kubectl get pods` works (full passthrough, no hop).
- With `testing_create_cluster_jumphosts = false` (or output absent /
  `[]`): behaviour unchanged — only `jumphost` is seeded, no error, no
  spurious targets, no warning noise.
- A failure reading/parsing the map output logs a single `warning:`
  and does not fail `up` (parity with `tryAutoJumphost`).
- Re-running `up` after a FIP rotation refreshes the
  `jumphost-<zone>` host values in place (upsert idempotence).
- The stale-target behaviour chosen ((a) or (b)) is implemented as
  decided and its caveat documented (couples to architect Issue 9).
- `go build ./...`, `go vet ./...`, `gofmt -l .`, `make staticcheck`,
  `go test ./...` clean/green; new unit test covers: map-output parse,
  empty/`[]`/absent → no-op, multi-zone → N upserts, key-PEM-missing
  → skip.

### Files affected

- `internal/cli/lifecycle.go` — extend `tryAutoJumphost` or add
  `tryAutoClusterJumphosts` + the `mapOutput` helper; wire into the
  same post-`up` hook call site that already invokes
  `tryAutoJumphost`.
- `internal/remote/targets.go` — only if option (b) reconcile is
  chosen (a prefix-scoped sweep helper); none for option (a).
- `internal/config` — only if an `auto:` marker is added (discouraged
  for v1.5.0; flagged in §"Proposed change").
- Tests: `internal/cli/lifecycle_test.go` (or the existing
  auto-jumphost test file if one exists — check before adding).
- Docs: couples to `issues/issue_sprint12_architect.md` Issue 9
  (9b becomes "verify with `targets list`"); `CHANGELOG.md`
  `### Added`/`### Changed` bullet **in whichever release ships it** —
  NOT the `v1.4.1` bugfix-only block.

### Related

- `issues/issue_sprint12_staff.md` Issue 4 (read-only `terraform`
  escape hatch) — independent, but both came from the same
  per-AZ-jumphost discoverability thread; the escape hatch is the
  manual-lookup path this feature automates away.
- `issues/issue_sprint12_architect.md` Issue 9 — the docs side; hard
  coupling (see Scope note / §"Doc coupling").
- Output/code facts: `terraform/outputs.tf:82-89`
  (`testing_cluster_jumphost_public_ips` / `_ssh_commands`),
  `terraform/modules/testing/main.tf:404,430`,
  `internal/cli/lifecycle.go:540-565` (`tryAutoJumphost` pattern to
  mirror), `:584-588` (`json.Unmarshal` output-parse model),
  `internal/remote/targets.go` (`SetTarget` upsert/idempotent).

### Out of scope

- Changing the single TGW `jumphost` seed behaviour (unchanged).
- Option (b) reconcile + any `config.TargetCfg` schema change, unless
  the integrator explicitly wants it in the first cut (recommend
  deferring; lean option (a)).
- `--on`-time discovery (this is a post-`up`-hook registration
  feature, not a lazy resolver).
- Pulling into `v1.4.1` (patch scope) absent explicit integrator
  decision — see Scope note.
