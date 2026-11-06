// Package config loads the process configuration from the environment.
// Every binary reads one Config, validates it at startup and fails fast.
// Nothing in this package reads files: environments are described only by
// environment variables so the same image runs in dev, staging and prod.
package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
)

// Config is the full configuration shared by the core binaries.
type Config struct {
	Env         string // dev, staging, prod
	HTTPAddr    string // api listen address
	GRPCAddr    string // arca-gateway listen address
	DatabaseURL string
	ArcaGateway string // host:port of the gateway as seen by core
	ArcaEnv     string // sim, homologacion, produccion
	LogLevel    string
}

// Load reads the environment. Missing optional values get dev defaults;
// a missing DATABASE_URL outside dev is an error.
func Load() (Config, error) {
	c := Config{
		Env:         get("APP_ENV", "dev"),
		HTTPAddr:    get("HTTP_ADDR", ":8080"),
		GRPCAddr:    get("GRPC_ADDR", ":9090"),
		DatabaseURL: get("DATABASE_URL", ""),
		ArcaGateway: get("ARCA_GATEWAY_ADDR", "localhost:9090"),
		ArcaEnv:     get("ARCA_ENV", "sim"),
		LogLevel:    get("LOG_LEVEL", "info"),
	}
	if c.Env != "dev" && c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL is required outside dev")
	}
	switch c.ArcaEnv {
	case "sim", "homologacion", "produccion":
	default:
		return c, fmt.Errorf("ARCA_ENV %q is not one of sim, homologacion, produccion", c.ArcaEnv)
	}
	return c, nil
}

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
