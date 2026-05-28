# PRD 10 — Demo experience: `up --demo`, the launch renderer, and `demo run`

> **Status:** draft — authored 2026-05-27 during the operator tool-verification session on the live `syd-tracer` lab. Builds directly on the scenarios framework ([PRD 09](09-SCENARIOS-FRAMEWORK.md)). Operator-facing surface for *presenting* BNK-on-AWS to an audience, as distinct from *validating* it. **Decomposition Architect-validated 2026-05-27** (verdict: NEEDS REVISION → revised below; the A→B→C→D / E / F spine is sound, the thin-`internal/demo/` composition is confirmed viable against the live code).

## Why this PRD exists

awsbnkctl can stand up a real BNK-on-EKS cluster and prove it serves traffic. But **demoing** that to an audience today is a manual, fragile, off-tool ritual. In the 2026-05-27 session, presenting the three protocol use-cases (HTTP/2, gRPC, Diameter) by hand required:

- opening an EC2 Instance Connect (EICE) tunnel to the jumphost,
- pushing an ephemeral SSH key in 60-second windows (repeatedly),
- discovering the jumphost user is `ec2-user`, that `grpcurl` lives at `./grpcurl`, and that the diameter client must be `scp`'d over,
- hunting the right cluster-targeting flag (`--config` vs positional vs `--kubeconfig`),
- running `bnk resync` by hand to un-stale a TMM pool member before a call would succeed,
- and reading raw `curl`/`grpcurl` output with no narration for the audience.

None of that should touch the demoer's hands, and none of it should be visible to the audience except as intentional theater. The demos themselves currently live under `docs/demo/` which is **gitignored** — throwaway local artifacts, not a shippable product surface.

**Goal:** a demoer, from their laptop terminal, runs two commands —

```
awsbnkctl up   --demo -f cluster.yaml     # provision a real cluster, dressed for an audience
awsbnkctl demo run                         # (second terminal) narrated use-case demos
```

— and gets a polished, reliable, audience-legible demonstration of BNK running on AWS EKS, with forge open alongside for the live component view.

## Design principle (non-negotiable)

**Demo mode is additive, never a different provisioning path.** `up --demo` provisions the *identical* real cluster `up` does (Phases 00–25, same EKS, same BNK). Demo mode only *adds*: pre-staging, a launch renderer, demo tagging, a status banner, and the `demo` command group. This is what makes the demo honest — "this isn't a toy stack, it's the real BNK-on-EKS deployment, just dressed for an audience."

## Prior art we build on

- **Scenarios framework (PRD 09):** `internal/scenarios/<name>/` packages implementing `Manifests()/Apply()/Verify()/Cleanup()`, self-registering, with per-run artifacts and an ASCII environment rendering. The scenarios runner already does manifest-render → SSA apply → `bnk resync` → SSH+EICE verify → narrate → clean, and it auto-resynced flawlessly in the 2026-05-27 session. **The demo subsystem reuses this machinery; it does not reinvent it.**
- **`docs/demo/{http2,grpc,diameter}/`:** validated manifests + clients + captured proofs. v1 of this PRD is largely "promote these gitignored artifacts into a shipped, embedded subsystem," exactly as the scenarios were ported from kindbnkctl.
- **Jumphost phase (PRD 09 slice-12):** the multi-ENI jumphost + EICE already exists and is provisioned by `up` when `testing.jumphost.enabled`. Demo pre-staging runs as a **new orchestration step *after*** this phase (calling the `jumphost` leaf package over EICE) — it does **not** modify the AWS-provisioning jumphost phase itself, which keeps the `jumphost` package a dependency-free leaf (no cli/scenario imports). *(Architect correction 2026-05-27: the earlier "extends this phase" framing risked dragging cli/scenario deps into the leaf.)*
- **Forge integration:** the component/GUI view. The demoer drives forge separately; this PRD does not change forge, but `demo run` narration should reference what forge will show.

## Scope

### In scope (v1)
- `up --demo` flag + persisted demo marker + optional `demo:` config block.
- Jumphost **pre-staging** of demo client tooling during `up --demo`.
- The **launch renderer** (rocket-themed progress UI) gated by `--demo`.
- A **`demo {list,run,clean}`** command group (sibling to `scenarios`).
- **Narration** layer for `demo run` output.
- v1 use-case catalogue: **`http2`, `diameter`, and the green `scenarios`** suite.

### Deferred (not v1)
- **gRPC demo** — `grpcurl` against the working HTTPRoute path (`:50052`) failed live on 2026-05-27 with `RST_STREAM INTERNAL_ERROR` on the server-reflection stream, even after `bnk resync` (the grpcbin pod had been recreated since the proof was captured). gRPC reflection is a bidi stream and is the fragile path through TMM. gRPC re-enters the catalogue once we either (a) ship grpcbin's `.proto` so `grpcurl` skips reflection, or (b) validate a reliable non-reflection unary path. Tracked separately.
- Full mdBook retarget (separate backlog item).

## Command surface

Mirrors the existing `scenarios {list,run,clean}` vocabulary so operators see one mental model:

```
awsbnkctl up --demo -f cluster.yaml     # provision + pre-stage client + launch renderer + DEMO marker
awsbnkctl demo list                      # show the use-cases + ratings (like `scenarios list`)
awsbnkctl demo run [usecase...]          # narrated run via the pre-staged client; default = all v1 use-cases
awsbnkctl demo clean                     # remove demo workloads (also folded into `down`)
```

`demo run` with no argument runs the v1 catalogue in a presentation-friendly order. Naming chosen over a single `demo-start` verb because the `<group> <verb>` form matches `scenarios` and `forge`.

## `--demo` flag + persisted marker

`up --demo` must persist its intent so later commands cohere:

- Writes `DEMO_MODE=true` (and e.g. `DEMO_STAGED_AT`) into `.awsbnkctl/<name>/state.env`.
- Also expressible declaratively as a `demo: { enabled: true }` block in `cluster.yaml` (mirrors the existing `forge:` / `testing:` blocks) for reproducibility; the flag is sugar that sets it.
- `--demo` implies `testing.jumphost.enabled` (the demos need the jumphost) — error clearly if the operator combined `--demo` with `testing.jumphost.enabled: false`.

What the marker gates:
| Surface | normal `up` | `up --demo` |
|---|---|---|
| Provisioning phases | 00–25 | **identical** |
| Jumphost client tooling | not staged | **pre-staged** (see below) |
| Progress UI | plain phase log | launch renderer |
| AWS tags | standard | `+ awsbnkctl:demo=true` + `awsbnkctl:demo-expiry=<RFC3339>` |
| `status` | normal | `⚠ DEMO — not a production deployment` banner + warn-only TTL/expiry notice |
| `demo run` | refuses (guard: "not a demo cluster") | enabled |
| `down` | tears down infra | also runs `demo clean` first |

**Tag mechanism (resolved):** the demo tags ride the **existing `cl.Tags` map** — injected at config-load when `demo.enabled`, so the `tags.Merge(tags.Required(...), cl.Tags, ...)` call already present in every phase carries them for free. This is **zero per-phase edits** (the deletion-test winner over adding a new tag-map argument to ~15 `Merge` call-sites). The expiry tag is an **absolute** `awsbnkctl:demo-expiry=<RFC3339 UTC>` computed at `up --demo` from a default **1-day** window (overridable via `demo.ttl`, a Go duration); absolute rather than a duration so `status` can compute remaining time without separately knowing creation time.

## Jumphost pre-staging (during `up --demo`)

When `DEMO_MODE`, a **new orchestration step after** the jumphost phase (e.g. `Phase17dDemoStage`, wired in `runPhasedUp`) provisions the client demo-ready, idempotently — driven from the phases/cli layer through a **new exported `jumphost` staging wrapper** (it mints+pushes the EICE key internally, since `prepareEICEKey` is unexported; see Slice B), so the `jumphost` package stays a leaf:

- ensure `curl` present (it is, on the AL2023 client),
- install `grpcurl` (v1.9.3) to a known PATH location (not `./grpcurl` in `$HOME`),
- copy the embedded python diameter client + responder and the gRPC `.proto`,
- verify EICE reachability + the data-path ENI (the `10.0.10.x` source) end-to-end,
- record readiness (e.g. `DEMO_CLIENT_STAGED_AT`) in state.env.

Mechanism: reuse the scenario framework's SSH+EICE path (`internal/jumphost`, `ec2-user`), and embed client assets in the binary like scenario manifests. **No interactive key dance in the operator's face** — the tool manages EICE key push + tunnel internally, as the scenarios runner already does.

## `demo run` behavior

For each use-case, reuse the scenario lifecycle and add narration:

1. **Narrate the intent** ("→ Steering HTTP/2 (h2c) through the BNK VIP 10.0.10.111 — TMM will proxy HTTP/2 on both legs").
2. Render/apply manifests (embedded), wait for Programmed/Ready.
3. **Auto-resync** the relevant HTTPRoute(s) (`pkg/bnk.ResyncHTTPRoutes`) — built in, so the demoer never runs `bnk resync` by hand.
4. Run the client from the pre-staged jumphost; capture output.
5. **Narrate the result** with the audience-legible proof line (e.g. `HTTP/2 200 · backend saw HTTP/2.0`, `CER→CEA Result-Code=2001`), an ASCII data-path diagram (the scenarios framework already renders one), and a pointer to what forge shows.
6. Leave workloads up for Q&A; `demo clean` / `down` removes them.

Narration must be legible projected on a screen: short lines, clear ✓/✗, no raw verbose dumps unless `-v`.

## Launch renderer (rocket theme)

**Recommendation: an ASCII / TUI staging renderer in the CLI** (not an HTML page). Rationale: everything is terminal-on-laptop, so a browser page is friction; forge already provides the live visual component view, so an HTML launch page would duplicate it. The launch *theater* belongs where the action is.

It's a presentation layer over the per-phase progress the tool already emits, mapping Phases 00–25 onto labelled "stages," gated by `--demo` so normal/CI runs keep clean, parseable output:

```
   awsbnkctl ▸ syd-tracer ▸ DEMO LAUNCH
   T-00:00  🚀 LIFTOFF — preflight green
   ██████████  STAGE 1  VPC · subnets · IGW · NAT          [Phase 00–05]  ✓ 38s
   ██████░░░░  STAGE 2  EKS control plane (main engine)    [Phase 06–08]  ⏳
   ──────────  STAGE 3  node group · kubeconfig            [Phase 09–11]
   ──────────  STAGE 4  BNK supply chain · activation      [Phase 12–24]
   ──────────  ORBIT    CNEInstance 18/18 · TMM 7/7 · VIP live
```

Constraints: must degrade gracefully on a non-TTY / piped output (fall back to the plain phase log); honor `--no-color`; never swallow a phase error behind the animation (errors print clearly and abort).

## Inputs from the 2026-05-27 live session (must inform the build)

- **`status` kubeconfig bug** (fixed — PR #66, landed on `staging`): `status` used the host default kubeconfig, not the `--config` cluster's `state.env` `KUBECONFIG_PATH`. `resolveKubeconfigForStatus(st)` now reads the targeted cluster, so the DEMO banner can reuse the same `*state.State` `loadStatusState` produces.
- **Cluster-targeting flag unification** (PR #66, landed): `-f/--config` is now consistent; `bnk resync` gained `--config`. `demo run` should accept `-f/--config` like the rest.
- **Pool-member staleness is real and recurring:** `demo run` MUST auto-resync; do not rely on the operator.
- **gRPC reflection fragility:** see Deferred. Do not ship a demo that can `RST_STREAM` in front of an audience.

## Acceptance criteria

1. `awsbnkctl up --demo -f examples/syd-tracer/cluster.yaml` provisions the same cluster as a normal `up`, writes `DEMO_MODE=true` to state.env, tags resources `awsbnkctl:demo=true`, and renders the launch sequence (with graceful non-TTY fallback).
2. After that `up`, the jumphost has `grpcurl` on PATH, the diameter client/responder + proto staged, and EICE verified — with **zero** manual SSH/scp/key steps by the operator.
3. `awsbnkctl demo list` shows the v1 catalogue with ratings; `awsbnkctl demo run` runs http2 + diameter + the green scenarios, each with narrated intent + proof line + ASCII diagram, auto-resyncing pool members.
4. `awsbnkctl status` on a demo cluster prints the `⚠ DEMO` banner (reading the correct cluster's state).
5. `demo run` refuses on a non-demo cluster with a clear message.
6. `awsbnkctl down` runs `demo clean` — enumerating the registered demo use-cases and calling each idempotent `Cleanup` — before tearing down infra, and **succeeds even if `demo run` was never invoked** (no error on absent workloads/namespaces, no orphans left).
7. Normal (non-`--demo`) `up`/CI output is unchanged (no rocket theme, parseable).
8. The demo manifests + clients (diameter client/responder, gRPC `.proto`) are embedded via `//go:embed` in `internal/demo/` and shipped in the binary — `demo run` works with `docs/demo/` absent/gitignored.

## Decomposition (Architect-validated 2026-05-27 — two passes — revised)

> **Pass 1** (against `staging`, post-#66): original Slice A→F sound in spine but under-specified — fixed by **splitting Slice A** (it hid a cross-cutting tag-threading fork), adding an explicit **Embed/assets** slice (owns AC #8), and **defining the `demo clean` enumerator** (makes AC #6 testable). The thin-`internal/demo/` composition over the existing `Scenario` interface is confirmed viable (no framework fork).
> **Pass 2** (independent, run against the pre-rebase doc branch): caught that the jumphost key-mint (`prepareEICEKey`) is **unexported** — so Slice B/C need a new exported `jumphost` wrapper (corrected below); tightened the Embed scope, the `demo:`-block load order, and the A3 banner-vs-probe distinction; and confirmed the `cl.Tags` seam is safe (teardown discovers by `awsbnkctl:cluster`/`:component` only, and `cl.Tags` never round-trips to disk). It also surfaced that this doc branch had been cut from `main` (missing #66) — since corrected by rebasing onto `staging`.
>
> **Build base (load-bearing):** all development branches are cut from **`staging`** (never `main`), which already contains PR #66 (`resolveKubeconfigForStatus`, unified `-f/--config`, `bnk resync --config`). This PRD branch has been rebased onto `staging`, so #66 is now in its history. Cut slice branches from `staging`.

- **Slice A1 — `--demo` flag + `demo:` config block + state marker + validation.** Add the `--demo` flag (a **single-owner** cobra var in `lifecycle.go` — heed the shared-flag-var anti-pattern warnings already in that file); a `DemoSpec` / `demo:` block in `intent/cluster.go` mirroring `ForgeSpec` / `TestingSpec` (with `applyDefaults` + `validate`); write `DEMO_MODE=true` (+ `DEMO_STAGED_AT`, `DEMO_EXPIRY`) to `.awsbnkctl/<name>/state.env` **via `state.State.Set`** (precedent: `phase17b_jumphost.go`); enforce "`--demo` implies `testing.jumphost.enabled`, error if explicitly false." **Sequencing:** config load uses `decodeStrict` with `KnownFields(true)`, so any example `cluster.yaml` carrying a `demo:` block must land **in the same change** as the `DemoSpec` struct field, or loading fails. No contested decisions — smallest safe first slice. Build base = `staging` (already has PR #66); A1 itself needs no #66 symbol.
- **Slice A2 — demo + TTL AWS tagging.** Inject `awsbnkctl:demo=true` + `awsbnkctl:demo-expiry=<RFC3339>` into `cl.Tags` at config-load when `demo.enabled`, so the existing `tags.Merge(tags.Required(...), cl.Tags, ...)` in every phase carries them. **No per-phase edits, no new `Merge` argument.** Depends A1.
- **Slice A3 — `status` DEMO banner + warn-only TTL notice.** Read `DEMO_MODE` / `DEMO_EXPIRY` from the `*state.State` `loadStatusState` already produces — this is correct **regardless of #66**, because `loadStatusState` resolves the targeted cluster's `state.env` via `--config` → `cl.StateDir()`. Print the `⚠ DEMO` banner + a warn-only "expires in N days / **EXPIRED**" line; no automatic teardown. *(Distinct from the cluster-reachability **probe** in `inspect.go`, which uses the kubeconfig: on the `staging` build base #66's `resolveKubeconfigForStatus(st)` already points the probe at the right cluster, satisfying AC #4's "correct cluster" for the probe too. The banner-read does not depend on it.)* Depends A1.
- **Slice Embed — promote demo assets via `//go:embed`.** Move into embedded `internal/demo/<name>/` packages (same `//go:embed` pattern as every scenario package and `internal/k8s/manifests/embed.go`): **only** the `docs/demo/{http2,diameter}` manifests + the python diameter client/responder + the gRPC `.proto`. **Explicitly exclude** `docs/demo/{grpc,img,diagrams}/` and the ~1.3 MB HTML walkthrough — do not bloat the binary. **Owns AC #8.** Prereq for B and C; can start in parallel with A.
- **Slice B — jumphost demo pre-staging.** A **new orchestration step** after the jumphost phase (e.g. `Phase17dDemoStage` in `runPhasedUp`, between Phase17b and Phase18, under `DEMO_MODE`) that: ensures `curl`; installs `grpcurl` v1.9.3 to a PATH location; copies the embedded diameter client/responder + proto; verifies EICE + the `10.0.10.x` data-path ENI; records `DEMO_CLIENT_STAGED_AT`. **Deliverable:** a **new exported `jumphost` staging wrapper** (e.g. `RunStagingCommands`) — because `prepareEICEKey` is *unexported* and `SSHRunViaEICE` requires a caller-supplied `keyPath`, the wrapper must mint+push the EICE key internally (mirroring the existing `StartHTTPResponder`), **re-pushing per step for the ~60s key TTL**. Keep all orchestration in the phases/cli layer — the wrapper lives in the `jumphost` leaf but takes no cli/scenario imports. Depends A1 + Embed.
- **Slice C0 — `demo {list,run,clean}` command group + narration module + `demo clean` enumerator.** Mirror `cli/scenarios.go` (topo-sort → `scenarios.NewContext` → `scenarios.Run`); extract a small narration module shared by C1/C2 and D; define `demo clean` to iterate the registered demo use-cases and call each contractually-idempotent `Cleanup` (safe over absent namespaces, since `demo run` deploys on demand); wire it into `runPhasedDown` before infra teardown. **Makes AC #6 testable.** Depends B.
- **Slice C1 / C2 — http2 + diameter use-cases.** Each implements the `scenarios.Scenario` interface and self-registers, owns a **distinct namespace** (so `demo clean` can `Cleanup` it idempotently — see C0), applies embedded manifests via `scenarios.ApplyManifests` (SSA, `Force:true` — never `applyRawYAML`), runs its client through **Slice B's new exported `jumphost` staging wrapper** (NOT the HTTP-only `RunCurlProbes`, and not `SSHRunViaEICE` directly — same unexported-key reason), and copies `httproutee2e.Verify`'s settle→`ResyncHTTPRoutes`→probe ordering exactly. Depend C0 (+ Slice B's wrapper).
- **Slice D — green scenarios in `demo run`.** Wrap the existing green scenarios with the C0 narration layer so they present alongside the protocol demos. Thin once C0 exists. Depends C0.
- **Slice E — launch renderer.** Build the rocket-themed staging UI in `internal/ui/` (**today an empty stub — built from scratch, not layered over an existing seam**) + a stage-label hook around the `phases.PhaseNN(...)` calls in `runPhasedUp`; non-TTY (`term.IsTerminal`) + `--no-color` fallback to the plain `[phase NN]` log; never swallow a phase error behind the animation. **Shares `lifecycle.go` / `runPhasedUp` with A1 + B — serialize those edits; state-independent of C/D but not file-independent.**
- **Slice F (deferred) — gRPC use-case.** Ship grpcbin's `.proto` / a non-reflection unary path; re-add once reliable (live `RST_STREAM` evidence stands).

## Resolved decisions (operator 2026-05-27 + Architect validation 2026-05-27)

1. **Workload deploy timing:** `demo run` applies the demo namespaces/VIPs **on demand** (re-runnable; `up --demo` stays focused on infra + client pre-staging, not workloads).
2. **Package layout:** a thin **`internal/demo/`** package tree that *composes* the scenario lifecycle — keeps "validate" (scenarios) vs "present" (demo) separable. *(Confirmed viable against the live `Scenario` interface — no framework fork.)*
3. **Auto-expiry:** demo clusters carry a **TTL/expiry tag** alongside `awsbnkctl:demo=true`, surfaced as a **warn-only** notice in `status` (no automatic teardown). **Format:** absolute `awsbnkctl:demo-expiry=<RFC3339 UTC>`, default **1-day** window, overridable via `demo.ttl`. *(Operator-decided 2026-05-27: 1 day — it's a demo, not a long-lived cluster.)*
4. **Flag ergonomics:** **`-f`** shorthand + keep the `--config` long name (shipped via PR #66).
5. **Tag threading (Architect):** demo + TTL tags ride the existing `cl.Tags` map (injected at config-load), **not** a new `Merge` argument — zero per-phase edits.
6. **Pre-staging placement (Architect):** a new orchestration step *after* the jumphost phase, not an edit inside the AWS-provisioning jumphost phase; the `jumphost` package stays a dependency-free leaf.
7. **`demo clean` semantics (Architect):** enumerates the registered demo use-cases and calls each idempotent `Cleanup`; safe when `demo run` never ran.
8. **`lifecycle.go` serialization (Architect):** A1, B, and E all edit `runPhasedUp` — land A1 first, then serialize B's and E's edits to that function (E is state-independent of C/D but shares this file, so it is **not** freely parallel there).
9. **Jumphost staging wrapper (Architect pass 2):** `prepareEICEKey` is unexported and `SSHRunViaEICE` needs a caller-supplied key — so a **new exported `jumphost` wrapper** (mints/pushes the key internally, re-pushing per the ~60s TTL) is a real deliverable of Slice B, **shared by C1/C2**. Demo packages and cli call the wrapper, never `SSHRunViaEICE` directly.
10. **Build base (Architect pass 2 + operator):** all branches are cut from `staging` (which has PR #66), never from `main`. This PRD branch was mistakenly cut from `main` and has been rebased onto `staging` to fix it.
