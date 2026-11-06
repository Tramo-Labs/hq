# Infra as code

One VPS (Hetzner), one DNS zone (Cloudflare), one bucket for encrypted backups.
Written so a buyer of the software can run `terraform apply` with their own
tokens and get an identical environment. Variables: provider tokens, domain,
region. Nothing Tramo specific inside.
