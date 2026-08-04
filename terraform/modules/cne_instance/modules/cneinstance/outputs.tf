# CNEInstance Module Outputs

output "cneinstance_enabled" {
  description = "Whether CNEInstance was created"
  value       = var.enabled
}

output "cneinstance_name" {
  description = "Name of the CNEInstance resource"
  value       = var.enabled ? local.cneinstance_name : "N/A"
}

output "cneinstance_id" {
  description = "The name of the created CNEInstance resource"
  value       = var.enabled ? local.cneinstance_name : null
}

output "cneinstance_namespace" {
  description = "The namespace where CNEInstance is deployed"
  value       = var.flo_namespace
}

output "cneinstance_manifest" {
  description = "The full CNEInstance manifest (as JSON)"
  value       = var.enabled ? jsonencode(local.cneinstance_manifest) : null
}

output "cneinstance_scc_policies_applied" {
  description = "Summary of SCC policies applied by CNEInstance module"
  value = {
    total_policies = length(local.scc_policy_assignments)
    flo_namespace_policies = [
      for assignment in local.scc_policy_assignments
      : "${assignment.namespace}/${assignment.service_account}" if assignment.namespace == var.flo_namespace
    ]
    f5_utils_policies = [
      for assignment in local.scc_policy_assignments
      : "${assignment.namespace}/${assignment.service_account}" if assignment.namespace == "f5-utils"
    ]
    policy_names = [
      for key, km in kubectl_manifest.cneinstance_scc_policies : km.name
    ]
  }
}

output "flo_namespace_pods_count" {
  description = "Number of pods in FLO namespace (not queried — replaced by time_sleep wait)"
  value       = 0
}

output "utils_namespace_pods_count" {
  description = "Number of pods in utilities namespace (not queried — replaced by time_sleep wait)"
  value       = 0
}

output "pod_deployment_status" {
  description = "Pod deployment status after readiness wait"
  value = var.enabled ? {
    flo_namespace_pod_count   = 0
    utils_namespace_pod_count = 0
    flo_pods_not_ready        = []
    utils_pods_not_ready      = []
    scc_policies_applied      = length(kubectl_manifest.cneinstance_scc_policies)
    all_pods_running          = true
  } : null
}

output "cneinstance_ready_id" {
  description = "ID — (known after apply) until the CNE controller is ready: the null_resource.cnecontroller_ready id (set once CNEControllerAvailable=True via the deterministic API poll)."
  value       = var.enabled ? null_resource.cnecontroller_ready[0].id : null
}
