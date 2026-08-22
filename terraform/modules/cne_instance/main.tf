# ============================================================
# Root Terraform Configuration
# F5 BNK Orchestrator — deploys to an existing ROKS cluster
# Modules: cert-manager → flo → cneinstance → license
# ============================================================

terraform {
  required_version = ">= 1.0"
  required_providers {
    ibm = {
      source  = "IBM-Cloud/ibm"
      version = ">= 1.60.0"
    }
    null = {
      source  = "hashicorp/null"
      version = ">= 3.2.0"
    }
    time = {
      source  = "hashicorp/time"
      version = ">= 0.9.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = ">= 2.25"
    }
    kubectl = {
      source  = "alekc/kubectl"
      version = ">= 2.4.0"
    }
  }
}

# ============================================================
# Module: cneinstance
# ============================================================

module "cneinstance" {
  bnk_line = var.bnk_line
  source   = "./modules/cneinstance"

  # Defense-in-depth: no-op while the cluster is being created (provider +
  # cluster-config are count=0 then; see providers.tf). Correct phases already
  # pass create_roks_cluster=false, so this is inert there and only turns an
  # accidental phase combination into a clean no-op rather than a plan crash.
  enabled = var.deploy_bnk && !var.create_roks_cluster

  flo_namespace                      = var.flo_namespace
  utils_namespace                    = var.flo_utils_namespace
  cluster_issuer_name                = var.flo_cluster_issuer_name
  far_repo_url                       = var.far_repo_url
  far_image_repo_url                 = var.far_image_repo_url
  use_registry_mirror                = var.use_registry_mirror
  registry_mirror_username           = var.registry_mirror_username
  registry_mirror_password           = var.registry_mirror_password
  f5_bigip_k8s_manifest_version      = var.f5_bigip_k8s_manifest_version
  cneinstance_ibm_trusted_profile_id = var.flo_trusted_profile_id
  trusted_profile_sa_name            = var.flo_trusted_profile_sa_name

  kube_host  = try(data.ibm_container_cluster_config.runtime_config[0].host, "")
  kube_token = try(data.ibm_container_cluster_config.runtime_config[0].token, "")

  flo_deployment_id         = var.flo_dependency_id != null ? var.flo_dependency_id : ""
  flo_deployment_dependency = var.flo_dependency_id

  cneinstance_gateway_api       = true
  cneinstance_whole_cluster     = true
  cneinstance_logging_subsystem = true
  cneinstance_metric_subsystem  = true
  cneinstance_deployment_size   = var.cneinstance_deployment_size
  cneinstance_dynamic_routing   = false
  cneinstance_firewall_acl      = true
  cneinstance_pseudocni         = true
  cneinstance_env_discovery     = false
  cneinstance_advanced_env      = var.cneinstance_advanced_env

  cneinstance_tcp_settings      = var.cneinstance_tcp_settings
  cneinstance_tcp_settings_name = var.cneinstance_tcp_settings_name
  # BNK 2.4 conformance with F5's reference CNEInstance (all 2.4-gated inside).
  cneinstance_tmm_replicas                   = var.cneinstance_tmm_replicas
  cneinstance_watch_namespaces               = var.cneinstance_watch_namespaces
  cneinstance_tmm_anti_affinity              = var.cneinstance_tmm_anti_affinity
  cneinstance_tmm_anti_affinity_topology_key = var.cneinstance_tmm_anti_affinity_topology_key
  cneinstance_tmm_zone_spread                = var.cneinstance_tmm_zone_spread
  cneinstance_tmm_zone_topology_key          = var.cneinstance_tmm_zone_topology_key
  cneinstance_tmm_zone_max_skew              = var.cneinstance_tmm_zone_max_skew
  cneinstance_tmm_zone_when_unsatisfiable    = var.cneinstance_tmm_zone_when_unsatisfiable
  cneinstance_tmm_pod_label                  = var.cneinstance_tmm_pod_label
  cneinstance_tmm_rolling_update             = var.cneinstance_tmm_rolling_update
  cneinstance_external_bigip                 = var.cneinstance_external_bigip
  cneinstance_external_bigip_login_secret    = var.cneinstance_external_bigip_login_secret
  cneinstance_cluster_identifier             = var.cneinstance_cluster_identifier
  cneinstance_gateway_api_version            = var.cneinstance_gateway_api_version
  cneinstance_demo_mode                      = var.cneinstance_demo_mode
  cneinstance_cloud_env                      = true
  cneinstance_cloud_provider                 = "ibm"
  cneinstance_vpc_name                       = try(data.ibm_is_vpc.cluster_vpc[0].name, "")
  cneinstance_cloud_region                   = var.ibmcloud_cluster_region
  cneinstance_gslb_datacenter_name           = var.cneinstance_gslb_datacenter_name
  cneinstance_gtm_url                        = var.cneinstance_gtm_url
  cneinstance_gtm_username                   = var.cneinstance_gtm_username
  cneinstance_gtm_password                   = var.cneinstance_gtm_password
  cneinstance_network_attachments            = var.cneinstance_network_attachments
  # null when unset → the inner module's install-guide zone defaults apply.
  cneinstance_network_zones           = length(var.cneinstance_network_zones) > 0 ? var.cneinstance_network_zones : null
  cneinstance_vlan_prefixlen          = var.cneinstance_vlan_prefixlen
  cneinstance_vlan_prefixlen_external = var.cneinstance_vlan_prefixlen_external
  cneinstance_vlan_prefixlen_internal = var.cneinstance_vlan_prefixlen_internal
  cneinstance_tmm_k8s_routes          = var.cneinstance_tmm_k8s_routes
  roksbnkctl_binary                   = var.roksbnkctl_binary
}
