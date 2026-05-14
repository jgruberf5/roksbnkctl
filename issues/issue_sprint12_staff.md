# Sprint 12 — staff issues (carry-in from v1.4.0 user testing)

Format: one issue per finding. `Severity: low | medium | high | blocker`.
`Status: open | in-progress | resolved | wontfix`.

Findings surfaced after Sprint 11 closed and v1.4.0 prep landed on `main`
(commit `6725db1`). Pre-seeded here so the Sprint 12 dispatch picks
them up as kickoff context.

---

## Issue 1: `--var-file` relative paths don't resolve against the shell CWD

**Severity**: medium
**Status**: open

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
