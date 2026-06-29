package config

import (
	"os"
	"testing"
	"time"
)

func TestDefaults(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with empty file: %v", err)
	}

	if cfg.Server.Port != 8080 {
		t.Errorf("default server.port: got %d, want 8080", cfg.Server.Port)
	}
	if cfg.Database.Host != "localhost" {
		t.Errorf("default database.host: got %q, want %q", cfg.Database.Host, "localhost")
	}
	if cfg.RBAC.DefaultAction != "deny" {
		t.Errorf("default rbac.default_action: got %q, want %q", cfg.RBAC.DefaultAction, "deny")
	}
	if cfg.Auth.SessionTTL != time.Hour {
		t.Errorf("default auth.session_ttl: got %v, want %v", cfg.Auth.SessionTTL, time.Hour)
	}
	if cfg.Auth.SessionRevalidateInterval != 30*time.Second {
		t.Errorf("default auth.session_revalidate_interval: got %v, want 30s", cfg.Auth.SessionRevalidateInterval)
	}
}

func TestEnvOverride(t *testing.T) {
	t.Setenv("MCPGATE_SERVER.PORT", "9999")
	t.Setenv("MCPGATE_DATABASE.HOST", "pg.internal")

	cfg, err := Load("")
	if err != nil {
		t.Fatalf("Load with env override: %v", err)
	}

	if cfg.Server.Port != 9999 {
		t.Errorf("env server.port: got %d, want 9999", cfg.Server.Port)
	}
	if cfg.Database.Host != "pg.internal" {
		t.Errorf("env database.host: got %q, want %q", cfg.Database.Host, "pg.internal")
	}
}

func TestValidationInvalidPort(t *testing.T) {
	t.Setenv("MCPGATE_SERVER.PORT", "99999")
	_, err := Load("")
	if err == nil {
		t.Fatal("expected validation error for port 99999, got nil")
	}
}

func TestValidationInvalidRBACAction(t *testing.T) {
	t.Setenv("MCPGATE_RBAC.DEFAULT_ACTION", "maybe")
	_, err := Load("")
	if err == nil {
		t.Fatal("expected validation error for rbac.default_action='maybe', got nil")
	}
}

func TestLoadFile(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "mcpgate-*.yaml")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close() //nolint:errcheck

	_, err = f.WriteString(`
server:
  port: 7777
database:
  host: testhost
  name: testdb
  user: testuser
rbac:
  default_action: allow
`)
	if err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(f.Name())
	if err != nil {
		t.Fatalf("Load from file: %v", err)
	}

	if cfg.Server.Port != 7777 {
		t.Errorf("file server.port: got %d, want 7777", cfg.Server.Port)
	}
	if cfg.Database.Host != "testhost" {
		t.Errorf("file database.host: got %q, want %q", cfg.Database.Host, "testhost")
	}
}
