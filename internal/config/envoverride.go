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
//	                                  the legal set is a property of the manifest,
//	                                  so it is deliberately not validated here)
//	ROKSBNKCTL_GATEWAY_API_MTLS     → bnk.gateway_api_mtls (bool) — install the
//	                                  Gateway API bundle 2.4 needs for mTLS; off by
//	                                  default, ignored on 2.3
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
//	ROKSBNKCTL_BNKFORGE_CA_B64      → bnkforge.ca_b64 (PEM CA pinning the Forge server)
//	ROKSBNKCTL_REGISTRY_TARGET      → registry.target (icr|generic)
//	ROKSBNKCTL_GENERIC_HOST         → registry.generic_host
//	ROKSBNKCTL_GENERIC_REPO_PREFIX  → registry.generic_repo_prefix
//	ROKSBNKCTL_GENERIC_USERNAME     → registry.generic_username
//	ROKSBNKCTL_GENERIC_PASSWORD     → registry.generic_password_b64 (raw, base64-encoded)
//	ROKSBNKCTL_GENERIC_CA_B64       → registry.generic_ca_b64 (verbatim; already base64)
//	ROKSBNKCTL_GENERIC_CA_SHA256    → registry.generic_ca_sha256 (the out-of-band CA pin)
//	ROKSBNKCTL_GTM_URL              → bnk.gtm.url (BIG-IP DNS for GSLB; #51)
//	ROKSBNKCTL_GTM_USERNAME         → bnk.gtm.username
//	ROKSBNKCTL_GTM_PASSWORD         → bnk.gtm.password_b64 (raw, base64-encoded)
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

	// GTM / BIG-IP DNS connection for GSLB (#51). Same shape as the CIS BIG-IP
	// credentials: the password arrives RAW and is stored base64.
	if v := envValue("ROKSBNKCTL_GTM_PASSWORD"); v != "" {
		gtmCfg(ws).PasswordB64 = base64.StdEncoding.EncodeToString([]byte(v))
		applied = append(applied, "bnk.gtm.password_b64 (ROKSBNKCTL_GTM_PASSWORD)")
	}
	// Credentials without a URL configure nothing: the render is gated on the
	// URL, so a pipeline that sets the user and password but forgets
	// ROKSBNKCTL_GTM_URL would see both reported as applied, written to
	// config.yaml, and silently never rendered — GSLB simply never registers,
	// with nothing anywhere saying why. Say it here, where the omission is.
	if g := ws.BNK.GTM; g != nil && strings.TrimSpace(g.URL) == "" &&
		(g.Username != "" || g.PasswordB64 != "") {
		applied = append(applied,
			"! bnk.gtm credentials set without ROKSBNKCTL_GTM_URL — GTM stays DISABLED and they will not be used")
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

// gtmCfg lazily creates bnk.gtm so the overrides above can populate it.
func gtmCfg(ws *Workspace) *BNKGTMCfg {
	if ws.BNK.GTM == nil {
		ws.BNK.GTM = &BNKGTMCfg{}
	}
	return ws.BNK.GTM
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
func envValue(name string) string {
	return strings.TrimSpace(os.Getenv(name))
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
	{"ROKSBNKCTL_FLO_NAMESPACE", "bnk.flo_namespace", func(ws *Workspace, v string) { ws.BNK.FLONamespace = v }},
	{"ROKSBNKCTL_FLO_UTILS_NAMESPACE", "bnk.flo_utils_namespace", func(ws *Workspace, v string) { ws.BNK.FLOUtilsNamespace = v }},
	{"ROKSBNKCTL_GTM_URL", "bnk.gtm.url", func(ws *Workspace, v string) { gtmCfg(ws).URL = v }},
	{"ROKSBNKCTL_GTM_USERNAME", "bnk.gtm.username", func(ws *Workspace, v string) { gtmCfg(ws).Username = v }},
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
	{"ROKSBNKCTL_FLP_EXTERNAL_URL", "bnk.flp.external.url", func(ws *Workspace, v string) { flpExternal(ws).URL = v }},
	{"ROKSBNKCTL_FLP_ROOT_CA_B64", "bnk.flp.external.root_ca_b64", func(ws *Workspace, v string) { flpExternal(ws).RootCAB64 = v }},
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
	"ROKSBNKCTL_CLIENT_VPC_CREATE",
	"ROKSBNKCTL_CLIENT_VPC_NAME",
	"ROKSBNKCTL_CLUSTER_CREATE",
	"ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY",
	"ROKSBNKCTL_CLUSTER_VPC_ID",
	"ROKSBNKCTL_EXISTING_SUBNET_IDS",
	"ROKSBNKCTL_FAR_AUTH_FILE",
	"ROKSBNKCTL_FAR_AUTH_LOCAL_FILE",
	"ROKSBNKCTL_GATEWAY_API_MTLS",
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
	"ROKSBNKCTL_GTM_PASSWORD",
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
