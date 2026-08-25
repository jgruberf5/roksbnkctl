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
# Module: cert-manager
# Required before flo — installs cert-manager CRDs
# ============================================================

module "cert_manager" {
  source = "./modules/cert-manager"

  depends_on = [data.ibm_container_cluster_config.runtime_config]

  # Defense-in-depth: stay a no-op while the cluster is being created in the same
  # apply — the provider + cluster-config data are count=0 then (see providers.tf),
  # so live resources would crash the plan. Every correct phase already passes
  # create_roks_cluster=false, so this changes nothing there; it only neutralises
  # an accidental phase combination (e.g. a legacy monolithic apply) into a clean
  # no-op instead of a plan-time failure.
  enabled                  = var.deploy_cert_manager && !var.create_roks_cluster
  namespace                = var.cert_manager_namespace
  chart_version            = var.cert_manager_version
  registry_mirror_username = var.registry_mirror_username
  registry_mirror_password = var.registry_mirror_password
  image_repository         = var.cert_manager_image_repository
}
