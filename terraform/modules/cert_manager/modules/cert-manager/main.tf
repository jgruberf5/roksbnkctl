# ============================================================
# Cert-Manager Module
# Manages cert-manager installation including:
# - Namespace creation (via kubectl local-exec)
# - Helm release deployment (via helm CLI local-exec)
# - Post-deployment wait for CRD availability
# ============================================================

terraform {
  required_providers {
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

# Create cert-manager namespace via kubectl local-exec
resource "null_resource" "cert_manager_namespace" {
  count = var.enabled ? 1 : 0

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
  count = var.enabled ? 1 : 0

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
  count = var.enabled ? 1 : 0

  depends_on      = [null_resource.cert_manager[0]]
  create_duration = "${var.post_deployment_delay}s"
}
