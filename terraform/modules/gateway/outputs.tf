output "gateway_enabled" {
  description = "Whether the Gateway phase applied its resources"
  value       = local.enabled
}

output "gateway_listener_networks" {
  description = "The per-zone F5BnkGateway listener networks (name + VIP range + zone)"
  value       = local.enabled ? local.default_listener_networks : []
}

output "gateway_app_namespace" {
  description = "The application namespace the Gateway + HTTPRoute serve"
  value       = var.app_namespace
}

# ── Resource graph (names you'd kubectl) — empty when the phase isn't deployed ──

output "gateway_flo_namespace" {
  description = "Namespace of the F5BnkGateway + F5SPK CRs (snatpool/egress/static-route)"
  value       = local.enabled ? var.flo_namespace : ""
}

output "gateway_name" {
  description = "Name of the Gateway (gateway.networking.k8s.io) CR"
  value       = local.enabled ? var.gateway_name : ""
}

output "gateway_class_name" {
  description = "Name of the GatewayClass"
  value       = local.enabled ? var.gateway_class_name : ""
}

output "gateway_bnkgateway_name" {
  description = "Name of the F5BnkGateway (k8s.f5net.com) CR"
  value       = local.enabled ? var.gateway_bnkgateway_name : ""
}

output "gateway_route_name" {
  description = "Name of the HTTPRoute"
  value       = local.enabled ? var.gateway_route_name : ""
}

output "gateway_backend" {
  description = "Backend the HTTPRoute targets, as service:port"
  value       = local.enabled ? "${var.gateway_backend_service}:${var.gateway_backend_port}" : ""
}

# ── Egress / SNAT (data-plane) ──

output "gateway_egress_mode" {
  description = "Egress mode: snatpool | automap | both"
  value       = local.enabled ? var.gateway_egress_mode : ""
}

output "gateway_snatpool_name" {
  description = "Name of the F5SPKSnatpool (empty unless egress mode includes snatpool)"
  value       = local.enabled && local.do_snatpool ? var.gateway_snatpool_name : ""
}

output "gateway_snat_addresses" {
  description = "Per-zone SNAT addresses (empty unless egress mode includes snatpool)"
  value       = local.enabled && local.do_snatpool ? flatten(local.snat_address_list) : []
}

output "gateway_egress_cr_names" {
  description = "Names of the F5SPKEgress CRs applied for the active egress mode"
  value = local.enabled ? compact([
    local.do_automap ? "egress-cr-primary-vxlan-101" : "",
    local.do_snatpool ? "egress-cr-primary-vxlan-102" : "",
  ]) : []
}

output "gateway_vxlan_port" {
  description = "VXLAN tunnel port used by the egress CRs"
  value       = local.enabled ? var.gateway_vxlan_port : 0
}

# ── Connectivity (static routes) ──

output "gateway_static_routes" {
  description = "F5SPKStaticRoute set: name => { destination, prefix_len, gateway }"
  value       = local.enabled ? local.static_routes : {}
}
