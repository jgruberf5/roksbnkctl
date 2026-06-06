# IBM provider — used to add the VXLAN security-group rule to the cluster's
# worker security group.
provider "ibm" {
  ibmcloud_api_key = var.ibmcloud_api_key
  region           = var.ibmcloud_cluster_region
}

# The Gateway phase always runs against an EXISTING cluster (it deploys AFTER
# BNK), so create_roks_cluster is false and cluster_config resolves the live
# credentials. The count-gate + localhost fallback mirror the cne_instance
# module so an accidental create-time plan still configures cleanly.
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

# alekc/kubectl — applies the Gateway-API + F5SPK CRs with no plan-time CRD
# schema lookup (the CRDs ship with the BNK manifest installed in the BNK
# phase). Eager config validation → non-empty localhost fallback; never dialed
# when the resources are count-gated off.
provider "kubectl" {
  host                   = try(data.ibm_container_cluster_config.cluster_config[0].host, "https://localhost")
  token                  = try(data.ibm_container_cluster_config.cluster_config[0].token, "")
  cluster_ca_certificate = try(base64decode(data.ibm_container_cluster_config.cluster_config[0].ca_certificate), null)
  load_config_file       = false
}
