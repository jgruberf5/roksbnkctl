# Cluster Outputs
output "roks_cluster_id" {
  description = "ID of the OpenShift cluster"
  value       = module.cluster.cluster_id
}

# Overrides the GitHub module's apply-time cluster_name with a plan-time value
# so downstream provider configs (ibm_container_cluster_config) can be evaluated
# during plan without getting (known after apply).
output "roks_cluster_name" {
  description = "Name of the ROKS cluster — known at plan time"
  value       = var.create_roks_cluster ? var.openshift_cluster_name : var.roks_cluster_id_or_name
}

output "openshift_cluster_id" {
  description = "ID of the OpenShift cluster"
  value       = module.cluster.openshift_cluster_id
}

output "openshift_cluster_name" {
  description = "Name of the OpenShift cluster"
  value       = module.cluster.openshift_cluster_name
}

output "openshift_cluster_public_endpoint" {
  description = "Public endpoint URL for the OpenShift cluster"
  value       = module.cluster.openshift_cluster_public_endpoint
}

output "openshift_cluster_private_endpoint" {
  description = "Private endpoint URL for the OpenShift cluster"
  value       = module.cluster.openshift_cluster_private_endpoint
}

output "openshift_cluster_ingress_hostname" {
  description = "Ingress hostname for the OpenShift cluster"
  value       = module.cluster.openshift_cluster_ingress_hostname
}

output "openshift_cluster_state" {
  description = "State of the OpenShift cluster"
  value       = module.cluster.openshift_cluster_state
}

output "openshift_cluster_crn" {
  description = "CRN of the OpenShift cluster"
  value       = module.cluster.openshift_cluster_crn
}

output "openshift_version_used" {
  description = "OpenShift version used for cluster (auto-detected if not specified)"
  value       = module.cluster.openshift_version_used
}

output "available_openshift_versions" {
  description = "All available OpenShift versions in the cluster region"
  value       = module.cluster.available_openshift_versions
}

output "openshift_worker_zone1_ip" {
  description = "IP address of the worker node in zone 1"
  value       = module.cluster.openshift_worker_zone1_ip
}

output "openshift_worker_zone2_ip" {
  description = "IP address of the worker node in zone 2"
  value       = module.cluster.openshift_worker_zone2_ip
}

output "openshift_worker_zone3_ip" {
  description = "IP address of the worker node in zone 3"
  value       = module.cluster.openshift_worker_zone3_ip
}

output "roks_cluster_vpc_id" {
  description = "ID of the cluster VPC"
  value       = module.cluster.cluster_vpc_id
}

output "roks_cluster_vpc_name" {
  description = "Name of the cluster VPC"
  value       = module.cluster.cluster_vpc_name
}

output "roks_cluster_vpc_crn" {
  description = "CRN of the cluster VPC"
  value       = module.cluster.cluster_vpc_crn
}

output "roks_transit_gateway_id" {
  description = "ID of the Transit Gateway"
  value       = module.cluster.transit_gateway_id
}

output "roks_transit_gateway_name" {
  description = "Name of the Transit Gateway"
  value       = module.cluster.transit_gateway_name
}

output "roks_transit_gateway_crn" {
  description = "CRN of the Transit Gateway"
  value       = module.cluster.transit_gateway_crn
}

output "roks_transit_gateway_location" {
  description = "Location of the Transit Gateway"
  value       = module.cluster.transit_gateway_location
}

output "roks_transit_gateway_global_routing" {
  description = "Global routing status of the Transit Gateway"
  value       = module.cluster.transit_gateway_global_routing
}

# Plan-time transit gateway name for downstream modules (same as roks_transit_gateway_name
# but aliased to the name root main.tf uses for testing_transit_gateway_name wiring).
output "transit_gateway_name" {
  description = "Transit Gateway name — known at plan time"
  value       = var.roks_transit_gateway_name
}

# Apply-time sentinel ID — (known after apply) when create_roks_cluster=true, null otherwise.
# Pass as roks_cluster_dependency_id to downstream modules to enforce apply ordering.
output "cluster_ready_id" {
  description = "Sentinel ID for apply-time ordering"
  value       = var.create_roks_cluster ? null_resource.cluster_ready[0].id : null
}
