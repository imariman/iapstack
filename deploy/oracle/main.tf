locals {
  install_script_url = "${replace(trimsuffix(var.iapstack_repo, ".git"), "https://github.com/", "https://raw.githubusercontent.com/")}/${var.iapstack_ref}/deploy/vps/install.sh"
  allow_ssh          = var.ssh_source_cidr != ""
}

data "oci_core_images" "ubuntu_arm" {
  compartment_id           = var.compartment_ocid
  operating_system         = "Canonical Ubuntu"
  operating_system_version = "24.04"
  shape                    = "VM.Standard.A1.Flex"
  sort_by                  = "TIMECREATED"
  sort_order               = "DESC"

  # Exclude the Minimal variant, which lacks packages cloud-init and Docker expect.
  filter {
    name   = "display_name"
    values = ["^Canonical-Ubuntu-24\\.04-aarch64-"]
    regex  = true
  }
}

# Generated once and stored in the stack's Terraform state; shown as a sensitive output.
resource "random_password" "bootstrap_admin_key" {
  length  = 48
  special = false
}

resource "oci_core_vcn" "iapstack" {
  compartment_id = var.compartment_ocid
  display_name   = "${var.name_prefix}-vcn"
  cidr_blocks    = ["10.42.0.0/16"]
  dns_label      = "iapstack"
}

resource "oci_core_internet_gateway" "iapstack" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.iapstack.id
  display_name   = "${var.name_prefix}-igw"
  enabled        = true
}

resource "oci_core_route_table" "iapstack" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.iapstack.id
  display_name   = "${var.name_prefix}-routes"

  route_rules {
    destination       = "0.0.0.0/0"
    destination_type  = "CIDR_BLOCK"
    network_entity_id = oci_core_internet_gateway.iapstack.id
  }
}

# Outbound only: the Cloudflare Tunnel connector dials out, so no inbound rule is
# needed for application traffic. SSH is opened only for an explicitly given CIDR.
resource "oci_core_security_list" "iapstack" {
  compartment_id = var.compartment_ocid
  vcn_id         = oci_core_vcn.iapstack.id
  display_name   = "${var.name_prefix}-security"

  egress_security_rules {
    destination = "0.0.0.0/0"
    protocol    = "all"
  }

  dynamic "ingress_security_rules" {
    for_each = local.allow_ssh ? [var.ssh_source_cidr] : []
    content {
      source   = ingress_security_rules.value
      protocol = "6"
      tcp_options {
        min = 22
        max = 22
      }
    }
  }
}

resource "oci_core_subnet" "iapstack" {
  compartment_id    = var.compartment_ocid
  vcn_id            = oci_core_vcn.iapstack.id
  display_name      = "${var.name_prefix}-subnet"
  cidr_block        = "10.42.1.0/24"
  dns_label         = "app"
  route_table_id    = oci_core_route_table.iapstack.id
  security_list_ids = [oci_core_security_list.iapstack.id]
}

resource "oci_core_instance" "iapstack" {
  compartment_id      = var.compartment_ocid
  availability_domain = var.availability_domain
  display_name        = "${var.name_prefix}-vm"
  shape               = "VM.Standard.A1.Flex"

  shape_config {
    ocpus         = var.instance_ocpus
    memory_in_gbs = var.instance_memory_gbs
  }

  source_details {
    source_type             = "image"
    source_id               = data.oci_core_images.ubuntu_arm.images[0].id
    boot_volume_size_in_gbs = var.boot_volume_size_gbs
  }

  create_vnic_details {
    subnet_id        = oci_core_subnet.iapstack.id
    assign_public_ip = true
    hostname_label   = "iapstack"
  }

  metadata = merge(
    {
      user_data = base64encode(templatefile("${path.module}/cloud-init.yaml.tftpl", {
        install_script_url      = local.install_script_url
        iapstack_repo           = var.iapstack_repo
        iapstack_ref            = var.iapstack_ref
        cloudflare_tunnel_token = var.cloudflare_tunnel_token
        bootstrap_admin_key     = random_password.bootstrap_admin_key.result
      }))
    },
    var.ssh_public_key != "" ? { ssh_authorized_keys = var.ssh_public_key } : {}
  )

  lifecycle {
    # The newest image changes over time; do not replace a running VM because of it.
    ignore_changes = [source_details[0].source_id, metadata]
  }
}
