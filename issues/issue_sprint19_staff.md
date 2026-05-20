# Sprint 19 — staff issues (`init --var-file` workspace-persistent tfvars)

> **Sprint 19 frame.** First regular work sprint post-`v1.6.3`.
> Staff owns the `init --var-file <path>` flag + the file-copy +
> interview-skip wiring. Lifecycle / orchestration / cos / ibm
> stay UNTOUCHED — Sprint 18 hardened them and the plumbing
> (`tfws.HasUserTFVars()` + auto-layering of
> `state/terraform.tfvars.user`) already exists in the tree. This
> sprint just puts the file at the path the existing wiring
> expects.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1 — feat: `roksbnkctl init --var-file <path>` — workspace-persistent tfvars at init time

**Severity**: medium
**Status**: open

### Motivation

Live use of `v1.6.3` (post-Sprint-18) surfaced a residual UX gap that the Sprint 16 applied-tfvars replay can't close: between `init` and the first successful `up`, there is no `terraform.applied.tfvars` snapshot yet, so bare `roksbnkctl <verb> -w <ws>` refuses with the Sprint 16 option-(b) actionable error ("workspace has no terraform.applied.tfvars snapshot for the <phase> phase yet"). The operator's actual remedy — re-supplying `--var-file ./terraform.tfvars` on every subsequent command — is the exact friction Issue 3 was supposed to solve.

The existing `tfws.HasUserTFVars()` codepath in `internal/tf/terraform.go` already auto-layers a workspace-persistent `state/terraform.tfvars.user` between the auto-rendered tfvars and the caller's `--var-file` flags. Today it's a manual-drop file; nobody does it because it's undocumented. This issue automates the drop at `init` time.

### Proposed surface

```
roksbnkctl init -w <workspace> --var-file <path>
```

- `--var-file <path>` — optional. When supplied: read the file, parse the tfvars assignments, seed `config.yaml` from the fields the interview cares about, skip those interview prompts, and copy the file verbatim to both phase state dirs. When omitted: byte-identical to today's interview.

### Behavior

- **Parse the var-file** via `internal/config/applied_tfvars.go`'s existing `readTFVarsAssignments(path)` (the snapshot writer already uses this — same format, same tolerance for unsupported HCL shapes which are skipped).
- **Map to `config.yaml` fields the interview already asks about**:
  - `ibmcloud_cluster_region` → `cluster.region`
  - `ibmcloud_resource_group` → `cluster.resource_group`
  - `openshift_cluster_name` → `cluster.name`
  - `openshift_cluster_version` → `cluster.version`
  - `roks_workers_per_zone` → `cluster.workers_per_zone`
  - `create_roks_cluster` → `cluster.create`
  - (Add any others the current interview prompts for; do NOT add new `config.yaml` fields this sprint.)
  - `kubeconfig_dir` / `scratch_dir` are **computed paths** (workspace-local), NOT taken from the var-file even if it carries them.
- **Skip the corresponding interview prompts** for any field the var-file answered. Fields the var-file doesn't carry still prompt (or default) exactly as today.
- **Copy the var-file verbatim** to BOTH phase state dirs:
  - `<config.WorkspaceStateDir(name)>/terraform.tfvars.user`
  - `<config.WorkspaceClusterStateDir(name)>/terraform.tfvars.user`
- **File mode `0600`** on both copies (matches the existing `terraform.applied.tfvars` snapshot pattern, since the file carries `ibmcloud_api_key`).
- **Print one confirmation line per copy**: `✓ Wrote <abs-path>` (style matches the existing init output).
- **Missing file**: exit non-zero with a roksbnkctl-level actionable error naming the path the operator passed.
- **Malformed file**: exit non-zero pointing the operator at `terraform.tfvars.example`.

### Acceptance criteria

1. `roksbnkctl init -w ws1 --var-file ./terraform.tfvars` creates `~/.roksbnkctl/ws1/state/terraform.tfvars.user` AND `~/.roksbnkctl/ws1/state-cluster/terraform.tfvars.user`, both mode `0600`, both byte-identical to the input file.
2. `~/.roksbnkctl/ws1/config.yaml` post-init reflects the var-file's `ibmcloud_cluster_region` / `openshift_cluster_name` / etc. values (the interview did NOT prompt for those).
3. `roksbnkctl init` without `--var-file` is byte-identical to today: interactive interview, no `terraform.tfvars.user` file created.
4. `roksbnkctl init -w ws1 --var-file /nonexistent` exits non-zero with an error that names the path.
5. `roksbnkctl init -w ws1 --var-file <a-file-that-isn't-tfvars>` exits non-zero with a message pointing the operator at `terraform.tfvars.example`.
6. After step 1, bare `roksbnkctl plan -w ws1` (NO `--var-file`) does NOT hit the Sprint 16 option-(b) "no snapshot yet" error — it picks up the `terraform.tfvars.user` via the existing `HasUserTFVars()` codepath and reaches `terraform plan`. (The live verify gate.)
7. `roksbnkctl init -w ws1 --var-file ./alt.tfvars` on an already-initialized workspace overwrites the existing `terraform.tfvars.user` copies (with a brief stderr note that an existing file was replaced).
8. New hermetic test file `internal/cli/init_var_file_test.go` covers cases (a)–(e) from the validator spec; no edits to any pre-existing `_test.go`.
9. `--var-file <path>` flag appears in the auto-generated `book/src/27-command-reference.md` after the architect's regen (separate role, but the staff impl must be cobra-correct enough that the regen produces clean output).

### Out of scope

- A `--force` flag for the re-init-with-overwrite case (Acceptance #7 just overwrites + notes it; explicit `--force` is a v1.7 polish).
- Caching the IAM token to disk (already explicit out-of-scope in Sprint 18 Issue 2; the persisted tfvars carries the key directly, which is sufficient).
- Validating the var-file's contents against the terraform variable schema (the file is passed verbatim; terraform validates on the next lifecycle op).
- Any change to `internal/orchestration/`, `internal/tf/`, `internal/cos/`, `internal/ibm/`, `internal/cli/cos.go`. Sprint 18 hardened those; this sprint is `init`-side only.

### Files likely touched

- `internal/cli/init.go` — `--var-file` flag binding + the parse/seed/copy/skip-interview logic.
- (Possibly) a small sibling helper in `internal/cli/` if the parse-and-seed logic is large enough to deserve extraction; staff's call.
- `internal/cli/init_var_file_test.go` (new, additive — covers AC #8).
- `book/src/27-command-reference.md` — regenerated by the architect (not staff).

### Related

- Sprint 16 validator Issue 3 (applied-tfvars replay) — this issue closes the residual gap between `init` and the first successful apply.
- Sprint 16 round-2 option (b) actionable-error — its hint at "workspace has no snapshot yet" remains for the genuine never-initialized case; `init --var-file` provides the explicit remedy the message currently lacks.
- The existing `HasUserTFVars()` + `UserTFVarsPath()` helpers in `internal/tf/terraform.go` — staff confirms they're already wired into the lifecycle, no edits there.
