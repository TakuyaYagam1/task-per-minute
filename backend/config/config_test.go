package config

import (
	"os"
	"testing"
	"time"
)

// Unset by tests up front so the host shell can't pollute results.
var configEnvVars = []string{
	"HTTP_HOST", "HTTP_PORT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT",
	"DB_DSN", "DB_MAX_CONNS",
	"REDIS_ADDR", "REDIS_PASSWORD", "REDIS_DB",
	"SEAWEEDFS_ENDPOINT", "SEAWEEDFS_ACCESS_KEY", "SEAWEEDFS_SECRET_KEY", "SEAWEEDFS_BUCKET", "SEAWEEDFS_SECURE",
	"JWT_SECRET", "JWT_ACCESS_TTL", "JWT_REFRESH_TTL",
	"ADMIN_PASSWORD",
}

func clearConfigEnv(t *testing.T) {
	t.Helper()
	for _, k := range configEnvVars {
		_ = os.Unsetenv(k)
	}
}

func setRequiredEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DB_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("ADMIN_PASSWORD", "secret-admin-password")
	t.Setenv("SEAWEEDFS_ENDPOINT", "localhost:8333")
	t.Setenv("SEAWEEDFS_ACCESS_KEY", "access-key")
	t.Setenv("SEAWEEDFS_SECRET_KEY", "secret-key")
}

func TestLoad_AppliesDefaults(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Host != "0.0.0.0" {
		t.Errorf("HTTP.Host = %q, want 0.0.0.0", cfg.HTTP.Host)
	}
	if cfg.HTTP.Port != 8080 {
		t.Errorf("HTTP.Port = %d, want 8080", cfg.HTTP.Port)
	}
	if cfg.HTTP.ReadTimeout != 15*time.Second {
		t.Errorf("HTTP.ReadTimeout = %s, want 15s", cfg.HTTP.ReadTimeout)
	}
	if cfg.HTTP.ShutdownTimeout != 30*time.Second {
		t.Errorf("HTTP.ShutdownTimeout = %s, want 30s", cfg.HTTP.ShutdownTimeout)
	}
	if cfg.DB.MaxConns != 20 {
		t.Errorf("DB.MaxConns = %d, want 20", cfg.DB.MaxConns)
	}
	if cfg.Redis.Addr != "localhost:6379" {
		t.Errorf("Redis.Addr = %q, want localhost:6379", cfg.Redis.Addr)
	}
	if cfg.Redis.Password != "" {
		t.Errorf("Redis.Password expected empty by default, got %q", cfg.Redis.Password)
	}
	if cfg.SeaweedFS.Bucket != "task-per-minute" {
		t.Errorf("SeaweedFS.Bucket = %q, want task-per-minute", cfg.SeaweedFS.Bucket)
	}
	if cfg.JWT.AccessTTL != 15*time.Minute {
		t.Errorf("JWT.AccessTTL = %s, want 15m", cfg.JWT.AccessTTL)
	}
	if cfg.JWT.RefreshTTL != 168*time.Hour {
		t.Errorf("JWT.RefreshTTL = %s, want 168h", cfg.JWT.RefreshTTL)
	}
}

func TestLoad_OverridesAreApplied(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("HTTP_PORT", "9000")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "5s")
	t.Setenv("DB_MAX_CONNS", "50")
	t.Setenv("REDIS_PASSWORD", "redis-pwd")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.HTTP.Port != 9000 {
		t.Errorf("HTTP.Port = %d, want 9000", cfg.HTTP.Port)
	}
	if cfg.HTTP.ShutdownTimeout != 5*time.Second {
		t.Errorf("HTTP.ShutdownTimeout = %s, want 5s", cfg.HTTP.ShutdownTimeout)
	}
	if cfg.DB.MaxConns != 50 {
		t.Errorf("DB.MaxConns = %d, want 50", cfg.DB.MaxConns)
	}
	if cfg.Redis.Password != "redis-pwd" {
		t.Errorf("Redis.Password = %q, want redis-pwd", cfg.Redis.Password)
	}
}

func TestLoad_RequiredSecretsAreLoaded(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.DB.DSN == "" {
		t.Error("DB.DSN must be loaded from env")
	}
	if cfg.JWT.Secret == "" {
		t.Error("JWT.Secret must be loaded from env")
	}
	if cfg.Admin.Password == "" {
		t.Error("Admin.Password must be loaded from env")
	}
	if cfg.SeaweedFS.AccessKey == "" || cfg.SeaweedFS.SecretKey == "" {
		t.Error("SeaweedFS keys must be loaded from env")
	}
}

func TestLoad_FailsWhenAnyRequiredMissing(t *testing.T) {
	cases := []string{
		"DB_DSN",
		"JWT_SECRET",
		"ADMIN_PASSWORD",
		"SEAWEEDFS_ENDPOINT",
		"SEAWEEDFS_ACCESS_KEY",
		"SEAWEEDFS_SECRET_KEY",
	}
	for _, missing := range cases {
		t.Run(missing, func(t *testing.T) {
			clearConfigEnv(t)
			setRequiredEnv(t)
			_ = os.Unsetenv(missing)

			if _, err := Load(); err == nil {
				t.Fatalf("expected error when %s is missing", missing)
			}
		})
	}
}

func TestLoad_FailsWhenAllMissing(t *testing.T) {
	clearConfigEnv(t)
	if _, err := Load(); err == nil {
		t.Fatal("expected error when no env vars are set")
	}
}

func TestMustLoad_PanicsWhenRequiredMissing(t *testing.T) {
	clearConfigEnv(t)
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("MustLoad should panic when required env is missing")
		}
	}()
	_ = MustLoad()
}
