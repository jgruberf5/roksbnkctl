# ============================================================
# Root Terraform Outputs
# F5 BNK Orchestrator for existing ROKS cluster
# ============================================================

# ============================================================
# FLO Outputs
# ============================================================

output "flo_release_name" {
  description = "Name of the f5-lifecycle-operator Helm release"
  value       = module.flo.flo_release_name
}

output "flo_namespace" {
  description = "Namespace where f5-lifecycle-operator is installed"
  value       = module.flo.flo_namespace
}

output "flo_utils_namespace" {
  description = "Namespace where f5-lifecycle-operator utils are installed"
  value       = module.flo.f5_utils_namespace
}

output "flo_version" {
  description = "Installed f5-lifecycle-operator version"
  value       = module.flo.flo_version
}

output "flo_extracted_flo_version" {
  description = "FLO version extracted from f5-bigip-k8s-manifest"
  value       = module.flo.extracted_flo_version
}

output "flo_trusted_profile_id" {
  description = "IBM IAM Trusted Profile ID created for the CNE controller service account"
  value       = module.flo.trusted_profile_id
}

output "flo_pod_deployment_status" {
  description = "FLO pod deployment status"
  value       = module.flo.flo_pod_deployment_status
}

output "flo_cluster_issuer_name" {
  description = "mTLS certificate issuer name"
  value       = module.flo.cluster_issuer_name
}

output "cneinstance_network_attachments" {
  description = "Network attachments configured for CNEInstance"
  value       = module.flo.cneinstance_network_attachments
}

# Apply-time sentinel — always (known after apply) because token rotates each run.
# Pass as flo_dependency_id to cne_instance to enforce apply ordering.
output "flo_ready_id" {
  description = "Sentinel ID for apply-time ordering — (known after apply) on every apply"
  value       = null_resource.flo_ready.id
}
