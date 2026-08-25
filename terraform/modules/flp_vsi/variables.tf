variable "deploy_flp_vsi" {
  description = "Master toggle — when false the whole module is a no-op (count=0). Set true only by the FLP phase in mode: vsi."
  type        = bool
  default     = false
}

variable "ibmcloud_api_key" {
  description = "IBM Cloud API key (provider + COS/IAM REST auth)."
  type        = string
  sensitive   = true
}

variable "ibmcloud_cluster_region" {
  description = "Region of the ROKS cluster / where the FLP VSI is provisioned."
  type        = string
}

variable "ibmcloud_resource_group" {
  description = "Resource group for the VSI + network (empty = account default)."
  type        = string
  default     = ""
}

# ── FLP VSI shape ─────────────────────────────────────────────────────────────
variable "flp_vsi_profile" {
  description = "VSI instance profile (>= 4 vCPU / 8 GB)."
  type        = string
  default     = "bx2-4x16"
}

variable "flp_vsi_zone" {
  description = "Zone for the VSI (e.g. us-south-1). Empty → <region>-1."
  type        = string
  default     = ""
}

variable "flp_vsi_boot_size_gb" {
  description = "Boot volume size in GB (>= 80)."
  type        = number
  default     = 100
}

variable "flp_vsi_floating_ip" {
  description = "Attach an operator floating IP to the FLP VSI for remote management — running `roksbnkctl flp status` and reaching the :80 web UI + :8443 proxy from another machine. NOT the CWC endpoint (the cluster always reaches the proxy privately). The floating IP is added to the leaf-cert SAN; reachability is still gated by flp_vsi_allowed_cidrs. Default true."
  type        = bool
  default     = true
}

variable "flp_vsi_management_allowed_cidrs" {
  description = "Source CIDRs allowed to reach the :80 flp-status web UI (read-only status). Empty → 0.0.0.0/0 (open — the page carries no secrets)."
  type        = list(string)
  default     = []
}
variable "flp_vsi_licensing_allowed_cidrs" {
  description = "Source CIDRs allowed to reach the :8443 licensing proxy (and :22 SSH). Empty → the RFC-1918 private ranges (the cluster reaches the proxy privately over the VPC / Transit Gateway)."
  type        = list(string)
  default     = []
}
variable "flp_vsi_allowed_cidrs" {
  description = "DEPRECATED — legacy single list. When set, seeds BOTH flp_vsi_management_allowed_cidrs and flp_vsi_licensing_allowed_cidrs. Prefer the two per-plane variables. Empty → the per-plane defaults apply."
  type        = list(string)
  default     = []
}

# ── Cluster VPC to attach to (the CWC reaches the VSI here) ────────────────────
variable "existing_cluster_vpc_id" {
  description = "The cluster VPC id (from cluster-outputs.json) the FLP VSI joins so the CWC reaches it directly."
  type        = string
  default     = ""
}

# ── Resource naming ──────────────────────────────────────────────────────────
# Every resource below used to be named with a LITERAL — "flp-vsi",
# "flp-vsi-subnet", "flp-vsi-sg" and so on — ignoring the workspace prefix that
# the rest of the tool honours. Two consequences, both real (#88):
#
#   1. A second `flp up --mode vsi` in the same region collided on the instance
#      name whatever `prefix:` the workspace declared, so ONE account could hold
#      only one standalone proxy. That rules out the shared-licensing topology
#      the FLP exists for — a proxy per environment, several per account.
#   2. `cleanup` sweeps orphans by `<prefix>-*`. "flp-vsi" matches no workspace
#      prefix, so a failed `flp down` stranded a VSI, floating IP, subnet,
#      security group and boot volume that the sweep could never find.
#
# EMPTY IS THE DEFAULT, and deliberately so: renaming a resource forces
# terraform to REPLACE it, so defaulting this to the workspace prefix would
# destroy and rebuild every running proxy on the next apply. Existing
# deployments keep their names until an operator opts in.
variable "flp_vsi_name_prefix" {
  description = "Prefix for the FLP VSI's resource names, e.g. \"bnk-ci\" yields bnk-ci-flp-vsi. Empty (the default) keeps the legacy unprefixed names, so an existing proxy is not replaced on upgrade. Set it to run more than one standalone FLP in an account, and to make the resources visible to `roksbnkctl cleanup`."
  type        = string
  default     = ""
}

# ── FAR coordinates ───────────────────────────────────────────────────────────
# This module used to spell the FAR host as the literal "repo.f5.com" in its two
# chart pulls, and default its image host to "repo.f5.com/images" with no way to
# override either. Every OTHER consumer of FAR (flo, cne_instance, flp) takes
# far_repo_url, so a workspace that set bnk.far_repo_url got the alternate host
# everywhere EXCEPT the standalone VSI path — which then pulled from the wrong
# registry and failed on a chart that was never there. Non-production FAR hosts
# (an EA repo, for one) are exactly the case that surfaces it.
variable "far_repo_url" {
  description = "FAR repository host for the FLP chart pulls. Same value as the root far_repo_url."
  type        = string
  default     = "repo.f5.com"
}

# ── FLP image coordinates (resolved from the BNK manifest by the phase) ───────
variable "flp_image_registry" {
  description = "FAR image host prefix. Empty (the default) derives <far_repo_url>/images, so the image host follows the chart host instead of pinning repo.f5.com independently."
  type        = string
  default     = ""
}

variable "f5_bigip_k8s_manifest_version" {
  description = "BNK manifest version — the f5-license-proxy chart/image tag is resolved from it (like the helm path) when flp_chart_version is empty."
  type        = string
}

variable "flp_chart_version" {
  description = "Pin the f5-license-proxy chart/image tag. Empty → resolved from the BNK manifest."
  type        = string
  default     = ""
}

variable "flp_vault_image_tag" {
  description = "Tag for the vault image."
  type        = string
  default     = "2.0.0"
}

# ── F5 licensing endpoints (prod defaults) ────────────────────────────────────
variable "f5_cert_url" {
  type    = string
  default = "https://product.apis.f5.com/ee/v1"
}
variable "f5_entitlement_url" {
  type    = string
  default = "https://product-s.apis.f5.com/ee/v1"
}
variable "f5_initial_config_url" {
  type    = string
  default = "https://product-s.apis.f5.com/ee/v1"
}
variable "mode_of_operation" {
  type    = string
  default = "connected"
}

# ── optional egress forward proxy ─────────────────────────────────────────────
variable "flp_forward_proxy_host" {
  type    = string
  default = ""
}
variable "flp_forward_proxy_port" {
  type    = number
  default = 0
}
variable "flp_forward_proxy_protocol" {
  type    = string
  default = "http"
}

# ── COS coordinates for the FAR auth tarball + subscription JWT ────────────────
variable "ibmcloud_cos_instance_name" {
  type    = string
  default = "bnk-supply-chain"
}
variable "ibmcloud_resources_cos_bucket" {
  type    = string
  default = "bnk-artifacts"
}
variable "ibmcloud_cos_bucket_region" {
  type    = string
  default = "us-south"
}
variable "f5_cne_far_auth_file" {
  type    = string
  default = "f5-far-auth-key.tgz"
}
variable "f5_cne_subscription_jwt_file" {
  type    = string
  default = "subscription.jwt"
}

variable "scratch_dir" {
  description = "Working dir for the FAR-auth download/extract + chart pull."
  type        = string
  default     = "/tmp/flp-vsi-scratch"
}

variable "flp_prod_jwks_b64" {
  description = "Base64 of F5's public prod_jwks.txt (JWT signature verification). Supplied by the FLP phase, which extracts it from the f5-license-proxy chart."
  type        = string
  default     = ""
}

variable "roksbnkctl_binary" {
  description = "Absolute path to the roksbnkctl binary; the FLP-VSI phase invokes `roksbnkctl tfx <verb>` in place of host curl/tar (no interpreter, so cmd.exe execs it on Windows). Empty falls back to `roksbnkctl` on PATH."
  type        = string
  default     = ""
}

variable "use_cos_bucket" {
  description = "True: pull the FAR tarball + subscription JWT from COS. False (disconnected): use far_service_account_b64 + f5_cne_subscription_jwt supplied by the root from local files."
  type        = bool
  default     = true
}

variable "far_service_account_b64" {
  description = "Base64 FAR service account (from bnk.far_auth_local_file), used when use_cos_bucket=false."
  type        = string
  default     = ""
}

variable "f5_cne_subscription_jwt" {
  description = "Subscription JWT contents (from bnk.subscription_jwt_local_file), used when use_cos_bucket=false."
  type        = string
  default     = ""
}

variable "flp_vsi_ssh_key" {
  description = "Existing VPC SSH key name to attach to the FLP VSI (operator access). Empty = no key."
  type        = string
  default     = ""
}

# ── BYO / build-your-own network for the proxy (#60) ─────────────────────────
variable "flp_vsi_create_vpc" {
  description = "Build the proxy its own VPC, address prefix and public gateway instead of placing it in one that already exists. Default false keeps existing workspaces byte-identical."
  type        = bool
  default     = false
}

variable "flp_vsi_vpc_name" {
  description = "Name for the VPC created when flp_vsi_create_vpc = true. Empty uses flp-vsi-vpc."
  type        = string
  default     = ""
}

variable "flp_vsi_subnet_cidr" {
  description = "Address prefix for the VPC created when flp_vsi_create_vpc = true. Must not overlap anything the consuming clusters can already route to."
  type        = string
  default     = "10.250.0.0/24"
}
