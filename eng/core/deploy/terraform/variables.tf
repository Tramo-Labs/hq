variable "name" {
  description = "Prefix for every resource, e.g. tramo-prod."
  type        = string
  validation {
    condition     = can(regex("^[a-z0-9][a-z0-9-]{2,30}$", var.name))
    error_message = "Lowercase letters, digits and dashes, 3 to 31 characters."
  }
}

variable "environment" {
  description = "staging or prod."
  type        = string
  validation {
    condition     = contains(["staging", "prod"], var.environment)
    error_message = "environment must be staging or prod."
  }
}

variable "server_type" {
  description = "Hetzner server type."
  type        = string
  default     = "cx22"
}

variable "location" {
  description = "Hetzner location. Data residency: Europe (eng/ARQUITECTURA.md section 6)."
  type        = string
  default     = "fsn1"
}

variable "ssh_public_key" {
  description = "Public key of the deploy user (public, not a secret)."
  type        = string
}

variable "admin_cidrs" {
  description = "CIDRs allowed to reach ssh. Never 0.0.0.0/0."
  type        = list(string)
  validation {
    condition     = length(var.admin_cidrs) > 0 && !contains(var.admin_cidrs, "0.0.0.0/0") && !contains(var.admin_cidrs, "::/0")
    error_message = "ssh must be restricted to operator CIDRs."
  }
}

variable "cloudflare_zone_id" {
  description = "Cloudflare zone of the domain."
  type        = string
}

variable "cloudflare_account_id" {
  description = "Cloudflare account that owns the R2 bucket."
  type        = string
}

variable "hostname" {
  description = "Record name inside the zone, e.g. app or api.staging."
  type        = string
}

variable "backup_location" {
  description = "R2 location hint for the backup bucket."
  type        = string
  default     = "WEUR"
}
