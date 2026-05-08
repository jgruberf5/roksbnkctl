# ============================================================
# Root Terraform Variables
# IBM Cloud Testing Jumphosts
# ============================================================


# ============================================================
# IBM Cloud Credentials
# ============================================================

variable "ibmcloud_api_key" {
  description = "IBM Cloud API Key"
  type        = string
  sensitive   = true
}

variable "ibmcloud_cluster_region" {
  description = "IBM Cloud region where the referenced cluster resides"
  type        = string
  default     = "ca-tor"
}

variable "ibmcloud_resource_group" {
  description = "IBM Cloud Resource Group name (leave empty to use account default)"
  type        = string
  default     = ""
}

# ============================================================
# ROKS Output
# ============================================================

variable "roks_cluster_name_or_id" {
  description = "Name or ID of the existing OpenShift ROKS cluster"
  type        = string

  validation {
    condition     = length(var.roks_cluster_name_or_id) > 0
    error_message = "roks_cluster_name_or_id cannot be empty."
  }
}

# ============================================================
# Feature Flags
# ============================================================

variable "testing_create_tgw_jumphost" {
  description = "Create a jumphost in a client VPC and (optionally) connect it to the cluster via a Transit Gateway"
  type        = bool
  default     = true
}

variable "testing_create_cluster_jumphosts" {
  description = "Create one jumphost per availability zone directly inside the cluster VPC"
  type        = bool
  default     = false
}

# ============================================================
# Shared Jumphost Configuration
# Applied to both TGW and cluster jumphosts
# ============================================================

variable "testing_ssh_key_name" {
  description = "Name of the SSH key to inject into all jumphosts. Must exist in client_vpc_region (for TGW jumphost) and in ibmcloud_cluster_region (for cluster jumphosts)"
  type        = string
  default     = ""
}

variable "testing_jumphost_profile" {
  description = "Instance profile for all jumphosts (leave empty to auto-select from min_vcpu_count and min_memory_gb)"
  type        = string
  default     = ""
}

variable "testing_min_vcpu_count" {
  description = "Minimum vCPU count when auto-selecting the instance profile"
  type        = number
  default     = 4
}

variable "testing_min_memory_gb" {
  description = "Minimum memory in GB when auto-selecting the instance profile"
  type        = number
  default     = 8
}

# ============================================================
# TGW Jumphost — Client VPC Configuration
#
# VPC resolution when create_tgw_jumphost = true:
#   create_client_vpc = true           → new VPC created in client_vpc_region
#   create_client_vpc = false          → existing VPC looked up by client_vpc_name in client_vpc_region
# ============================================================

variable "testing_create_client_vpc" {
  description = "Create a new client VPC for the TGW jumphost. When false, client_vpc_name must reference an existing VPC"
  type        = bool
  default     = false
}

variable "testing_client_vpc_name" {
  description = "Name of the client VPC — created when create_client_vpc = true, or looked up when create_client_vpc = false"
  type        = string
  default     = "tf-testing-vpc"
}

variable "testing_client_vpc_region" {
  description = "IBM Cloud region for the client VPC and TGW jumphost"
  type        = string
  default     = "ca-tor"
}

variable "testing_transit_gateway_name" {
  description = "Name of an existing Transit Gateway to connect the client VPC to (leave empty to skip TGW attachment)"
  type        = string
  default     = ""
}

variable "testing_tgw_jumphost_name" {
  description = "Name of the TGW-connected jumphost instance (used as prefix for subnet, gateway, security group, and floating IP)"
  type        = string
  default     = "tf-testing-jumphost-tgw"
}

# ============================================================
# Cluster Jumphosts — One per availability zone
# ============================================================

variable "testing_cluster_jumphost_name_prefix" {
  description = "Name prefix for cluster jumphosts — zone name is appended (<prefix>-<zone>)"
  type        = string
  default     = "tf-testing-jumphost-cluster"
}

variable "roks_cluster_dependency_id" {
  description = "roks_cluster sentinel ID — when set, defers cluster/TGW data source reads to apply time after roks_cluster completes"
  type        = string
  default     = null
}

variable "create_roks_cluster" {
  description = "Set to true when the ROKS cluster is being created in this run — skips cluster-VPC-derived data sources that require a pre-existing cluster"
  type        = bool
  default     = false
}

variable "cluster_vpc_id" {
  description = "ID of the cluster VPC — pass module.roks_cluster.roks_cluster_vpc_id directly; avoids deriving via worker-pool subnet chain which is deferred to apply time"
  type        = string
  default     = ""
}
