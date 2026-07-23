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
  use_existing_cluster_vpc          = var.use_existing_cluster_vpc
  existing_cluster_vpc_id           = var.existing_cluster_vpc_id
  kubeconfig_dir                    = "${var.kubeconfig_dir}/cluster"
  roksbnkctl_binary                 = var.roksbnkctl_binary
}


# ============================================================
# cert_manager — cert-manager
# ============================================================

module "cert_manager" {
  source = "./modules/cert_manager"

  ibmcloud_api_key        = var.ibmcloud_api_key
  ibmcloud_cluster_region = var.ibmcloud_cluster_region
  ibmcloud_resource_group = var.ibmcloud_resource_group
  roks_cluster_name_or_id = module.roks_cluster.roks_cluster_name
  cert_manager_namespace  = var.cert_manager_namespace
  cert_manager_version    = var.cert_manager_version
  # Sprint 29 air-gap mirror: when a mirror is populated, pull every cert-manager
  # component image from the in-cluster image HOST. The module appends each
  # component's jetstack/cert-manager-<comp> path (mirrored from quay.io/jetstack).
  # Empty (the default) leaves the chart's public image.repository untouched.
  cert_manager_image_repository = var.use_registry_mirror ? var.far_image_repo_url : ""
  create_roks_cluster           = var.create_roks_cluster
  deploy_cert_manager           = var.deploy_cert_manager
  bnk_cr_mode                   = var.bnk_cr_mode
  roks_cluster_dependency_id    = module.roks_cluster.cluster_ready_id
  kubeconfig_dir                = "${var.kubeconfig_dir}/cert_manager"
  registry_mirror_username      = var.registry_mirror_username
  registry_mirror_password      = var.registry_mirror_password
}


# ============================================================
# flo — F5 Lifecycle Operator (FLO)
# ============================================================

module "flo" {
  source = "./modules/flo"

  ibmcloud_api_key        = var.ibmcloud_api_key
  ibmcloud_cluster_region = var.ibmcloud_cluster_region
  ibmcloud_resource_group = var.ibmcloud_resource_group
  roks_cluster_name_or_id = module.roks_cluster.roks_cluster_name
  # Sprint 23 round-2: pass the ROOT variable directly, not the cert_manager
  # module's output. When deploy_cert_manager=false (bnk-phase override),
  # the inner cert-manager module's outputs return null (mode=managed
  # resources gated to count=0). flo's null_resource.ca_certificate at
  # modules/flo/modules/flo/main.tf:364-365 interpolates this value into a
  # template string — interpolating null produces an "Invalid template
  # interpolation value" error. The root variable is always defined
  # (defaults to "cert-manager") and matches the namespace the CLUSTER
  # phase already provisioned cert_manager into, which is exactly what flo
  # needs to know to deploy BNK resources against the existing namespace.
  cert_manager_namespace        = var.cert_manager_namespace
  far_repo_url                  = var.far_repo_url
  far_chart_repo_url            = var.far_chart_repo_url
  far_image_repo_url            = var.far_image_repo_url
  use_registry_mirror           = var.use_registry_mirror
  registry_mirror_username      = var.registry_mirror_username
  registry_mirror_password      = var.registry_mirror_password
  f5_bigip_k8s_manifest_version = var.f5_bigip_k8s_manifest_version
  use_cos_bucket                = var.use_cos_bucket
  far_service_account_b64       = var.far_service_account_b64
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
  bnk_cr_mode                   = var.bnk_cr_mode
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
  far_image_repo_url               = var.far_image_repo_url
  use_registry_mirror              = var.use_registry_mirror
  flo_namespace                    = local.flo_namespace
  flo_utils_namespace              = var.flo_utils_namespace
  f5_bigip_k8s_manifest_version    = var.f5_bigip_k8s_manifest_version
  flo_trusted_profile_id           = local.flo_trusted_profile_id
  flo_cluster_issuer_name          = local.flo_cluster_issuer_name
  cneinstance_deployment_size      = var.cneinstance_deployment_size
  cneinstance_gslb_datacenter_name = var.cneinstance_gslb_datacenter_name
  cneinstance_network_attachments  = local.cneinstance_network_attachments
  cneinstance_network_zones        = var.cneinstance_network_zones
  create_roks_cluster              = var.create_roks_cluster
  roks_cluster_dependency_id       = module.roks_cluster.cluster_ready_id
  flo_dependency_id                = module.flo.flo_ready_id
  deploy_bnk                       = var.deploy_bnk
  bnk_cr_mode                      = var.bnk_cr_mode
  kubeconfig_dir                   = "${var.kubeconfig_dir}/cne_instance"
  registry_mirror_username         = var.registry_mirror_username
  registry_mirror_password         = var.registry_mirror_password
  roksbnkctl_binary                = var.roksbnkctl_binary
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
  use_cos_bucket                = var.use_cos_bucket
  jwt_token                     = var.f5_cne_subscription_jwt
  license_mode                  = var.license_mode
  flp_license_server_url        = var.flp_license_server_url
  license_server_root_ca        = var.license_server_root_ca
  create_roks_cluster           = var.create_roks_cluster
  roks_cluster_dependency_id    = module.roks_cluster.cluster_ready_id
  cneinstance_dependency_id     = module.cne_instance.cneinstance_ready_id
  deploy_bnk                    = var.deploy_bnk
  bnk_cr_mode                   = var.bnk_cr_mode
  kubeconfig_dir                = "${var.kubeconfig_dir}/license"
  roksbnkctl_binary             = var.roksbnkctl_binary
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


# ============================================================
# gateway — data-plane ingress/egress configuration (optional phase)
# ============================================================
# Gated entirely by deploy_gateway (off by default). Runs only via the
# standalone `roksbnkctl gateway up/down` against an already-healthy BNK.

module "gateway" {
  source = "./modules/gateway"

  ibmcloud_api_key           = var.ibmcloud_api_key
  ibmcloud_cluster_region    = var.ibmcloud_cluster_region
  ibmcloud_resource_group    = var.ibmcloud_resource_group
  roks_cluster_name_or_id    = module.roks_cluster.roks_cluster_name
  roks_cluster_dependency_id = module.roks_cluster.cluster_ready_id
  create_roks_cluster        = var.create_roks_cluster
  deploy_gateway             = var.deploy_gateway
  flo_namespace              = local.flo_namespace
  # null when unset → the gateway module's install-guide zone defaults apply.
  cneinstance_network_zones = length(var.cneinstance_network_zones) > 0 ? var.cneinstance_network_zones : null
  kubeconfig_dir            = "${var.kubeconfig_dir}/gateway"

  app_namespace                = var.gateway_app_namespace
  gateway_backend_service      = var.gateway_backend_service
  gateway_backend_port         = var.gateway_backend_port
  gateway_egress_mode          = var.gateway_egress_mode
  gateway_client_subnet_local  = var.gateway_client_subnet_local
  gateway_client_subnet_remote = var.gateway_client_subnet_remote
  gateway_vxlan_port           = var.gateway_vxlan_port
}

# F5 License Proxy — optional, deployed only by `roksbnkctl flp up` (deploy_flp).
# A no-op in every other phase (its override forces deploy_flp=false). Reuses the
# BNK install's registry/mirror + COS contract so it pulls from Harbor or FAR.
module "flp" {
  source = "./modules/flp"

  deploy_flp                 = var.deploy_flp
  create_roks_cluster        = var.create_roks_cluster
  roks_cluster_name_or_id    = module.roks_cluster.roks_cluster_name
  roks_cluster_dependency_id = module.roks_cluster.cluster_ready_id
  kubeconfig_dir             = "${var.kubeconfig_dir}/flp"

  ibmcloud_api_key        = var.ibmcloud_api_key
  ibmcloud_cluster_region = var.ibmcloud_cluster_region
  ibmcloud_resource_group = var.ibmcloud_resource_group

  ibmcloud_cos_instance_name    = var.ibmcloud_cos_instance_name
  ibmcloud_resources_cos_bucket = var.ibmcloud_resources_cos_bucket
  ibmcloud_cos_bucket_region    = var.ibmcloud_cos_bucket_region
  f5_cne_far_auth_file          = var.f5_cne_far_auth_file
  f5_cne_subscription_jwt_file  = var.f5_cne_subscription_jwt_file
  scratch_dir                   = var.scratch_dir

  far_repo_url             = var.far_repo_url
  far_chart_repo_url       = var.far_chart_repo_url
  far_image_repo_url       = var.far_image_repo_url
  use_registry_mirror      = var.use_registry_mirror
  registry_mirror_username = var.registry_mirror_username
  registry_mirror_password = var.registry_mirror_password

  flp_namespace                 = var.flp_namespace
  flp_chart_version             = var.flp_chart_version
  f5_bigip_k8s_manifest_version = var.f5_bigip_k8s_manifest_version
  flp_storage_class             = var.flp_storage_class
  flp_node_port_access          = var.flp_node_port_access
  flp_node_port_source_cidrs    = var.flp_node_port_source_cidrs
  # helm post-renders the chart through the roksbnkctl binary itself (no python).
  roksbnkctl_binary = var.roksbnkctl_binary

}

# F5 License Proxy as a standalone VSI (mode: vsi) — reconstructs the
# f5-license-proxy stack as a podman pod (no Kubernetes) in the cluster VPC. Gated
# entirely by deploy_flp_vsi (set true only by the FLP phase in mode: vsi); a no-op
# otherwise. Terminates in the same flp_root_ca / flp_external_endpoint outputs the
# helm flp module produces, so the BNK phase consumes the handoff unchanged.
module "flp_vsi" {
  source = "./modules/flp_vsi"

  deploy_flp_vsi          = var.deploy_flp_vsi
  ibmcloud_api_key        = var.ibmcloud_api_key
  ibmcloud_cluster_region = var.ibmcloud_cluster_region
  ibmcloud_resource_group = var.ibmcloud_resource_group
  existing_cluster_vpc_id = var.existing_cluster_vpc_id

  flp_vsi_profile       = var.flp_vsi_profile
  flp_vsi_zone          = var.flp_vsi_zone
  flp_vsi_boot_size_gb  = var.flp_vsi_boot_size_gb
  flp_vsi_reach         = var.flp_vsi_reach
  flp_vsi_allowed_cidrs = var.flp_vsi_allowed_cidrs

  f5_bigip_k8s_manifest_version = var.f5_bigip_k8s_manifest_version
  flp_chart_version             = var.flp_chart_version
  flp_prod_jwks_b64             = var.flp_prod_jwks_b64

  ibmcloud_cos_instance_name    = var.ibmcloud_cos_instance_name
  ibmcloud_resources_cos_bucket = var.ibmcloud_resources_cos_bucket
  ibmcloud_cos_bucket_region    = var.ibmcloud_cos_bucket_region
  f5_cne_far_auth_file          = var.f5_cne_far_auth_file
  f5_cne_subscription_jwt_file  = var.f5_cne_subscription_jwt_file
  scratch_dir                   = "${var.scratch_dir}/flp-vsi"

  flp_forward_proxy_host     = var.flp_forward_proxy_host
  flp_forward_proxy_port     = var.flp_forward_proxy_port
  flp_forward_proxy_protocol = var.flp_forward_proxy_protocol
}

module "tgw_connection" {
  source = "./modules/tgw_connection"

  deploy_tgw_connection = var.deploy_tgw_connection

  ibmcloud_api_key        = var.ibmcloud_api_key
  ibmcloud_cluster_region = var.ibmcloud_cluster_region

  # The cluster VPC to attach. In the tgw phase the override sets
  # use_existing_cluster_vpc + existing_cluster_vpc_id from cluster-outputs.json,
  # so this resolves for a created OR a registered cluster.
  cluster_vpc_id  = var.existing_cluster_vpc_id
  transit_gateway = var.tgw_connection_target
  connection_name = var.tgw_connection_name
}
