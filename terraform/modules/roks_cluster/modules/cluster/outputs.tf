output "cluster_vpc_id" {
  description = "ID of the cluster VPC"
  value       = local.cluster_vpc_id
}

output "cluster_vpc_name" {
  description = "Name of the cluster VPC"
  value       = var.use_existing_cluster_vpc ? data.ibm_is_vpc.existing_cluster_vpc[0].name : ibm_is_vpc.cluster_vpc[0].name
}

output "openshift_version_used" {
  description = "OpenShift version used for cluster (auto-detected if not specified)"
  value       = local.openshift_version
}

output "available_openshift_versions" {
  description = "All available OpenShift versions in the cluster region"
  value       = local.available_openshift_versions
}

output "cluster_vpc_crn" {
  description = "CRN of the cluster VPC"
  value       = local.cluster_vpc_crn
}

# OpenShift Cluster Outputs
output "openshift_cluster_id" {
  description = "ID of the OpenShift cluster"
  value       = var.create_cluster ? ibm_container_vpc_cluster.openshift_cluster[0].id : "Cluster not created"
}

output "openshift_cluster_name" {
  description = "Name of the OpenShift cluster"
  value       = var.create_cluster ? ibm_container_vpc_cluster.openshift_cluster[0].name : "Cluster not created"
}

output "openshift_cluster_state" {
  description = "State of the OpenShift cluster"
  value       = var.create_cluster ? data.ibm_container_vpc_cluster.cluster_info[0].state : "Cluster not created"
}

# Registry COS identity — emitted so `roksbnkctl cluster up` records it in
# cluster-outputs.json directly, instead of reverse-guessing the instance name.
# Populated for BOTH paths (#294): the instance this phase created, or the one it
# adopted. Previously only the created case was reported, so an adopted COS left
# these empty and the CLI fell back to guessing "<cluster>-cos-instance" /
# "<cluster>-cos" -- names an adopted instance has no reason to match, so the
# workspace recorded no registry COS at all. Still empty when the cluster is not
# created by this phase, which is what that fallback is really for.
output "registry_cos_name" {
  description = "Name of the registry COS instance backing the cluster registry (created or adopted; empty when this phase manages no cluster)"
  value       = local.registry_cos_name
}

output "registry_cos_crn" {
  description = "CRN of the registry COS instance backing the cluster registry (created or adopted; empty when this phase manages no cluster)"
  value       = local.registry_cos_crn
}

output "openshift_cluster_ingress_hostname" {
  description = "Ingress hostname for the OpenShift cluster"
  value       = var.create_cluster ? ibm_container_vpc_cluster.openshift_cluster[0].ingress_hostname : "Cluster not created"
}

output "openshift_cluster_public_endpoint" {
  description = "Public service endpoint URL"
  value       = var.create_cluster ? ibm_container_vpc_cluster.openshift_cluster[0].public_service_endpoint_url : "Cluster not created"
}

output "openshift_cluster_private_endpoint" {
  description = "Private service endpoint URL"
  value       = var.create_cluster ? ibm_container_vpc_cluster.openshift_cluster[0].private_service_endpoint_url : "Cluster not created"
}

output "openshift_worker_zone1_ip" {
  description = "IP address of zone 1 worker node"
  value       = var.create_cluster ? local.zone1_worker_ip : "Cluster not created"
}

output "openshift_worker_zone2_ip" {
  description = "IP address of zone 2 worker node"
  value       = var.create_cluster ? local.zone2_worker_ip : "Cluster not created"
}

output "openshift_worker_zone3_ip" {
  description = "IP address of zone 3 worker node"
  value       = var.create_cluster ? local.zone3_worker_ip : "Cluster not created"
}

# Transit Gateway Outputs
output "transit_gateway_id" {
  description = "ID of the Transit Gateway"
  value       = var.create_transit_gateway ? ibm_tg_gateway.transit_gateway[0].id : "Transit Gateway not created"
}

output "transit_gateway_name" {
  description = "Name of the Transit Gateway"
  value       = var.create_transit_gateway ? ibm_tg_gateway.transit_gateway[0].name : "Transit Gateway not created"
}

output "transit_gateway_crn" {
  description = "CRN of the Transit Gateway"
  value       = var.create_transit_gateway ? ibm_tg_gateway.transit_gateway[0].crn : "Transit Gateway not created"
}

output "transit_gateway_location" {
  description = "Location of the Transit Gateway"
  value       = var.create_transit_gateway ? ibm_tg_gateway.transit_gateway[0].location : "Transit Gateway not created"
}

output "transit_gateway_global_routing" {
  description = "Global routing status"
  value       = var.create_transit_gateway ? ibm_tg_gateway.transit_gateway[0].global : false
}

output "transit_gateway_connections" {
  description = "Transit Gateway connection summary"
  value = var.create_transit_gateway && var.create_cluster ? {
    cluster_vpc = ibm_tg_connection.cluster_vpc_connection[0].name
  } : null
}


# ============================================================
# Kubeconfig Outputs
# ============================================================

output "kubeconfig_file_path" {
  description = "Path to the kubeconfig file for the OpenShift cluster (use default location)"
  value       = "~/.kube/config"
}

output "cluster_id" {
  description = "ID of the OpenShift cluster (alias for openshift_cluster_id)"
  # try(...,"") tolerates the cluster-less standalone case (no adopt data source).
  value = var.create_cluster ? ibm_container_vpc_cluster.openshift_cluster[0].id : try(data.ibm_container_vpc_cluster.existing_cluster[0].id, "")
}

output "cluster_name" {
  description = "Name of the OpenShift cluster (alias for openshift_cluster_name)"
  value       = var.create_cluster ? ibm_container_vpc_cluster.openshift_cluster[0].name : try(data.ibm_container_vpc_cluster.existing_cluster[0].name, "")
}

output "openshift_cluster_crn" {
  description = "CRN of the OpenShift cluster"
  value       = var.create_cluster ? ibm_container_vpc_cluster.openshift_cluster[0].crn : try(data.ibm_container_vpc_cluster.existing_cluster[0].crn, "")
}
