output "nad_crds_installed" {
  description = "NetworkAttachmentDefinition CRDs installation status"
  value       = "Installed from k8s-network-plumbing-wg"
}

output "nad_name" {
  description = "Name of the NetworkAttachmentDefinition resource"
  value       = local.nad_name_computed
}

output "nad_cni_type" {
  description = "CNI type used for NAD"
  value       = var.nad_cni_type
}

output "nad_interface" {
  description = "Network interface used for NAD"
  value       = var.nad_interface_name
}

output "cluster_issuers" {
  description = "List of ClusterIssuers created"
  value = [
    "selfsigned-cluster-issuer",
    "sample-issuer"
  ]
}

output "ca_certificate_name" {
  description = "Name of the CA certificate"
  value       = "arm-ca"
}

output "ca_secret_name" {
  description = "Name of the secret containing the CA certificate"
  value       = "arm-ca"
}

output "flo_release_name" {
  description = "Name of the f5-lifecycle-operator helm release"
  value       = var.enabled ? "flo" : null
}

output "flo_namespace" {
  description = "Namespace where f5-lifecycle-operator is installed"
  value       = var.enabled ? var.flo_namespace : null
}

output "flo_version" {
  description = "Version of f5-lifecycle-operator installed (extracted from manifest)"
  value       = var.enabled ? try(data.external.versions[0].result.flo, null) : null
}

output "f5_utils_namespace" {
  description = "Namespace for F5 utility components"
  value       = var.enabled ? var.utils_namespace : null
}

output "f5_bigip_k8s_manifest_version" {
  description = "Version of f5-bigip-k8s-manifest used"
  value       = var.f5_bigip_k8s_manifest_version
}

output "extracted_flo_version" {
  description = "FLO version extracted from f5-bigip-k8s-manifest"
  value       = var.enabled ? try(data.external.versions[0].result.flo, null) : null
}

output "extracted_cis_version" {
  description = "CIS version extracted from f5-bigip-k8s-manifest"
  value       = var.enabled ? try(data.external.versions[0].result.cis, null) : null
}

output "manifest_download_dir" {
  description = "Directory where manifest chart was downloaded and extracted"
  value       = var.manifest_download_dir
}

output "cneinstance_network_attachments" {
  description = "Network attachments configured for CNEInstance"
  value       = var.enabled ? local.cneinstance_network_attachments : []
}

output "nodes_labeled" {
  description = "All nodes have been labeled with app=f5-tmm"
  value       = "Applied to all cluster nodes"
}

output "cluster_issuer_name" {
  description = "Name of the cluster issuer"
  value       = var.cluster_issuer_name
}

output "flo_scc_policy_applied" {
  description = "Whether privileged SCC policy was applied to flo-f5-lifecycle-operator service account"
  value       = var.enabled ? (var.enabled ? "Applied: flo-f5-lifecycle-operator in ${var.flo_namespace}" : null) : null
}

output "flo_namespace_pods_count" {
  description = "Number of pods in FLO namespace (not queried — replaced by time_sleep wait)"
  value       = 0
}

output "flo_pod_deployment_status" {
  description = "Status of FLO pod deployment"
  value = var.enabled ? {
    pod_count        = 0
    scc_policy_count = length(null_resource.flo_scc_privileged)
    namespace        = var.flo_namespace
    status_message   = "FLO deployed via Helm CLI; SCC privileged policy applied"
    next_steps = [
      "Verify pod status: kubectl get pods -n ${var.flo_namespace}",
      "Check pod logs: kubectl logs -n ${var.flo_namespace} <pod-name>",
      "Get pod details: kubectl describe pod -n ${var.flo_namespace} <pod-name>"
    ]
  } : null
}

output "trusted_profile_id" {
  description = "ID of the IBM IAM trusted profile created for the CNE controller service account"
  value       = local.global_enabled ? ibm_iam_trusted_profile.cne_controller[0].id : null
}
