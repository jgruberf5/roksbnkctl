# PRD 10 — Demo experience: `up --demo`, the launch renderer, and `demo run`

> **Status:** shipped. Builds on the scenarios framework ([PRD 09](09-SCENARIOS-FRAMEWORK.md)). Operator-facing surface for *presenting* BNK-on-AWS to an audience, as distinct from *validating* it.

## Why this PRD exists

awsbnkctl can stand up a real BNK-on-EKS cluster and prove it serves traffic. But **demoing** that to an audience, prior to this PRD, was a manual, fragile, off-tool ritual. Presenting the protocol use-cases (HTTP/2, gRPC, Diameter) by hand required:

- opening an EC2 Instance Connect (EICE) tunnel to the jumphost,
- pushing an ephemeral SSH key in 60-second windows (repeatedly),
- discovering the jumphost user is `ec2-user`, that `grpcurl` must be installed manually, and that the diameter client must be `scp`'d over,
- running `bnk resync` by hand to un-stale a TMM pool member before a call would succeed,
- and reading raw `curl`/`grpcurl` output with no narration for the audience.

None of that should touch the demoer's hands, and none of it should be visible to the audience except as intentional theater.

**Goal:** a demoer, from their laptop terminal, runs two commands —

```
awsbnkctl up   --demo -f cluster.yaml     # provision a real cluster, dressed for an audience
awsbnkctl demo run                         # (second terminal) narrated use-case demos
```

— and gets a polished, reliable, audience-legible demonstration of BNK running on AWS EKS.

## Design principle (non-negotiable)

**Demo mode is additive, never a different provisioning path.** `up --demo` provisions the *identical* real cluster `up` does (Phases 00–25, same EKS, same BNK). Demo mode only *adds*: pre-staging, a launch renderer, demo tagging, a status banner, and the `demo` command group. This is what makes the demo honest — "this isn't a toy stack, it's the real BNK-on-EKS deployment, just dressed for an audience."

## Prior art we build on

- **Scenarios framework (PRD 09):** `internal/scenarios/<name>/` packages implementing `Manifests()/Apply()/Verify()/Cleanup()`, self-registering, with per-run artifacts and an ASCII environment rendering. The scenarios runner already does manifest-render → SSA apply → `bnk resync` → SSH+EICE verify → narrate → clean. **The demo subsystem reuses this machinery; it does not reinvent it.**
- **Embedded demo assets:** validated manifests + clients are shipped via `//go:embed` in `internal/demo/{http2,diameter}/` packages.
- **Jumphost phase:** the multi-ENI jumphost + EICE is provisioned by `up` when `testing.jumphost.enabled`. Demo pre-staging runs as a new orchestration step *after* this phase (calling the `jumphost` leaf package over EICE) — it does not modify the AWS-provisioning jumphost phase itself, which keeps the `jumphost` package a dependency-free leaf (no cli/scenario imports).
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
- **gRPC demo** — `grpcurl` against the HTTPRoute path returns `RST_STREAM INTERNAL_ERROR` on server-reflection streams in the current BNK release; reflection is a bidi stream and is the fragile path through TMM. gRPC re-enters the catalogue once either (a) `grpcbin`'s `.proto` ships so `grpcurl` skips reflection, or (b) a reliable non-reflection unary path is validated.
- Full mdBook retarget (separate backlog item).

## Command surface

Mirrors the existing `scenarios {list,run,clean}` vocabulary so operators see one mental model:

```
awsbnkctl up --demo -f cluster.yaml     # provision + pre-stage client + launch renderer + DEMO marker
awsbnkctl demo list                      # show the use-cases + ratings (like `scenarios list`)
awsbnkctl demo run [usecase...]          # narrated run via the pre-staged client
awsbnkctl demo clean                     # remove demo workloads (also folded into `down`)
```

`demo run --all` runs the v1 catalogue in a presentation-friendly topo-sorted order. Naming chosen over a single `demo-start` verb because the `<group> <verb>` form matches `scenarios` and `forge`.

## `--demo` flag + persisted marker

`up --demo` persists its intent so later commands cohere:

- Writes `DEMO_MODE=true` (and `DEMO_STAGED_AT`, `DEMO_EXPIRY`) into `.awsbnkctl/<name>/state.env`.
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

**Tag mechanism:** the demo tags ride the existing `cl.Tags` map — injected at config-load when `demo.enabled`, so the `tags.Merge(tags.Required(...), cl.Tags, ...)` call already present in every phase carries them for free. Zero per-phase edits (the deletion-test winner over adding a new tag-map argument to ~15 `Merge` call-sites). The expiry tag is an **absolute** `awsbnkctl:demo-expiry=<RFC3339 UTC>` computed at `up --demo` from a default **1-day** window (overridable via `demo.ttl`, a Go duration); absolute rather than a duration so `status` can compute remaining time without separately knowing creation time.

## Jumphost pre-staging (during `up --demo`)

When `DEMO_MODE`, a new orchestration step after the jumphost phase (`Phase17dDemoStage`, wired in `runPhasedUp`) provisions the client demo-ready, idempotently — driven from the phases/cli layer through an exported `jumphost` staging wrapper (it mints+pushes the EICE key internally, since `prepareEICEKey` is unexported), so the `jumphost` package stays a leaf:

- ensure `curl` present (it is, on the AL2023 client),
- install `grpcurl` (v1.9.3) to a known PATH location (not `./grpcurl` in `$HOME`),
- copy the embedded python diameter client + responder,
- verify EICE reachability + the data-path ENI (the `10.0.10.x` source) end-to-end,
- record readiness (e.g. `DEMO_CLIENT_STAGED_AT`) in state.env.

Mechanism: reuse the scenario framework's SSH+EICE path (`internal/jumphost`, `ec2-user`), and embed client assets in the binary like scenario manifests. No interactive key dance in the operator's face — the tool manages EICE key push + tunnel internally, as the scenarios runner already does.

## `demo run` behavior

For each use-case, reuse the scenario lifecycle and add narration:

1. **Narrate the intent** ("→ Steering HTTP/2 (h2c) through the BNK VIP 10.0.10.111 — TMM will proxy HTTP/2 on both legs").
2. Render/apply manifests (embedded), wait for Programmed/Ready.
3. **Auto-resync** the relevant HTTPRoute(s) (`pkg/bnk.ResyncHTTPRoutes`) — built in, so the demoer never runs `bnk resync` by hand.
4. Run the client from the pre-staged jumphost; capture output.
5. **Narrate the result** with the audience-legible proof line (e.g. `HTTP/2 200 · backend saw HTTP/2.0`, `CER→CEA Result-Code=2001`), an ASCII data-path diagram, and a pointer to what forge shows.
6. Leave workloads up for Q&A; `demo clean` / `down` removes them.

Narration must be legible projected on a screen: short lines, clear ✓/✗, no raw verbose dumps unless `-v`.

## Launch renderer (rocket theme)

An ASCII / TUI staging renderer in the CLI (not an HTML page). Rationale: everything is terminal-on-laptop, so a browser page is friction; forge already provides the live visual component view, so an HTML launch page would duplicate it. The launch *theater* belongs where the action is.

It's a presentation layer over the per-phase progress the tool already emits, mapping Phases 00–25 onto labelled "stages," gated by `--demo` so normal/CI runs keep clean, parseable output:

```
   awsbnkctl ▸ <cluster-name> ▸ DEMO LAUNCH
   T-00:00  🚀 LIFTOFF — preflight green
   ██████████  STAGE 1  VPC · subnets · IGW · NAT          [Phase 00–07]  ✓ 38s
   ██████░░░░  STAGE 2  EKS control plane (main engine)    [Phase 08–08b]  ⏳
   ──────────  STAGE 3  Nodes · kubeconfig · ENIs · jumphost [Phase 10–18]
   ──────────  STAGE 4  BNK supply chain · activation      [Phase 11b–25]
   ──────────  ORBIT    CNEInstance ready · TMM ready · VIP live
```

Constraints: must degrade gracefully on a non-TTY / piped output (fall back to the plain phase log); honor `--no-color`; never swallow a phase error behind the animation (errors print clearly and abort).

## Constraints from real-world testing

- **`status` kubeconfig fix** (PR #66): `status` used the host default kubeconfig, not the `--config` cluster's `state.env` `KUBECONFIG_PATH`. `resolveKubeconfigForStatus(st)` now reads the targeted cluster, so the DEMO banner can reuse the same `*state.State` `loadStatusState` produces.
- **Cluster-targeting flag unification** (PR #66): `-f/--config` is now consistent across the surface; `bnk resync` gained `--config`. `demo run` accepts `-f/--config` like the rest.
- **Pool-member staleness is real and recurring:** `demo run` MUST auto-resync; do not rely on the operator. See [`docs/upstream-issues/cne-controller-endpointslice-not-watched.md`](../../upstream-issues/cne-controller-endpointslice-not-watched.md).
- **gRPC reflection fragility:** see Deferred. Do not ship a demo that can `RST_STREAM` in front of an audience.

## Acceptance criteria

1. `awsbnkctl up --demo -f <cfg>` provisions the same cluster as a normal `up`, writes `DEMO_MODE=true` to state.env, tags resources `awsbnkctl:demo=true`, and renders the launch sequence (with graceful non-TTY fallback).
2. After that `up`, the jumphost has `grpcurl` on PATH, the diameter client/responder staged, and EICE verified — with **zero** manual SSH/scp/key steps by the operator.
3. `awsbnkctl demo list` shows the v1 catalogue with ratings; `awsbnkctl demo run` runs http2 + diameter + the green scenarios, each with narrated intent + proof line + ASCII diagram, auto-resyncing pool members.
4. `awsbnkctl status` on a demo cluster prints the `⚠ DEMO` banner (reading the correct cluster's state).
5. `demo run` refuses on a non-demo cluster with a clear message.
6. `awsbnkctl down` runs `demo clean` — enumerating the registered demo use-cases and calling each idempotent `Cleanup` — before tearing down infra, and **succeeds even if `demo run` was never invoked** (no error on absent workloads/namespaces, no orphans left).
7. Normal (non-`--demo`) `up`/CI output is unchanged (no rocket theme, parseable).
8. The demo manifests + clients (diameter client/responder, gRPC `.proto`) are embedded via `//go:embed` in `internal/demo/` and shipped in the binary — `demo run` works regardless of any local-only artifacts under `docs/demo/`.

## Decomposition

> **Build base:** all development branches are cut from `staging` (which contains PR #66 — `resolveKubeconfigForStatus`, unified `-f/--config`, `bnk resync --config`).

- **Slice A1 — `--demo` flag + `demo:` config block + state marker + validation.** Add the `--demo` flag (a single-owner cobra var in `lifecycle.go`); a `DemoSpec` / `demo:` block in `intent/cluster.go` mirroring `ForgeSpec` / `TestingSpec` (with `applyDefaults` + `validate`); write `DEMO_MODE=true` (+ `DEMO_STAGED_AT`, `DEMO_EXPIRY`) to `.awsbnkctl/<name>/state.env` via `state.State.Set`; enforce "`--demo` implies `testing.jumphost.enabled`, error if explicitly false." Config load uses `decodeStrict` with `KnownFields(true)`, so any example `cluster.yaml` carrying a `demo:` block must land in the same change as the `DemoSpec` struct field, or loading fails.
- **Slice A2 — demo + TTL AWS tagging.** Inject `awsbnkctl:demo=true` + `awsbnkctl:demo-expiry=<RFC3339>` into `cl.Tags` at config-load when `demo.enabled`, so the existing `tags.Merge(tags.Required(...), cl.Tags, ...)` in every phase carries them. No per-phase edits, no new `Merge` argument. Depends A1.
- **Slice A3 — `status` DEMO banner + warn-only TTL notice.** Read `DEMO_MODE` / `DEMO_EXPIRY` from the `*state.State` `loadStatusState` already produces — this is correct regardless of #66, because `loadStatusState` resolves the targeted cluster's `state.env` via `--config` → `cl.StateDir()`. Print the `⚠ DEMO` banner + a warn-only "expires in N days / **EXPIRED**" line; no automatic teardown. Distinct from the cluster-reachability probe in `inspect.go`, which uses the kubeconfig: on the `staging` build base #66's `resolveKubeconfigForStatus(st)` already points the probe at the right cluster. Depends A1.
- **Slice Embed — promote demo assets via `//go:embed`.** Move into embedded `internal/demo/<name>/` packages (same `//go:embed` pattern as every scenario package and `internal/k8s/manifests/embed.go`): only the http2/diameter manifests + the python diameter client/responder + the gRPC `.proto`. Explicitly exclude any non-embedded artifacts (large HTML walkthroughs, diagram source images, etc.) — do not bloat the binary. Owns AC #8. Prereq for B and C; can start in parallel with A.
- **Slice B — jumphost demo pre-staging.** A new orchestration step after the jumphost phase (`Phase17dDemoStage` in `runPhasedUp`, between Phase17b and Phase18, under `DEMO_MODE`) that: ensures `curl`; installs `grpcurl` v1.9.3 to a PATH location; copies the embedded diameter client/responder; verifies EICE + the `10.0.10.x` data-path ENI; records `DEMO_CLIENT_STAGED_AT`. **Deliverable:** a new exported `jumphost` staging wrapper (`RunStagingCommands` + `CopyFileViaEICE`) — because `prepareEICEKey` is unexported and `SSHRunViaEICE` requires a caller-supplied `keyPath`, the wrapper must mint+push the EICE key internally, re-pushing per step for the ~60s key TTL. Keep all orchestration in the phases/cli layer — the wrapper lives in the `jumphost` leaf but takes no cli/scenario imports. Depends A1 + Embed.
- **Slice C0 — `demo {list,run,clean}` command group + narration module + `demo clean` enumerator.** Mirror `cli/scenarios.go` (topo-sort → `scenarios.NewContext` → `scenarios.Run`); extract a small narration module shared by C1/C2 and D; define `demo clean` to iterate the registered demo use-cases and call each contractually-idempotent `Cleanup` (safe over absent namespaces, since `demo run` deploys on demand); wire it into `runPhasedDown` before infra teardown. Makes AC #6 testable. Depends B.
- **Slice C1 / C2 — http2 + diameter use-cases.** Each implements the `scenarios.Scenario` interface and self-registers, owns a **distinct namespace** (so `demo clean` can `Cleanup` it idempotently — see C0), applies embedded manifests via `scenarios.ApplyManifests` (SSA, `Force:true` — never `applyRawYAML`), runs its client through Slice B's exported `jumphost` staging wrapper (not the HTTP-only `RunCurlProbes`, not `SSHRunViaEICE` directly), and copies `httproutee2e.Verify`'s settle→`ResyncHTTPRoutes`→probe ordering exactly. Each demo owns a dedicated VIP via a `const scnVIP = "10.0.10.<N>"`; this avoids colliding with scenario-suite VIPs (`.100` / `.101` / `.102` / `.103`). Depend C0 + Slice B's wrapper.
- **Slice D — green scenarios in `demo run`.** Wrap the existing green scenarios with the C0 narration layer so they present alongside the protocol demos. Implemented via `demo.Catalogue()` + `demo.FindInCatalogue(name)` — a CLI-presentation-only union that keeps the demo and scenarios registries disjoint at the data-model layer. Thin once C0 exists. Depends C0.
- **Slice E — launch renderer.** Build the rocket-themed staging UI in `internal/ui/` + a stage-label hook around the `phases.PhaseNN(...)` calls in `runPhasedUp` (the `stage(num, name, fn) error` closure); non-TTY (`term.IsTerminal`) + `--no-color` fallback to the plain `[phase NN]` log; never swallow a phase error behind the animation. **Shares `lifecycle.go` / `runPhasedUp` with A1 + B — serialize those edits; state-independent of C/D but not file-independent.**
- **Slice F (deferred) — gRPC use-case.** Ship grpcbin's `.proto` / a non-reflection unary path; re-add once reliable.

## Resolved decisions

1. **Workload deploy timing:** `demo run` applies the demo namespaces/VIPs on demand (re-runnable; `up --demo` stays focused on infra + client pre-staging, not workloads).
2. **Package layout:** a thin `internal/demo/` package tree that composes the scenario lifecycle — keeps "validate" (scenarios) vs "present" (demo) separable. Confirmed viable against the live `Scenario` interface — no framework fork.
3. **Auto-expiry:** demo clusters carry a TTL/expiry tag alongside `awsbnkctl:demo=true`, surfaced as a warn-only notice in `status` (no automatic teardown). Format: absolute `awsbnkctl:demo-expiry=<RFC3339 UTC>`, default 1-day window, overridable via `demo.ttl`.
4. **Flag ergonomics:** `-f` shorthand + keep the `--config` long name (shipped via PR #66).
5. **Tag threading:** demo + TTL tags ride the existing `cl.Tags` map (injected at config-load), not a new `Merge` argument — zero per-phase edits.
6. **Pre-staging placement:** a new orchestration step after the jumphost phase, not an edit inside the AWS-provisioning jumphost phase; the `jumphost` package stays a dependency-free leaf.
7. **`demo clean` semantics:** enumerates the registered demo use-cases and calls each idempotent `Cleanup`; safe when `demo run` never ran.
8. **`lifecycle.go` serialization:** A1, B, and E all edit `runPhasedUp` — A1 lands first; B's and E's edits to that function are serialized after.
9. **Jumphost staging wrapper:** `prepareEICEKey` is unexported and `SSHRunViaEICE` needs a caller-supplied key — so the exported `jumphost` wrapper (mints/pushes the key internally, re-pushing per the ~60s TTL) is a real deliverable of Slice B, shared by C1/C2. Demo packages and cli call the wrapper, never `SSHRunViaEICE` directly.
10. **Build base:** all branches are cut from `staging` (which has PR #66), never from `main`.
