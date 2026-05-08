variable "ibmcloud_api_key" {
  description = "IBM Cloud API key"
  type        = string
  sensitive   = true
}

variable "openshift_cluster_version" {
  description = "OpenShift major.minor version to deploy (e.g. \"4.18\"). The latest available patch within that major.minor is selected automatically. Leave empty to use the latest available version overall."
  type        = string
  default     = ""
}

variable "cluster_region" {
  description = "IBM Cloud region for cluster"
  type        = string
  default     = "ca-tor"

  validation {
    condition     = length(var.cluster_region) > 0
    error_message = "cluster_region cannot be empty"
  }
}

variable "resource_group" {
  description = "Resource group name (leave empty to use account default resource group)"
  type        = string
  default     = ""
}

variable "cluster_vpc_name" {
  description = "Name of the cluster VPC (used for creation or lookup)"
  type        = string
  default     = "tf-cluster-vpc"
}

variable "use_existing_cluster_vpc" {
  description = "Set to true to use an existing cluster VPC instead of creating a new one"
  type        = bool
  default     = false
}

variable "existing_cluster_vpc_id" {
  description = "ID of existing cluster VPC (required if use_existing_cluster_vpc is true)"
  type        = string
  default     = ""
}

variable "zones" {
  description = "Availability zones (optional - will auto-detect from region if not specified)"
  type        = list(string)
  default     = []
}

variable "create_cluster" {
  description = "Enable creation of OpenShift cluster"
  type        = bool
  default     = false
}

variable "create_cos_instance" {
  description = "Enable creation of Cloud Object Storage instance for OpenShift registry"
  type        = bool
  default     = true
}

variable "openshift_cluster_name" {
  description = "Name of the OpenShift cluster"
  type        = string
  default     = "tf-cluster"
}

variable "worker_pool_name" {
  description = "Worker pool name"
  type        = string
  default     = "tf-worker-pool"
}

variable "worker_flavor" {
  description = "Worker node flavor (optional - will auto-select based on min requirements if not specified)"
  type        = string
  default     = ""
}

variable "min_worker_vcpu_count" {
  description = "Minimum number of vCPUs for OpenShift cluster worker nodes (used when auto-selecting flavor)"
  type        = number
  default     = 16
}

variable "min_worker_memory_gb" {
  description = "Minimum memory in GB for OpenShift cluster worker nodes (used when auto-selecting flavor)"
  type        = number
  default     = 64
}

variable "workers_per_zone" {
  description = "Number of workers per zone"
  type        = number
  default     = 1

  validation {
    condition     = var.workers_per_zone >= 1 && var.workers_per_zone <= 10
    error_message = "workers_per_zone must be between 1 and 10"
  }
}

variable "cos_instance_name" {
  description = "Cloud Object Storage instance name for OpenShift registry (defaults to cluster_name-cos)"
  type        = string
  default     = ""
}

variable "create_transit_gateway" {
  description = "Enable creation of Transit Gateway and VPC connections"
  type        = bool
  default     = false
}

variable "transit_gateway_name" {
  description = "Name of the Transit Gateway"
  type        = string
  default     = "tf-tgw"
}
