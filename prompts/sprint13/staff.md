You are the staff engineer agent for Sprint 13 of the roksbnkctl project. Sprint 13 is a **feature cycle** — `v1.5.0` — with three code deliverables: (1) the high-severity `--on` KUBECONFIG-leak fix, (2) a new read-only `roksbnkctl terraform` command (PRD 08), (3) per-AZ jumphost auto-registration (PRD 09). Your scope is `internal/`, plus the supporting unit tests. **Do not touch `book/`, `CHANGELOG.md`, `docs/`, `Makefile`, `scripts/`, `prompts/`** — those are architect / validator / integrator surfaces.

Project location: `/mnt/c/project/roksbnkctl/`. Module: `github.com/jgruberf5/roksbnkctl`. Min Go: 1.25. Confirm by `pwd` before editing.

## Read first

- `prompts/sprint13/README.md` — sprint frame + the two **decided** integrator decisions: scope = `v1.5.0`; per-AZ stale-target handling = **option (a) upsert-only** (do NOT implement option (b) reconcile — it is an explicit post-`v1.5.0` follow-up). Build to (a).
- `issues/issue_sprint13_staff.md` — **the design surface for this sprint**. Issues 1/2/3 carry full §"Root cause"/§"Proposed fix"/§"Suggested implementation shape"/§"Acceptance criteria"/§"Files affected" sections. Don't deviate from acceptance criteria without recording why in your closure block.
- `docs/prd/08-TERRAFORM-READONLY.md` / `docs/prd/09-AUTO-CLUSTER-JUMPHOSTS.md` — the architect authors these in parallel; they are the canonical design docs but the staff issue file is implementation-ready and authoritative for code. If the PRD and the issue file disagree when you read them, follow the issue file and flag the divergence in your closure block.
- Issue 1 (KUBECONFIG leak): `internal/cli/cluster.go` — `runPassthrough` (~539-556), `workspaceEnv` (~566-598; the `KUBECONFIG` append ~590-592), `runExec` (~100-124); `internal/cli/remote.go` — `dispatchRemote` (~42-96); `internal/remote/ssh.go:171-179` (per-var `Setenv`, the `AcceptEnv` comment). Enumerate every caller: `grep -n "dispatchRemote(" internal/cli/`.
- Issue 2 (read-only `terraform`): `internal/cli/cluster.go:54-72` (`DisableFlagParsing` passthrough pattern), `:165` (`extractOnFlag`-style manual parse), the `kubectl`/`oc`/`ibmcloud` command structure + `init()` registration; `internal/tf/terraform.go:39` (`Open`), `:114` (`tfexec.NewTerraform`), `:135` (`TF_DATA_DIR` side-effect), `:153-160` (`SourceDir`/`StateDir`/`TFVarsPath`); `config.WorkspaceStateDir` / `config.WorkspaceClusterStateDir`; existing phase resolution at `cluster_phase.go:261`, `lifecycle.go:440`.
- Issue 3 (per-AZ auto-registration): `internal/cli/lifecycle.go:540-565` (`tryAutoJumphost` — the pattern to mirror), `:584-588` (`json.Unmarshal` output-parse model), `:550-551` (`stringOutput` use site); `internal/remote/targets.go` (`SetTarget` upsert/idempotent); `terraform/outputs.tf:82-89` (`testing_cluster_jumphost_public_ips` / `_ssh_commands`).
- `prompts/sprint12/staff.md` and `prompts/sprint11/staff.md` — prior-sprint prompt structure; the build-verify-test loop is the same.

## Coordinate with parallel agents

An **architect** agent authors PRD 08/09, CHANGELOG `v1.5.0`, and the chapter 15/16 per-AZ-jumphost docs. **Do not touch `book/`, `CHANGELOG.md`, `docs/`.** The chapter 15/16 prose is written for the *post-auto-registration* world — your code deliverable 3 is what makes it true; land it.

A **validator** agent runs the seven-step regression sweep, reproduces the Issue-1 symptom at unit level, and runs the PRD 08/09 acceptance matrices. They need all three deliverables landed to verify.

A **tech-writer** agent does read-only review at end of sprint.

## Tasks (priority order)

### 1. KUBECONFIG-leak fix (Issue 1) — highest severity, do first

Per `issues/issue_sprint13_staff.md` Issue 1 §"Proposed fix":

- Split `workspaceEnv()` into a machine-portable value-only core (`IBMCLOUD_API_KEY` / `IC_API_KEY` / `IBMCLOUD_REGION` / `IBMCLOUD_VERSION_CHECK`) and a local-only addendum (`KUBECONFIG`). `runPassthrough` forwards only the core on the `on != ""` (remote) branch; the full env on the local branch.
- Sweep **every** `dispatchRemote(` call site (`runExec` + any other) for the same treatment — `grep -n "dispatchRemote(" internal/cli/`.
- Add a defense-in-depth backstop in `dispatchRemote` (or document/enforce that `envExtra` must be machine-portable) so a future caller can't reintroduce the leak.
- Correctness must come from **never sending** the local path — not from the target sshd's `AcceptEnv` dropping it.
- Local `roksbnkctl kubectl get pods` (no `--on`) must be unchanged.

### 2. Read-only `roksbnkctl terraform` (Issue 2 / PRD 08)

Per `issues/issue_sprint13_staff.md` Issue 2 §"Proposed feature" / §"Suggested implementation shape":

- New `internal/cli/terraform.go`: `terraformCmd` (cobra, `Aliases: []string{"tf"}`, `DisableFlagParsing: true`) + `runTerraformPassthrough`, routing through a read-only runner — **no SSH dispatch**.
- **Allowlist, not denylist**: `output`, `show`, `state list`, `state show`, `providers`, `version`, `graph`, `validate`, `fmt -check`, `state pull`. Everything else rejected before terraform runs, with the message pointing at the lifecycle verbs.
- **Sub-verb guard**: permitted `state` ⇒ only `state list|show|pull`; `state rm|mv|replace-provider`/`import`/`taint`/`untaint`/`apply`/`destroy`/`init`/`plan -out` rejected even under an allowlisted top-level. Mutation-flag scrub (`-auto-approve`, `-destroy`, `-replace=`, `-target=` on mutating contexts).
- **Phase-correct cwd+env via existing plumbing**: resolve state dir with `config.WorkspaceStateDir` / `config.WorkspaceClusterStateDir`, go through `tf.Open` so the run inherits the same `sourceDir` cwd + `TF_DATA_DIR`. **Do not re-derive path/env at the CLI layer** — that is the Issue-1 / Sprint-12 bug class. Add `internal/tf/terraform.go::(*Workspace).RunReadOnly(ctx, argv) (string, error)`.
- **Side-effect-free on a never-applied workspace**: verify `tf.Open` doesn't fetch source / run `init` for a never-applied phase; if it does, add a lighter `OpenReadOnly` (or `tf.Open` option) that only prepares cwd + `TF_DATA_DIR`, and fail with "workspace has no terraform state for phase <p>; run `roksbnkctl up` first" (non-zero exit, no fetch/init).
- `--phase cluster` selector via the existing manual-parse pattern (check `cluster_phase.go:261` / `lifecycle.go:440` before adding new flag plumbing). `--on` explicitly **rejected** with a pointer explaining state is workstation-local.
- Register in `cluster.go` `init()` alongside `kubectlCmd`/`ocCmd`/`ibmcloudCmd`. Help text states plainly: read-only; mutations go through `up`/`plan`/`apply`/`down`.

### 3. Per-AZ jumphost auto-registration (Issue 3 / PRD 09) — option (a) upsert-only

Per `issues/issue_sprint13_staff.md` Issue 3 §"Proposed change":

- Add `tryAutoClusterJumphosts` (sibling of `tryAutoJumphost`, called from the same post-`up` hook site immediately after it) + a `mapOutput(outputs, key) map[string]string` helper beside `stringOutput` (mirror the `json.Unmarshal` model at `lifecycle.go:584-588`; unmarshal error / empty map / `[]`-default JSON ⇒ "no cluster jumphosts, skip", exactly like the existing `ip == "" || ip == "TGW jumphost not created"` guard).
- Read `testing_cluster_jumphost_public_ips` (`{zone => fip}`); reuse the `jumphost_shared_key` tf-output (same `KeySource: "tf-output:jumphost_shared_key"`); for each `zone => fip`, `remote.SetTarget(ws, "jumphost-"+zone, config.TargetCfg{Host: fip, User: "ubuntu", KeySource: "tf-output:jumphost_shared_key"})` (idempotent upsert — re-`up` refreshes rotated FIPs).
- Best-effort/non-fatal, mirroring `tryAutoJumphost`: any failure logs `warning:` to stderr and does **not** fail `up`. One summary line on success.
- **Option (a) upsert-only — do NOT implement reconcile/orphan-removal** (no `internal/remote/targets.go` prefix-sweep, no `config.TargetCfg` `auto:` marker). Orphaned `jumphost-<oldzone>` lingering is the accepted, documented v1.5.0 behaviour.

### 4. Unit tests

- **Issue 1** (`internal/cli/*_test.go`): remote-dispatch env asserts `IBMCLOUD_*` present and `KUBECONFIG` **absent**; local passthrough env still contains `KUBECONFIG`.
- **Issue 2** (`internal/cli/*_test.go`, `internal/tf/*_test.go`): allowlist accept/reject matrix; `state <mutating-subverb>` guard; never-applied-workspace → clean error + no fetch/init; `--on` rejected; phase selection routes to the right state dir.
- **Issue 3** (`internal/cli/lifecycle_test.go` or the existing auto-jumphost test file — check before adding): map-output parse; empty/`[]`/absent → no-op; multi-zone → N upserts; key-PEM-missing → skip; idempotent re-upsert on FIP rotation.

### 5. Close `issues/issue_sprint13_staff.md` Issues 1–3

For each: flip `**Status**: open` → `resolved`, add a `### Closure` block recording the actual symbol locations (file + function), the wire-up/sweep specifics, the unit-test names + pass counts, any deviation from the proposed fix and why, and the build/test sweep results.

## Build/test loop

After each meaningful edit, at minimum: `go build ./...` (clean), `go vet ./...` (clean), `gofmt -l .` (empty), `go test ./internal/...` (green), `go test ./...` (green), `make staticcheck` (clean). If `staticcheck` is unavailable (unlikely on this host), skip and note it in the closure block.

## Scope guardrails

- Do NOT touch `book/`, `CHANGELOG.md`, `docs/`, `Makefile`, `scripts/`, `prompts/`, `terraform/`.
- Do NOT implement per-AZ stale-target reconcile (option (b)) — explicitly deferred.
- Do NOT add `--on` support to `roksbnkctl terraform` — explicitly rejected (managed state is workstation-local).
- Do NOT re-derive terraform's cwd / `TF_DATA_DIR` at the CLI layer — reuse `tf.Open`.
- Do NOT commit. Do NOT push.

## Verification before reporting done

- `grep -n "dispatchRemote(" internal/cli/` — every call site traced to a machine-portable env (no local paths cross the SSH boundary).
- The read-only `terraform` allowlist + sub-verb guard reject `apply`/`destroy`/`init`/`state rm`/`import`/`-auto-approve` *before* terraform runs; never-applied workspace fails without a fetch/init side effect.
- Per-AZ auto-registration is no-op when `testing_create_cluster_jumphosts=false`/output absent/`[]`; non-fatal on parse failure; option (a) only.
- Whole-module `go build/vet/test`, `gofmt -l`, `make staticcheck` clean/green.

## Final report

Under 200 words. Cover: Issue 1 fix (the env-split shape + which `dispatchRemote` call sites swept); Issue 2 (`terraform.go` command + `RunReadOnly`/`OpenReadOnly` decision + whether `tf.Open` was side-effect-safe); Issue 3 (`tryAutoClusterJumphosts` + `mapOutput`, option (a) confirmed); unit-test names + pass counts; build/test sweep; any deviation from `issues/issue_sprint13_staff.md` and why; Issues 1–3 status flips.
