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
