# PRD 12 — auto-derive the gateway client subnets from the Testing phase

> `roksbnkctl gateway up` is always run after the Testing phase is up. This PRD makes it read the deployed jumphosts' private IPs and auto-fill `gateway_client_subnet_local` / `gateway_client_subnet_remote` when the operator hasn't set them — so the TMM static routes reach the real test clients instead of the module's placeholder defaults. Sibling to [PRD 09](./09-AUTO-CLUSTER-JUMPHOSTS.md) (same best-effort, derive-from-testing-outputs posture). Estimated effort: small (~120 LOC + tests + two terraform outputs).

## Why

The Gateway phase installs per-zone `F5SPKStaticRoute` CRs so TMM can route back to the client VSIs that drive traffic at the VIP. The route destination is `gateway_client_subnet_local` (a client in the cluster VPC) and `gateway_client_subnet_remote` (a client in the client VPC, reached over the Transit Gateway); the next-hop is `cidrhost(z.ext_vlan_cidr, gateway_static_route_gw_host)`.

Today those two variables default to **placeholder /32 hosts** (`10.244.64.12` / `10.245.64.5`) baked into the gateway module from the BNK install guide. They are wrong for every real workspace — the jumphost subnets are auto-allocated by IBM at apply time, so the actual client IPs are not knowable ahead of time and differ per deploy. A hand-run `gateway up` that doesn't override them silently installs static routes to nowhere, and there is no signal that anything is off.

But the right values are already sitting in the deployed Testing phase: the jumphosts' **private IPs**. `gateway up` already runs against a workspace where the Testing phase has applied. So instead of asking the operator to discover and paste two IPs, derive them.

## Goal

When `roksbnkctl gateway up` runs and a client-subnet field is unset, read the Testing phase's jumphost private IPs and fill it in — `remote` from the TGW jumphost, `local` from a cluster jumphost — logging exactly what was chosen. Anything the operator set (config.yaml, a user tfvars file, or `--var-file`) wins; a missing/old Testing phase warns and falls back to the module default. Never fails `gateway up`.

After this lands, the common path is zero-config:

```
roksbnkctl testing up      # jumphosts up
roksbnkctl gateway up
# → ✓ gateway_client_subnet_remote auto-derived from the TGW jumphost: 10.245.0.7
# → ✓ gateway_client_subnet_local auto-derived from cluster jumphost jp-osa-1: 10.244.1.9 (single client — …)
```

## Design

### Where the values come from

The Testing module already emits the private IPs (`terraform/modules/testing/outputs.tf`):

- `testing_tgw_jumphost_private_ip` — the client-VPC jumphost (string; the `"TGW jumphost not created"` sentinel when `testing_create_tgw_jumphost = false`).
- `testing_cluster_jumphost_private_ips` — `{ zone => private-IP }` for the per-AZ cluster jumphosts.

They are **not** forwarded as top-level outputs (only the public/floating IPs are). This PRD forwards both to `terraform/outputs.tf`, `try()`-defaulted exactly like the existing `testing_*` outputs, so a deploy without the jumphosts renders empty.

### Reading them at `gateway up` time

The Testing phase applied earlier; its outputs live in `state-testing/terraform.tfstate`. `config.TestingJumphostPrivateIPs(workspace)` reads that file's `.outputs` directly (pure filesystem + JSON, no terraform invocation — matching the `DetectPresence` precedent in `internal/config/tfstate.go`) and returns `(tgw, cluster, ok)`. The TGW sentinel is normalised to `""`; `ok` is false when the file, the outputs, or every IP is absent.

> **Existing-deploy caveat (accepted, documented).** The two outputs only exist in `state-testing` once the Testing phase has applied *with this build*. A Testing phase applied before this change exposes neither until a no-op `roksbnkctl testing up` refresh. Until then, `TestingJumphostPrivateIPs` returns `ok=false` and `gateway up` warns + falls back — behavior identical to pre-PRD-12. (Parity with PRD 09's "existing deploys need a re-`up`" note.)

### Filling the config — precedence

`GatewayCfg` already has `ClientSubnetLocal` / `ClientSubnetRemote`, and `internal/tf/vars.go` renders `gateway_client_subnet_*` **only when the field is non-empty**, into the base `terraform.tfvars` — the **lowest** precedence layer. So the injector simply mutates the in-memory `ws.Gateway` fields **before** `WriteTFVars`, and only when they're empty:

- `tryAutoGatewayClientSubnets(ws, workspace, w)` runs in `RunGatewayUp`, right after `openGatewayTF`, before `writeAndInitGatewayPhase` (which renders tfvars). UP only — `down` replays the applied snapshot and doesn't derive.
- Because the rendered value lands in the base layer, anything the operator set in config.yaml, a user tfvars file, or `--var-file` (all layered above the base) **wins automatically**. The injector never touches the forced gateway-phase override (which is the *highest* layer and must not carry a user-overridable value).

### Endpoint mapping

- **`remote`** ← `testing_tgw_jumphost_private_ip`. A clean 1:1 mapping (there is exactly one TGW jumphost).
- **`local`** ← one entry of `testing_cluster_jumphost_private_ips`, chosen deterministically (lowest zone name with a non-empty IP). The value is a `/32` host.

The derived value is the jumphost's **private host IP** (same format as the placeholder defaults — a bare address), so it slots into the existing `F5SPKStaticRoute` destination unchanged.

### The `local`-is-scalar wrinkle (called out, not solved)

`gateway_client_subnet_local` is a single scalar, but a 3-AZ rig has three cluster jumphosts in three subnets, and the static route uses one destination across all zones. Auto-deriving picks **one** cluster jumphost and **logs the single-client caveat** ("set gateway.client_subnet_local explicitly to cover a wider subnet"). Covering several client subnets at once would need the gateway module to accept a *list* of local destinations — a larger module change, deliberately out of scope here. `remote` has no such ambiguity.

### Posture

Mirrors `tryAutoJumphost` exactly: best-effort, non-fatal, one log line per derived value, one `warning:` line on fallback. `gateway up` succeeds or fails on terraform, never on this convenience.

## Scope

### In scope
- Two new top-level outputs forwarding the jumphost private IPs.
- `config.TestingJumphostPrivateIPs` (tfstate-outputs reader).
- `tryAutoGatewayClientSubnets` + `pickClusterJumphost`, wired into `RunGatewayUp`.
- `terraform.tfvars.example`: the gateway block (+ the discover-it-yourself recipe and the "or let roksbnkctl fill it" note).
- Unit tests: the reader (incl. the TGW sentinel), the deterministic pick, fill-when-empty, respect-when-set, and warn-when-absent.

### Out of scope
- Making `gateway_client_subnet_local` a list / multi-destination static routes (the scalar wrinkle above).
- Surfacing the deeper, root-unexposed gateway-module knobs (`gateway_class_name`, listener port, VIP host range, egress interface names, …) — a separate root `variables.tf` + `main.tf` change.
- Auto-deriving `cneinstance_network_zones` (the static-route **next-hop** still depends on `ext_vlan_cidr` being correct — this PRD fixes the destination, not the next-hop).

## Acceptance

1. `roksbnkctl gateway up` against a workspace with the Testing phase up, and no client subnets set, fills `remote` from the TGW jumphost and `local` from a cluster jumphost, logging each.
2. A client subnet set in config.yaml / user tfvars / `--var-file` is never overwritten.
3. With the Testing phase absent (or its state predating this build), `gateway up` warns once and proceeds on the module defaults — exit unaffected.
4. The two private-IP outputs render empty (not error) on a deploy without jumphosts.
5. `-o`/CI behavior of `gateway up` is otherwise unchanged.

## Cross-references
- [PRD 09 — per-AZ cluster-jumphost auto-registration](./09-AUTO-CLUSTER-JUMPHOSTS.md) — the derive-from-testing-outputs pattern this mirrors.
- [`internal/orchestration/gateway_autosubnet.go`](../../internal/orchestration/gateway_autosubnet.go) — the injector; wired from `gateway_phase.go` `RunGatewayUp`.
- [`internal/config/tfstate.go`](../../internal/config/tfstate.go) — `TestingJumphostPrivateIPs`.
- [`terraform/outputs.tf`](../../terraform/outputs.tf) — the forwarded private-IP outputs.
- [`internal/tf/vars.go`](../../internal/tf/vars.go) — renders `gateway_client_subnet_*` from `GatewayCfg` (the base layer the fill lands in).
