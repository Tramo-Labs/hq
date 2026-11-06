output "server_ipv4" {
  value = hcloud_server.app.ipv4_address
}

output "server_ipv6" {
  value = hcloud_server.app.ipv6_address
}

output "fqdn" {
  value = cloudflare_record.app_v4.hostname
}

output "backup_bucket" {
  value = cloudflare_r2_bucket.backups.name
}
