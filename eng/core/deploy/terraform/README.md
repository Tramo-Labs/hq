# Infra as code

One VPS (Hetzner), one DNS zone (Cloudflare), one bucket for encrypted backups
(Cloudflare R2). Written so a buyer of the software can run `terraform apply`
with their own tokens and get an identical environment. Nothing Tramo
specific inside. Decisions in ADR 0005.

| File | What |
|------|------|
| `main.tf` | providers, ssh key, firewall (ssh only from `admin_cidrs`, 80 and 443 open), server (Debian 12), A and AAAA records (not proxied: Caddy does TLS), R2 bucket |
| `variables.tf` | inputs with validation (no `0.0.0.0/0` for ssh) |
| `cloud-init.yaml` | first boot: Docker and compose, `deploy` user with the key, unattended upgrades, no password ssh |
| `example.tfvars` | placeholders; copy to `<env>.tfvars`, which git ignores |
| `.terraform.lock.hcl` | provider versions and hashes, committed |

Tokens are never variables: `export HCLOUD_TOKEN=... CLOUDFLARE_API_TOKEN=...`.

CI runs `terraform fmt -check`, `terraform init -backend=false` and
`terraform validate` only. Nobody runs plan or apply from CI.

```
terraform init
terraform plan -var-file=staging.tfvars
terraform apply -var-file=staging.tfvars
```

## What needs Martín (accounts, credentials or a decision)

1. **Hetzner Cloud** project and an API token (`HCLOUD_TOKEN`), read and write.
2. **Cloudflare**: the domain's zone id, the account id, and an API token
   with DNS edit on the zone and R2 edit on the account
   (`CLOUDFLARE_API_TOKEN`). R2 has to be enabled on the account.
3. **Remote state**: choose where the Terraform state lives (for example an
   R2 bucket with the S3 backend, or Terraform Cloud) before the first real
   apply; local state is fine only for a first try.
4. **The deploy key**: an ed25519 key pair for the `deploy` user; the public
   half goes in the tfvars, the private half in a password manager and, later,
   as a GitHub Actions secret for the deploy job.
5. **Operator IPs** for `admin_cidrs`.
6. **GHCR**: the image job publishes with the workflow's own token; the VPS
   needs a read token (a fine grained PAT with `read:packages`) to pull, or
   make the package public.
7. **Secrets of the stack**, generated on the server and kept in the three
   env files (`deploy/env-examples/`): Postgres passwords, `MASTER_KEY`,
   `JWT_SIGNING_KEY`.
8. **ARCA**: the homologación certificate from WSASS (clave fiscal of the
   CUIT that will be Tramo's), then the production one. Goes only in
   `gateway.env`.

## Not built yet

The deploy job (`docker compose pull && up -d` on the VPS from Actions),
backups to the bucket with a monthly restore test, mTLS between core and the
gateway, monitoring and alerts.
