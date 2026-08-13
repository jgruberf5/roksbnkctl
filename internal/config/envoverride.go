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
	"encoding/base64"
	"os"
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
//	ROKSBNKCTL_OPENSHIFT_VERSION    → cluster.openshift_version
//	ROKSBNKCTL_WORKERS_PER_ZONE     → cluster.workers_per_zone (int)
//	ROKSBNKCTL_CLUSTER_PUBLIC_GATEWAY → cluster.public_gateway (bool; false = no worker egress)
//	ROKSBNKCTL_CLUSTER_VPC_CIDR     → cluster.vpc_cidr (per-zone prefixes; avoids TGW overlap)
//	ROKSBNKCTL_CLUSTER_NETWORK_MODE → cluster.network_mode (single-nic default, or multi-nic)
//	ROKSBNKCTL_TRUSTED_PROFILE_SA   → bnk.trusted_profile.service_account
//	ROKSBNKCTL_TRUSTED_PROFILE_ROLES → bnk.trusted_profile.roles (comma-separated)
//	ROKSBNKCTL_VLAN_PREFIXLEN       → bnk.network.vlan_prefixlen (TMM self-IP mask; independent of the zone CIDRs)
//	ROKSBNKCTL_CLUSTER_VPC_ID       → resources.cluster_vpc (create:false + existing=<vpc-id>)
//	ROKSBNKCTL_EXISTING_SUBNET_IDS  → cluster.existing_subnet_ids (comma-separated, zone order)
//	ROKSBNKCTL_TGW_JUMPHOST_CREATE  → resources.tgw_jumphost.create (bool)
//	ROKSBNKCTL_CLIENT_VPC_CREATE    → resources.client_vpc.create (bool)
//	ROKSBNKCTL_CLIENT_VPC_NAME      → resources.client_vpc.existing (adopt a client VPC)
//	ROKSBNKCTL_TESTING_SSH_KEY_NAME → resources.testing_ssh_key_name
//	ROKSBNKCTL_REGISTRY_TARGET      → registry.target (icr|generic)
//	ROKSBNKCTL_GENERIC_HOST         → registry.generic_host
//	ROKSBNKCTL_GENERIC_REPO_PREFIX  → registry.generic_repo_prefix
//	ROKSBNKCTL_GENERIC_USERNAME     → registry.generic_username
//	ROKSBNKCTL_GENERIC_PASSWORD     → registry.generic_password_b64 (raw, base64-encoded)
//	ROKSBNKCTL_GENERIC_CA_B64       → registry.generic_ca_b64 (verbatim; already base64)
//	ROKSBNKCTL_GENERIC_CA_SHA256    → registry.generic_ca_sha256 (the out-of-band CA pin)
//	ROKSBNKCTL_LICENSE_MODE         → bnk.license_mode (connected|disconnected|f5licenseproxy)
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

	if v := envValue("ROKSBNKCTL_PREFIX"); v != "" {
		ws.Prefix = v
		applied = append(applied, "prefix (ROKSBNKCTL_PREFIX)")
	}
	if v := envValue("ROKSBNKCTL_REGION"); v != "" {
		ws.IBMCloud.Region = v
		applied = append(applied, "ibmcloud.region (ROKSBNKCTL_REGION)")
	}
	if v := envValue("ROKSBNKCTL_RESOURCE_GROUP"); v != "" {
		ws.IBMCloud.ResourceGroup = v
		applied = append(applied, "ibmcloud.resource_group (ROKSBNKCTL_RESOURCE_GROUP)")
	}

	// Cluster identity — the fields a non-interactive (`--non-interactive`)
	// init needs that the seed/interview would otherwise supply. Lets an
	// argv+env runner (e.g. a CI / BNK Forge container step) seed config.yaml
	// from env alone.
	if v := envValue("ROKSBNKCTL_CLUSTER_NAME"); v != "" {
		ws.Cluster.Name = v
		applied = append(applied, "cluster.name (ROKSBNKCTL_CLUSTER_NAME)")
	}
	if v := envValue("ROKSBNKCTL_CLUSTER_CREATE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			ws.Cluster.Create = b
			applied = append(applied, "cluster.create (ROKSBNKCTL_CLUSTER_CREATE)")
		}
	}
	if v := envValue("ROKSBNKCTL_OPENSHIFT_VERSION"); v != "" {
		ws.Cluster.OpenShiftVersion = v
		applied = append(applied, "cluster.openshift_version (ROKSBNKCTL_OPENSHIFT_VERSION)")
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

	// The block the cluster VPC's per-zone prefixes come from. Without it every
	// roksbnkctl-created VPC in a region gets the SAME prefixes, so two clusters
	// cannot share a Transit Gateway — the norm for disconnected installs, which
	// must reach their mirror over one. An env-only runner had no way to say so.
	if v := envValue("ROKSBNKCTL_CLUSTER_VPC_CIDR"); v != "" {
		ws.Cluster.VPCCIDR = v
		applied = append(applied, "cluster.vpc_cidr (ROKSBNKCTL_CLUSTER_VPC_CIDR)")
	}

	// How the worker nodes are attached. Present for the same reason vpc_cidr is:
	// the CI runners drive a whole deployment from the environment and never
	// write a config.yaml, so without this there is no way for them to ask for a
	// multi-nic cluster at all. An invalid value is caught by `cluster up`, which
	// is where the value has to be right.
	if v := envValue("ROKSBNKCTL_CLUSTER_NETWORK_MODE"); v != "" {
		ws.Cluster.NetworkMode = v
		applied = append(applied, "cluster.network_mode (ROKSBNKCTL_CLUSTER_NETWORK_MODE)")
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
	applied = append(applied, overrideVLANPrefixLenFromEnv(ws)...)
	if v := envValue("ROKSBNKCTL_TESTING_SSH_KEY_NAME"); v != "" {
		if ws.Resources == nil {
			ws.Resources = &ResourcesCfg{}
		}
		ws.Resources.TestingSSHKeyName = v
		applied = append(applied, "resources.testing_ssh_key_name (ROKSBNKCTL_TESTING_SSH_KEY_NAME)")
	}

	// The registry mirror. A CI job needs to name its registry without a config
	// file: these four plus ROKSBNKCTL_GENERIC_PASSWORD are the whole surface for
	// `registry replicate --target generic` and the install that pulls back out of
	// it. Setting only the password (the pre-v1.19 surface) meant a pipeline still
	// had to shell out to four `registry target` subcommands to say WHERE.
	if v := envValue("ROKSBNKCTL_REGISTRY_TARGET"); v != "" {
		registryCfg(ws).Target = v
		applied = append(applied, "registry.target (ROKSBNKCTL_REGISTRY_TARGET)")
	}
	if v := envValue("ROKSBNKCTL_GENERIC_HOST"); v != "" {
		registryCfg(ws).GenericHost = v
		applied = append(applied, "registry.generic_host (ROKSBNKCTL_GENERIC_HOST)")
	}
	if v := envValue("ROKSBNKCTL_GENERIC_REPO_PREFIX"); v != "" {
		registryCfg(ws).GenericRepoPrefix = v
		applied = append(applied, "registry.generic_repo_prefix (ROKSBNKCTL_GENERIC_REPO_PREFIX)")
	}
	if v := envValue("ROKSBNKCTL_GENERIC_USERNAME"); v != "" {
		registryCfg(ws).GenericUsername = v
		applied = append(applied, "registry.generic_username (ROKSBNKCTL_GENERIC_USERNAME)")
	}
	// Generic OCI registry password (e.g. an Artifactory access token) — raw in
	// the env, base64-encoded into the config like the API key.
	if v := envValue("ROKSBNKCTL_GENERIC_PASSWORD"); v != "" {
		registryCfg(ws).GenericPasswordB64 = base64.StdEncoding.EncodeToString([]byte(v))
		applied = append(applied, "registry.generic_password_b64 (ROKSBNKCTL_GENERIC_PASSWORD)")
	}
	// The mirror's CA and/or its fingerprint. Without these an env-only runner
	// facing a SELF-SIGNED mirror has no way to supply trust out of band, and
	// `registry replicate` refuses to adopt a captured CA (it would be installed
	// into every node's trust store). _CA_B64 is the PEM chain, already base64 and
	// passed verbatim — a certificate is public data, encoded only so it survives
	// as a single env value.
	if v := envValue("ROKSBNKCTL_GENERIC_CA_B64"); v != "" {
		registryCfg(ws).GenericCAB64 = v
		applied = append(applied, "registry.generic_ca_b64 (ROKSBNKCTL_GENERIC_CA_B64)")
	}
	if v := envValue("ROKSBNKCTL_GENERIC_CA_SHA256"); v != "" {
		registryCfg(ws).GenericCASHA256 = v
		applied = append(applied, "registry.generic_ca_sha256 (ROKSBNKCTL_GENERIC_CA_SHA256)")
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

	// The foreign-proxy handoff (the "shared licensing cluster" topology). These
	// two are what one CI job hands the NEXT one: the job that owns the proxy
	// emits `flp output flp_external_endpoint` + `flp_root_ca`, and the job that
	// installs BNK receives them as ordinary pipeline variables. Without an env
	// surface a pipeline would have to template a config.yaml just to pass two
	// values between jobs.
	if v := envValue("ROKSBNKCTL_FLP_EXTERNAL_URL"); v != "" {
		flpExternal(ws).URL = v
		applied = append(applied, "bnk.flp.external.url (ROKSBNKCTL_FLP_EXTERNAL_URL)")
	}
	// Already base64 (that is how `flp output flp_root_ca` emits it), so it is
	// stored verbatim — unlike the raw-secret vars above, which get encoded here.
	if v := envValue("ROKSBNKCTL_FLP_ROOT_CA_B64"); v != "" {
		flpExternal(ws).RootCAB64 = v
		applied = append(applied, "bnk.flp.external.root_ca_b64 (ROKSBNKCTL_FLP_ROOT_CA_B64)")
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

// overrideNetworkZonesFromEnv assembles bnk.network.zones from the fixed indexed
// env vars. Each zone needs all six fields set (a partial zone is skipped so the
// rendered cneinstance_network_zones object is never half-populated). When any
// zone is supplied it REPLACES bnk.network.zones (env wins, as elsewhere).
func overrideNetworkZonesFromEnv(ws *Workspace) []string {
	var zones []BNKZoneCfg
	for i := 1; i <= maxNetworkZones; i++ {
		p := "ROKSBNKCTL_ZONE" + strconv.Itoa(i) + "_"
		z := BNKZoneCfg{
			ExtVLANCIDR:    envValue(p + "EXT_VLAN_CIDR"),
			IntVLANCIDR:    envValue(p + "INT_VLAN_CIDR"),
			IntSNATCIDR:    envValue(p + "INT_SNAT_CIDR"),
			IntVIPCIDR:     envValue(p + "INT_VIP_CIDR"),
			ExternalSelfIP: envValue(p + "EXTERNAL_SELFIP"),
			InternalSelfIP: envValue(p + "INTERNAL_SELFIP"),
		}
		if z.ExtVLANCIDR == "" || z.IntVLANCIDR == "" || z.IntSNATCIDR == "" ||
			z.IntVIPCIDR == "" || z.ExternalSelfIP == "" || z.InternalSelfIP == "" {
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

// envValue returns the trimmed value of an environment variable, or "" when
// unset or whitespace-only.
func envValue(name string) string {
	return strings.TrimSpace(os.Getenv(name))
}
