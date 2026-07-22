terraform {
  required_providers {
    ibm      = { source = "IBM-Cloud/ibm", version = ">= 1.60.0" }
    tls      = { source = "hashicorp/tls", version = ">= 4.0" }
    http     = { source = "hashicorp/http", version = ">= 3.0" }
    null     = { source = "hashicorp/null", version = ">= 3.0" }
    local    = { source = "hashicorp/local", version = ">= 2.0" }
    external = { source = "hashicorp/external", version = ">= 2.0" }
  }
}

provider "ibm" {
  ibmcloud_api_key = var.ibmcloud_api_key
  region           = var.ibmcloud_cluster_region
}
