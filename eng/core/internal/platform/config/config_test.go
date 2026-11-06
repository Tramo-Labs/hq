package config_test

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"

	"github.com/Tramo-Labs/hq/eng/core/internal/platform/config"
)

var key32 = base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))

func TestDevGeneratesEphemeralSecrets(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("MASTER_KEY", "")
	t.Setenv("JWT_SIGNING_KEY", "")
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.MasterKey) != 32 || len(c.JWTSeed) != 32 || len(c.Ephemeral) != 2 {
		t.Fatalf("ephemeral secrets not generated: %v", c.Ephemeral)
	}
}

func TestSecretsRequiredOutsideDev(t *testing.T) {
	t.Setenv("APP_ENV", "staging")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("ARCA_ENV", "homologacion")
	t.Setenv("MASTER_KEY", "")
	t.Setenv("JWT_SIGNING_KEY", key32)
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "MASTER_KEY") {
		t.Fatalf("staging without MASTER_KEY: %v", err)
	}
	t.Setenv("MASTER_KEY", key32)
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Ephemeral) != 0 {
		t.Fatalf("ephemeral outside dev: %v", c.Ephemeral)
	}
}

func TestRejectsBadValues(t *testing.T) {
	cases := map[string]map[string]string{
		"short key":   {"MASTER_KEY": base64.StdEncoding.EncodeToString([]byte("short"))},
		"not base64":  {"JWT_SIGNING_KEY": "%%%"},
		"arca env":    {"ARCA_ENV": "testing"},
		"app env":     {"APP_ENV": "qa"},
		"sim in prod": {"APP_ENV": "prod", "DATABASE_URL": "postgres://x", "MASTER_KEY": key32, "JWT_SIGNING_KEY": key32, "ARCA_ENV": "sim"},
	}
	for name, env := range cases {
		t.Run(name, func(t *testing.T) {
			for _, k := range []string{"APP_ENV", "DATABASE_URL", "MASTER_KEY", "JWT_SIGNING_KEY", "ARCA_ENV"} {
				t.Setenv(k, "")
			}
			for k, v := range env {
				t.Setenv(k, v)
			}
			if _, err := config.Load(); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// Outside dev the api never migrates by itself: migrations are a separate
// step with a privileged URL (ADR 0005).
func TestMigrationDefaults(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("DATABASE_URL", "postgres://runtime@db/x")
	t.Setenv("MIGRATE_DATABASE_URL", "")
	t.Setenv("MIGRATE_ON_START", "")
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !c.MigrateOnStart || c.MigrateDatabaseURL != c.DatabaseURL {
		t.Fatalf("dev: %v %q", c.MigrateOnStart, c.MigrateDatabaseURL)
	}
	t.Setenv("APP_ENV", "staging")
	t.Setenv("ARCA_ENV", "homologacion")
	t.Setenv("MASTER_KEY", key32)
	t.Setenv("JWT_SIGNING_KEY", key32)
	t.Setenv("MIGRATE_DATABASE_URL", "postgres://owner@db/x")
	c, err = config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.MigrateOnStart || c.MigrateDatabaseURL != "postgres://owner@db/x" {
		t.Fatalf("staging: %v %q", c.MigrateOnStart, c.MigrateDatabaseURL)
	}
	if s := c.String(); strings.Contains(s, "owner@") || strings.Contains(s, "runtime@") {
		t.Fatalf("database URLs printed: %s", s)
	}
}

func TestTrustedProxies(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("TRUST_PROXY_HEADERS", "")
	t.Setenv("TRUSTED_PROXIES", "")
	c, err := config.Load()
	if err != nil || len(c.TrustedProxies) != 0 {
		t.Fatalf("default: %v %v", c.TrustedProxies, err)
	}
	t.Setenv("TRUSTED_PROXIES", "172.30.0.10, 10.1.0.0/16")
	c, err = config.Load()
	if err != nil || len(c.TrustedProxies) != 2 || c.TrustedProxies[0].String() != "172.30.0.10/32" {
		t.Fatalf("list: %v %v", c.TrustedProxies, err)
	}
	for _, bad := range []string{"0.0.0.0/0", "::/0", "proxy.local", "10.0.0.1/99"} {
		t.Setenv("TRUSTED_PROXIES", bad)
		if _, err := config.Load(); err == nil {
			t.Errorf("TRUSTED_PROXIES=%q accepted", bad)
		}
	}
	// The old switch trusted any peer; it must not be silently ignored.
	t.Setenv("TRUSTED_PROXIES", "")
	t.Setenv("TRUST_PROXY_HEADERS", "true")
	if _, err := config.Load(); err == nil {
		t.Fatal("TRUST_PROXY_HEADERS still accepted")
	}
}

// D-18: the browser origins allowed to call the api.
func TestCORSAllowedOrigins(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("APP_BASE_URL", "")
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	c, err := config.Load()
	if err != nil || !c.CORSOrigins.Allowed("http://localhost:3000") || len(c.CORSOrigins.List()) != 1 {
		t.Fatalf("dev default is the app base url: %v %v", c.CORSOrigins.List(), err)
	}
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://app.tramo.test,https://console.tramo.test")
	if c, err = config.Load(); err != nil || !c.CORSOrigins.Allowed("https://console.tramo.test") || c.CORSOrigins.Allowed("http://localhost:3000") {
		t.Fatalf("explicit list: %v %v", c.CORSOrigins.List(), err)
	}
	for _, bad := range []string{"*", "https://*.tramo.test", "https://app.tramo.test/"} {
		t.Setenv("CORS_ALLOWED_ORIGINS", bad)
		if _, err := config.Load(); err == nil {
			t.Errorf("CORS_ALLOWED_ORIGINS=%q accepted", bad)
		}
	}

	t.Setenv("APP_ENV", "staging")
	t.Setenv("DATABASE_URL", "postgres://x")
	t.Setenv("MASTER_KEY", key32)
	t.Setenv("JWT_SIGNING_KEY", key32)
	t.Setenv("CORS_ALLOWED_ORIGINS", "")
	t.Setenv("APP_BASE_URL", "https://app.tramo.test")
	if c, err = config.Load(); err != nil || len(c.CORSOrigins.List()) != 0 {
		t.Fatalf("outside dev nothing is allowed unless listed: %v %v", c.CORSOrigins.List(), err)
	}
	t.Setenv("CORS_ALLOWED_ORIGINS", "http://app.tramo.test")
	if _, err := config.Load(); err == nil {
		t.Error("http origin accepted outside dev")
	}
}

func TestSecretsNeverPrinted(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	t.Setenv("MASTER_KEY", key32)
	t.Setenv("JWT_SIGNING_KEY", key32)
	t.Setenv("DATABASE_URL", "postgres://user:hunter2@db/x")
	c, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	for _, s := range []string{fmt.Sprint(c), fmt.Sprintf("%v", c), fmt.Sprintf("%+v", c)} {
		if strings.Contains(s, "hunter2") || strings.Contains(s, "0123456789abcdef") || strings.Contains(s, key32) {
			t.Fatalf("secret printed: %s", s)
		}
	}
}
