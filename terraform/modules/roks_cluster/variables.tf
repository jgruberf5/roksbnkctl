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
