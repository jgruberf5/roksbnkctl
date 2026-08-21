# ── BNK 2.4: the Infra CR (#168) ─────────────────────────────────────────────
#
# 2.4 runs the CNEInstance with USE_GATEWAY_SETTINGS=true. Under that flag the
# CNE controller IGNORES the cloud-network-mapping ConfigMap and the entire
# F5SPK* family this module creates for 2.3, and reads Infra + GatewaySettings
# instead. Confirmed on a live 2.4 cluster: no F5SPK* object exists, no ConfigMap
# exists, and the 2.3-only log line "External or internal VLAN is nil, skipping
# TMM SelfIP mapping" — which a 2.3 controller emits continuously when those CRs
# are missing — appears zero times.
#
# WHAT THIS IS NOT. Infra/GatewaySettings are a DATA-PATH concern, not an install
# prerequisite: on the live cluster the License CR went Active, TMM went 3/3
# Ready and all eighteen CNEInstance conditions went True with none of these CRs
# present. That is why they live in the gateway phase rather than the BNK phase.
#
# ADDRESSING COMES FROM EXISTING CONFIG. cneinstance_network_zones already
# carries the three per-zone CIDRs the vendor's worked example needs, so the base
# case needs no new configuration surface:
#
#     ext_vlan_cidr  -> external-vlan-ipam   (TMM VLAN self-IPs)
#     int_vip_cidr   -> vip-listener-ipam    (ingress VIPs)
#     int_snat_cidr  -> egress-snat-ipam     (egress SNAT addresses)
#
# The lists are built as NAMED entries rather than a hardcoded external/internal
# pair, because 2.4's model is N-network capable — every field is a named list.
# That is what makes IBM multi-NIC a config change later instead of a second
# rewrite.
#
# NOT AUTHORED HERE: fic.f5.com IPAM/IPAMRange objects. The controller generates
# them from this CR (named vlan-<ns>-<network>.<infra>, gw-<ns>-<gateway>,
# infra-<ns>-<infra>); writing them ourselves would fight the owner.

locals {
  # 2.4 only. `== "2.4"` here rather than `!= "2.3"`: these are NEW resources, so
  # an unrecognised line must NOT get them — the opposite asymmetry to the
  # subtractions in the flo/cneinstance modules, and for the same reason. Emitting
  # an unknown release's data path is a guess; withholding it is a no-op.
  line_24 = var.bnk_line == "2.4"

  # One IPAM per role, each with a per-zone pool. A named list, so a fourth role
  # (or a second NIC's pools) is an append.
  infra_ipams_24 = [
    {
      name = "external-vlan-ipam"
      ipPools = [
        for i, z in var.cneinstance_network_zones : {
          cidr             = z.ext_vlan_cidr
          availabilityZone = local.zone_names[i]
        }
      ]
    },
    {
      name = "vip-listener-ipam"
      ipPools = [
        for i, z in var.cneinstance_network_zones : {
          cidr             = z.int_vip_cidr
          availabilityZone = local.zone_names[i]
        }
      ]
    },
    {
      name = "egress-snat-ipam"
      ipPools = [
        for i, z in var.cneinstance_network_zones : {
          cidr             = z.int_snat_cidr
          availabilityZone = local.zone_names[i]
        }
      ]
    },
  ]

  # Per-zone static routes. destinations are the client subnets this install must
  # reach; nextHop is the .1 of that zone's external CIDR, which IPAM reserves —
  # visible in the generated IPAM status as a "...-reserved" key.
  infra_static_routes_24 = [
    for i, z in var.cneinstance_network_zones : {
      name         = "static-route-client-z${i + 1}"
      destinations = concat(var.gateway_client_subnet_local, var.gateway_client_subnet_remote)
      # Same knob 2.3 uses for its static-route gateway, so the two lines cannot
      # disagree about which address is the zone gateway. The vendor example uses
      # .1, which is this variable's default and is the address IPAM reserves.
      nextHop = cidrhost(z.ext_vlan_cidr, var.gateway_static_route_gw_host)
    }
    if length(concat(var.gateway_client_subnet_local, var.gateway_client_subnet_remote)) > 0
  ]

  infra_manifest_24 = {
    apiVersion = "gateway.k8s.f5.com/v1alpha1"
    kind       = "Infra"
    metadata = {
      name      = var.gateway_infra_name
      namespace = var.flo_namespace
    }
    spec = merge(
      {
        ipams = local.infra_ipams_24
        networkAttachments = [
          {
            name                         = "external-vlan-nad"
            networkAttachmentDefinitions = [{ name = var.gateway_nad_name }]
          }
        ]
        networks = [
          {
            name = "external-vlan"
            type = "vlan"
            vlan = {
              mtu                  = var.gateway_egress_mtu
              networkAttachmentRef = { name = "external-vlan-nad" }
              ipamRefs             = [{ name = "external-vlan-ipam" }]
            }
          }
        ]
        # egressDefaults alone drives the VXLAN — the vendor's example carries no
        # `vxlan` network entry and no `vrfs` block. networkRef is REQUIRED for
        # the tunnel to come up.
        egressDefaults = merge(
          {
            networkRef = { name = "external-vlan" }
            port       = var.gateway_egress_vxlan_port
          },
          # The controller's default egress VXLAN CIDR is 10.0.0.0/16 and must be
          # excluded from IPAM ranges unless overridden here. Empty leaves the
          # controller's default, which is correct only when no IPAM pool
          # overlaps it — so this is settable rather than assumed.
          var.gateway_egress_vxlan_subnet == "" ? {} : { subnet = var.gateway_egress_vxlan_subnet },
        )
      },
      length(local.infra_static_routes_24) == 0 ? {} : { staticRoutes = local.infra_static_routes_24 },
    )
  }
}

resource "kubectl_manifest" "infra_24" {
  count             = local.enabled && local.line_24 ? 1 : 0
  yaml_body         = yamlencode(local.infra_manifest_24)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}
