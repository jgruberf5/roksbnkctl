# ============================================================
# Cert-Manager Module Variables
# ============================================================

variable "enabled" {
  description = "Enable cert-manager module deployment"
  type        = bool
  default     = true
}

variable "bnk_cr_mode" {
  description = "BNK install mechanism: \"kubectl\" (terraform-native helm_release + kubernetes_namespace) or \"legacy_curl\" (null_resource local-exec baseline)."
  type        = string
  default     = "kubectl"

  validation {
    condition     = contains(["kubectl", "legacy_curl"], var.bnk_cr_mode)
    error_message = "bnk_cr_mode must be \"kubectl\" or \"legacy_curl\"."
  }
}

variable "namespace" {
  description = "Namespace for cert-manager"
  type        = string
  default     = "cert-manager"
}

variable "chart_version" {
  description = "Helm chart version for cert-manager"
  type        = string
  default     = "v1.13.0"
}

variable "chart_repository" {
  description = "Helm chart repository URL"
  type        = string
  default     = "https://charts.jetstack.io"
}

# Sprint 29 air-gap mirror. Empty (default) → no image.repository override is
# emitted, so the chart's public default stands (byte-identical).
variable "image_repository" {
  description = "Air-gap mirror image HOST (e.g. image-registry.openshift-image-registry.svc:5000). When set, every cert-manager component image (controller/webhook/cainjector/startupapicheck/acmesolver) is redirected to <host>/jetstack/cert-manager-<comp>. Empty leaves the chart defaults."
  type        = string
  default     = ""
}

variable "wait_for_deployment" {
  description = "Wait for cert-manager deployment to be ready"
  type        = bool
  default     = true
}

variable "timeout" {
  description = "Timeout for helm release (in seconds)"
  type        = number
  default     = 300
}

variable "post_deployment_delay" {
  description = "Delay after cert-manager deployment (in seconds) to ensure CRDs are registered"
  type        = number
  default     = 30
}

variable "kube_host" {
  description = "Kubernetes API server URL (used by kubectl/helm local-exec provisioners)"
  type        = string
  default     = ""
}

variable "kube_token" {
  description = "Kubernetes bearer token (used by kubectl/helm local-exec provisioners)"
  type        = string
  sensitive   = true
  default     = ""
}
