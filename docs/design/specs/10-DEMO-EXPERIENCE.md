# Demo experience: `up --demo`, the launch renderer, and `demo run`

The demo experience is the operator-facing surface for *presenting* BNK-on-AWS to
an audience, as distinct from *validating* it. It builds on the
[scenarios framework](09-SCENARIOS-FRAMEWORK.md): a demoer runs two commands and
gets a polished, reliable, audience-legible demonstration of BNK running on AWS
EKS.

## Overview

awsbnkctl can stand up a real BNK-on-EKS cluster and prove it serves traffic. The
demo experience makes *showing* that to an audience equally turnkey. Without it,
presenting the protocol use-cases (HTTP/2, Diameter) by hand means opening an EC2
Instance Connect (EICE) tunnel to the jumphost, pushing ephemeral SSH keys in
60-second windows, installing `grpcurl` and `scp`-ing a Diameter client by hand,
running `bnk resync` to un-stale a TMM pool member before a call succeeds, and
reading raw `curl` output with no narration. None of that should touch the
demoer's hands or be visible to the audience except as intentional theater.

A demoer runs two commands:

```
awsbnkctl up   --demo -f cluster.yaml     # provision a real cluster, dressed for an audience
awsbnkctl demo run    -f cluster.yaml     # (second terminal) narrated use-case demos
```

**Demo mode is additive, never a different provisioning path.** `up --demo`
provisions the *identical* real cluster `up` does (same EKS, same BNK). Demo mode
only adds pre-staging of client tooling, a launch renderer, demo tagging, a status
banner, and the `demo` command group. That's what makes the demo honest — it's
the real BNK-on-EKS deployment, just dressed for an audience.

## Using it

The command surface mirrors `scenarios {list,run,clean}` so operators see one
mental model:

```
awsbnkctl up --demo -f cluster.yaml      # provision + pre-stage client + launch renderer + DEMO marker
awsbnkctl demo list                       # show the use-cases + ratings + descriptions
awsbnkctl demo run [name] -f cluster.yaml # narrated run via the pre-staged client
awsbnkctl demo clean [name] -f cluster.yaml
```

- `demo run --all` runs the catalogue in a presentation-friendly topo-sorted
  order; `demo clean --all` invokes `Cleanup` on every registered use-case.
- `demo run` and `demo clean` both require `-f, --config` and accept `--dry-run`
  (render manifests only, without touching the cluster).
- `demo run` refuses on a non-demo cluster with a clear message
  (`run 'awsbnkctl up --demo' first`).
- `demo clean` is also folded into `down`, which runs it before tearing down
  infra.

### The `--demo` flag and the demo marker

`up --demo` persists its intent so later commands cohere:

- It writes `DEMO_MODE=true` (plus `DEMO_STAGED_AT` and `DEMO_EXPIRY`) into
  `.awsbnkctl/<name>/state.env` before the provisioning phase graph, so a
  partially-failed `up` still records the cluster as a demo.
- The same intent is expressible declaratively as a `demo: { enabled: true }`
  block in `cluster.yaml` (mirroring the existing `forge:` / `testing:` blocks)
  for reproducibility; the flag is sugar that sets it.
- `--demo` implies `testing.jumphost.enabled` (the demos need the jumphost) and
  errors clearly if combined with `testing.jumphost.enabled: false`.

What the marker gates:

| Surface | normal `up` | `up --demo` |
|---|---|---|
| Provisioning phases | full graph | **identical** |
| Jumphost client tooling | not staged | **pre-staged** (see below) |
| Progress UI | plain phase log | launch renderer |
| AWS tags | standard | `+ awsbnkctl:demo=true` + `awsbnkctl:demo-expiry=<RFC3339>` |
| `status` | normal | `⚠ DEMO — not a production deployment` banner + warn-only expiry notice |
| `demo run` | refuses (not a demo cluster) | enabled |
| `down` | tears down infra | also runs `demo clean` first |

The demo tags ride the existing `cl.Tags` map — injected at config-load when
`demo.enabled`, so the `tags.Merge(...)` call already present in every phase
carries them onto every resource with no per-phase edits. The expiry tag is an
absolute `awsbnkctl:demo-expiry=<RFC3339 UTC>` computed at `up --demo` from a
default 1-day window (overridable via `demo.ttl`, a Go duration). It's absolute
rather than a duration so `status` can compute remaining time without separately
knowing creation time, and it's surfaced as a warn-only notice — no automatic
teardown.

## How it works

The demo subsystem composes the scenario lifecycle rather than reinventing it. A
thin `internal/demo/` package tree wraps the scenario machinery, keeping
"validate" (scenarios) and "present" (demo) separable. Each demo use-case is a
package under `internal/demo/<name>/` that implements the same `Scenario`
interface, self-registers in `init()`, and owns a distinct namespace and a
dedicated VIP (`const scnVIP = "10.0.10.<N>"`) so it doesn't collide with the
scenario-suite VIPs.

### Jumphost pre-staging (during `up --demo`)

When `DEMO_MODE`, a demo-staging phase runs after the jumphost phase (once the
`10.0.10.x` data-path ENI is attached and configured) and before the rest of the
graph. It pre-stages the client demo-ready, idempotently, driving the `jumphost`
leaf package over EICE — it mints and pushes the ephemeral EICE key internally, so
no interactive key dance is ever in the operator's face. The phase:

- installs `grpcurl` (v1.9.3) to `/usr/local/bin` (skip-if-present),
- verifies `curl` is present (it ships on the AL2023 client),
- copies the embedded Python Diameter client and responder to
  `/home/ec2-user/demo/`,
- verifies the EICE data path end-to-end by confirming the BNK_EXT ENI
  (`10.0.10.x`) carries the expected source address,
- records `DEMO_CLIENT_STAGED_AT` in `state.env`.

A normal (non-demo) `up` skips this phase entirely and is byte-for-byte
unchanged.

### `demo run` behavior

For each use-case, `demo run` reuses the scenario lifecycle and adds narration:

1. **Narrate the intent** (for example, "→ Steering HTTP/2 (h2c) through the BNK
   VIP — TMM will proxy HTTP/2 on both legs").
2. Render and apply the embedded manifests; wait for Programmed / Ready.
3. **Auto-resync** the relevant HTTPRoute(s) via `pkg/bnk.ResyncHTTPRoutes` —
   built in after a successful apply, so the demoer never runs `bnk resync` by
   hand.
4. Run the client from the pre-staged jumphost and capture output.
5. **Narrate the result** with an audience-legible proof line (for example,
   `HTTP/2 200 · backend saw HTTP/2.0`, `CER→CEA Result-Code=2001`) and an ASCII
   data-path diagram.
6. Leave workloads up for Q&A; `demo clean` (or `down`) removes them.

Narration is built to be legible projected on a screen: short lines, clear
✓ / ✗, and no raw verbose dumps unless `-v` is set.

### Launch renderer (rocket theme)

The launch renderer is an ASCII / TUI staging renderer in the CLI, gated by
`--demo`. It's a presentation layer over the per-phase progress the tool already
emits, mapping the provisioning phases onto four labelled stages:

```
   awsbnkctl ▸ <cluster-name> ▸ DEMO LAUNCH
   T-00:00  🚀 LIFTOFF — preflight green
   ██████████  STAGE 1  VPC · subnets · IGW · NAT          [Phase 00–07]  ✓ 38s
   ██████░░░░  STAGE 2  EKS control plane                  [Phase 08–08b]  ⏳
   ──────────  STAGE 3  Nodes · kubeconfig · ENIs · jumphost [Phase 10–18]
   ──────────  STAGE 4  BNK supply chain · activation      [Phase 11b–25]
   ──────────  ORBIT    CNEInstance ready · TMM ready · VIP live
```

The renderer degrades gracefully: on a non-TTY or piped output it falls back to
the plain phase log, it honors `--no-color`, and it never swallows a phase error
behind the animation — errors print clearly and abort. Normal (non-`--demo`)
`up` and CI output stay plain and parseable.

## Use-case catalogue

`demo list` shows the registered protocol demos plus the Green scenarios from the
[scenarios framework](09-SCENARIOS-FRAMEWORK.md), so they present alongside each
other. The catalogue is a CLI-presentation-only union; the demo and scenarios
registries stay disjoint at the data-model layer.

| Use-case | Kind | What it shows | Rating |
|---|---|---|---|
| `http2` | demo | HTTP/2 (h2c) proxied on both legs through the BNK VIP | Green |
| `diameter` | demo | Diameter CER → CEA (Result-Code 2001) through a BNK VIP | Green |
| Green scenarios | scenario | The Green scenarios, wrapped with demo narration | Green |

## Operational notes

- **Pool-member staleness → auto-resync.** Pool-member staleness is real and
  recurring, so `demo run` auto-resyncs after a successful apply rather than
  relying on the operator to run `bnk resync`. See the
  [upstream issue](../../upstream-issues/cne-controller-endpointslice-not-watched.md).

- **`down` always cleans demo workloads.** `down` enumerates the registered demo
  use-cases and calls each idempotent `Cleanup` before tearing down infra. It
  succeeds even if `demo run` was never invoked — no error on absent
  workloads / namespaces, no orphans left.

- **Embedded assets.** The demo manifests and clients (the Diameter client and
  responder) are embedded via `//go:embed` in `internal/demo/` and shipped in the
  binary, so `demo run` works regardless of any local-only artifacts.

- **`status` reads the targeted cluster.** The `⚠ DEMO` banner and expiry notice
  read `DEMO_MODE` / `DEMO_EXPIRY` from the targeted cluster's `state.env`
  (resolved via `-f/--config`), so they reflect the cluster you named, not the
  host default.

## Related

- [Scenarios framework](09-SCENARIOS-FRAMEWORK.md) — the validation machinery the
  demo experience composes.
