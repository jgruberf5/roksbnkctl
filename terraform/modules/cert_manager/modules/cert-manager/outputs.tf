# ============================================================
# Cert-Manager Module Outputs
# ============================================================

output "namespace" {
  description = "Namespace where cert-manager is deployed"
  value       = var.enabled ? var.namespace : null
}

output "namespace_id" {
  description = "Kubernetes namespace (same as name — UID not available via local-exec)"
  value       = var.enabled ? var.namespace : null
}

output "helm_release_name" {
  description = "Name of the helm release"
  value       = var.enabled ? "cert-manager" : null
}

output "helm_release_version" {
  description = "Version of the installed helm release"
  value       = var.enabled ? var.chart_version : null
}

output "crd_ready" {
  description = "True when cert-manager is enabled and its module has been applied"
  value       = var.enabled
}

output "cert_manager_ready_id" {
  description = "ID of the time_sleep resource — (known after apply) until cert-manager is ready"
  value       = var.enabled ? time_sleep.cert_manager_ready[0].id : null
}
