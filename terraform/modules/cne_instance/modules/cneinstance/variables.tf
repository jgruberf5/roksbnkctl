variable "bnk_line" {
  description = "BNK release line ('2.3' or '2.4') for per-release `count` gating. See PRD 18 §4."
  type        = string
  default     = "2.3"
}

# CNEInstance Module Variables

variable "enabled" {
  description = "Enable CNEInstance deployment"
  type        = bool
  default     = true
}

variable "flo_namespace" {
  description = "Namespace for FLO deployment (where CNEInstance will be deployed)"
  type        = string
  default     = "f5-bnk"
}

variable "utils_namespace" {
  description = "Namespace for F5 utility components"
  type        = string
  default     = "f5-utils"
}

variable "cneinstance_spec" {
  description = "Full CNEInstance specification (if empty, will be generated from individual variables)"
  type        = any
  default     = {}
}

# Individual spec configuration variables (used if cneinstance_spec is not provided)
variable "f5_bigip_k8s_manifest_version" {
  description = "F5 BIG-IP Kubernetes manifest version"
  type        = string
  default     = "2.3.0-3.2598.3-0.0.170"
}

variable "cneinstance_gateway_api" {
  description = "Enable Gateway API support"
  type        = bool
  default     = true
}

variable "cneinstance_whole_cluster" {
  description = "Deploy CNEInstance to whole cluster"
  type        = bool
  default     = true
}

variable "cneinstance_logging_subsystem" {
  description = "Enable logging subsystem"
  type        = bool
  default     = false
}

variable "cneinstance_metric_subsystem" {
  description = "Enable metric subsystem"
  type        = bool
  default     = false
}

variable "cluster_issuer_name" {
  description = "Name of the cluster issuer for certificates"
  type        = string
  default     = "sample-issuer"
}

variable "cneinstance_deployment_size" {
  description = "Deployment size for CNEInstance"
  type        = string
  default     = "Small"
}

variable "far_repo_url" {
  description = "FAR repository URL"
  type        = string
  default     = ""
}

# Sprint 29 air-gap mirror — image host for spec.registry.uri. Empty
# coalesces back to far_repo_url (byte-identical default).
variable "far_image_repo_url" {
  description = "Image-pull host for the mirror (spec.registry.uri). Empty falls back to far_repo_url."
  type        = string
  default     = ""
}

variable "use_registry_mirror" {
  description = "Pull charts + images from the registry mirror instead of FAR. The far-secret dockerconfig is dropped; how pods then authenticate depends on the mirror: an in-cluster/ICR mirror authorizes by RBAC and needs no pull secret, while an EXTERNAL mirror (Harbor, Artifactory) gets a `mirror-secret` dockerconfig built from registry_mirror_username/password. A private mirror therefore needs no anonymous/public project."
  type        = bool
  default     = false
}

variable "cneinstance_dynamic_routing" {
  description = "Enable dynamic routing"
  type        = bool
  default     = false
}

variable "cneinstance_firewall_acl" {
  description = "Enable firewall ACL"
  type        = bool
  default     = false
}

variable "cneinstance_pseudocni" {
  description = "Enable pseudo CNI"
  type        = bool
  default     = true
}

variable "cneinstance_env_discovery" {
  description = "Enable environment discovery"
  type        = bool
  default     = false
}

variable "cneinstance_cloud_env" {
  description = "Enable cloud environment"
  type        = bool
  default     = true
}

variable "cneinstance_cloud_provider" {
  description = "Cloud provider type"
  type        = string
  default     = "ibm"
}

variable "cneinstance_vpc_name" {
  description = "VPC name for cloud environment"
  type        = string
  default     = ""
}

variable "cneinstance_cloud_region" {
  description = "Cloud region for environment"
  type        = string
  default     = ""
}

variable "trusted_profile_sa_name" {
  description = "The CNE controller service account. Must be the SAME value the flo module links its Trusted Profile to — a profile the pod may assume and an SCC the pod may use have to name the same account, or one of them is inert."
  type        = string
  default     = ""
}

variable "cneinstance_ibm_trusted_profile_id" {
  description = "IBM Trusted Profile ID for authentication"
  type        = string
  default     = ""
}

variable "cneinstance_gtm_url" {
  description = "BIG-IP DNS / GTM management URL the CNE controller registers its GSLB datacenter with (#51). Empty disables GTM entirely."
  type        = string
  default     = ""
}

variable "cneinstance_gtm_username" {
  description = "Username for the GTM at cneinstance_gtm_url."
  type        = string
  default     = ""
}

variable "cneinstance_gtm_password" {
  description = "Password for the GTM at cneinstance_gtm_url."
  type        = string
  default     = ""
  sensitive   = true
}

variable "cneinstance_gslb_datacenter_name" {
  description = "GSLB datacenter name"
  type        = string
  default     = ""
}

variable "cneinstance_network_attachments" {
  description = "Network attachment definitions for CNEInstance (computed from NAD configuration)"
  type        = list(string)
  default     = []
}

variable "flo_deployment_id" {
  description = "F5 Lifecycle Operator deployment identifier (used to trigger waiting)"
  type        = string
  default     = ""
}

variable "flo_deployment_dependency" {
  description = "Explicit dependency on FLO deployment (pass the helm_release resource)"
  type        = any
  default     = null
}

variable "kube_host" {
  description = "Kubernetes API server URL (used by curl local-exec provisioners)"
  type        = string
  default     = ""
}

variable "kube_token" {
  description = "Kubernetes bearer token (used by curl local-exec provisioners)"
  type        = string
  sensitive   = true
  default     = ""
}

# ── Cloud network mapping + VLAN self-IPs (BNK 2.3 install-guide "Configuration") ──
# Zone NAMES are derived from cneinstance_cloud_region (<region>-1, -2, …) and the
# list order; only the subnet CIDRs + TMM self-IPs are configurable here. Defaults
# are the install-guide values, so a default deploy is fully wired with no input.
variable "cneinstance_network_zones" {
  description = "Per-zone subnet CIDRs + TMM self-IPs for the cloud-network-mapping ConfigMap and the external/internal F5SPKVlan CRs. List order = zone 1..N (zone name = <cneinstance_cloud_region>-<index>)."
  type = list(object({
    ext_vlan_cidr   = string
    int_vlan_cidr   = string
    int_snat_cidr   = string
    int_vip_cidr    = string
    external_selfip = string
    internal_selfip = string
  }))
  # nullable=false → a null from the parent (passed when the workspace sets no
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

variable "cneinstance_vlan_external_interface" {
  description = "TMM interface id for the external F5SPKVlan (spec.interfaces)"
  type        = string
  default     = "1.1"
}

variable "cneinstance_vlan_internal_interface" {
  description = "TMM interface id for the internal F5SPKVlan (spec.interfaces)"
  type        = string
  default     = "1.2"
}

variable "cneinstance_vlan_prefixlen_external" {
  description = "External VLAN self-IP prefix length; 0 inherits cneinstance_vlan_prefixlen."
  type        = number
  default     = 0
}

variable "cneinstance_vlan_prefixlen_internal" {
  description = "Internal VLAN self-IP prefix length; 0 inherits cneinstance_vlan_prefixlen."
  type        = number
  default     = 0
}

variable "cneinstance_vlan_prefixlen" {
  description = "selfip prefix length (spec.prefixlen_v4) for the F5SPKVlan CRs"
  type        = number
  default     = 24
}

variable "cneinstance_tmm_k8s_routes" {
  description = "Pod CIDR TMM routes to (advanced.tmm.env TMM_K8S_ROUTES). Default is the ROKS default pod subnet."
  type        = string
  default     = "172.17.0.0/18"
}


variable "registry_mirror_username" {
  description = "Basic-auth username for an external registry mirror (private Harbor/Artifactory)."
  type        = string
  default     = ""
}

variable "registry_mirror_password" {
  description = "Basic-auth password for an external registry mirror. When set with use_registry_mirror, the CNEInstance references the mirror-secret pull secret instead of pulling anonymously."
  type        = string
  sensitive   = true
  default     = ""
}

variable "roksbnkctl_binary" {
  description = "Absolute path to the roksbnkctl binary; the CNE-instance phase invokes `roksbnkctl tfx <verb>` in place of host curl (no interpreter, so cmd.exe execs it on Windows). Empty falls back to `roksbnkctl` on PATH."
  type        = string
  default     = ""
}

variable "cneinstance_advanced_env" {
  description = "Per-component advanced.<component>.env passthrough (#175). See the root variable of the same name."
  type        = map(map(string))
  default     = {}
}

# ── BNK 2.4 conformance with F5's reference CNEInstance ──────────────────────
#
# Everything below is emitted on 2.4 ONLY. The 2.3 spec is asserted
# byte-identical to the previous release, so none of these may appear there.
#
# The defaults are F5's reference values, taken from the live 2.4 cluster
# capture in /mnt/d/roksbnkctl-gap-2-3-to-2-4 — not invented. A field whose
# default differs from the reference is a field that needs a reason.

variable "cneinstance_tmm_replicas" {
  description = "Number of f5-tmm data-plane replicas (2.4). Reference: 3."
  type        = number
  default     = 3
}

variable "cneinstance_watch_namespaces" {
  description = "Namespaces the CNE controller watches (2.4). Reference: [\"All\"]."
  type        = list(string)
  default     = ["All"]
}

variable "cneinstance_tmm_anti_affinity" {
  description = <<-EOT
    Require f5-tmm pods onto DIFFERENT NODES (2.4).

    On 2.4 this replaces the node-labeler: 2.4 removed the labeler (#171) and
    `placement` is the mechanism that took over. Without it nothing stops two
    TMMs landing on one node — which happened to work in verification only
    because the scheduler spread them, not because anything required it.
  EOT
  type        = bool
  default     = true
}

variable "cneinstance_tmm_zone_spread" {
  description = "Spread f5-tmm pods across zones with maxSkew 1, DoNotSchedule (2.4). Reference: on."
  type        = bool
  default     = true
}

variable "cneinstance_tmm_rolling_update" {
  description = <<-EOT
    Pin TMM's rolling update to maxSurge 0 / maxUnavailable 1 (2.4).

    Same shape as the cwc Multi-Attach deadlock: an unconstrained rolling update
    on a workload holding a single-attach resource can wedge. Reference sets it.
  EOT
  type        = bool
  default     = true
}

variable "cneinstance_external_bigip" {
  description = "Enable the external BIG-IP controller (2.4). Reference: true."
  type        = bool
  default     = false
}

variable "cneinstance_external_bigip_login_secret" {
  description = "Secret holding external BIG-IP credentials (2.4). Reference: f5-bigip-ctlr-login."
  type        = string
  default     = "f5-bigip-ctlr-login"
}

variable "cneinstance_cluster_identifier" {
  description = "CLUSTER_IDENTIFIER passed to the external BIG-IP controller (2.4). Empty derives from the cluster name."
  type        = string
  default     = ""
}

variable "cneinstance_gateway_api_version" {
  description = <<-EOT
    GATEWAY_API_VERSION for the CNE controller (2.4). Reference: 1.5.0.

    roksbnkctl previously set this nowhere, so the controller ran on whatever
    the operator defaulted to — v1.4.1 on the verified cluster. The 2.4 EA guide
    requires the 1.5 bundle for mTLS.
  EOT
  type        = string
  default     = "1.5.0"
}

variable "cneinstance_demo_mode" {
  description = <<-EOT
    advanced.demoMode.enabled.

    Empty string means "the line default": true on 2.3 (what has always
    shipped) and FALSE on 2.4, matching F5's reference. Demo mode was being set
    true on every install, which is not something a customer deployment should
    carry. Set "true"/"false" to pin it explicitly.
  EOT
  type        = string
  default     = ""
}

variable "cneinstance_tmm_pod_label" {
  description = "Value of the `app` label the placement rules select f5-tmm pods by (2.4). Reference: f5-tmm."
  type        = string
  default     = "f5-tmm"
}

variable "cneinstance_tmm_anti_affinity_topology_key" {
  description = <<-EOT
    Node label the TMM anti-affinity rule spreads across (2.4).

    The IBM ROKS per-node label. Surfaced rather than hard-coded so a cluster
    that labels its topology differently is still configurable — the assumption
    the node-labeler used to bake in.
  EOT
  type        = string
  default     = "kubernetes.io/hostname"
}

variable "cneinstance_tmm_zone_topology_key" {
  description = "Node label the TMM zone spread uses (2.4). The IBM ROKS zone label. Reference: topology.kubernetes.io/zone."
  type        = string
  default     = "topology.kubernetes.io/zone"
}

variable "cneinstance_tmm_zone_max_skew" {
  description = "maxSkew for the TMM zone topology-spread constraint (2.4). Reference: 1."
  type        = number
  default     = 1
}

variable "cneinstance_tmm_zone_when_unsatisfiable" {
  description = "whenUnsatisfiable for the TMM zone spread (2.4): DoNotSchedule or ScheduleAnyway. Reference: DoNotSchedule."
  type        = string
  default     = "DoNotSchedule"
  validation {
    condition     = contains(["DoNotSchedule", "ScheduleAnyway"], var.cneinstance_tmm_zone_when_unsatisfiable)
    error_message = "cneinstance_tmm_zone_when_unsatisfiable must be DoNotSchedule or ScheduleAnyway."
  }
}
