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
  }
}

# ============================================================
# Module: license
# ============================================================

module "license" {
  source = "./modules/license"

  providers = {
    ibm  = ibm
    http = http
  }

  enabled = var.deploy_bnk

  use_cos_bucket = true
  jwt_token      = ""

  ibmcloud_api_key              = var.ibmcloud_api_key
  ibmcloud_cos_bucket_region    = var.ibmcloud_cos_bucket_region
  ibmcloud_resource_group       = var.ibmcloud_resource_group
  ibmcloud_cos_instance_name    = var.ibmcloud_cos_instance_name
  ibmcloud_resources_cos_bucket = var.ibmcloud_resources_cos_bucket

  utils_namespace              = var.flo_utils_namespace
  f5_cne_subscription_jwt_file = var.f5_cne_subscription_jwt_file
  license_mode                 = var.license_mode

  kube_host              = data.ibm_container_cluster_config.runtime_config.host
  kube_token             = data.ibm_container_cluster_config.runtime_config.token
  cneinstance_dependency = var.cneinstance_dependency_id
}
