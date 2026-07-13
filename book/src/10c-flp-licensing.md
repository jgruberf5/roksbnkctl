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

You do not have to let `roksbnkctl` build the cluster. If you already have a ROKS
cluster, [register](./09-registering-existing-cluster.md) it into the workspace
and install the proxy onto it. Registration looks the cluster up in your IBM Cloud
account and writes the very same `cluster-outputs.json` that the `cluster up` path
produces — and that is the *only* thing the FLP and BNK phases consume. So from
step 3 on, the two flows are character-for-character identical:

```bash
# 1. Seed the workspace with FLP mode, and do NOT create a cluster.
roksbnkctl -w prod init          # answer "no" to "create a cluster?", "yes" to FLP
#    (equivalently, in config.yaml: cluster.create: false + bnk.license_mode: f5licenseproxy)

# 2. Register the cluster you already have.
roksbnkctl -w prod cluster register my-existing-roks

# 3. (Optional) mirror BNK — including the FLP — into your private registry.
roksbnkctl -w prod registry replicate --target generic

# 4. Install the F5 License Proxy into the registered cluster.
roksbnkctl -w prod flp up

# 5. Install BNK, licensed via the proxy.
roksbnkctl -w prod bnk up
```

**`roksbnkctl` never takes ownership of a registered cluster.** `flp up` adds only
the proxy workload (its namespace, secrets and Helm release); `flp down` removes
exactly that and nothing else. The cluster itself is never created, modified at the
infrastructure level, or destroyed — `cluster down` will not touch it.

## FLP behind a private registry

The FLP's chart and its four images are part of the standard BNK bill of
materials, so [`registry replicate`](./10a-air-gapped-install.md) mirrors them
along with everything else. Combining the two gives a genuinely disconnected,
FLP-licensed install: `flp up` and `bnk up` pull **every** chart and image from
your registry, and the CWC brokers licensing through the in-cluster proxy rather
than reaching F5 itself.

Nothing extra is required — point the workspace at the mirror and the FLP phase
follows it automatically:

```bash
roksbnkctl -w prod registry target generic         # your Harbor/Artifactory/ICR
roksbnkctl -w prod registry replicate              # mirrors BNK + the FLP
roksbnkctl -w prod flp up                          # proxy, pulled from the mirror
roksbnkctl -w prod bnk up                          # BNK, pulled from the mirror
```

## Tearing down

The FLP is an opt-in phase, so — exactly like `gateway` — the composite `down`
does **not** cover it. Tear it down **first**, while the cluster is still up:

```bash
roksbnkctl -w prod flp down     # remove the proxy
roksbnkctl -w prod down         # then BNK + the cluster (composite)
```

Both `roksbnkctl down` and `cluster down` **refuse** while FLP state exists, and
tell you to run `flp down` first. That guard is deliberate: the proxy's Helm
release and secrets live *in* the cluster, so destroying the cluster out from
under them would strand `state-flp/` pointing at resources that no longer exist,
and a later `flp down` could never reconcile.

`flp down` is a no-op success when there is no FLP state, so it is safe to put
unconditionally in a teardown script. Registered clusters are never destroyed —
`flp down` removes only the proxy workload.

## Reference

| Config key | Meaning |
|---|---|
| `bnk.license_mode` | `connected` (default), `disconnected`, or `f5licenseproxy` |
| `bnk.flp.namespace` | namespace for the proxy (default `f5-license-proxy`) |
| `bnk.flp.chart_version` | **optional** override. Left unset, the chart version is read from the BNK manifest (which lists `charts/f5-license-proxy` for the release), exactly like the FLO and CIS charts — you do not normally pin it. |

| Environment variable | Overrides |
|---|---|
| `ROKSBNKCTL_LICENSE_MODE` | `bnk.license_mode` |
| `ROKSBNKCTL_FLP_NAMESPACE` | `bnk.flp.namespace` |

| File | Written by | Read by |
|---|---|---|
| `~/.roksbnkctl/<ws>/flp-outputs.json` | `flp up` | `bnk up` (FLP mode) |
