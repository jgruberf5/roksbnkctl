# ============================================================
# Data Sources
# Resolve resource group, cluster, VPC, and optional transit gateway
# ============================================================

data "ibm_resource_groups" "all" {}

data "ibm_resource_group" "resource_group" {
  name = var.ibmcloud_resource_group != "" ? var.ibmcloud_resource_group : [
    for rg in data.ibm_resource_groups.all.resource_groups :
    rg.name if rg.is_default == true
  ][0]
}

# Look up the existing OpenShift cluster (skip when we're creating it — it doesn't exist yet)
data "ibm_container_vpc_cluster" "cluster" {
  count             = var.create_roks_cluster ? 0 : 1
  name              = var.roks_cluster_name_or_id
  resource_group_id = data.ibm_resource_group.resource_group.id
}
