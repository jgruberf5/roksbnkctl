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
