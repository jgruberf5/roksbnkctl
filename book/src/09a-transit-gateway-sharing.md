# Sharing a Transit Gateway across clusters

A Transit Gateway (TGW) routes between VPCs. By default `roksbnkctl cluster up`
creates a prefix-named gateway for the cluster it builds. But you often want the
opposite: **one gateway that several clusters share**, so they can reach each
other — the shape behind a [shared licensing cluster](./10c-flp-licensing.md#flow-c--a-shared-licensing-cluster),
a central services VPC, or cross-cluster testing.

roksbnkctl attaches a cluster's VPC to an **existing** Transit Gateway — by name
or by id, at create time or after — as its own small phase. Each workspace owns
its own connection, so N clusters can point at the one gateway.

## First: give each cluster VPC its own address block

Decide this **before** `cluster up`, because it cannot be changed afterwards.

A Transit Gateway routes on VPC **address prefixes**. Two attached VPCs claiming
the same block make routing ambiguous, and the gateway resolves that by silently
dropping traffic for one of them. Nothing logs an error. What you see instead is
*intermittent* image-pull timeouts against a mirror while every security group and
network ACL in the path plainly allows the traffic — which is why this is worth a
minute up front.

It is easy to hit by accident. IBM's default address prefix management is `auto`,
which assigns **every** VPC in a region the same three per-zone prefixes:

```
10.241.0.0/18   10.241.64.0/18   10.241.128.0/18
```

So a second roksbnkctl-created cluster joining a shared gateway collides by
construction, not by bad luck. Give each one a distinct block:

```yaml
cluster:
  create: true
  name: acme-eu-roks
  vpc_cidr: 10.242.0.0/16     # this cluster's own block
```

or `ROKSBNKCTL_CLUSTER_VPC_CIDR=10.242.0.0/16` for CI. The block is split into
three per-zone prefixes (a `/16` becomes three `/18`s), so **`/18` is the smallest
usable value**. Leaving it blank keeps IBM's auto assignment, and the default
`10.241.0.0/16` produces byte-identical prefixes to what `auto` gives today — so
setting it explicitly on a *first* cluster changes no addresses.

A worked allocation for a shared gateway:

| VPC | `vpc_cidr` | Per-zone prefixes |
|---|---|---|
| services (Harbor, FLP) | *existing, `10.243.0.0/24`* | — |
| first cluster | `10.241.0.0/16` | `10.241.0.0/18`, `.64.0/18`, `.128.0/18` |
| second cluster | `10.242.0.0/16` | `10.242.0.0/18`, `.64.0/18`, `.128.0/18` |

roksbnkctl checks this for you. `cluster up` refuses **before** terraform creates
anything when the VPC it is about to build would overlap a VPC already on the
gateway, and `tgw connect` refuses before attaching an existing VPC that would
overlap — each naming the other VPC and the colliding prefixes. If the check
cannot reach the API it warns and continues rather than blocking the build.

**Already built two overlapping clusters?** The prefixes of a live VPC can't be
edited — moving a subnet's CIDR replaces the subnet, which destroys the cluster on
it. Either keep only one of them attached at a time (`tgw disconnect` on the
other; the cluster itself survives), or rebuild one with `vpc_cidr` set.

## Attach when creating (the interview)

Decline to create a gateway, and `init` **discovers the transit gateways already
in your account** and lets you pick one by number — name, location, and status —
so you don't have to remember a name or id (since `v1.25.0`):

```console
$ roksbnkctl -w prod init
...
Create Transit Gateway? [Y/n] n
→ Discovering existing transit gateways...
  Existing transit gateways in this account:
     1) shared-tgw            (us-south, available)
     2) prod-tgw-eu           (eu-de, available)
Attach an existing Transit Gateway — pick a number (0 = none / attach later): 1
```

Picking `0` (or if the account has none) leaves the cluster unattached, to connect
later with `tgw connect`; if the discovery call fails it falls back to a free-text
name/ID prompt.

`cluster up` (or `cluster register`) then attaches the cluster's VPC to
`shared-tgw` automatically — no extra step. In config that is:

```yaml
resources:
  transit_gateway:
    create: false
    existing: shared-tgw      # a NAME or an ID — either is resolved
```

or, for CI, the environment variable `ROKSBNKCTL_TRANSIT_GATEWAY_NAME=shared-tgw`
(it too accepts a name or an id).

## Attach (or detach) after the fact

`tgw connect` works on a cluster that already exists — one roksbnkctl built, **or
one you registered** — because it reads the cluster's VPC from
`cluster-outputs.json`, not from any terraform state:

```bash
roksbnkctl -w prod tgw connect shared-tgw       # by name
roksbnkctl -w prod tgw connect 2f28a749-fcc0-…  # or by id
```

Run it in several workspaces with the same gateway and each cluster gets its own
connection to it:

```bash
roksbnkctl -w app-a tgw connect shared-tgw
roksbnkctl -w app-b tgw connect shared-tgw      # same gateway, second cluster
```

Detach removes **only this cluster's** connection — the gateway and every other
cluster's connection stay:

```bash
roksbnkctl -w prod tgw disconnect
```

## Status — the connected state, id, and name

`tgw status` shows the gateway identity and the **live** connection state, queried
from IBM (not just what was recorded at apply time):

```console
$ roksbnkctl -w prod tgw status
transit_gateway_id:    2f28a749-fcc0-43d7-97dd-403a6b00b56f
transit_gateway_name:  shared-tgw
connection_name:       prod
connection_id:         a1b2c3d4-…
cluster_vpc:           r010-072ad88a-…
connection_state:      attached
```

`connection_state` is the live attachment: `attached`, `pending`, or `detached`
(no connection for this VPC on the gateway). `roksbnkctl -w prod cluster config`
shows the same block alongside the rest of the cluster identity.

## How it works, and what to know

- **It's its own phase** (`state-tgw/`), separate from cluster / BNK / testing /
  gateway / FLP. So `tgw connect`/`disconnect` never touch anything else, and the
  connection survives a `bnk down` or a re-applied trial.
- **Name or id** is resolved against your account's gateway list, so an ambiguous
  name (two gateways sharing it) is an error, not an arbitrary pick — pass the id.
- **One VPC, one connection per gateway.** Clusters that share a *VPC* (rare) can't
  each hold a separate connection to the same gateway; clusters in *distinct* VPCs
  each get their own, which is the normal shared-gateway case.
- **A global gateway** connects VPCs in any region; a local one only its own
  region. `tgw connect` attaches to whatever gateway you name — make sure it can
  reach the region your cluster VPC is in.
- **`cluster down` refuses** while a connection exists, so the cluster isn't
  destroyed out from under its own TGW attachment — run `tgw disconnect` first.
  **`roksbnkctl down` (the composite) auto-detaches for you** (since `v1.26.0`):
  it removes this cluster's connection *after* the BNK/Testing teardown and
  *before* the cluster, because the connection pins the VPC's CRN and the VPC
  delete would otherwise fail (`VPC still has an attached transit gateway
  connection`). Only *this* cluster's connection is removed — the shared gateway
  and every other cluster's connection stay.
