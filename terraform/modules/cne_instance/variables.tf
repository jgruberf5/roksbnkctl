variable "bnk_line" {
  description = <<-EOT
    BNK release line driving per-release resource gating ("2.3" or "2.4").

    Derived by roksbnkctl from bnk.manifest_version and rendered as a tfvar; the
    modules gate with `count` on it rather than the tree being forked per line,
    because the 2.4 changes are additive resources and two copies of a file
    drift (PRD 18 §4).
  EOT
  type        = string
  default     = "2.3"
}

# ============================================================
# Root Terraform Variables
# F5 BNK Orchestrator for existing ROKS cluster
# ============================================================


# ============================================================
# IBM Cloud Variables
# ============================================================

variable "ibmcloud_api_key" {
  description = "IBM Cloud API Key"
  type        = string
  sensitive   = true
}

variable "ibmcloud_cluster_region" {
  description = "IBM Cloud region where the cluster resides"
  type        = string
  default     = "ca-tor"
}

variable "ibmcloud_resource_group" {
  description = "IBM Cloud Resource Group name (leave empty to use account default)"
  type        = string
  default     = "default"
}

# ============================================================
# Cluster Inputs
# ============================================================

variable "roks_cluster_name_or_id" {
  description = "Name or ID of the existing OpenShift ROKS cluster to deploy BNK onto"
  type        = string

  validation {
    condition     = length(var.roks_cluster_name_or_id) > 0
    error_message = "roks_cluster_name_or_id cannot be empty — an existing cluster is required."
  }
}

# ============================================================
# FAR / Registry Configuration
# ============================================================

variable "far_repo_url" {
  description = "FAR Repository URL for Docker and Helm registry"
  type        = string
  default     = "repo.f5.com"
}

# Sprint 29 air-gap mirror — image host for the CNEInstance spec.registry.uri.
# Empty falls back to far_repo_url in the inner module (byte-identical default).
variable "far_image_repo_url" {
  description = "Image-pull host for the mirror (CNEInstance spec.registry.uri). Empty falls back to far_repo_url."
  type        = string
  default     = ""
}

variable "use_registry_mirror" {
  description = "Pull charts + images from the registry mirror instead of FAR. The far-secret dockerconfig is dropped; how pods then authenticate depends on the mirror: an in-cluster/ICR mirror authorizes by RBAC and needs no pull secret, while an EXTERNAL mirror (Harbor, Artifactory) gets a `mirror-secret` dockerconfig built from registry_mirror_username/password. A private mirror therefore needs no anonymous/public project."
  type        = bool
  default     = false
}

# ============================================================
# FLO Namespace Configuration
# ============================================================

variable "flo_namespace" {
  description = "Namespace for F5 Lifecycle Operator"
  type        = string
  default     = "f5-bnk"
}

variable "flo_utils_namespace" {
  description = "Namespace for F5 utility components"
  type        = string
  default     = "f5-utils"
}

variable "f5_bigip_k8s_manifest_version" {
  description = "Version of f5-bigip-k8s-manifest chart - used by flo, cneinstance modules"
  type        = string
  default     = "2.3.0-3.2598.3-0.0.170"
}

variable "flo_trusted_profile_sa_name" {
  description = "The CNE controller service account; must match what the flo module linked."
  type        = string
  default     = ""
}

variable "flo_trusted_profile_id" {
  description = "IBM IAM Trusted Profile ID for provisioning VPC routes"
  type        = string
  default     = ""
}

variable "flo_cluster_issuer_name" {
  description = "mTLS certificate issuer name"
  type        = string
  default     = ""
}


# ============================================================
# CNEInstance Configuration
# ============================================================

variable "cneinstance_deployment_size" {
  description = "Deployment size for CNEInstance (Tiny, Small, Medium, Large). EMPTY takes the line default: Small on 2.3, Tiny on 2.4 (what the 2.4 install guide and F5's reference cluster use). Tiny is what the BNK 2.4 install guide uses."
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
  description = "GSLB datacenter name for CNEInstance (optional)"
  type        = string
  default     = ""
}

variable "cneinstance_network_attachments" {
  description = "The Multus Network Attachment Definitions for the CNEInstance TMM deployments"
  type        = list(string)
  default     = ["ens3-ipvlan-l2", "macvlan-conf"]
}

# Per-zone subnet CIDRs + TMM self-IPs for the cloud-network-mapping ConfigMap +
# F5SPKVlan CRs. Empty (default) → the inner cneinstance module's install-guide
# defaults apply (the parent passes null below so that default is used).
variable "cneinstance_network_zones" {
  description = "Per-zone subnet CIDRs + TMM self-IPs (empty = use the install-guide defaults)"
  type = list(object({
    ext_vlan_cidr   = string
    int_vlan_cidr   = string
    int_snat_cidr   = string
    int_vip_cidr    = string
    external_selfip = string
    internal_selfip = string
  }))
  default = []
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
  description = "TMM self-IP prefix length (spec.prefixlen_v4) for the external/internal F5SPKVlan CRs"
  type        = number
  default     = 24
}

variable "cneinstance_tmm_k8s_routes" {
  description = "Pod CIDR TMM routes to (advanced.tmm.env TMM_K8S_ROUTES). Default is the ROKS default pod subnet."
  type        = string
  default     = "172.17.0.0/18"
}

variable "create_roks_cluster" {
  description = "When true, cluster is being created by roks_cluster — skip plan-time cluster credential fetch"
  type        = bool
  default     = false
}

variable "roks_cluster_dependency_id" {
  description = "roks_cluster sentinel ID — when set, defers runtime_config fetch to apply time after roks_cluster completes"
  type        = string
  default     = null
}

variable "flo_dependency_id" {
  description = "flo_ready sentinel ID — pass module.flo.flo_ready_id to defer cne_instance until flo completes and CRDs are registered"
  type        = string
  default     = null
}

variable "deploy_bnk" {
  description = "Deploy BIG-IP Next for Kubernetes — when false the inner cneinstance module is disabled and no CNEInstance resources are created"
  type        = bool
  default     = true
}

# Persistent dir for the kubeconfig that ibm_container_cluster_config downloads.
# Default lives under /work/.bnk/scratch (host-bind-mounted in the bnk runner) so
# the non-root container user can write it and the file survives across container
# exits. path.module would resolve to /opt/tf-project/modules/cne_instance inside
# the image — root-owned, read-only for the non-root container user, so MkdirAll
# fails. Per-module subdir keeps concurrent data sources from clobbering each other.
variable "kubeconfig_dir" {
  description = "Persistent, writable dir for ibm_container_cluster_config kubeconfig downloads. Defaults to a host-bind-mounted, module-scoped path under .bnk/scratch."
  type        = string
  default     = "/work/.bnk/scratch/kubeconfig/cne_instance"
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

variable "cluster_absent" {
  description = "True in the standalone FLP-VSI phase: no ROKS cluster exists, so all cluster data-source lookups + kube providers are skipped (count=0)."
  type        = bool
  default     = false
}

variable "cneinstance_tmm_resources" {
  description = "TMM resource requests/limits override; rendered as advanced.tmm.resources (#203)."
  type        = map(map(string))
  default     = {}
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

variable "cneinstance_tcp_settings" {
  description = <<-EOT
    F5BigTcpSetting field overrides, as a flat map of field name to value.

    The CR has 54 fields across bool, int and string. Surfacing each as its own
    config field would be 54 fields nobody reads; a map keeps the whole surface
    reachable and lets F5 add fields between releases without a code change here.

    Values are strings and coerced on the way out: "1500" renders as a number,
    "true" as a bool, anything else as a string. That is because a config file
    and an environment variable can only carry text, while the CR is typed.

    Empty renders NO CR at all. That is deliberate — the product creates its own
    default TCP profile, and emitting an empty one would fight it.
  EOT
  type        = map(string)
  default     = {}
}

variable "cneinstance_tcp_settings_name" {
  description = "Name of the F5BigTcpSetting CR to write. Reference: sys-default-tcp."
  type        = string
  default     = "sys-default-tcp"
}

variable "cneinstance_whole_cluster_override" {
  description = <<-EOT
    spec.wholeCluster, as a tri-state.

    Empty means the LINE default: true on 2.3, which is what has always shipped,
    and FALSE on 2.4, matching F5's reference. "true"/"false" pins it.

    This has to move with watchNamespaces. The two are validated together by the
    product — `wholeCluster: true` with `watchNamespaces: ["All"]` is rejected as
    "Invalid product configuration, please check WholeCluster, WatchNamespaces
    and GatewayAPI settings", because saying "watch everything" twice in two
    different ways is a contradiction rather than emphasis.
  EOT
  type        = string
  default     = ""
}

# ── hugepages on the worker pool ─────────────────────────────────────────────

variable "cneinstance_hugepages" {
  description = <<-EOT
    Allocate hugepages on the worker pool via the OpenShift Node Tuning Operator.

    OFF by default. Turning it on sets a bootloader kernel argument, which makes
    the Machine Config Operator DRAIN AND REBOOT every matching worker, one at a
    time. That is a maintenance event, not a configuration change, and is not
    something a default should decide.
  EOT
  type        = bool
  default     = false
}

variable "cneinstance_hugepages_size" {
  description = "Hugepage size, e.g. 2M or 1G. TMM requests hugepages-2Mi, so 2M is the matching size."
  type        = string
  default     = "2M"
}

variable "cneinstance_hugepages_count" {
  description = "Hugepages PER NODE. 2048 x 2M = 4Gi, which is what deploymentSize Small was observed to request."
  type        = number
  default     = 2048
}

variable "cneinstance_hugepages_node_role" {
  description = "machineconfiguration.openshift.io/role the Tuned profile applies to."
  type        = string
  default     = "worker"
}

variable "cneinstance_hugepages_profile_name" {
  description = "Name of the Tuned profile and CR."
  type        = string
  default     = "bnk-hugepages"
}

variable "cneinstance_storage_class_name" {
  description = "StorageClass for the CNEInstance's persistent volumes. See the leaf module variable of the same name (#189)."
  type        = string
  default     = ""
}
