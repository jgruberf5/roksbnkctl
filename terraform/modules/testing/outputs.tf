# ============================================================
# Root Terraform Outputs
# IBM Cloud Testing Jumphosts
# ============================================================

# ============================================================
# Shared Jumphost SSH Key
# ============================================================

output "testing_jumphost_shared_public_key" {
  description = "Public key installed on all jumphosts — add to your local ~/.ssh/authorized_keys to log in, or use the private key below"
  value       = trimspace(tls_private_key.jumphost_shared_key.public_key_openssh)
}

output "testing_jumphost_shared_private_key" {
  description = "Private key shared across all jumphosts. Write to a local file (chmod 600) to SSH between hosts: ssh -i <keyfile> ubuntu@<ip>"
  value       = tls_private_key.jumphost_shared_key.private_key_openssh
  sensitive   = true
}

# ============================================================
# Referenced Cluster
# ============================================================

output "roks_cluster_id" {
  description = "ID of the referenced OpenShift cluster"
  value       = data.ibm_container_vpc_cluster.cluster.id
}

output "roks_cluster_name" {
  description = "Name of the referenced OpenShift cluster"
  value       = data.ibm_container_vpc_cluster.cluster.name
}

# ============================================================
# TGW Jumphost
# ============================================================

output "testing_tgw_jumphost_vpc_id" {
  description = "ID of the VPC containing the TGW jumphost"
  value       = var.testing_create_tgw_jumphost ? local.tgw_vpc_id : "TGW jumphost not created"
}

output "testing_tgw_jumphost_vpc_name" {
  description = "Name of the VPC containing the TGW jumphost"
  value       = var.testing_create_tgw_jumphost ? local.tgw_vpc_name : "TGW jumphost not created"
}

output "testing_tgw_jumphost_id" {
  description = "Instance ID of the TGW jumphost"
  value       = var.testing_create_tgw_jumphost ? ibm_is_instance.tgw_jumphost[0].id : "TGW jumphost not created"
}

output "testing_tgw_jumphost_private_ip" {
  description = "Private IP address of the TGW jumphost"
  value       = var.testing_create_tgw_jumphost ? ibm_is_instance.tgw_jumphost[0].primary_network_interface[0].primary_ip[0].address : "TGW jumphost not created"
}

output "testing_tgw_jumphost_public_ip" {
  description = "Floating (public) IP address of the TGW jumphost"
  value       = var.testing_create_tgw_jumphost ? ibm_is_floating_ip.tgw_jumphost_fip[0].address : "TGW jumphost not created"
}

output "testing_tgw_jumphost_ssh_command" {
  description = "SSH command to connect to the TGW jumphost"
  value = var.testing_create_tgw_jumphost ? (
    var.testing_ssh_key_name != "" ?
    "ssh -i ${var.testing_ssh_key_name} ubuntu@${ibm_is_floating_ip.tgw_jumphost_fip[0].address}" :
    "ssh ubuntu@${ibm_is_floating_ip.tgw_jumphost_fip[0].address}"
  ) : "TGW jumphost not created"
}

output "testing_tgw_jumphost_zone" {
  description = "Availability zone where the TGW jumphost was placed"
  value       = var.testing_create_tgw_jumphost ? local.tgw_jumphost_zone : "TGW jumphost not created"
}

output "testing_tgw_jumphost_profile_used" {
  description = "Instance profile selected for the TGW jumphost"
  value       = var.testing_create_tgw_jumphost ? local.tgw_jumphost_profile : "TGW jumphost not created"
}

output "testing_transit_gateway_connection_id" {
  description = "ID of the Transit Gateway VPC connection (empty when transit_gateway_name not set)"
  value       = (var.testing_create_tgw_jumphost && var.testing_transit_gateway_name != "") ? ibm_tg_connection.tgw_vpc_connection[0].connection_id : "TGW connection not created"
}

# ============================================================
# Cluster Jumphosts (maps keyed by availability zone)
# ============================================================

output "testing_cluster_jumphost_ids" {
  description = "Map of availability zone to instance ID for cluster jumphosts"
  value       = { for zone, inst in ibm_is_instance.cluster_jumphost : zone => inst.id }
}

output "testing_cluster_jumphost_private_ips" {
  description = "Map of availability zone to private IP address for cluster jumphosts"
  value       = { for zone, inst in ibm_is_instance.cluster_jumphost : zone => inst.primary_network_interface[0].primary_ip[0].address }
}

output "testing_cluster_jumphost_public_ips" {
  description = "Map of availability zone to floating IP address for cluster jumphosts"
  value       = { for zone, fip in ibm_is_floating_ip.cluster_jumphost_fip : zone => fip.address }
}

output "testing_cluster_jumphost_ssh_commands" {
  description = "Map of availability zone to SSH command for cluster jumphosts"
  value = {
    for zone, fip in ibm_is_floating_ip.cluster_jumphost_fip :
    zone => (var.testing_ssh_key_name != "" ? "ssh -i ${var.testing_ssh_key_name} ubuntu@${fip.address}" : "ssh ubuntu@${fip.address}")
  }
}
