# The FLP root CA (PEM). The bnk phase writes this into the CWC's
# `licenseserver-rootca` Secret so BNK trusts the proxy. Empty when the module is
# disabled — the guard keeps `flp up`-less phases from erroring on a null index.
output "flp_root_ca" {
  description = "PEM of the FLP root CA (base64 for transport through tfvars)."
  value       = length(tls_self_signed_cert.ca) > 0 ? base64encode(tls_self_signed_cert.ca[0].cert_pem) : ""
}

# The in-cluster FLP service base URL BNK's License CR points its teem*Url at.
output "flp_endpoint" {
  description = "Base URL of the in-cluster F5 License Proxy service."
  value       = var.deploy_flp ? "https://f5-license-proxy.${var.flp_namespace}.svc.cluster.local:8443" : ""
}

output "flp_namespace" {
  description = "Namespace the FLP was installed into."
  value       = var.flp_namespace
}

# The address a CWC in a DIFFERENT cluster dials. Empty unless the proxy was
# exposed with flp_node_port_access — an in-cluster-only proxy has no external
# address, and handing one out would be a lie.
#
# Any worker node works (the post-renderer sets externalTrafficPolicy: Cluster, so
# every node forwards to the pod), and every worker IP is an IP SAN on the proxy's
# certificate. The first is published as THE endpoint for convenience; the full
# list is exposed so an operator can pick another if a node is drained.
output "flp_external_endpoint" {
  description = "Externally-reachable base URL of the FLP (empty unless flp_node_port_access)."
  value = (var.deploy_flp && var.flp_node_port_access && length(local.node_ips) > 0
    ? "https://${local.node_ips[0]}:${local.flp_node_port}"
  : "")
}

output "flp_external_endpoints" {
  description = "Every worker-node URL the FLP answers on (all are cert IP SANs). Empty unless flp_node_port_access."
  value = [
    for ip in local.node_ips : "https://${ip}:${local.flp_node_port}"
  ]
}

output "flp_node_port" {
  description = "The NodePort the FLP Service listens on."
  value       = var.deploy_flp && var.flp_node_port_access ? local.flp_node_port : 0
}
