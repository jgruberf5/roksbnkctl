variable "deploy_tgw_connection" {
  description = "Attach the cluster's VPC to an existing Transit Gateway. Off in every other phase's override; on only for `tgw connect`. When false the module is a complete no-op."
  type        = bool
  default     = false
}

variable "ibmcloud_api_key" {
  description = "IBM Cloud API key (IAM auth + provider config)."
  type        = string
  sensitive   = true
  default     = ""
}

variable "ibmcloud_cluster_region" {
  description = "Region of the cluster VPC (ibm provider + the ibm_is_vpc lookup)."
  type        = string
  default     = ""
}

variable "cluster_vpc_id" {
  description = "ID of the cluster's VPC — the network attached to the Transit Gateway. Resolved to a CRN here; the connection needs the CRN, but cluster-outputs.json records the id."
  type        = string
  default     = ""
}

variable "transit_gateway" {
  description = "The EXISTING Transit Gateway to attach to, by NAME or by ID. Resolved against the account's gateway list, so either works. Multiple clusters passing the same value share one gateway; each workspace owns its own connection."
  type        = string
  default     = ""
}

variable "connection_name" {
  description = "Name for THIS cluster's connection on the gateway. Must be unique per gateway, so it is prefix-derived — two clusters sharing one Transit Gateway get distinct connection names."
  type        = string
  default     = ""
}
