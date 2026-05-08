# IBM Provider - infrastructure and IAM resources
provider "ibm" {
  ibmcloud_api_key = var.ibmcloud_api_key
  region           = var.ibmcloud_cluster_region
}

# Fetch cluster credentials only when cluster already exists (create_roks_cluster = false).
# When create_roks_cluster = true the cluster doesn't exist yet at plan time, so count=0
# and the kubernetes provider receives empty strings — safe for planning new objects.
data "ibm_container_cluster_config" "cluster_config" {
  count           = var.create_roks_cluster ? 0 : 1
  cluster_name_id = var.roks_cluster_name_or_id
  config_dir      = var.kubeconfig_dir
}

provider "kubernetes" {
  host                   = try(data.ibm_container_cluster_config.cluster_config[0].host, "")
  token                  = try(data.ibm_container_cluster_config.cluster_config[0].token, "")
  cluster_ca_certificate = try(base64decode(data.ibm_container_cluster_config.cluster_config[0].ca_certificate), null)
}

# Runtime config — deferred to apply time via roks_cluster_gate dependency.
# Used by null_resource provisioners (resource arguments, not provider config),
# so (known after apply) is fine here.
resource "null_resource" "roks_cluster_gate" {
  triggers = {
    dep = var.roks_cluster_dependency_id != null ? var.roks_cluster_dependency_id : "direct-apply"
  }
}

data "ibm_container_cluster_config" "runtime_config" {
  cluster_name_id = var.roks_cluster_name_or_id
  config_dir      = var.kubeconfig_dir
  depends_on      = [null_resource.roks_cluster_gate]
}
