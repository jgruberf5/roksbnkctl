variable "enabled" {
  description = "Enable or disable the BNK orchestrator module deployment"
  type        = bool
  default     = false
}

# Directory for artifacts that have to survive across `terraform apply`
# invocations (FAR auth tarball, extracted JSON read later by data.local_file
# resources). The default lives under .bnk/ in the bind-mounted cwd so the
# bnk runner image preserves these files between docker runs. Inside the
# image /tmp is lost when the container exits, which would orphan these
# files between the null_resource that created them and the data.local_file
# that reads them on a later apply.
variable "scratch_dir" {
  description = "Persistent scratch directory for cross-apply artifacts (FAR tarball, extracted JSON). Inside the bnk runner this lives in the cwd's .bnk/ subdir."
  type        = string
  default     = "/work/.bnk/scratch"
}

variable "cert_manager_crd_ready" {
  description = "Set to true when cert-manager has been applied and its CRDs are registered"
  type        = bool
  default     = false
}

variable "far_repo_url" {
  description = "FAR Repository URL for docker and helm registry"
  type        = string
  default     = "repo.f5.com"
}

variable "cert_manager_namespace" {
  description = "Namespace for cert-manager installation"
  type        = string
  default     = "cert-manager"
}

# F5 BIG-IP K8s Manifest Variables
variable "f5_bigip_k8s_manifest_version" {
  description = "Version of f5-bigip-k8s-manifest chart (FLO version will be extracted from this)"
  type        = string
}

variable "manifest_download_dir" {
  description = "Directory to download and extract the f5-bigip-k8s-manifest chart. Lives under .bnk/ so the extracted flo-version.txt and cis-version.txt files survive between bnk container invocations (the install null_resources read them on subsequent applies)."
  type        = string
  default     = "/work/.bnk/scratch/f5-manifest"
}

# F5 Lifecycle Operator (FLO) Variables
variable "bigip_username" {
  description = "BIG-IP username for CIS controller login"
  type        = string
  default     = "admin"
}

variable "bigip_password" {
  description = "BIG-IP password for CIS controller login"
  type        = string
  sensitive   = true
}

variable "bigip_url" {
  description = "BIG-IP URL for CIS controller login"
  type        = string
  default     = ""
}

variable "flo_namespace" {
  description = "Namespace for f5-lifecycle-operator installation"
  type        = string
  default     = "f5-bnk"
}

variable "utils_namespace" {
  description = "Namespace for F5 Utilities"
  type        = string
  default     = "f5-utils"
}

variable "kube_host" {
  description = "Kubernetes API server URL (used by null_resource curl provisioners)"
  type        = string
  sensitive   = true
}

variable "kube_token" {
  description = "Kubernetes bearer token (used by null_resource curl provisioners)"
  type        = string
  sensitive   = true
}

variable "jwt_token" {
  description = "JWT token for license authentication (unused — fetched from COS)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "cluster_issuer_name" {
  description = "Name of the cluster issuer for certificates"
  type        = string
  default     = "sample-issuer"
}

# NetworkAttachmentDefinition (NAD) Variables
variable "nad_cni_type" {
  description = "CNI type for NAD (host-device or ipvlan)"
  type        = string
  default     = "ipvlan"
  validation {
    condition     = contains(["host-device", "ipvlan"], var.nad_cni_type)
    error_message = "CNI type must be either 'host-device' or 'ipvlan'."
  }
}

variable "nad_interface_name" {
  description = "Network interface name for NAD (e.g., ens7, eth1)"
  type        = string
  default     = "ens3"
}

variable "nad_ipvlan_mode" {
  description = "IPVLAN mode (l2 or l3) - only used when nad_cni_type is ipvlan"
  type        = string
  default     = "l2"
  validation {
    condition     = contains(["l2", "l3"], var.nad_ipvlan_mode)
    error_message = "IPVLAN mode must be either 'l2' or 'l3'."
  }
}

variable "nad_ipvlan_address" {
  description = "Static IP address with CIDR for IPVLAN (e.g., 10.10.1.1/24) - only used when nad_cni_type is ipvlan"
  type        = string
  default     = "10.10.1.1/24"
}

# ==============================================================================
# COS Bucket Configuration (Optional - fetch FAR auth key and JWT from COS)
# ==============================================================================

variable "use_cos_bucket" {
  description = "Fetch FAR auth key and JWT from IBM Cloud Object Storage instead of local files"
  type        = bool
  default     = true
}

variable "ibmcloud_api_key" {
  description = "IBM Cloud API Key (required when use_cos_bucket = true)"
  type        = string
  default     = ""
  sensitive   = true
}

variable "ibmcloud_cos_bucket_region" {
  description = "IBM Cloud region where the COS bucket is located (required when use_cos_bucket = true)"
  type        = string
  default     = "us-south"
}

variable "ibmcloud_resource_group" {
  description = "IBM Cloud resource group name (required when use_cos_bucket = true)"
  type        = string
  default     = "default"
}

variable "ibmcloud_cos_instance_name" {
  description = "IBM Cloud COS instance name"
  type        = string
  default     = "bnk-orchestration"
}

variable "ibmcloud_resources_cos_bucket" {
  description = "IBM Cloud COS bucket for file resources"
  type        = string
  default     = "bnk-schematics-resources"
}

variable "f5_cne_far_auth_file" {
  description = "FAR auth key filename in COS bucket (.tgz)"
  type        = string
  default     = "f5-far-auth-key.tgz"
}

variable "f5_cne_subscription_jwt_file" {
  description = "Subscription JWT filename in COS bucket"
  type        = string
  default     = "trial.jwt"
}

# ==============================================================================
# IBM IAM Trusted Profile Variables
# ==============================================================================

variable "openshift_cluster_name" {
  description = "Name of the OpenShift cluster (used to make the IAM trusted profile name cluster-specific)"
  type        = string
  default     = ""
}

variable "openshift_cluster_crn" {
  description = "CRN of the OpenShift cluster (used to link trusted profile to ROKS service account)"
  type        = string
  default     = ""
}

variable "cluster_vpc_id" {
  description = "ID of the cluster VPC (used to grant trusted profile Viewer and Editor IAM roles)"
  type        = string
  default     = ""
}
