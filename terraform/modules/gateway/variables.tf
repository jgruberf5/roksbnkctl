# ── Cluster wiring (mirrors the cne_instance / testing modules) ──────────────
variable "ibmcloud_api_key" {
  description = "IBM Cloud API key"
  type        = string
  sensitive   = true
}

variable "ibmcloud_cluster_region" {
  description = "IBM Cloud region the cluster runs in (zone names derive as <region>-1/2/3)"
  type        = string
}

variable "ibmcloud_resource_group" {
  description = "IBM Cloud resource group"
  type        = string
  default     = ""
}

variable "roks_cluster_name_or_id" {
  description = "Existing ROKS cluster name or id the Gateway phase configures"
  type        = string
}

variable "kubeconfig_dir" {
  description = "Directory the IBM provider writes the cluster kubeconfig into"
  type        = string
  default     = ""
}

variable "create_roks_cluster" {
  description = "Always false for the Gateway phase (it reuses an existing cluster)"
  type        = bool
  default     = false
}

variable "roks_cluster_dependency_id" {
  description = "Cluster-ready handle (apply-time ordering); unused gate kept for symmetry"
  type        = string
  default     = null
}

variable "deploy_gateway" {
  description = "Master toggle — when false the whole Gateway phase is a no-op (count=0)"
  type        = bool
  default     = false
}

# ── Namespaces ───────────────────────────────────────────────────────────────
variable "flo_namespace" {
  description = "BNK namespace (F5BnkGateway / SnatPool / Egress / StaticRoute live here)"
  type        = string
  default     = "f5-bnk"
}

variable "app_namespace" {
  description = "Application namespace the Gateway + HTTPRoute serve (created by this module)"
  type        = string
  default     = "f5-app"
}

# ── Zone data (shared with the BNK phase's cneinstance_network_zones) ─────────
# Empty → install-guide defaults. The Gateway phase uses int_vip_cidr (BnkGateway
# listener networks), int_snat_cidr (SnatPool addresses), and ext_vlan_cidr
# (StaticRoute gateways). Zone NAMES derive from ibmcloud_cluster_region.
variable "cneinstance_network_zones" {
  description = "Per-zone subnet CIDRs (empty = install-guide defaults). Shared with the BNK phase."
  type = list(object({
    ext_vlan_cidr   = string
    int_vlan_cidr   = string
    int_snat_cidr   = string
    int_vip_cidr    = string
    external_selfip = string
    internal_selfip = string
  }))
  # nullable=false → a null from the root (passed when the workspace sets no
  # zones) falls back to this install-guide default instead of erroring.
  nullable = false
  default = [
    {
      ext_vlan_cidr   = "10.155.15.0/24"
      int_vlan_cidr   = "10.254.99.0/24"
      int_snat_cidr   = "10.10.11.0/24"
      int_vip_cidr    = "10.135.15.0/24"
      external_selfip = "10.155.15.101"
      internal_selfip = "10.254.99.101"
    },
    {
      ext_vlan_cidr   = "10.156.16.0/24"
      int_vlan_cidr   = "10.254.100.0/24"
      int_snat_cidr   = "10.10.21.0/24"
      int_vip_cidr    = "10.136.16.0/24"
      external_selfip = "10.156.16.101"
      internal_selfip = "10.254.100.101"
    },
    {
      ext_vlan_cidr   = "10.157.17.0/24"
      int_vlan_cidr   = "10.254.101.0/24"
      int_snat_cidr   = "10.10.31.0/24"
      int_vip_cidr    = "10.137.17.0/24"
      external_selfip = "10.157.17.101"
      internal_selfip = "10.254.101.101"
    },
  ]
}

# ── Gateway API / HTTPRoute ──────────────────────────────────────────────────
variable "gateway_class_name" {
  description = "GatewayClass name"
  type        = string
  default     = "gateway-class"
}

variable "gateway_controller_name" {
  description = "GatewayClass controllerName (the BNK CNE controller)"
  type        = string
  default     = "f5.com/f5-bnk-f5-cne-controller"
}

variable "gateway_bnkgateway_name" {
  description = "F5BnkGateway name (referenced by the Gateway parametersRef)"
  type        = string
  default     = "bnkgateway-cloud1"
}

variable "gateway_name" {
  description = "Gateway (gateway.networking.k8s.io) name"
  type        = string
  default     = "http-gw"
}

variable "gateway_listener_port" {
  description = "Gateway HTTP listener port"
  type        = number
  default     = 80
}

variable "gateway_vip_start_host" {
  description = "Host number in int_vip_cidr for F5BnkGateway listener startAddress"
  type        = number
  default     = 100
}

variable "gateway_vip_end_host" {
  description = "Host number in int_vip_cidr for F5BnkGateway listener endAddress"
  type        = number
  default     = 120
}

variable "gateway_route_name" {
  description = "HTTPRoute name"
  type        = string
  default     = "http-route"
}

variable "gateway_backend_service" {
  description = "HTTPRoute backend Service name in the app namespace"
  type        = string
  default     = "nginx-service"
}

variable "gateway_backend_port" {
  description = "HTTPRoute backend Service port"
  type        = number
  default     = 80
}

# ── Egress / SnatPool ────────────────────────────────────────────────────────
variable "gateway_egress_mode" {
  description = "Egress SNAT strategy: snatpool (default; creates the SnatPool + snatpool Egress), automap (automap Egress only), or both."
  type        = string
  default     = "snatpool"
  validation {
    condition     = contains(["snatpool", "automap", "both"], var.gateway_egress_mode)
    error_message = "gateway_egress_mode must be one of: snatpool, automap, both."
  }
}

variable "gateway_snatpool_name" {
  description = "F5SPKSnatpool name"
  type        = string
  default     = "egress-snat-vx102"
}

variable "gateway_snat_host" {
  description = "Host number in int_snat_cidr for each zone's SNAT address"
  type        = number
  default     = 111
}

variable "gateway_egress_app_interface" {
  description = "Application pod interface the Egress intercepts"
  type        = string
  default     = "eth0"
}

variable "gateway_egress_tmm_interface" {
  description = "TMM interface name the Egress VXLAN binds to (matches the external VLAN)"
  type        = string
  default     = "external-vlan"
}

variable "gateway_egress_node_interface" {
  description = "Node interface name for the Egress VXLAN"
  type        = string
  default     = "ens3"
}

variable "gateway_egress_mtu" {
  description = "Egress VXLAN MTU"
  type        = number
  default     = 1460
}

variable "gateway_vxlan_port" {
  description = "Egress VXLAN UDP port (also opened on the cluster security group)"
  type        = number
  default     = 6789
}

# ── Static routes (client subnets reachable via the TMM zone gateways) ───────
variable "gateway_client_subnet_local" {
  description = "Local-VSI client subnet CIDRs the static routes reach (cluster-VPC clients; one F5SPKStaticRoute per entry × zone). Empty = no local client routes."
  type        = list(string)
  default     = []
}

variable "gateway_client_subnet_remote" {
  description = "Remote-VSI client subnet CIDRs the static routes reach (client-VPC clients over the TGW; one F5SPKStaticRoute per entry × zone). Empty = no remote client routes."
  type        = list(string)
  default     = []
}

variable "gateway_static_route_gw_host" {
  description = "Host number in ext_vlan_cidr used as each zone's static-route gateway"
  type        = number
  default     = 1
}
