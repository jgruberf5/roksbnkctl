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
  # Defense-in-depth: no-op while the cluster is being created (the module's
  # provider is count=0 then; see providers.tf). The gateway phase already runs
  # with create_roks_cluster=false, so this is inert in every correct flow and
  # only turns an accidental phase combination into a clean no-op, not a crash.
  enabled = var.deploy_gateway && !var.create_roks_cluster

  # The 2.3 network surface. A 2.4 controller runs with USE_GATEWAY_SETTINGS=true
  # and ignores the F5SPK* family and the F5BnkGateway entirely, reading Infra +
  # GatewaySettings instead (#168) — so creating them there leaves objects nothing
  # reads. `!= "2.4"` keeps the 2.3 behaviour for an unrecognised line.
  line_pre_24 = var.bnk_line != "2.4"

  zone_names = [for i in range(length(var.cneinstance_network_zones)) : "${var.ibmcloud_cluster_region}-${i + 1}"]

  # The GatewayClass controllerName is not a free-form label — it is the string
  # the CNE controller matches itself against, and the controller identifies as
  # the CNEInstance's own name, which is "<flo_namespace>-f5-cne-controller".
  # Deriving it here is what keeps the two in step; a literal cannot, because the
  # namespace is a variable. The BNK 2.4 install guide states the requirement
  # outright ("Make sure controller name(f5-bnk-f5-cne-controller) is same as in
  # CNEInstance CR"), but it holds for 2.3 just as much.
  #
  # Empty var → derive. A non-default bnk.flo_namespace previously produced
  # "f5.com/f5-bnk-f5-cne-controller" against a controller calling itself
  # something else: GatewayClass ACCEPTED=<none>, Gateway never programmed, and
  # nothing in the apply failed. With the default namespace the derived value is
  # byte-identical to the old literal, so no existing deployment moves.
  gateway_controller_name = (
    var.gateway_controller_name != ""
    ? var.gateway_controller_name
    : "f5.com/${var.flo_namespace}-f5-cne-controller"
  )

  # ── Route-kind catalogue ───────────────────────────────────────────────────
  #
  # DATA, not a resource per kind. The set of route kinds BNK can serve is a
  # property of the Gateway API channel it installs, and that changes by BNK
  # line: 2.3 pins 1.4.1 STANDARD, which contains HTTPRoute and GRPCRoute and
  # NOT TCPRoute/TLSRoute/UDPRoute — BNK ships L4Route
  # (gateway.k8s.f5net.com/v1) for TCP instead. 2.4 is expected to move to the
  # experimental channel, which adds the three upstream L4 kinds; that is a row
  # here, not a rewrite, which is the whole reason this is a map.
  #
  # `listener` is which Gateway listener the kind can attach to. It is not
  # cosmetic: a GRPCRoute attaches to an HTTP listener, an L4Route cannot, and a
  # route on the wrong listener is created successfully and then never accepted.
  route_kind_catalog = {
    GRPCRoute = { group = "gateway.networking.k8s.io", api = "gateway.networking.k8s.io/v1", listener = "http" }
    L4Route   = { group = "gateway.k8s.f5net.com", api = "gateway.k8s.f5net.com/v1", listener = "tcp" }
  }
  route_examples = { for k in var.gateway_route_examples : k => local.route_kind_catalog[k] }
  want_l4        = contains(var.gateway_route_examples, "L4Route")

  # The HTTP listener must ALLOW every kind that attaches to it. This was
  # hard-coded to HTTPRoute, so a GRPCRoute could be created and would never
  # attach — the Gateway refused it and nothing in the apply failed.
  http_listener_kinds = concat(
    [{ kind = "HTTPRoute" }],
    [for k, v in local.route_examples : { group = v.group, kind = k } if v.listener == "http"],
  )

  gateway_listeners = concat(
    [{
      name     = "http"
      protocol = "HTTP"
      port     = var.gateway_listener_port
      allowedRoutes = {
        namespaces = { from = "Same" }
        kinds      = local.http_listener_kinds
      }
    }],
    local.want_l4 ? [{
      name     = "tcp"
      protocol = "TCP"
      port     = var.gateway_l4_listener_port
      allowedRoutes = {
        namespaces = { from = "Same" }
        kinds      = [{ group = "gateway.k8s.f5net.com", kind = "L4Route" }]
      }
    }] : [],
  )

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

  # Per-zone static routes: one F5SPKStaticRoute per (client subnet × zone),
  # each via that zone's external-VLAN gateway (ext_vlan .1). The client
  # subnets are LISTS, so several local clients (e.g. per-AZ cluster
  # jumphosts in different subnets) and remote clients each get a return
  # route in every zone. Empty lists → no client routes.
  static_routes = merge(concat(
    [for i, z in var.cneinstance_network_zones : {
      for j, dest in var.gateway_client_subnet_local :
      "static-route-local-z${i + 1}-${j + 1}" => {
        # F5SPKStaticRoute.spec.destination is a BARE network IP (no /prefix);
        # the mask lives in spec.prefixLen. The client subnets are CIDRs
        # (e.g. "10.241.0.0/24"), so split: address → destination, prefix →
        # prefixLen (defaulting to /32 if a bare host IP was supplied).
        destination = split("/", dest)[0]
        prefix_len  = length(split("/", dest)) > 1 ? tonumber(split("/", dest)[1]) : 32
        gateway     = cidrhost(z.ext_vlan_cidr, var.gateway_static_route_gw_host)
      }
    }],
    [for i, z in var.cneinstance_network_zones : {
      for j, dest in var.gateway_client_subnet_remote :
      "static-route-remote-z${i + 1}-${j + 1}" => {
        destination = split("/", dest)[0]
        prefix_len  = length(split("/", dest)) > 1 ? tonumber(split("/", dest)[1]) : 32
        gateway     = cidrhost(z.ext_vlan_cidr, var.gateway_static_route_gw_host)
      }
    }],
  )...)

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
      controllerName = local.gateway_controller_name
      description    = "F5 BIG-IP Kubernetes Gateway"
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "bnk_gateway" {
  count = local.enabled && local.line_pre_24 ? 1 : 0
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

# ONE NAMESPACE, CONTINUED (#66).
#
# The BNK phase learned to collapse flo_namespace and flo_utils_namespace into
# one name. A customer who wants ONE namespace means all of it, so they set
# gateway.app_namespace to the same value too — and this resource had no guard.
#
# It would not merely be redundant, it would FAIL. The gateway phase runs against
# its own state dir with deploy_bnk = false, so kubernetes_namespace_v1.flo is
# count 0 in this root and terraform has no idea the namespace already exists. It
# plans a create, and the apply dies on `namespaces "f5-bnk" already exists`.
#
# Guarded in the same direction as the BNK phase's pair: the namespace that
# something else owns is the conditional one. When the names are equal the BNK
# phase owns it, exactly as flo owns it there.
resource "kubernetes_namespace_v1" "app" {
  count = local.enabled && var.app_namespace != var.flo_namespace ? 1 : 0
  metadata {
    name = var.app_namespace
  }
}

# The Gateway itself is line-conditional in two ways (#173):
#
#   parametersRef — 2.3 points at k8s.f5net.com/F5BnkGateway; 2.4 points at
#   gateway.k8s.f5.com/GatewaySettings, the CR emitted in config_24.tf.
#
#   namespace — the 2.4 guide is explicit that "GatewaySettings and Gateways
#   [are] to be applied in Same Namespace", and puts GatewaySettings, Gateway and
#   EgressGateway all in the FLO namespace. On 2.3 the Gateway lives in the
#   application namespace. The HTTPRoute stays in the app namespace on both lines
#   and reaches across via parentRefs.namespace.
locals {
  gateway_ns_effective = local.line_pre_24 ? var.app_namespace : var.flo_namespace

  gateway_parameters_ref = local.line_pre_24 ? {
    group = "k8s.f5net.com"
    kind  = "F5BnkGateway"
    name  = var.gateway_bnkgateway_name
    } : {
    group = "gateway.k8s.f5.com"
    kind  = "GatewaySettings"
    name  = var.gateway_settings_name
  }
}

resource "kubectl_manifest" "gateway" {
  count = local.enabled ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = "gateway.networking.k8s.io/v1"
    kind       = "Gateway"
    metadata   = { name = var.gateway_name, namespace = local.gateway_ns_effective }
    spec = {
      infrastructure = {
        parametersRef = local.gateway_parameters_ref
      }
      gatewayClassName = var.gateway_class_name
      listeners        = local.gateway_listeners
    }
  })
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  # bnk_gateway is 2.3-only and count-gated; kubectl_manifest handles an empty
  # list, so one depends_on serves both lines. gateway_settings_24 is likewise
  # empty on 2.3.
  depends_on = [
    kubectl_manifest.bnk_gateway,
    kubectl_manifest.gateway_settings_24,
    kubernetes_namespace_v1.app,
  ]
}

resource "kubectl_manifest" "http_route" {
  count = local.enabled ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = "gateway.networking.k8s.io/v1"
    kind       = "HTTPRoute"
    metadata   = { name = var.gateway_route_name, namespace = var.app_namespace }
    spec = {
      # The route stays in the APPLICATION namespace on both lines; only the
      # Gateway moves. On 2.4 that makes this a genuine cross-namespace parentRef
      # (#173), which is what the guide's own example does.
      parentRefs = [{
        name        = var.gateway_name
        namespace   = local.gateway_ns_effective
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

# ── Route examples ───────────────────────────────────────────────────────────
#
# One resource per requested kind, rendered from the catalogue. They point at
# the SAME backend Service as the default HTTPRoute, so enabling them needs no
# extra workload — the example is the routing, not the application.
#
# GRPCRoute. Attaches to the HTTP listener. The empty rule matches all gRPC
# traffic on the listener, which is the honest minimal example: a `matches`
# block on service/method looks more complete but encodes a service name that
# does not exist in the reader's cluster.
resource "kubectl_manifest" "grpc_route_example" {
  count = local.enabled && contains(var.gateway_route_examples, "GRPCRoute") ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = local.route_kind_catalog["GRPCRoute"].api
    kind       = "GRPCRoute"
    metadata   = { name = "${var.gateway_route_name}-grpc", namespace = var.app_namespace }
    spec = {
      # The route stays in the APPLICATION namespace on both lines; only the
      # Gateway moves. On 2.4 that makes this a genuine cross-namespace parentRef
      # (#173), which is what the guide's own example does.
      parentRefs = [{
        name        = var.gateway_name
        namespace   = local.gateway_ns_effective
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

# L4Route — BNK's own CRD, not an upstream Gateway API kind. It exists because
# the 1.4.1 STANDARD channel has no TCPRoute; this is how BNK does L4. It
# attaches to the TCP listener that local.want_l4 added, and `protocol` carries
# no enum on the CRD, so the accepted values are controller-validated.
resource "kubectl_manifest" "l4_route_example" {
  count = local.enabled && local.want_l4 ? 1 : 0
  yaml_body = yamlencode({
    apiVersion = local.route_kind_catalog["L4Route"].api
    kind       = "L4Route"
    metadata   = { name = "${var.gateway_route_name}-l4", namespace = var.app_namespace }
    spec = {
      # As above: the route stays in the app namespace, the parentRef follows the
      # Gateway to whichever namespace the line puts it in (#173).
      parentRefs = [{
        name        = var.gateway_name
        namespace   = local.gateway_ns_effective
        sectionName = "tcp"
      }]
      protocol = "TCP"
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
  count = local.enabled && local.do_snatpool && local.line_pre_24 ? 1 : 0
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
  count = local.enabled && local.do_automap && local.line_pre_24 ? 1 : 0
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
  count = local.enabled && local.do_snatpool && local.line_pre_24 ? 1 : 0
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
      prefixLen   = each.value.prefix_len
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
  count = local.enabled && local.line_pre_24 ? 1 : 0
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
