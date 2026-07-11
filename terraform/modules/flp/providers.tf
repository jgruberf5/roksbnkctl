# IBM provider — used only for the ibm_container_cluster_config data source that
# resolves live cluster credentials for the kubernetes/helm providers below.
provider "ibm" {
  ibmcloud_api_key = var.ibmcloud_api_key
  region           = var.ibmcloud_cluster_region
}

# Gated on deploy_flp so the FLP module is a TRUE no-op in the other phases
# (cluster/BNK/testing/gateway all carry this module in the shared root config,
# but only the FLP phase sets deploy_flp=true). Without this gate it would read
# the cluster config — and download a kubeconfig — in every reuse phase. The FLP
# phase always runs against an existing cluster, so when enabled
# create_roks_cluster is false and cluster_config resolves the live credentials;
# the empty fallbacks keep the lazy kubernetes/helm providers happy when count=0.
data "ibm_container_cluster_config" "cluster_config" {
  count           = var.deploy_flp && !var.create_roks_cluster ? 1 : 0
  cluster_name_id = var.roks_cluster_name_or_id
  config_dir      = var.kubeconfig_dir
}

provider "kubernetes" {
  host                   = try(data.ibm_container_cluster_config.cluster_config[0].host, "")
  token                  = try(data.ibm_container_cluster_config.cluster_config[0].token, "")
  cluster_ca_certificate = try(base64decode(data.ibm_container_cluster_config.cluster_config[0].ca_certificate), null)
}

provider "helm" {
  kubernetes {
    host                   = try(data.ibm_container_cluster_config.cluster_config[0].host, "")
    token                  = try(data.ibm_container_cluster_config.cluster_config[0].token, "")
    cluster_ca_certificate = try(base64decode(data.ibm_container_cluster_config.cluster_config[0].ca_certificate), null)
  }
}
