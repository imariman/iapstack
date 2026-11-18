# Populated automatically by Oracle Resource Manager.
variable "tenancy_ocid" {
  type = string
}

variable "region" {
  type = string
}

variable "compartment_ocid" {
  description = "Compartment for the network and the VM."
  type        = string
}

variable "availability_domain" {
  description = "Availability domain name. Always Free Ampere A1 capacity varies by domain."
  type        = string
}

variable "instance_ocpus" {
  description = "Ampere A1 OCPUs. Always Free covers 4 OCPUs in total across the tenancy."
  type        = number
  default     = 2

  validation {
    condition     = var.instance_ocpus >= 1 && var.instance_ocpus <= 4
    error_message = "instance_ocpus must be between 1 and 4."
  }
}

variable "instance_memory_gbs" {
  description = "Memory in GB. Always Free covers 24 GB in total across the tenancy."
  type        = number
  default     = 12

  validation {
    condition     = var.instance_memory_gbs >= 2 && var.instance_memory_gbs <= 24
    error_message = "instance_memory_gbs must be between 2 and 24."
  }
}

variable "boot_volume_size_gbs" {
  description = "Boot volume size in GB. Always Free covers 200 GB of block storage in total."
  type        = number
  default     = 50

  validation {
    condition     = var.boot_volume_size_gbs >= 47 && var.boot_volume_size_gbs <= 200
    error_message = "boot_volume_size_gbs must be between 47 and 200."
  }
}

variable "cloudflare_tunnel_token" {
  description = "Connector token of a remotely managed Cloudflare Tunnel. Leave empty to add it later."
  type        = string
  default     = ""
  sensitive   = true

  validation {
    condition     = length(regexall("^[A-Za-z0-9+/=_-]*$", var.cloudflare_tunnel_token)) == 1
    error_message = "cloudflare_tunnel_token must be the token string shown by Cloudflare, without quotes or spaces."
  }
}

variable "public_hostname" {
  description = "Hostname routed to http://iapstack:8080 in the tunnel, for example iapstack.example.com."
  type        = string
  default     = ""
}

variable "ssh_public_key" {
  description = "Optional SSH public key for the ubuntu user."
  type        = string
  default     = ""
}

variable "ssh_source_cidr" {
  description = "Optional CIDR allowed to reach SSH, for example 203.0.113.10/32. Empty keeps all inbound ports closed."
  type        = string
  default     = ""
}

variable "iapstack_ref" {
  description = "IAPStack Git tag, branch, or full 40-character commit SHA to deploy."
  type        = string
  default     = "main"

  validation {
    condition     = length(regexall("^[A-Za-z0-9._/-]+$", var.iapstack_ref)) == 1
    error_message = "iapstack_ref may contain only letters, digits, dots, underscores, slashes, and hyphens."
  }
}

variable "iapstack_repo" {
  description = "IAPStack repository URL. The install script is read from the matching raw GitHub URL."
  type        = string
  default     = "https://github.com/imariman/iapstack"

  validation {
    condition     = length(regexall("^https://github\\.com/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$", var.iapstack_repo)) == 1
    error_message = "iapstack_repo must be a https://github.com/<owner>/<repository> URL."
  }
}

variable "name_prefix" {
  description = "Prefix for resource display names."
  type        = string
  default     = "iapstack"
}
