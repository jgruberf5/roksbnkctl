locals {
  # Name the flavor rather than auto-select from vCPU/memory minimums.
  #
  # The auto-select returns the smallest bx2 profile meeting both minimums, and
  # profile FAMILIES differ in ways minimums do not express — local disk, network
  # bandwidth, CPU generation. Two profiles with the same vCPU and memory are not
  # interchangeable, and we only ever test specific ones. Naming the flavor makes
  # the cluster deterministic and matches what was actually validated.
  #
  # 2.4 defaults to the flavor of F5's approved reference cluster, cx3d.8x20,
  # which five consecutive clean demo runs were performed on. 2.3 keeps the
  # auto-select it has always used: it has never been validated on cx3d, and
  # changing a working line's node shape without a run to back it would be a
  # guess.
  worker_flavor_effective = (
    var.roks_worker_flavor != "" ? var.roks_worker_flavor :
    (var.bnk_line == "2.4" ? "cx3d.8x20" : "")
  )
}

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
  source         = "./modules/cluster"
  cluster_absent = var.cluster_absent

  ibmcloud_api_key = var.ibmcloud_api_key
  cluster_region   = var.ibmcloud_cluster_region
  resource_group   = var.ibmcloud_resource_group
  kubeconfig_dir   = var.kubeconfig_dir

  create_cluster         = var.create_roks_cluster
  cluster_public_gateway = var.cluster_public_gateway
  create_transit_gateway = var.create_roks_transit_gateway
  create_cos_instance    = var.create_roks_registry_cos_instance

  cluster_http_allowed_cidrs           = var.cluster_http_allowed_cidrs
  cluster_vpc_default_sg_inbound_cidrs = var.cluster_vpc_default_sg_inbound_cidrs

  cluster_vpc_name     = var.roks_cluster_vpc_name
  transit_gateway_name = var.roks_transit_gateway_name
  cos_instance_name    = var.roks_cos_instance_name

  # Existing-cluster-VPC reuse handoff (issue_sprint16_validator.md
  # Issue 2). Default false → cluster phase creates the VPC unchanged.
  # The bnk/testing phase passes true + the cluster-phase VPC id so the
  # submodule resolves data.ibm_is_vpc.existing_cluster_vpc[0] instead of
  # planning ibm_is_vpc.cluster_vpc[0] (duplicate-name failure).
  use_existing_cluster_vpc     = var.use_existing_cluster_vpc
  use_existing_cluster_subnets = var.use_existing_cluster_subnets
  existing_cluster_subnet_ids  = var.existing_cluster_subnet_ids
  cluster_vpc_cidr             = var.cluster_vpc_cidr
  existing_cluster_vpc_id      = var.existing_cluster_vpc_id

  # Creating → the name to CREATE. Adopting → the identity to LOOK UP, which
  # is carried by roks_cluster_id_or_name. The submodule resolves an adopted
  # cluster with data.ibm_container_vpc_cluster.existing_cluster{ name = ... },
  # and that data source reads THIS variable — nothing downstream ever read
  # roks_cluster_id_or_name, so adopting a cluster looked up the variable's
  # DEFAULT and 404'd ("The specified cluster could not be found"), breaking
  # every phase against a REGISTERED cluster. Same coalesce outputs.tf already
  # applies to roks_cluster_name; the data source accepts a name or an id.
  openshift_cluster_name    = var.create_roks_cluster ? var.openshift_cluster_name : var.roks_cluster_id_or_name
  openshift_cluster_version = var.openshift_cluster_version
  workers_per_zone          = var.roks_workers_per_zone
  min_worker_vcpu_count     = var.roks_min_worker_vcpu_count
  worker_flavor             = local.worker_flavor_effective
  min_worker_memory_gb      = var.roks_min_worker_memory_gb
  roksbnkctl_binary         = var.roksbnkctl_binary
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
