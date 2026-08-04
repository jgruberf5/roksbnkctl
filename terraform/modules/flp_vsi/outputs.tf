# Output names MATCH the helm flp module so the FLP phase's persistFLPOutputs
# captures them identically into flp-outputs.json — the BNK phase consumes the
# same {root CA, endpoint} handoff regardless of backend.

output "flp_root_ca" {
  description = "Base64-encoded PEM of the FLP root CA (BNK's bnk.flp.external.root_ca_b64)."
  value       = var.deploy_flp_vsi ? base64encode(tls_self_signed_cert.ca[0].cert_pem) : ""
}

output "flp_external_endpoint" {
  description = "The https://<ip>:8443 URL a BNK CWC dials to license through the VSI proxy."
  value       = var.deploy_flp_vsi ? "https://${local.reach_ip}:8443" : ""
}

output "flp_external_endpoints" {
  description = "All URLs the proxy answers on (single-VSI: just the one)."
  value       = var.deploy_flp_vsi ? ["https://${local.reach_ip}:8443"] : []
}

output "flp_vsi_id" {
  description = "The VSI id (for diagnostics)."
  value       = var.deploy_flp_vsi ? ibm_is_instance.flp[0].id : ""
}

output "flp_floating_ip" {
  description = "Operator floating IP for remote management (flp status + web UI from another machine). Empty when flp_vsi_floating_ip=false."
  value       = local.floating_ip_addr
}
