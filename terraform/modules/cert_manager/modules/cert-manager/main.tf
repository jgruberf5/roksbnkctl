# ============================================================
# Cert-Manager Module
# Manages cert-manager installation: the namespace is a
# kubernetes_namespace and the chart is a helm_release (wait = true).
# No local-exec, no time_sleep.
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
  use_kubectl = var.enabled

  # Sprint 29 air-gap mirror — cert-manager's chart pins each component image to a
  # full quay.io/jetstack/cert-manager-<comp> path, and the mirror keeps that
  # layout under the image host. Redirect ALL of them: overriding only
  # image.repository (the controller) leaves webhook/cainjector/startupapicheck on
  # quay.io, which is blocked under air-gap and hangs the helm_release. Empty
  # var.image_repository (the mirror image host) leaves the chart's public defaults.
  cm_image_sets = var.image_repository == "" ? {} : {
    "image.repository"                 = "${var.image_repository}/jetstack/cert-manager-controller"
    "webhook.image.repository"         = "${var.image_repository}/jetstack/cert-manager-webhook"
    "cainjector.image.repository"      = "${var.image_repository}/jetstack/cert-manager-cainjector"
    "startupapicheck.image.repository" = "${var.image_repository}/jetstack/cert-manager-startupapicheck"
    "acmesolver.image.repository"      = "${var.image_repository}/jetstack/cert-manager-acmesolver"
  }

  # A private external mirror needs a pull secret — redirecting the images at it is
  # not enough, the pods still have to authenticate. Without this they pull
  # anonymously and land in ImagePullBackOff unless the mirror is world-readable,
  # which is not something to require of a registry holding F5's images.
  has_mirror_creds        = var.image_repository != "" && var.registry_mirror_password != ""
  mirror_pull_secret_name = "mirror-secret"
  # kubelet matches image refs by registry HOST, so strip the repo-prefix path the
  # image host may carry (harbor.example.com/bnk-mirror → harbor.example.com).
  mirror_registry_host = split("/", var.image_repository)[0]
  mirror_docker_config_json = replace(
    jsonencode({
      auths = {
        (local.mirror_registry_host) = {
          auth = base64encode("${var.registry_mirror_username}:${var.registry_mirror_password}")
        }
      }
    }),
    ":", ": "
  )

  # Every cert-manager component reads its own imagePullSecrets list.
  cm_pull_secret_sets = local.has_mirror_creds ? {
    "global.imagePullSecrets[0].name" = local.mirror_pull_secret_name
  } : {}
}

# Pull secret for a private external mirror, in cert-manager's own namespace.
resource "kubernetes_secret_v1" "mirror_pull" {
  count = local.use_kubectl && local.has_mirror_creds ? 1 : 0
  metadata {
    name      = local.mirror_pull_secret_name
    namespace = var.namespace
  }
  type = "kubernetes.io/dockerconfigjson"
  data = {
    ".dockerconfigjson" = local.mirror_docker_config_json
  }
  depends_on = [kubernetes_namespace_v1.cert_manager]
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

# cert-manager chart via helm_release (wait = true) — real rollout readiness.
resource "helm_release" "cert_manager" {
  count = local.use_kubectl ? 1 : 0

  name       = "cert-manager"
  repository = var.chart_repository
  chart      = "cert-manager"
  version    = var.chart_version
  namespace  = var.namespace

  wait    = true
  timeout = var.timeout

  # crds.enabled, NOT installCRDs. cert-manager >= 1.15 replaced installCRDs with
  # crds.enabled (and crds.keep); 1.17.3 still honours the old name as an alias
  # and prints on every plan and apply:
  #
  #   ⚠️  WARNING: `installCRDs` is deprecated, use `crds.enabled` instead.
  #
  # Same value, same behaviour, and it keeps working when the alias is dropped.
  #
  # crds.keep is deliberately left at its default (true), which is what the alias
  # already did: cert-manager's CRDs are cluster-scoped and shared, and removing
  # them on uninstall would delete every Certificate and Issuer on the cluster,
  # including ones this deployment never created.
  set {
    name  = "crds.enabled"
    value = "true"
  }

  set {
    name  = "featureGates"
    value = "ServerSideApply=true"
  }

  # Sprint 29 air-gap mirror — redirect every cert-manager component image at the
  # in-cluster mirror (see local.cm_image_sets). Empty image_repository → no sets,
  # so the chart's public defaults stand.
  dynamic "set" {
    for_each = local.cm_image_sets
    content {
      name  = set.key
      value = set.value
    }
  }

  # …and, for a PRIVATE external mirror, the credential to pull them with.
  dynamic "set" {
    for_each = local.cm_pull_secret_sets
    content {
      name  = set.key
      value = set.value
    }
  }

  depends_on = [kubernetes_namespace_v1.cert_manager]
}
