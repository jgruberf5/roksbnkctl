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
  description = "License operation mode: connected, disconnected, or f5licenseproxy (FLP)."
  type        = string
  default     = "connected"

  validation {
    condition     = contains(["connected", "disconnected", "f5licenseproxy"], var.license_mode)
    error_message = "license_mode must be 'connected', 'disconnected', or 'f5licenseproxy'."
  }
}

# ── F5 License Proxy (FLP) mode ───────────────────────────────────────────────
# Only consumed when license_mode == "f5licenseproxy". In FLP mode the License CR
# additionally requires the three teem*Url endpoints (the in-cluster FLP service)
# and licenseProxyServerRootCaPath (a file path). The FLP root CA is delivered to
# the CWC pod via the `licenseserver-rootca` Secret, which CWC mounts at that path.

variable "flp_license_server_url" {
  description = "Base URL of the in-cluster F5 License Proxy service, e.g. https://f5-license-proxy.<ns>.svc.cluster.local:8443. Required when license_mode=f5licenseproxy."
  type        = string
  default     = ""
}

variable "license_server_root_ca" {
  description = "PEM of the FLP root CA. Written into the `licenseserver-rootca` Secret so CWC trusts the FLP. Required when license_mode=f5licenseproxy."
  type        = string
  default     = ""
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

variable "roksbnkctl_binary" {
  description = "Absolute path to the roksbnkctl binary; the license phase invokes `roksbnkctl tfx <verb>` in place of host curl (no interpreter, so cmd.exe execs it on Windows). Empty falls back to `roksbnkctl` on PATH."
  type        = string
  default     = ""
}
