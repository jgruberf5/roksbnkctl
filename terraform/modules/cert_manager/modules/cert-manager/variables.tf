# ============================================================
# Cert-Manager Module Variables
# ============================================================

variable "enabled" {
  description = "Enable cert-manager module deployment"
  type        = bool
  default     = true
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
