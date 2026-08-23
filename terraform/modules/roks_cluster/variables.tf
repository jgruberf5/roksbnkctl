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
  description = <<-EOT
    OpenShift cluster version as a BARE major.minor prefix (e.g. "4.20"). Empty
    uses the latest available.

    Do NOT include the "_openshift" suffix — this module appends it. A value that
    carries it matches nothing, and an unmatched value used to build the newest
    OCP silently (#178).
  EOT
  type        = string
  default     = "4.20"

  validation {
    # The suffix is appended by modules/roks_cluster/modules/cluster; supplying
    # it is always wrong, and catching it here costs a second where the
    # alternative was a 45-minute install failure on an unintended OCP version.
    condition     = !can(regex("_openshift", var.openshift_cluster_version))
    error_message = "openshift_cluster_version must be a bare version like \"4.20\" — the \"_openshift\" suffix is appended automatically."
  }
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
    condition     = var.cluster_vpc_cidr == "" || try(tonumber(split("/", var.cluster_vpc_cidr)[1]) <= 18, false)
    error_message = "cluster_vpc_cidr needs /18 or larger. It is split into three per-zone prefixes (/n+2) and each cluster subnet is the first /n+8 of its zone: /16 gives 256-address subnets (today's size), /17 gives 128, /18 gives 64."
  }
}

# ── BYO cluster subnets (#61) ────────────────────────────────────────────────
# Adopting the VPC alone is not enough for an estate that allocates address space
# centrally: the subnets carry the ACLs and routing, and a cluster placed in
# freshly-created subnets sits outside all of it.
variable "use_existing_cluster_subnets" {
  description = "Place the cluster in subnets that already exist instead of creating them. Requires use_existing_cluster_vpc — a subnet cannot be adopted independently of its VPC."
  type        = bool
  default     = false
}

variable "existing_cluster_subnet_ids" {
  description = "Subnet ids to place the cluster in, one per zone, in zone order. Used only when use_existing_cluster_subnets = true. Their zones are read from the subnets themselves."
  type        = list(string)
  default     = []
}

variable "cluster_http_allowed_cidrs" {
  description = "Source CIDRs allowed to reach :80 on the cluster security group. Empty → 0.0.0.0/0."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.cluster_http_allowed_cidrs : can(cidrhost(c, 0))])
    error_message = "cluster_http_allowed_cidrs entries must be CIDRs (a bare address is missing its prefix — write 203.0.113.7/32, not 203.0.113.7)."
  }
}
variable "cluster_vpc_default_sg_inbound_cidrs" {
  description = "Source CIDRs allowed inbound (all protocols/ports) to the cluster VPC's default security group. Empty → 0.0.0.0/0."
  type        = list(string)
  default     = []

  validation {
    condition     = alltrue([for c in var.cluster_vpc_default_sg_inbound_cidrs : can(cidrhost(c, 0))])
    error_message = "cluster_vpc_default_sg_inbound_cidrs entries must be CIDRs (a bare address is missing its prefix — write 203.0.113.7/32, not 203.0.113.7)."
  }
}

variable "roks_worker_flavor" {
  description = <<-EOT
    Exact worker-node flavor, e.g. "cx3d.8x20". Empty auto-selects.

    The auto-select only considers the bx2 family — its filter is
    `^bx2-[0-9]+x[0-9]+$` — so any other profile family is unreachable without
    naming it here. F5's approved reference cluster runs cx3d.8x20, which the
    auto-select can never produce at any minimum.

    The inner cluster module has always honoured this; nothing surfaced it, so no
    config.yaml or environment override could reach it.
  EOT
  type        = string
  default     = ""
}

variable "bnk_line" {
  description = "BNK release line (2.3 / 2.4), used to pick a VALIDATED default worker flavor per line."
  type        = string
  default     = "2.3"
}
