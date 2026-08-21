# The three-phase lifecycle (Cluster / BNK / Testing)

> **Transcripts in this chapter are illustrative.** They show the *shape*
> of the output, not byte-exact captures. Resource counts and timings vary
> with upstream chart releases and IBM Cloud region behaviour.

As of this release, a `roksbnkctl` workspace is **three independent phases**
rather than two. The durable **Cluster** phase (the ROKS cluster + cluster
VPC + transit gateway + registry COS) sits underneath two independent
sibling phases that deploy on top of it: the **BNK** phase (cert-manager +
F5 Lifecycle Operator + CNE Instance + License — everything that talks to
the Kubernetes API) and the **Testing** phase (the jumphost rig — pure IBM
VPC, no Kubernetes).

```
           ┌──────────── 1. CLUSTER ────────────┐  (create OR reuse)
           │  ROKS cluster + cluster VPC + TGW   │  → cluster-outputs.json
           │  + registry COS                     │
           └──────────────────┬──────────────────┘
                     ┌─────────┴─────────┐
                     ▼                   ▼
           ┌──────────────────┐   ┌──────────────────┐
           │ 2. BNK  (k8s)    │   │ 3. TESTING (VPC) │
           │ cert-manager,    │   │ jumphosts (pure  │
           │ flo, cne, license│   │ IBM VPC, no k8s) │
           │ needs: cluster   │   │ needs: cluster   │
           │   (k8s API)      │   │   VPC + TGW only  │
           └──────────────────┘   └──────────────────┘
                └── parallel; independent up/down ──┘
```

**Why three?** BNK depends fully on the cluster (its resources live *in* the
cluster and need the Kubernetes API). Testing depends on the cluster only
for the **network** — the cluster VPC it attaches a jumphost to, and the
transit gateway it bridges across regions. The two workloads are otherwise
unrelated. Splitting them means:

- You can tear down the BNK trial and **keep the jumphosts** for the next
  iteration (`roksbnkctl bnk down` leaves Testing alone) — and the inverse
  (`roksbnkctl testing down` leaves BNK alone).
- The two unrelated workloads **deploy in parallel** after the cluster is
  ready, shaving wall-clock off a fresh `up`.

> **`testing` is not `test`.** This is the one naming collision worth
> internalising up front:
>
> - **`roksbnkctl testing up/down`** — a **provisioning phase**. It creates
>   and destroys the jumphost *infrastructure* (the TGW jumphost, the per-AZ
>   cluster jumphosts, the optional client VPC). Parallel to `cluster` and
>   `bnk`. Writes `state-testing/`.
> - **`roksbnkctl test`** — **runs validation probes** (connectivity, DNS,
>   throughput) against an already-deployed environment. Provisions nothing.
>   See [Chapter 20](./20-connectivity-testing.md).
> - **`roksbnkctl test hosts`** — manages the **list of hosts** the `test`
>   probes target. Still no provisioning.
>
> Mnemonic: *`testing` builds the rig; `test` runs the checks.*

## The three state directories

Each phase owns its own Terraform state directory so the phases never tangle:

```
~/.roksbnkctl/<workspace>/
  state-cluster/      # Cluster phase — `cluster up/down`
    terraform.tfstate
    cluster-phase-override.tfvars
  state/              # BNK phase — `bnk up/down` (and `up`/`down`/`plan`/`apply`)
    terraform.tfstate
    bnk-phase-override.tfvars
  state-testing/      # Testing phase — `testing up/down`
    terraform.tfstate
    testing-phase-override.tfvars
  cluster-outputs.json   # the cluster's identity — the handoff to BNK + Testing
```

The BNK phase keeps the original `state/` directory it has always used
(this is deliberate — see [§"Migration"](#migration-from-the-two-phase-layout)
below); the jumphosts now live in the new `state-testing/`. Naming is
slightly asymmetric (`state/` for BNK vs the explicit `state-cluster/` /
`state-testing/`) for exactly one reason: keeping `state/` as BNK means
existing workspaces don't have to migrate their BNK resources, only evict
the jumphosts.

## `cluster-outputs.json` — the handoff both siblings read

The Cluster phase writes `cluster-outputs.json` on success (or
`cluster register` writes it for a pre-existing cluster). Both BNK and
Testing read it to learn the cluster's identity without re-specifying it:

- **BNK** reads the cluster id/name (to attach to the existing cluster), the
  VPC id, and the cluster's **network mode** and **contract schema version** —
  which it checks against the BNK release it is about to install, before it
  plans anything (see [Chapter 28](./28-configuration-reference.md#bnk-release-and-network-mode)).
- **Testing** reads the cluster VPC id (the jumphost attaches to it), the
  cluster id/name, and the **transit gateway name** (the Testing module
  looks the gateway up by name to bridge the client VPC to the cluster).

The transit-gateway *name* is recorded alongside the id specifically so the
Testing phase can run standalone against a registered cluster.

### Which side wins

For most settings `config.yaml` is the input and the record is just an echo of
the result. For **create-time** settings the relationship inverts: the record
describes what the cluster *is*, and the config is a request that has to agree
with it. A cluster is never converted in place, so a contradiction is either a
mistake or a request for a different cluster.

That gives three cases, and the middle one is the one worth knowing:

| `cluster.network_mode` | vs the record | result |
|---|---|---|
| set, and matches | agrees | proceeds |
| **unset** | cannot disagree | **defers to the record** |
| set, and differs | contradicts | **refused**, before planning |

**Silence is not an assertion.** An unset `network_mode` means the config has no
opinion, not that it insists on `single-nic` — so it defers to whatever the
cluster actually is. This matters because a `config.yaml` is not always
hand-written and durable: the [BNK Forge modules](./24a-bnk-forge-registration.md)
regenerate one per step from a curated environment, so a mode set by the step
that *created* the cluster is absent by the time the step that *installs
BNK* runs. Reading that absence as a demand for `single-nic` would refuse a
correct deployment at its second step, over a contradiction nobody expressed.

The refusal in the third row is what stops terraform from planning a
**replacement** of a running cluster — which it will do without complaint, rendered
as output that reads like an update.

## Parallel `up`: Cluster first, then BNK || Testing

A fresh `roksbnkctl up` provisions the cluster **serially first** (both
siblings depend on it), then launches **BNK and Testing concurrently**:

```bash
roksbnkctl up --auto
```

```
→ Cluster phase (serial; both BNK and Testing depend on it)
  module.roks_cluster.ibm_container_vpc_cluster.cluster: Creating...
  ...
  Apply complete! Resources: 36 added.
✓ Wrote ~/.roksbnkctl/default/cluster-outputs.json

→ BNK || Testing (parallel)
  [bnk]     → terraform apply
  [testing] → terraform apply
  [testing] module.testing.ibm_is_instance.tgw_jumphost: Creating...
  [bnk]     module.cert_manager.helm_release.cert_manager: Creating...
  [testing] module.testing.tls_private_key.jumphost_shared_key: Creation complete
  [bnk]     module.flo.helm_release.flo: Creating...
  [testing] Apply complete! Resources: 9 added.
  [bnk]     Apply complete! Resources: 32 added.
✓ Auto-registered target jumphost (169.45.91.177)
```

The two parallel phases' output is **line-prefixed** (`[bnk]` / `[testing]`)
so you can follow each independently. The Cluster phase, being serial, prints
unprefixed.

Without `--auto`, `up` **plans both phases first** (sequentially, so each diff
is cleanly attributed), then asks a **separate confirmation for each** —
`Apply BNK plan? [y/N]` and `Apply Testing plan? [y/N]`. Two concurrent
applies can't each own an interactive prompt on one terminal, so the approval
is taken up front; the approved phases then apply **in parallel**. Approving
only one (e.g. `y` to BNK, `N` to Testing) brings up just that phase — handy
when you want to redeploy the BNK trial without touching the jumphosts. A
phase whose plan shows no changes is skipped without prompting.

To review a change out-of-band before applying — save the full plan to a file, get
sign-off, then apply **exactly** that plan — use the dissociated `roksbnkctl plan
--out <file>` / `roksbnkctl apply --plan <file>` flow (see [Chapter 10 — Deploying BNK
trials](./10-deploying-bnk-trials.md), §"Reviewing a plan before applying").

Why Cluster-completes-first rather than starting Testing the moment the
cluster VPC exists? The ROKS cluster create dominates wall-clock (~30-50 min);
Testing is a handful of VSIs (minutes). Starting Testing against a half-built
cluster invites data-source races for a few minutes' savings. Cluster-first
is simpler and correct.

## Per-phase `up` / `down`

Each phase has its own command pair, all sharing the same flag model
(`--auto`, `--var-file`, …):

| Phase | Up | Down | State |
|---|---|---|---|
| Cluster | `roksbnkctl cluster up` | `roksbnkctl cluster down` | `state-cluster/` |
| BNK | `roksbnkctl bnk up` | `roksbnkctl bnk down` | `state/` |
| Testing | `roksbnkctl testing up` | `roksbnkctl testing down` | `state-testing/` |

`bnk up` and `testing up` each bootstrap the Cluster phase first if the
workspace has no cluster yet (with a confirmation prompt — the cluster take
~30 min). Against an existing cluster they go straight to their own apply.

### `bnk down` leaves the jumphosts (and the inverse)

The headline capability of the split: the two siblings tear down
independently.

```bash
# Iterate on BNK while keeping the jumphost rig warm:
roksbnkctl bnk down      # destroys cert-manager/flo/cne/license; Testing untouched
roksbnkctl bnk up        # redeploy BNK against the same cluster + same jumphosts
```

```
✓ BNK phase destroyed. Cluster phase state-cluster/ and Testing phase
  state-testing/ are intact. Run `roksbnkctl bnk up` to redeploy.
```

And the inverse — destroy the jumphost rig but keep BNK running:

```bash
roksbnkctl testing down   # destroys the jumphosts; cluster + BNK untouched
```

Because each phase owns a separate state directory, `bnk down` physically
cannot touch `state-testing/`, and `testing down` cannot touch `state/`.

## Reuse an existing cluster

If you already have a ROKS cluster (yours or a teammate's), skip the Cluster
phase entirely — [register it](./09-registering-existing-cluster.md), then
deploy BNK and Testing on top:

```bash
roksbnkctl cluster register existing-bnk-cluster
roksbnkctl up --auto        # Cluster phase skipped; BNK || Testing deploy on the registered cluster
# or per-phase:
roksbnkctl bnk up --auto
roksbnkctl testing up --auto
```

`cluster down` is a no-op there — the cluster wasn't yours to destroy.

## Teardown ordering and the `cluster down` guard

Destroy in **reverse of create**: the siblings first (in parallel, they're
independent), then the cluster.

```bash
roksbnkctl down --auto
```

```
This will destroy the BNK, Testing, AND Cluster phases for workspace "default".
Continue? [y/N]: y

→ BNK || Testing (parallel destroy)
  [bnk]     Destroy complete! Resources: 32 destroyed.
  [testing] Destroy complete! Resources: 9 destroyed.
→ Cluster phase (destroy)
  Destroy complete! Resources: 36 destroyed.
```

The bare `down` takes **one** composite confirmation naming every present
phase, then runs the parallel sibling-destroy followed by the cluster
destroy — you can't accidentally confirm one phase and miss another.

`roksbnkctl cluster down` **refuses** while BNK or Testing state still has
resources — they reference the cluster VPC/TGW, so destroying the cluster
out from under them would orphan resources:

```
$ roksbnkctl cluster down
BNK and Testing state exist in this workspace; run `roksbnkctl bnk down` and
`roksbnkctl testing down` first (or `roksbnkctl down` to tear down all phases)
```

This is a correctness guard, not a prompt — `--auto` does not bypass it.
`cluster-outputs.json` is deleted only on `cluster down` (it's the cluster's
identity); `bnk down` and `testing down` leave it in place.

## Migration from the two-phase layout

Workspaces created before the three-phase split have their jumphosts living
in the **BNK state** (`state/`, alongside the BNK modules — that's how the
old two-phase "trial" worked). The three-phase split needs those jumphosts
in `state-testing/` instead. Three cases:

1. **Fresh / empty workspaces** — nothing to migrate. The first `up` lays
   out all three state directories cleanly.

2. **Two-phase split workspaces (the common case)** — the jumphosts need to
   move out of `state/` into `state-testing/` **without destroying the live
   jumphosts**. The recommended path preserves them:

   ```bash
   roksbnkctl testing migrate    # terraform state mv: jumphosts → state-testing/
   ```

   This `terraform state mv`s every `module.testing.*` resource (including
   the shared SSH key) from `state/` into a fresh `state-testing/`, with
   **no cloud churn** — the jumphosts keep their IPs and your `known_hosts`
   stays valid. A subsequent `bnk up` reconciles the now-jumphost-free BNK
   state (it plans no jumphost create), and `testing up` adopts the moved
   resources (in-place, not create).

   If a workspace is in an awkward partial-apply state, the bulletproof
   fallback is a full `roksbnkctl down` then `roksbnkctl up`, which
   re-creates everything in the three-state layout. This *does* churn the
   jumphosts (new IPs, `known_hosts` reset), so the `state mv` path is
   preferred whenever it's available.

`roksbnkctl` nudges you when it detects jumphosts still living in the BNK
state with an empty `state-testing/` — it won't silently rewrite your state.

## The optional Gateway phase (`gateway up` / `down`)

The BNK phase brings up a licensed, running control plane and now also creates
the `cloud-network-mapping` ConfigMap and the external/internal VLANs that TMM
needs to program its data plane. The **rest** of the install guide's
"Configuration" section — the Gateway API objects, egress SnatPool/Egress, the
per-zone static routes, and the cluster security-group VXLAN rule — is a
**separate, optional phase** with its own state (`state-gateway/`):

```bash
roksbnkctl up                  # Cluster → BNK ∥ Testing  (Gateway NOT included)
# … verify BNK is healthy: TMM pods Ready, CNEInstance Available …
roksbnkctl gateway up          # apply the data-plane config
roksbnkctl gateway down        # remove it, leaving cluster/BNK/testing intact
```

It is **never** run by the composite `up`/`down` — the Gateway config needs a
healthy BNK (its `F5SPK*` CRDs ship with the BNK manifest, and TMM must be up),
so it is strictly opt-in. It reuses the existing cluster via
`cluster-outputs.json` (like the Testing phase), forces every other phase's
creation off in its override, and manages only its own resources. Defaults all
come from the BNK 2.3 install guide; override the HTTPRoute backend, client
subnets, egress mode (`snatpool`/`automap`/`both`) and VXLAN port via the
`config.yaml` `gateway` block. `cluster down` refuses while the Gateway phase
has resources, so tear it down first (`gateway down`).

## Cross-references

- [Chapter 8 — The cluster phase](./08-cluster-phase.md) — the Cluster phase
  in depth.
- [Chapter 9 — Registering an existing cluster](./09-registering-existing-cluster.md)
  — the reuse-existing-cluster path.
- [Chapter 10 — Deploying BNK trials](./10-deploying-bnk-trials.md) — the BNK
  phase (`bnk up/down`) in depth.
- [Chapter 11 — Tearing down](./11-tearing-down.md) — the full destroy decision
  tree and refusal catalogue.
- [Chapter 20 — Connectivity testing](./20-connectivity-testing.md) — what
  `roksbnkctl test` (not `testing`!) actually runs.
