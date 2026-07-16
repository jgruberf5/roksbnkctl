# All guarded so the module contributes nothing when disabled (the reuse phases
# carry it in the shared root config but never set deploy_tgw_connection).

output "tgw_gateway_id" {
  description = "ID of the Transit Gateway the cluster VPC is attached to."
  value       = local.enabled && local.gateway != null ? local.gateway.id : ""
}

output "tgw_gateway_name" {
  description = "Name of the Transit Gateway the cluster VPC is attached to."
  value       = local.enabled && local.gateway != null ? local.gateway.name : ""
}

output "tgw_gateway_crn" {
  description = "CRN of the Transit Gateway."
  value       = local.enabled && local.gateway != null ? local.gateway.crn : ""
}

output "tgw_connection_id" {
  description = "ID of this cluster's connection on the gateway."
  value       = local.enabled ? ibm_tg_connection.cluster_vpc[0].connection_id : ""
}

output "tgw_connection_name" {
  description = "Name of this cluster's connection on the gateway."
  value       = local.enabled ? ibm_tg_connection.cluster_vpc[0].name : ""
}

output "tgw_vpc_id" {
  description = "ID of the cluster VPC that was attached."
  value       = local.enabled ? data.ibm_is_vpc.cluster_vpc[0].id : ""
}

output "tgw_vpc_crn" {
  description = "CRN of the cluster VPC (the connection's network_id) — recorded so `tgw status` needs no live VPC lookup."
  value       = local.enabled ? data.ibm_is_vpc.cluster_vpc[0].crn : ""
}
