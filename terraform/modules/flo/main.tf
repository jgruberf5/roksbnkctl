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
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = ">= 2.25"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.12"
    }
    kubectl = {
      source  = "alekc/kubectl"
      version = ">= 2.4.0"
    }
  }
}

# ============================================================
# Module: flo (F5 Lifecycle Operator)
# ============================================================

module "flo" {
  bnk_line = var.bnk_line
  source   = "./modules/flo"

  depends_on = [data.ibm_container_cluster_config.runtime_config, null_resource.cert_manager_gate]

  # Defense-in-depth: no-op while the cluster is being created (provider +
  # cluster-config are count=0 then; see providers.tf). Correct phases already
  # pass create_roks_cluster=false, so this is inert there and only turns an
  # accidental phase combination into a clean no-op rather than a plan crash.
  enabled                  = var.deploy_bnk && !var.create_roks_cluster
  far_repo_url             = var.far_repo_url
  far_chart_repo_url       = var.far_chart_repo_url
  far_image_repo_url       = var.far_image_repo_url
  use_registry_mirror      = var.use_registry_mirror
  registry_mirror_username = var.registry_mirror_username
  registry_mirror_password = var.registry_mirror_password

  # COS Bucket Configuration
  use_cos_bucket                = var.use_cos_bucket
  far_service_account_b64       = var.far_service_account_b64
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
  trusted_profile_sa_name       = var.flo_trusted_profile_sa_name
  trusted_profile_roles         = var.flo_trusted_profile_roles
  utils_namespace               = var.flo_utils_namespace
  kube_host                     = try(data.ibm_container_cluster_config.runtime_config[0].host, "")
  kube_token                    = try(data.ibm_container_cluster_config.runtime_config[0].token, "")

  # BIG-IP CIS Configuration
  bigip_username = var.bigip_username
  bigip_password = var.bigip_password
  bigip_url      = var.bigip_url

  # Scratch dirs — single root-level scratch_dir collapses to both
  # inner-module knobs by convention.
  scratch_dir           = var.scratch_dir
  manifest_download_dir = "${var.scratch_dir}/f5-manifest"

  openshift_cluster_name = try(data.ibm_container_vpc_cluster.cluster[0].name, "")
  openshift_cluster_crn  = try(data.ibm_container_vpc_cluster.cluster[0].crn, "")
  openshift_cluster_id   = try(data.ibm_container_vpc_cluster.cluster[0].id, "")
  cluster_vpc_id         = try(data.ibm_is_vpc.cluster_vpc[0].id, "")

  # NAD Configuration
  nad_cni_type       = "ipvlan"
  nad_interface_name = "ens3"
  nad_ipvlan_mode    = "l2"

  # Certificate Manager
  cert_manager_namespace = var.cert_manager_namespace
  roksbnkctl_binary      = var.roksbnkctl_binary
  helm_registry_config   = var.helm_registry_config
}

# Sentinel: token rotates on every apply, so this null_resource is replaced every apply.
# Its ID is always (known after apply), giving downstream modules a reliable apply-time
# dependency on flo completing — regardless of whether flo's other resources changed.
resource "null_resource" "flo_ready" {
  triggers = {
    token = try(data.ibm_container_cluster_config.runtime_config[0].token, "")
  }
  depends_on = [module.flo]
}
