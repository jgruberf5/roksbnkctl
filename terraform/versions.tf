terraform {
  required_version = ">= 1.5"

  # Each constraint is bounded at the major currently pinned in
  # .terraform.lock.hcl, which ships in the binary (#147). The lockfile decides
  # what actually gets installed; these bounds are the floor under a
  # `terraform init -upgrade` or any run where the lockfile is absent, where a
  # bare ">=" would happily accept a breaking major. "~> 1.65" is exactly
  # ">= 1.65, < 2.0", so each of these keeps its original floor except
  # kubernetes, whose floor rises from 2.25 to the 3.x the lockfile already
  # pins. Module-level required_providers keep their ">=" forms: terraform
  # intersects every constraint, so the bound here governs — which is why every
  # provider the lockfile pins is declared here, including the four the root
  # module does not use itself.

  required_providers {
    ibm = {
      source  = "IBM-Cloud/ibm"
      version = "~> 1.65"
    }
    null = {
      source  = "hashicorp/null"
      version = "~> 3.0"
    }
    http = {
      source  = "hashicorp/http"
      version = "~> 3.0"
    }
    kubernetes = {
      source  = "hashicorp/kubernetes"
      version = "~> 3.0"
    }
    helm = {
      source  = "hashicorp/helm"
      version = "~> 2.12"
    }
    kubectl = {
      source  = "alekc/kubectl"
      version = "~> 2.4"
    }
    # Declared here even though the root module does not use them directly.
    # terraform intersects every constraint in the tree, so a bound at the root
    # governs the whole configuration — but only for providers the root
    # actually names. tls, time, local and external appear ONLY in submodules,
    # all with bare ">=", so before this they were unbounded everywhere and a
    # breaking major would have been accepted. Each keeps its existing floor.
    tls = {
      source  = "hashicorp/tls"
      version = "~> 4.0"
    }
    time = {
      source  = "hashicorp/time"
      version = "~> 0.9"
    }
    local = {
      source  = "hashicorp/local"
      version = "~> 2.0"
    }
    external = {
      source  = "hashicorp/external"
      version = "~> 2.0"
    }
  }
}
