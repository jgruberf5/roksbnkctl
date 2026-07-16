locals {
  enabled = var.deploy_tgw_connection
}

# Resolve the cluster VPC's CRN (the connection's network_id). Works for a
# created OR a registered cluster — cluster-outputs.json records the VPC id
# either way, and this looks up its CRN + name.
data "ibm_is_vpc" "cluster_vpc" {
  count      = local.enabled ? 1 : 0
  identifier = var.cluster_vpc_id
}

# List every Transit Gateway in the account, then match var.transit_gateway
# against BOTH id and name — so the operator can pass either. The singular
# ibm_tg_gateway data source only looks up by name; the plural one is how we
# accept an id too.
data "ibm_tg_gateways" "all" {
  count = local.enabled ? 1 : 0
}

locals {
  gateways = local.enabled ? data.ibm_tg_gateways.all[0].transit_gateways : []
  matches = [
    for g in local.gateways : g
    if g.id == var.transit_gateway || g.name == var.transit_gateway
  ]
  gateway    = length(local.matches) > 0 ? local.matches[0] : null
  gateway_id = local.gateway != null ? local.gateway.id : ""

  # Unique per gateway; falls back to the VPC name when the caller passes none.
  conn_name = var.connection_name != "" ? var.connection_name : (
    local.enabled ? data.ibm_is_vpc.cluster_vpc[0].name : ""
  )
}

# Attach the cluster VPC to the resolved gateway. THIS workspace owns this
# connection: destroying the phase (`tgw disconnect`) removes only this one,
# leaving the gateway and every other cluster's connection to it intact.
resource "ibm_tg_connection" "cluster_vpc" {
  count        = local.enabled ? 1 : 0
  gateway      = local.gateway_id
  network_type = "vpc"
  name         = local.conn_name
  network_id   = data.ibm_is_vpc.cluster_vpc[0].crn

  lifecycle {
    precondition {
      condition     = length(local.matches) == 1
      error_message = "Transit Gateway ${var.transit_gateway}: matched ${length(local.matches)} gateways in this account (need exactly 1). Pass a unique name or the gateway id."
    }
  }

  timeouts {
    create = "30m"
    update = "30m"
    delete = "30m"
  }
}
