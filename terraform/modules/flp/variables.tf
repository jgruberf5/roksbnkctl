# ── gating + cluster wiring ───────────────────────────────────────────────────

variable "deploy_flp" {
  description = "Deploy the F5 License Proxy. Off in every other phase's override; on only for `flp up`. When false the module is a complete no-op."
  type        = bool
  default     = false
}

variable "create_roks_cluster" {
  description = "When true the cluster is being created in this same apply (FLP never runs then); gates the plan-time cluster-config fetch."
  type        = bool
  default     = false
}

variable "roks_cluster_name_or_id" {
  description = "Name or id of the existing ROKS cluster to deploy the FLP into."
  type        = string
  default     = ""
}

variable "roks_cluster_dependency_id" {
  description = "Dependency handle from the cluster phase, deferring apply until the cluster is ready."
  type        = any
  default     = null
}

variable "kubeconfig_dir" {
  description = "Directory the ibm_container_cluster_config data source writes the kubeconfig into."
  type        = string
  default     = ""
}

# ── IBM Cloud / COS (FAR auth + subscription JWT) ────────────────────────────

variable "ibmcloud_api_key" {
  description = "IBM Cloud API key (COS/IAM auth + provider config)."
  type        = string
  sensitive   = true
  default     = ""
}

variable "ibmcloud_cluster_region" {
  description = "Region of the ROKS cluster (ibm provider)."
  type        = string
  default     = ""
}

variable "ibmcloud_resource_group" {
  description = "Resource group containing the COS instance (empty = default group)."
  type        = string
  default     = ""
}

variable "ibmcloud_cos_instance_name" {
  description = "COS service instance holding the FAR auth tarball + subscription JWT."
  type        = string
  default     = ""
}

variable "ibmcloud_resources_cos_bucket" {
  description = "COS bucket holding the FAR auth tarball + subscription JWT."
  type        = string
  default     = ""
}

variable "ibmcloud_cos_bucket_region" {
  description = "Region of the COS bucket."
  type        = string
  default     = ""
}

variable "f5_cne_far_auth_file" {
  description = "FAR auth tarball object key in the COS bucket (the _json_key_base64 SA lives inside)."
  type        = string
  default     = "f5-far-auth-key.tgz"
}

variable "f5_cne_subscription_jwt_file" {
  description = "Subscription JWT object key in the COS bucket — seeds flp-jwt-secret."
  type        = string
  default     = "subscription.jwt"
}

variable "scratch_dir" {
  description = "Working directory for the FAR-auth download/extract."
  type        = string
  default     = "/tmp/roksbnkctl-flp"
}

# ── registry / mirror (identical contract to the BNK install) ────────────────

variable "far_repo_url" {
  description = "FAR registry host (fallback when no mirror)."
  type        = string
  default     = "repo.f5.com"
}

variable "far_chart_repo_url" {
  description = "Mirror host for chart pulls (empty → coalesces to far_repo_url)."
  type        = string
  default     = ""
}

variable "far_image_repo_url" {
  description = "Mirror host for image pulls (empty → coalesces to far_repo_url)."
  type        = string
  default     = ""
}

variable "use_registry_mirror" {
  description = "Pull charts + images from the registry mirror instead of FAR. The far-secret dockerconfig is dropped; how pods then authenticate depends on the mirror: an in-cluster/ICR mirror authorizes by RBAC and needs no pull secret, while an EXTERNAL mirror (Harbor, Artifactory) gets a `mirror-secret` dockerconfig built from registry_mirror_username/password. A private mirror therefore needs no anonymous/public project."
  type        = bool
  default     = false
}

variable "registry_mirror_username" {
  description = "Basic-auth username for an EXTERNAL registry mirror (e.g. a Harbor robot/admin). Empty → in-cluster/ICR mirror (kube-token/IAM auth)."
  type        = string
  default     = ""
}

variable "registry_mirror_password" {
  description = "Basic-auth password/token for an external registry mirror. When set (with use_registry_mirror), chart + image pulls authenticate with it instead of the kube token."
  type        = string
  sensitive   = true
  default     = ""
}

# ── FLP specifics ─────────────────────────────────────────────────────────────

variable "flp_namespace" {
  description = "Namespace to install the F5 License Proxy into."
  type        = string
  default     = "f5-license-proxy"
}

variable "flp_chart_version" {
  description = "Pin the f5-license-proxy chart version. Empty (the default) → resolved from the BNK manifest, which lists charts/f5-license-proxy for the release — same as the FLO and CIS charts. Set this only to override the manifest."
  type        = string
  default     = ""
}

variable "f5_bigip_k8s_manifest_version" {
  description = "BNK manifest version. The f5-license-proxy chart version is read out of this manifest when flp_chart_version is empty."
  type        = string
  default     = ""
}

variable "flp_node_port_access" {
  description = "Expose the proxy OUTSIDE its own cluster so a BNK install in a different cluster can license through it. The chart's Service is already type NodePort (30001), but it hardcodes externalTrafficPolicy: Local — with one replica only the node running the pod answers — so this flips it to Cluster and adds the worker node IPs to the proxy's server certificate (without them the remote CWC rejects the TLS handshake)."
  type        = bool
  default     = false
}

variable "flp_node_port_source_cidrs" {
  description = "With flp_node_port_access: open the proxy's NodePort on the cluster's worker security group to these CIDRs (the consuming cluster's subnets). A LIST, because a multi-zone VPC carries one address prefix per zone — allowing only one means a consuming pod scheduled in another zone is silently dropped at the security group. Empty leaves the security group untouched."
  type        = list(string)
  default     = []
}

variable "flp_storage_class" {
  description = "Dynamic StorageClass for the FLP's PVCs. The chart ships hostPath PVs (incompatible with ROKS multi-node/non-root); a post-renderer drops them and repoints the PVCs here, so the CSI driver provisions block volumes chowned to fsGroup. Default is the ROKS VPC block default."
  type        = string
  default     = "ibmc-vpc-block-metro-10iops-tier"
}

variable "roksbnkctl_binary" {
  description = "Absolute path to the roksbnkctl binary, which helm invokes as the f5-license-proxy chart's POST-RENDERER (`roksbnkctl flp postrender`). roksbnkctl sets this to its own path automatically via TF_VAR_roksbnkctl_binary; empty falls back to `roksbnkctl` on PATH for a direct `terraform apply`. Replaces a generated python script, which made python3 an undeclared runtime dependency of the FLP phase — absent in the tools-runner container."
  type        = string
  default     = ""
}

variable "helm_registry_config" {
  description = "Path to the helm registry config file (HELM_REGISTRY_CONFIG). When set, roksbnkctl writes the OCI pull credential inline here and the helm_release resources drop repository_username/password, so the provider reads the auth instead of doing a login-and-store (which fails on Windows credential helpers). Empty = direct terraform apply, provider does its own OCI login."
  type        = string
  default     = ""
}
