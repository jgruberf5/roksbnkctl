# ============================================================
# ROKS Cluster + Transit Gateway
# ============================================================

terraform {
  required_version = ">= 1.3"
  required_providers {
    ibm = {
      source  = "IBM-Cloud/ibm"
      version = ">= 1.60.0"
    }
    null = {
      source  = "hashicorp/null"
      version = ">= 3.0"
    }
  }
}

module "cluster" {
  source = "./modules/cluster"

  ibmcloud_api_key = var.ibmcloud_api_key
  cluster_region   = var.ibmcloud_cluster_region
  resource_group   = var.ibmcloud_resource_group

  create_cluster         = var.create_roks_cluster
  create_transit_gateway = var.create_roks_transit_gateway
  create_cos_instance    = var.create_roks_registry_cos_instance

  cluster_vpc_name     = var.roks_cluster_vpc_name
  transit_gateway_name = var.roks_transit_gateway_name
  cos_instance_name    = var.roks_cos_instance_name

  openshift_cluster_name    = var.openshift_cluster_name
  openshift_cluster_version = var.openshift_cluster_version
  workers_per_zone          = var.roks_workers_per_zone
  min_worker_vcpu_count     = var.roks_min_worker_vcpu_count
  min_worker_memory_gb      = var.roks_min_worker_memory_gb
}

# Sentinel: captures apply-time IDs so downstream modules can declare a real
# dependency on roks_cluster completing without breaking plan-time evaluation.
resource "null_resource" "cluster_ready" {
  count = var.create_roks_cluster ? 1 : 0
  triggers = {
    cluster_id         = module.cluster.cluster_id
    transit_gateway_id = var.create_roks_transit_gateway ? module.cluster.transit_gateway_id : "none"
  }
}
