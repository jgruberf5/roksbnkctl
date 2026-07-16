# Self-contained IBM provider, configured from this module's own vars — matches
# the flp/testing modules. A Transit Gateway is a global resource, but the
# provider still needs a region for its API endpoint and for the regional
# ibm_is_vpc lookup that resolves the cluster VPC's CRN.
provider "ibm" {
  ibmcloud_api_key = var.ibmcloud_api_key
  region           = var.ibmcloud_cluster_region
}
