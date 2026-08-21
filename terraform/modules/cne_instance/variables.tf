variable "bnk_line" {
  description = <<-EOT
    BNK release line driving per-release resource gating ("2.3" or "2.4").

    Derived by roksbnkctl from bnk.manifest_version and rendered as a tfvar; the
    modules gate with `count` on it rather than the tree being forked per line,
    because the 2.4 changes are additive resources and two copies of a file
    drift (PRD 18 §4).
  EOT
  type        = string
  default     = "2.3"
}

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

# Sprint 29 air-gap mirror — image host for the CNEInstance spec.registry.uri.
# Empty falls back to far_repo_url in the inner module (byte-identical default).
variable "far_image_repo_url" {
  description = "Image-pull host for the mirror (CNEInstance spec.registry.uri). Empty falls back to far_repo_url."
  type        = string
  default     = ""
}

variable "use_registry_mirror" {
  description = "Pull charts + images from the registry mirror instead of FAR. The far-secret dockerconfig is dropped; how pods then authenticate depends on the mirror: an in-cluster/ICR mirror authorizes by RBAC and needs no pull secret, while an EXTERNAL mirror (Harbor, Artifactory) gets a `mirror-secret` dockerconfig built from registry_mirror_username/password. A private mirror therefore needs no anonymous/public project."
  type        = bool
  default     = false
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

variable "f5_bigip_k8s_manifest_version" {
  description = "Version of f5-bigip-k8s-manifest chart - used by flo, cneinstance modules"
  type        = string
  default     = "2.3.0-3.2598.3-0.0.170"
}

variable "flo_trusted_profile_sa_name" {
  description = "The CNE controller service account; must match what the flo module linked."
  type        = string
  default     = ""
}

variable "flo_trusted_profile_id" {
  description = "IBM IAM Trusted Profile ID for provisioning VPC routes"
  type        = string
  default     = ""
}

variable "flo_cluster_issuer_name" {
  description = "mTLS certificate issuer name"
  type        = string
  default     = ""
}


# ============================================================
# CNEInstance Configuration
# ============================================================

variable "cneinstance_deployment_size" {
  description = "Deployment size for CNEInstance (Tiny, Small, Medium, Large). Tiny is what the BNK 2.4 install guide uses."
  type        = string
  default     = "Small"
}

variable "cneinstance_gtm_url" {
  description = "BIG-IP DNS / GTM management URL the CNE controller registers its GSLB datacenter with (#51). Empty disables GTM entirely."
  type        = string
  default     = ""
}

variable "cneinstance_gtm_username" {
  description = "Username for the GTM at cneinstance_gtm_url."
  type        = string
  default     = ""
}

variable "cneinstance_gtm_password" {
  description = "Password for the GTM at cneinstance_gtm_url."
  type        = string
  default     = ""
  sensitive   = true
}

variable "cneinstance_gslb_datacenter_name" {
  description = "GSLB datacenter name for CNEInstance (optional)"
  type        = string
  default     = ""
}

variable "cneinstance_network_attachments" {
  description = "The Multus Network Attachment Definitions for the CNEInstance TMM deployments"
  type        = list(string)
  default     = ["ens3-ipvlan-l2", "macvlan-conf"]
}

# Per-zone subnet CIDRs + TMM self-IPs for the cloud-network-mapping ConfigMap +
# F5SPKVlan CRs. Empty (default) → the inner cneinstance module's install-guide
# defaults apply (the parent passes null below so that default is used).
variable "cneinstance_network_zones" {
  description = "Per-zone subnet CIDRs + TMM self-IPs (empty = use the install-guide defaults)"
  type = list(object({
    ext_vlan_cidr   = string
    int_vlan_cidr   = string
    int_snat_cidr   = string
    int_vip_cidr    = string
    external_selfip = string
    internal_selfip = string
  }))
  default = []
}

variable "cneinstance_vlan_prefixlen_external" {
  description = "External VLAN self-IP prefix length; 0 inherits cneinstance_vlan_prefixlen."
  type        = number
  default     = 0
}

variable "cneinstance_vlan_prefixlen_internal" {
  description = "Internal VLAN self-IP prefix length; 0 inherits cneinstance_vlan_prefixlen."
  type        = number
  default     = 0
}

variable "cneinstance_vlan_prefixlen" {
  description = "TMM self-IP prefix length (spec.prefixlen_v4) for the external/internal F5SPKVlan CRs"
  type        = number
  default     = 24
}

variable "cneinstance_tmm_k8s_routes" {
  description = "Pod CIDR TMM routes to (advanced.tmm.env TMM_K8S_ROUTES). Default is the ROKS default pod subnet."
  type        = string
  default     = "172.17.0.0/18"
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

variable "flo_dependency_id" {
  description = "flo_ready sentinel ID — pass module.flo.flo_ready_id to defer cne_instance until flo completes and CRDs are registered"
  type        = string
  default     = null
}

variable "deploy_bnk" {
  description = "Deploy BIG-IP Next for Kubernetes — when false the inner cneinstance module is disabled and no CNEInstance resources are created"
  type        = bool
  default     = true
}

# Persistent dir for the kubeconfig that ibm_container_cluster_config downloads.
# Default lives under /work/.bnk/scratch (host-bind-mounted in the bnk runner) so
# the non-root container user can write it and the file survives across container
# exits. path.module would resolve to /opt/tf-project/modules/cne_instance inside
# the image — root-owned, read-only for the non-root container user, so MkdirAll
# fails. Per-module subdir keeps concurrent data sources from clobbering each other.
variable "kubeconfig_dir" {
  description = "Persistent, writable dir for ibm_container_cluster_config kubeconfig downloads. Defaults to a host-bind-mounted, module-scoped path under .bnk/scratch."
  type        = string
  default     = "/work/.bnk/scratch/kubeconfig/cne_instance"
}
variable "registry_mirror_username" {
  description = "Basic-auth username for an external registry mirror (private Harbor/Artifactory)."
  type        = string
  default     = ""
}

variable "registry_mirror_password" {
  description = "Basic-auth password for an external registry mirror. When set with use_registry_mirror, the CNEInstance references the mirror-secret pull secret instead of pulling anonymously."
  type        = string
  sensitive   = true
  default     = ""
}

variable "roksbnkctl_binary" {
  description = "Absolute path to the roksbnkctl binary; the CNE-instance phase invokes `roksbnkctl tfx <verb>` in place of host curl (no interpreter, so cmd.exe execs it on Windows). Empty falls back to `roksbnkctl` on PATH."
  type        = string
  default     = ""
}

variable "cluster_absent" {
  description = "True in the standalone FLP-VSI phase: no ROKS cluster exists, so all cluster data-source lookups + kube providers are skipped (count=0)."
  type        = bool
  default     = false
}
