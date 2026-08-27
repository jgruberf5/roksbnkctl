package config

// Sprint 30 (PRD 13 Issue 4) — `roksbnkctl init --override-from-env`. After a
// workspace config.yaml is assembled (from --config-file and/or the interview),
// overlay a FIXED set of fields from environment variables. This lets a single
// committed config.yaml template carry placeholders (e.g. api_key_b64: "") and
// have a CI pipeline inject the real values from the environment — no secret in
// version control. Env values WIN over whatever the seed/interview produced.
//
// This is a fixed field map, NOT arbitrary interpolation (a non-goal): each
// supported variable maps to exactly one config field.

import (
	"crypto/x509"
	"encoding/base64"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

// OverrideFromEnv overlays config.yaml fields from environment variables that
// are set + non-empty, mutating ws in place. It returns a list of human-
// readable field labels that were overridden (for logging) — NEVER the values,
// so a secret like the API key is never printed.
//
// Supported variables (env → field):
//
//	IBMCLOUD_API_KEY                 → ibmcloud.api_key_b64   (raw key, base64-encoded)
//	ROKSBNKCTL_API_KEY_B64          → ibmcloud.api_key_b64   (verbatim; pre-encoded)
//	ROKSBNKCTL_PREFIX               → prefix
//	ROKSBNKCTL_REGION               → ibmcloud.region
//	ROKSBNKCTL_RESOURCE_GROUP       → ibmcloud.resource_group
//	ROKSBNKCTL_CLUSTER_NAME         → cluster.name
//	ROKSBNKCTL_CLUSTER_CREATE       → cluster.create (bool: true/false/1/0)
//	ROKSBNKCTL_CNEINSTANCE_SIZE     → bnk.cneinstance_size (Tiny/Small/Medium/…;
//	ROKSBNKCTL_WORKER_FLAVOR        → cluster.worker_flavor (exact profile, e.g.
//	                                  cx3d.8x20; empty auto-selects from bx2 only)
//	                                  the legal set is a property of the manifest,
//	                                  so it is deliberately not validated here)
//	ROKSBNKCTL_GATEWAY_API_MTLS     → bnk.gateway_api_mtls (bool) — install the
//	                                  Gateway API bundle 2.4 needs for mTLS; off by
//	                                  default, ignored on 2.3
//	ROKSBNKCTL_GATEWAY_API_BUNDLE_URL → bnk.gateway_api_bundle_url — where that
//	                                  bundle is fetched from when no mirror is
//	                                  recorded; empty derives the upstream release
//	                                  for bnk.gateway_api_version. The sha256 pin
//	                                  still applies, so this changes only WHERE
//	                                  the bytes come from
//
// BNK 2.4 conformance with F5's reference CNEInstance. All 2.4-only; unset means
// the terraform default, which is F5's reference value.
//
//	ROKSBNKCTL_TMM_REPLICAS                    → bnk.tmm_replicas (int)
//	ROKSBNKCTL_WATCH_NAMESPACES                → bnk.watch_namespaces (comma-separated)
//	ROKSBNKCTL_TMM_ANTI_AFFINITY               → bnk.tmm_anti_affinity (bool)
//	ROKSBNKCTL_TMM_ANTI_AFFINITY_TOPOLOGY_KEY  → bnk.tmm_anti_affinity_topology_key
//	ROKSBNKCTL_TMM_ZONE_SPREAD                 → bnk.tmm_zone_spread (bool)
//	ROKSBNKCTL_TMM_ZONE_TOPOLOGY_KEY           → bnk.tmm_zone_topology_key
//	ROKSBNKCTL_TMM_ZONE_MAX_SKEW               → bnk.tmm_zone_max_skew (int)
//	ROKSBNKCTL_TMM_ZONE_WHEN_UNSATISFIABLE     → bnk.tmm_zone_when_unsatisfiable
//	ROKSBNKCTL_TMM_POD_LABEL                   → bnk.tmm_pod_label
//	ROKSBNKCTL_TMM_ROLLING_UPDATE              → bnk.tmm_rolling_update (bool)
//	ROKSBNKCTL_EXTERNAL_BIGIP                  → bnk.external_bigip (bool)
//	ROKSBNKCTL_EXTERNAL_BIGIP_LOGIN_SECRET     → bnk.external_bigip_login_secret
//	ROKSBNKCTL_CLUSTER_IDENTIFIER              → bnk.cluster_identifier
//	ROKSBNKCTL_GATEWAY_API_VERSION             → bnk.gateway_api_version
//	ROKSBNKCTL_DEMO_MODE                       → bnk.demo_mode (bool)
//	ROKSBNKCTL_WHOLE_CLUSTER                   → bnk.whole_cluster (bool)
//	ROKSBNKCTL_HUGEPAGES                       → bnk.hugepages.enabled (bool) —
//	                                             allocates hugepages on the worker
//	                                             pool. REBOOTS WORKERS.
//	ROKSBNKCTL_HUGEPAGES_SIZE                  → bnk.hugepages.size (2M / 1G)
//	ROKSBNKCTL_HUGEPAGES_COUNT                 → bnk.hugepages.count (per node)
//	ROKSBNKCTL_OPENSHIFT_VERSION    → cluster.openshift_version
//	ROKSBNKCTL_WORKERS_PER_ZONE     → cluster.workers_per_zone (int)
//	ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY → cluster.public_gateway (bool; false = no worker egress)
//	ROKSBNKCTL_CLUSTER_VPC_CIDR     → cluster.vpc_cidr (per-zone prefixes; avoids TGW overlap)
//	ROKSBNKCTL_CLUSTER_NETWORK_MODE → cluster.network_mode (single-nic default, or multi-nic)
//	ROKSBNKCTL_TRUSTED_PROFILE_SA   → bnk.trusted_profile.service_account
//	ROKSBNKCTL_TRUSTED_PROFILE_ROLES → bnk.trusted_profile.roles (comma-separated)
//	ROKSBNKCTL_VLAN_PREFIXLEN       → bnk.network.vlan_prefixlen (TMM self-IP mask; independent of the zone CIDRs)
//	ROKSBNKCTL_VLAN_PREFIXLEN_EXTERNAL → bnk.network.vlan_prefixlen_external (overrides the shared value for one VLAN)
//	ROKSBNKCTL_VLAN_PREFIXLEN_INTERNAL → bnk.network.vlan_prefixlen_internal
//	ROKSBNKCTL_CLUSTER_VPC_ID       → resources.cluster_vpc (create:false + existing=<vpc-id>)
//	ROKSBNKCTL_EXISTING_SUBNET_IDS  → cluster.existing_subnet_ids (comma-separated, zone order)
//	ROKSBNKCTL_TGW_JUMPHOST_CREATE  → resources.tgw_jumphost.create (bool)
//	ROKSBNKCTL_CLIENT_VPC_CREATE    → resources.client_vpc.create (bool)
//	ROKSBNKCTL_CLIENT_VPC_NAME      → resources.client_vpc.existing (adopt a client VPC)
//	ROKSBNKCTL_TESTING_SSH_KEY_NAME → resources.testing_ssh_key_name
//	ROKSBNKCTL_TESTING_VPC_NAME     → resources.testing_client_vpc_name (name the created testing client VPC)
//	ROKSBNKCTL_TRANSIT_GATEWAY_NAME → resources.transit_gateway.existing (create:false — adopt a shared TGW by name or id)
//	ROKSBNKCTL_STORAGE_CLASS_NAME   → bnk.storage_class_name — the CNEInstance
//	                                  StorageClass; needs ReadWriteMany for TMM
//	ROKSBNKCTL_CERT_MANAGER_CREATE  → resources.cert_manager.create (bool) — set
//	                                  false to ADOPT a cert-manager the cluster
//	                                  already runs, which an adopted cluster very
//	                                  often does
//	ROKSBNKCTL_REGISTRY_COS_CREATE  → resources.registry_cos.create (bool)
//	ROKSBNKCTL_CLUSTER_JUMPHOSTS_CREATE → resources.cluster_jumphosts.create (bool)
//	ROKSBNKCTL_BNKFORGE_CA_B64      → bnkforge.ca_b64 (PEM CA pinning the Forge server)
//	ROKSBNKCTL_BNKFORGE_PASSWORD    → bnkforge.password_b64 (raw, base64-encoded)
//	ROKSBNKCTL_REGISTRY_TARGET      → registry.target (icr|generic)
//	ROKSBNKCTL_GENERIC_HOST         → registry.generic_host
//	ROKSBNKCTL_GENERIC_REPO_PREFIX  → registry.generic_repo_prefix
//	ROKSBNKCTL_GENERIC_USERNAME     → registry.generic_username
//	ROKSBNKCTL_GENERIC_PASSWORD     → registry.generic_password_b64 (raw, base64-encoded)
//	ROKSBNKCTL_GENERIC_CA_B64       → registry.generic_ca_b64 (verbatim; already base64)
//	ROKSBNKCTL_GENERIC_CA_SHA256    → registry.generic_ca_sha256 (the out-of-band CA pin)
//	ROKSBNKCTL_BIGIP_URL            → bnk.cis.bigip_url (the BNK CIS controller's BIG-IP target)
//	ROKSBNKCTL_BIGIP_USERNAME       → bnk.cis.bigip_username
//	ROKSBNKCTL_BIGIP_PASSWORD       → bnk.cis.bigip_password_b64 (raw, base64-encoded)
//	ROKSBNKCTL_REACHABILITY_RETRY_SECONDS → bnk.preflight.reachability_retry_seconds (0 = one-shot)
//	ROKSBNKCTL_REACHABILITY_TIMEOUT_SECONDS → bnk.preflight.reachability_timeout_seconds
//	ROKSBNKCTL_LICENSE_MODE         → bnk.license_mode (connected|disconnected|f5licenseproxy)
//	ROKSBNKCTL_FLO_NAMESPACE        → bnk.flo_namespace (set both to one value for a
//	ROKSBNKCTL_FLO_UTILS_NAMESPACE  → bnk.flo_utils_namespace   single shared namespace)
//	ROKSBNKCTL_GATEWAY_CLASS_NAME   → gateway.class_name (GatewayClass is cluster-scoped)
//	ROKSBNKCTL_GATEWAY_CONTROLLER_NAME → gateway.controller_name (empty derives it from the FLO namespace)
//	ROKSBNKCTL_GATEWAY_ROUTE_EXAMPLES → gateway.route_examples (comma-separated: GRPCRoute,L4Route)
//	ROKSBNKCTL_GATEWAY_L4_LISTENER_PORT → gateway.l4_listener_port
//	ROKSBNKCTL_TESTING_JUMPHOST_ALLOWED_CIDRS       → resources.testing_jumphost_allowed_cidrs (comma-separated)
//	ROKSBNKCTL_TESTING_CLIENT_VPC_INBOUND_CIDRS     → resources.testing_client_vpc_inbound_cidrs (comma-separated)
//	ROKSBNKCTL_CLUSTER_HTTP_ALLOWED_CIDRS           → resources.cluster_http_allowed_cidrs (comma-separated)
//	ROKSBNKCTL_CLUSTER_VPC_DEFAULT_SG_INBOUND_CIDRS → resources.cluster_vpc_default_sg_inbound_cidrs (comma-separated)
//	ROKSBNKCTL_FLP_NAMESPACE        → bnk.flp.namespace
//	ROKSBNKCTL_FLP_EXTERNAL_URL     → bnk.flp.external.url        (license via a proxy in ANOTHER cluster)
//	ROKSBNKCTL_FLP_ROOT_CA_B64      → bnk.flp.external.root_ca_b64 (verbatim; already base64)
//
//	── #234: every remaining settable field now has one ──
//	ROKSBNKCTL_API_KEY_SOURCE                    → ibmcloud.api_key_source
//	ROKSBNKCTL_BNKFORGE_INSECURE                 → bnkforge.insecure
//	ROKSBNKCTL_BNKFORGE_PROJECT                  → bnkforge.project
//	ROKSBNKCTL_BNKFORGE_REGISTER                 → bnkforge.register
//	ROKSBNKCTL_BNKFORGE_URL                      → bnkforge.url
//	ROKSBNKCTL_BNKFORGE_USERNAME                 → bnkforge.username
//	ROKSBNKCTL_BNK_CREATE                        → resources.bnk.create
//	ROKSBNKCTL_BNK_EXISTING                      → resources.bnk.existing
//	ROKSBNKCTL_CERT_MANAGER_EXISTING             → resources.cert_manager.existing
//	ROKSBNKCTL_CERT_MANAGER_NAMESPACE            → bnk.cert_manager.namespace
//	ROKSBNKCTL_CERT_MANAGER_VERSION              → bnk.cert_manager.version
//	ROKSBNKCTL_CLIENT_REGION                     → resources.client_region
//	ROKSBNKCTL_CLUSTER_JUMPHOSTS_EXISTING        → resources.cluster_jumphosts.existing
//	ROKSBNKCTL_CLUSTER_VPC_CREATE                → resources.cluster_vpc.create
//	ROKSBNKCTL_COPIED_SSH_KEY_FILES              → resources.copied_ssh_key_files
//	ROKSBNKCTL_FAR_REPO_URL                      → bnk.far_repo_url
//	ROKSBNKCTL_FLP_CHART_VERSION                 → bnk.flp.chart_version
//	ROKSBNKCTL_FLP_NODE_PORT_ACCESS              → bnk.flp.node_port_access
//	ROKSBNKCTL_FLP_NODE_PORT_SOURCE_CIDRS        → bnk.flp.node_port_source_cidrs
//	ROKSBNKCTL_FLP_STORAGE_CLASS                 → bnk.flp.storage_class
//	ROKSBNKCTL_FLP_VSI_ALLOWED_CIDRS             → bnk.flp.vsi.allowed_cidrs
//	ROKSBNKCTL_FLP_VSI_FORWARD_PROXY_HOST        → bnk.flp.vsi.forward_proxy.host
//	ROKSBNKCTL_FLP_VSI_FORWARD_PROXY_PORT        → bnk.flp.vsi.forward_proxy.port
//	ROKSBNKCTL_FLP_VSI_FORWARD_PROXY_PROTOCOL    → bnk.flp.vsi.forward_proxy.protocol
//	ROKSBNKCTL_GATEWAY_APP_NAMESPACE             → gateway.app_namespace
//	ROKSBNKCTL_GATEWAY_BACKEND_PORT              → gateway.backend_port
//	ROKSBNKCTL_GATEWAY_BACKEND_SERVICE           → gateway.backend_service
//	ROKSBNKCTL_GATEWAY_CLIENT_SUBNET_LOCAL       → gateway.client_subnet_local
//	ROKSBNKCTL_GATEWAY_CLIENT_SUBNET_REMOTE      → gateway.client_subnet_remote
//	ROKSBNKCTL_GATEWAY_EGRESS_MODE               → gateway.egress_mode
//	ROKSBNKCTL_GATEWAY_VXLAN_PORT                → gateway.vxlan_port
//	ROKSBNKCTL_GSLB_DATACENTER_NAME              → bnk.gslb_datacenter_name
//	ROKSBNKCTL_HUGEPAGES_NODE_ROLE               → bnk.hugepages.node_role
//	ROKSBNKCTL_HUGEPAGES_PROFILE_NAME            → bnk.hugepages.profile_name
//	ROKSBNKCTL_ICR_HOST                          → registry.icr_host
//	ROKSBNKCTL_ICR_NAMESPACE                     → registry.icr_namespace
//	ROKSBNKCTL_MIN_WORKER_MEMORY_GB              → cluster.min_worker_memory_gb
//	ROKSBNKCTL_MIN_WORKER_VCPU_COUNT             → cluster.min_worker_vcpu_count
//	ROKSBNKCTL_REGISTRY_COS_EXISTING             → resources.registry_cos.existing
//	ROKSBNKCTL_REGISTRY_INCLUDE_DEPS             → registry.include_deps
//	ROKSBNKCTL_REGISTRY_NAMESPACE                → registry.namespace
//	ROKSBNKCTL_SOURCE_SERVICE_ACCOUNT_B64        → registry.source_service_account_b64
//	ROKSBNKCTL_TCP_SETTINGS_NAME                 → bnk.tcp_settings_name
//	ROKSBNKCTL_TESTING_JUMPHOST_PROFILE          → resources.testing_jumphost_profile
//	ROKSBNKCTL_TESTING_MIN_MEMORY_GB             → resources.testing_min_memory_gb
//	ROKSBNKCTL_TESTING_MIN_VCPU_COUNT            → resources.testing_min_vcpu_count
//	ROKSBNKCTL_TGW_JUMPHOST_EXISTING             → resources.tgw_jumphost.existing
//	ROKSBNKCTL_TMM_K8S_ROUTES                    → bnk.network.tmm_k8s_routes
//	ROKSBNKCTL_TRANSIT_GATEWAY_CREATE            → resources.transit_gateway.create
//
// The last two are the cross-job handoff: the CI job that owns the proxy emits
// them with `flp output flp_external_endpoint` / `flp_root_ca`, and the job that
// installs BNK consumes them as ordinary pipeline variables — no config file has
// to be templated to carry two values between jobs.
//
// Two further maps live in envoverride_flp.go, with their own variable → field
// tables: ROKSBNKCTL_FLP_MODE / ROKSBNKCTL_FLP_VSI_* (the standalone F5 License
// Proxy VSI appliance) and ROKSBNKCTL_{MANIFEST_VERSION,FAR_AUTH_*,
// SUBSCRIPTION_JWT_*,COS_*} (the FAR supply chain — local files or a COS bucket).
//
// ROKSBNKCTL_API_KEY_B64 takes precedence over IBMCLOUD_API_KEY when both are
// set (an explicit pre-encoded value beats the raw-key convenience path).
func OverrideFromEnv(ws *Workspace) []string {
	var applied []string

	// API key — pre-encoded escape hatch first, else the raw-key convenience.
	if v := envValue("ROKSBNKCTL_API_KEY_B64"); v != "" {
		ws.IBMCloud.APIKeyB64 = v
		applied = append(applied, "ibmcloud.api_key_b64 (ROKSBNKCTL_API_KEY_B64)")
	} else if v := envValue("IBMCLOUD_API_KEY"); v != "" {
		ws.IBMCloud.APIKeyB64 = base64.StdEncoding.EncodeToString([]byte(v))
		applied = append(applied, "ibmcloud.api_key_b64 (IBMCLOUD_API_KEY)")
	}

	// The uniform overrides: read one env var, assign one field. Driven from a
	// table so each variable's NAME is written once — the read, the assignment
	// and the "what was applied" report all derive from the same row, and the
	// three can no longer disagree.
	for _, o := range stringOverrides {
		if v := envValue(o.env); v != "" {
			o.set(ws, v)
			applied = append(applied, o.field+" ("+o.env+")")
		}
	}

	// Cluster identity — the fields a non-interactive (`--non-interactive`)
	// init needs that the seed/interview would otherwise supply. Lets an
	// argv+env runner (e.g. a CI / BNK Forge container step) seed config.yaml
	// from env alone.
	if v := envValue("ROKSBNKCTL_CLUSTER_CREATE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			ws.Cluster.Create = b
			applied = append(applied, "cluster.create (ROKSBNKCTL_CLUSTER_CREATE)")
		}
	}
	// Opt into the Gateway API bundle BNK 2.4 needs for mTLS (#170). Off by
	// default: installing it means deleting a platform admission policy, which is
	// not something to do on a cluster that does not need the newer bundle.
	if v := envValue("ROKSBNKCTL_GATEWAY_API_MTLS"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			ws.BNK.GatewayAPIMTLS = b
			applied = append(applied, "bnk.gateway_api_mtls (ROKSBNKCTL_GATEWAY_API_MTLS)")
		}
	}
	// ── BNK 2.4 conformance with F5's reference CNEInstance ──────────────────
	//
	// Each is unset-by-default: nothing renders unless the operator asks, and the
	// terraform default (F5's reference value) applies otherwise.
	for _, o := range []struct {
		env string
		set func(string)
	}{
		{"ROKSBNKCTL_TMM_ANTI_AFFINITY_TOPOLOGY_KEY", func(v string) { ws.BNK.TMMAntiAffinityTopologyKey = v }},
		{"ROKSBNKCTL_TMM_ZONE_TOPOLOGY_KEY", func(v string) { ws.BNK.TMMZoneTopologyKey = v }},
		{"ROKSBNKCTL_TMM_ZONE_WHEN_UNSATISFIABLE", func(v string) { ws.BNK.TMMZoneWhenUnsatisfiable = v }},
		{"ROKSBNKCTL_TMM_POD_LABEL", func(v string) { ws.BNK.TMMPodLabel = v }},
		{"ROKSBNKCTL_HUGEPAGES_SIZE", func(v string) {
			if ws.BNK.Hugepages == nil {
				ws.BNK.Hugepages = &HugepagesCfg{}
			}
			ws.BNK.Hugepages.Size = v
		}},
		{"ROKSBNKCTL_EXTERNAL_BIGIP_LOGIN_SECRET", func(v string) { ws.BNK.ExternalBigIPLoginSecret = v }},
		{"ROKSBNKCTL_CLUSTER_IDENTIFIER", func(v string) { ws.BNK.ClusterIdentifier = v }},
		{"ROKSBNKCTL_GATEWAY_API_VERSION", func(v string) { ws.BNK.GatewayAPIVersion = v }},
	} {
		if v := envValue(o.env); v != "" {
			o.set(v)
			applied = append(applied, strings.ToLower(strings.TrimPrefix(o.env, "ROKSBNKCTL_"))+" ("+o.env+")")
		}
	}
	for _, o := range []struct {
		env string
		set func(int)
	}{
		{"ROKSBNKCTL_TMM_REPLICAS", func(n int) { ws.BNK.TMMReplicas = n }},
		{"ROKSBNKCTL_TMM_ZONE_MAX_SKEW", func(n int) { ws.BNK.TMMZoneMaxSkew = n }},
		{"ROKSBNKCTL_HUGEPAGES_COUNT", func(n int) {
			if ws.BNK.Hugepages == nil {
				ws.BNK.Hugepages = &HugepagesCfg{}
			}
			ws.BNK.Hugepages.Count = n
		}},
	} {
		if v := envValue(o.env); v != "" {
			if n, err := strconv.Atoi(v); err == nil {
				o.set(n)
				applied = append(applied, strings.ToLower(strings.TrimPrefix(o.env, "ROKSBNKCTL_"))+" ("+o.env+")")
			}
		}
	}
	// Pointer-valued, so "explicitly false" is distinguishable from "unset" —
	// which matters most for demo_mode, where unset means the LINE default and
	// that default is true on 2.3.
	for _, o := range []struct {
		env string
		set func(*bool)
	}{
		{"ROKSBNKCTL_TMM_ANTI_AFFINITY", func(b *bool) { ws.BNK.TMMAntiAffinity = b }},
		{"ROKSBNKCTL_TMM_ZONE_SPREAD", func(b *bool) { ws.BNK.TMMZoneSpread = b }},
		{"ROKSBNKCTL_TMM_ROLLING_UPDATE", func(b *bool) { ws.BNK.TMMRollingUpdate = b }},
		{"ROKSBNKCTL_EXTERNAL_BIGIP", func(b *bool) { ws.BNK.ExternalBigIP = b }},
		{"ROKSBNKCTL_DEMO_MODE", func(b *bool) { ws.BNK.DemoMode = b }},
		{"ROKSBNKCTL_WHOLE_CLUSTER", func(b *bool) { ws.BNK.WholeCluster = b }},
		{"ROKSBNKCTL_HUGEPAGES", func(b *bool) {
			if ws.BNK.Hugepages == nil {
				ws.BNK.Hugepages = &HugepagesCfg{}
			}
			ws.BNK.Hugepages.Enabled = *b
		}},
	} {
		if v := envValue(o.env); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				o.set(&b)
				applied = append(applied, strings.ToLower(strings.TrimPrefix(o.env, "ROKSBNKCTL_"))+" ("+o.env+")")
			}
		}
	}
	// Adopt-what-exists toggles. An adopted customer cluster very often already
	// runs cert-manager, and without a way to say so from the environment the CI
	// path — which takes its whole configuration from -e variables — cannot
	// install onto one at all.
	for _, o := range []struct {
		env, path string
		set       func(bool)
	}{
		{"ROKSBNKCTL_CERT_MANAGER_CREATE", "resources.cert_manager.create", func(b bool) { ws.Resources.CertManager.Create = b }},
		{"ROKSBNKCTL_REGISTRY_COS_CREATE", "resources.registry_cos.create", func(b bool) { ws.Resources.RegistryCOS.Create = b }},
		{"ROKSBNKCTL_CLUSTER_JUMPHOSTS_CREATE", "resources.cluster_jumphosts.create", func(b bool) { ws.Resources.ClusterJumphosts.Create = b }},
	} {
		if v := envValue(o.env); v != "" {
			if b, err := strconv.ParseBool(v); err == nil {
				// env-only init starts from an empty Workspace, so Resources
				// is nil here far more often than on the file-loaded path.
				if ws.Resources == nil {
					ws.Resources = &ResourcesCfg{}
				}
				o.set(b)
				applied = append(applied, o.path+" ("+o.env+")")
			}
		}
	}

	if v := envValue("ROKSBNKCTL_STORAGE_CLASS_NAME"); v != "" {
		ws.BNK.StorageClassName = v
		applied = append(applied, "bnk.storage_class_name (ROKSBNKCTL_STORAGE_CLASS_NAME)")
	}

	if v := envValue("ROKSBNKCTL_WATCH_NAMESPACES"); v != "" {
		parts := strings.Split(v, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		if len(out) > 0 {
			ws.BNK.WatchNamespaces = out
			applied = append(applied, "bnk.watch_namespaces (ROKSBNKCTL_WATCH_NAMESPACES)")
		}
	}

	if v := envValue("ROKSBNKCTL_WORKERS_PER_ZONE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			ws.Cluster.WorkersPerZone = n
			applied = append(applied, "cluster.workers_per_zone (ROKSBNKCTL_WORKERS_PER_ZONE)")
		}
	}

	// Worker Internet egress. This is the switch that makes a NEW cluster
	// disconnected, and it was the one topology field an argv+env runner could
	// not reach: nil renders nothing and terraform defaults to true, so every
	// env-built cluster came up with a public gateway. Kept a *bool so "unset"
	// (inherit the terraform default) stays distinct from an explicit false.
	if v := envValue("ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			ws.Cluster.PublicGateway = &b
			applied = append(applied, "cluster.public_gateway (ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY)")
		}
	}

	// The CNE controller's Trusted Profile. Present for the same reason the other
	// env overrides are: the CI/Forge runners build a whole deployment from the
	// environment and never write a config.yaml, so without these the profile is
	// not tunable on that path at all.
	if v := envValue("ROKSBNKCTL_TRUSTED_PROFILE_SA"); v != "" {
		if ws.BNK.TrustedProfile == nil {
			ws.BNK.TrustedProfile = &BNKTrustedProfileCfg{}
		}
		ws.BNK.TrustedProfile.ServiceAccount = v
		applied = append(applied, "bnk.trusted_profile.service_account (ROKSBNKCTL_TRUSTED_PROFILE_SA)")
	}
	if v := envValue("ROKSBNKCTL_TRUSTED_PROFILE_ROLES"); v != "" {
		roles := []string{}
		for _, r := range strings.Split(v, ",") {
			if r = strings.TrimSpace(r); r != "" {
				roles = append(roles, r)
			}
		}
		if len(roles) > 0 {
			if ws.BNK.TrustedProfile == nil {
				ws.BNK.TrustedProfile = &BNKTrustedProfileCfg{}
			}
			ws.BNK.TrustedProfile.Roles = roles
			applied = append(applied, "bnk.trusted_profile.roles (ROKSBNKCTL_TRUSTED_PROFILE_ROLES)")
		}
	}

	// ── bring-your-own network (#60, #61) — issue #64 ────────────────────────
	// These shipped in v1.43.0 with a config surface and no env override, which
	// made them unreachable from BNK Forge: every module runs
	// `init --override-from-env --non-interactive` and there is no config.yaml to
	// edit. A field reachable only through YAML cannot be used by a blueprint.
	//
	// The FLP VSI half lives in envoverride_flp.go, which already owns every
	// other ROKSBNKCTL_FLP_VSI_* variable.
	if v := envValue("ROKSBNKCTL_EXISTING_SUBNET_IDS"); v != "" {
		ids := []string{}
		for _, id := range strings.Split(v, ",") {
			if id = strings.TrimSpace(id); id != "" {
				ids = append(ids, id)
			}
		}
		if len(ids) > 0 {
			ws.Cluster.ExistingSubnetIDs = ids
			applied = append(applied, "cluster.existing_subnet_ids (ROKSBNKCTL_EXISTING_SUBNET_IDS)")
		}
	}

	// Adopt an existing Transit Gateway by name OR id (create=false + existing).
	// Lets a cluster attach to a shared TGW; `cluster up`/`register` then connects
	// it. Preserves the other resource toggles.
	if v := envValue("ROKSBNKCTL_TRANSIT_GATEWAY_NAME"); v != "" {
		if ws.Resources == nil {
			ws.Resources = &ResourcesCfg{}
		}
		ws.Resources.TransitGateway = ResourceToggle{Create: false, Existing: v}
		applied = append(applied, "resources.transit_gateway.existing (ROKSBNKCTL_TRANSIT_GATEWAY_NAME)")
	}
	// Adopt an existing cluster VPC by ID (create=false + existing). Brings your
	// own VPC for a NEW cluster instead of provisioning one.
	if v := envValue("ROKSBNKCTL_CLUSTER_VPC_ID"); v != "" {
		if ws.Resources == nil {
			ws.Resources = &ResourcesCfg{}
		}
		ws.Resources.ClusterVPC = ResourceToggle{Create: false, Existing: v}
		applied = append(applied, "resources.cluster_vpc.existing (ROKSBNKCTL_CLUSTER_VPC_ID)")
	}
	// The optional testing client (jumphost VSI + client VPC). Both default OFF,
	// matching the `init` interview, so these exist to opt IN from a runner that
	// has no prompts. The client VPC consumes a Transit Gateway connection, so
	// creating one unasked is not a free mistake.
	if v := envValue("ROKSBNKCTL_TGW_JUMPHOST_CREATE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			if ws.Resources == nil {
				ws.Resources = &ResourcesCfg{}
			}
			ws.Resources.TGWJumphost.Create = b
			applied = append(applied, "resources.tgw_jumphost.create (ROKSBNKCTL_TGW_JUMPHOST_CREATE)")
		}
	}
	if v := envValue("ROKSBNKCTL_CLIENT_VPC_CREATE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			if ws.Resources == nil {
				ws.Resources = &ResourcesCfg{}
			}
			ws.Resources.ClientVPC.Create = b
			applied = append(applied, "resources.client_vpc.create (ROKSBNKCTL_CLIENT_VPC_CREATE)")
		}
	}
	// The jumphost lives IN a client VPC — terraform resolves its VPC as
	// "created one, else the named existing one" (modules/testing/data.tf:69).
	// The interview offers both branches ("Create a new client VPC for it?" →
	// no → "Existing client VPC name"); without this the env surface could only
	// express the create branch, so opting the jumphost in without also creating
	// a VPC produced a config terraform cannot plan.
	if v := envValue("ROKSBNKCTL_CLIENT_VPC_NAME"); v != "" {
		if ws.Resources == nil {
			ws.Resources = &ResourcesCfg{}
		}
		ws.Resources.ClientVPC.Existing = v
		applied = append(applied, "resources.client_vpc.existing (ROKSBNKCTL_CLIENT_VPC_NAME)")
	}

	// Name the testing client VPC to create (rendered as testing_client_vpc_name).
	if v := envValue("ROKSBNKCTL_TESTING_VPC_NAME"); v != "" {
		if ws.Resources == nil {
			ws.Resources = &ResourcesCfg{}
		}
		ws.Resources.TestingClientVPCName = v
		applied = append(applied, "resources.testing_client_vpc_name (ROKSBNKCTL_TESTING_VPC_NAME)")
	}

	// BNK CIS controller's BIG-IP target (optional; blank → BNK without CIS).
	applied = append(applied, overrideCISFromEnv(ws)...)

	// Per-zone TMM network mapping (listener/VIP + SNAT CIDRs, VLAN CIDRs,
	// self-IPs). Fixed indexed env vars ROKSBNKCTL_ZONE<n>_* for up to maxZones;
	// a zone is emitted only when all six of its fields are set.
	applied = append(applied, overrideNetworkZonesFromEnv(ws)...)
	// Per-component env passthrough for the 2.4 CNEInstance (#175).
	applied = append(applied, OverrideAdvancedEnvFromEnv(ws)...)
	applied = append(applied, OverrideTCPSettingsFromEnv(ws)...)
	applied = append(applied, overrideVLANPrefixLenFromEnv(ws)...)
	applied = append(applied, overrideVLANPrefixLenPerVLANFromEnv(ws)...)
	if v := envValue("ROKSBNKCTL_TESTING_SSH_KEY_NAME"); v != "" {
		if ws.Resources == nil {
			ws.Resources = &ResourcesCfg{}
		}
		ws.Resources.TestingSSHKeyName = v
		applied = append(applied, "resources.testing_ssh_key_name (ROKSBNKCTL_TESTING_SSH_KEY_NAME)")
	}

	// The BNK Forge server's CA (PEM, base64) — how an env-only runner pins a
	// self-signed Forge, mirroring `bnkforge enable --forge-ca`. Validated here
	// for the same reason the flag path x509-parses before encoding: a bad value
	// must be rejected at seed time, where the operator is watching — not inside
	// the best-effort registration hook after `cluster up`. GNU base64 wraps at
	// 76 columns, so the natural $(base64 ca.pem) arrives line-wrapped; the
	// value is stored with all whitespace stripped.
	if v := envValue("ROKSBNKCTL_BNKFORGE_CA_B64"); v != "" {
		compact := strings.Join(strings.Fields(v), "")
		pem, err := DecodeB64Field("ROKSBNKCTL_BNKFORGE_CA_B64", compact)
		switch {
		case err != nil:
			fmt.Fprintf(os.Stderr, "! ROKSBNKCTL_BNKFORGE_CA_B64 ignored: %v\n", err)
		case !x509.NewCertPool().AppendCertsFromPEM(pem):
			fmt.Fprintln(os.Stderr, "! ROKSBNKCTL_BNKFORGE_CA_B64 ignored: no PEM certificate found in the decoded value")
		default:
			bnkForgeCfg(ws).CAB64 = compact
			applied = append(applied, "bnkforge.ca_b64 (ROKSBNKCTL_BNKFORGE_CA_B64)")
		}
	}

	// The BNK Forge password — raw in the env, base64 into the config, the same
	// shape as every other machine credential here (CIS, GTM, registry, API key).
	//
	// This is the SEEDING path, distinct from BNK_FORGE_PASSWORD: that one is
	// read at register time and never written down, this one populates
	// bnkforge.password_b64 so an unattended runner can be configured once. The
	// env still wins at use time, so setting both is not ambiguous.
	if v := envValue("ROKSBNKCTL_BNKFORGE_PASSWORD"); v != "" {
		bnkForgeCfg(ws).PasswordB64 = base64.StdEncoding.EncodeToString([]byte(v))
		applied = append(applied, "bnkforge.password_b64 (ROKSBNKCTL_BNKFORGE_PASSWORD)")
	}

	// Generic OCI registry password (e.g. an Artifactory access token) — raw in
	// the env, base64-encoded into the config like the API key. The rest of the
	// registry surface is uniform and lives in stringOverrides.
	if v := envValue("ROKSBNKCTL_GENERIC_PASSWORD"); v != "" {
		registryCfg(ws).GenericPasswordB64 = base64.StdEncoding.EncodeToString([]byte(v))
		applied = append(applied, "registry.generic_password_b64 (ROKSBNKCTL_GENERIC_PASSWORD)")
	}
	// License mode (optional; connected|disconnected|f5licenseproxy). f5licenseproxy
	// also seeds an flp block so the FLP phase deploys into a namespace, overridable
	// by ROKSBNKCTL_FLP_NAMESPACE. Empty → the JWT/connected default is unchanged.
	if v := envValue("ROKSBNKCTL_LICENSE_MODE"); v != "" {
		ws.BNK.LicenseMode = v
		applied = append(applied, "bnk.license_mode (ROKSBNKCTL_LICENSE_MODE)")
		if v == "f5licenseproxy" && ws.BNK.FLP == nil {
			ws.BNK.FLP = &BNKFLPCfg{}
		}
	}
	if v := envValue("ROKSBNKCTL_FLP_NAMESPACE"); v != "" {
		if ws.BNK.FLP == nil {
			ws.BNK.FLP = &BNKFLPCfg{}
		}
		ws.BNK.FLP.Namespace = v
		applied = append(applied, "bnk.flp.namespace (ROKSBNKCTL_FLP_NAMESPACE)")
	}

	// Comma-separated, like the other list-valued overrides (TRUSTED_PROFILE_ROLES,
	// the *_CIDRS set below), so a caller does not have to learn a second
	// convention. Validation is terraform's: which kinds are valid depends on
	// the Gateway API channel the BNK line installs, and that is knowledge the
	// terraform already holds.
	if v := envValue("ROKSBNKCTL_GATEWAY_ROUTE_EXAMPLES"); v != "" {
		if kinds := splitCommaList(v); len(kinds) > 0 {
			ws.Gateway.RouteExamples = kinds
			applied = append(applied, "gateway.route_examples (ROKSBNKCTL_GATEWAY_ROUTE_EXAMPLES)")
		}
	}
	if v := envValue("ROKSBNKCTL_GATEWAY_L4_LISTENER_PORT"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n > 0 {
			ws.Gateway.L4ListenerPort = n
			applied = append(applied, "gateway.l4_listener_port (ROKSBNKCTL_GATEWAY_L4_LISTENER_PORT)")
		}
	}

	// #234 tables. Each ignores an unparseable value rather than storing a zero:
	// a toggle silently set to the opposite of what a pipeline meant, or a port
	// set to 0, is harder to diagnose than one that was never set at all.
	for _, o := range boolOverrides {
		v := envValue(o.env)
		if v == "" {
			continue
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "! %s ignored: %q is not a boolean (true/false/1/0)\n", o.env, v)
			continue
		}
		o.set(ws, b)
		applied = append(applied, o.field+" ("+o.env+")")
	}
	for _, o := range ptrBoolOverrides {
		v := envValue(o.env)
		if v == "" {
			continue
		}
		b, err := strconv.ParseBool(v)
		if err != nil {
			fmt.Fprintf(os.Stderr, "! %s ignored: %q is not a boolean (true/false/1/0)\n", o.env, v)
			continue
		}
		o.set(ws, &b)
		applied = append(applied, o.field+" ("+o.env+")")
	}
	for _, o := range intOverrides {
		v := envValue(o.env)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil || n < 0 {
			fmt.Fprintf(os.Stderr, "! %s ignored: %q is not a non-negative integer\n", o.env, v)
			continue
		}
		o.set(ws, n)
		applied = append(applied, o.field+" ("+o.env+")")
	}
	for _, o := range stringListOverrides {
		v := envValue(o.env)
		if v == "" {
			continue
		}
		list := splitCommaList(v)
		if len(list) == 0 {
			continue
		}
		o.set(ws, list)
		applied = append(applied, o.field+" ("+o.env+")")
	}

	// Security-group source CIDRs (sgCIDROverrides). Comma-separated like the
	// overrides above.
	// Each leaves the terraform module's own default standing when unset — the
	// defaults differ per plane and that knowledge lives there. Values are
	// validated by the terraform variables at plan time (can(cidrhost(...))),
	// before anything is provisioned.
	for _, o := range sgCIDROverrides {
		v := envValue(o.env)
		if v == "" {
			continue
		}
		cidrs := splitCommaList(v)
		if len(cidrs) == 0 {
			continue
		}
		if ws.Resources == nil {
			ws.Resources = DefaultResources()
		}
		o.set(ws, cidrs)
		applied = append(applied, o.field+" ("+o.env+")")
	}

	// The reachability gate's tunables (issue #57). They belong on the env surface for
	// the same reason as everything else here: a CI runner building a workspace from
	// argv alone has no config.yaml to edit, and these are exactly the values a
	// pipeline needs to raise when its fabric programs routes slowly.
	//
	// 0 is MEANINGFUL for the retry budget — it means one-shot — so the parse must
	// distinguish "set to 0" from "absent", which is why the fields are pointers.
	if v := envValue("ROKSBNKCTL_REACHABILITY_RETRY_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			preflightCfg(ws).ReachabilityRetrySeconds = &n
			applied = append(applied, "bnk.preflight.reachability_retry_seconds (ROKSBNKCTL_REACHABILITY_RETRY_SECONDS)")
		}
	}
	if v := envValue("ROKSBNKCTL_REACHABILITY_TIMEOUT_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			preflightCfg(ws).ReachabilityTimeoutSeconds = &n
			applied = append(applied, "bnk.preflight.reachability_timeout_seconds (ROKSBNKCTL_REACHABILITY_TIMEOUT_SECONDS)")
		}
	}

	// The FLP deployment backend (helm vs. a standalone VSI appliance) and the
	// supply chain the phase reads its entitlement material from. Both maps live
	// in envoverride_flp.go — see there for the variable → field tables.
	applied = append(applied, overrideFLPFromEnv(ws)...)
	applied = append(applied, overrideSupplyChainFromEnv(ws)...)

	return applied
}

// registryCfg returns ws.Registry, creating it when the config never had a
// registry block — the normal CI case, where the whole target comes from env.
func registryCfg(ws *Workspace) *RegistryCfg {
	if ws.Registry == nil {
		ws.Registry = &RegistryCfg{}
	}
	return ws.Registry
}

// preflightCfg returns ws.BNK.Preflight, creating it when the config never had a
// preflight block — the normal case, since both fields have working defaults and
// only an environment that needs different ones ever names them.
func preflightCfg(ws *Workspace) *BNKPreflightCfg {
	if ws.BNK.Preflight == nil {
		ws.BNK.Preflight = &BNKPreflightCfg{}
	}
	return ws.BNK.Preflight
}

// bnkForgeCfg lazily creates ws.BNKForge so the overrides above can populate it.
func bnkForgeCfg(ws *Workspace) *BNKForgeCfg {
	if ws.BNKForge == nil {
		ws.BNKForge = &BNKForgeCfg{}
	}
	return ws.BNKForge
}

// flpExternal returns ws.BNK.FLP.External, creating the intermediate blocks. Both
// are pointers, so a config that never mentioned the FLP would otherwise nil-panic
// the moment CI sets only the handoff vars.
func flpExternal(ws *Workspace) *BNKFLPExternalCfg {
	if ws.BNK.FLP == nil {
		ws.BNK.FLP = &BNKFLPCfg{}
	}
	if ws.BNK.FLP.External == nil {
		ws.BNK.FLP.External = &BNKFLPExternalCfg{}
	}
	return ws.BNK.FLP.External
}

// overrideCISFromEnv overlays the BNK CIS BIG-IP target from
// ROKSBNKCTL_BIGIP_URL / _USERNAME / _PASSWORD (the password is base64-encoded
// into bigip_password_b64, like the API key).
func overrideCISFromEnv(ws *Workspace) []string {
	url := envValue("ROKSBNKCTL_BIGIP_URL")
	user := envValue("ROKSBNKCTL_BIGIP_USERNAME")
	pass := envValue("ROKSBNKCTL_BIGIP_PASSWORD")
	if url == "" && user == "" && pass == "" {
		return nil
	}
	if ws.BNK.CIS == nil {
		ws.BNK.CIS = &BNKCISCfg{}
	}
	var applied []string
	if url != "" {
		ws.BNK.CIS.BigIPURL = url
		applied = append(applied, "bnk.cis.bigip_url (ROKSBNKCTL_BIGIP_URL)")
	}
	if user != "" {
		ws.BNK.CIS.BigIPUsername = user
		applied = append(applied, "bnk.cis.bigip_username (ROKSBNKCTL_BIGIP_USERNAME)")
	}
	if pass != "" {
		ws.BNK.CIS.BigIPPasswordB64 = base64.StdEncoding.EncodeToString([]byte(pass))
		applied = append(applied, "bnk.cis.bigip_password_b64 (ROKSBNKCTL_BIGIP_PASSWORD)")
	}
	return applied
}

// maxNetworkZones bounds the indexed per-zone env overrides (ROKSBNKCTL_ZONE1_*
// … ROKSBNKCTL_ZONE3_*). IBM multi-zone regions have up to 3 zones.
const maxNetworkZones = 3

// zoneOverridePrefix is the literal half of the computed per-zone variable
// names (ROKSBNKCTL_ZONE1_EXT_VLAN_CIDR, …). Held apart from the loop so
// zoneOverrideNames below enumerates exactly what overrideNetworkZonesFromEnv
// reads — the two derive from this one declaration and zoneFields.
const zoneOverridePrefix = "ROKSBNKCTL_ZONE"

// zoneFields maps each per-zone variable suffix to the BNKZoneCfg field it
// fills. The reader and the surface both range over this — a suffix added here
// is read AND reported without a second list to remember.
var zoneFields = []struct {
	suffix string
	set    func(*BNKZoneCfg, string)
}{
	{"EXT_VLAN_CIDR", func(z *BNKZoneCfg, v string) { z.ExtVLANCIDR = v }},
	{"INT_VLAN_CIDR", func(z *BNKZoneCfg, v string) { z.IntVLANCIDR = v }},
	{"INT_SNAT_CIDR", func(z *BNKZoneCfg, v string) { z.IntSNATCIDR = v }},
	{"INT_VIP_CIDR", func(z *BNKZoneCfg, v string) { z.IntVIPCIDR = v }},
	{"EXTERNAL_SELFIP", func(z *BNKZoneCfg, v string) { z.ExternalSelfIP = v }},
	{"INTERNAL_SELFIP", func(z *BNKZoneCfg, v string) { z.InternalSelfIP = v }},
}

// zoneOverrideNames enumerates the whole computed family
// (maxNetworkZones × zoneFields) for SupportedOverrideNames.
func zoneOverrideNames() []string {
	out := make([]string, 0, maxNetworkZones*len(zoneFields))
	for i := 1; i <= maxNetworkZones; i++ {
		for _, f := range zoneFields {
			out = append(out, zoneOverridePrefix+strconv.Itoa(i)+"_"+f.suffix)
		}
	}
	return out
}

// overrideNetworkZonesFromEnv assembles bnk.network.zones from the fixed indexed
// env vars. Each zone needs all six fields set (a partial zone is skipped so the
// rendered cneinstance_network_zones object is never half-populated). When any
// zone is supplied it REPLACES bnk.network.zones (env wins, as elsewhere).
func overrideNetworkZonesFromEnv(ws *Workspace) []string {
	var zones []BNKZoneCfg
	for i := 1; i <= maxNetworkZones; i++ {
		p := zoneOverridePrefix + strconv.Itoa(i) + "_"
		var z BNKZoneCfg
		complete := true
		for _, f := range zoneFields {
			v := envValue(p + f.suffix)
			if v == "" {
				complete = false
				break
			}
			f.set(&z, v)
		}
		if !complete {
			continue // skip a partially-specified zone
		}
		zones = append(zones, z)
	}
	if len(zones) == 0 {
		return nil
	}
	if ws.BNK.Network == nil {
		ws.BNK.Network = &BNKNetworkCfg{}
	}
	ws.BNK.Network.Zones = zones
	return []string{"bnk.network.zones (ROKSBNKCTL_ZONE*_*)"}
}

// overrideVLANPrefixLenFromEnv overlays the TMM self-IP prefix length from
// ROKSBNKCTL_VLAN_PREFIXLEN.
//
// DELIBERATELY INDEPENDENT OF THE ZONE CIDRs, and not derived from them. It is
// tempting to treat a prefix length that disagrees with its subnet as a mistake
// — it usually is — but the disagreement is also a tool: a mask that makes TMM
// treat a smaller or larger block as directly connected, with static routes
// steering the remainder, is how a specific traffic pattern gets forced. Deriving
// this from the VPC subnet would remove that, so it stays an independent value
// with no cross-validation against the zones.
//
// Exists because without it the setting was unreachable from the environment
// entirely: the per-zone overrides above carry six fields and no mask, so every
// env-driven deployment — CI, and every BNK Forge blueprint — was pinned to the
// terraform default of 24 no matter what CIDRs it supplied. Separate from the
// zone loop because it is network-wide, not per-zone, and must be settable
// WITHOUT respecifying the zones.
func overrideVLANPrefixLenFromEnv(ws *Workspace) []string {
	v := envValue("ROKSBNKCTL_VLAN_PREFIXLEN")
	if v == "" {
		return nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 32 {
		// Ignore rather than fail: this runs during config assembly, where a
		// hard error would abort a deployment over one malformed variable. An
		// out-of-range mask cannot be honoured and cannot be guessed at, so the
		// terraform default stands and the value is simply not reported as
		// applied.
		return nil
	}
	if ws.BNK.Network == nil {
		ws.BNK.Network = &BNKNetworkCfg{}
	}
	ws.BNK.Network.VLANPrefixLen = &n
	return []string{"bnk.network.vlan_prefixlen (ROKSBNKCTL_VLAN_PREFIXLEN)"}
}

// overrideVLANPrefixLenPerVLANFromEnv overlays the per-VLAN overrides. Same
// independence rule as the shared value: nothing derives or validates these
// against the zone CIDRs.
func overrideVLANPrefixLenPerVLANFromEnv(ws *Workspace) []string {
	var applied []string
	for _, f := range vlanPerVLANOverrides {
		v := envValue(f.env)
		if v == "" {
			continue
		}
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 32 {
			continue // ignored, not fatal — same rule as the shared value
		}
		if ws.BNK.Network == nil {
			ws.BNK.Network = &BNKNetworkCfg{}
		}
		f.set(ws.BNK.Network, &n)
		applied = append(applied, f.label+" ("+f.env+")")
	}
	return applied
}

// envValue returns the trimmed value of an environment variable, or "" when
// unset or whitespace-only.
// envLookup is where every override reads its value. It is os.Getenv in normal
// use and swapped only by OverrideFromMap, so the override tables stay the ONE
// description of the surface: anything that wants to apply overrides from
// somewhere other than the process environment goes through the same rows,
// rather than growing a second copy that can disagree with this one.
var envLookup = os.Getenv

func envValue(name string) string {
	return strings.TrimSpace(envLookup(name))
}

// OverrideFromMap applies overrides from an explicit map instead of the process
// environment, and reports what it set exactly as OverrideFromEnv does.
//
// Not concurrency-safe: it swaps the package-level lookup for the duration.
// That is deliberate rather than lazy — the alternative is threading a lookup
// through every table row, which doubles the surface each name is written on,
// and the whole point of the tables is that a name appears once.
func OverrideFromMap(ws *Workspace, env map[string]string) []string {
	prev := envLookup
	envLookup = func(k string) string { return env[k] }
	defer func() { envLookup = prev }()
	return OverrideFromEnv(ws)
}

// stringOverride is one uniform env override: read o.env, assign it verbatim to
// one config field.
//
// The table exists for two reasons. It writes each variable's name ONCE — the
// read, the assignment and the report all derive from the same row, where
// before each was spelled separately and could drift. And it makes the surface
// ENUMERABLE: the drift guards that keep .env.example and the docs honest used
// to discover overrides by regex-scraping this file, which meant they silently
// covered less whenever the code's shape changed.
//
// Only the genuinely uniform ones live here. An override that decodes base64,
// parses an int, splits a list, or validates its input keeps its own block
// below — forcing those through a table would trade one kind of repetition for
// a worse kind of indirection.
type stringOverride struct {
	env   string
	field string // config path, for the applied-overrides report
	set   func(ws *Workspace, v string)
}

var stringOverrides = []stringOverride{
	{"ROKSBNKCTL_PREFIX", "prefix", func(ws *Workspace, v string) { ws.Prefix = v }},
	{"ROKSBNKCTL_REGION", "ibmcloud.region", func(ws *Workspace, v string) { ws.IBMCloud.Region = v }},
	{"ROKSBNKCTL_RESOURCE_GROUP", "ibmcloud.resource_group", func(ws *Workspace, v string) { ws.IBMCloud.ResourceGroup = v }},
	{"ROKSBNKCTL_CLUSTER_NAME", "cluster.name", func(ws *Workspace, v string) { ws.Cluster.Name = v }},
	{"ROKSBNKCTL_OPENSHIFT_VERSION", "cluster.openshift_version", func(ws *Workspace, v string) { ws.Cluster.OpenShiftVersion = v }},
	// vpc_cidr is the block the cluster VPC's per-zone prefixes come from.
	// Without it every roksbnkctl-created VPC in a region gets the SAME
	// prefixes, so two clusters cannot share a Transit Gateway — the norm for
	// disconnected installs, which must reach their mirror over one.
	{"ROKSBNKCTL_CLUSTER_VPC_CIDR", "cluster.vpc_cidr", func(ws *Workspace, v string) { ws.Cluster.VPCCIDR = v }},
	// How the worker nodes are attached. An invalid value is caught by
	// `cluster up`, which is where the value has to be right.
	{"ROKSBNKCTL_CLUSTER_NETWORK_MODE", "cluster.network_mode", func(ws *Workspace, v string) { ws.Cluster.NetworkMode = v }},
	// The FLO namespaces. Both are settable together to ONE value, which
	// collapses the two namespaces into one — verified working against BNK 2.3,
	// where FLO tolerates sharedComponentNamespace equalling its own namespace
	// (#66).
	// The CNEInstance scalars (#175). Uniform string setters, so they belong in
	// this table rather than in bespoke blocks. Each is reachable from YAML
	// already; without a matching override it cannot reach a blueprint, because
	// `init --non-interactive` builds config.yaml from the environment alone.
	{"ROKSBNKCTL_CNEINSTANCE_SIZE", "bnk.cneinstance_size", func(ws *Workspace, v string) { ws.BNK.CNEInstanceSize = v }},
	{"ROKSBNKCTL_WORKER_FLAVOR", "cluster.worker_flavor", func(ws *Workspace, v string) { ws.Cluster.WorkerFlavor = v }},
	{"ROKSBNKCTL_FLO_NAMESPACE", "bnk.flo_namespace", func(ws *Workspace, v string) { ws.BNK.FLONamespace = v }},
	{"ROKSBNKCTL_FLO_UTILS_NAMESPACE", "bnk.flo_utils_namespace", func(ws *Workspace, v string) { ws.BNK.FLOUtilsNamespace = v }},
	// Where the Gateway API bundle is fetched from when no mirror is recorded
	// (#185). An estate that blocks github.com but proxies it internally has no
	// other way to say so, and `init --non-interactive` builds config.yaml from
	// the environment alone — so without this row the setting cannot reach a
	// blueprint at all.
	{"ROKSBNKCTL_GATEWAY_API_BUNDLE_URL", "bnk.gateway_api_bundle_url", func(ws *Workspace, v string) { ws.BNK.GatewayAPIBundleURL = v }},
	// The registry mirror. A CI job needs to name its registry without a
	// config file: these four plus the bespoke ROKSBNKCTL_GENERIC_PASSWORD are
	// the whole surface for `registry replicate --target generic` and the
	// install that pulls back out of it.
	{"ROKSBNKCTL_REGISTRY_TARGET", "registry.target", func(ws *Workspace, v string) { registryCfg(ws).Target = v }},
	{"ROKSBNKCTL_GENERIC_HOST", "registry.generic_host", func(ws *Workspace, v string) { registryCfg(ws).GenericHost = v }},
	{"ROKSBNKCTL_GENERIC_REPO_PREFIX", "registry.generic_repo_prefix", func(ws *Workspace, v string) { registryCfg(ws).GenericRepoPrefix = v }},
	{"ROKSBNKCTL_GENERIC_USERNAME", "registry.generic_username", func(ws *Workspace, v string) { registryCfg(ws).GenericUsername = v }},
	// The mirror's CA and/or its fingerprint — how an env-only runner facing a
	// SELF-SIGNED mirror supplies trust out of band. _CA_B64 is the PEM chain,
	// already base64 and stored verbatim: a certificate is public data, encoded
	// only so it survives as a single env value.
	{"ROKSBNKCTL_GENERIC_CA_B64", "registry.generic_ca_b64", func(ws *Workspace, v string) { registryCfg(ws).GenericCAB64 = v }},
	{"ROKSBNKCTL_GENERIC_CA_SHA256", "registry.generic_ca_sha256", func(ws *Workspace, v string) { registryCfg(ws).GenericCASHA256 = v }},
	// Gateway-phase identity. GatewayClass is CLUSTER-scoped, so two BNK
	// installs sharing a cluster need distinct class names — which a CI matrix
	// sets per job, not per committed config.yaml. The controller name is what
	// the CNE controller matches itself against; empty derives it from the FLO
	// namespace, right for every deployment that installs its own controller.
	{"ROKSBNKCTL_GATEWAY_CLASS_NAME", "gateway.class_name", func(ws *Workspace, v string) { ws.Gateway.ClassName = v }},
	{"ROKSBNKCTL_GATEWAY_CONTROLLER_NAME", "gateway.controller_name", func(ws *Workspace, v string) { ws.Gateway.ControllerName = v }},
	// The foreign-proxy handoff (the "shared licensing cluster" topology):
	// what one CI job hands the NEXT one. The job that owns the proxy emits
	// `flp output flp_external_endpoint` + `flp_root_ca`; the job installing
	// BNK receives them as ordinary pipeline variables. The CA is already
	// base64 (that is how `flp output` emits it), so it is stored verbatim —
	// unlike the raw-secret vars, which get encoded on the way in.
	// ── #234: string fields that had no override ─────────────────────────────
	{"ROKSBNKCTL_CERT_MANAGER_NAMESPACE", "bnk.cert_manager.namespace", func(ws *Workspace, v string) { certManagerCfg(ws).Namespace = v }},
	{"ROKSBNKCTL_CERT_MANAGER_VERSION", "bnk.cert_manager.version", func(ws *Workspace, v string) { certManagerCfg(ws).Version = v }},
	{"ROKSBNKCTL_FAR_REPO_URL", "bnk.far_repo_url", func(ws *Workspace, v string) { ws.BNK.FARRepoURL = v }},
	{"ROKSBNKCTL_FLP_CHART_VERSION", "bnk.flp.chart_version", func(ws *Workspace, v string) { flpCfg(ws).ChartVersion = v }},
	{"ROKSBNKCTL_FLP_STORAGE_CLASS", "bnk.flp.storage_class", func(ws *Workspace, v string) { flpCfg(ws).StorageClass = v }},
	{"ROKSBNKCTL_FLP_VSI_FORWARD_PROXY_HOST", "bnk.flp.vsi.forward_proxy.host", func(ws *Workspace, v string) { flpForwardProxy(ws).Host = v }},
	{"ROKSBNKCTL_FLP_VSI_FORWARD_PROXY_PROTOCOL", "bnk.flp.vsi.forward_proxy.protocol", func(ws *Workspace, v string) { flpForwardProxy(ws).Protocol = v }},
	{"ROKSBNKCTL_GSLB_DATACENTER_NAME", "bnk.gslb_datacenter_name", func(ws *Workspace, v string) { ws.BNK.GSLBDatacenterName = v }},
	{"ROKSBNKCTL_HUGEPAGES_NODE_ROLE", "bnk.hugepages.node_role", func(ws *Workspace, v string) { hugepagesCfg(ws).NodeRole = v }},
	{"ROKSBNKCTL_HUGEPAGES_PROFILE_NAME", "bnk.hugepages.profile_name", func(ws *Workspace, v string) { hugepagesCfg(ws).ProfileName = v }},
	{"ROKSBNKCTL_TMM_K8S_ROUTES", "bnk.network.tmm_k8s_routes", func(ws *Workspace, v string) { networkCfg(ws).TMMK8SRoutes = v }},
	{"ROKSBNKCTL_TCP_SETTINGS_NAME", "bnk.tcp_settings_name", func(ws *Workspace, v string) { ws.BNK.TCPSettingsName = v }},
	{"ROKSBNKCTL_BNKFORGE_URL", "bnkforge.url", func(ws *Workspace, v string) { bnkForgeCfg(ws).URL = v }},
	{"ROKSBNKCTL_BNKFORGE_USERNAME", "bnkforge.username", func(ws *Workspace, v string) { bnkForgeCfg(ws).Username = v }},
	{"ROKSBNKCTL_BNKFORGE_PROJECT", "bnkforge.project", func(ws *Workspace, v string) { bnkForgeCfg(ws).Project = v }},
	{"ROKSBNKCTL_GATEWAY_APP_NAMESPACE", "gateway.app_namespace", func(ws *Workspace, v string) { ws.Gateway.AppNamespace = v }},
	{"ROKSBNKCTL_GATEWAY_BACKEND_SERVICE", "gateway.backend_service", func(ws *Workspace, v string) { ws.Gateway.BackendService = v }},
	{"ROKSBNKCTL_GATEWAY_EGRESS_MODE", "gateway.egress_mode", func(ws *Workspace, v string) { ws.Gateway.EgressMode = v }},
	{"ROKSBNKCTL_API_KEY_SOURCE", "ibmcloud.api_key_source", func(ws *Workspace, v string) { ws.IBMCloud.APIKeySource = v }},
	{"ROKSBNKCTL_ICR_HOST", "registry.icr_host", func(ws *Workspace, v string) { registryCfg(ws).ICRHost = v }},
	{"ROKSBNKCTL_ICR_NAMESPACE", "registry.icr_namespace", func(ws *Workspace, v string) { registryCfg(ws).ICRNamespace = v }},
	{"ROKSBNKCTL_REGISTRY_NAMESPACE", "registry.namespace", func(ws *Workspace, v string) { registryCfg(ws).Namespace = v }},
	// The FAR service account the registry verbs authenticate with. Its absence
	// is what forced a hand-edited config.yaml when mirroring 2.4-EA into
	// Artifactory -- in a flow whose whole point is being drivable from the
	// environment (#234).
	{"ROKSBNKCTL_SOURCE_SERVICE_ACCOUNT_B64", "registry.source_service_account_b64", func(ws *Workspace, v string) { registryCfg(ws).SourceServiceAccountB64 = v }},
	{"ROKSBNKCTL_BNK_EXISTING", "resources.bnk.existing", func(ws *Workspace, v string) { resourcesCfg(ws).BNK.Existing = v }},
	{"ROKSBNKCTL_CERT_MANAGER_EXISTING", "resources.cert_manager.existing", func(ws *Workspace, v string) { resourcesCfg(ws).CertManager.Existing = v }},
	{"ROKSBNKCTL_CLUSTER_JUMPHOSTS_EXISTING", "resources.cluster_jumphosts.existing", func(ws *Workspace, v string) { resourcesCfg(ws).ClusterJumphosts.Existing = v }},
	{"ROKSBNKCTL_REGISTRY_COS_EXISTING", "resources.registry_cos.existing", func(ws *Workspace, v string) { resourcesCfg(ws).RegistryCOS.Existing = v }},
	{"ROKSBNKCTL_TGW_JUMPHOST_EXISTING", "resources.tgw_jumphost.existing", func(ws *Workspace, v string) { resourcesCfg(ws).TGWJumphost.Existing = v }},
	{"ROKSBNKCTL_CLIENT_REGION", "resources.client_region", func(ws *Workspace, v string) { resourcesCfg(ws).ClientRegion = v }},
	{"ROKSBNKCTL_TESTING_JUMPHOST_PROFILE", "resources.testing_jumphost_profile", func(ws *Workspace, v string) { resourcesCfg(ws).TestingJumphostProfile = v }},
	{"ROKSBNKCTL_FLP_EXTERNAL_URL", "bnk.flp.external.url", func(ws *Workspace, v string) { flpExternal(ws).URL = v }},
	{"ROKSBNKCTL_FLP_ROOT_CA_B64", "bnk.flp.external.root_ca_b64", func(ws *Workspace, v string) { flpExternal(ws).RootCAB64 = v }},
}

// certManagerCfg, hugepagesCfg and networkCfg lazily create their blocks, the
// same shape as registryCfg and flpCfg above. Each replaces a nil check that the
// hand-written overrides repeated inline.
func certManagerCfg(ws *Workspace) *BNKCertManagerCfg {
	if ws.BNK.CertManager == nil {
		ws.BNK.CertManager = &BNKCertManagerCfg{}
	}
	return ws.BNK.CertManager
}

func hugepagesCfg(ws *Workspace) *HugepagesCfg {
	if ws.BNK.Hugepages == nil {
		ws.BNK.Hugepages = &HugepagesCfg{}
	}
	return ws.BNK.Hugepages
}

func networkCfg(ws *Workspace) *BNKNetworkCfg {
	if ws.BNK.Network == nil {
		ws.BNK.Network = &BNKNetworkCfg{}
	}
	return ws.BNK.Network
}

// resourcesCfg lazily creates the resources block so the tables above can write
// into it. The hand-written bool overrides each carried their own three-line
// nil check; this is the same thing once.
func resourcesCfg(ws *Workspace) *ResourcesCfg {
	if ws.Resources == nil {
		ws.Resources = &ResourcesCfg{}
	}
	return ws.Resources
}

// flpForwardProxy returns bnk.flp.vsi.forward_proxy, creating the intermediate
// blocks.
func flpForwardProxy(ws *Workspace) *BNKFLPForwardProxyCfg {
	v := flpVSI(ws)
	if v.ForwardProxy == nil {
		v.ForwardProxy = &BNKFLPForwardProxyCfg{}
	}
	return v.ForwardProxy
}

// ── #234: the overrides that were missing ────────────────────────────────────
//
// 78 of 187 config.yaml fields had no ROKSBNKCTL_* variable, and WHICH ones was
// arbitrary rather than principled: the overrides were added on demand, so
// resources.cert_manager.create was settable from the environment and
// resources.transit_gateway.create, an identical toggle beside it, was not.
//
// It stayed invisible because internal/cli/env.example is generated FROM the
// overrides — a gap cannot appear in a list derived from the thing that is
// missing. The generated cheatsheet is the first artefact that shows every field
// next to its override and leaves a dash where there is none.
//
// These three tables exist so the additions are rows rather than 49 bespoke
// blocks. Bespoke is for a variable that does something beyond its shape:
// base64-encodes, validates, or writes more than one field.

// boolOverrides parse with strconv.ParseBool, matching every hand-written bool
// override that predates them. An unparseable value is IGNORED rather than
// treated as false: "flase" meaning false is how a toggle silently does the
// opposite of what the pipeline intended.
var boolOverrides = []struct {
	env   string
	field string
	set   func(*Workspace, bool)
}{
	{"ROKSBNKCTL_BNK_CREATE", "resources.bnk.create",
		func(ws *Workspace, b bool) { resourcesCfg(ws).BNK.Create = b }},
	{"ROKSBNKCTL_CLUSTER_VPC_CREATE", "resources.cluster_vpc.create",
		func(ws *Workspace, b bool) { resourcesCfg(ws).ClusterVPC.Create = b }},
	{"ROKSBNKCTL_TRANSIT_GATEWAY_CREATE", "resources.transit_gateway.create",
		func(ws *Workspace, b bool) { resourcesCfg(ws).TransitGateway.Create = b }},
	{"ROKSBNKCTL_FLP_NODE_PORT_ACCESS", "bnk.flp.node_port_access",
		func(ws *Workspace, b bool) { flpCfg(ws).NodePortAccess = b }},
	{"ROKSBNKCTL_BNKFORGE_REGISTER", "bnkforge.register",
		func(ws *Workspace, b bool) { bnkForgeCfg(ws).Register = b }},
	{"ROKSBNKCTL_BNKFORGE_INSECURE", "bnkforge.insecure",
		func(ws *Workspace, b bool) { bnkForgeCfg(ws).Insecure = b }},
}

// ptrBoolOverrides set a *bool, where nil means UNSET and is not the same as
// false -- registry.include_deps nil takes the built-in default, false
// explicitly excludes. A plain bool table cannot express the difference, which
// is why this is separate rather than a third case inside boolOverrides.
var ptrBoolOverrides = []struct {
	env   string
	field string
	set   func(*Workspace, *bool)
}{
	{"ROKSBNKCTL_REGISTRY_INCLUDE_DEPS", "registry.include_deps",
		func(ws *Workspace, b *bool) { registryCfg(ws).IncludeDeps = b }},
}

// intOverrides parse with strconv.Atoi and IGNORE a non-numeric or negative
// value rather than storing a zero, for the same reason: a port silently set to
// 0 is harder to diagnose than one that was never set.
var intOverrides = []struct {
	env   string
	field string
	set   func(*Workspace, int)
}{
	{"ROKSBNKCTL_MIN_WORKER_MEMORY_GB", "cluster.min_worker_memory_gb",
		func(ws *Workspace, n int) { ws.Cluster.MinWorkerMemoryGB = n }},
	{"ROKSBNKCTL_MIN_WORKER_VCPU_COUNT", "cluster.min_worker_vcpu_count",
		func(ws *Workspace, n int) { ws.Cluster.MinWorkerVCPUCount = n }},
	{"ROKSBNKCTL_TESTING_MIN_MEMORY_GB", "resources.testing_min_memory_gb",
		func(ws *Workspace, n int) { resourcesCfg(ws).TestingMinMemoryGB = n }},
	{"ROKSBNKCTL_TESTING_MIN_VCPU_COUNT", "resources.testing_min_vcpu_count",
		func(ws *Workspace, n int) { resourcesCfg(ws).TestingMinVCPUCount = n }},
	{"ROKSBNKCTL_GATEWAY_BACKEND_PORT", "gateway.backend_port",
		func(ws *Workspace, n int) { ws.Gateway.BackendPort = n }},
	{"ROKSBNKCTL_GATEWAY_VXLAN_PORT", "gateway.vxlan_port",
		func(ws *Workspace, n int) { ws.Gateway.VXLANPort = n }},
	{"ROKSBNKCTL_FLP_VSI_FORWARD_PROXY_PORT", "bnk.flp.vsi.forward_proxy.port",
		func(ws *Workspace, n int) { flpForwardProxy(ws).Port = n }},
}

// stringListOverrides split on commas, trimming each element and dropping
// empties, so "a, b," yields two entries rather than three with one blank.
//
// Distinct from sgCIDROverrides, which is the same shape but named for the
// security-group CIDR lists it carries; keeping them apart means neither table's
// name lies about what is in it.
var stringListOverrides = []struct {
	env   string
	field string
	set   func(*Workspace, []string)
}{
	{"ROKSBNKCTL_FLP_NODE_PORT_SOURCE_CIDRS", "bnk.flp.node_port_source_cidrs",
		func(ws *Workspace, v []string) { flpCfg(ws).NodePortSourceCIDRs = v }},
	{"ROKSBNKCTL_COPIED_SSH_KEY_FILES", "resources.copied_ssh_key_files",
		func(ws *Workspace, v []string) { resourcesCfg(ws).CopiedSSHKeyFiles = v }},
	{"ROKSBNKCTL_GATEWAY_CLIENT_SUBNET_LOCAL", "gateway.client_subnet_local",
		func(ws *Workspace, v []string) { ws.Gateway.ClientSubnetLocal = v }},
	{"ROKSBNKCTL_GATEWAY_CLIENT_SUBNET_REMOTE", "gateway.client_subnet_remote",
		func(ws *Workspace, v []string) { ws.Gateway.ClientSubnetRemote = v }},
	{"ROKSBNKCTL_FLP_VSI_ALLOWED_CIDRS", "bnk.flp.vsi.allowed_cidrs",
		func(ws *Workspace, v []string) { flpVSI(ws).AllowedCIDRs = v }},
}

// sgCIDROverrides are the security-group source-CIDR lists — uniform except
// for the comma-list split, so they keep their own table. Package-level so
// SupportedOverrideNames can enumerate them: an earlier revision declared this
// table inline in OverrideFromEnv, and the four names silently dropped out of
// the reported surface.
var sgCIDROverrides = []struct {
	env   string
	field string
	set   func(*Workspace, []string)
}{
	{"ROKSBNKCTL_TESTING_JUMPHOST_ALLOWED_CIDRS", "resources.testing_jumphost_allowed_cidrs",
		func(ws *Workspace, v []string) { ws.Resources.TestingJumphostAllowedCIDRs = v }},
	{"ROKSBNKCTL_TESTING_CLIENT_VPC_INBOUND_CIDRS", "resources.testing_client_vpc_inbound_cidrs",
		func(ws *Workspace, v []string) { ws.Resources.TestingClientVPCInboundCIDRs = v }},
	{"ROKSBNKCTL_CLUSTER_HTTP_ALLOWED_CIDRS", "resources.cluster_http_allowed_cidrs",
		func(ws *Workspace, v []string) { ws.Resources.ClusterHTTPAllowedCIDRs = v }},
	{"ROKSBNKCTL_CLUSTER_VPC_DEFAULT_SG_INBOUND_CIDRS", "resources.cluster_vpc_default_sg_inbound_cidrs",
		func(ws *Workspace, v []string) { ws.Resources.ClusterVPCDefaultSGInboundCIDRs = v }},
}

// vlanPerVLANOverrides are the per-VLAN TMM prefix-length overrides — uniform
// except for the bounded-int parse. Package-level for the same reason as
// sgCIDROverrides.
var vlanPerVLANOverrides = []struct {
	env   string
	label string
	set   func(*BNKNetworkCfg, *int)
}{
	{"ROKSBNKCTL_VLAN_PREFIXLEN_EXTERNAL", "bnk.network.vlan_prefixlen_external", func(c *BNKNetworkCfg, n *int) { c.VLANPrefixLenExternal = n }},
	{"ROKSBNKCTL_VLAN_PREFIXLEN_INTERNAL", "bnk.network.vlan_prefixlen_internal", func(c *BNKNetworkCfg, n *int) { c.VLANPrefixLenInternal = n }},
}

// bespokeOverrideNames are the ROKSBNKCTL_* variables handled by their own
// block rather than by one of the tables (stringOverrides, sgCIDROverrides,
// vlanPerVLANOverrides, and envoverride_flp.go's flpVSIStringOverrides /
// cosOverrides), because each does something beyond what its table's shape
// expresses — base64-encodes, parses a bool or an int, or validates.
//
// Declared so SupportedOverrideNames can report the WHOLE surface. A guard test
// checks the surface bidirectionally against every ROKSBNKCTL_* literal the
// code carries, so an override added in any shape — bespoke block, table row,
// or the computed zone family — fails rather than quietly leaving the docs and
// the demo allowlist behind.
var bespokeOverrideNames = []string{
	"IBMCLOUD_API_KEY",
	"ROKSBNKCTL_API_KEY_B64",
	"ROKSBNKCTL_BIGIP_PASSWORD",
	"ROKSBNKCTL_BIGIP_URL",
	"ROKSBNKCTL_BIGIP_USERNAME",
	"ROKSBNKCTL_BNKFORGE_CA_B64",
	"ROKSBNKCTL_BNKFORGE_PASSWORD",
	"ROKSBNKCTL_CLIENT_VPC_CREATE",
	"ROKSBNKCTL_CLIENT_VPC_NAME",
	"ROKSBNKCTL_CLUSTER_CREATE",
	"ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY",
	"ROKSBNKCTL_CLUSTER_VPC_ID",
	"ROKSBNKCTL_EXISTING_SUBNET_IDS",
	"ROKSBNKCTL_FAR_AUTH_FILE",
	"ROKSBNKCTL_FAR_AUTH_LOCAL_FILE",
	"ROKSBNKCTL_GATEWAY_API_MTLS",
	// BNK 2.4 conformance with F5's reference CNEInstance.
	"ROKSBNKCTL_CLUSTER_IDENTIFIER",
	"ROKSBNKCTL_DEMO_MODE",
	"ROKSBNKCTL_WHOLE_CLUSTER",
	"ROKSBNKCTL_CERT_MANAGER_CREATE",
	"ROKSBNKCTL_STORAGE_CLASS_NAME",
	"ROKSBNKCTL_CLUSTER_JUMPHOSTS_CREATE",
	"ROKSBNKCTL_REGISTRY_COS_CREATE",
	"ROKSBNKCTL_HUGEPAGES",
	"ROKSBNKCTL_HUGEPAGES_COUNT",
	"ROKSBNKCTL_HUGEPAGES_SIZE",
	"ROKSBNKCTL_EXTERNAL_BIGIP",
	"ROKSBNKCTL_EXTERNAL_BIGIP_LOGIN_SECRET",
	"ROKSBNKCTL_GATEWAY_API_VERSION",
	"ROKSBNKCTL_TMM_ANTI_AFFINITY",
	"ROKSBNKCTL_TMM_ANTI_AFFINITY_TOPOLOGY_KEY",
	"ROKSBNKCTL_TMM_POD_LABEL",
	"ROKSBNKCTL_TMM_REPLICAS",
	"ROKSBNKCTL_TMM_ROLLING_UPDATE",
	"ROKSBNKCTL_TMM_ZONE_MAX_SKEW",
	"ROKSBNKCTL_TMM_ZONE_SPREAD",
	"ROKSBNKCTL_TMM_ZONE_TOPOLOGY_KEY",
	"ROKSBNKCTL_TMM_ZONE_WHEN_UNSATISFIABLE",
	"ROKSBNKCTL_WATCH_NAMESPACES",
	"ROKSBNKCTL_FLP_MODE",
	"ROKSBNKCTL_FLP_NAMESPACE",
	"ROKSBNKCTL_FLP_VSI_BOOT_SIZE_GB",
	"ROKSBNKCTL_FLP_VSI_CREATE_VPC",
	"ROKSBNKCTL_FLP_VSI_FLOATING_IP",
	"ROKSBNKCTL_FLP_VSI_LICENSING_ALLOWED_CIDRS",
	"ROKSBNKCTL_FLP_VSI_MANAGEMENT_ALLOWED_CIDRS",
	"ROKSBNKCTL_GATEWAY_L4_LISTENER_PORT",
	"ROKSBNKCTL_GATEWAY_ROUTE_EXAMPLES",
	"ROKSBNKCTL_GENERIC_PASSWORD",
	"ROKSBNKCTL_LICENSE_MODE",
	"ROKSBNKCTL_MANIFEST_VERSION",
	"ROKSBNKCTL_REACHABILITY_RETRY_SECONDS",
	"ROKSBNKCTL_REACHABILITY_TIMEOUT_SECONDS",
	"ROKSBNKCTL_SUBSCRIPTION_JWT_FILE",
	"ROKSBNKCTL_SUBSCRIPTION_JWT_LOCAL_FILE",
	"ROKSBNKCTL_TESTING_SSH_KEY_NAME",
	"ROKSBNKCTL_TESTING_VPC_NAME",
	"ROKSBNKCTL_TGW_JUMPHOST_CREATE",
	"ROKSBNKCTL_TRANSIT_GATEWAY_NAME",
	"ROKSBNKCTL_TRUSTED_PROFILE_ROLES",
	"ROKSBNKCTL_TRUSTED_PROFILE_SA",
	"ROKSBNKCTL_VLAN_PREFIXLEN",
	"ROKSBNKCTL_WORKERS_PER_ZONE",
}

// SupportedOverrideNames returns every ROKSBNKCTL_* (and IBMCLOUD_API_KEY)
// variable OverrideFromEnv honours — the uniform tables here and in
// envoverride_flp.go, the bespoke blocks, and the computed per-zone family,
// enumerated.
//
// This is the authoritative list. It is what the .env.example parity guard and
// the documentation guard enumerate, so a new override is covered by them the
// moment it exists rather than whenever a scraping regex happens to match it.
// An earlier revision summed only stringOverrides + bespokeOverrideNames and
// silently reported 19 fewer names than the code honoured — which is why the
// surface guard now checks this list against the source bidirectionally.
func SupportedOverrideNames() []string {
	var out []string
	for _, o := range stringOverrides {
		out = append(out, o.env)
	}
	for _, o := range sgCIDROverrides {
		out = append(out, o.env)
	}
	for _, o := range boolOverrides {
		out = append(out, o.env)
	}
	for _, o := range ptrBoolOverrides {
		out = append(out, o.env)
	}
	for _, o := range intOverrides {
		out = append(out, o.env)
	}
	for _, o := range stringListOverrides {
		out = append(out, o.env)
	}
	for _, o := range vlanPerVLANOverrides {
		out = append(out, o.env)
	}
	for _, o := range flpVSIStringOverrides {
		out = append(out, o.env)
	}
	for _, o := range cosOverrides {
		out = append(out, o.env)
	}
	out = append(out, bespokeOverrideNames...)
	out = append(out, zoneOverrideNames()...)
	// The advanced.* family is reported from the ENVIRONMENT rather than
	// enumerated, because the component set belongs to the product (#175). An
	// unset environment contributes nothing, so this changes no existing report.
	out = append(out, AdvancedEnvOverrideNames()...)
	sort.Strings(out)
	return out
}
