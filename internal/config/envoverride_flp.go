package config

// PRD 13 Issue 4, FLP-VSI increment — the `init --override-from-env` field map
// for the STANDALONE F5 License Proxy VSI, its local-file supply chain, and the
// orchestration COS coordinates.
//
// Why these exist: a container operator (BNK Forge's container engine, or any
// argv-only CI runner) builds config.yaml from the environment alone — it has no
// shell to write a YAML file with. The tested reference deployment
// (scripts/demos/disconnected-cluster-cli-demo, Phase 3) drives `flp up` from a
// config.yaml written by a heredoc on a VSI; every field that recipe sets is
// mapped here so the SAME deployment is reachable from env vars only.
//
// Same rules as the core map in envoverride.go: a FIXED field map (never
// arbitrary interpolation), env WINS over whatever the seed/interview produced,
// and only set + non-empty variables are applied.

import (
	"strconv"
	"strings"
)

// overrideFLPFromEnv overlays the FLP deployment backend from the environment.
//
//	ROKSBNKCTL_FLP_MODE                     → bnk.flp.mode ("" | helm | vsi)
//	ROKSBNKCTL_FLP_VSI_VPC                  → bnk.flp.vsi.vpc (existing VPC id — the
//	                                          standalone, cluster-less appliance)
//	ROKSBNKCTL_FLP_VSI_CREATE_VPC           → bnk.flp.vsi.create_vpc (bool; #60/#64)
//	ROKSBNKCTL_FLP_VSI_VPC_NAME             → bnk.flp.vsi.vpc_name
//	ROKSBNKCTL_FLP_VSI_SUBNET_CIDR          → bnk.flp.vsi.subnet_cidr
//	ROKSBNKCTL_FLP_VSI_ZONE                 → bnk.flp.vsi.zone
//	ROKSBNKCTL_FLP_VSI_PROFILE              → bnk.flp.vsi.profile
//	ROKSBNKCTL_FLP_VSI_SSH_KEY              → bnk.flp.vsi.ssh_key
//	ROKSBNKCTL_FLP_VSI_BOOT_SIZE_GB         → bnk.flp.vsi.boot_size_gb (int)
//	ROKSBNKCTL_FLP_VSI_REACH                → bnk.flp.vsi.reach (private | floating)
//	ROKSBNKCTL_FLP_VSI_FLOATING_IP          → bnk.flp.vsi.floating_ip (bool)
//	ROKSBNKCTL_FLP_VSI_MANAGEMENT_ALLOWED_CIDRS → bnk.flp.vsi.management_allowed_cidrs (comma list)
//	ROKSBNKCTL_FLP_VSI_LICENSING_ALLOWED_CIDRS  → bnk.flp.vsi.licensing_allowed_cidrs (comma list)
//	ROKSBNKCTL_FLP_VSI_STATUS_IMAGE         → bnk.flp.vsi.status_image
//	ROKSBNKCTL_FLP_VSI_STATUS_REGISTRY_HOST → bnk.flp.vsi.status_registry_host
//	ROKSBNKCTL_FLP_VSI_STATUS_REGISTRY_CA_B64 → bnk.flp.vsi.status_registry_ca_b64 (verbatim; already base64)
//
// StandaloneFLPVSI() keys on mode==vsi AND a non-empty vsi.vpc, so those two are
// what turn a cluster-less appliance on; the rest are refinements.
func overrideFLPFromEnv(ws *Workspace) []string {
	var applied []string

	if v := envValue("ROKSBNKCTL_FLP_MODE"); v != "" {
		flpCfg(ws).Mode = v
		applied = append(applied, "bnk.flp.mode (ROKSBNKCTL_FLP_MODE)")
	}

	// Each VSI field creates the vsi block on demand, so setting only one of them
	// (e.g. just the SSH key on a config that already carries the VPC) never
	// nil-panics and never wipes the others.
	for _, f := range []struct {
		env   string
		label string
		set   func(*BNKFLPVSICfg, string)
	}{
		{"ROKSBNKCTL_FLP_VSI_VPC", "bnk.flp.vsi.vpc", func(c *BNKFLPVSICfg, v string) { c.VPC = v }},
		{"ROKSBNKCTL_FLP_VSI_ZONE", "bnk.flp.vsi.zone", func(c *BNKFLPVSICfg, v string) { c.Zone = v }},
		{"ROKSBNKCTL_FLP_VSI_PROFILE", "bnk.flp.vsi.profile", func(c *BNKFLPVSICfg, v string) { c.Profile = v }},
		{"ROKSBNKCTL_FLP_VSI_SSH_KEY", "bnk.flp.vsi.ssh_key", func(c *BNKFLPVSICfg, v string) { c.SSHKey = v }},
		{"ROKSBNKCTL_FLP_VSI_REACH", "bnk.flp.vsi.reach", func(c *BNKFLPVSICfg, v string) { c.Reach = v }},
		// #60 gave the proxy its own VPC; #64 makes that reachable from a
		// blueprint. Being able to create the VPC is what lets the FLP be the
		// FIRST thing deployed in an air-gapped estate, which is exactly the
		// case a Forge module automates — and until now it could only be
		// expressed in a config.yaml the modules never write.
		{"ROKSBNKCTL_FLP_VSI_VPC_NAME", "bnk.flp.vsi.vpc_name", func(c *BNKFLPVSICfg, v string) { c.VPCName = v }},
		{"ROKSBNKCTL_FLP_VSI_SUBNET_CIDR", "bnk.flp.vsi.subnet_cidr", func(c *BNKFLPVSICfg, v string) { c.SubnetCIDR = v }},
		{"ROKSBNKCTL_FLP_VSI_STATUS_IMAGE", "bnk.flp.vsi.status_image", func(c *BNKFLPVSICfg, v string) { c.StatusImage = v }},
		{"ROKSBNKCTL_FLP_VSI_STATUS_REGISTRY_HOST", "bnk.flp.vsi.status_registry_host", func(c *BNKFLPVSICfg, v string) { c.StatusRegistryHost = v }},
		// Already base64 (that is how the operator captures a mirror's CA), so it is
		// stored verbatim — like ROKSBNKCTL_FLP_ROOT_CA_B64, unlike the raw secrets
		// elsewhere in the map that get encoded on the way in.
		{"ROKSBNKCTL_FLP_VSI_STATUS_REGISTRY_CA_B64", "bnk.flp.vsi.status_registry_ca_b64", func(c *BNKFLPVSICfg, v string) { c.StatusRegistryCAB64 = v }},
	} {
		if v := envValue(f.env); v != "" {
			f.set(flpVSI(ws), v)
			applied = append(applied, f.label+" ("+f.env+")")
		}
	}

	// Typed fields. A value that does not parse is IGNORED rather than fatal,
	// matching how the core map treats ROKSBNKCTL_CLUSTER_CREATE /
	// ROKSBNKCTL_WORKERS_PER_ZONE — a malformed pipeline variable must not take
	// down an otherwise-valid config.
	if v := envValue("ROKSBNKCTL_FLP_VSI_BOOT_SIZE_GB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			flpVSI(ws).BootSizeGB = n
			applied = append(applied, "bnk.flp.vsi.boot_size_gb (ROKSBNKCTL_FLP_VSI_BOOT_SIZE_GB)")
		}
	}
	// CreateVPC is mutually exclusive with VPC (see the config docs); it is a
	// plain bool, so an unparseable value must leave it false rather than pin it
	// true — false is the existing-workspace behaviour.
	if v := envValue("ROKSBNKCTL_FLP_VSI_CREATE_VPC"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			flpVSI(ws).CreateVPC = b
			applied = append(applied, "bnk.flp.vsi.create_vpc (ROKSBNKCTL_FLP_VSI_CREATE_VPC)")
		}
	}

	// FloatingIP is a *bool: unset means "the module default (true)", so it is only
	// written when the variable actually parses — an empty/garbage value must leave
	// the pointer nil rather than pinning false.
	if v := envValue("ROKSBNKCTL_FLP_VSI_FLOATING_IP"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			flpVSI(ws).FloatingIP = &b
			applied = append(applied, "bnk.flp.vsi.floating_ip (ROKSBNKCTL_FLP_VSI_FLOATING_IP)")
		}
	}

	// The two per-plane security-group CIDR lists. Comma-separated, because a form
	// field / pipeline variable is a single string; empty entries are dropped so
	// "10.0.0.0/8," does not render a bogus "" CIDR into the terraform list.
	if v := envValue("ROKSBNKCTL_FLP_VSI_MANAGEMENT_ALLOWED_CIDRS"); v != "" {
		if list := splitCommaList(v); len(list) > 0 {
			flpVSI(ws).ManagementAllowedCIDRs = list
			applied = append(applied, "bnk.flp.vsi.management_allowed_cidrs (ROKSBNKCTL_FLP_VSI_MANAGEMENT_ALLOWED_CIDRS)")
		}
	}
	if v := envValue("ROKSBNKCTL_FLP_VSI_LICENSING_ALLOWED_CIDRS"); v != "" {
		if list := splitCommaList(v); len(list) > 0 {
			flpVSI(ws).LicensingAllowedCIDRs = list
			applied = append(applied, "bnk.flp.vsi.licensing_allowed_cidrs (ROKSBNKCTL_FLP_VSI_LICENSING_ALLOWED_CIDRS)")
		}
	}

	return applied
}

// overrideSupplyChainFromEnv overlays where the FAR auth tarball + subscription
// JWT come from, and the BNK manifest version.
//
//	ROKSBNKCTL_MANIFEST_VERSION           → bnk.manifest_version
//	ROKSBNKCTL_FAR_AUTH_LOCAL_FILE        → bnk.far_auth_local_file
//	ROKSBNKCTL_SUBSCRIPTION_JWT_LOCAL_FILE → bnk.subscription_jwt_local_file
//	ROKSBNKCTL_COS_INSTANCE               → cos.instance
//	ROKSBNKCTL_COS_BUCKET                 → cos.bucket
//	ROKSBNKCTL_COS_REGION                 → cos.region
//	ROKSBNKCTL_FAR_AUTH_FILE              → bnk.far_auth_file        (object name in the bucket)
//	ROKSBNKCTL_SUBSCRIPTION_JWT_FILE      → bnk.subscription_jwt_file (object name in the bucket)
//
// Two supply chains, both reachable from env:
//
//   - LOCAL FILES — set both *_LOCAL_FILE and the render reads them directly and
//     sets use_cos_bucket=false. This is what the tested disconnected demo does,
//     and what an argv-only runner wants when its orchestrator can place the two
//     files in the workspace (BNK Forge materializes them from project secrets).
//   - COS — the cos.* + object-name vars name the bucket instead. Without them the
//     built-in defaults apply (bnk-supply-chain / bnk-artifacts /
//     f5-far-auth-key.tgz / subscription.jwt), which is a silent trap for an
//     account still on the pre-v1.22 names — hence an explicit env surface.
func overrideSupplyChainFromEnv(ws *Workspace) []string {
	var applied []string

	if v := envValue("ROKSBNKCTL_MANIFEST_VERSION"); v != "" {
		ws.BNK.ManifestVersion = v
		applied = append(applied, "bnk.manifest_version (ROKSBNKCTL_MANIFEST_VERSION)")
	}
	if v := envValue("ROKSBNKCTL_FAR_AUTH_LOCAL_FILE"); v != "" {
		ws.BNK.FarAuthLocalFile = v
		applied = append(applied, "bnk.far_auth_local_file (ROKSBNKCTL_FAR_AUTH_LOCAL_FILE)")
	}
	if v := envValue("ROKSBNKCTL_SUBSCRIPTION_JWT_LOCAL_FILE"); v != "" {
		ws.BNK.SubscriptionJWTLocalFile = v
		applied = append(applied, "bnk.subscription_jwt_local_file (ROKSBNKCTL_SUBSCRIPTION_JWT_LOCAL_FILE)")
	}
	if v := envValue("ROKSBNKCTL_FAR_AUTH_FILE"); v != "" {
		ws.BNK.FarAuthFile = v
		applied = append(applied, "bnk.far_auth_file (ROKSBNKCTL_FAR_AUTH_FILE)")
	}
	if v := envValue("ROKSBNKCTL_SUBSCRIPTION_JWT_FILE"); v != "" {
		ws.BNK.SubscriptionJWTFile = v
		applied = append(applied, "bnk.subscription_jwt_file (ROKSBNKCTL_SUBSCRIPTION_JWT_FILE)")
	}

	for _, f := range []struct {
		env   string
		label string
		set   func(*COSCfg, string)
	}{
		{"ROKSBNKCTL_COS_INSTANCE", "cos.instance", func(c *COSCfg, v string) { c.Instance = v }},
		{"ROKSBNKCTL_COS_BUCKET", "cos.bucket", func(c *COSCfg, v string) { c.Bucket = v }},
		{"ROKSBNKCTL_COS_REGION", "cos.region", func(c *COSCfg, v string) { c.Region = v }},
	} {
		if v := envValue(f.env); v != "" {
			f.set(cosCfg(ws), v)
			applied = append(applied, f.label+" ("+f.env+")")
		}
	}

	return applied
}

// flpCfg returns ws.BNK.FLP, creating it when the config never had an flp block —
// the normal CI case, where the whole proxy definition comes from env.
func flpCfg(ws *Workspace) *BNKFLPCfg {
	if ws.BNK.FLP == nil {
		ws.BNK.FLP = &BNKFLPCfg{}
	}
	return ws.BNK.FLP
}

// flpVSI returns ws.BNK.FLP.VSI, creating the intermediate blocks. Both are
// pointers, so a config that never mentioned the FLP would otherwise nil-panic
// the moment CI sets only a vsi field.
func flpVSI(ws *Workspace) *BNKFLPVSICfg {
	f := flpCfg(ws)
	if f.VSI == nil {
		f.VSI = &BNKFLPVSICfg{}
	}
	return f.VSI
}

// cosCfg returns ws.COS, creating it when the config carries no cos block.
func cosCfg(ws *Workspace) *COSCfg {
	if ws.COS == nil {
		ws.COS = &COSCfg{}
	}
	return ws.COS
}

// splitCommaList turns "a, b ,, c" into ["a","b","c"] — trimmed, empties dropped.
// A single-string env var is the only shape a pipeline/form variable has, and a
// stray trailing comma must not render an empty CIDR into terraform.
func splitCommaList(v string) []string {
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
