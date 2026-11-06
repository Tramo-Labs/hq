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
