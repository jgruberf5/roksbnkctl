# License Module Outputs

output "license_id" {
  description = "The name of the created License resource"
  value       = var.enabled ? "bnk-license" : null
}

output "license_namespace" {
  description = "The namespace where License CR is deployed"
  value       = var.utils_namespace
}
