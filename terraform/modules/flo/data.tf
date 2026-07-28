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
  count             = var.cluster_absent ? 0 : 1
  name              = var.roks_cluster_name_or_id
  resource_group_id = data.ibm_resource_group.resource_group.id
  depends_on        = [null_resource.roks_cluster_gate]
}

# Resolve a subnet from the first worker pool zone to learn the VPC
data "ibm_is_subnet" "cluster_subnet" {
  count      = var.cluster_absent ? 0 : 1
  identifier = try(data.ibm_container_vpc_cluster.cluster[0].worker_pools[0].zones[0].subnets[0].id, null)
}

# Learn the cluster VPC from the subnet
data "ibm_is_vpc" "cluster_vpc" {
  count      = var.cluster_absent ? 0 : 1
  identifier = try(data.ibm_is_subnet.cluster_subnet[0].vpc, null)
}
