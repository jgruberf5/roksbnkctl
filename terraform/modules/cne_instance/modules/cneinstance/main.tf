locals {
  use_kubectl = var.enabled

  cneinstance_name = "${var.flo_namespace}-f5-cne-controller"

  # roksbnkctl binary for the tfx-based local-exec conversions (empty => on PATH).
  roksbnkctl_bin = var.roksbnkctl_binary != "" ? var.roksbnkctl_binary : "roksbnkctl"

  # Sprint 29 air-gap mirror — spec.registry.uri host. The image host
  # coalesces back to far_repo_url when no mirror is set (byte-identical
  # default). In mirror mode imagePullSecrets collapses to an empty list and
  # RBAC (system:image-puller) authorizes the pods' pulls.
  cneinstance_registry_uri = replace(coalesce(var.far_image_repo_url, var.far_repo_url), "https://", "")
  # An EXTERNAL mirror (private Harbor/Artifactory) authorizes by credential, not by
  # RBAC — so the component pods need a pull secret, not an empty list. The secret
  # itself is created by the FLO module in this namespace (mirror-secret); dropping
  # it here left the BNK images pulling anonymously against a private registry.
  has_mirror_creds = var.use_registry_mirror && var.registry_mirror_password != ""
  cneinstance_image_pull_secrets = (
    local.has_mirror_creds ? [{ name = "mirror-secret" }] :
    var.use_registry_mirror ? [] : [{ name = "far-secret" }]
  )

  # Define all service accounts that require privileged SCC
  # These service accounts are created by CNEInstance and FLO deployment
  # Must resolve to the SAME account the flo module links the Trusted Profile
  # to — a profile the pod may assume and an SCC the pod may use have to name
  # one account, or one of them is inert. Empty derives FLO's own name.
  trusted_profile_sa = var.trusted_profile_sa_name != "" ? var.trusted_profile_sa_name : "f5-cne-controller-${var.flo_namespace}-f5-cne-controller-serviceaccount"

  # Every entry below is already parameterised on var.flo_namespace /
  # var.utils_namespace, so it follows whatever those are. There used to be a
  # `var.flo_namespace == "f5-bnk" ?` guard on the first group, comparing against
  # the DEFAULT as a literal — which meant any custom flo_namespace silently
  # dropped all nine FLO-side bindings while the utils half stayed. The install
  # then failed in the cluster, at pod start, naming service accounts rather than
  # the setting that caused it (#65).
  # BNK 2.4 collapses the SCC surface (#171). FLO grants its own components what
  # they need, so the guide asks for ONE binding for the install — privileged on
  # the FLO service account — plus one per application namespace. We create
  # roughly nineteen for 2.3.
  #
  # `!= "2.4"` rather than `== "2.3"`: an unrecognised line keeps the 2.3 set,
  # which is over-broad but working, where treating it as 2.4 would strip
  # privileges a future release may still require and fail at pod admission on a
  # running cluster.
  line_pre_24 = var.bnk_line != "2.4"

  # See the note on the readiness gate below. 2.3 cannot wait on the aggregate
  # without deadlocking against its own licensing step; 2.4 must, because
  # CNEControllerAvailable is True on an install where TMM is 0/3 and nothing
  # passes traffic.
  # The GATE is CNEControllerAvailable on BOTH lines, and that is not the same
  # question as "is this install healthy".
  #
  # It flips when the CNE controller is up, which is what licensing needs — and
  # the License CR is gated on this id. Waiting on the aggregate Available here
  # DEADLOCKS on either line: Available cannot go True until TMM is licensed, TMM
  # cannot be licensed until the License CR applies, and the License CR waits on
  # this gate. Observed on a live 2.4 install (#167): 16 conditions True,
  # F5TmmAvailable=False (Pending), no License CR, and the 15-minute wait timed
  # out having blocked the very step that would have cleared it.
  #
  # The aggregate is checked AFTER licensing instead — see
  # null_resource.cneinstance_available_24 below.
  cneinstance_ready_condition = "CNEControllerAvailable"

  # The 2.4 set: the one binding the install needs.
  #
  # The guide also asks for one per APPLICATION namespace (`-n f5-app -z default`).
  # Those are not created here: this module knows the FLO and utils namespaces and
  # nothing about where a user will run workloads, and inventing a list would
  # either be empty or wrong. It stays an operator step until an app-namespace
  # input exists, and #175 is where that surface gets designed.
  # The name the resource and the outputs already use, now line-selected. Keeping
  # it means the SCC report in outputs.tf describes what was actually applied
  # rather than the 2.3 set regardless of line.
  scc_policy_assignments = local.line_pre_24 ? local.scc_policy_assignments_23 : local.scc_policy_assignments_24

  scc_policy_assignments_24 = [
    {
      namespace       = var.flo_namespace
      service_account = "flo-f5-lifecycle-operator"
    },
  ]

  scc_policy_assignments_23 = concat(
    # FLO-namespace service accounts.
    [
      {
        namespace       = var.flo_namespace
        service_account = "f5-cne-env-discovery-serviceaccount"
      },
      {
        namespace       = var.flo_namespace
        service_account = "tmm-sa"
      },
      {
        namespace       = var.flo_namespace
        service_account = "f5-dssm"
      },
      {
        namespace       = var.flo_namespace
        service_account = "f5-downloader"
      },
      {
        namespace       = var.flo_namespace
        service_account = local.trusted_profile_sa
      },
      {
        namespace       = var.flo_namespace
        service_account = "f5-afm"
      },
      # Install-guide f5-bnk SCC bindings that were missing: the CIS/BIG-IP
      # controller SA, the FLO operator SA, and the namespace default SA.
      {
        namespace       = var.flo_namespace
        service_account = "f5-bigip-ctlr-serviceaccount"
      },
      {
        namespace       = var.flo_namespace
        service_account = "flo-f5-lifecycle-operator"
      },
      {
        namespace       = var.flo_namespace
        service_account = "default"
      }
    ],
    # Utils-namespace service accounts.
    [
      {
        namespace       = var.utils_namespace
        service_account = "crd-installer"
      },
      {
        namespace       = var.utils_namespace
        service_account = "cwc"
      },
      {
        namespace       = var.utils_namespace
        service_account = "f5-coremond"
      },
      {
        namespace       = var.utils_namespace
        service_account = "f5-crdconversion"
      },
      {
        namespace       = var.utils_namespace
        service_account = "f5-observer-operator"
      },
      {
        namespace       = var.utils_namespace
        service_account = "f5-rabbitmq"
      },
      {
        namespace       = var.utils_namespace
        service_account = "f5-toda-fluentd-serviceaccount"
      },
      {
        namespace       = var.utils_namespace
        service_account = "otel-sa"
      },
      {
        namespace       = var.utils_namespace
        service_account = "default"
      },
      {
        namespace       = var.utils_namespace
        service_account = "f5-ipam-ctlr"
      }
    ]
  )

  # GTM / BIG-IP DNS connection (#51). Built as a CONDITIONAL list rather than
  # six always-present entries with empty values: an existing deployment that
  # sets no GTM variables must produce no diff at all. Six new env entries in the
  # CNEInstance spec is a real yaml_body change, so a no-op `bnk up` would
  # server-side-apply a changed CR, FLO would reconcile it, and the CNE
  # controller pod template would change — bouncing the controller on a running
  # cluster for a feature nobody enabled.
  #
  # It also avoids asserting that an empty GTM_URL means "off". If a controller
  # build does read that name, an empty value may select a GSLB path with a blank
  # endpoint rather than no path at all.
  cnecontroller_gtm_env = var.cneinstance_gtm_url == "" ? [] : [
    { name = "GSLB_GTM_URL", value = var.cneinstance_gtm_url },
    { name = "GSLB_GTM_USERNAME", value = var.cneinstance_gtm_username },
    { name = "GSLB_GTM_PASSWORD", value = var.cneinstance_gtm_password },
    # Emitted under both prefixes for the same reason CLOUD_VPC sits beside
    # VPC_NAME: the real names are F5's contract from the install guide.
    # VERIFY against BNK 2.3 and drop the pair that is not real.
    { name = "GTM_URL", value = var.cneinstance_gtm_url },
    { name = "GTM_USERNAME", value = var.cneinstance_gtm_username },
    { name = "GTM_PASSWORD", value = var.cneinstance_gtm_password },
  ]

  # ── advanced.<component>.env ────────────────────────────────────────────────
  #
  # The defaults this module has always emitted, hoisted so that
  # `cneinstance_advanced_env` (#175) can actually reach them. That variable was
  # declared, rendered as a tfvar and documented, but nothing in terraform read
  # it — the override was a no-op end to end.
  #
  # A user entry REPLACES a default of the same name rather than appending a
  # second copy: the CNEInstance spec is read by the lifecycle operator, not by
  # kubelet, so "last duplicate wins" is not a rule we get to rely on.
  #
  # With no overrides set, the filter removes nothing and the appended list is
  # empty, so every component renders exactly the bytes it rendered before.
  adv_env_defaults = {
    coremon = [
      {
        name  = "COREMOND_OVERRIDE_CORE_PATTERN"
        value = "true"
      }
    ]
    cneController = concat([
      {
        name  = "TMM_DEFAULT_MTU"
        value = "9000"
      },
      {
        name  = "CLOUD_ENV"
        value = tostring(var.cneinstance_cloud_env)
      },
      {
        name  = "CLOUD_PROVIDER"
        value = var.cneinstance_cloud_provider
      },
      {
        name  = "CLOUD_NETWORK_CONFIGMAP"
        value = "cloud-network-mapping"
      },
      {
        name  = "VPC_NAME"
        value = var.cneinstance_vpc_name
      },
      {
        name  = "CLOUD_REGION"
        value = var.cneinstance_cloud_region
      },
      {
        name  = "IBM_TRUSTED_PROFILE_ID"
        value = var.cneinstance_ibm_trusted_profile_id
      },
      {
        name  = "GSLB_DATACENTER_NAME"
        value = var.cneinstance_gslb_datacenter_name
      },
      {
        name  = "CLOUD_VPC"
        value = var.cneinstance_vpc_name
      },
      {
        name  = "CLOUD_TRUSTED_PROFILE"
        value = var.cneinstance_ibm_trusted_profile_id
      }
    ], local.cnecontroller_gtm_env)
    tmm = [
      {
        name  = "TMM_CALICO_ROUTER"
        value = "default"
      },
      {
        name  = "TMM_DEFAULT_MTU"
        value = "9000"
      },
      {
        name  = "PAL_CPU_SET"
        value = "0,2"
      },
      {
        name  = "TMM_MAPRES_ADDL_VETHS_ON_DP"
        value = "TRUE"
      },
      # Pod CIDR TMM routes to (install-guide value = ROKS default pod
      # subnet). Was missing — without it TMM can't route to application
      # pods.
      {
        name  = "TMM_K8S_ROUTES"
        value = var.cneinstance_tmm_k8s_routes
      }
    ]
    pseudoCNI = [
      {
        name  = "DISABLE_CHECKSUM_OFFLOAD"
        value = "true"
      }
    ]
  }

  # 2.4 runs the controller off Infra + GatewaySettings instead of the
  # cloud-network-mapping ConfigMap, and the flag that selects that model is
  # this one. Without it the controller never reconciles the CRs `gateway up`
  # applies: they sit at Accepted=Unknown / "Waiting for controller" forever,
  # with an epoch-zero transition time showing nothing ever touched them.
  # Verified against a live 2.4 cluster, which is how this was found.
  #
  # It is a default, not an opt-in, because on 2.4 there is no working
  # configuration without it. It stays overridable like any other entry.
  adv_env_line = local.line_pre_24 ? {} : {
    cneController = [
      {
        name  = "USE_GATEWAY_SETTINGS"
        value = "true"
      },
      # F5's reference pins this; roksbnkctl set it nowhere, so the controller
      # ran on the operator default (v1.4.1 on the verified cluster). The 2.4 EA
      # guide requires the 1.5 bundle for mTLS.
      {
        name  = "GATEWAY_API_VERSION"
        value = var.cneinstance_gateway_api_version
      },
    ]
    # The three TMM settings F5's reference carries that this tree did not.
    tmm = [
      {
        name  = "TMM_IGNORE_GATEWAYS"
        value = "true"
      },
      # Hyperthreading off: TMM pins cores, and a sibling hyperthread on a
      # pinned core is contention the data plane cannot see or schedule around.
      {
        name  = "DISABLE_HT"
        value = "true"
      },
      {
        name  = "ENABLE_K8S_ROUTES"
        value = "true"
      },
    ]
  }

  # ── 2.4 pod placement ───────────────────────────────────────────────────────
  #
  # This is the mechanism that REPLACED the node-labeler on 2.4. #171 removed the
  # labeler because 2.4 does not need it; what it did not do is add the thing
  # that took over, so 2.4 shipped with neither. TMM landing one-per-node and
  # spread across zones was left to the scheduler's discretion.
  # Every attribute of the reference placement block is a variable, including
  # the topology keys. Those keys are the IBM ROKS node labels — `kubernetes.io/
  # hostname` and `topology.kubernetes.io/zone` — and hard-coding them would make
  # this unusable on a cluster that labels its topology differently, which is
  # exactly the assumption the node-labeler used to bake in.
  tmm_pod_selector = {
    matchLabels = {
      app = var.cneinstance_tmm_pod_label
    }
  }

  # Built as lists so the "off" case is an empty list rather than null: a
  # conditional whose branches are an object and null has no consistent type and
  # terraform rejects it at evaluation.
  tmm_anti_affinity_terms = var.cneinstance_tmm_anti_affinity ? [
    {
      labelSelector = local.tmm_pod_selector
      topologyKey   = var.cneinstance_tmm_anti_affinity_topology_key
    },
  ] : []

  tmm_zone_spread_terms = var.cneinstance_tmm_zone_spread ? [
    {
      labelSelector     = local.tmm_pod_selector
      maxSkew           = var.cneinstance_tmm_zone_max_skew
      topologyKey       = var.cneinstance_tmm_zone_topology_key
      whenUnsatisfiable = var.cneinstance_tmm_zone_when_unsatisfiable
    },
  ] : []

  tmm_placement = merge(
    length(local.tmm_anti_affinity_terms) > 0 ? {
      affinity = {
        podAntiAffinity = {
          requiredDuringSchedulingIgnoredDuringExecution = local.tmm_anti_affinity_terms
        }
      }
    } : {},
    length(local.tmm_zone_spread_terms) > 0 ? {
      topologySpreadConstraints = local.tmm_zone_spread_terms
    } : {},
  )

  # Emitted only when something is actually configured, so an operator who turns
  # both off gets no placement key rather than an empty object the CR must
  # interpret.
  # `cond ? {} : {...}` does not type-check in terraform: an empty object and a
  # populated one have different types and it refuses to unify them. A `for` with
  # an `if` yields an empty object of the SAME type, so it composes with merge().
  cneinstance_placement_24 = {
    for k, v in {
      placement = {
        dataPlane = local.tmm_placement
      }
    } : k => v if !local.line_pre_24 && length(local.tmm_placement) > 0
  }

  # demoMode: true is what 2.3 has always shipped; 2.4 conforms to the reference
  # and turns it OFF. An explicit setting wins on either line.
  demo_mode_effective = var.cneinstance_demo_mode != "" ? var.cneinstance_demo_mode == "true" : local.line_pre_24

  cneinstance_conformance_24 = merge({
    for k, v in {
      tmmReplicas     = var.cneinstance_tmm_replicas
      watchNamespaces = var.cneinstance_watch_namespaces
      externalBigip = {
        enabled = var.cneinstance_external_bigip
      }
    } : k => v if !local.line_pre_24
  }, local.cneinstance_placement_24)

  # advanced.externalBigip.env, 2.4 and only when the feature is on.
  adv_external_bigip_24 = {
    for k, v in {
      externalBigip = {
        env = [
          { name = "ENABLE_EXT_BIGIP_DATASERVER_MONITOR", value = "true" },
          { name = "ENABLE_EXT_BIGIP_POOL_MONITOR", value = "true" },
          { name = "EXTERNAL_BIGIP_LOGIN_SECRET", value = var.cneinstance_external_bigip_login_secret },
          { name = "CLUSTER_IDENTIFIER", value = var.cneinstance_cluster_identifier },
        ]
      }
    } : k => v if !local.line_pre_24 && var.cneinstance_external_bigip
  }

  # TMM rolling-update policy, 2.4.
  adv_tmm_rolling_24 = {
    for k, v in {
      rollingUpdate = {
        maxSurge       = 0
        maxUnavailable = 1
      }
    } : k => v if !local.line_pre_24 && var.cneinstance_tmm_rolling_update
  }

  # `lookup(m, k, [])` is not usable here: adv_env_defaults is an OBJECT (its
  # keys hold lists of different lengths), and lookup() requires the default to
  # match the collection's element type. `terraform validate` accepts it and it
  # fails at evaluation, so this is written as an explicit key test instead.
  # The user map is part of the key set, not just the value set: a component
  # that exists only there still has to get an entry in adv_env, or the
  # adv_env_extra lookup below indexes a key that was never built.
  adv_env_base = { for c in distinct(concat(keys(local.adv_env_defaults), keys(local.adv_env_line), keys(var.cneinstance_advanced_env))) :
    c => concat(
      contains(keys(local.adv_env_defaults), c) ? local.adv_env_defaults[c] : [],
      contains(keys(local.adv_env_line), c) ? local.adv_env_line[c] : [],
    )
  }

  adv_env = { for c, d in local.adv_env_base :
    c => concat(
      [for e in d : e if !contains(keys(lookup(var.cneinstance_advanced_env, c, {})), e.name)],
      [for k in sort(keys(lookup(var.cneinstance_advanced_env, c, {}))) :
        { name = k, value = var.cneinstance_advanced_env[c][k] }
      ],
    )
  }

  # Every component that has a STATIC attribute in the advanced block below.
  # This is deliberately NOT keys(adv_env_defaults): three of these seven carry
  # settings but no env defaults, and subtracting only the four with defaults
  # sent them through adv_env_extra into a SHALLOW merge, which replaced the
  # whole object. An override of envDiscovery silently deleted `enabled`,
  # `stopOnFail` and `runAfterSuccess`; demoMode and maintenanceMode lost their
  # `enabled`. Adding a component to the block below means adding it here.
  adv_env_static_components = [
    "coremon", "envDiscovery", "cneController", "demoMode", "maintenanceMode",
    "tmm", "pseudoCNI",
  ]

  # Components the product gains between releases: named only in the user map,
  # with no static attribute of their own. Empty map -> merge() is the identity.
  adv_env_extra = { for c in setsubtract(keys(var.cneinstance_advanced_env), toset(local.adv_env_static_components)) :
    c => { env = local.adv_env[c] }
  }

  # For the three static components with no env defaults, an env list is added
  # only when the user actually asked for one. Attaching `env = []`
  # unconditionally would put a new key in the spec that 2.3 never rendered.
  adv_env_optional = { for c in ["envDiscovery", "demoMode", "maintenanceMode"] :
    c => contains(keys(var.cneinstance_advanced_env), c) ? { env = local.adv_env[c] } : {}
  }

  cneinstance_spec = merge({
    product = {
      gatewayAPI = var.cneinstance_gateway_api
      type       = "BNK"
    }
    manifestVersion = var.f5_bigip_k8s_manifest_version
    wholeCluster    = var.cneinstance_whole_cluster
    telemetry = {
      loggingSubsystem = {
        enabled = var.cneinstance_logging_subsystem
      }
      metricSubsystem = {
        enabled = var.cneinstance_metric_subsystem
      }
    }
    certificate = {
      clusterIssuer = var.cluster_issuer_name
    }
    deploymentSize = var.cneinstance_deployment_size
    registry = {
      uri              = local.cneinstance_registry_uri
      imagePullSecrets = local.cneinstance_image_pull_secrets
      imagePullPolicy  = "Always"
    }
    networkAttachments = var.cneinstance_network_attachments
    dynamicRouting = {
      enabled = var.cneinstance_dynamic_routing
    }
    firewallACL = {
      enabled = var.cneinstance_firewall_acl
    }
    pseudoCNI = {
      enabled = var.cneinstance_pseudocni
    }
    coreCollection = {
      enabled = true
    }
    advanced = merge({
      coremon = {
        hostPath = true
        env      = local.adv_env["coremon"]
      }
      envDiscovery = merge({
        enabled         = var.cneinstance_env_discovery
        stopOnFail      = var.cneinstance_env_discovery
        runAfterSuccess = var.cneinstance_env_discovery
      }, local.adv_env_optional["envDiscovery"])
      cneController = {
        env = local.adv_env["cneController"]
      }
      demoMode = merge({
        enabled = local.demo_mode_effective
      }, local.adv_env_optional["demoMode"])
      maintenanceMode = merge({
        enabled = false
      }, local.adv_env_optional["maintenanceMode"])
      tmm = merge({
        env = local.adv_env["tmm"]
      }, local.adv_tmm_rolling_24)
      pseudoCNI = {
        env = local.adv_env["pseudoCNI"]
      }
    }, local.adv_env_extra, local.adv_external_bigip_24)
  }, local.cneinstance_conformance_24)
}

locals {
  cneinstance_manifest = {
    apiVersion = "k8s.f5.com/v1"
    kind       = "CNEInstance"
    metadata = {
      labels = {
        "app.kubernetes.io/name"       = "f5-lifecycle-operator"
        "app.kubernetes.io/managed-by" = "kustomize"
      }
      name      = local.cneinstance_name
      namespace = var.flo_namespace
    }
    spec = local.cneinstance_spec
  }
}

# ============================================================
# kubectl mode (terraform-native)
# ============================================================
# CNEInstance CR as a real terraform resource + wait_for the FLO-reported
# Available condition; the SCC ClusterRoleBindings as plain kubectl_manifest
# (no wait), re-pointed to depend on the FLO helm_release (via
# flo_deployment_dependency) rather than on the CNEInstance so they
# parallelize. No time_sleep.

# The OpenShift ingress operator installs a ValidatingAdmissionPolicyBinding
# (openshift-ingress-operator-gatewayapi-crd-admission) that blocks third-party
# Gateway API CRD creation. The FLO crd-installer Job must see it (and its policy)
# GONE ~1-3 min into the CNEInstance reconcile, and the ingress operator recreates
# them within ~1 min, so a single delete loses the race.
#
# This used to be a DETACHED `nohup` bash loop (deleting every 5s for ~5m) — which
# does not port to Windows. It has been lifted OUT of terraform into roksbnkctl's
# bnk-up orchestration: a Go GOROUTINE runs the same delete-if-present sweep for the
# duration of `terraform apply` (identical on Windows/Linux, no detached process).
# See internal/orchestration/admission_sweep.go and the PRD
# docs/prd/native-windows-tfx.md §"The one imperative case".

# ── Cloud network mapping ConfigMap + external/internal F5SPKVlan CRs ──────────
# (BNK 2.3 install-guide "Configuration"). The CNE controller reads
# CLOUD_NETWORK_CONFIGMAP=cloud-network-mapping for the zone→CIDR map that
# programs TMM's data-plane networking, so the ConfigMap must exist BEFORE the
# CNEInstance reconciles (cneinstance depends_on it below) — otherwise TMM's
# RoutingDone readiness gate never flips. The F5SPKVlan CRs program TMM's
# external/internal self-IPs and are applied AFTER the CNEInstance is Available
# (their CRD ships with the BNK manifest the operator installs). Zone NAMES are
# derived from the deployment region; CIDRs/self-IPs come from variables.
locals {
  cnm_zone_letters = ["a", "b", "c", "d", "e", "f", "g", "h"]
  cnm_zone_names   = [for i in range(length(var.cneinstance_network_zones)) : "${var.cneinstance_cloud_region}-${i + 1}"]

  cloud_network_mapping_manifest = {
    apiVersion = "v1"
    kind       = "ConfigMap"
    metadata = {
      name      = "cloud-network-mapping"
      namespace = var.flo_namespace
      labels    = { app = "f5-cne-controller", component = "network-config" }
    }
    data = {
      "config.yaml" = yamlencode({
        availability_zones = [
          for i, z in var.cneinstance_network_zones : {
            name = local.cnm_zone_names[i]
            subnets = [
              { cidr = z.ext_vlan_cidr, subnet_id = "ext-vlan-${local.cnm_zone_letters[i]}" },
              { cidr = z.int_vlan_cidr, subnet_id = "int-vlan-${local.cnm_zone_letters[i]}" },
              { cidr = z.int_snat_cidr, subnet_id = "int-snat-${local.cnm_zone_letters[i]}" },
              { cidr = z.int_vip_cidr, subnet_id = "int-vip-${local.cnm_zone_letters[i]}" },
            ]
          }
        ]
      })
    }
  }

  # 0 means "inherit", so a deployment that never sets these behaves exactly as
  # it did when one scalar served both VLANs.
  vlan_prefixlen_external = var.cneinstance_vlan_prefixlen_external != 0 ? var.cneinstance_vlan_prefixlen_external : var.cneinstance_vlan_prefixlen
  vlan_prefixlen_internal = var.cneinstance_vlan_prefixlen_internal != 0 ? var.cneinstance_vlan_prefixlen_internal : var.cneinstance_vlan_prefixlen

  external_vlan_manifest = {
    apiVersion = "k8s.f5net.com/v1"
    kind       = "F5SPKVlan"
    metadata   = { name = "external-vlan", namespace = var.flo_namespace }
    spec = {
      name         = "external-vlan"
      interfaces   = [var.cneinstance_vlan_external_interface]
      selfip_v4s   = [for z in var.cneinstance_network_zones : z.external_selfip]
      prefixlen_v4 = local.vlan_prefixlen_external
      auto_lasthop = "AUTO_LASTHOP_ENABLED"
    }
  }

  internal_vlan_manifest = {
    apiVersion = "k8s.f5net.com/v1"
    kind       = "F5SPKVlan"
    metadata   = { name = "internal-vlan", namespace = var.flo_namespace }
    spec = {
      name         = "internal-vlan"
      interfaces   = [var.cneinstance_vlan_internal_interface]
      selfip_v4s   = [for z in var.cneinstance_network_zones : z.internal_selfip]
      prefixlen_v4 = local.vlan_prefixlen_internal
      auto_lasthop = "AUTO_LASTHOP_ENABLED"
      internal     = true
    }
  }
}

resource "kubectl_manifest" "cloud_network_mapping" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.cloud_network_mapping_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
}

resource "kubectl_manifest" "cneinstance" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.cneinstance_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true

  # We deliberately do NOT use the provider's `wait_for { condition }` here.
  # With alekc/kubectl 2.4.1 + server_side_apply, the wait on the custom
  # CNEControllerAvailable condition did not clear even after the condition was
  # True — it hung for hours, past the timeout — which DEADLOCKED the apply: the
  # License CR is gated on cneinstance_ready_id (this resource), so with the wait
  # stuck the License never applied, TMM never got its license, and in
  # f5licenseproxy mode the CWC polled the proxy forever with an empty
  # entitlement. The readiness gate is now a deterministic API poll
  # (null_resource.cnecontroller_ready below), which drives cneinstance_ready_id.
  # SSA here just creates/updates the CR — fast, no wait.
  # The gateway-api admission-policy sweep that used to be ordered here is now a Go
  # goroutine in roksbnkctl's bnk-up (runs for the whole apply), so no dependency
  # edge is needed — see internal/orchestration/admission_sweep.go.
  depends_on = [
    var.flo_deployment_dependency,
    kubectl_manifest.cloud_network_mapping,
  ]
}

# Deterministic replacement for the provider wait_for above: poll the CNEInstance
# status via the K8s REST API and clear as soon as CNEControllerAvailable=True.
# This is the gate the License CR waits on (cneinstance_ready_id).
# CNEControllerAvailable flips when the CNE controller is up — BEFORE TMM needs its
# license — so licensing (which the License CR drives) can proceed and TMM then
# reaches Available on its own. Bounded (~15m) and fails loudly rather than hanging.
#
# Parsing is pure bash + curl + coreutils (grep/tr) — NO python3 and NO jq. python3
# is deliberately avoided: it is absent in the tools-runner container (the FLP phase
# was reworked for exactly this reason). We first strip ALL whitespace (`tr -d
# '[:space:]'`) so the parse works whether the API server returns pretty-printed or
# compact JSON; then `tr '{}' '\n'` splits the status.conditions array so each
# condition object lands on its own line, and we require the CNEControllerAvailable
# object to also carry status=True (order-independent, no interior spaces).
resource "null_resource" "cnecontroller_ready" {
  count = local.use_kubectl ? 1 : 0

  triggers = {
    cneinstance = kubectl_manifest.cneinstance[0].id
  }

  # tfx wait (watch-first, event-driven) replaces the curl+grep+tr poll: block until
  # the CNEInstance reports condition CNEControllerAvailable=True. No interpreter =>
  # cmd.exe execs roksbnkctl.exe on Windows; token via KUBE_TOKEN env.
  # WHICH CONDITION, and why it differs by line (#167).
  #
  # 2.3 waits on CNEControllerAvailable. That is correct there and deliberate: it
  # flips when the CNE controller is up, BEFORE TMM needs its license, so the
  # License CR — which is gated on this id — can proceed and TMM reaches Available
  # afterwards. Waiting on the aggregate on 2.3 would deadlock: TMM cannot become
  # Available until it is licensed, and licensing waits on this gate.
  #
  # 2.4 must wait on the aggregate Available. On a live 2.4 cluster, while
  # unlicensed:
  #
  #     CNEControllerAvailable=True      <- what a 2.3-style wait sees
  #     Available=False                  <- the truth
  #     F5TmmAvailable=False
  #
  # TMM was 0/3 Ready and nothing could pass traffic, and a 2.3-style wait would
  # have declared that install successful. A false green is worse than a timeout:
  # it sends the operator looking at their own configuration for a fault that is
  # not there.
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx wait --kube-host ${var.kube_host} --insecure --gvr k8s.f5.com/v1/cneinstances --ns ${var.flo_namespace} --name ${local.cneinstance_name} --for condition=${local.cneinstance_ready_condition}=True --timeout 15m"
    environment = {
      KUBE_TOKEN = var.kube_token
    }
  }

  depends_on = [kubectl_manifest.cneinstance]
}

# Gate the F5SPKVlan CRs on the f5validate-f5-bnk admission webhook actually
# SERVING TLS. cnecontroller_ready flips on CNEControllerAvailable=True, but the
# webhook's TLS server (in the f5-cne-controller pod, backing Service
# f5-validation-svc:3340) can lag a few seconds behind that — a real apply in the
# gap fails "http: server gave HTTP response to HTTPS client", which is why
# `bnk up` historically needed a second pass to land the VLANs. Probe the webhook
# with a server-side DRY-RUN apply of the external-vlan (routes through admission,
# persists NOTHING) and retry until it is accepted; then the declarative VLAN
# applies below land on the FIRST `bnk up`. Mirrors the License CR's own
# admission-webhook retry (modules/license) so both consumers of this webhook are
# consistent. Token via KUBE_TOKEN env (kept out of the command string).
resource "null_resource" "validation_webhook_ready" {
  count = local.use_kubectl ? 1 : 0

  triggers = {
    cneinstance = kubectl_manifest.cneinstance[0].id
  }

  provisioner "local-exec" {
    command = <<-EOT
      probe='${jsonencode(local.external_vlan_manifest)}'
      # The F5SPKVlan REST plural is f5-spk-vlans (CRD f5-spk-vlans.k8s.f5net.com),
      # NOT f5spkvlans — the wrong path 404s forever. The kubectl_manifest applies
      # below resolve it via discovery; this raw probe must spell it out. Early 404s
      # are also expected until the CNE reconcile's crd-installer establishes the
      # CRD (retried, same as the webhook-TLS race the gate exists for).
      url="${var.kube_host}/apis/k8s.f5net.com/v1/namespaces/${var.flo_namespace}/f5-spk-vlans/external-vlan?dryRun=All&fieldManager=roksbnkctl-webhook-probe&force=true"
      status=000
      for i in $(seq 1 60); do
        status=$(curl -sk -o /dev/null -w "%%{http_code}" -X PATCH \
          -H "Authorization: Bearer $KUBE_TOKEN" \
          -H "Content-Type: application/apply-patch+yaml" \
          "$url" -d "$probe")
        case "$status" in 2??) break ;; esac
        echo "f5-spk-vlans admission not ready yet (attempt $i/60, HTTP $status), retrying in 10s..." >&2
        sleep 10
      done
      case "$status" in
        2??) echo "f5validate webhook serving for f5-spk-vlans (dry-run accepted, HTTP $status)" ;;
        *)   echo "ERROR: f5-spk-vlans admission webhook not ready after 600s (last HTTP $status)" >&2; exit 1 ;;
      esac
    EOT
    environment = {
      KUBE_TOKEN = var.kube_token
    }
  }

  depends_on = [null_resource.cnecontroller_ready]
}

resource "kubectl_manifest" "external_vlan" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.external_vlan_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [null_resource.cnecontroller_ready, null_resource.validation_webhook_ready]
}

resource "kubectl_manifest" "internal_vlan" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.internal_vlan_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [null_resource.cnecontroller_ready, null_resource.validation_webhook_ready]
}

resource "kubectl_manifest" "cneinstance_scc_policies" {
  # distinct() because collapsing both namespaces onto one makes some
  # (namespace, service_account) pairs identical — `default` exists in both
  # groups — and a `for` expression that produces the same key twice is a
  # plan-time error, not a merge. The entries carry only those two fields, so an
  # identical key means an identical object and dropping the duplicate loses
  # nothing (#66).
  for_each = local.use_kubectl ? {
    for assignment in distinct(local.scc_policy_assignments) :
    # "/" rather than "-": a namespace cannot contain a slash, so the key is
    # collision-free. With "-", the pairs (f5, bnk-default) and (f5-bnk, default)
    # produce the same key and distinct() would not save them — no plausible
    # naming hits it today, but the separator costs nothing to get right.
    "${assignment.namespace}/${assignment.service_account}" => assignment
  } : {}

  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  yaml_body = yamlencode({
    apiVersion = "rbac.authorization.k8s.io/v1"
    kind       = "ClusterRoleBinding"
    metadata   = { name = "system:openshift:scc:privileged:${each.value.namespace}:${each.value.service_account}" }
    roleRef = {
      apiGroup = "rbac.authorization.k8s.io"
      kind     = "ClusterRole"
      name     = "system:openshift:scc:privileged"
    }
    subjects = [{
      kind      = "ServiceAccount"
      name      = each.value.service_account
      namespace = each.value.namespace
    }]
  })

  # Architect: depend on FLO (chart present), NOT on the CNEInstance — lets the
  # ~16 bindings apply concurrently with the CNEInstance Available wait.
  depends_on = [var.flo_deployment_dependency]
}


# ── F5BigTcpSetting ──────────────────────────────────────────────────────────
#
# The data-plane TCP profile. F5's approved reference cluster carries a
# hand-applied `sys-default-tcp` in the BNK namespace — despite the "sys-default"
# name it was applied by engineering, not created by the product, which is why it
# is ours to surface.
#
# Only written when the operator sets something. The product manages its own
# default otherwise, and an empty CR would be this tree fighting it.
#
# Values arrive as strings because config.yaml and environment variables carry
# text; the CR is typed, so each value is coerced here — a number if it parses as
# one, a bool for "true"/"false", a string otherwise.
resource "kubectl_manifest" "tcp_settings" {
  count             = local.use_kubectl && length(var.cneinstance_tcp_settings) > 0 ? 1 : 0
  server_side_apply = true
  force_conflicts   = true

  # Written as YAML text rather than yamlencode(map) because a terraform map has
  # ONE value type: a `for` producing mixed bool/number/string unifies them all
  # to string, and the CR would get "300" where it wants 300. Emitting the
  # document directly lets YAML's own scalar rules do the typing — an unquoted
  # 300 is an int, true is a bool — and anything else is JSON-quoted so a value
  # containing a colon or a leading special character stays a valid string.
  yaml_body = <<-YAML
    apiVersion: k8s.f5net.com/v1
    kind: F5BigTcpSetting
    metadata:
      name: ${var.cneinstance_tcp_settings_name}
      namespace: ${var.flo_namespace}
    spec:
    ${indent(2, join("\n", [
  for k in sort(keys(var.cneinstance_tcp_settings)) :
  "${k}: ${(
    can(tonumber(var.cneinstance_tcp_settings[k])) ||
    contains(["true", "false"], lower(var.cneinstance_tcp_settings[k]))
  ) ? lower(var.cneinstance_tcp_settings[k]) : jsonencode(var.cneinstance_tcp_settings[k])}"
]))}
  YAML

depends_on = [kubectl_manifest.cneinstance]
}
