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

# Sprint 29 air-gap mirror. Empty (default) → no image.repository override is
# emitted, so the chart's public default stands (byte-identical).
variable "image_repository" {
  description = "Air-gap mirror image HOST (e.g. image-registry.openshift-image-registry.svc:5000). When set, every cert-manager component image (controller/webhook/cainjector/startupapicheck/acmesolver) is redirected to <host>/jetstack/cert-manager-<comp>. Empty leaves the chart defaults."
  type        = string
  default     = ""
}

variable "timeout" {
  description = "Timeout for helm release (in seconds). 600s (not 300) gives an air-gapped cluster margin: cert-manager's images pull from a private mirror over the Transit Gateway and the startupapicheck hook must then issue a test Certificate, which can exceed 5 min on a cold cluster."
  type        = number
  default     = 600
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
