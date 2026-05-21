You are the **staff engineer** agent for Sprint 19 of the roksbnkctl
project. Repo root: `/mnt/c/project/roksbnkctl`. You run with no
memory of prior conversation.

## Read first (in order)

1. `prompts/sprint19/README.md` — integrator decisions; especially
   §"Integrator decisions baked in" and §"Per-role scope".
2. `issues/issue_sprint19_staff.md` Issue 1 — the **authoritative
   spec** for what to build. Every acceptance criterion in that file
   is binding.
3. The existing `init` command surface:
   - `internal/cli/init.go` — the cobra command + interview wiring.
     This is where `--var-file` gets added.
   - `internal/config/init.go` (if present) or wherever the
     `config.yaml` writer lives — that's where the parsed tfvars
     values seed the per-workspace config.
   - `internal/tf/terraform.go` — the `tfws.HasUserTFVars()` /
     `UserTFVarsPath()` helpers. **You do NOT modify these.** They
     already do the layering for free; you just put the file at the
     path they expect.
4. `internal/config/applied_tfvars.go` —
   `readTFVarsAssignments(path)` is the existing parser you can
   reuse to extract per-key values from the user's `--var-file`. It
   handles the same shape `terraform.tfvars.example` ships, which
   is what the operator will pass.
5. The interview today (`internal/cli/init.go`) — read enough to
   know which questions it asks (region, cluster name, etc.) so
   you can map tfvars keys to the corresponding `config.yaml`
   fields.

## Tasks

1. Add `--var-file <path>` to the `init` cobra command. Flag
   description matches the rest of the codebase: one-line, names
   the canonical input file (`terraform.tfvars.example`).
2. When `--var-file` is supplied:
   - Read the file via `readTFVarsAssignments(path)`. If the file
     is missing or unparseable, exit non-zero with a roksbnkctl-
     level message that names the path (mirrors the
     option-(b)-style actionable-error pattern Sprint 16 round-2
     established).
   - Map tfvars assignments to the `config.yaml` fields the
     interview seeds: `ibmcloud_cluster_region`,
     `openshift_cluster_name`, `openshift_cluster_version`,
     `roks_workers_per_zone`, plus the `kubeconfig_dir` /
     `scratch_dir` which are computed paths (don't take those from
     the tfvars — keep computing them workspace-local).
   - For each field the file carries, skip the interview prompt
     for it. For fields the file doesn't carry, prompt (or default)
     exactly as today.
   - After the (possibly-shortened) interview and the
     `config.yaml` write, **copy the var-file verbatim** to BOTH
     phase state dirs:
     - `<WorkspaceStateDir>/terraform.tfvars.user`
     - `<WorkspaceClusterStateDir>/terraform.tfvars.user`
     File mode `0600` (matches the existing applied-tfvars
     snapshot pattern). Use the existing
     `config.WorkspaceStateDir(name)` /
     `config.WorkspaceClusterStateDir(name)` helpers; do not
     re-derive paths.
   - Print a single confirmation line per copy:
     `✓ Wrote <abs-path>` (style matches the existing init output).
3. Without `--var-file`, behaviour is byte-identical to today.

## Constraints

- **Touch only `internal/cli/init.go`** (+ a sibling tiny helper if
  the parse-and-seed logic deserves extraction, your call).
- **Do not edit** `internal/tf/`, `internal/orchestration/`,
  `internal/cos/`, `internal/ibm/`, or `internal/cli/cos.go`.
  The lifecycle / cos / ibm work landed in Sprint 18; this sprint
  is `init`-side only.
- **Do not edit any pre-existing `_test.go` file.** New additive
  tests only (the spec requires them).
- Do **not** commit. Integrator commits. Do not push.
- Do **not** run `gh issue create`.

## Verify before reporting done

- `go build ./...` clean. `go vet ./...` clean.
  `gofmt -l internal/` empty.
- `go test -race ./internal/cli/` green incl. your new test file(s).
  (Pre-existing `init`-adjacent tests must keep passing
  byte-unchanged; `git diff --stat -- '*_test.go'` shows only your
  new file(s).)
- Trace in your closure: `init --var-file ./tfvars -w testws` flow →
  parser reads N assignments → config.yaml seeded with M of them →
  interview skips those M questions → file copied to both phase
  state dirs at mode `0600` → existing `HasUserTFVars()` codepath
  picks them up on subsequent lifecycle ops (you should NOT have
  needed to touch this — name the file and the helper to confirm
  you read the existing wiring).

## Issue file

Append a **Closure** section to `issues/issue_sprint19_staff.md`:
files changed, the tfvars→config.yaml key mapping you wired (one
short line per mapped key), gates run + results, acceptance-criteria
pass list.

## Final report

≤200 words: files touched, the tfvars→config.yaml key mapping, the
two destination paths the file lands at, test results, and the
dataflow trace showing a subsequent bare `plan -w <ws>` picks up
the file via `HasUserTFVars()` without any further code change.
State explicitly you did not commit and did not touch the
out-of-scope packages.
