terraform {
  required_version = ">= 1.2.0"

  required_providers {
    oci = {
      source  = "oracle/oci"
      version = ">= 6.0.0"
    }
    random = {
      source  = "hashicorp/random"
      version = ">= 3.5.0"
    }
  }
}

# Resource Manager supplies credentials for the signed-in user; only the region is set here.
provider "oci" {
  region = var.region
}
