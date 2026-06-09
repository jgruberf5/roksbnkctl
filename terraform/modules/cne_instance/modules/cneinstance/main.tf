locals {
  use_kubectl = var.enabled && var.bnk_cr_mode == "kubectl"
  use_legacy  = var.enabled && var.bnk_cr_mode == "legacy_curl"

  cneinstance_name = "${var.flo_namespace}-f5-cne-controller"

  # Sprint 29 air-gap mirror — spec.registry.uri host. The image host
  # coalesces back to far_repo_url when no mirror is set (byte-identical
  # default). In mirror mode imagePullSecrets collapses to an empty list and
  # RBAC (system:image-puller) authorizes the pods' pulls.
  cneinstance_registry_uri = replace(coalesce(var.far_image_repo_url, var.far_repo_url), "https://", "")
  cneinstance_image_pull_secrets = var.use_registry_mirror ? [] : [
    {
      name = "far-secret"
    }
  ]

  # Define all service accounts that require privileged SCC
  # These service accounts are created by CNEInstance and FLO deployment
  scc_policy_assignments = concat(
    # f5-bnk namespace service accounts (if this is the main FLO namespace)
    var.flo_namespace == "f5-bnk" ? [
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
        service_account = "f5-cne-controller-${var.flo_namespace}-f5-cne-controller-serviceaccount"
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
    ] : [],
    # f5-utils namespace service accounts
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
        env = [
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
          # BNK 2.3 install-guide env names, emitted ALONGSIDE VPC_NAME /
          # IBM_TRUSTED_PROFILE_ID above for cross-version compatibility: the
          # 2.3 CNE controller reads CLOUD_VPC / CLOUD_TRUSTED_PROFILE for the
          # VPC route programming. Same values; harmless to whichever version
          # ignores them.
          {
            name  = "CLOUD_VPC"
            value = var.cneinstance_vpc_name
          },
          {
            name  = "CLOUD_TRUSTED_PROFILE"
            value = var.cneinstance_ibm_trusted_profile_id
          }
        ]
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

# Wait for CNEInstance CRD to be available (legacy mode only — kubectl mode
# gates on the FLO helm_release ordering + the CNEInstance Available condition).
resource "time_sleep" "wait_for_cneinstance_crd" {
  count           = local.use_legacy ? 1 : 0
  depends_on      = [var.flo_deployment_dependency]
  create_duration = "30s"

  triggers = {
    flo_deployed = var.flo_deployment_id
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

# Create CNEInstance resource via curl Server-Side Apply (legacy mode)
resource "null_resource" "cneinstance" {
  count = local.use_legacy ? 1 : 0

  triggers = {
    manifest  = jsonencode(local.cneinstance_manifest)
    kube_host = var.kube_host
    token     = var.kube_token
    namespace = var.flo_namespace
    name      = local.cneinstance_name
  }

  provisioner "local-exec" {
    command = <<-EOT
      printf '%s' "${base64encode(jsonencode(local.cneinstance_manifest))}" | base64 -d | \
      curl -f -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        -k "${var.kube_host}/apis/k8s.f5.com/v1/namespaces/${var.flo_namespace}/cneinstances/${local.cneinstance_name}?fieldManager=terraform&force=true" \
        --data-binary @-
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.kube_host}/apis/k8s.f5.com/v1/namespaces/${self.triggers.namespace}/cneinstances/${self.triggers.name}" || true
    EOT
  }

  depends_on = [time_sleep.wait_for_cneinstance_crd[0]]
}

# ============================================================
# OpenShift Security Context Constraint (SCC) Policies
# ============================================================
# Apply privileged SCC to service accounts created by CNEInstance deployment
# via curl server-side apply — no kubernetes provider required at plan time.

resource "null_resource" "cneinstance_scc_policies" {
  for_each = local.use_legacy ? {
    for assignment in local.scc_policy_assignments :
    "${assignment.namespace}-${assignment.service_account}" => assignment
  } : {}

  triggers = {
    name      = "system:openshift:scc:privileged:${each.value.namespace}:${each.value.service_account}"
    namespace = each.value.namespace
    sa        = each.value.service_account
    kube_host = var.kube_host
    token     = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      NAME="system:openshift:scc:privileged:${each.value.namespace}:${each.value.service_account}"
      curl -sf -X PATCH \
        -H "Authorization: Bearer ${var.kube_token}" \
        -H "Content-Type: application/apply-patch+yaml" \
        -k "${var.kube_host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/$NAME?fieldManager=terraform&force=true" \
        -d "{\"apiVersion\":\"rbac.authorization.k8s.io/v1\",\"kind\":\"ClusterRoleBinding\",\"metadata\":{\"name\":\"$NAME\"},\"roleRef\":{\"apiGroup\":\"rbac.authorization.k8s.io\",\"kind\":\"ClusterRole\",\"name\":\"system:openshift:scc:privileged\"},\"subjects\":[{\"kind\":\"ServiceAccount\",\"name\":\"${each.value.service_account}\",\"namespace\":\"${each.value.namespace}\"}]}"
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      curl -sk -X DELETE \
        -H "Authorization: Bearer ${self.triggers.token}" \
        "${self.triggers.kube_host}/apis/rbac.authorization.k8s.io/v1/clusterrolebindings/${self.triggers.name}" || true
    EOT
  }

  depends_on = [null_resource.cneinstance[0]]
}

# ============================================================
# Wait for SCC Policies to Propagate
# ============================================================

resource "time_sleep" "wait_for_scc_policies" {
  count           = local.use_legacy ? 1 : 0
  depends_on      = [null_resource.cneinstance_scc_policies]
  create_duration = "30s"

  triggers = {
    scc_policies_count = length(null_resource.cneinstance_scc_policies)
  }
}

# ============================================================
# Sprint 27 — kubectl mode (terraform-native)
# ============================================================
# CNEInstance CR as a real terraform resource + wait_for the FLO-reported
# Available condition; the SCC ClusterRoleBindings as plain kubectl_manifest
# (no wait), re-pointed to depend on the FLO helm_release (via
# flo_deployment_dependency) rather than on the CNEInstance so they
# parallelize. No time_sleep.

# The OpenShift ingress operator installs a ValidatingAdmissionPolicyBinding
# (openshift-ingress-operator-gatewayapi-crd-admission) that blocks
# third-party Gateway API CRD creation. The FLO operator reconciles the
# CNEInstance by running a crd-installer Job that creates the Gateway API CRDs
# (e.g. backendtlspolicies.gateway.networking.k8s.io) at the version BNK
# requires, so that binding must be gone WHEN the crd-installer runs — which is
# ~1-3 minutes INTO the CNEInstance reconcile, not up-front. The ingress
# operator reconciles the binding (and its policy) back within ~1 minute, so a
# single up-front delete loses the race. Launch a DETACHED loop that keeps the
# binding + policy deleted every 5s for ~5 minutes, covering the crd-installer
# window. The loop survives the local-exec (nohup) and stays alive because
# terraform is still applying — blocked on the CNEInstance wait_for below —
# during this window. Runs on every apply (timestamp trigger); best-effort.
resource "null_resource" "delete_gatewayapi_admission_policy" {
  count = local.use_kubectl ? 1 : 0

  triggers = {
    always_run = timestamp()
  }

  provisioner "local-exec" {
    interpreter = ["/bin/bash", "-c"]
    command     = <<-EOT
      nohup bash -c '
        token="${var.kube_token}"
        host="${var.kube_host}"
        base="$host/apis/admissionregistration.k8s.io/v1"
        for i in $(seq 1 60); do
          curl -sk -X DELETE -H "Authorization: Bearer $token" "$base/validatingadmissionpolicybindings/openshift-ingress-operator-gatewayapi-crd-admission" -o /dev/null 2>/dev/null || true
          curl -sk -X DELETE -H "Authorization: Bearer $token" "$base/validatingadmissionpolicies/openshift-ingress-operator-gatewayapi-crd-admission" -o /dev/null 2>/dev/null || true
          sleep 5
        done
      ' >/dev/null 2>&1 &
      echo "gateway-api admission-policy delete-loop launched (~5m, covers the crd-installer window)"
    EOT
  }
}

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

  external_vlan_manifest = {
    apiVersion = "k8s.f5net.com/v1"
    kind       = "F5SPKVlan"
    metadata   = { name = "external-vlan", namespace = var.flo_namespace }
    spec = {
      name         = "external-vlan"
      interfaces   = [var.cneinstance_vlan_external_interface]
      selfip_v4s   = [for z in var.cneinstance_network_zones : z.external_selfip]
      prefixlen_v4 = var.cneinstance_vlan_prefixlen
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
      prefixlen_v4 = var.cneinstance_vlan_prefixlen
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

  # Gate on CNEControllerAvailable, NOT the terminal Available. Available also
  # requires F5TmmAvailable, and TMM can't reach ConfigurationDone until it is
  # LICENSED — but the License CR applies AFTER this resource (it depends on
  # cneinstance_ready_id = this resource's id, set only once wait_for clears).
  # Waiting on Available here is therefore a deadlock:
  #   License ← cneinstance.id ← Available ← F5TmmAvailable ← ConfigurationDone ← License.
  # CNEControllerAvailable flips as soon as the CNE controller is up (before TMM
  # needs its license), so the apply proceeds to the License, which licenses
  # TMM, which then reaches Available on its own. The License's own wait_for
  # ("Verification Complete") is the meaningful licensing gate downstream.
  wait_for {
    condition {
      type   = "CNEControllerAvailable"
      status = "True"
    }
  }

  # Generous create timeout: the controller bring-up runs the crd-installer
  # (Gateway API CRDs) which the admission-policy delete-loop has to shepherd
  # through, so first-time reconciles can take well past the provider default.
  timeouts {
    create = "30m"
  }

  depends_on = [
    var.flo_deployment_dependency,
    null_resource.delete_gatewayapi_admission_policy,
    kubectl_manifest.cloud_network_mapping,
  ]
}

resource "kubectl_manifest" "external_vlan" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.external_vlan_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [kubectl_manifest.cneinstance]
}

resource "kubectl_manifest" "internal_vlan" {
  count             = local.use_kubectl ? 1 : 0
  yaml_body         = yamlencode(local.internal_vlan_manifest)
  server_side_apply = true
  field_manager     = "roksbnkctl"
  force_conflicts   = true
  depends_on        = [kubectl_manifest.cneinstance]
}

resource "kubectl_manifest" "cneinstance_scc_policies" {
  for_each = local.use_kubectl ? {
    for assignment in local.scc_policy_assignments :
    "${assignment.namespace}-${assignment.service_account}" => assignment
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

