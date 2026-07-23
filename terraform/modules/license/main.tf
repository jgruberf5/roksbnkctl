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
    http = {
      source  = "hashicorp/http"
      version = ">= 3.0.0"
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
# Module: license
# ============================================================

module "license" {
  source = "./modules/license"

  providers = {
    ibm     = ibm
    http    = http
    kubectl = kubectl
  }

  # Defense-in-depth: no-op while the cluster is being created (provider +
  # cluster-config are count=0 then; see providers.tf). Correct phases already
  # pass create_roks_cluster=false, so this is inert there and only turns an
  # accidental phase combination into a clean no-op rather than a plan crash.
  enabled     = var.deploy_bnk && !var.create_roks_cluster
  bnk_cr_mode = var.bnk_cr_mode

  use_cos_bucket = var.use_cos_bucket
  jwt_token      = var.jwt_token

  ibmcloud_api_key              = var.ibmcloud_api_key
  ibmcloud_cos_bucket_region    = var.ibmcloud_cos_bucket_region
  ibmcloud_resource_group       = var.ibmcloud_resource_group
  ibmcloud_cos_instance_name    = var.ibmcloud_cos_instance_name
  ibmcloud_resources_cos_bucket = var.ibmcloud_resources_cos_bucket

  utils_namespace              = var.flo_utils_namespace
  f5_cne_subscription_jwt_file = var.f5_cne_subscription_jwt_file
  license_mode                 = var.license_mode
  flp_license_server_url       = var.flp_license_server_url
  license_server_root_ca       = var.license_server_root_ca

  kube_host              = data.ibm_container_cluster_config.runtime_config.host
  kube_token             = data.ibm_container_cluster_config.runtime_config.token
  cneinstance_dependency = var.cneinstance_dependency_id
}
