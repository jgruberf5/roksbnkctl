# Sprint 13 — staff issues (carry-in from Sprint 12 / post-v1.4.0 user testing)

> **Sprint 13 frame.** Feature cycle, `v1.5.0`. These three issues are
> Sprint 12 staff Issues 3, 4, 5 carried verbatim (provenance/scope
> notes preserved as history) and renumbered 1/2/3. They are the
> **implementation-ready design surface** — staff builds directly from
> the §"Proposed fix" / §"Proposed feature" / §"Acceptance criteria"
> sections; the architect formalizes PRD 08 (Issue 2) and PRD 09
> (Issue 3) in parallel. Status reset to `open` for this cycle.
>
> **Integrator decisions (decided — do not relitigate; see
> `prompts/sprint13/README.md` and `docs/PLAN.md` §"Sprint 13"):**
> 1. Scope is `v1.5.0` (not the standalone `v1.4.2` the Sprint 12
>    CHANGELOG `§Deferred` note named for Issue 1).
> 2. Issue 3 per-AZ stale-target handling is **option (a) upsert-only**
>    with a documented orphan caveat. Option (b) reconcile is an
>    explicit post-`v1.5.0` follow-up — **do not implement it**.

`Status: open | in-progress | resolved | wontfix | accepted`.

---

## Issue 1: local `KUBECONFIG` filesystem path leaks into the `--on <target>` remote environment

**Severity**: high
**Status**: open — Sprint 13 / `v1.5.0` (carried from Sprint 12 staff Issue 3; the Sprint 12 CHANGELOG `§Deferred` note named a standalone `v1.4.2` fast-follow — integrator re-targeted to `v1.5.0`, shipped alongside the two features below)

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


## Issue 2: no read-only `terraform` escape hatch — feature request

**Severity**: low (ergonomic enhancement, not a defect)
**Status**: open — Sprint 13 / `v1.5.0` (carried from Sprint 12 staff Issue 4; integrator accepted as a `v1.5.0` feature — formalized as PRD 08. The Sprint 12 "strict bugfix-only patch" scope note below is historical context for *why it was deferred then*; it is now in scope.)

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


## Issue 3: auto-register per-AZ cluster jumphosts as `jumphost-<zone>` targets — feature request

**Severity**: low (ergonomic enhancement, not a defect)
**Status**: open — Sprint 13 / `v1.5.0` (carried from Sprint 12 staff Issue 5; integrator accepted as a `v1.5.0` feature — formalized as PRD 09. **Stale-target handling = option (a) upsert-only** with a documented orphan caveat; option (b) reconcile is an explicit post-`v1.5.0` follow-up — do **not** implement (b) this cycle. The Sprint 12 "strict bugfix-only patch" scope note below is historical context.)

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
