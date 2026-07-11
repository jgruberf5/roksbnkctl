# Licensing BNK with the F5 License Proxy (FLP)

By default `roksbnkctl` licenses BIG-IP Next for Kubernetes (BNK) with a
**subscription JWT** — the `trial.jwt` (or your production token) staged in the
orchestration COS bucket. In that mode the cluster's controller reaches F5's
licensing service directly. Nothing in this chapter changes that default.

Some environments prefer (or require) an **F5 License Proxy (FLP)**: an in-cluster
service that brokers licensing on behalf of every BNK instance in the cluster,
so the workloads themselves never talk to F5 directly. `roksbnkctl` can deploy
the FLP and point BNK at it, as an **opt-in** capability — a new `flp` lifecycle
phase plus a one-line config switch. Both licensing modes are first-class; if you
don't opt in, the JWT path is byte-for-byte unchanged.

> **FLP is optional.** Everything here is gated behind `bnk.license_mode:
> f5licenseproxy`. Leave it unset (the default) and BNK licenses with the
> subscription JWT exactly as before — no `flp` phase, no extra resources.

## What the FLP is

The FLP is a Helm-installed workload (the `f5-license-proxy` chart: the proxy
itself plus a bundled Vault and PostgreSQL). It presents an in-cluster TLS
endpoint that the BNK **Cluster-Wide Controller (CWC)** calls for licensing; the
proxy forwards to F5's licensing backend. Because the CWC must trust the proxy's
TLS, the proxy's **root CA** is delivered to the CWC and the BNK **License**
custom resource is set to `operationMode: f5licenseproxy` with the proxy's
service URL.

`roksbnkctl` automates all of that: `roksbnkctl flp up` generates the proxy's CA
and mTLS certs, creates its secrets, installs the chart (pulling from your
registry mirror or FAR), and records the CA + endpoint. A later `bnk up` reads
that handoff and wires the License CR — you never copy a certificate by hand.

The FLP's charts and images are part of the standard BNK bill of materials, so
[`registry replicate`](./10a-air-gapped-install.md) already mirrors them into a
private registry — an air-gapped, FLP-licensed install pulls everything,
including the proxy, from a registry you control.

## The `flp` phase

Like [`testing`](./08a-three-phase-lifecycle.md) and `gateway`, the FLP is an
**independent, opt-in phase** with its own terraform state (`state-flp/`). The
composite `roksbnkctl up` never runs it; you run it explicitly, between the
cluster and BNK:

```bash
roksbnkctl flp up      # install the proxy into an existing cluster
roksbnkctl flp down    # remove it (leaves cluster / BNK / testing intact)
roksbnkctl flp output  # print its terraform outputs (endpoint, namespace)
```

`flp up` refuses if the workspace has no cluster yet, and records
`flp-outputs.json` (the proxy's root CA + service endpoint) on success. `flp
down` is a no-op success when there is no FLP state, so it is safe in a
reverse-order teardown of every phase.

The proxy installs into the `f5-license-proxy` namespace by default; override
with `bnk.flp.namespace` (or `ROKSBNKCTL_FLP_NAMESPACE`).

## Opting in

Set the license mode in the workspace `config.yaml`:

```yaml
bnk:
  manifest_version: 2.3.0-3.2598.3-0.0.170
  license_mode: f5licenseproxy   # connected (default) | disconnected | f5licenseproxy
  flp:
    namespace: f5-license-proxy  # optional; this is the default
```

Or answer **yes** to *License via an in-cluster F5 License Proxy (FLP)?* in the
`roksbnkctl init` interview, or set it non-interactively:

```bash
export ROKSBNKCTL_LICENSE_MODE=f5licenseproxy
export ROKSBNKCTL_FLP_NAMESPACE=f5-license-proxy   # optional
```

The subscription JWT is **still required** in FLP mode (the proxy presents it to
F5); it is resolved from COS exactly as in the JWT path — no change to
`bnk.subscription_jwt_file` or its credential.

## Flow A — a cluster you are creating

The full sequence when `roksbnkctl` provisions the cluster:

```bash
# 1. Seed the workspace with FLP mode (interview, config.yaml, or env).
roksbnkctl -w prod init          # answer "yes" to the FLP prompt

# 2. Create the cluster.
roksbnkctl -w prod cluster up

# 3. (Optional) mirror BNK — including the FLP — into your private registry.
#    Skip this to pull from repo.f5.com directly.
roksbnkctl -w prod registry replicate --target generic

# 4. Install the F5 License Proxy.
roksbnkctl -w prod flp up

# 5. Install BNK — it licenses via the proxy automatically.
roksbnkctl -w prod bnk up
```

Step 5 reads `flp-outputs.json` and sets the License CR to
`operationMode: f5licenseproxy`, its `teem*Url` at the proxy service, and
delivers the proxy root CA to the CWC. `bnk up` errors clearly if the `flp`
phase has not been run.

## Flow B — an existing cluster registered in the workspace

The same, but you [register](./09-registering-existing-cluster.md) a cluster you
did not provision instead of creating one. Registration writes the same
`cluster-outputs.json` the FLP and BNK phases consume, so from step 3 on the flow
is identical:

```bash
# 1. Seed the workspace with FLP mode.
roksbnkctl -w prod init          # answer "no" to "create a cluster?", "yes" to FLP

# 2. Register the existing cluster (or `cluster up` with cluster.create=false).
roksbnkctl -w prod cluster register my-existing-roks

# 3. (Optional) mirror BNK into your private registry.
roksbnkctl -w prod registry replicate --target generic

# 4. Install the F5 License Proxy into the registered cluster.
roksbnkctl -w prod flp up

# 5. Install BNK, licensed via the proxy.
roksbnkctl -w prod bnk up
```

`roksbnkctl` never owns the registered cluster's lifecycle — `flp up` adds only
the proxy workload, and `flp down` removes just that. The cluster stays under its
original owner.

## Tearing down

Remove the phases in reverse order; each leaves the others intact:

```bash
roksbnkctl -w prod bnk down     # remove BNK
roksbnkctl -w prod flp down     # remove the proxy
# then `cluster down` (created cluster) — registered clusters are left alone
```

`roksbnkctl down` (the composite) removes the phases it manages; run `flp down`
explicitly, as with the other opt-in phases. `cluster down` refuses while an FLP
phase still exists (it would orphan the proxy).

## Reference

| Config key | Meaning |
|---|---|
| `bnk.license_mode` | `connected` (default), `disconnected`, or `f5licenseproxy` |
| `bnk.flp.namespace` | namespace for the proxy (default `f5-license-proxy`) |
| `bnk.flp.chart_version` | pin the `f5-license-proxy` chart version (optional) |

| Environment variable | Overrides |
|---|---|
| `ROKSBNKCTL_LICENSE_MODE` | `bnk.license_mode` |
| `ROKSBNKCTL_FLP_NAMESPACE` | `bnk.flp.namespace` |

| File | Written by | Read by |
|---|---|---|
| `~/.roksbnkctl/<ws>/flp-outputs.json` | `flp up` | `bnk up` (FLP mode) |
