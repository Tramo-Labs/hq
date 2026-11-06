// Package config loads the process configuration from the environment.
// Every binary reads one Config, validates it at startup and fails fast.
// Nothing in this package reads files: environments are described only by
// environment variables so the same image runs in dev, staging and prod.
//
// Secrets (MASTER_KEY, JWT_SIGNING_KEY) are base64 of 32 random bytes:
//
//	openssl rand -base64 32
//
// Outside dev they are required. In dev a missing one is generated at
// startup and listed in Ephemeral: tokens and encrypted data do not survive a
// restart, which is fine for local work and impossible to ship by accident.
// Config never prints a secret: LogValue and String redact them.
package config

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/netip"
	"os"
	"strconv"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/httpx"
)

// Config is the full configuration shared by the core binaries.
type Config struct {
	Env         string // dev, staging, prod
	HTTPAddr    string // api listen address
	GRPCAddr    string // arca-gateway listen address
	DatabaseURL string // runtime connection (tramo_runtime in production)
	// MigrateDatabaseURL is the privileged connection used only by
	// `api migrate` (ADR 0005). Defaults to DatabaseURL.
	MigrateDatabaseURL string
	// MigrateOnStart applies migrations when the api starts. True by
	// default only in dev; elsewhere migrations are a separate step.
	MigrateOnStart bool
	ArcaGateway    string // host:port of the gateway as seen by core
	ArcaEnv        string // sim, homologacion, produccion
	LogLevel       string
	// TrustedProxies are the reverse proxies (our Caddy) whose
	// X-Forwarded-For is believed. TRUSTED_PROXIES: comma separated IPs or
	// CIDRs. Empty: the TCP peer is always the client.
	TrustedProxies []netip.Prefix
	DocsDir        string // directory with openapi.yaml and the Swagger UI page

	MasterKey []byte // wraps the per tenant data keys
	JWTSeed   []byte // ed25519 seed for access tokens
	JWTKeyID  string // kid header, rotates with the key
	Ephemeral []string
}

// Load reads the environment.
func Load() (Config, error) {
	c := Config{
		Env:         get("APP_ENV", "dev"),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
		GRPCAddr:    get("GRPC_ADDR", ":9090"),
		DatabaseURL: get("DATABASE_URL", ""),
		ArcaGateway: get("ARCA_GATEWAY_ADDR", "localhost:9090"),
		ArcaEnv:     get("ARCA_ENV", "sim"),
		LogLevel:    get("LOG_LEVEL", "info"),
		JWTKeyID:    get("JWT_KEY_ID", "k1"),
		DocsDir:     get("DOCS_DIR", "api"),
	}
	switch c.Env {
	case "dev", "staging", "prod":
	default:
		return c, fmt.Errorf("APP_ENV %q is not one of dev, staging, prod", c.Env)
	}
	if c.Env != "dev" && c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required outside dev")
	}
	if os.Getenv("TRUST_PROXY_HEADERS") != "" {
		// The old switch trusted X-Forwarded-For from any peer. Refuse it
		// loudly rather than silently ignoring a setting someone relies on.
		return c, errors.New("TRUST_PROXY_HEADERS is gone: set TRUSTED_PROXIES to the proxy IPs or CIDRs")
	}
	var err error
	if c.TrustedProxies, err = httpx.ParseTrustedProxies(os.Getenv("TRUSTED_PROXIES")); err != nil {
		return c, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}
	for _, p := range c.TrustedProxies {
		if p.Bits() == 0 {
			return c, errors.New("TRUSTED_PROXIES must not contain 0.0.0.0/0 or ::/0")
		}
	}
	c.MigrateDatabaseURL = get("MIGRATE_DATABASE_URL", c.DatabaseURL)
	defaultMigrate := "false"
	if c.Env == "dev" {
		defaultMigrate = "true"
	}
	c.MigrateOnStart = get("MIGRATE_ON_START", defaultMigrate) == "true"
	switch c.ArcaEnv {
	case "sim", "homologacion", "produccion":
	default:
		return c, fmt.Errorf("ARCA_ENV %q is not one of sim, homologacion, produccion", c.ArcaEnv)
	}
	if c.Env == "prod" && c.ArcaEnv == "sim" {
		return c, errors.New("ARCA_ENV=sim is not allowed in prod")
	}
	if c.MasterKey, err = c.secret("MASTER_KEY"); err != nil {
		return c, err
	}
	if c.JWTSeed, err = c.secret("JWT_SIGNING_KEY"); err != nil {
		return c, err
	}
	return c, nil
}

func (c *Config) secret(name string) ([]byte, error) {
	v := os.Getenv(name)
	if v == "" {
		if c.Env != "dev" {
			return nil, fmt.Errorf("%s is required outside dev", name)
		}
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		c.Ephemeral = append(c.Ephemeral, name)
		return b, nil
	}
	b, err := base64.StdEncoding.DecodeString(v)
	if err != nil || len(b) != 32 {
		// The value itself is never echoed.
		return nil, fmt.Errorf("%s must be base64 of 32 bytes", name)
	}
	return b, nil
}

// LogValue lets slog print the config without secrets.
func (c Config) LogValue() slog.Value {
	return slog.GroupValue(
		slog.String("env", c.Env),
		slog.String("http_addr", c.HTTPAddr),
		slog.String("grpc_addr", c.GRPCAddr),
		slog.Bool("database_url_set", c.DatabaseURL != ""),
		slog.Bool("separate_migrate_url", c.MigrateDatabaseURL != c.DatabaseURL),
		slog.Bool("migrate_on_start", c.MigrateOnStart),
		slog.String("arca_env", c.ArcaEnv),
		slog.Any("trusted_proxies", c.TrustedProxies),
		slog.Any("ephemeral_secrets", c.Ephemeral),
	)
}

// String redacts like LogValue, for fmt.
func (c Config) String() string { return c.LogValue().String() }

// Int reads an integer variable with a default.
func Int(key string, def int) int {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

func get(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
