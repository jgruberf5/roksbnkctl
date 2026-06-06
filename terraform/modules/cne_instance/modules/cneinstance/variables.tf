# CNEInstance Module Variables

variable "enabled" {
  description = "Enable CNEInstance deployment"
  type        = bool
  default     = true
}

variable "bnk_cr_mode" {
  description = "BNK install mechanism: \"kubectl\" (terraform-native kubectl_manifest + wait_for) or \"legacy_curl\" (null_resource local-exec baseline)."
  type        = string
  default     = "kubectl"

  validation {
    condition     = contains(["kubectl", "legacy_curl"], var.bnk_cr_mode)
    error_message = "bnk_cr_mode must be \"kubectl\" or \"legacy_curl\"."
  }
}

variable "flo_namespace" {
  description = "Namespace for FLO deployment (where CNEInstance will be deployed)"
  type        = string
  default     = "f5-bnk"
}

variable "utils_namespace" {
  description = "Namespace for F5 utility components"
  type        = string
  default     = "f5-utils"
}

variable "cneinstance_spec" {
  description = "Full CNEInstance specification (if empty, will be generated from individual variables)"
  type        = any
  default     = {}
}

# Individual spec configuration variables (used if cneinstance_spec is not provided)
variable "f5_bigip_k8s_manifest_version" {
  description = "F5 BIG-IP Kubernetes manifest version"
  type        = string
  default     = "2.3.0-3.2598.3-0.0.170"
}

variable "cneinstance_gateway_api" {
  description = "Enable Gateway API support"
  type        = bool
  default     = true
}

variable "cneinstance_whole_cluster" {
  description = "Deploy CNEInstance to whole cluster"
  type        = bool
  default     = true
}

variable "cneinstance_logging_subsystem" {
  description = "Enable logging subsystem"
  type        = bool
  default     = false
}

variable "cneinstance_metric_subsystem" {
  description = "Enable metric subsystem"
  type        = bool
  default     = false
}

variable "cluster_issuer_name" {
  description = "Name of the cluster issuer for certificates"
  type        = string
  default     = "sample-issuer"
}

variable "cneinstance_deployment_size" {
  description = "Deployment size for CNEInstance"
  type        = string
  default     = "Small"
}

variable "far_repo_url" {
  description = "FAR repository URL"
  type        = string
  default     = ""
}

variable "cneinstance_dynamic_routing" {
  description = "Enable dynamic routing"
  type        = bool
  default     = false
}

variable "cneinstance_firewall_acl" {
  description = "Enable firewall ACL"
  type        = bool
  default     = false
}

variable "cneinstance_pseudocni" {
  description = "Enable pseudo CNI"
  type        = bool
  default     = true
}

variable "cneinstance_env_discovery" {
  description = "Enable environment discovery"
  type        = bool
  default     = false
}

variable "cneinstance_cloud_env" {
  description = "Enable cloud environment"
  type        = bool
  default     = true
}

variable "cneinstance_cloud_provider" {
  description = "Cloud provider type"
  type        = string
  default     = "ibm"
}

variable "cneinstance_vpc_name" {
  description = "VPC name for cloud environment"
  type        = string
  default     = ""
}

variable "cneinstance_cloud_region" {
  description = "Cloud region for environment"
  type        = string
  default     = ""
}

variable "cneinstance_ibm_trusted_profile_id" {
  description = "IBM Trusted Profile ID for authentication"
  type        = string
  default     = ""
}

variable "cneinstance_gslb_datacenter_name" {
  description = "GSLB datacenter name"
  type        = string
  default     = ""
}

variable "cneinstance_network_attachments" {
  description = "Network attachment definitions for CNEInstance (computed from NAD configuration)"
  type        = list(string)
  default     = []
}

variable "flo_deployment_id" {
  description = "F5 Lifecycle Operator deployment identifier (used to trigger waiting)"
  type        = string
  default     = ""
}

variable "flo_deployment_dependency" {
  description = "Explicit dependency on FLO deployment (pass the helm_release resource)"
  type        = any
  default     = null
}

variable "kube_host" {
  description = "Kubernetes API server URL (used by curl local-exec provisioners)"
  type        = string
  default     = ""
}

variable "kube_token" {
  description = "Kubernetes bearer token (used by curl local-exec provisioners)"
  type        = string
  sensitive   = true
  default     = ""
}

# ── Cloud network mapping + VLAN self-IPs (BNK 2.3 install-guide "Configuration") ──
# Zone NAMES are derived from cneinstance_cloud_region (<region>-1, -2, …) and the
# list order; only the subnet CIDRs + TMM self-IPs are configurable here. Defaults
# are the install-guide values, so a default deploy is fully wired with no input.
variable "cneinstance_network_zones" {
  description = "Per-zone subnet CIDRs + TMM self-IPs for the cloud-network-mapping ConfigMap and the external/internal F5SPKVlan CRs. List order = zone 1..N (zone name = <cneinstance_cloud_region>-<index>)."
  type = list(object({
    ext_vlan_cidr   = string
    int_vlan_cidr   = string
    int_snat_cidr   = string
    int_vip_cidr    = string
    external_selfip = string
    internal_selfip = string
  }))
  # nullable=false → a null from the parent (passed when the workspace sets no
  # zones) falls back to this install-guide default instead of erroring.
  nullable = false
  default = [
    {
      ext_vlan_cidr   = "10.155.15.0/24"
      int_vlan_cidr   = "10.254.99.0/24"
      int_snat_cidr   = "10.10.11.0/24"
      int_vip_cidr    = "10.135.15.0/24"
      external_selfip = "10.155.15.101"
      internal_selfip = "10.254.99.101"
    },
    {
      ext_vlan_cidr   = "10.156.16.0/24"
      int_vlan_cidr   = "10.254.100.0/24"
      int_snat_cidr   = "10.10.21.0/24"
      int_vip_cidr    = "10.136.16.0/24"
      external_selfip = "10.156.16.101"
      internal_selfip = "10.254.100.101"
    },
    {
      ext_vlan_cidr   = "10.157.17.0/24"
      int_vlan_cidr   = "10.254.101.0/24"
      int_snat_cidr   = "10.10.31.0/24"
      int_vip_cidr    = "10.137.17.0/24"
      external_selfip = "10.157.17.101"
      internal_selfip = "10.254.101.101"
    },
  ]
}

variable "cneinstance_vlan_external_interface" {
  description = "TMM interface id for the external F5SPKVlan (spec.interfaces)"
  type        = string
  default     = "1.1"
}

variable "cneinstance_vlan_internal_interface" {
  description = "TMM interface id for the internal F5SPKVlan (spec.interfaces)"
  type        = string
  default     = "1.2"
}

variable "cneinstance_vlan_prefixlen" {
  description = "selfip prefix length (spec.prefixlen_v4) for the F5SPKVlan CRs"
  type        = number
  default     = 24
}

variable "cneinstance_tmm_k8s_routes" {
  description = "Pod CIDR TMM routes to (advanced.tmm.env TMM_K8S_ROUTES). Default is the ROKS default pod subnet."
  type        = string
  default     = "172.17.0.0/18"
}

