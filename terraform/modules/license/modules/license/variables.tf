# License Module Variables

variable "enabled" {
  description = "Enable License CR deployment"
  type        = bool
  default     = true
}

variable "utils_namespace" {
  description = "Namespace for F5 utility components (where License CR will be deployed)"
  type        = string
  default     = "f5-utils"
}

variable "jwt_token" {
  description = "JWT token for F5 license authentication"
  type        = string
  sensitive   = true
}

variable "license_mode" {
  description = "License operation mode (connected or disconnected)"
  type        = string
  default     = "connected"

  validation {
    condition     = contains(["connected", "disconnected"], var.license_mode)
    error_message = "license_mode must be either 'connected' or 'disconnected'."
  }
}

variable "cneinstance_dependency" {
  description = "Explicit dependency on CNEInstance deployment (ensures License CRD is available)"
  type        = any
  default     = null
}

variable "use_cos_bucket" {
  description = "Fetch JWT token from an IBM COS bucket instead of passing it directly"
  type        = bool
  default     = false
}

variable "ibmcloud_api_key" {
  description = "IBM Cloud API key used to authenticate COS requests"
  type        = string
  sensitive   = true
  default     = ""
}

variable "ibmcloud_cos_bucket_region" {
  description = "Region where the COS bucket is located"
  type        = string
  default     = ""
}

variable "ibmcloud_resource_group" {
  description = "IBM Cloud resource group containing the COS instance (empty = default group)"
  type        = string
  default     = ""
}

variable "ibmcloud_cos_instance_name" {
  description = "Name of the IBM Cloud Object Storage service instance"
  type        = string
  default     = ""
}

variable "ibmcloud_resources_cos_bucket" {
  description = "Name of the COS bucket that holds the JWT file"
  type        = string
  default     = ""
}

variable "f5_cne_subscription_jwt_file" {
  description = "Object key (filename) of the JWT file within the COS bucket"
  type        = string
  default     = ""
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
