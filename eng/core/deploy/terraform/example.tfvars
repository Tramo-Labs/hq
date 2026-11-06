# Copy to <env>.tfvars (ignored by git) and fill in. Placeholders only.
# Tokens are NOT variables: export HCLOUD_TOKEN and CLOUDFLARE_API_TOKEN.
name                  = "tramo-staging"
environment           = "staging"
server_type           = "cx22"
location              = "fsn1"
ssh_public_key        = "ssh-ed25519 AAAA...replace-with-the-deploy-public-key"
admin_cidrs           = ["203.0.113.10/32"] # documentation range: replace with the operators' IPs
cloudflare_zone_id    = "replace-with-zone-id"
cloudflare_account_id = "replace-with-account-id"
hostname              = "api.staging"
backup_location       = "WEUR"
