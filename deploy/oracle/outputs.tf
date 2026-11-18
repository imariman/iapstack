output "instance_public_ip" {
  description = "Public IP of the VM. Application traffic arrives through the tunnel, not this address."
  value       = oci_core_instance.iapstack.public_ip
}

output "dashboard_url" {
  description = "Dashboard URL once the tunnel hostname is routed to http://iapstack:8080."
  value       = var.public_hostname != "" ? "https://${var.public_hostname}/dashboard/" : "Set a public hostname in the Cloudflare Tunnel, then open https://<hostname>/dashboard/"
}

output "bootstrap_admin_key" {
  description = "One-time bootstrap administrator key. Create a durable administrator, then remove it from the VM's .env."
  value       = random_password.bootstrap_admin_key.result
  sensitive   = true
}

output "ssh_command" {
  description = "SSH command when an SSH key and source CIDR were provided."
  value       = var.ssh_public_key != "" && var.ssh_source_cidr != "" ? "ssh ubuntu@${oci_core_instance.iapstack.public_ip}" : "SSH is closed. Use the OCI Cloud Shell or serial console, or redeploy with an SSH key and source CIDR."
}

output "installation_log" {
  description = "Where to follow the first-boot installation, which takes about 5-10 minutes."
  value       = "sudo tail -f /var/log/cloud-init-output.log"
}
