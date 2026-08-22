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

# Surfaced because the failure it diagnoses is SILENT: a controllerName that no
# controller answers to leaves GatewayClass ACCEPTED=<none> and the apply
# succeeds. Having the resolved value in `gateway output` makes "does this match
# the CNEInstance?" a one-line check instead of an inspection of two CRs.
output "gateway_controller_name" {
  description = "Resolved GatewayClass controllerName (derived from flo_namespace unless set)"
  value       = local.enabled ? local.gateway_controller_name : ""
}

output "gateway_bnkgateway_name" {
  description = "Name of the F5BnkGateway (k8s.f5net.com) CR"
  # `gateway status` treats a non-empty name as "go read this CR", so leaving it
  # set on 2.4 makes every status call chase an F5BnkGateway that count-0 never
  # created. Mirrors gateway_settings_name / gateway_infra_name below.
  value = local.enabled && local.line_pre_24 ? var.gateway_bnkgateway_name : ""
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

output "gateway_namespace" {
  description = <<-EOT
    Namespace the Gateway object actually lives in.

    2.3 puts it in the application namespace; 2.4 puts it beside GatewaySettings
    in the FLO namespace, because the guide requires them to share one (#173).
    `gateway status` reads the allocated VIP from this Gateway's status, so it
    needs to know where to look — computing it from the line in two places is how
    they drift.
  EOT
  value       = local.gateway_ns_effective
}

output "gateway_settings_name" {
  description = "Name of the 2.4 GatewaySettings CR, or empty on 2.3."
  value       = local.line_pre_24 ? "" : var.gateway_settings_name
}

output "gateway_infra_name" {
  description = "Name of the 2.4 Infra CR, or empty on 2.3."
  value       = local.line_pre_24 ? "" : var.gateway_infra_name
}
