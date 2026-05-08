# CNEInstance Module Variables

variable "enabled" {
  description = "Enable CNEInstance deployment"
  type        = bool
  default     = true
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

