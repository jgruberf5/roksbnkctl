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

# ============================================================
# Cluster Inputs
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
# FAR / Registry Configuration
# ============================================================

variable "far_repo_url" {
  description = "FAR Repository URL for Docker and Helm registry"
  type        = string
  default     = "repo.f5.com"
}

# Sprint 29 air-gap mirror — chart/image host split. Both empty → fall back
# to far_repo_url in the inner module's locals (byte-identical default).
variable "far_chart_repo_url" {
  description = "Chart-pull host for the mirror (helm_release repository + manifest pull). Empty falls back to far_repo_url."
  type        = string
  default     = ""
}

variable "far_image_repo_url" {
  description = "Image-pull host for the mirror (image.repository). Empty falls back to far_repo_url."
  type        = string
  default     = ""
}

variable "use_registry_mirror" {
  description = "Pull charts + images from the registry mirror instead of FAR. The far-secret dockerconfig is dropped; how pods then authenticate depends on the mirror: an in-cluster/ICR mirror authorizes by RBAC and needs no pull secret, while an EXTERNAL mirror (Harbor, Artifactory) gets a `mirror-secret` dockerconfig built from registry_mirror_username/password. A private mirror therefore needs no anonymous/public project."
  type        = bool
  default     = false
}

variable "registry_mirror_username" {
  description = "Basic-auth username for an external registry mirror (Harbor). Empty → in-cluster/ICR mirror."
  type        = string
  default     = ""
}

variable "registry_mirror_password" {
  description = "Basic-auth password/token for an external registry mirror; chart pulls authenticate with it when set."
  type        = string
  sensitive   = true
  default     = ""
}

variable "f5_bigip_k8s_manifest_version" {
  description = "Version of the f5-bigip-k8s-manifest chart (FLO/CIS versions are extracted from this)"
  type        = string
  default     = "2.3.0-3.2598.3-0.0.170"
}

# ============================================================
# COS Bucket Configuration
# Optional — fetch FAR auth key and JWT from IBM Cloud Object Storage
# ============================================================

variable "use_cos_bucket" {
  description = "Fetch FAR auth key and JWT from IBM Cloud Object Storage instead of local variables"
  type        = bool
  default     = true
}

variable "far_service_account_b64" {
  description = "FAR _json_key_base64 service account (base64 of the .json), injected when use_cos_bucket = false"
  type        = string
  default     = ""
  sensitive   = true
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

variable "f5_cne_far_auth_file" {
  description = "FAR auth key filename in the COS bucket (.tgz)"
  type        = string
  default     = "f5-far-auth-key.tgz"
}

variable "f5_cne_subscription_jwt_file" {
  description = "Subscription JWT filename in the COS bucket"
  type        = string
  default     = "subscription.jwt"
}

# ============================================================
# FLO Namespace Configuration
# ============================================================

variable "flo_namespace" {
  description = "Namespace for F5 Lifecycle Operator"
  type        = string
  default     = "f5-bnk"
}

variable "flo_utils_namespace" {
  description = "Namespace for F5 utility components"
  type        = string
  default     = "f5-utils"
}

variable "cert_manager_namespace" {
  description = "Kubernetes namespace for cert-manager - used by cert-manager, flo modules"
  type        = string
  default     = "cert-manager"
}

# ============================================================
# BIG-IP CIS Configuration
# ============================================================

variable "bigip_username" {
  description = "BIG-IP username for CIS controller login"
  type        = string
  default     = "admin"
}

variable "bigip_password" {
  description = "BIG-IP password for CIS controller login"
  type        = string
  default     = "admin"
  sensitive   = true
}

variable "bigip_url" {
  description = "BIG-IP URL for CIS controller login"
  type        = string
  default     = "https://192.168.1.245"
}

variable "create_roks_cluster" {
  description = "When true, cluster is being created by roks_cluster — skip plan-time cluster credential fetch"
  type        = bool
  default     = false
}

variable "bnk_cr_mode" {
  description = "BNK install mechanism: \"kubectl\" (terraform-native helm_release + kubernetes_* + alekc/kubectl) or \"legacy_curl\" (null_resource local-exec baseline)."
  type        = string
  default     = "kubectl"

  validation {
    condition     = contains(["kubectl", "legacy_curl"], var.bnk_cr_mode)
    error_message = "bnk_cr_mode must be \"kubectl\" or \"legacy_curl\"."
  }
}

variable "roks_cluster_dependency_id" {
  description = "roks_cluster sentinel ID — when set, defers runtime_config fetch to apply time after roks_cluster completes"
  type        = string
  default     = null
}

variable "cert_manager_dependency_id" {
  description = "cert_manager ready sentinel ID — when set, blocks flo inner module until cert-manager CRDs are available"
  type        = string
  default     = null
}

variable "deploy_bnk" {
  description = "Deploy BIG-IP Next for Kubernetes — when false the inner flo module is disabled and no FLO resources are created"
  type        = bool
  default     = true
}

# Persistent dir for the kubeconfig that ibm_container_cluster_config downloads.
# Default lives under /work/.bnk/scratch (host-bind-mounted in the bnk runner) so
# the non-root container user can write it and the file survives across container
# exits. path.module would resolve to /opt/tf-project/modules/flo inside the
# image — root-owned, read-only for the non-root container user, so MkdirAll fails.
# Per-module subdir keeps concurrent data sources from clobbering each other.
variable "kubeconfig_dir" {
  description = "Persistent, writable dir for ibm_container_cluster_config kubeconfig downloads. Defaults to a host-bind-mounted, module-scoped path under .bnk/scratch."
  type        = string
  default     = "/work/.bnk/scratch/kubeconfig/flo"
}

# Persistent scratch directory for cross-apply artifacts (FAR auth tarball,
# extracted JSON read by data.local_file on later applies, f5-manifest helm
# extraction). Inner flo module declares scratch_dir + manifest_download_dir
# separately; this outer module collapses them into one knob and derives
# manifest_download_dir as ${scratch_dir}/f5-manifest.
#
# Default targets the bnk runner image's bind-mount layout (/work is the
# host cwd inside the container). Consumers running terraform directly on
# a host (e.g., roksbnkctl) override this to a writable path.
variable "scratch_dir" {
  description = "Persistent scratch directory for FAR/manifest cross-apply artifacts. Default is the bnk runner image's /work mount."
  type        = string
  default     = "/work/.bnk/scratch"
}

variable "roksbnkctl_binary" {
  description = "Absolute path to the roksbnkctl binary; the FLO phase invokes `roksbnkctl tfx <verb>` in place of host curl/tar (no interpreter, so cmd.exe execs it on Windows). Empty falls back to `roksbnkctl` on PATH."
  type        = string
  default     = ""
}

variable "helm_registry_config" {
  description = "Path to the helm registry config file (HELM_REGISTRY_CONFIG). When set, roksbnkctl writes the OCI pull credential inline here and the helm_release resources drop repository_username/password, so the provider reads the auth instead of doing a login-and-store (which fails on Windows credential helpers). Empty = direct terraform apply, provider does its own OCI login."
  type        = string
  default     = ""
}
