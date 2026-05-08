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
