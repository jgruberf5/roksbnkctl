# ============================================================
# Root provider configurations
#
# ibm   — used directly for any root-level resources and implicitly
#          available to child modules that configure their own ibm
#          provider from ibmcloud_api_key / ibmcloud_cluster_region.
#
# null  — required because roks_cluster (and its nested GitHub module) contain legacy proxy
#          empty `provider "null" {}` block; the root must declare it.
#
# http  — required because license contains a legacy proxy
#          empty `provider "http" {}` block; the root must declare it.
# ============================================================

provider "ibm" {
  ibmcloud_api_key = var.ibmcloud_api_key
  region           = var.ibmcloud_cluster_region
}

provider "null" {}

provider "http" {}
