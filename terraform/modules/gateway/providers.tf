# IBM provider — used to add the VXLAN security-group rule to the cluster's
# worker security group.
provider "ibm" {
  ibmcloud_api_key = var.ibmcloud_api_key
  region           = var.ibmcloud_cluster_region
}

# Gated on deploy_gateway so the gateway module is a TRUE no-op in the other
# phases (cluster/BNK/testing all carry this module in the shared root config,
# but only the gateway phase sets deploy_gateway=true). Without this gate it
# would read the cluster config — and download a kubeconfig — in every reuse
# phase. The Gateway phase always runs against an existing cluster, so when
# enabled create_roks_cluster is false and cluster_config resolves the live
# credentials; the localhost fallback below keeps the eager kubectl provider
# happy when count=0.
data "ibm_container_cluster_config" "cluster_config" {
  count           = var.deploy_gateway && !var.create_roks_cluster ? 1 : 0
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
