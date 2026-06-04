# ============================================================
# Cert-Manager Module
# Manages cert-manager installation. Two install mechanisms,
# selected by var.bnk_cr_mode:
#
#   - "kubectl" (default, Sprint 27 terraform-native): the namespace
#     is a kubernetes_namespace and the chart is a helm_release
#     (wait = true). No local-exec, no time_sleep.
#   - "legacy_curl": the original null_resource local-exec kubectl/helm
#     bring-up gated by a fixed time_sleep. Kept byte-identical as the
#     validator's benchmark baseline.
#
# The providers (kubernetes / helm) are inherited from the wrapping
# module's providers.tf, wired from ibm_container_cluster_config.
# ============================================================

terraform {
  required_providers {
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = ">= 2.25"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.12"
    }
    null = {
      source  = "hashicorp/null"
      version = ">= 3.2.0"
    }
    time = {
      source  = "hashicorp/time"
      version = ">= 0.9.0"
    }
  }
}

locals {
  use_kubectl = var.enabled && var.bnk_cr_mode == "kubectl"
  use_legacy  = var.enabled && var.bnk_cr_mode == "legacy_curl"
}

# ============================================================
# kubectl mode (Sprint 27 terraform-native)
# ============================================================

# cert-manager namespace via the kubernetes provider — precedes the chart.
resource "kubernetes_namespace_v1" "cert_manager" {
  count = local.use_kubectl ? 1 : 0

  metadata {
    name = var.namespace
  }
}

# cert-manager chart via helm_release (wait = true) — real rollout readiness,
# replacing the legacy --wait helm local-exec + the post-deployment time_sleep.
resource "helm_release" "cert_manager" {
  count = local.use_kubectl ? 1 : 0

  name       = "cert-manager"
  repository = var.chart_repository
  chart      = "cert-manager"
  version    = var.chart_version
  namespace  = var.namespace

  wait    = true
  timeout = var.timeout

  set {
    name  = "installCRDs"
    value = "true"
  }

  set {
    name  = "featureGates"
    value = "ServerSideApply=true"
  }

  depends_on = [kubernetes_namespace_v1.cert_manager]
}

# ============================================================
# legacy_curl mode (unchanged baseline)
# ============================================================

# Create cert-manager namespace via kubectl local-exec
resource "null_resource" "cert_manager_namespace" {
  count = local.use_legacy ? 1 : 0

  triggers = {
    namespace  = var.namespace
    kube_host  = var.kube_host
    kube_token = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      kubectl --server="${var.kube_host}" --token="${var.kube_token}" --insecure-skip-tls-verify=true \
        create namespace "${var.namespace}" --dry-run=client -o yaml | \
      kubectl --server="${var.kube_host}" --token="${var.kube_token}" --insecure-skip-tls-verify=true \
        apply -f -
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      kubectl --server="${self.triggers.kube_host}" --token="${self.triggers.kube_token}" --insecure-skip-tls-verify=true \
        delete namespace "${self.triggers.namespace}" --ignore-not-found=true
    EOT
  }
}

# Install cert-manager via Helm CLI local-exec
resource "null_resource" "cert_manager" {
  count = local.use_legacy ? 1 : 0

  triggers = {
    chart_version = var.chart_version
    namespace     = var.namespace
    kube_host     = var.kube_host
    kube_token    = var.kube_token
  }

  provisioner "local-exec" {
    command = <<-EOT
      helm upgrade --install cert-manager \
        --repo "${var.chart_repository}" \
        --namespace "${var.namespace}" \
        --create-namespace \
        --version "${var.chart_version}" \
        --set installCRDs=true \
        --set "featureGates=ServerSideApply=true" \
        --wait --timeout "${var.timeout}s" \
        --kube-apiserver="${var.kube_host}" \
        --kube-token="${var.kube_token}" \
        --kube-insecure-skip-tls-verify=true \
        cert-manager
    EOT
  }

  provisioner "local-exec" {
    when    = destroy
    command = <<-EOT
      helm uninstall cert-manager \
        --namespace "${self.triggers.namespace}" \
        --kube-apiserver="${self.triggers.kube_host}" \
        --kube-token="${self.triggers.kube_token}" \
        --kube-insecure-skip-tls-verify=true \
        --ignore-not-found || true
    EOT
  }

  depends_on = [null_resource.cert_manager_namespace[0]]
}

# Wait for cert-manager CRDs to be fully registered
# This ensures ClusterIssuer, Certificate, and other cert-manager CRDs
# are available before dependent resources try to use them
resource "time_sleep" "cert_manager_ready" {
  count = local.use_legacy ? 1 : 0

  depends_on      = [null_resource.cert_manager[0]]
  create_duration = "${var.post_deployment_delay}s"
}
