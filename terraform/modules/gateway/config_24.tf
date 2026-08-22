# ── BNK 2.4: GatewaySettings (#168) ──────────────────────────────────────────
#
# The second half of the 2.4 network model. Where Infra describes the addressing
# (IPAM pools, networks, static routes), GatewaySettings describes how traffic
# uses it: which pool ingress VIPs come from, and how egress is source-NATted.
#
# Mapping from the 2.3 objects this replaces:
#
#     F5SPKSnatpool                       -> sourceNATPools[]
#     F5SPKEgress x2                      -> egressConfigs[] (+ Infra.egressDefaults)
#     F5BnkGateway.defaultListenerNetworks -> ingressConfig.defaultListenerNetwork
#
# gateway_egress_mode carries over unchanged, so an existing workspace's egress
# choice means the same thing on both lines:
#
#     snatpool -> a Pool config backed by egress-snat-ipam
#     automap  -> an Automap config
#     both     -> both, which is what the vendor's example demonstrates
#
# The guide is explicit that "BNK does not support changing the SNAT type from
# UseIngressAddress to other SNAT mode after the CR has been applied", so
# UseIngressAddress is deliberately NOT reachable from gateway_egress_mode — an
# irreversible choice should not ride on a value that reads like a preference.
# It stays available by editing the CR, where the irreversibility is visible.

locals {
  do_snatpool_24 = var.gateway_egress_mode == "snatpool" || var.gateway_egress_mode == "both"
  do_automap_24  = var.gateway_egress_mode == "automap" || var.gateway_egress_mode == "both"

  gateway_settings_manifest_24 = {
    apiVersion = "gateway.k8s.f5.com/v1alpha1"
    kind       = "GatewaySettings"
    metadata = {
      name      = var.gateway_settings_name
      namespace = var.flo_namespace
    }
    spec = merge(
      {
        ingressConfig = {
          defaultListenerNetwork = {
            ipamRefs        = [{ name = "vip-listener-ipam" }]
            networkRefs     = [{ name = "external-vlan" }]
            sourceNATConfig = { type = "Automap" }
          }
        }
      },
      local.do_snatpool_24 ? {
        sourceNATPools = [
          {
            name     = var.gateway_snatpool_name
            ipamRefs = [{ name = "egress-snat-ipam" }]
            shared   = false
          }
        ]
      } : {},
      {
        egressConfigs = concat(
          local.do_automap_24 ? [
            {
              name            = "egress-vxlan-automap"
              networkRef      = { name = "external-vlan" }
              sourceNATConfig = { type = "Automap" }
            }
          ] : [],
          local.do_snatpool_24 ? [
            {
              name       = "egress-vxlan-snatpool"
              networkRef = { name = "external-vlan" }
              sourceNATConfig = {
                type             = "Pool"
                sourceNATPoolRef = { name = var.gateway_snatpool_name }
              }
            }
          ] : [],
        )
      },
    )
  }
}

resource "kubectl_manifest" "gateway_settings_24" {
  count             = local.enabled && local.line_24 ? 1 : 0
  yaml_body         = yamlencode(local.gateway_settings_manifest_24)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true

  # Infra first: the IPAMs and the network this references must exist, or the
  # controller rejects the refs and the failure reads as a validation error on a
  # CR that is actually correct.
  depends_on = [kubectl_manifest.infra_24]
}
