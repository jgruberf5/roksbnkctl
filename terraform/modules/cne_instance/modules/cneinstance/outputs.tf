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
  # KEPT sensitive after #227 removed the GTM password this used to carry.
  #
  # The spec no longer holds a credential VALUE -- what is left is names,
  # namespaces and env whose values are not secret. The flag stays anyway: it
  # costs nothing, and the failure modes are asymmetric. Marking a
  # non-credential sensitive hides it from `terraform output`; unmarking one that
  # turns out to carry a credential puts it in a log. If a secret is ever added
  # to the spec, this is already right.
  sensitive = true
}

output "cneinstance_scc_policies_applied" {
  description = "Summary of SCC policies applied by CNEInstance module"
  value = {
    # distinct(), because that is what for_each actually consumes — counting the
    # raw list over-reports by one whenever the namespaces are collapsed.
    total_policies = length(distinct(local.scc_policy_assignments))
    # distinct() here too, and for a second reason beyond de-duplication: when
    # the namespaces are COLLAPSED both filters match every assignment, so the
    # two breakdowns each reported all 19 while total_policies reported the real
    # count. A status output that disagrees with itself is worse than one that is
    # merely coarse — it invites the reader to trust the wrong half.
    flo_namespace_policies = [
      for assignment in distinct(local.scc_policy_assignments)
      : "${assignment.namespace}/${assignment.service_account}" if assignment.namespace == var.flo_namespace
    ]
    # Empty when the namespaces are collapsed, which is the honest answer: there
    # is no separate utils namespace to have applied anything to.
    f5_utils_policies = [
      for assignment in distinct(local.scc_policy_assignments)
      : "${assignment.namespace}/${assignment.service_account}"
      if assignment.namespace == var.utils_namespace && var.utils_namespace != var.flo_namespace
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
