# ============================================================
# Root Terraform Outputs
# F5 BNK Orchestrator for existing ROKS cluster
# ============================================================

# ============================================================
# cert-manager Outputs
# ============================================================

output "cert_manager_namespace" {
  description = "Namespace where cert-manager is deployed"
  value       = module.cert_manager.namespace
}

output "cert_manager_version" {
  description = "Installed cert-manager Helm chart version"
  value       = module.cert_manager.helm_release_version
}

output "cert_manager_ready_id" {
  description = "ID of cert-manager ready time_sleep — (known after apply) until cert-manager is fully deployed"
  value       = module.cert_manager.cert_manager_ready_id
}
