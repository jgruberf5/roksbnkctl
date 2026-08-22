# Configuration reference

<!-- GENERATED FILE — DO NOT EDIT.
     Produced by: go run ./tools/refgen/config-md > book/src/28-configuration-reference.md
     Enforced by: TestConfigReferenceIsCurrent in internal/config.
     Edit the struct doc comments in internal/config/workspace.go instead. -->

Field-by-field schema for the workspace `config.yaml`, generated from the
[`Workspace` struct](https://github.com/jgruberf5/roksbnkctl/blob/main/internal/config/workspace.go)
in `internal/config/workspace.go`. This chapter is the single source of truth for
what a field is called, what type it takes, and which BNK line it applies to.

[Chapter 12 — Workspace config](./12-workspace-config.md) is the *teaching* chapter:
use it to learn the shape. Use this one to look up a specific field. Every other
chapter links here rather than restating fields, because a field restated in four
places is a field that will disagree with itself by the next release.

**The `line` column** says which BNK release line a field applies to. Most apply to
both. A field marked 2.4 has no effect on a 2.3 install and vice versa — the line
itself is selected by `bnk.manifest_version` and nothing else.

**Required** means the field has no `omitempty`: it is always rendered, and its zero
value is meaningful. It does not mean you must type it into your `config.yaml`.

**Default** is carried on the field as a `default:"..."` struct tag. A dash means no
default is declared — either the zero value applies, or the default is computed at
run time and belongs in the prose rather than a table cell.

## `Workspace`

Workspace is ~/.roksbnkctl/<name>/config.yaml.  Mirrors the per-workspace example in docs/PRD.md. Note that there is no `api_key` field — secrets live in env vars or the OS keychain, never in this struct. Plaintext keys in the YAML are rejected at load time by rejectPlaintextSecrets.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `ibmcloud` | `IBMCloudCfg` | 2.3 + 2.4 | — | yes |  |
| `cluster` | `ClusterCfg` | 2.3 + 2.4 | — | yes |  |
| `bnk` | `BNKCfg` | 2.3 + 2.4 | — | no |  |
| `gateway` | `GatewayCfg` | 2.3 + 2.4 | — | no |  |
| `registry` | `*RegistryCfg` | 2.3 + 2.4 | — | no |  |
| `test` | `TestCfg` | 2.3 + 2.4 | — | no |  |
| `tf_source` | `TFSourceCfg` | 2.3 + 2.4 | — | yes |  |
| `cos` | `*COSCfg` | 2.3 + 2.4 | — | no |  |
| `targets` | `map[string]TargetCfg` | 2.3 + 2.4 | — | no |  |
| `state` | `StateCfg` | 2.3 + 2.4 | — | no |  |
| `bnkforge` | `*BNKForgeCfg` | 2.3 + 2.4 | — | no |  |
| `agent` | `*AgentCfg` | 2.3 + 2.4 | — | no |  |
| `prefix` | `string` | 2.3 + 2.4 | — | no | Prefix is the workspace's account-scoped resource-name base. |
| `resources` | `*ResourcesCfg` | 2.3 + 2.4 | — | no | Resources carries the per-resource create toggles (and the existing-resource name/ID for any declined-but-still-depended-on resource). |
| `exec` | `map[string]ExecToolCfg` | 2.3 + 2.4 | — | no | Exec is the per-tool execution-backend config block introduced in Sprint 3 (PRD 03). |

## `AdvancedComponentCfg`

AdvancedComponentCfg is one component's advanced settings. Only env is surfaced today; the CR's advanced block carries more, and this is the shape that lets those arrive without moving what already works.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `env` | `map[string]string` | 2.3 + 2.4 | — | no |  |

## `AgentCfg`

AgentCfg configures roksbnkctl's agentic mode (the `agent` command). It is purely advisory metadata for launching an external coding-agent CLI against the workspace's scaffolded AGENTS.md + personas/ — roksbnkctl embeds no LLM. nil/absent = `agent` defaults to claude and the CLI's own endpoint config.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `default` | `string` | 2.3 + 2.4 | — | no | Default is the agentic CLI `roksbnkctl agent` (no arg) reports as this workspace's default — claude \| gemini \| aider \| openai \| pi \| opencode. |
| `llm_endpoint` | `string` | 2.3 + 2.4 | — | no | LLMEndpoint is an optional OpenAI-/Anthropic-compatible base URL woven into the printed invocation (cloud vendor, local vLLM, etc.). |

## `BNKCISCfg`

BigIPPasswordB64 stores the password base64-encoded (obfuscation, NOT encryption — like ibmcloud.api_key_b64); the raw value is rendered to terraform.tfvars as bigip_password at apply time.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `bigip_url` | `string` | 2.3 + 2.4 | — | no |  |
| `bigip_username` | `string` | 2.3 + 2.4 | — | no |  |
| `bigip_password_b64` | `string` | 2.3 + 2.4 | — | no |  |

## `BNKCertManagerCfg`

BNKCertManagerCfg overrides cert-manager's install coordinates. All optional; the create/skip decision stays on resources.cert_manager.create.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `namespace` | `string` | 2.3 + 2.4 | — | no | Namespace cert-manager installs into (rendered as cert_manager_namespace). |
| `version` | `string` | 2.3 + 2.4 | — | no | Version pins the cert-manager Helm chart (rendered as cert_manager_version). |

## `BNKCfg`

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `cneinstance_size` | `string` | 2.3 + 2.4 | — | no |  |
| `far_repo_url` | `string` | 2.3 + 2.4 | — | no |  |
| `manifest_version` | `string` | 2.3 + 2.4 | — | no |  |
| `far_auth_file` | `string` | 2.3 + 2.4 | — | no | FarAuthFile is the FAR auth tarball's filename in the orchestration COS bucket; rendered as the f5_cne_far_auth_file tfvar + used by `registry` to resolve the FAR _json_key_base64 service account. |
| `subscription_jwt_file` | `string` | 2.3 + 2.4 | — | no | SubscriptionJWTFile is the subscription/license JWT's filename in the orchestration COS bucket; rendered as the f5_cne_subscription_jwt_file tfvar. |
| `far_auth_local_file` | `string` | 2.3 + 2.4 | — | no | FarAuthLocalFile / SubscriptionJWTLocalFile point at LOCAL files instead of COS objects. |
| `subscription_jwt_local_file` | `string` | 2.3 + 2.4 | — | no |  |
| `trusted_profile` | `*BNKTrustedProfileCfg` | 2.3 + 2.4 | — | no | TrustedProfile tunes the IBM Cloud Trusted Profile the CNE controller assumes to manage its own cluster's VPC. |
| `flo_namespace` | `string` | 2.3 + 2.4 | `f5-bnk` | no | FLONamespace / FLOUtilsNamespace override the namespaces the F5 Lifecycle Operator and its utility components install into (rendered as flo_namespace / flo_utils_namespace). |
| `flo_utils_namespace` | `string` | 2.3 + 2.4 | `f5-utils` | no |  |
| `gateway_api_mtls` | `bool` | 2.4 | — | no | GatewayAPIMTLS opts into the Gateway API bundle BNK 2.4 needs for mTLS (#170). |
| `tmm_replicas` | `int` | 2.4 | `3` | no | TMMReplicas is the number of f5-tmm data-plane replicas. |
| `watch_namespaces` | `[]string` | 2.4 | `All` | no | WatchNamespaces are the namespaces the CNE controller watches. |
| `tmm_anti_affinity` | `*bool` | 2.4 | `true` | no | TMMAntiAffinity requires f5-tmm pods onto different nodes. |
| `tmm_anti_affinity_topology_key` | `string` | 2.4 | `kubernetes.io/hostname` | no | TMMAntiAffinityTopologyKey is the node label the anti-affinity rule spreads across — the IBM ROKS per-node label. |
| `tmm_zone_spread` | `*bool` | 2.4 | `true` | no | TMMZoneSpread spreads f5-tmm pods across zones. |
| `tmm_zone_topology_key` | `string` | 2.4 | `topology.kubernetes.io/zone` | no | TMMZoneTopologyKey is the IBM ROKS zone label the spread constraint uses. |
| `tmm_zone_max_skew` | `int` | 2.4 | `1` | no | TMMZoneMaxSkew is maxSkew for the zone topology-spread constraint. |
| `tmm_zone_when_unsatisfiable` | `string` | 2.4 | `DoNotSchedule` | no | TMMZoneWhenUnsatisfiable is DoNotSchedule or ScheduleAnyway. |
| `tmm_pod_label` | `string` | 2.4 | `f5-tmm` | no | TMMPodLabel is the `app` label value the placement rules select TMM by. |
| `tmm_rolling_update` | `*bool` | 2.4 | `true` | no | TMMRollingUpdate pins TMM's rolling update to maxSurge 0 / maxUnavailable 1 — the same shape as the cwc Multi-Attach deadlock, where an unconstrained rolling update on a single-attach resource wedges. |
| `external_bigip` | `*bool` | 2.4 | `false` | no | ExternalBigIP enables the external BIG-IP controller. |
| `external_bigip_login_secret` | `string` | 2.4 | `f5-bigip-ctlr-login` | no | ExternalBigIPLoginSecret holds the external BIG-IP credentials. |
| `cluster_identifier` | `string` | 2.4 | — | no | ClusterIdentifier is passed to the external BIG-IP controller. |
| `gateway_api_version` | `string` | 2.4 | `1.5.0` | no | GatewayAPIVersion is GATEWAY_API_VERSION for the CNE controller. |
| `demo_mode` | `*bool` | 2.3 + 2.4 | `false on 2.4, true on 2.3` | no | DemoMode sets advanced.demoMode.enabled. |
| `tcp_settings` | `map[string]string` | 2.3 + 2.4 | — | no | TCPSettings overrides fields on the data-plane F5BigTcpSetting CR. |
| `tcp_settings_name` | `string` | 2.3 + 2.4 | `sys-default-tcp` | no | TCPSettingsName is the F5BigTcpSetting to write. |
| `advanced` | `map[string]AdvancedComponentCfg` | 2.3 + 2.4 | — | no | Advanced carries per-component environment passthrough for the 2.4 CNEInstance's advanced.<component>.env[] lists (#175). |
| `gslb_datacenter_name` | `string` | 2.3 + 2.4 | — | no | GSLBDatacenterName sets the optional CNEInstance GSLB datacenter name (rendered as cneinstance_gslb_datacenter_name). |
| `gtm` | `*BNKGTMCfg` | 2.3 + 2.4 | — | no | GTM is the BIG-IP DNS the datacenter above registers with (#51). |
| `cert_manager` | `*BNKCertManagerCfg` | 2.3 + 2.4 | — | no | CertManager overrides cert-manager's namespace + chart version. |
| `network` | `*BNKNetworkCfg` | 2.3 + 2.4 | — | no | Network holds the optional per-zone subnet CIDRs + TMM self-IPs for the cloud-network-mapping ConfigMap and the external/internal F5SPKVlan CRs (BNK install-guide "Configuration"). |
| `cis` | `*BNKCISCfg` | 2.3 + 2.4 | — | no | CIS holds the BIG-IP management endpoint + credentials the BNK CIS controller (k8s-bigip-ctlr) uses. |
| `license_mode` | `string` | 2.3 + 2.4 | `connected` | no | LicenseMode selects the License CR operationMode: "connected" (default when empty), "disconnected", or "f5licenseproxy". |
| `flp` | `*BNKFLPCfg` | 2.3 + 2.4 | — | no | FLP holds settings for the optional F5 License Proxy phase. |
| `preflight` | `*BNKPreflightCfg` | 2.3 + 2.4 | — | no | Preflight tunes the pre-install checks `bnk up` runs before it plans anything. |

## `BNKFLPCfg`

BNKFLPCfg configures the F5 License Proxy (FLP) phase deployment. All optional; nil block means FLP is off. It never carries secrets — the FLP generates its own certs, and its subscription JWT is the same one resolved from COS.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `mode` | `string` | 2.3 + 2.4 | — | no | Mode selects HOW the FLP phase deploys the proxy: "" \| "helm" → the f5-license-proxy Helm chart into the ROKS cluster (default). |
| `vsi` | `*BNKFLPVSICfg` | 2.3 + 2.4 | — | no | VSI configures the mode: vsi deployment backend. |
| `namespace` | `string` | 2.3 + 2.4 | — | no | Namespace the FLP is installed into (helm mode). |
| `chart_version` | `string` | 2.3 + 2.4 | — | no | ChartVersion pins the f5-license-proxy chart. |
| `storage_class` | `string` | 2.3 + 2.4 | — | no | StorageClass is the dynamic StorageClass for the FLP's PVCs (rendered as flp_storage_class). |
| `node_port_access` | `bool` | 2.3 + 2.4 | — | no | NodePortAccess exposes the proxy OUTSIDE its own cluster, so a BNK install in a DIFFERENT cluster (same VPC, or across a transit gateway) can license through it — the "shared licensing cluster" topology, where only the cluster running the proxy needs egress to F5. |
| `node_port_source_cidrs` | `[]string` | 2.3 + 2.4 | — | no | NodePortSourceCIDRs, when set with NodePortAccess, opens the proxy's NodePort on the cluster's worker security group to these CIDRs — the consuming cluster's subnets. |
| `external` | `*BNKFLPExternalCfg` | 2.3 + 2.4 | — | no | External points a workspace at a FOREIGN proxy — one deployed by a DIFFERENT workspace/cluster. |

## `BNKFLPExternalCfg`

BNKFLPExternalCfg addresses an F5 License Proxy that this workspace does not own. Both fields come from the owning workspace's `roksbnkctl flp output` — the URL is its externally-reachable endpoint and the CA is its (base64) root CA, which the CWC must trust to complete the TLS handshake to the proxy.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `url` | `string` | 2.3 + 2.4 | — | no | URL of the proxy, e.g. |
| `root_ca_b64` | `string` | 2.3 + 2.4 | — | no | RootCAB64 is the proxy's root CA, base64-encoded (as `flp output` emits it). |

## `BNKFLPForwardProxyCfg`

BNKFLPForwardProxyCfg describes an egress forward proxy for the FLP VSI's calls to F5's licensing backend (product-s.apis.f5.com).

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `host` | `string` | 2.3 + 2.4 | — | no |  |
| `port` | `int` | 2.3 + 2.4 | — | no |  |
| `protocol` | `string` | 2.3 + 2.4 | — | no | http (default) \| https. |

## `BNKFLPVSICfg`

BNKFLPVSICfg configures the mode: vsi FLP backend — a standalone VSI running the f5-license-proxy stack as a podman pod. All fields optional; sensible defaults apply.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `name_prefix` | `string` | 2.3 + 2.4 | — | no | NamePrefix prefixes the FLP VSI's IBM Cloud resource names — instance, subnet, security group, floating IP, public gateway, boot volume, and the VPC when this module creates one. |
| `vpc` | `string` | 2.3 + 2.4 | — | no | VPC is an existing VPC id to deploy the standalone FLP VSI into, WITHOUT any ROKS cluster. |
| `create_vpc` | `bool` | 2.3 + 2.4 | — | no | CreateVPC builds the proxy its OWN VPC, address prefix and public gateway rather than placing it in one that already exists (#60). |
| `vpc_name` | `string` | 2.3 + 2.4 | — | no | VPCName names the VPC created when CreateVPC is set. |
| `subnet_cidr` | `string` | 2.3 + 2.4 | — | no | SubnetCIDR is the address prefix for that VPC. |
| `profile` | `string` | 2.3 + 2.4 | — | no | Profile is the IBM Cloud VSI instance profile. |
| `zone` | `string` | 2.3 + 2.4 | — | no | Zone the VSI lands in (e.g. |
| `boot_size_gb` | `int` | 2.3 + 2.4 | — | no | BootSizeGB is the boot volume size. |
| `reach` | `string` | 2.3 + 2.4 | — | no | Reach selects the address the CWC dials: "private" (default — the VSI's VPC IP, for a CWC in the same/peered VPC) or "floating" (a public floating IP). |
| `management_allowed_cidrs` | `[]string` | 2.3 + 2.4 | — | no | ManagementAllowedCIDRs are the source CIDRs permitted to reach the :80 flp-status web UI (read-only status). |
| `licensing_allowed_cidrs` | `[]string` | 2.3 + 2.4 | — | no | LicensingAllowedCIDRs are the source CIDRs permitted to reach the :8443 licensing proxy (and :22 SSH). |
| `allowed_cidrs` | `[]string` | 2.3 + 2.4 | — | no | AllowedCIDRs is DEPRECATED — a legacy single list. |
| `ssh_key` | `string` | 2.3 + 2.4 | — | no | SSHKey is the name of an existing IBM Cloud VPC SSH key (RSA) to attach to the FLP VSI, so an operator can SSH in to inspect/recover the licensing appliance (podman pod, Vault, logs). |
| `floating_ip` | `*bool` | 2.3 + 2.4 | — | no | FloatingIP attaches an operator floating IP to the FLP VSI for remote management — running `roksbnkctl flp status` and reaching the :80 web UI + the :8443 proxy from a machine OUTSIDE the VPC. |
| `status_image` | `string` | 2.3 + 2.4 | — | no | StatusImage, when set, runs the flp-status web UI as a container in the FLP pod (mobile-friendly status page + /api/status + live logs on :80, no auth — a read-only private status endpoint). |
| `status_registry_host` | `string` | 2.3 + 2.4 | — | no | StatusRegistryHost + StatusRegistryCAB64 make the VSI trust a self-signed mirror so it can pull StatusImage: cloud-init drops the (base64) CA into /etc/containers/certs.d/<host>/ca.crt before the pod comes up. |
| `status_registry_ca_b64` | `string` | 2.3 + 2.4 | — | no |  |
| `forward_proxy` | `*BNKFLPForwardProxyCfg` | 2.3 + 2.4 | — | no | ForwardProxy optionally routes the VSI's egress to F5 licensing through an HTTP forward proxy (air-gapped/egress-controlled networks). |

## `BNKForgeCfg`

BNKForgeCfg is the optional integration with a co-located BNK Forge (v3) install. When Register is true, `cluster up` registers the just-provisioned ROKS cluster with BNK Forge via its REST API — credential-backed, so BNK Forge derives the kubeconfig on demand from an IBM Cloud credential template rather than storing a perishable one. Best-effort: registration never blocks or fails the deploy. nil/absent (legacy config) = no-op.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `register` | `bool` | 2.3 + 2.4 | — | no | Register opts the workspace in. |
| `url` | `string` | 2.3 + 2.4 | — | no | URL is the BNK Forge server base URL (e.g. |
| `project` | `string` | 2.3 + 2.4 | — | no | Project is the target BNK Forge project NAME. |
| `username` | `string` | 2.3 + 2.4 | — | no | Username is the BNK Forge login user. |
| `insecure` | `bool` | 2.3 + 2.4 | — | no | Insecure skips TLS verification against the Forge server. |
| `ca_b64` | `string` | 2.3 + 2.4 | — | no | CAB64 pins the CA the Forge server's certificate must chain to, PEM, base64-encoded. |

## `BNKGTMCfg`

BNKCISCfg configures the BNK CIS controller's BIG-IP target. All optional. BNKGTMCfg is the BIG-IP DNS / GTM the CNE controller registers its GSLB datacenter with (#51) — the connection half of GSLB, which until now only had the datacenter NAME.  The password is stored base64-encoded (obfuscation, NOT encryption — like ibmcloud.api_key_b64 and bnk.cis.bigip_password_b64) and rendered raw into terraform.tfvars at apply time.  Absent → nothing is emitted and the CNEInstance is unchanged, so GSLB stays exactly as it behaves today for every workspace that does not use it.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `url` | `string` | 2.3 + 2.4 | — | no | URL of the GTM/BIG-IP DNS management endpoint, e.g. |
| `username` | `string` | 2.3 + 2.4 | — | no | Username to authenticate with. |
| `password_b64` | `string` | 2.3 + 2.4 | — | no | PasswordB64 is the base64 of the password. |

## `BNKNetworkCfg`

BNKNetworkCfg is the optional cloud-network-mapping / VLAN zone data.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `zones` | `[]BNKZoneCfg` | 2.3 + 2.4 | — | no |  |
| `vlan_prefixlen` | `*int` | 2.3 + 2.4 | — | no | VLANPrefixLen is the self-IP prefix length (spec.prefixlen_v4) TMM applies to its external and internal self-IPs on the F5SPKVlan CRs — the size of the L2 subnet TMM treats as directly connected on each VLAN. |
| `vlan_prefixlen_external` | `*int` | 2.3 + 2.4 | — | no | VLANPrefixLenExternal / VLANPrefixLenInternal override VLANPrefixLen for one VLAN. |
| `vlan_prefixlen_internal` | `*int` | 2.3 + 2.4 | — | no |  |
| `tmm_k8s_routes` | `string` | 2.3 + 2.4 | — | no | TMMK8SRoutes is the Kubernetes pod CIDR TMM installs a route toward (advanced.tmm.env TMM_K8S_ROUTES), so TMM can reach backend pods on the internal data path. |

## `BNKPreflightCfg`

BNKPreflightCfg tunes the per-node reachability gate.  These are exposed because the right values are a property of the ENVIRONMENT, not of roksbnkctl. A Transit Gateway attachment is asynchronous — IBM programs the routes some time after the connection reports `attached` — and how long that takes varies by account, region and gateway. A probe run ~73s after attach saw both targets unreachable and refused an install whose path was healthy minutes later (issue #57), while a sibling cluster on the same gateway passed simply by landing on the other side of route programming.  A site that consistently sees slower propagation should raise the budget rather than rediscover the race; a site with a static, long-established gateway can lower it to fail faster.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `reachability_retry_seconds` | `*int` | 2.3 + 2.4 | — | no | ReachabilityRetrySeconds is how long a target may keep failing before the verdict is believed. |
| `reachability_timeout_seconds` | `*int` | 2.3 + 2.4 | — | no | ReachabilityTimeoutSeconds is how long to wait for the probe DaemonSet to report from every node. |

## `BNKTrustedProfileCfg`

BNKTrustedProfileCfg is the IBM Cloud Trusted Profile the CNE controller assumes at runtime — the identity that lets it manage the VPC network attachments it creates for TMM, without a stored API key.  Both fields default in the HCL rather than here, so an absent block renders nothing and the shipped terraform decides — the same rule every other optional BNK field follows.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `service_account` | `string` | 2.3 + 2.4 | — | no | ServiceAccount is the Kubernetes service account the profile is LINKED to: which account may assume it. |
| `roles` | `[]string` | 2.3 + 2.4 | `[Viewer, Editor]` | no | Roles granted to the profile, scoped to the cluster's OWN VPC. |

## `BNKZoneCfg`

BNKZoneCfg is one availability zone's subnet CIDRs + TMM self-IPs. Field order/names match the terraform cneinstance_network_zones object.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `ext_vlan_cidr` | `string` | 2.3 + 2.4 | — | yes |  |
| `int_vlan_cidr` | `string` | 2.3 + 2.4 | — | yes |  |
| `int_snat_cidr` | `string` | 2.3 + 2.4 | — | yes |  |
| `int_vip_cidr` | `string` | 2.3 + 2.4 | — | yes |  |
| `external_selfip` | `string` | 2.3 + 2.4 | — | yes |  |
| `internal_selfip` | `string` | 2.3 + 2.4 | — | yes |  |

## `COSCfg`

COSCfg points roksbnkctl at the IBM Cloud Object Storage that holds the FAR auth key + subscription JWT (the "orchestration" COS). Empty fields fall back to the built-in defaults (bnk-supply-chain / bnk-artifacts / us-south). These are honoured BOTH by the terraform render (ibmcloud_cos_instance_name / ibmcloud_resources_cos_bucket / ibmcloud_cos_bucket_region) AND by the `registry` FAR-file resolver, so a customer-owned COS bucket is used consistently across both.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `instance` | `string` | 2.3 + 2.4 | — | no |  |
| `bucket` | `string` | 2.3 + 2.4 | — | no |  |
| `region` | `string` | 2.3 + 2.4 | — | no |  |
| `upload` | `[]COSUpload` | 2.3 + 2.4 | — | no |  |

## `COSUpload`

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `source` | `string` | 2.3 + 2.4 | — | yes |  |
| `key` | `string` | 2.3 + 2.4 | — | yes |  |

## `ClusterCfg`

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `create` | `bool` | 2.3 + 2.4 | — | yes |  |
| `name` | `string` | 2.3 + 2.4 | — | yes |  |
| `openshift_version` | `string` | 2.3 + 2.4 | — | no |  |
| `workers_per_zone` | `int` | 2.3 + 2.4 | `1` | no |  |
| `public_gateway` | `*bool` | 2.3 + 2.4 | `true` | no | PublicGateway controls whether the cluster subnets attach a public gateway for worker Internet egress. |
| `vpc_cidr` | `string` | 2.3 + 2.4 | — | no | VPCCIDR is the block the cluster VPC's per-zone address prefixes are carved from — "10.241.0.0/16" becomes 10.241.0.0/18, 10.241.64.0/18, 10.241.128.0/18. |
| `network_mode` | `string` | 2.3 + 2.4 | `single-nic` | no | NetworkMode selects how the cluster's worker nodes are attached: "single-nic" (the default, and today's only behaviour) or "multi-nic". |
| `existing_subnet_ids` | `[]string` | 2.3 + 2.4 | — | no | ExistingSubnetIDs places the cluster in subnets that ALREADY EXIST, one per zone in zone order, instead of creating them (#61). |
| `min_worker_vcpu_count` | `int` | 2.3 + 2.4 | `16` | no | MinWorkerVCPUCount / MinWorkerMemoryGB drive the worker-flavor auto-select (the cluster module picks the smallest bx2 profile meeting both minimums). |
| `min_worker_memory_gb` | `int` | 2.3 + 2.4 | `64` | no |  |

## `ConnectivityCfg`

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `extra_hosts` | `[]string` | 2.3 + 2.4 | — | no |  |

## `DNSCfg`

DNSCfg drives the Sprint 5 flag-driven DNS probe (PRD 03 §"DNS probe (GSLB-aware)" §"Server resolution"). The map's keys are the names users pass to `--server <name>` and the values are concrete "<ip>[:<port>]" strings the miekg/dns client dials. DefaultTarget is used when --target isn't passed on the command line.  Example:  test: dns: resolvers: google:     "8.8.8.8:53" cloudflare: "1.1.1.1:53" gslb-vip:   "169.45.91.5:53" default_target: "www.example.com"

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `resolvers` | `map[string]string` | 2.3 + 2.4 | — | no |  |
| `default_target` | `string` | 2.3 + 2.4 | — | no |  |

## `ExecToolCfg`

ExecToolCfg is one entry under workspace.Exec — the chosen backend for a given tool.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `backend` | `string` | 2.3 + 2.4 | — | yes | Backend is the execution-backend spec: "local" \| "docker" \| "k8s" \| "ssh:<target>". |

## `GatewayCfg`

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `app_namespace` | `string` | 2.3 + 2.4 | — | no |  |
| `class_name` | `string` | 2.3 + 2.4 | — | no | ClassName is the GatewayClass name. |
| `controller_name` | `string` | 2.3 + 2.4 | — | no | ControllerName is the GatewayClass controllerName. |
| `backend_service` | `string` | 2.3 + 2.4 | — | no |  |
| `backend_port` | `int` | 2.3 + 2.4 | — | no |  |
| `egress_mode` | `string` | 2.3 + 2.4 | — | no | snatpool \| automap \| both. |
| `client_subnet_local` | `[]string` | 2.3 + 2.4 | — | no |  |
| `client_subnet_remote` | `[]string` | 2.3 + 2.4 | — | no |  |
| `vxlan_port` | `int` | 2.3 + 2.4 | — | no |  |
| `route_examples` | `[]string` | 2.3 + 2.4 | — | no | RouteExamples names extra route kinds to create WORKING examples of, alongside the HTTPRoute the gateway phase already creates. |
| `l4_listener_port` | `int` | 2.3 + 2.4 | — | no | L4ListenerPort is the port for that TCP listener. |

## `IBMCloudCfg`

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `region` | `string` | 2.3 + 2.4 | — | yes |  |
| `resource_group` | `string` | 2.3 + 2.4 | `default` | yes |  |
| `api_key_source` | `string` | 2.3 + 2.4 | — | no | env \| keychain \| config \| prompt — see secrets.go. |
| `api_key_b64` | `string` | 2.3 + 2.4 | — | no | APIKeyB64 stores the API key base64-encoded inline in the workspace config. |

## `RegistryCfg`

RegistryCfg configures the Sprint 29 air-gap registry mirror (PRD 11): which target the `roksbnkctl registry replicate` populates and which namespace it uses, plus the optional source/target credentials. All fields are optional — an absent block (nil) means the mirror is not configured and the BNK install pulls directly from FAR (far_repo_url). Additive + omitempty, so existing config.yaml files load unchanged.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `target` | `string` | 2.3 + 2.4 | — | no | Target selects the mirror backend: "icr" (IBM Container Registry — the DEFAULT when unset) or "generic" (any OCI-compliant registry — Artifactory / Harbor / Quay / registry:2). |
| `icr_host` | `string` | 2.3 + 2.4 | — | no | ICRHost overrides the IBM Container Registry host for target=icr (e.g. |
| `icr_namespace` | `string` | 2.3 + 2.4 | — | no | ICRNamespace is the ICR namespace artifacts nest under for target=icr. |
| `generic_host` | `string` | 2.3 + 2.4 | — | no | GenericHost is the OCI registry host for target=generic (e.g. |
| `generic_repo_prefix` | `string` | 2.3 + 2.4 | — | no | GenericRepoPrefix is the repository path artifacts nest under for target=generic (e.g. |
| `generic_username` | `string` | 2.3 + 2.4 | — | no | GenericUsername / GenericPasswordB64 are the static basic-auth credential for target=generic (an Artifactory user + access token). |
| `generic_password_b64` | `string` | 2.3 + 2.4 | — | no |  |
| `generic_ca_b64` | `string` | 2.3 + 2.4 | — | no | GenericCAB64 is the mirror's CA chain, PEM, base64-encoded — the AUTHORITATIVE copy, recorded from the file that generated it rather than learned from the network. |
| `generic_ca_sha256` | `string` | 2.3 + 2.4 | — | no | GenericCASHA256 pins the mirror's CA by SHA-256 (hex; a "sha256:" prefix and colons are accepted). |
| `namespace` | `string` | 2.3 + 2.4 | — | no | Namespace is the mirror project the artifacts land in. |
| `include_deps` | `*bool` | 2.3 + 2.4 | — | no | IncludeDeps unions the non-F5 dependency artifacts (Jetstack cert-manager chart + images, the bitnami/kubectl node-labeler image) into the BOM. |
| `source_service_account_b64` | `string` | 2.3 + 2.4 | — | no | SourceServiceAccountB64 is the FAR `_json_key_base64` service-account JSON, base64-encoded, used as the replication SOURCE credential for repo.f5.com. |

## `ResourceToggle`

ResourceToggle is one create/reuse decision: Create=true provisions the resource (under its prefix-derived name); Create=false reuses an existing one named by Existing (when a live dependent consumes it).

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `create` | `bool` | 2.3 + 2.4 | — | yes |  |
| `existing` | `string` | 2.3 + 2.4 | — | no | existing name/ID when Create=false. |

## `ResourcesCfg`

ResourcesCfg holds the per-resource create toggles for a prefix-driven workspace (Sprint 26). The cluster itself is NOT here — it reuses the existing ClusterCfg.Create / ClusterCfg.Name (Name doubles as the existing id/name when Create=false, as today). Each toggle carries an Existing name/ID used when Create=false and a live dependent still needs to reference the resource by name.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `transit_gateway` | `ResourceToggle` | 2.3 + 2.4 | — | yes |  |
| `registry_cos` | `ResourceToggle` | 2.3 + 2.4 | — | yes |  |
| `cert_manager` | `ResourceToggle` | 2.3 + 2.4 | — | yes |  |
| `bnk` | `ResourceToggle` | 2.3 + 2.4 | — | yes |  |
| `tgw_jumphost` | `ResourceToggle` | 2.3 + 2.4 | — | yes |  |
| `cluster_jumphosts` | `ResourceToggle` | 2.3 + 2.4 | — | yes |  |
| `client_vpc` | `ResourceToggle` | 2.3 + 2.4 | — | yes |  |
| `cluster_vpc` | `ResourceToggle` | 2.3 + 2.4 | — | yes | ClusterVPC controls the ROKS cluster's OWN VPC. |
| `client_region` | `string` | 2.3 + 2.4 | — | no | ClientRegion is the region the testing client (TGW jumphost + client VPC) is installed in. |
| `testing_client_vpc_name` | `string` | 2.3 + 2.4 | — | no | TestingClientVPCName names the testing client VPC when ClientVPC.Create is true (rendered as testing_client_vpc_name). |
| `testing_ssh_key_name` | `string` | 2.3 + 2.4 | — | no | TestingSSHKeyName is the IBM Cloud VPC SSH key name attached to the testing jumphosts (rendered as testing_ssh_key_name). |
| `testing_jumphost_profile` | `string` | 2.3 + 2.4 | — | no | Jumphost sizing. |
| `testing_min_vcpu_count` | `int` | 2.3 + 2.4 | — | no |  |
| `testing_min_memory_gb` | `int` | 2.3 + 2.4 | — | no |  |
| `testing_jumphost_allowed_cidrs` | `[]string` | 2.3 + 2.4 | — | no | Security-group source CIDRs, following the flp_vsi module's split between a management plane and an in-fabric plane. |
| `testing_client_vpc_inbound_cidrs` | `[]string` | 2.3 + 2.4 | — | no | TestingClientVPCInboundCIDRs gates the client VPC's DEFAULT security group, which the testing phase widens to all protocols and ports. |
| `cluster_http_allowed_cidrs` | `[]string` | 2.3 + 2.4 | — | no | ClusterHTTPAllowedCIDRs gates :80 on the cluster security group — the ingress/ALB path, which is meant to be publicly reachable, so empty means open. |
| `cluster_vpc_default_sg_inbound_cidrs` | `[]string` | 2.3 + 2.4 | — | no | ClusterVPCDefaultSGInboundCIDRs gates the cluster VPC's DEFAULT security group, which the cluster phase widens to all protocols and ports. |
| `copied_ssh_key_files` | `[]string` | 2.3 + 2.4 | — | no | CopiedSSHKeyFiles lists the ~/.ssh basenames `roksbnkctl init` ACTUALLY wrote when the user accepted the "copy the private key to ~/.ssh" prompt (only files it created — pre-existing files are skipped, never recorded). |

## `StateCfg`

GatewayCfg carries optional overrides for the Gateway phase (the BNK data-plane config). Every field is optional — unset values fall back to the terraform gateway module's BNK install-guide defaults. Rendered as gateway_* tfvars. The phase itself is driven by `roksbnkctl gateway up/down`, not a toggle here. StateCfg selects where terraform state lives (PRD 16). Backend "" or "local" (the default) keeps per-phase local tfstate under the workspace dir — the original behaviour. "s3" stores each phase's state in an S3-compatible bucket (IBM COS), so a stateless runner / parallel CI needs no shared volume, with native lockfile locking (terraform >= 1.10). Additive + omitempty — an absent `state:` block loads as the local default.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `backend` | `string` | 2.3 + 2.4 | — | no | "" \| "local" \| "s3". |
| `s3` | `*StateS3Cfg` | 2.3 + 2.4 | — | no |  |

## `StateS3Cfg`

StateS3Cfg configures the COS/S3 remote backend. The HMAC access/secret keys are NOT stored here — *KeySource names the env var they come from (env-first), and roksbnkctl injects them as AWS_* env to the terraform child, never into the rendered HCL or the state object.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `endpoint` | `string` | 2.3 + 2.4 | — | yes | COS S3 endpoint URL. |
| `bucket` | `string` | 2.3 + 2.4 | — | yes | pre-provisioned bucket. |
| `region` | `string` | 2.3 + 2.4 | — | yes | COS location / region. |
| `key_prefix` | `string` | 2.3 + 2.4 | — | no | default: the workspace name. |
| `access_key_source` | `string` | 2.3 + 2.4 | — | no | env var name; default ROKSBNKCTL_COS_HMAC_ACCESS_KEY. |
| `secret_key_source` | `string` | 2.3 + 2.4 | — | no | env var name; default ROKSBNKCTL_COS_HMAC_SECRET_KEY. |

## `TFSourceCfg`

TFSourceCfg picks where Terraform's source tree comes from. Type drives which other fields apply:  - embedded — uses the HCL bundled into the roksbnkctl binary via Go's embed directive. No other fields needed. This is the default and what most users want; install one binary, get CLI + matched TF together. - github — downloads a tarball release from a GitHub repo. Repo ("owner/name") and Ref (release tag) required. For testing forks or pinning to a specific upstream tag. - local — points Terraform at a directory on disk. Path required. For active development on the HCL itself.  An empty Type (legacy / forgot-to-set) is treated as embedded.

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `type` | `string` | 2.3 + 2.4 | — | yes | embedded \| github \| local. |
| `repo` | `string` | 2.3 + 2.4 | — | no |  |
| `ref` | `string` | 2.3 + 2.4 | — | no |  |
| `path` | `string` | 2.3 + 2.4 | — | no | populated for type=local. |

## `TargetCfg`

TargetCfg is the on-disk shape of one entry under `targets:` in the workspace config. Lives in this package (rather than internal/remote) to avoid an import cycle: workspace.go needs to (de)serialise it, internal/remote needs to consume it. Keeping the wire shape here and the runtime Target type in internal/remote keeps the dep direction clean (remote → config, never the reverse).

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `host` | `string` | 2.3 + 2.4 | — | yes |  |
| `port` | `int` | 2.3 + 2.4 | — | no | default 22. |
| `user` | `string` | 2.3 + 2.4 | — | yes |  |
| `key_path` | `string` | 2.3 + 2.4 | — | no | file path (PEM). |
| `key_source` | `string` | 2.3 + 2.4 | — | no | "agent" \| "tf-output:<name>". |

## `TestCfg`

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `throughput` | `ThroughputCfg` | 2.3 + 2.4 | — | no |  |
| `connectivity` | `ConnectivityCfg` | 2.3 + 2.4 | — | no |  |
| `dns` | `DNSCfg` | 2.3 + 2.4 | — | no |  |

## `ThroughputCfg`

| key | type | line | default | required | description |
|---|---|---|---|---|---|
| `image` | `string` | 2.3 + 2.4 | `networkstatic/iperf3:latest` | no | default: networkstatic/iperf3:latest. |
| `duration` | `int` | 2.3 + 2.4 | `30` | no | seconds; default 30. |
| `streams` | `int` | 2.3 + 2.4 | `8` | no | parallel; default 8. |
| `default_mode` | `string` | 2.3 + 2.4 | `north-south` | no | north-south \| east-west. |

