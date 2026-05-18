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
