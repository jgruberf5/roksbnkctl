# Sharing a Transit Gateway across clusters

A Transit Gateway (TGW) routes between VPCs. By default `roksbnkctl cluster up`
creates a prefix-named gateway for the cluster it builds. But you often want the
opposite: **one gateway that several clusters share**, so they can reach each
other — the shape behind a [shared licensing cluster](./10c-flp-licensing.md#flow-c--a-shared-licensing-cluster),
a central services VPC, or cross-cluster testing.

roksbnkctl attaches a cluster's VPC to an **existing** Transit Gateway — by name
or by id, at create time or after — as its own small phase. Each workspace owns
its own connection, so N clusters can point at the one gateway.

## Attach when creating (the interview)

Decline to create a gateway, then name an existing one:

```console
$ roksbnkctl -w prod init
...
Create Transit Gateway? [Y/n] n
Existing Transit Gateway name or ID (blank = attach later with `tgw connect`): shared-tgw
```

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
  destroyed out from under its own TGW attachment — run `tgw disconnect` first (or
  `roksbnkctl down`, which sequences it).
