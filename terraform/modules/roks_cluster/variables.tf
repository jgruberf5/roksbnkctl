variable "ibmcloud_api_key" {
  description = "IBM Cloud API key"
  type        = string
  sensitive   = true
}

variable "ibmcloud_cluster_region" {
  description = "IBM Cloud region for all cluster resources"
  type        = string
}

variable "ibmcloud_resource_group" {
  description = "IBM Cloud resource group name"
  type        = string
  default     = "default"
}

variable "create_roks_cluster" {
  description = "Create a new ROKS cluster. When false, supply roks_cluster_id_or_name instead."
  type        = bool
  default     = true
}

variable "cluster_public_gateway" {
  description = "Attach public gateways for worker Internet egress. true (default) = current behavior; false = private/disconnected cluster (no egress)."
  type        = bool
  default     = true
}

variable "roks_cluster_id_or_name" {
  description = "ID or name of an existing ROKS cluster — used when create_roks_cluster = false"
  type        = string
  default     = ""
}

variable "create_roks_transit_gateway" {
  description = "Create Transit Gateway and VPC connections"
  type        = bool
  default     = true
}

variable "create_roks_registry_cos_instance" {
  description = "Create Cloud Object Storage instance for the OpenShift image registry"
  type        = bool
  default     = true
}

variable "roks_cluster_vpc_name" {
  description = "Name of the cluster VPC"
  type        = string
  default     = "tf-cluster-vpc"
}

variable "openshift_cluster_name" {
  description = "Name of the OpenShift cluster"
  type        = string
  default     = "tf-openshift-cluster"
}

variable "openshift_cluster_version" {
  description = "OpenShift cluster version (e.g. 4.18)"
  type        = string
  default     = "4.18"
}

variable "roks_workers_per_zone" {
  description = "Number of worker nodes per availability zone"
  type        = number
  default     = 1
}

variable "roks_min_worker_vcpu_count" {
  description = "Minimum vCPU count when auto-selecting the worker node flavor"
  type        = number
  default     = 16
}

variable "roks_min_worker_memory_gb" {
  description = "Minimum memory in GB when auto-selecting the worker node flavor"
  type        = number
  default     = 64
}

variable "roks_cos_instance_name" {
  description = "Name of the COS instance for the OpenShift image registry"
  type        = string
  default     = "tf-openshift-cos-instance"
}

variable "roks_transit_gateway_name" {
  description = "Name of the Transit Gateway"
  type        = string
  default     = "tf-tgw"
}

# Existing-cluster-VPC reuse passthrough — fed from the root
# use_existing_cluster_vpc / existing_cluster_vpc_id and forwarded into
# module "cluster". Default false keeps the cluster phase creating the
# VPC; the bnk/testing phase sets true to look up the cluster-phase VPC.
variable "use_existing_cluster_vpc" {
  description = "Reuse an existing cluster VPC instead of creating one (forwarded to module.cluster)."
  type        = bool
  default     = false
}

variable "existing_cluster_vpc_id" {
  description = "ID of the existing cluster VPC (used only when use_existing_cluster_vpc = true; forwarded to module.cluster)."
  type        = string
  default     = ""
}

variable "kubeconfig_dir" {
  description = "Directory where ibm_container_cluster_config writes the admin kubeconfig. Must be writable; set explicitly to avoid the provider's HOME-derived default, which resolves empty under the roksbnkctl runner."
  type        = string
}

variable "roksbnkctl_binary" {
  description = "Absolute path to the roksbnkctl binary; the cluster phase invokes `roksbnkctl tfx <verb>` in place of host curl/kubectl (no interpreter, so cmd.exe execs it on Windows). roksbnkctl sets this via TF_VAR_roksbnkctl_binary; empty falls back to `roksbnkctl` on PATH."
  type        = string
  default     = ""
}

variable "cluster_absent" {
  description = "True only in the standalone FLP-VSI phase: no ROKS cluster exists or will be adopted, so all cluster data-source lookups + kube providers across modules are skipped (count=0)."
  type        = bool
  default     = false
}

variable "cluster_vpc_cidr" {
  description = <<-EOT
    CIDR block the cluster VPC's per-zone address prefixes are carved from
    (e.g. "10.241.0.0/16" → 10.241.0.0/18, 10.241.64.0/18, 10.241.128.0/18).

    Empty (the default) leaves IBM's "auto" address prefix management in place,
    which gives EVERY VPC in a region the same prefixes — so two roksbnkctl-created
    clusters cannot share a Transit Gateway without overlapping. Set a distinct block
    per cluster when they must. Ignored when reusing an existing VPC. See issue #46.
  EOT
  type        = string
  default     = ""

  validation {
    condition     = var.cluster_vpc_cidr == "" || can(cidrhost(var.cluster_vpc_cidr, 0))
    error_message = "cluster_vpc_cidr must be empty or a valid CIDR block, e.g. 10.241.0.0/16."
  }
  validation {
    condition     = var.cluster_vpc_cidr == "" || tonumber(split("/", var.cluster_vpc_cidr)[1]) <= 18
    error_message = "cluster_vpc_cidr needs /18 or larger. It is split into three per-zone prefixes (/n+2) and each cluster subnet is the first /n+8 of its zone: /16 gives 256-address subnets (today's size), /17 gives 128, /18 gives 64."
  }
}
