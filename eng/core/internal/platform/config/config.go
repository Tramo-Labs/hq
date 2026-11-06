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
	"os"
	"strconv"
)

// Config is the full configuration shared by the core binaries.
type Config struct {
	Env               string // dev, staging, prod
	HTTPAddr          string // api listen address
	GRPCAddr          string // arca-gateway listen address
	DatabaseURL       string
	ArcaGateway       string // host:port of the gateway as seen by core
	ArcaEnv           string // sim, homologacion, produccion
	LogLevel          string
	TrustProxyHeaders bool   // true behind our own Caddy: client IP from X-Forwarded-For
	DocsDir           string // directory with openapi.yaml and the Swagger UI page

	MasterKey []byte // wraps the per tenant data keys
	JWTSeed   []byte // ed25519 seed for access tokens
	JWTKeyID  string // kid header, rotates with the key
	Ephemeral []string
}

// Load reads the environment.
func Load() (Config, error) {
	c := Config{
		Env:               get("APP_ENV", "dev"),
		HTTPAddr:          get("HTTP_ADDR", ":8080"),
		GRPCAddr:          get("GRPC_ADDR", ":9090"),
		DatabaseURL:       get("DATABASE_URL", ""),
		ArcaGateway:       get("ARCA_GATEWAY_ADDR", "localhost:9090"),
		ArcaEnv:           get("ARCA_ENV", "sim"),
		LogLevel:          get("LOG_LEVEL", "info"),
		TrustProxyHeaders: get("TRUST_PROXY_HEADERS", "false") == "true",
		JWTKeyID:          get("JWT_KEY_ID", "k1"),
		DocsDir:           get("DOCS_DIR", "api"),
	}
	switch c.Env {
	case "dev", "staging", "prod":
	default:
		return c, fmt.Errorf("APP_ENV %q is not one of dev, staging, prod", c.Env)
	}
	if c.Env != "dev" && c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required outside dev")
	}
	switch c.ArcaEnv {
	case "sim", "homologacion", "produccion":
	default:
		return c, fmt.Errorf("ARCA_ENV %q is not one of sim, homologacion, produccion", c.ArcaEnv)
	}
	if c.Env == "prod" && c.ArcaEnv == "sim" {
		return c, errors.New("ARCA_ENV=sim is not allowed in prod")
	}
	var err error
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
		slog.String("arca_env", c.ArcaEnv),
		slog.Bool("trust_proxy_headers", c.TrustProxyHeaders),
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
