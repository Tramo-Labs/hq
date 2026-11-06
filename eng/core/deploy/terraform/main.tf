# One environment of Tramo core: a VPS (Hetzner Cloud) running the compose
# stack behind Caddy, its DNS records (Cloudflare) and a bucket for encrypted
# backups (Cloudflare R2). Nothing Tramo specific: a buyer of the software
# runs it with their own tokens and domain.
#
# State: local by default. Choose a remote backend (and its credentials)
# before the first real apply; see README.md.

terraform {
  required_version = ">= 1.6"
  required_providers {
    hcloud = {
      source  = "hetznercloud/hcloud"
      version = "~> 1.48"
    }
    cloudflare = {
      source  = "cloudflare/cloudflare"
      version = "~> 4.40"
    }
  }
}

# Tokens come only from the environment (HCLOUD_TOKEN, CLOUDFLARE_API_TOKEN),
# never from variables files committed to the repo.
provider "hcloud" {}
provider "cloudflare" {}

resource "hcloud_ssh_key" "deploy" {
  name       = "${var.name}-deploy"
  public_key = var.ssh_public_key
}

resource "hcloud_firewall" "app" {
  name = "${var.name}-app"

  rule {
    description = "ssh from operators only"
    direction   = "in"
    protocol    = "tcp"
    port        = "22"
    source_ips  = var.admin_cidrs
  }
  rule {
    description = "http (ACME and redirect to https)"
    direction   = "in"
    protocol    = "tcp"
    port        = "80"
    source_ips  = ["0.0.0.0/0", "::/0"]
  }
  rule {
    description = "https"
    direction   = "in"
    protocol    = "tcp"
    port        = "443"
    source_ips  = ["0.0.0.0/0", "::/0"]
  }
}

resource "hcloud_server" "app" {
  name         = var.name
  server_type  = var.server_type
  location     = var.location
  image        = "debian-12"
  ssh_keys     = [hcloud_ssh_key.deploy.id]
  firewall_ids = [hcloud_firewall.app.id]
  user_data    = templatefile("${path.module}/cloud-init.yaml", { ssh_public_key = var.ssh_public_key })
  labels = {
    app = "tramo-core"
    env = var.environment
  }
}

resource "cloudflare_record" "app_v4" {
  zone_id = var.cloudflare_zone_id
  name    = var.hostname
  type    = "A"
  content = hcloud_server.app.ipv4_address
  ttl     = 300
  proxied = false # Caddy terminates TLS and needs the ACME challenge
}

resource "cloudflare_record" "app_v6" {
  zone_id = var.cloudflare_zone_id
  name    = var.hostname
  type    = "AAAA"
  content = hcloud_server.app.ipv6_address
  ttl     = 300
  proxied = false
}

# Encrypted backups (pg_dump encrypted before upload; the bucket never sees
# plaintext). Lifecycle and retention are set when the backup job lands.
resource "cloudflare_r2_bucket" "backups" {
  account_id = var.cloudflare_account_id
  name       = "${var.name}-backups"
  location   = var.backup_location
}
