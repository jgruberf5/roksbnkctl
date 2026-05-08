# ============================================================
# Resource Group
# ============================================================

data "ibm_resource_groups" "all" {}

data "ibm_resource_group" "resource_group" {
  name = var.ibmcloud_resource_group != "" ? var.ibmcloud_resource_group : [
    for rg in data.ibm_resource_groups.all.resource_groups :
    rg.name if rg.is_default == true
  ][0]
}

# ============================================================
# Cluster (always required)
# ============================================================

data "ibm_container_vpc_cluster" "cluster" {
  name              = var.roks_cluster_name_or_id
  resource_group_id = data.ibm_resource_group.resource_group.id
  depends_on        = [null_resource.roks_cluster_gate]
}

# ============================================================
# Cluster Jumphost Data Sources
# Provider: ibm (default — cluster region)
# ============================================================

# All availability zones in the cluster region
data "ibm_is_zones" "cluster_region_zones" {
  count  = var.testing_create_cluster_jumphosts ? 1 : 0
  region = var.ibmcloud_cluster_region
}

# Ubuntu images in cluster region
data "ibm_is_images" "cluster_ubuntu_images" {
  count      = var.testing_create_cluster_jumphosts ? 1 : 0
  visibility = "public"
  status     = "available"
}

# Instance profiles in cluster region (only when auto-selecting)
data "ibm_is_instance_profiles" "cluster_profiles" {
  count = (var.testing_create_cluster_jumphosts && var.testing_jumphost_profile == "") ? 1 : 0
}

# SSH key in cluster region
data "ibm_is_ssh_key" "cluster_ssh_key" {
  count = (var.testing_create_cluster_jumphosts && var.testing_ssh_key_name != "") ? 1 : 0
  name  = var.testing_ssh_key_name
}

# Existing public gateways in the cluster region — used to attach cluster
# jumphost subnets to per-zone PGWs that already exist in the cluster VPC
# (IBM Cloud quota: one PGW per zone per VPC).
# Skipped when create_roks_cluster=true: cluster PGWs are provisioned concurrently
# in the same apply; run a second apply once the cluster is stable to add attachments.
data "ibm_is_public_gateways" "cluster_pgws" {
  count = (var.testing_create_cluster_jumphosts && !var.create_roks_cluster) ? 1 : 0
}

# ============================================================
# TGW Jumphost Data Sources
# Provider: ibm.vpc_region (client VPC region)
# ============================================================

# Existing client VPC — looked up when not creating a new one
data "ibm_is_vpc" "existing_client_vpc" {
  count    = (var.testing_create_tgw_jumphost && !var.testing_create_client_vpc) ? 1 : 0
  provider = ibm.vpc_region
  name     = var.testing_client_vpc_name
}

# First availability zone in the client VPC region (for jumphost placement)
data "ibm_is_zones" "vpc_region_zones" {
  count    = var.testing_create_tgw_jumphost ? 1 : 0
  provider = ibm.vpc_region
  region   = var.testing_client_vpc_region
}

# Ubuntu images in client VPC region
data "ibm_is_images" "tgw_ubuntu_images" {
  count      = var.testing_create_tgw_jumphost ? 1 : 0
  provider   = ibm.vpc_region
  visibility = "public"
  status     = "available"
}

# Instance profiles in client VPC region (only when auto-selecting)
data "ibm_is_instance_profiles" "tgw_profiles" {
  count    = (var.testing_create_tgw_jumphost && var.testing_jumphost_profile == "") ? 1 : 0
  provider = ibm.vpc_region
}

# SSH key in client VPC region
data "ibm_is_ssh_key" "tgw_ssh_key" {
  count    = (var.testing_create_tgw_jumphost && var.testing_ssh_key_name != "") ? 1 : 0
  provider = ibm.vpc_region
  name     = var.testing_ssh_key_name
}

# Transit Gateway (global resource — uses default provider)
data "ibm_tg_gateway" "transit_gateway" {
  count      = (var.testing_create_tgw_jumphost && var.testing_transit_gateway_name != "") ? 1 : 0
  name       = var.testing_transit_gateway_name
  depends_on = [null_resource.roks_cluster_gate]
}
