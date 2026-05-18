# PRD 08 — read-only `roksbnkctl terraform` escape hatch

> A gated, read-only passthrough to terraform against a workspace's managed state, so users can run `output` / `show` / `state list` / `version` / etc. without the fragile `cd ~/.roksbnkctl/<ws>/state && TF_DATA_DIR=… terraform …` workaround. Estimated effort: small-to-medium (~250 LOC + tests).

## Why

`roksbnkctl` drives terraform; it does not wrap it. The lifecycle verbs (`up` / `plan` / `apply` / `down`, plus phase-scoped `cluster` / `bnk` up/down) are the **mutating** terraform interface and must stay the *only* mutation path. Running `apply` / `destroy` outside the orchestration skips the rendered `terraform.tfvars`, the apply-retry wrapper, the post-apply kubeconfig fetch, the `terraform.applied.tfvars` snapshot (PRD 07), and the auto-jumphost seeding (PRD 01 / PRD 09), and desyncs the managed state.

But there is currently **no** supported way to run *read-only* terraform against a workspace's managed state. Real cases that hit this in post-v1.4.0 user testing:

- Looking up the per-AZ cluster-jumphost floating IPs (`terraform output testing_cluster_jumphost_ips`) after discovering that only the singular TGW `jumphost` target auto-registers — the thread that produced PRD 09. (Even with PRD 09's auto-registration, this is still the canonical "show me the IPs" diagnostic.)
- Inspecting state (`terraform state list`, `terraform show`) when debugging a partial apply.
- Confirming provider / terraform versions (`terraform version`, `terraform providers`).

Today the only workaround is the undocumented, fragile

```
cd ~/.roksbnkctl/<ws>/state[-cluster] && TF_DATA_DIR=$PWD/terraform terraform output
```

which (a) leaks the internal workspace layout into muscle memory, (b) is one wrong directory from running against the wrong phase, and (c) is one fat-fingered `terraform apply` / `terraform state rm` away from corrupting the managed state `roksbnkctl` is responsible for. A gated escape hatch removes the foot-gun *and* the layout leak in one move, and is strictly read-only by construction so it can never become an alternate mutation path.

## Goal

Add a new passthrough-style command, `roksbnkctl terraform <subcommand> [args…]` (alias `tf`), that runs a fixed allowlist of **read-only** terraform subcommands against the workspace's phase-correct managed state, reusing the exact cwd + `TF_DATA_DIR` plumbing the lifecycle uses, and rejecting everything else (every mutating subcommand, mutating sub-verbs of `state`, mutation flags, and `--on`) *before* terraform is invoked.

```
roksbnkctl terraform output testing_cluster_jumphost_ips
roksbnkctl --phase cluster terraform state list
roksbnkctl tf show
roksbnkctl tf version
```

The command is read-only **by allowlist, permanently**. It is not a generalized terraform wrapper and never will be — mutations are the lifecycle verbs' exclusive domain. This is the entire point of the gate.

## Design

### The allowlist (not a denylist)

Only an explicit set of read-only top-level subcommands is permitted; anything not in the set is rejected before terraform runs:

```
output  show  state list  state show  state pull
providers  version  graph  validate  fmt -check
```

A denylist is explicitly rejected as a design: terraform's surface grows, and a forgotten-to-deny new mutating verb would silently become a corruption path. The allowlist fails closed — a new terraform verb is rejected until someone deliberately adds it here and to this PRD.

Rejection message names the offending verb and points at the lifecycle verbs:

> `roksbnkctl terraform` is read-only; `<sub>` can mutate state. Use `roksbnkctl up`/`plan`/`apply`/`down` (or `cluster`/`bnk` up/down) for changes.

### The sub-verb guard (`state`)

`state` is allowlisted only for its read-only children. The permitted set is **`{output, show, providers, version, graph, validate, fmt}` ∪ (for `state`) `{state list, state show, state pull}`** — a second guard inspects `argv` when `argv[0] == "state"` and rejects any first sub-arg that is not `list` / `show` / `pull`. So `terraform state rm <addr>`, `state mv`, `state replace-provider`, and `state push` are rejected **even though top-level `state` is allowlisted**. `import`, `taint`, `untaint`, `apply`, `destroy`, `init`, `plan` are not allowlisted at all and fall through to the top-level rejection.

### The mutation-flag scrub

Even on an allowlisted subcommand, reject the mutating-context flags before exec: `-auto-approve`, `-destroy`, `-replace=`, `-target=`, `-out=` (`plan -out` is not allowlisted anyway, but the scrub is defense-in-depth and keeps the rejection message specific). `fmt` is allowlisted only with `-check` semantics — a bare `terraform fmt` rewrites files on disk, so the command requires the `-check` (or `-diff`/`-list` read-only) posture and rejects a write-mode `fmt`. The scrub is a second, independent gate: the allowlist gates the *subcommand*, the scrub gates *flags on an otherwise-permitted subcommand*.

### Phase-correct cwd + env via the existing plumbing

This is the load-bearing invariant and the reason this PRD exists as a design doc rather than a one-liner: **`roksbnkctl` owns terraform's working directory and `TF_DATA_DIR`; the CLI layer must not re-derive them.** This is the same bug class as the Sprint 12 `--var-file`/`--tf-source` relative-path traps and the Sprint 13 KUBECONFIG-leak (`issues/issue_sprint13_staff.md` Issue 1) — a context-correct path computed at the wrong layer detonates later.

Resolve the state dir exactly as the lifecycle does — `config.WorkspaceStateDir` (default phase) / `config.WorkspaceClusterStateDir` (`--phase cluster`) — then go through `tf.Open(ctx, name, wsCfg, stateDir, apiKey, …)` so the run inherits the same `sourceDir` cwd (`<stateDir>/tf-source`), the same `TF_DATA_DIR` side-effect, and the same configured `tfexec.Terraform`. The read-only runner never recomputes a path the lifecycle already computes.

`--phase` selection reuses the existing resolution path that the non-lifecycle commands use (the same selector `cluster show` / status already rely on); a new `--phase`-style selector is added only if one does not already exist for non-lifecycle commands — checked, not assumed.

### Side-effect-free against a never-applied workspace

`tf.Open` today fetches the TF source into `<stateDir>/tf-source` and can run `init`. A read-only invocation **must not** trigger a source fetch or `init` against a workspace that was never applied — a user running `roksbnkctl terraform output` on a fresh workspace must not silently materialize a source tree or mutate anything. If `tf.Open` is not side-effect-safe for a never-applied workspace, the implementation adds a lighter `tf.OpenReadOnly` (or a `tf.Open` option) that only prepares cwd + `TF_DATA_DIR` and skips fetch/init. Either way, the user-facing behavior is a clear, non-zero-exit error:

> workspace has no terraform state for phase `<p>`; run `roksbnkctl up` first

— and **no** filesystem side effect.

### `--on` is rejected, not deferred

The managed state lives on the local workstation (`~/.roksbnkctl/<ws>/state…`). Running read-only terraform "on the jumphost" is nonsensical — the jumphost has no copy of the state. `roksbnkctl --on <target> terraform …` is rejected with a pointer explaining state is workstation-local; it is not a deferred feature, it is out of scope by construction.

### Command surface and flag parsing

`DisableFlagParsing: true` (like the existing `kubectl`/`oc`/`ibmcloud` passthroughs) so terraform's own flags reach terraform untouched; the `--phase` / `-w` selectors are extracted with the existing manual `extractOnFlag`-style parse before the rest of `argv` is handed through. The command registers in the same `init()` alongside the existing passthrough commands. Help text states plainly: **read-only; mutations go through `up`/`plan`/`apply`/`down`**.

## Resolved design decisions

Locked in for `v1.5.0`:

1. **Allowlist, not denylist** — fails closed; a new terraform verb is rejected until deliberately added here.
2. **Two independent gates** — the subcommand allowlist *and* the mutation-flag scrub; `state` carries a third (sub-verb) guard. A reject at any gate happens before terraform is exec'd.
3. **Phase-correct cwd/env via `tf.Open` only** — the CLI layer never re-derives the state dir or `TF_DATA_DIR`. Non-negotiable; it is the bug class this whole sprint is about.
4. **Never-applied workspace → clear error, zero side effects** — no source fetch, no `init`. An `OpenReadOnly`/option is added if `tf.Open` is not already side-effect-safe.
5. **`--on` rejected, not deferred** — state is workstation-local; remote dispatch is nonsensical here.
6. **Read-only is permanent** — no future "write mode" knob. Mutations are the lifecycle verbs' exclusive domain, forever.
7. **`tf` alias** via cobra `Aliases: []string{"tf"}`.

## Open questions

1. **`terraform console`** — interactive, read-only-ish, but can evaluate arbitrary expressions and (via provider data sources) make API calls. Not in the v1.5.0 allowlist; revisit only if a concrete user need appears, and only behind an explicit decision (it is the one allowlist candidate that is read-only-but-not-obviously-inert).
2. **`-json` normalization** — `roksbnkctl terraform output -json` works (the flag passes through), but should `roksbnkctl` ever offer a stable typed wrapper around specific outputs (e.g. a first-class "list jumphost IPs")? Out of scope here; if it happens it's a separate typed command, not a relaxation of this gate. PRD 09's auto-registration already removes the most common reason a user would script around `terraform output`.

## Out of scope

- **Any mutating terraform operation** — permanently. `apply`, `destroy`, `init`, `import`, `taint`/`untaint`, `state rm`/`mv`/`push`/`replace-provider`, write-mode `fmt`, `plan -out`, `-auto-approve`/`-replace`/`-target`/`-destroy` anywhere. This is the entire point of the gate; mutations are the lifecycle verbs' exclusive domain.
- **`--on <target>` remote dispatch** — state is workstation-local; explicitly rejected, not deferred.
- **A generalized terraform-policy engine** — no config-driven allowlist, no per-workspace policy knob. The allowlist is hardcoded; extending it is a deliberate code + PRD change.
- **`terraform console`** — see Open question 1.

## Cross-references

- [`issues/issue_sprint13_staff.md` Issue 2](../../issues/issue_sprint13_staff.md) — the implementation-ready design surface this PRD formalizes (allowlist, sub-verb guard, suggested file shape).
- [PRD 09 — per-AZ jumphost auto-registration](./09-AUTO-CLUSTER-JUMPHOSTS.md) — the headline use case (`terraform output testing_cluster_jumphost_ips`) and the IP-lookup one-liner the chapter 15/16 docs use.
- [PRD 01 — SSH client + `--on` flag](./01-SSH-AND-ON-FLAG.md) — the auto-jumphost seeding that running raw `apply` would skip; why mutations must stay orchestrated.
- [PRD 07 — deployed-tfvars snapshot](./07-DEPLOYED-TFVARS.md) — another post-apply artifact that an out-of-band `apply` would desync; same "lifecycle owns the apply" principle.
- [`internal/tf/terraform.go`](../../internal/tf/terraform.go) — `Open` (the cwd + `TF_DATA_DIR` plumbing to reuse), and where `RunReadOnly` / `OpenReadOnly` land.
- [`docs/PLAN.md` §"Sprint 13"](../PLAN.md) — the cycle frame and the integrator decisions this PRD reflects.
- [Chapter 15 §"Auto-discovery from terraform outputs"](../../book/src/15-ssh-targets.md) and [Chapter 16 §"Working examples"](../../book/src/16-on-flag-ssh-jumphosts.md) — the user-facing surface that uses the `roksbnkctl terraform output …` one-liners shipped by this PRD.
