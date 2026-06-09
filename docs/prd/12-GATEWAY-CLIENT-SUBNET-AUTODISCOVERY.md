# PRD 12 — auto-derive the gateway client subnets from the Testing phase

> `roksbnkctl gateway up` is always run after the Testing phase is up. This PRD makes it read the deployed jumphosts' subnet CIDRs and auto-fill `gateway_client_subnet_local` / `gateway_client_subnet_remote` (now **lists**) when the operator hasn't set them — so the TMM static routes reach every real test client instead of the module's placeholder defaults. Sibling to [PRD 09](./09-AUTO-CLUSTER-JUMPHOSTS.md) (same best-effort, derive-from-testing-outputs posture). Estimated effort: small–medium (~150 LOC + tests + a module-variable type change + four terraform outputs).

## Why

The Gateway phase installs `F5SPKStaticRoute` CRs so TMM can route back to the client VSIs that drive traffic at the VIP. The route destination is the client subnet; the next-hop is `cidrhost(z.ext_vlan_cidr, gateway_static_route_gw_host)`, **per zone**.

Originally `gateway_client_subnet_local` / `_remote` were single-`string` `/32` placeholders (`10.244.64.12` / `10.245.64.5`) — wrong for every real workspace, since the jumphost subnets are auto-allocated by IBM at apply time. Worse, a *single* value can't serve the perf matrix's locality axis: the matrix drives traffic from a cluster jumphost in TMM's zone **and** one in a different zone — two different cluster-VPC subnets — and a single `/32` gives a return route to only one. The diff-zone cells would hang.

So both variables become **lists**, one `F5SPKStaticRoute` per (entry × zone), and `gateway up` derives them from the deployed Testing phase — which already holds the right values: the jumphosts' **subnet CIDRs**.

## Goal

When `roksbnkctl gateway up` runs and a client-subnet list is empty, fill it from the Testing phase's jumphost subnet CIDRs — `local` from **every** cluster-VPC jumphost subnet (so same-zone and different-zone clients each get a route), `remote` from the client-VPC jumphost subnet — logging what was chosen. Anything the operator set (config.yaml, a user tfvars file, or `--var-file`) wins; a missing/old Testing phase warns and falls back to the (now empty) module default. Never fails `gateway up`.

```
roksbnkctl testing up      # jumphosts up
roksbnkctl gateway up
# → ✓ gateway_client_subnet_remote auto-derived from the TGW jumphost subnet: 10.245.0.0/24
# → ✓ gateway_client_subnet_local auto-derived from 3 cluster jumphost subnet(s): 10.244.1.0/24, 10.244.2.0/24, 10.244.3.0/24
```

## Design

### The variable shape

`gateway_client_subnet_local` / `_remote` (gateway module + root) become `list(string)`, default `[]`. The `static_routes` local renders one route per (destination × zone):

```hcl
static_routes = merge(concat(
  [for i, z in var.cneinstance_network_zones : {
    for j, dest in var.gateway_client_subnet_local :
    "static-route-local-z${i + 1}-${j + 1}" => { destination = dest, gateway = cidrhost(z.ext_vlan_cidr, var.gateway_static_route_gw_host) }
  }],
  [for i, z in var.cneinstance_network_zones : {
    for j, dest in var.gateway_client_subnet_remote :
    "static-route-remote-z${i + 1}-${j + 1}" => { destination = dest, gateway = cidrhost(z.ext_vlan_cidr, var.gateway_static_route_gw_host) }
  }],
)...)
```

Empty lists → no client routes (the placeholder routes-to-nowhere are gone). `GatewayCfg.ClientSubnet{Local,Remote}` become `[]string` and `vars.go` renders them as HCL list literals when non-empty.

### Where the values come from

The Testing module emits the jumphost **subnet CIDRs** (new outputs `testing_cluster_jumphost_subnet_cidrs` = `{ zone => cidr }`, `testing_tgw_jumphost_subnet_cidr` = string with the `"TGW jumphost not created"` sentinel). This PRD forwards both to `terraform/outputs.tf`, `try()`-defaulted like the existing `testing_*` outputs.

> A subnet CIDR (not a `/32` host) is the right entry: it covers the jumphost *and* any other VSI you place in that subnet, and there's one per AZ — exactly the set the matrix's local clients live in.

### Reading them at `gateway up` time

`config.TestingJumphostSubnetCIDRs(workspace)` reads `state-testing/terraform.tfstate`'s `.outputs` directly (pure filesystem + JSON, matching the `DetectPresence` precedent), normalising the TGW sentinel to `""` and returning `(tgw, cluster, ok)`.

> **Existing-deploy caveat (accepted, documented).** The outputs only exist in `state-testing` once the Testing phase has applied *with this build*. A Testing phase applied earlier exposes neither until a no-op `roksbnkctl testing up` refresh; until then `ok=false` and `gateway up` warns + falls back. (Parity with PRD 09's "existing deploys need a re-`up`" note.)

### Filling the config — precedence

`vars.go` renders `gateway_client_subnet_*` into the base `terraform.tfvars` **only when the list is non-empty** — the **lowest** precedence layer. So `tryAutoGatewayClientSubnets` mutates the in-memory `ws.Gateway` lists **before** `WriteTFVars`, and only when they're empty:

- runs in `RunGatewayUp`, after `openGatewayTF`, before `writeAndInitGatewayPhase`. UP only.
- `remote` ← `[testing_tgw_jumphost_subnet_cidr]`.
- `local` ← the cluster jumphost subnet CIDRs, sorted by zone (deterministic), blanks dropped.
- Because the rendered value lands in the base layer, anything set in config.yaml / a user tfvars file / `--var-file` (all layered above) **wins automatically**. The injector never touches the forced gateway-phase override (highest layer).

### Posture

Mirrors `tryAutoJumphost`: best-effort, non-fatal, one log line per derived list, one `warning:` on fallback. `gateway up` succeeds or fails on terraform, never on this convenience.

## Scope

### In scope
- `gateway_client_subnet_local` / `_remote` → `list(string)` (gateway module + root), `[]` default; the per-(subnet × zone) `static_routes` rewrite.
- `GatewayCfg.ClientSubnet*` → `[]string`; HCL-list rendering in `vars.go`.
- Four new outputs (module + top-level) forwarding the jumphost subnet CIDRs.
- `config.TestingJumphostSubnetCIDRs`; `tryAutoGatewayClientSubnets` + `sortedClusterCIDRs`, wired into `RunGatewayUp`.
- `terraform.tfvars.example`: the gateway block with the list form + the auto-fill note + the manual `ibmcloud is subnet … ipv4_cidr_block` recipe.
- Unit tests: the reader (incl. the sentinel), the sorted derivation, list rendering, fill-when-empty, respect-when-set, warn-when-absent.

### Out of scope
- The deeper, root-unexposed gateway-module knobs (`gateway_class_name`, listener port, VIP host range, egress interface names, …) — a separate root `variables.tf` + `main.tf` change.
- Auto-deriving `cneinstance_network_zones` (the static-route **next-hop** still depends on `ext_vlan_cidr` being correct — this PRD fixes the destinations, not the next-hop).

## Acceptance

1. `gateway up` against a workspace with the Testing phase up, and no client subnets set, fills `remote` from the TGW jumphost subnet and `local` from **all** cluster jumphost subnets, logging each — so a `test matrix` run from a same-zone *and* a diff-zone jumphost both have TMM return routes.
2. A client subnet set in config.yaml / user tfvars / `--var-file` is never overwritten.
3. With the Testing phase absent (or its state predating this build), `gateway up` warns once and proceeds — exit unaffected.
4. The four subnet-CIDR outputs render empty (not error) on a deploy without jumphosts.
5. tfvars render emits `gateway_client_subnet_* = ["…", "…"]` (valid HCL list); empty lists render nothing.

## Cross-references
- [PRD 09 — per-AZ cluster-jumphost auto-registration](./09-AUTO-CLUSTER-JUMPHOSTS.md) — the derive-from-testing-outputs pattern this mirrors.
- [`internal/orchestration/gateway_autosubnet.go`](../../internal/orchestration/gateway_autosubnet.go) — the injector; wired from `gateway_phase.go` `RunGatewayUp`.
- [`internal/config/tfstate.go`](../../internal/config/tfstate.go) — `TestingJumphostSubnetCIDRs`.
- [`terraform/outputs.tf`](../../terraform/outputs.tf) — the forwarded subnet-CIDR outputs; [`terraform/modules/gateway/main.tf`](../../terraform/modules/gateway/main.tf) — the per-(subnet × zone) `static_routes`.
- [`internal/tf/vars.go`](../../internal/tf/vars.go) — renders `gateway_client_subnet_*` from `GatewayCfg` (the base layer the fill lands in).
