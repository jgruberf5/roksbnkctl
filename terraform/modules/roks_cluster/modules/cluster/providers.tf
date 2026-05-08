terraform {
  required_version = ">= 1.3"
  required_providers {
    ibm = {
      source  = "IBM-Cloud/ibm"
      version = "~> 1.60"
    }
    null = {
      source  = "hashicorp/null"
      version = ">= 3.0"
    }
  }
}
