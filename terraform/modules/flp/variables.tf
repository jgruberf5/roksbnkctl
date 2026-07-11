# ── gating + cluster wiring ───────────────────────────────────────────────────

variable "deploy_flp" {
  description = "Deploy the F5 License Proxy. Off in every other phase's override; on only for `flp up`. When false the module is a complete no-op."
  type        = bool
  default     = false
}

variable "create_roks_cluster" {
  description = "When true the cluster is being created in this same apply (FLP never runs then); gates the plan-time cluster-config fetch."
  type        = bool
  default     = false
}

variable "roks_cluster_name_or_id" {
  description = "Name or id of the existing ROKS cluster to deploy the FLP into."
  type        = string
  default     = ""
}

variable "roks_cluster_dependency_id" {
  description = "Dependency handle from the cluster phase, deferring apply until the cluster is ready."
  type        = any
  default     = null
}

variable "kubeconfig_dir" {
  description = "Directory the ibm_container_cluster_config data source writes the kubeconfig into."
  type        = string
  default     = ""
}

# ── IBM Cloud / COS (FAR auth + subscription JWT) ────────────────────────────

variable "ibmcloud_api_key" {
  description = "IBM Cloud API key (COS/IAM auth + provider config)."
  type        = string
  sensitive   = true
  default     = ""
}

variable "ibmcloud_cluster_region" {
  description = "Region of the ROKS cluster (ibm provider)."
  type        = string
  default     = ""
}

variable "ibmcloud_resource_group" {
  description = "Resource group containing the COS instance (empty = default group)."
  type        = string
  default     = ""
}

variable "ibmcloud_cos_instance_name" {
  description = "COS service instance holding the FAR auth tarball + subscription JWT."
  type        = string
  default     = ""
}

variable "ibmcloud_resources_cos_bucket" {
  description = "COS bucket holding the FAR auth tarball + subscription JWT."
  type        = string
  default     = ""
}

variable "ibmcloud_cos_bucket_region" {
  description = "Region of the COS bucket."
  type        = string
  default     = ""
}

variable "f5_cne_far_auth_file" {
  description = "FAR auth tarball object key in the COS bucket (the _json_key_base64 SA lives inside)."
  type        = string
  default     = "f5-far-auth-key.tgz"
}

variable "f5_cne_subscription_jwt_file" {
  description = "Subscription JWT object key in the COS bucket — seeds flp-jwt-secret."
  type        = string
  default     = "trial.jwt"
}

variable "scratch_dir" {
  description = "Working directory for the FAR-auth download/extract."
  type        = string
  default     = "/tmp/roksbnkctl-flp"
}

# ── registry / mirror (identical contract to the BNK install) ────────────────

variable "far_repo_url" {
  description = "FAR registry host (fallback when no mirror)."
  type        = string
  default     = "repo.f5.com"
}

variable "far_chart_repo_url" {
  description = "Mirror host for chart pulls (empty → coalesces to far_repo_url)."
  type        = string
  default     = ""
}

variable "far_image_repo_url" {
  description = "Mirror host for image pulls (empty → coalesces to far_repo_url)."
  type        = string
  default     = ""
}

variable "use_registry_mirror" {
  description = "When true, pull chart+images from the mirror and drop the FAR dockerconfig secret (RBAC handles pulls), matching the BNK install."
  type        = bool
  default     = false
}

# ── FLP specifics ─────────────────────────────────────────────────────────────

variable "flp_namespace" {
  description = "Namespace to install the F5 License Proxy into."
  type        = string
  default     = "f5-license-proxy"
}

variable "flp_chart_version" {
  description = "f5-license-proxy chart version. Empty → the chart's latest in the registry."
  type        = string
  default     = ""
}
