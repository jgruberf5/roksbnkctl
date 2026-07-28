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

variable "flp_vsi_reach" {
  description = "How the CWC dials the proxy: private (VSI VPC IP) or floating (public floating IP)."
  type        = string
  default     = "private"
}

variable "flp_vsi_allowed_cidrs" {
  description = "Source CIDRs allowed to reach the proxy's 8443 port. Empty → the cluster VPC address space."
  type        = list(string)
  default     = []
}

# ── Cluster VPC to attach to (the CWC reaches the VSI here) ────────────────────
variable "existing_cluster_vpc_id" {
  description = "The cluster VPC id (from cluster-outputs.json) the FLP VSI joins so the CWC reaches it directly."
  type        = string
  default     = ""
}

# ── FLP image coordinates (resolved from the BNK manifest by the phase) ───────
variable "flp_image_registry" {
  description = "FAR image host prefix, e.g. repo.f5.com/images."
  type        = string
  default     = "repo.f5.com/images"
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
