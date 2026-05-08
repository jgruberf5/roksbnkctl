locals {
  cneinstance_name = "${var.flo_namespace}-f5-cne-controller"

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
      uri = replace(var.far_repo_url, "https://", "")
      imagePullSecrets = [
        {
          name = "far-secret"
        }
      ]
      imagePullPolicy = "Always"
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

# Wait for CNEInstance CRD to be available
resource "time_sleep" "wait_for_cneinstance_crd" {
  count           = var.enabled ? 1 : 0
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

# Create CNEInstance resource via curl Server-Side Apply
resource "null_resource" "cneinstance" {
  count = var.enabled ? 1 : 0

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
  for_each = {
    for assignment in local.scc_policy_assignments :
    "${assignment.namespace}-${assignment.service_account}" => assignment
  }

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
  count           = var.enabled ? 1 : 0
  depends_on      = [null_resource.cneinstance_scc_policies]
  create_duration = "30s"

  triggers = {
    scc_policies_count = length(null_resource.cneinstance_scc_policies)
  }
}

