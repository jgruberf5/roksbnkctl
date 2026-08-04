# ============================================================
# Root Terraform Variables
# F5 BNK Orchestrator for existing ROKS cluster
# ============================================================


# ============================================================
# IBM Cloud Variables
# ============================================================

variable "ibmcloud_api_key" {
  description = "IBM Cloud API Key"
  type        = string
  sensitive   = true
}

variable "ibmcloud_cluster_region" {
  description = "IBM Cloud region where the cluster resides"
  type        = string
  default     = "ca-tor"
}

variable "ibmcloud_resource_group" {
  description = "IBM Cloud Resource Group name (leave empty to use account default)"
  type        = string
  default     = "default"
}

variable "ibmcloud_cos_bucket_region" {
  description = "IBM Cloud region where the COS bucket is located"
  type        = string
  default     = "us-south"
}

variable "ibmcloud_cos_instance_name" {
  description = "IBM Cloud COS instance name"
  type        = string
  default     = "bnk-supply-chain"
}

variable "ibmcloud_resources_cos_bucket" {
  description = "IBM Cloud COS bucket containing the FAR auth key and JWT files"
  type        = string
  default     = "bnk-artifacts"
}

# ============================================================
# ROKs Output
# ============================================================

variable "roks_cluster_name_or_id" {
  description = "Name or ID of the existing OpenShift ROKS cluster to deploy BNK onto"
  type        = string

  validation {
    condition     = length(var.roks_cluster_name_or_id) > 0
    error_message = "roks_cluster_name_or_id cannot be empty — an existing cluster is required."
  }
}


# ============================================================
# FLO Output
# ============================================================

variable "flo_utils_namespace" {
  description = "Namespace for F5 utility components"
  type        = string
  default     = "f5-utils"
}

# ============================================================
# License Configuration
# ============================================================

variable "f5_cne_subscription_jwt_file" {
  description = "Subscription JWT filename in the COS bucket"
  type        = string
  default     = "subscription.jwt"
}

variable "use_cos_bucket" {
  description = "Download the subscription JWT from COS. false = use the injected jwt_token content (local file)."
  type        = bool
  default     = true
}

variable "jwt_token" {
  description = "Subscription/license JWT content, injected when use_cos_bucket = false. Empty on the COS path."
  type        = string
  default     = ""
  sensitive   = true
}

variable "license_mode" {
  description = "License operation mode (connected, disconnected, or f5licenseproxy)"
  type        = string
  default     = "connected"
}

variable "flp_license_server_url" {
  description = "Base URL of the in-cluster F5 License Proxy (FLP mode only)"
  type        = string
  default     = ""
}

variable "license_server_root_ca" {
  description = "PEM of the FLP root CA, written to the licenseserver-rootca Secret (FLP mode only)"
  type        = string
  default     = ""
}

variable "create_roks_cluster" {
  description = "When true, cluster is being created by roks_cluster — skip plan-time cluster credential fetch"
  type        = bool
  default     = false
}

variable "roks_cluster_dependency_id" {
  description = "roks_cluster sentinel ID — when set, defers runtime_config fetch to apply time after roks_cluster completes"
  type        = string
  default     = null
}

variable "cneinstance_dependency_id" {
  description = "cneinstance_ready_id from ws4 — when set, ensures License CRD is available before applying License CR"
  type        = string
  default     = null
}

variable "deploy_bnk" {
  description = "Deploy BIG-IP Next for Kubernetes — when false the inner license module is disabled and no License resources are created"
  type        = bool
  default     = true
}

# Persistent dir for the kubeconfig that ibm_container_cluster_config downloads.
# Default lives under /work/.bnk/scratch (host-bind-mounted in the bnk runner) so
# the non-root container user can write it and the file survives across container
# exits. path.module would resolve to /opt/tf-project/modules/license inside the
# image — root-owned, read-only for the non-root container user, so MkdirAll fails.
# Per-module subdir keeps concurrent data sources from clobbering each other.
variable "kubeconfig_dir" {
  description = "Persistent, writable dir for ibm_container_cluster_config kubeconfig downloads. Defaults to a host-bind-mounted, module-scoped path under .bnk/scratch."
  type        = string
  default     = "/work/.bnk/scratch/kubeconfig/license"
}

variable "roksbnkctl_binary" {
  description = "Absolute path to the roksbnkctl binary; the license phase invokes `roksbnkctl tfx <verb>` in place of host curl (no interpreter, so cmd.exe execs it on Windows). Empty falls back to `roksbnkctl` on PATH."
  type        = string
  default     = ""
}

variable "cluster_absent" {
  description = "True in the standalone FLP-VSI phase: no ROKS cluster exists, so all cluster data-source lookups + kube providers are skipped (count=0)."
  type        = bool
  default     = false
}
