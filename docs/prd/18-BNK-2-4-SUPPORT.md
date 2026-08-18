# PRD 18 — BNK 2.4 support

**Status:** evaluation + plan. Nothing here is implemented.
**Source:** `BNK Installation on IBM ROK Cluster BNK 2-4 EA.pdf` (42 pages, EA
draft), read in full. Compared against the shipped 2.3 implementation in
`terraform/` and `internal/`.

**Trigger contract:** setting `bnk.manifest_version` to a `2.4.*` value — and
nothing else — must select every 2.4 behaviour described below. No new required
config key, no new CLI flag, no changed default for a `2.3.*` workspace.

---

## 1. Headline: 2.4 replaces the data-plane configuration model

The 2.3 → 2.4 delta is not a version bump with a few new fields. **The entire
"Configuration" CR set is replaced by a different API group with different
semantics**, and the replacement absorbs objects that 2.3 spreads across two
phases.

| Concern | 2.3 (what we ship) | 2.4 (EA guide) |
|---|---|---|
| Zone → subnet map | ConfigMap `cloud-network-mapping`, referenced by `CLOUD_NETWORK_CONFIGMAP` | **gone** — folded into `Infra` |
| TMM self-IPs / VLANs | `F5SPKVlan` `external-vlan` + `internal-vlan` (`k8s.f5net.com/v1`), **static** `selfip_v4s` + `prefixlen_v4` | `Infra.spec.networks[]` + `Infra.spec.ipams[]`; self-IPs are **allocated by IPAM**, not configured. `internal-vlan` is created by the controller |
| Static routes | N × `F5SPKStaticRoute` (`k8s.f5net.com/v1`), one per (subnet × zone) | `Infra.spec.staticRoutes[]` — one entry per zone, `destinations[]` is a list |
| Listener/VIP networks | `F5BnkGateway.spec.ingressConfig.defaultListenerNetworks[]` with explicit `startAddress`/`endAddress` | `GatewaySettings.spec.ingressConfig.defaultListenerNetwork` → `ipamRefs` + `networkRefs` |
| SNAT pool | `F5SPKSnatpool` with literal `addressList` | `GatewaySettings.spec.sourceNATPools[]` → `ipamRefs` |
| Egress | `F5SPKEgress` `k8s.f5net.com/v3`, `snatType: SRC_TRANS_AUTOMAP\|SRC_TRANS_SNATPOOL`, inline `pseudoCNIConfig.vxlan` | `EgressGateway` `gateway.k8s.f5.com/v1alpha1` → `parametersRef.sectionName` into `GatewaySettings.egressConfigs[]`; VXLAN defaults live in `Infra.spec.egressDefaults` |
| Gateway `parametersRef` | `group: k8s.f5net.com`, `kind: F5BnkGateway` | `group: gateway.k8s.f5.com`, `kind: GatewaySettings` |
| Gateway namespace | app namespace (`f5-app`) | **`f5-bnk`** — "GatewaySettings and Gateways to be applied in Same Namespace". Cross-namespace routes via `allowedRoutes.namespaces.from: Selector` |
| New CRD groups | — | `gateway.k8s.f5.com`, `fic.f5.com` (IPAM/IPAMRange status objects) |

The consequence for us: **the `gateway` phase is a rewrite for 2.4, and the
network half of the `bnk` phase moves into it.** `cloud-network-mapping` and the
two `F5SPKVlan` CRs — today created by `modules/cne_instance` — have no 2.4
equivalent inside the BNK phase; their content becomes `Infra`, which belongs
with the rest of the Configuration CRs.

### The IPAM inversion is the substantive design change

In 2.3 the operator states addresses: self-IP `10.155.15.101`, VIP range
`.10`–`.50`, SNAT address `10.10.11.9`. In 2.4 the operator states *pools*
(`ipams[].ipPools[] {cidr, availabilityZone}`) and the controller allocates. The
guide's own IPAM status shows the controller reserving `10.112.9.1` and handing
TMM `10.112.9.2` on a `/26` carved out of the declared `/24`.

This means three existing config fields become **inert** under 2.4:
`bnk.network.zones[].external_selfip`, `.internal_selfip`, and all three
`vlan_prefixlen*` values. They must not silently do nothing — see §5.

It also means the internal VLAN is no longer ours to configure. Limitation 3 in
the guide fixes it at `169.254.99.100` (TMM) / `169.254.99.101` (node), created
on first `EgressGateway` and deleted with the last, over a controller-owned NAD
named `macvlan-internal`. `bnk.network.zones[].int_vlan_cidr` therefore has no
2.4 consumer either.

---

## 2. Phase-by-phase evaluation

### `cluster` — no functional change

Guide asks for OpenShift 4.18 (16 vCPU / 32 GB workers). We already default
above that. One new conditional step:

- **OCP ≥ 4.19** requires deleting
  `validatingwebhookconfiguration/openshift-ingress-operator-gatewayapi-crd-admission`,
  `oc rollout restart deployment flo-f5-lifecycle-operator`, then re-applying the
  CNEInstance.

  We already sweep this name, but only as `validatingadmissionpolicybindings` +
  `validatingadmissionpolicies`
  (`internal/orchestration/admission_sweep.go:34-39`). The 2.4 guide names a
  **`validatingwebhookconfigurations`** object — a third resource type we do not
  touch. We also never restart FLO. See §5.4.

### `flp` — no change

FLP is licensing-plane, untouched by the 2.4 data-plane rework. The manifest
subchart lookup (`charts/f5-license-proxy`) is version-parameterised already.

### `bnk` — CNEInstance spec changes; network CRs move out

Confirmed differences in the CNEInstance body (guide pp. 9–13 vs
`terraform/modules/cne_instance/modules/cneinstance/main.tf`):

| Field | Ours (2.3) | 2.4 guide |
|---|---|---|
| `metadata.annotations` | — | `cneinstance.k8s.f5.com/disable-session-state: "true"` |
| `wholeCluster` | hard-coded `true` (`modules/cne_instance/main.tf:65`) | `false` |
| `watchNamespaces` | absent | `["All"]` |
| `tmmReplicas` | absent | `3` |
| `placement.dataPlane` | absent | `topologySpreadConstraints` (zone, maxSkew 1, DoNotSchedule) + `podAntiAffinity` (hostname) on `app: f5-tmm` |
| `deploymentSize` | `"Small"` | `"Tiny"` |
| `externalBigip.enabled` | absent (CIS driven from FLO helm values) | `true` |
| `advanced.externalBigip.env` | absent | `ENABLE_EXT_BIGIP_DATASERVER_MONITOR`, `ENABLE_EXT_BIGIP_POOL_MONITOR`, `EXTERNAL_BIGIP_LOGIN_SECRET: f5-bigip-ctlr-login`, `CLUSTER_IDENTIFIER` |
| `advanced.demoMode.enabled` | `true` | `false` |
| `advanced.tmm.rollingUpdate` | absent | `maxUnavailable: 1, maxSurge: 0` |
| `advanced.tmm.env` | `TMM_CALICO_ROUTER`, `TMM_DEFAULT_MTU`, `PAL_CPU_SET`, `TMM_MAPRES_ADDL_VETHS_ON_DP`, `TMM_K8S_ROUTES` | same minus `PAL_CPU_SET` and `TMM_K8S_ROUTES`, plus `TMM_IGNORE_GATEWAYS: "true"`, `DISABLE_HT: "true"`, **`ENABLE_K8S_ROUTES: "true"`** |
| `advanced.cneController.env` | `TMM_DEFAULT_MTU`, `CLOUD_ENV`, `CLOUD_PROVIDER`, `CLOUD_NETWORK_CONFIGMAP`, `VPC_NAME`, `CLOUD_REGION`, `IBM_TRUSTED_PROFILE_ID`, `GSLB_DATACENTER_NAME`, `CLOUD_VPC`, `CLOUD_TRUSTED_PROFILE` | `TMM_DEFAULT_MTU`, `CLOUD_ENV`, `CLOUD_PROVIDER`, `CLOUD_TRUSTED_PROFILE`, `GSLB_DATACENTER_NAME`, **`USE_GATEWAY_SETTINGS: "true"`** |
| `advanced.ipamController.env` | absent | optional Infoblox block (`PROVIDER`, `INFOBLOX_*`, `CREDENTIAL_SECRET`, `CERTIFICATE_SECRET`, `INSECURE`) |
| `networkAttachments` | `["ens3-ipvlan-l2", "macvlan-conf"]` | `["ens3-ipvlan-l2"]` only |

Two notes on the env list:

- `TMM_K8S_ROUTES` (a CIDR) appears to become `ENABLE_K8S_ROUTES` (a boolean).
  If so, `bnk.network.tmm_k8s_routes` is inert under 2.4 — same class of problem
  as the self-IPs. **Must verify**, because the alternative reading is that
  `ENABLE_K8S_ROUTES` merely gates a route set the controller now derives itself.
- The guide's *prose* ("Change the variables in cneinstance-cr.yaml") lists
  `CLOUD_VPC` and `CLOUD_REGION` as things to set, but the sample YAML on the
  same page omits both. That is an internal inconsistency in the EA document.
  Keeping them emitted is the safe reading (extra env is inert if unread), and
  that is what §4 proposes.

What already matches 2.4 and needs no work:

- cert-manager installed with `featureGates=ServerSideApply=true`
  (`modules/cert_manager/modules/cert-manager/main.tf:120`). Guide pins v1.16.1;
  we default v1.17.3, which is newer, not wrong.
- The issuer chain `selfsigned-cluster-issuer` → `ext-ca` Certificate →
  `ext-ca-cluster-issuer` is byte-for-byte the guide's
  (`modules/flo/modules/flo/main.tf:406-436`).
- NAD `ens3-ipvlan-l2` — ipvlan / l2 / master `ens3` / static IPAM — already
  built, address already a variable.
- FLO release name `flo`, chart `f5-lifecycle-operator`, `crds.keep: false`,
  `namespace` / `sharedComponentNamespace` wiring.
- SCC `privileged` for `flo-f5-lifecycle-operator`
  (`modules/cne_instance/modules/cneinstance/main.tf:73`).
- CIS secret `f5-bigip-ctlr-login` with `username`/`password`/`url`
  (`modules/flo/modules/flo/main.tf:655`).
- License CR (`k8s.f5net.com/v1`, utils namespace, `jwt` + `operationMode`).
- Egress VXLAN port default `6789` (`modules/gateway/variables.tf:217`).

One pre-existing mismatch worth resolving while we are here: our FLO values set
`containerPlatform = "Generic"` (`modules/flo/modules/flo/main.tf:142`); both the
2.3 and 2.4 guides say `IBM`. This is not a 2.4 change and is out of scope for
this PRD, but it should be raised as its own issue.

### `gateway` — full rewrite for 2.4

Mapping from the current module to the 2.4 objects:

| `modules/gateway/main.tf` | 2.4 replacement |
|---|---|
| `kubectl_manifest.gateway_class` | unchanged (`gateway.networking.k8s.io/v1`) |
| `kubectl_manifest.bnk_gateway` (`F5BnkGateway`) | `GatewaySettings` `gw-settings-common` |
| `kubectl_manifest.snatpool` (`F5SPKSnatpool`) | `GatewaySettings.spec.sourceNATPools[]` |
| `kubectl_manifest.gateway` (in `f5-app`) | `Gateway` in **`f5-bnk`**, `parametersRef` → `GatewaySettings`, listener gains `hostname` + `allowedRoutes.namespaces.from: Selector` |
| `kubectl_manifest.http_route` | unchanged shape; `parentRefs[].namespace` now points at `f5-bnk` |
| `kubectl_manifest.egress_automap` / `egress_snatpool` | two/three `EgressGateway` CRs, differentiated by `parametersRef.sectionName` and `sourceSelector.selectionMode` |
| `kubectl_manifest.static_route` (N per zone) | `Infra.spec.staticRoutes[]` (one per zone, `destinations` is a list) |
| `ibm_is_security_group_rule.vxlan_ingress` | unchanged, still needed |
| — | **new:** `Infra` CR (`cloud-infra`, `f5-bnk`) |
| — | **new:** SG rule for the ingress listener port (guide adds TCP 80 inbound) |
| — | **new:** app-namespace SCC `oc adm policy add-scc-to-user privileged -n f5-app -z default` |

`EgressGateway` gains a selection model 2.3 has no equivalent for —
`NamespaceSelector`, `PodSelector`, and `Combined`. The current
`gateway.egress_mode` (`snatpool | automap | both`) is a strictly weaker knob.

### `testing` — no change beyond addressing

Ingress verification is still curl-to-VIP; egress verification is still
`nc` to a local and a remote VSI. The VIPs are IPAM-allocated rather than
computed, so the test phase must **read** the allocated address (from the
`Gateway` status `ADDRESS` field) instead of deriving it. Today
`gateway_vip_start_host` lets us predict it; under 2.4 we cannot.

### Uninstall — order changes

2.4 order: `EgressGateway` → `HTTPRoute` → `Gateway` → `GatewaySettings` →
`Infra` → `License` → `CNEInstance` → `helm uninstall flo` → delete CRDs for
`k8s.f5.com`, `k8s.f5net.com`, `fic.f5.com`, `metrics.f5.net`. The `fic.f5.com`
group is new and is not in any teardown list we have.

---

## 3. Corrections to the existing 2.4 groundwork — **DONE (Step 0)**

Two things already in the repo were wrong against this guide, and everything else
builds on them. Both are fixed; recorded here because the reasoning is what makes
the fixes reviewable.

**3.1 The support matrix claimed 2.4 does multi-NIC. This guide does not show
that.** `internal/config/support_matrix.yaml` carried
`network_modes: [single-nic, multi-nic]` for the 2.4 row, flagged PROVISIONAL.
The EA guide is single-NIC throughout: one NAD, `master: ens3`, one
`external-vlan` network. Nothing evidences multi-NIC, so the row now reads
`[single-nic]` — note kept. Leaving the claim in place meant
`guardSupportedCombination` would *pass* a multi-NIC 2.4 plan we have no basis
for. The tests that asserted the pairing was supported now assert it is refused,
so re-adding multi-NIC is a deliberate act with a failing test attached rather
than a quiet edit to a data file.

**3.2 `gateway_controller_name` was hard-coded to the default namespace.** Its
default was the literal `f5.com/f5-bnk-f5-cne-controller` while the CNEInstance
name it must match is `"${var.flo_namespace}-f5-cne-controller"`
(`modules/cne_instance/modules/cneinstance/main.tf:4`). Any workspace with a
non-default `bnk.flo_namespace` got a GatewayClass no controller accepts. A
**2.3 bug**, surfaced by the 2.4 guide's note ("Make sure controller
name(f5-bnk-f5-cne-controller) is same as in CNEInstance CR").

Now derived from `flo_namespace` when unset — byte-identical to the old literal
at the default namespace, so no existing deployment moves. Because the failure is
*silent* (GatewayClass never `Accepted`, Gateway never programmed, `terraform
apply` succeeds), the fix ships with two ways to see it: the resolved value is a
terraform output, and `gateway status` now probes the GatewayClass's `Accepted`
condition alongside the `controllerName` that produced it.

---

## 4. How `2.4.*` selects the behaviour

`Workspace.BNKLine()` (`internal/config/bnkversion.go:38`) already derives
`"2.4"` from both spellings the guide uses — `2.4.0-EA-1` (chart) and
`2.4.0-3.3175.0-0.0.119` (`manifestVersion`) — via `^(\d+)\.(\d+)(?:[.\-]|$)`.
Verified: both yield `"2.4"`, `2.3.0-3.2598.3-0.0.170` yields `"2.3"`, and an
empty manifest still falls back to `DefaultManifestVersion` → `"2.3"`. **No
parser change is needed.** That derived line is the only selector.

### Recommendation: gate in the base tree on a `bnk_line` tfvar; keep `lines/` empty

`terraform/lines/README.md` sets the rule: *"A difference that a variable can
express belongs in the base as a variable, not here: an overlay is a fork of a
file, and two copies of a file drift."* That rule points away from overlays here.

An overlay can only add or replace whole files. To stop creating the `F5SPKVlan`
CRs under 2.4, an overlay would have to replace all ~570 lines of
`modules/cne_instance/modules/cneinstance/main.tf` and all ~280 of
`modules/gateway/main.tf` — forking the CNEInstance body, the SCC list, the
readiness gates and the webhook probe, none of which differ by line. That is the
drift the README warns about, in the two files we change most often.

So:

1. `internal/tf/vars.go` renders a new tfvar `bnk_line`, derived from
   `ws.BNKLine()`. Not a config key — a derived value, like the existing
   contract fields.
2. Split the line-varying resources into their own files in the **base** tree,
   each gated on `var.bnk_line`:
   - `modules/cne_instance/modules/cneinstance/netcfg_23.tf` — the
     `cloud-network-mapping` ConfigMap and the two `F5SPKVlan` CRs, `count`
     gated on `var.bnk_line != "2.4"` (so an unknown/empty line keeps 2.3
     behaviour, matching how `ErrUnknownLine` warns rather than refuses).
   - `modules/gateway/config_23.tf` / `config_24.tf` — the two Configuration CR
     sets, mutually exclusive on `var.bnk_line`.
   - `modules/gateway/infra_24.tf` — the `Infra` CR.
   - The shared CNEInstance spec stays in one `main.tf`, with the
     line-conditional fields expressed as locals (`wholeCluster`,
     `networkAttachments`, the two env lists, `placement`), exactly the way
     `cnecontroller_gtm_env` is already built as a conditional list so an
     unset feature produces no diff.
3. `terraform/lines/` **stays empty**. Reserve it for what a variable genuinely
   cannot express — a different provider version constraint, or a changed module
   input signature.

A `count = 0` `kubectl_manifest` performs no API discovery, so shipping the 2.4
resources in the base costs a 2.3 cluster nothing: they do not appear in the
plan, and the CRDs they reference are never looked up.

### Line changes on an existing deployment

Editing `bnk.manifest_version` from `2.3.*` to `2.4.*` on a live workspace would
have terraform destroy the F5SPK* objects and create the `gateway.k8s.f5.com`
ones in one apply — an ordering the guide's uninstall sequence says must be
staged, and across a CNEInstance whose controller is simultaneously being
replaced.

**Do not support in-place line migration in the first cut.** Add a line-change
guard alongside `guardCreateTimeSettings` in
`internal/orchestration/create_time_guard.go`: if the recorded line in the
deployed tfvars differs from the derived line, refuse with "tear down and
redeploy". Same shape as the `network_mode` guard, same reasoning — silence is
not an assertion, but an explicit contradiction is refused.

---

## 5. Config surface — additive only

Everything below is optional and absent-means-2.3-behaviour, so the
config.yaml/env/CLI contract holds.

### 5.0 The rule every step follows

**A setting is not surfaced until it exists in all five places.** A tfvar alone
is unreachable from the CLI; a config key alone is unreachable from CI, which has
no `config.yaml` at all.

1. the terraform variable (root **and** module — a module default the root never
   passes cannot be set),
2. the `config.yaml` field, `omitempty`, absent → the terraform default,
3. the `ROKSBNKCTL_*` env override, plus its row in the `OverrideFromEnv` doc
   table,
4. the `.env.example` allowlist entry — the demo builds `bnk-env` from the keys
   declared there, so a missing one is silently unreachable from every Argo
   workflow,
5. the book: the `gateway:`/`bnk:` block table in
   `28-configuration-reference.md`, the env table in `07a-unattended-setup.md`,
   and `29-terraform-variable-reference.md` (regenerated, never hand-edited:
   `go run ./tools/refgen/tfvars-md > book/src/29-terraform-variable-reference.md`).

Step 4 is enforced — `TestDemoEnvAllowlistCoversEveryOverride` fails on any
override that cannot reach a blueprint. The rest are not, which is why they are
written down. Step 0 exercised all five for `gateway.class_name` /
`gateway.controller_name`; that is the worked example to copy.

### 5.1 New optional blocks

```yaml
bnk:
  manifest_version: 2.4.0-3.3175.0-0.0.119   # the only required change

  # New, all optional. Consumed only when the derived line is 2.4.
  tmm_replicas: 3                # CNEInstance.tmmReplicas
  cneinstance_size: Tiny         # existing key; "Tiny" becomes a valid value
  watch_namespaces: ["All"]      # CNEInstance.watchNamespaces
  cluster_identifier: staging-small   # advanced.externalBigip.env CLUSTER_IDENTIFIER

  network:
    zones:
      - ext_vlan_cidr:  10.112.9.0/24    # → ipams[external-vlan-ipam]
        int_vip_cidr:   10.122.15.0/24   # → ipams[vip-listener-ipam]
        int_snat_cidr:  10.22.11.0/24    # → ipams[egress-snat-ipam]
        # external_selfip / internal_selfip / int_vlan_cidr: INERT under 2.4
    egress_defaults:               # → Infra.spec.egressDefaults
      subnet: 50.0.0.0/16
      port: 6789
    mtu: 1500                      # → Infra.spec.networks[].vlan.mtu

  ipam:                            # optional Infoblox; absent → no ipamController block
    provider: infoblox
    grid_host: ...
    wapi_version: "2.11.2"
    wapi_port: 443
    insecure: false
    credential_secret: infoblox-login-secret
    certificate_secret: infoblox-ca-cert-secret

gateway:
  listener_hostname: staging-http-gw-f5-app.test.example.com
  route_namespace_selector:        # allowedRoutes selector
    shared-gateway-access: "true"
  egress_gateways:                 # replaces/extends egress_mode under 2.4
    - name: egress-gw-1
      section: egress-vxlan-automap
      selection_mode: NamespaceSelector
      namespaces: [f5-app]
```

The three zone CIDRs already exist and map cleanly onto the three named IPAM
pools — that is the single biggest reason this is tractable. `int_vlan_cidr`,
`external_selfip`, `internal_selfip` are the casualties.

### 5.2 Env overrides

One per new scalar, following the existing table in
`internal/config/envoverride.go` and the five-layer rule in §5.0:
`ROKSBNKCTL_TMM_REPLICAS`, `ROKSBNKCTL_CLUSTER_IDENTIFIER`,
`ROKSBNKCTL_WATCH_NAMESPACES`, `ROKSBNKCTL_EGRESS_SUBNET`,
`ROKSBNKCTL_EGRESS_PORT`, `ROKSBNKCTL_NETWORK_MTU`,
`ROKSBNKCTL_LISTENER_HOSTNAME`, the `INFOBLOX_*` set. Per-zone CIDRs already have
the indexed `ROKSBNKCTL_ZONE<n>_*` family and need no new names.

`internal/config/demo_env_parity_test.go` fails until each is added to
`scripts/demos/blueprint-workflows-ci-demo/.env.example`, so layer 4 needs no
separate checklist. Layers 1, 2, 3 and 5 do — that is what §5.0 is for.

### 5.3 Inert-field handling

`external_selfip`, `internal_selfip`, `int_vlan_cidr`, `vlan_prefixlen*` and
(pending verification) `tmm_k8s_routes` have no 2.4 consumer. Silently ignoring
them is the wrong behaviour: an operator who tunes `vlan_prefixlen` for traffic
shaping — the exact reason it was surfaced — would get no effect and no signal.

**Warn at plan time**, in `guardSupportedCombination`: "`bnk.network.zones[].external_selfip`
is set but BNK 2.4 allocates self-IPs from `Infra.spec.ipams`; the value is
ignored." Warn, never refuse — the field is legitimately still set in a
config.yaml that is being moved forward.

### 5.4 Admission sweep

**Done in Step 0:** `validatingwebhookconfigurations` is now in
`admissionSweepGVRs` (`internal/orchestration/admission_sweep.go`), alongside the
policy and binding. Which of the three the ingress operator uses is a function of
the cluster's OCP version, so sweeping all three is the only version-independent
answer; the names are explicit, so a delete of an absent object is a no-op.

The `oc rollout restart deployment flo-f5-lifecycle-operator` step is harder: the
guide's sequence is *delete CNEInstance → delete webhook → restart FLO → re-apply
CNEInstance*, whereas our sweep runs continuously for the whole apply and never
restarts FLO. Whether the continuous sweep makes the restart unnecessary is
**unknown and must be tested on a 4.19 cluster** — it is the single most likely
cause of a first-`bnk up` failure on 2.4.

---

## 6. Implementation plan

Sized so each step is independently reviewable and each lands green.

**Step 0 — corrections (no 2.4 dependency) — DONE**
- Support matrix 2.4 row → `network_modes: [single-nic]`; the two tests that
  asserted 2.4 + multi-NIC was supported now assert it is refused.
- `gateway_controller_name` derived from `flo_namespace` (§3.2), surfaced through
  all five layers of §5.0 together with `gateway_class_name`, plus a
  `gateway_controller_name` output and a GatewayClass `Accepted` probe in
  `gateway status` — because the bug's defining property is that it is silent.
- `validatingwebhookconfigurations` added to the admission sweep (§5.4). Safe on
  4.18: the name is explicit, so a delete of an absent object is a no-op.
- `cneinstance_deployment_size` description gains `Tiny`. Deliberately still
  unvalidated — the set of legal sizes is a property of the manifest, not of this
  tool, so the operator is the right place to reject a bad one.
- The workspace `gateway:` block turned out to be **entirely undocumented** in the
  book. All nine fields are now in `28-configuration-reference.md`.

**Step 1 — the selector**
- `bnk_line` tfvar rendered from `config.BNKLine(ws)`; threaded to
  `modules/cne_instance` and `modules/gateway`.
- Line-change guard in `create_time_guard.go`.
- Tests: `2.3.*` renders `bnk_line = "2.3"` and produces a byte-identical plan to
  today (the regression gate for the whole PRD); `2.4.0-EA-1` and
  `2.4.0-3.3175.0-0.0.119` both render `"2.4"`; empty manifest still renders the
  default line.

**Step 2 — CNEInstance line-conditional fields**
- `wholeCluster`, `watchNamespaces`, `tmmReplicas`, `placement.dataPlane`,
  `annotations`, `networkAttachments`, `demoMode`, `tmm.rollingUpdate`, and the
  two env lists become locals switched on `var.bnk_line`.
- `externalBigip` block emitted when `bnk.cis` is set **and** line is 2.4.
- Optional `ipamController` block, conditional-list style (absent → no diff).
- Test: golden-file the rendered CNEInstance for both lines.

**Step 3 — split the network CRs out of the BNK phase**
- Move ConfigMap + `F5SPKVlan` into `netcfg_23.tf`, gated `bnk_line != "2.4"`.
- No behaviour change for 2.3. This is the step that makes Step 4 possible.

**Step 4 — the `Infra` CR**
- `modules/gateway/infra_24.tf`: `ipams` from the three zone CIDRs,
  `networkAttachments`, `networks`, `egressDefaults`, `staticRoutes` from the
  existing `gateway_client_subnet_{local,remote}` lists.
- Readiness gate: poll `ipams.fic.f5.com` for `status.IPStatus[].status: Ok`
  before the Gateway CRs apply — the 2.4 analogue of the existing
  `validation_webhook_ready` probe, and for the same reason (the CRD arrives
  mid-reconcile).

**Step 5 — `GatewaySettings`, `Gateway`, `EgressGateway`**
- `config_24.tf` with the three CR kinds; `config_23.tf` holds today's set
  unchanged.
- Gateway moves to `flo_namespace`; HTTPRoute `parentRefs` follows.
- App-namespace SCC binding; ingress-listener SG rule.

**Step 6 — testing + teardown**
- `testing` phase reads the Gateway `status.addresses` instead of computing the
  VIP.
- Teardown order per §2; `fic.f5.com` added to the CRD cleanup list.

**Step 7 — verification**
- One `2.3.*` e2e (the regression gate: nothing changed).
- One `2.4.*` e2e end to end, on OCP 4.19, against the EA registry
  (`devrepo.f5.com`) — which requires FAR credentials for the EA repo we do not
  currently have.
- Only after that run: flip the support-matrix 2.4 row from PROVISIONAL, and add
  the CI plan the matrix comment says every cell must have.

**Step 8 — docs**
- Book chapters for the 2.4 config surface; `refgen` regeneration picks up the
  new tfvars automatically.

Steps 0–3 are safe to land before any 2.4 artifact is obtainable. Steps 4–6 can
be written and unit-tested against golden files, but cannot be *validated*
without Step 7.

---

## 7. Must verify before implementing

Each of these changes the design, not just a value:

1. **`ENABLE_K8S_ROUTES` vs `TMM_K8S_ROUTES`** — boolean replacement, or an
   additional gate? Determines whether `bnk.network.tmm_k8s_routes` survives.
2. **`CLOUD_VPC` / `CLOUD_REGION`** — the guide's prose and its YAML disagree.
   If the controller now discovers both from the trusted profile, our emitting
   them is harmless; if it validates its env, it is not.
3. **Does `Infra` replace `F5SPKVlan` entirely, or coexist?** The guide never
   applies an `F5SPKVlan` under 2.4 and the uninstall list still deletes the
   `k8s.f5net.com` CRDs (which the License CR also uses), so coexistence cannot
   be ruled out from the document alone.
4. **The OCP 4.19 FLO restart** (§5.4) — the highest-risk unknown.
5. **`deploymentSize: "Tiny"`** — new size, or an EA-only value? Affects whether
   `Small` remains a sane default for 2.4.
6. **Is multi-NIC in 2.4 at all?** This guide says no. §3.1 assumes no.
7. **EA registry access** — `devrepo.f5.com` is not `repo.f5.com`. The FAR auth
   tarball we hold may not authenticate against it, which blocks Step 7 entirely
   and is worth resolving early.

---

## 8. Effort and risk

The `bnk`-phase work (Steps 1–3) is mechanical: conditional locals over a body we
already build, plus a file split. The `gateway`-phase work (Steps 4–6) is a
rewrite of a phase against an API we have never run, whose addressing model is
inverted from the one every one of our defaults assumes.

The plan's real dependency is not engineering time — it is Step 7. Until a 2.4
cluster exists, everything from Step 4 on is an unverified translation of a
document that already contradicts itself in two places (§7.2). The support
matrix comment already states the rule that applies: *"A cell with no coverage is
a claim nobody has checked."*
