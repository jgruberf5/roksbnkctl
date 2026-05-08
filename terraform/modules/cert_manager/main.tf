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
# Module: cert-manager
# Required before flo — installs cert-manager CRDs
# ============================================================

module "cert_manager" {
  source = "./modules/cert-manager"

  depends_on = [data.ibm_container_cluster_config.runtime_config]

  enabled               = true
  namespace             = var.cert_manager_namespace
  chart_version         = var.cert_manager_version
  post_deployment_delay = 30
  kube_host             = data.ibm_container_cluster_config.runtime_config.host
  kube_token            = data.ibm_container_cluster_config.runtime_config.token
}
