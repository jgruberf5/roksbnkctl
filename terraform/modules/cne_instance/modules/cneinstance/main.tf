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
  # Every entry below is already parameterised on var.flo_namespace /
  # var.utils_namespace, so it follows whatever those are. There used to be a
  # `var.flo_namespace == "f5-bnk" ?` guard on the first group, comparing against
  # the DEFAULT as a literal — which meant any custom flo_namespace silently
  # dropped all nine FLO-side bindings while the utils half stayed. The install
  # then failed in the cluster, at pod start, naming service accounts rather than
  # the setting that caused it (#65).
  scc_policy_assignments = concat(
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
        service_account = var.trusted_profile_sa_name
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

  cneinstance_spec = {
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
    advanced = {
      coremon = {
        hostPath = true
        env = [
          {
            name  = "COREMOND_OVERRIDE_CORE_PATTERN"
            value = "true"
          }
        ]
      }
      envDiscovery = {
        enabled         = var.cneinstance_env_discovery
        stopOnFail      = var.cneinstance_env_discovery
        runAfterSuccess = var.cneinstance_env_discovery
      }
      cneController = {
        env = concat([
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
      }
      demoMode = {
        enabled = true
      }
      maintenanceMode = {
        enabled = false
      }
      tmm = {
        env = [
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
      }
      pseudoCNI = {
        env = [
          {
            name  = "DISABLE_CHECKSUM_OFFLOAD"
            value = "true"
          }
        ]
      }
    }
  }
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
  provisioner "local-exec" {
    command = "${local.roksbnkctl_bin} tfx wait --kube-host ${var.kube_host} --insecure --gvr k8s.f5.com/v1/cneinstances --ns ${var.flo_namespace} --name ${local.cneinstance_name} --for condition=CNEControllerAvailable=True --timeout 15m"
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

