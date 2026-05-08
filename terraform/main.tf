# ============================================================
# F5 BIG-IP Next for Kubernetes 2.3 — Root Module
#
# Execution order (enforced by Terraform dependency graph):
#
#   roks_cluster ──► cert_manager ──► flo
#                └──────────────────────► cne_instance  ← also wired from flo outputs
#                └──────────────────────► license        ← depends on cne_instance
#                └──────────────────────► testing
#
# Cross-module wiring:
#   roks_cluster.roks_cluster_name         → all modules: roks_cluster_name_or_id
#   roks_cluster.transit_gateway_name      → testing: testing_transit_gateway_name
#   roks_cluster.cluster_ready_id          → all modules: roks_cluster_dependency_id
#   cert_manager.cert_manager_namespace    → flo: cert_manager_namespace
#   flo.flo_namespace                      → cne_instance: flo_namespace
#   flo.flo_trusted_profile_id             → cne_instance: flo_trusted_profile_id
#   flo.flo_cluster_issuer_name            → cne_instance: flo_cluster_issuer_name
#   flo.cneinstance_network_attachments    → cne_instance: cneinstance_network_attachments
#   cne_instance.cneinstance_ready_id      → license: cneinstance_dependency_id
#
# Legacy module note:
#   All modules declare their own provider blocks, making them legacy modules.
#   They cannot accept providers, count, for_each, or depends_on at call sites.
# ============================================================


# ============================================================
# roks_cluster — ROKS Cluster 4.18 + Transit Gateway
# ============================================================

module "roks_cluster" {
  source = "./modules/roks_cluster"

  ibmcloud_api_key                  = var.ibmcloud_api_key
  ibmcloud_cluster_region           = var.ibmcloud_cluster_region
  ibmcloud_resource_group           = var.ibmcloud_resource_group
  create_roks_cluster               = var.create_roks_cluster
  roks_cluster_id_or_name           = var.roks_cluster_id_or_name
  create_roks_transit_gateway       = var.create_roks_transit_gateway
  create_roks_registry_cos_instance = var.create_roks_registry_cos_instance
  roks_cluster_vpc_name             = var.roks_cluster_vpc_name
  openshift_cluster_name            = var.openshift_cluster_name
  openshift_cluster_version         = var.openshift_cluster_version
  roks_workers_per_zone             = var.roks_workers_per_zone
  roks_min_worker_vcpu_count        = var.roks_min_worker_vcpu_count
  roks_min_worker_memory_gb         = var.roks_min_worker_memory_gb
  roks_cos_instance_name            = var.roks_cos_instance_name
  roks_transit_gateway_name         = var.roks_transit_gateway_name
}


# ============================================================
# cert_manager — cert-manager
# ============================================================

module "cert_manager" {
  source = "./modules/cert_manager"

  ibmcloud_api_key           = var.ibmcloud_api_key
  ibmcloud_cluster_region    = var.ibmcloud_cluster_region
  ibmcloud_resource_group    = var.ibmcloud_resource_group
  roks_cluster_name_or_id    = module.roks_cluster.roks_cluster_name
  cert_manager_namespace     = var.cert_manager_namespace
  cert_manager_version       = var.cert_manager_version
  create_roks_cluster        = var.create_roks_cluster
  roks_cluster_dependency_id = module.roks_cluster.cluster_ready_id
  kubeconfig_dir             = "${var.kubeconfig_dir}/cert_manager"
}


# ============================================================
# flo — F5 Lifecycle Operator (FLO)
# ============================================================

module "flo" {
  source = "./modules/flo"

  ibmcloud_api_key              = var.ibmcloud_api_key
  ibmcloud_cluster_region       = var.ibmcloud_cluster_region
  ibmcloud_resource_group       = var.ibmcloud_resource_group
  roks_cluster_name_or_id       = module.roks_cluster.roks_cluster_name
  cert_manager_namespace        = module.cert_manager.cert_manager_namespace
  far_repo_url                  = var.far_repo_url
  f5_bigip_k8s_manifest_version = var.f5_bigip_k8s_manifest_version
  use_cos_bucket                = true
  ibmcloud_cos_bucket_region    = var.ibmcloud_cos_bucket_region
  ibmcloud_cos_instance_name    = var.ibmcloud_cos_instance_name
  ibmcloud_resources_cos_bucket = var.ibmcloud_resources_cos_bucket
  f5_cne_far_auth_file          = var.f5_cne_far_auth_file
  f5_cne_subscription_jwt_file  = var.f5_cne_subscription_jwt_file
  flo_namespace                 = var.flo_namespace
  flo_utils_namespace           = var.flo_utils_namespace
  bigip_username                = var.bigip_username
  bigip_password                = var.bigip_password
  bigip_url                     = var.bigip_url
  create_roks_cluster           = var.create_roks_cluster
  roks_cluster_dependency_id    = module.roks_cluster.cluster_ready_id
  cert_manager_dependency_id    = module.cert_manager.cert_manager_ready_id
  deploy_bnk                    = var.deploy_bnk
  kubeconfig_dir                = "${var.kubeconfig_dir}/flo"
  scratch_dir                   = var.scratch_dir
}

locals {
  # Wire flo outputs into cne_instance inputs, falling back to root variables
  # when flo output is not yet in state, errors out, or is null (e.g. when
  # var.deploy_bnk = false disables the inner flo module and its outputs return null).
  _flo_namespace_out                   = try(module.flo.flo_namespace, null)
  _flo_trusted_profile_id_out          = try(module.flo.flo_trusted_profile_id, null)
  _flo_cluster_issuer_name_out         = try(module.flo.flo_cluster_issuer_name, null)
  _flo_cneinstance_network_attachments = try(module.flo.cneinstance_network_attachments, null)

  flo_namespace                   = local._flo_namespace_out != null ? local._flo_namespace_out : var.flo_namespace
  flo_trusted_profile_id          = local._flo_trusted_profile_id_out != null ? local._flo_trusted_profile_id_out : var.flo_trusted_profile_id
  flo_cluster_issuer_name         = local._flo_cluster_issuer_name_out != null ? local._flo_cluster_issuer_name_out : var.flo_cluster_issuer_name
  cneinstance_network_attachments = local._flo_cneinstance_network_attachments != null ? local._flo_cneinstance_network_attachments : var.cneinstance_network_attachments
}


# ============================================================
# cne_instance — CNEInstance
# ============================================================

module "cne_instance" {
  source = "./modules/cne_instance"

  ibmcloud_api_key                 = var.ibmcloud_api_key
  ibmcloud_cluster_region          = var.ibmcloud_cluster_region
  ibmcloud_resource_group          = var.ibmcloud_resource_group
  roks_cluster_name_or_id          = module.roks_cluster.roks_cluster_name
  far_repo_url                     = var.far_repo_url
  flo_namespace                    = local.flo_namespace
  flo_utils_namespace              = var.flo_utils_namespace
  f5_bigip_k8s_manifest_version    = var.f5_bigip_k8s_manifest_version
  flo_trusted_profile_id           = local.flo_trusted_profile_id
  flo_cluster_issuer_name          = local.flo_cluster_issuer_name
  cneinstance_deployment_size      = var.cneinstance_deployment_size
  cneinstance_gslb_datacenter_name = var.cneinstance_gslb_datacenter_name
  cneinstance_network_attachments  = local.cneinstance_network_attachments
  create_roks_cluster              = var.create_roks_cluster
  roks_cluster_dependency_id       = module.roks_cluster.cluster_ready_id
  flo_dependency_id                = module.flo.flo_ready_id
  deploy_bnk                       = var.deploy_bnk
  kubeconfig_dir                   = "${var.kubeconfig_dir}/cne_instance"
}


# ============================================================
# license — License
# ============================================================

module "license" {
  source    = "./modules/license"
  providers = { http = http }

  ibmcloud_api_key              = var.ibmcloud_api_key
  ibmcloud_cluster_region       = var.ibmcloud_cluster_region
  ibmcloud_resource_group       = var.ibmcloud_resource_group
  ibmcloud_cos_bucket_region    = var.ibmcloud_cos_bucket_region
  ibmcloud_cos_instance_name    = var.ibmcloud_cos_instance_name
  ibmcloud_resources_cos_bucket = var.ibmcloud_resources_cos_bucket
  roks_cluster_name_or_id       = module.roks_cluster.roks_cluster_name
  flo_utils_namespace           = var.flo_utils_namespace
  f5_cne_subscription_jwt_file  = var.f5_cne_subscription_jwt_file
  license_mode                  = var.license_mode
  create_roks_cluster           = var.create_roks_cluster
  roks_cluster_dependency_id    = module.roks_cluster.cluster_ready_id
  cneinstance_dependency_id     = module.cne_instance.cneinstance_ready_id
  deploy_bnk                    = var.deploy_bnk
  kubeconfig_dir                = "${var.kubeconfig_dir}/license"
}


# ============================================================
# testing — Testing Jumphosts
# ============================================================

module "testing" {
  source = "./modules/testing"

  ibmcloud_api_key                     = var.ibmcloud_api_key
  ibmcloud_cluster_region              = var.ibmcloud_cluster_region
  ibmcloud_resource_group              = var.ibmcloud_resource_group
  roks_cluster_name_or_id              = module.roks_cluster.roks_cluster_name
  testing_transit_gateway_name         = module.roks_cluster.transit_gateway_name
  testing_create_tgw_jumphost          = var.testing_create_tgw_jumphost
  testing_create_cluster_jumphosts     = var.testing_create_cluster_jumphosts
  testing_ssh_key_name                 = var.testing_ssh_key_name
  testing_jumphost_profile             = var.testing_jumphost_profile
  testing_min_vcpu_count               = var.testing_min_vcpu_count
  testing_min_memory_gb                = var.testing_min_memory_gb
  testing_create_client_vpc            = var.testing_create_client_vpc
  testing_client_vpc_name              = var.testing_client_vpc_name
  testing_client_vpc_region            = var.testing_client_vpc_region
  testing_tgw_jumphost_name            = var.testing_tgw_jumphost_name
  testing_cluster_jumphost_name_prefix = var.testing_cluster_jumphost_name_prefix
  cluster_vpc_id                       = module.roks_cluster.roks_cluster_vpc_id
  roks_cluster_dependency_id           = module.roks_cluster.cluster_ready_id
  create_roks_cluster                  = var.create_roks_cluster
}
