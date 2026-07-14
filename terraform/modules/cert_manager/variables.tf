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
# cert-manager Configuration
# ============================================================

variable "cert_manager_namespace" {
  description = "Kubernetes namespace for cert-manager"
  type        = string
  default     = "cert-manager"
}

variable "cert_manager_version" {
  description = "cert-manager Helm chart version"
  type        = string
  default     = "v1.17.3"
}

# Sprint 29 air-gap mirror. Empty (default) leaves the chart's public
# image.repository untouched — byte-identical off the mirror path.
variable "cert_manager_image_repository" {
  description = "Override the cert-manager controller image repository (air-gap mirror image host). Empty leaves the chart default."
  type        = string
  default     = ""
}

variable "create_roks_cluster" {
  description = "When true, cluster is being created by roks_cluster — skip plan-time cluster credential fetch"
  type        = bool
  default     = false
}

variable "bnk_cr_mode" {
  description = "BNK install mechanism: \"kubectl\" (terraform-native helm_release + kubernetes_namespace + alekc/kubectl) or \"legacy_curl\" (null_resource local-exec baseline)."
  type        = string
  default     = "kubectl"

  validation {
    condition     = contains(["kubectl", "legacy_curl"], var.bnk_cr_mode)
    error_message = "bnk_cr_mode must be \"kubectl\" or \"legacy_curl\"."
  }
}

# Sprint 23: gate the inner cert-manager helm/null_resource bring-up so the
# second-phase apply doesn't re-manage cert_manager that the cluster phase
# already deployed. When false, the inner module's count flips to 0; its
# outputs (namespace / helm_release_version / cert_manager_ready_id) become
# null, and downstream consumers (flo/cne_instance/license) gate on `!= null`
# (flo/providers.tf:42's `"direct-apply"` fallback). The destroy provisioner
# at modules/cert-manager/main.tf — `kubectl delete namespace cert-manager` —
# CANNOT fire when the resource was never created, which is the whole point.
variable "deploy_cert_manager" {
  description = "When true, manage the cert_manager helm/null_resource bring-up. Set false in the bnk-phase override when cluster-outputs.json exists — cluster phase already provisioned cert_manager and the second phase must NOT re-manage it (would attempt kubectl delete namespace cert-manager on a subsequent bnk down)."
  type        = bool
  default     = true
}

variable "roks_cluster_dependency_id" {
  description = "roks_cluster sentinel ID — when set, defers runtime_config fetch to apply time after roks_cluster completes"
  type        = string
  default     = null
}

# Persistent dir for the kubeconfig that ibm_container_cluster_config downloads.
# Default lives under /work/.bnk/scratch (host-bind-mounted in the bnk runner) so
# the non-root container user can write it and the file survives across container
# exits. path.module would resolve to /opt/tf-project/modules/cert_manager inside
# the image — root-owned, read-only for the non-root container user, so MkdirAll
# fails. Per-module subdir keeps concurrent data sources from clobbering each other.
variable "kubeconfig_dir" {
  description = "Persistent, writable dir for ibm_container_cluster_config kubeconfig downloads. Defaults to a host-bind-mounted, module-scoped path under .bnk/scratch."
  type        = string
  default     = "/work/.bnk/scratch/kubeconfig/cert_manager"
}


variable "registry_mirror_username" {
  description = "Basic-auth username for an external registry mirror (a private Harbor/Artifactory). Empty → no mirror pull secret is created."
  type        = string
  default     = ""
}

variable "registry_mirror_password" {
  description = "Basic-auth password for an external registry mirror. When set with image_repository, cert-manager's pods pull with a dockerconfig secret built from it instead of anonymously."
  type        = string
  sensitive   = true
  default     = ""
}
