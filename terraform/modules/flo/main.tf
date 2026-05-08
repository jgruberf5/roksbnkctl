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
    local = {
      source  = "hashicorp/local"
      version = ">= 2.4.0"
    }
    http = {
      source  = "hashicorp/http"
      version = ">= 3.0.0"
    }
    external = {
      source  = "hashicorp/external"
      version = ">= 2.3.0"
    }
    time = {
      source  = "hashicorp/time"
      version = ">= 0.9.0"
    }
  }
}

# ============================================================
# Module: flo (F5 Lifecycle Operator)
# ============================================================

module "flo" {
  source = "./modules/flo"

  depends_on = [data.ibm_container_cluster_config.runtime_config, null_resource.cert_manager_gate]

  enabled = var.deploy_bnk

  cert_manager_crd_ready = true

  far_repo_url = var.far_repo_url

  # COS Bucket Configuration
  use_cos_bucket                = var.use_cos_bucket
  ibmcloud_api_key              = var.ibmcloud_api_key
  ibmcloud_cos_bucket_region    = var.ibmcloud_cos_bucket_region
  ibmcloud_resource_group       = var.ibmcloud_resource_group
  ibmcloud_cos_instance_name    = var.ibmcloud_cos_instance_name
  ibmcloud_resources_cos_bucket = var.ibmcloud_resources_cos_bucket
  f5_cne_far_auth_file          = var.f5_cne_far_auth_file
  f5_cne_subscription_jwt_file  = var.f5_cne_subscription_jwt_file

  # FLO Configuration
  f5_bigip_k8s_manifest_version = var.f5_bigip_k8s_manifest_version
  flo_namespace                 = var.flo_namespace
  utils_namespace               = var.flo_utils_namespace
  kube_host                     = data.ibm_container_cluster_config.runtime_config.host
  kube_token                    = data.ibm_container_cluster_config.runtime_config.token

  # BIG-IP CIS Configuration
  bigip_username = var.bigip_username
  bigip_password = var.bigip_password
  bigip_url      = var.bigip_url

  # Scratch dirs — single root-level scratch_dir collapses to both
  # inner-module knobs by convention.
  scratch_dir           = var.scratch_dir
  manifest_download_dir = "${var.scratch_dir}/f5-manifest"

  openshift_cluster_name = data.ibm_container_vpc_cluster.cluster.name
  openshift_cluster_crn  = data.ibm_container_vpc_cluster.cluster.crn
  cluster_vpc_id         = data.ibm_is_vpc.cluster_vpc.id

  # NAD Configuration
  nad_cni_type       = "ipvlan"
  nad_interface_name = "ens3"
  nad_ipvlan_mode    = "l2"

  # Certificate Manager
  cert_manager_namespace = var.cert_manager_namespace
}

# Sentinel: token rotates on every apply, so this null_resource is replaced every apply.
# Its ID is always (known after apply), giving downstream modules a reliable apply-time
# dependency on flo completing — regardless of whether flo's other resources changed.
resource "null_resource" "flo_ready" {
  triggers = {
    token = data.ibm_container_cluster_config.runtime_config.token
  }
  depends_on = [module.flo]
}
