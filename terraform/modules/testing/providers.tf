# Default IBM provider — used for cluster data source lookups (cluster region)
provider "ibm" {
  ibmcloud_api_key = var.ibmcloud_api_key
  region           = var.ibmcloud_cluster_region
}

# VPC-region IBM provider — used for all jumphost and VPC resources.
# When using the cluster VPC (no client VPC created or specified), set
# client_vpc_region equal to ibmcloud_cluster_region.
provider "ibm" {
  alias            = "vpc_region"
  ibmcloud_api_key = var.ibmcloud_api_key
  region           = var.testing_client_vpc_region
}

# Gate resource — ensures cluster/TGW data sources are deferred to apply time
# when roks_cluster is being created (roks_cluster_dependency_id is (known after apply)).
resource "null_resource" "roks_cluster_gate" {
  triggers = {
    dep = var.roks_cluster_dependency_id != null ? var.roks_cluster_dependency_id : "direct-apply"
  }
}
