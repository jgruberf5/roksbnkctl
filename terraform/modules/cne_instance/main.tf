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
  }
}

# ============================================================
# Module: cneinstance
# ============================================================

module "cneinstance" {
  source = "./modules/cneinstance"

  enabled = var.deploy_bnk

  flo_namespace                      = var.flo_namespace
  utils_namespace                    = var.flo_utils_namespace
  cluster_issuer_name                = var.flo_cluster_issuer_name
  far_repo_url                       = var.far_repo_url
  f5_bigip_k8s_manifest_version      = var.f5_bigip_k8s_manifest_version
  cneinstance_ibm_trusted_profile_id = var.flo_trusted_profile_id

  kube_host  = data.ibm_container_cluster_config.runtime_config.host
  kube_token = data.ibm_container_cluster_config.runtime_config.token

  flo_deployment_id = var.flo_dependency_id != null ? var.flo_dependency_id : ""

  cneinstance_gateway_api          = true
  cneinstance_whole_cluster        = true
  cneinstance_logging_subsystem    = true
  cneinstance_metric_subsystem     = true
  cneinstance_deployment_size      = var.cneinstance_deployment_size
  cneinstance_dynamic_routing      = false
  cneinstance_firewall_acl         = true
  cneinstance_pseudocni            = true
  cneinstance_env_discovery        = false
  cneinstance_cloud_env            = true
  cneinstance_cloud_provider       = "ibm"
  cneinstance_vpc_name             = data.ibm_is_vpc.cluster_vpc.name
  cneinstance_cloud_region         = var.ibmcloud_cluster_region
  cneinstance_gslb_datacenter_name = var.cneinstance_gslb_datacenter_name
  cneinstance_network_attachments  = var.cneinstance_network_attachments
}
