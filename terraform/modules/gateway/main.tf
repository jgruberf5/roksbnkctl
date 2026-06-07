# ============================================================
# Gateway phase — data-plane ingress/egress configuration
# ============================================================
# Applies the BNK 2.3 install-guide "Configuration" CRs that the BNK phase
# leaves to the operator: the Gateway API set (GatewayClass / F5BnkGateway /
# Gateway / HTTPRoute), the egress SnatPool + Egress CRs, the per-zone static
# routes, and the cluster security-group VXLAN rule. Runs only against an
# already-healthy BNK (its CRDs ship with the BNK manifest). Everything is
# gated on var.deploy_gateway so the phase is optional to install/uninstall.
# All CRs are server-side-applied with force_conflicts (the CNE controller
# co-owns fields once it reconciles them).

locals {
  enabled = var.deploy_gateway

  zone_names = [for i in range(length(var.cneinstance_network_zones)) : "${var.ibmcloud_cluster_region}-${i + 1}"]

  do_automap  = contains(["automap", "both"], var.gateway_egress_mode)
  do_snatpool = contains(["snatpool", "both"], var.gateway_egress_mode)

  # F5BnkGateway listener networks (one per zone), addresses derived from int_vip.
  default_listener_networks = [
    for i, z in var.cneinstance_network_zones : {
      name             = "${local.zone_names[i]}-ipv4"
      ipv4BaseCidr     = z.int_vip_cidr
      startAddress     = cidrhost(z.int_vip_cidr, var.gateway_vip_start_host)
      endAddress       = cidrhost(z.int_vip_cidr, var.gateway_vip_end_host)
      availabilityZone = local.zone_names[i]
    }
  ]

  # SnatPool addressList is a list-of-lists (one address list per zone).
  snat_address_list = [for z in var.cneinstance_network_zones : [cidrhost(z.int_snat_cidr, var.gateway_snat_host)]]

  # Per-zone static routes, both the local-VSI and remote-VSI client subnets,
  # each via that zone's external-VLAN gateway (ext_vlan .1).
  static_routes = merge(
    { for i, z in var.cneinstance_network_zones : "static-route-client-z${i + 1}" => {
      destination = var.gateway_client_subnet_local
      gateway     = cidrhost(z.ext_vlan_cidr, var.gateway_static_route_gw_host)
    } },
    { for i, z in var.cneinstance_network_zones : "static-route-client-z${i + 1}-remote" => {
      destination = var.gateway_client_subnet_remote
      gateway     = cidrhost(z.ext_vlan_cidr, var.gateway_static_route_gw_host)
    } },
  )

  egress_pseudo_cni = {
    namespaces      = [var.app_namespace]
    appPodInterface = var.gateway_egress_app_interface
    vxlan = {
      create            = true
      tmmInterfaceName  = var.gateway_egress_tmm_interface
      nodeInterfaceName = var.gateway_egress_node_interface
      mtu               = var.gateway_egress_mtu
      port              = var.gateway_vxlan_port
    }
  }
}

# ── Gateway API ──────────────────────────────────────────────────────────────

resource "kubectl_manifest" "gateway_class" {
  count = local.enabled ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = "gateway.networking.k8s.io/v1"
    kind       = "GatewayClass"
    metadata   = { name = var.gateway_class_name }
    spec = {
      controllerName = var.gateway_controller_name
      description    = "F5 BIG-IP Kubernetes Gateway"
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "bnk_gateway" {
  count = local.enabled ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = "k8s.f5net.com/v1"
    kind       = "F5BnkGateway"
    metadata   = { name = var.gateway_bnkgateway_name, namespace = var.flo_namespace }
    spec = {
      ingressConfig = {
        defaultListenerNetworks = local.default_listener_networks
      }
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubernetes_namespace_v1" "app" {
  count = local.enabled ? 1 : 0
  metadata {
    name = var.app_namespace
  }
}

resource "kubectl_manifest" "gateway" {
  count = local.enabled ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = "gateway.networking.k8s.io/v1"
    kind       = "Gateway"
    metadata   = { name = var.gateway_name, namespace = var.app_namespace }
    spec = {
      infrastructure = {
        parametersRef = {
          group = "k8s.f5net.com"
          kind  = "F5BnkGateway"
          name  = var.gateway_bnkgateway_name
        }
      }
      gatewayClassName = var.gateway_class_name
      listeners = [{
        name     = "http"
        protocol = "HTTP"
        port     = var.gateway_listener_port
        allowedRoutes = {
          namespaces = { from = "Same" }
          kinds      = [{ kind = "HTTPRoute" }]
        }
      }]
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [kubectl_manifest.bnk_gateway, kubernetes_namespace_v1.app]
}

resource "kubectl_manifest" "http_route" {
  count = local.enabled ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = "gateway.networking.k8s.io/v1"
    kind       = "HTTPRoute"
    metadata   = { name = var.gateway_route_name, namespace = var.app_namespace }
    spec = {
      parentRefs = [{
        name        = var.gateway_name
        namespace   = var.app_namespace
        sectionName = "http"
      }]
      rules = [{
        backendRefs = [{
          name = var.gateway_backend_service
          port = var.gateway_backend_port
        }]
      }]
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [kubectl_manifest.gateway]
}

# ── Egress: SnatPool + Egress CRs ────────────────────────────────────────────

resource "kubectl_manifest" "snatpool" {
  count = local.enabled && local.do_snatpool ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = "k8s.f5net.com/v1"
    kind       = "F5SPKSnatpool"
    metadata   = { name = var.gateway_snatpool_name, namespace = var.flo_namespace }
    spec = {
      name                     = var.gateway_snatpool_name
      sharedSnatAddressEnabled = false
      addressList              = local.snat_address_list
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "egress_automap" {
  count = local.enabled && local.do_automap ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = "k8s.f5net.com/v3"
    kind       = "F5SPKEgress"
    metadata   = { name = "egress-cr-primary-vxlan-101", namespace = var.flo_namespace }
    spec = {
      dualStackEnabled = true
      snatType         = "SRC_TRANS_AUTOMAP"
      pseudoCNIConfig  = merge(local.egress_pseudo_cni, { vxlan = merge(local.egress_pseudo_cni.vxlan, { key = 101 }) })
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "egress_snatpool" {
  count = local.enabled && local.do_snatpool ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = "k8s.f5net.com/v3"
    kind       = "F5SPKEgress"
    metadata   = { name = "egress-cr-primary-vxlan-102", namespace = var.flo_namespace }
    spec = {
      dualStackEnabled = true
      snatType         = "SRC_TRANS_SNATPOOL"
      egressSnatpool   = var.gateway_snatpool_name
      pseudoCNIConfig  = merge(local.egress_pseudo_cni, { vxlan = merge(local.egress_pseudo_cni.vxlan, { key = 102 }) })
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [kubectl_manifest.snatpool]
}

# ── Static routes (per zone, local + remote VSI) ─────────────────────────────

resource "kubectl_manifest" "static_route" {
  for_each = local.enabled ? local.static_routes : {}
  yaml_body = yamlencode({
    apiVersion = "k8s.f5net.com/v1"
    kind       = "F5SPKStaticRoute"
    metadata   = { name = each.key, namespace = var.flo_namespace }
    spec = {
      destination = each.value.destination
      prefixLen   = 32
      type        = "gateway"
      gateway     = each.value.gateway
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

# ── VXLAN security-group rule on the cluster's worker security group ─────────
# The egress VXLAN needs the UDP port open inbound on the cluster SG
# (kube-<clusterid>). Resolve the cluster id → SG → add the rule.

data "ibm_container_vpc_cluster" "cluster" {
  count = local.enabled ? 1 : 0
  name  = var.roks_cluster_name_or_id
}

data "ibm_is_security_group" "cluster_sg" {
  count = local.enabled ? 1 : 0
  name  = "kube-${data.ibm_container_vpc_cluster.cluster[0].id}"
}

resource "ibm_is_security_group_rule" "vxlan_ingress" {
  count     = local.enabled ? 1 : 0
  group     = data.ibm_is_security_group.cluster_sg[0].id
  direction = "inbound"
  remote    = "0.0.0.0/0"

  # Top-level protocol/port_min/port_max — the nested `udp {}` block form is
  # deprecated in the IBM provider.
  protocol = "udp"
  port_min = var.gateway_vxlan_port
  port_max = var.gateway_vxlan_port
}
