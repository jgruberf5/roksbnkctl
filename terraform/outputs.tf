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

output "registry_cos_name" {
  description = "Name of the registry COS instance created by the cluster phase (empty when reusing an existing one)"
  value       = module.roks_cluster.registry_cos_name
}

output "registry_cos_crn" {
  description = "CRN of the registry COS instance created by the cluster phase (empty when reusing an existing one)"
  value       = module.roks_cluster.registry_cos_crn
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

output "testing_ssh_key_name" {
  description = "IBM Cloud VPC SSH key name attached to the testing jumphosts (non-sensitive; empty when only the generated cloud-init key is used)"
  value       = try(module.testing.testing_ssh_key_name, "")
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

# Jumphost SUBNET CIDRs. Forwarded so `roksbnkctl gateway up` can auto-derive
# the gateway client-subnet LISTS from the deployed test rig (PRD 12): one
# local route per cluster-VPC jumphost subnet (so same-zone AND different-zone
# clients each get a return route), one remote route for the client-VPC subnet
# (reached over the Transit Gateway). try()-defaulted so a deploy without the
# jumphosts renders empty.
output "testing_tgw_jumphost_subnet_cidr" {
  description = "Subnet CIDR of the TGW (client-VPC) jumphost (empty when testing_create_tgw_jumphost = false)"
  value       = try(module.testing.testing_tgw_jumphost_subnet_cidr, "")
}

output "testing_cluster_jumphost_subnet_cidrs" {
  description = "Per-zone subnet CIDRs of the cluster jumphosts, keyed by zone (empty when testing_create_cluster_jumphosts = false)"
  value       = try(module.testing.testing_cluster_jumphost_subnet_cidrs, {})
}


# ============================================================
# gateway (empty/placeholder unless the gateway phase is deployed)
# ============================================================

output "gateway_enabled" {
  description = "Whether the Gateway phase applied its resources"
  value       = module.gateway.gateway_enabled
}

# ============================================================
# F5 License Proxy (empty/placeholder unless the FLP phase is deployed).
# The FLP phase persists these to flp-outputs.json; the BNK phase reads them
# in f5licenseproxy mode.
# ============================================================

output "flp_root_ca" {
  description = "Base64 PEM of the FLP root CA (for CWC's licenseserver-rootca Secret)"
  value       = module.flp.flp_root_ca != "" ? module.flp.flp_root_ca : module.flp_vsi.flp_root_ca
}

output "flp_endpoint" {
  description = "Base URL of the in-cluster F5 License Proxy service"
  value       = module.flp.flp_endpoint != "" ? module.flp.flp_endpoint : module.flp_vsi.flp_external_endpoint
}

output "flp_namespace" {
  description = "Namespace the F5 License Proxy was installed into"
  value       = module.flp.flp_namespace
}

output "gateway_app_namespace" {
  description = "Namespace of the Gateway + HTTPRoute"
  value       = module.gateway.gateway_app_namespace
}

output "gateway_flo_namespace" {
  description = "Namespace of the F5BnkGateway + F5SPK CRs"
  value       = module.gateway.gateway_flo_namespace
}

output "gateway_name" {
  description = "Name of the Gateway (gateway.networking.k8s.io) CR"
  value       = module.gateway.gateway_name
}

output "gateway_class_name" {
  description = "Name of the GatewayClass"
  value       = module.gateway.gateway_class_name
}

output "gateway_controller_name" {
  description = "Resolved GatewayClass controllerName (derived from flo_namespace unless set)"
  value       = module.gateway.gateway_controller_name
}

output "gateway_bnkgateway_name" {
  description = "Name of the F5BnkGateway (k8s.f5net.com) CR"
  value       = module.gateway.gateway_bnkgateway_name
}

# These three are read by `gateway status` (internal/cli/phase_status.go). A
# module output that is not re-exported here is invisible: ReadStateOutputs
# reads only the ROOT state's .outputs, so the CLI sees "" and silently takes a
# fallback path. On 2.4 that meant gateway status looked in the wrong namespace
# and never rendered the Infra or GatewaySettings blocks at all.
output "gateway_namespace" {
  description = "Namespace the Gateway object lives in (FLO namespace on 2.4)"
  value       = module.gateway.gateway_namespace
}

output "gateway_settings_name" {
  description = "Name of the 2.4 GatewaySettings CR, or empty on 2.3"
  value       = module.gateway.gateway_settings_name
}

output "gateway_infra_name" {
  description = "Name of the 2.4 Infra CR, or empty on 2.3"
  value       = module.gateway.gateway_infra_name
}

output "gateway_route_name" {
  description = "Name of the HTTPRoute"
  value       = module.gateway.gateway_route_name
}

output "gateway_backend" {
  description = "Backend the HTTPRoute targets (service:port)"
  value       = module.gateway.gateway_backend
}

output "gateway_listener_networks" {
  description = "Per-zone F5BnkGateway listener networks (name + VIP range + zone)"
  value       = module.gateway.gateway_listener_networks
}

output "gateway_egress_mode" {
  description = "Egress mode: snatpool | automap | both"
  value       = module.gateway.gateway_egress_mode
}

output "gateway_snatpool_name" {
  description = "Name of the F5SPKSnatpool (empty unless egress includes snatpool)"
  value       = module.gateway.gateway_snatpool_name
}

output "gateway_snat_addresses" {
  description = "Per-zone SNAT addresses"
  value       = module.gateway.gateway_snat_addresses
}

output "gateway_egress_cr_names" {
  description = "Names of the F5SPKEgress CRs applied"
  value       = module.gateway.gateway_egress_cr_names
}

output "gateway_vxlan_port" {
  description = "VXLAN tunnel port used by the egress CRs"
  value       = module.gateway.gateway_vxlan_port
}

output "gateway_static_routes" {
  description = "F5SPKStaticRoute set: name => { destination, prefix_len, gateway }"
  value       = module.gateway.gateway_static_routes
}

output "flp_external_endpoint" {
  description = "Externally-reachable FLP URL for a BNK install in another cluster (empty unless flp_node_port_access)."
  value       = try(module.flp.flp_external_endpoint, "") != "" ? module.flp.flp_external_endpoint : try(module.flp_vsi.flp_external_endpoint, "")
}

output "flp_external_endpoints" {
  description = "Every worker-node URL the FLP answers on."
  value       = length(try(module.flp.flp_external_endpoints, [])) > 0 ? module.flp.flp_external_endpoints : try(module.flp_vsi.flp_external_endpoints, [])
}

output "flp_node_port" {
  description = "NodePort the FLP Service listens on (0 unless flp_node_port_access)."
  value       = try(module.flp.flp_node_port, 0)
}

output "flp_floating_ip" {
  description = "Operator floating IP of the standalone FLP VSI (remote flp status + web UI). Empty for in-cluster FLP or when flp_vsi_floating_ip=false."
  value       = try(module.flp_vsi.flp_floating_ip, "")
}

output "tgw_gateway_id" {
  description = "ID of the Transit Gateway the cluster VPC is attached to (tgw phase)."
  value       = try(module.tgw_connection.tgw_gateway_id, "")
}

output "tgw_gateway_name" {
  description = "Name of the Transit Gateway the cluster VPC is attached to (tgw phase)."
  value       = try(module.tgw_connection.tgw_gateway_name, "")
}

output "tgw_gateway_crn" {
  description = "CRN of the Transit Gateway (tgw phase)."
  value       = try(module.tgw_connection.tgw_gateway_crn, "")
}

output "tgw_connection_id" {
  description = "ID of this cluster's connection on the gateway (tgw phase)."
  value       = try(module.tgw_connection.tgw_connection_id, "")
}

output "tgw_connection_name" {
  description = "Name of this cluster's connection on the gateway (tgw phase)."
  value       = try(module.tgw_connection.tgw_connection_name, "")
}

output "tgw_vpc_crn" {
  description = "CRN of the cluster VPC attached to the Transit Gateway (tgw phase)."
  value       = try(module.tgw_connection.tgw_vpc_crn, "")
}
