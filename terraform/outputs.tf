# ============================================================
# Outputs — F5 BIG-IP Next for Kubernetes 2.3
# ============================================================


# ============================================================
# roks_cluster
# ============================================================

output "roks_cluster_id" {
  description = "ID of the ROKS cluster"
  value       = module.roks_cluster.roks_cluster_id
}

output "roks_cluster_name" {
  description = "Name of the ROKS cluster"
  value       = module.roks_cluster.roks_cluster_name
}

output "openshift_cluster_public_endpoint" {
  description = "Public endpoint URL for the OpenShift cluster"
  value       = module.roks_cluster.openshift_cluster_public_endpoint
}

output "openshift_cluster_private_endpoint" {
  description = "Private endpoint URL for the OpenShift cluster"
  value       = module.roks_cluster.openshift_cluster_private_endpoint
}

output "roks_transit_gateway_name" {
  description = "Name of the Transit Gateway"
  value       = module.roks_cluster.transit_gateway_name
}


# ============================================================
# flo
# ============================================================

output "flo_namespace" {
  description = "Kubernetes namespace where the F5 Lifecycle Operator is installed"
  value       = local.flo_namespace
}

output "flo_utils_namespace" {
  description = "Kubernetes namespace where the F5 Lifecycle Operator utils are installed"
  value       = try(module.flo.flo_utils_namespace, var.flo_utils_namespace)
}

output "flo_trusted_profile_id" {
  description = "IBM Cloud Trusted Profile ID created by FLO for cluster authentication"
  value       = local.flo_trusted_profile_id
}


# ============================================================
# testing
# ============================================================

output "testing_tgw_jumphost_ip" {
  description = "Public IP of the TGW-connected jumphost (empty when testing_create_tgw_jumphost = false)"
  value       = try(module.testing.testing_tgw_jumphost_public_ip, "")
}

# jumphost_shared_key is the private key (PEM) for the TGW + cluster
# jumphosts. Read by `roksbnkctl up`'s post-apply hook to auto-populate
# `targets.jumphost` in the workspace config (PRD 01); referenced as
# `key_source: tf-output:jumphost_shared_key` from then on. Sensitive
# so it's masked in `terraform output` but available via tfexec's
# Output() with raw bytes.
output "jumphost_shared_key" {
  description = "PEM private key shared across all jumphosts; used by `roksbnkctl --on jumphost`"
  value       = try(module.testing.testing_jumphost_shared_private_key, "")
  sensitive   = true
}

output "testing_tgw_jumphost_ssh_command" {
  description = "SSH command to connect to the TGW-connected jumphost (empty when testing_create_tgw_jumphost = false)"
  value       = try(module.testing.testing_tgw_jumphost_ssh_command, "")
}

output "testing_cluster_jumphost_ips" {
  description = "Public IPs of the per-zone cluster jumphosts (empty when testing_create_cluster_jumphosts = false)"
  value       = try(module.testing.testing_cluster_jumphost_public_ips, [])
}

output "testing_cluster_jumphost_ssh_commands" {
  description = "SSH commands keyed by availability zone for the cluster jumphosts (empty when testing_create_cluster_jumphosts = false)"
  value       = try(module.testing.testing_cluster_jumphost_ssh_commands, {})
}
