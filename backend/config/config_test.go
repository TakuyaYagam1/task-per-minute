package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// Unset by tests up front so the host shell can't pollute results.
var configEnvVars = []string{
	"HTTP_HOST", "HTTP_PORT", "HTTP_READ_TIMEOUT", "HTTP_WRITE_TIMEOUT", "HTTP_SHUTDOWN_TIMEOUT",
	"HTTP_ALLOWED_ORIGINS", "HTTP_TRUSTED_PROXY_CIDRS",
	"DB_DSN", "DB_MAX_CONNS",
	"REDIS_ADDR", "REDIS_PASSWORD", "REDIS_DB",
	"SEAWEEDFS_ENDPOINT", "SEAWEEDFS_PUBLIC_ENDPOINT", "SEAWEEDFS_ACCESS_KEY", "SEAWEEDFS_SECRET_KEY",
	"SEAWEEDFS_BUCKET", "SEAWEEDFS_SECURE", "SEAWEEDFS_PUBLIC_SECURE",
	"JWT_SECRET", "JWT_ACCESS_TTL", "JWT_REFRESH_TTL",
	"ADMIN_PASSWORD", "ADMIN_LOGIN_RATE_ATTEMPTS", "ADMIN_LOGIN_RATE_WINDOW", "ADMIN_LOGIN_RATE_BUCKET_TTL",
	"PLAYER_JOIN_RATE_ATTEMPTS", "PLAYER_JOIN_RATE_WINDOW", "PLAYER_JOIN_RATE_BUCKET_TTL",
	"WS_ALLOWED_ORIGINS",
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
	if len(cfg.HTTP.AllowedOrigins) != 0 {
		t.Errorf("HTTP.AllowedOrigins = %v, want empty", cfg.HTTP.AllowedOrigins)
	}
	if len(cfg.HTTP.TrustedProxyCIDRs) != 0 {
		t.Errorf("HTTP.TrustedProxyCIDRs = %v, want empty", cfg.HTTP.TrustedProxyCIDRs)
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
	if cfg.SeaweedFS.PublicEndpoint != "" {
		t.Errorf("SeaweedFS.PublicEndpoint = %q, want empty default", cfg.SeaweedFS.PublicEndpoint)
	}
	if cfg.SeaweedFS.PublicSecure {
		t.Error("SeaweedFS.PublicSecure = true, want false default")
	}
	if cfg.JWT.AccessTTL != 15*time.Minute {
		t.Errorf("JWT.AccessTTL = %s, want 15m", cfg.JWT.AccessTTL)
	}
	if cfg.JWT.RefreshTTL != 168*time.Hour {
		t.Errorf("JWT.RefreshTTL = %s, want 168h", cfg.JWT.RefreshTTL)
	}
	if cfg.Admin.LoginRateAttempts != 5 {
		t.Errorf("Admin.LoginRateAttempts = %d, want 5", cfg.Admin.LoginRateAttempts)
	}
	if cfg.Admin.LoginRateWindow != 15*time.Minute {
		t.Errorf("Admin.LoginRateWindow = %s, want 15m", cfg.Admin.LoginRateWindow)
	}
	if cfg.Admin.LoginRateBucketTTL != time.Hour {
		t.Errorf("Admin.LoginRateBucketTTL = %s, want 1h", cfg.Admin.LoginRateBucketTTL)
	}
	if cfg.Player.JoinRateAttempts != 20 {
		t.Errorf("Player.JoinRateAttempts = %d, want 20", cfg.Player.JoinRateAttempts)
	}
	if cfg.Player.JoinRateWindow != 5*time.Minute {
		t.Errorf("Player.JoinRateWindow = %s, want 5m", cfg.Player.JoinRateWindow)
	}
	if cfg.Player.JoinRateBucketTTL != time.Hour {
		t.Errorf("Player.JoinRateBucketTTL = %s, want 1h", cfg.Player.JoinRateBucketTTL)
	}
	if len(cfg.WS.AllowedOrigins) != 0 {
		t.Errorf("WS.AllowedOrigins = %v, want empty", cfg.WS.AllowedOrigins)
	}
}

func TestLoad_OverridesAreApplied(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("HTTP_PORT", "9000")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "5s")
	t.Setenv("HTTP_ALLOWED_ORIGINS", " https://example.com,https://api.example.com ")
	t.Setenv("HTTP_TRUSTED_PROXY_CIDRS", " 127.0.0.0/8,172.18.0.0/16 ")
	t.Setenv("DB_MAX_CONNS", "50")
	t.Setenv("REDIS_PASSWORD", "redis-pwd")
	t.Setenv("SEAWEEDFS_PUBLIC_ENDPOINT", " files.example.com ")
	t.Setenv("SEAWEEDFS_PUBLIC_SECURE", "true")
	t.Setenv("ADMIN_LOGIN_RATE_ATTEMPTS", "7")
	t.Setenv("ADMIN_LOGIN_RATE_WINDOW", "10m")
	t.Setenv("ADMIN_LOGIN_RATE_BUCKET_TTL", "2h")
	t.Setenv("PLAYER_JOIN_RATE_ATTEMPTS", "11")
	t.Setenv("PLAYER_JOIN_RATE_WINDOW", "30s")
	t.Setenv("PLAYER_JOIN_RATE_BUCKET_TTL", "3h")
	t.Setenv("WS_ALLOWED_ORIGINS", "https://example.com,https://api.example.com")

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
	if got := cfg.HTTP.AllowedOrigins; len(got) != 2 || got[0] != "https://example.com" || got[1] != "https://api.example.com" {
		t.Errorf("HTTP.AllowedOrigins = %v, want configured origins", got)
	}
	if got := cfg.HTTP.TrustedProxyCIDRs; len(got) != 2 || got[0] != "127.0.0.0/8" || got[1] != "172.18.0.0/16" {
		t.Errorf("HTTP.TrustedProxyCIDRs = %v, want configured CIDRs", got)
	}
	if cfg.DB.MaxConns != 50 {
		t.Errorf("DB.MaxConns = %d, want 50", cfg.DB.MaxConns)
	}
	if cfg.Redis.Password != "redis-pwd" {
		t.Errorf("Redis.Password = %q, want redis-pwd", cfg.Redis.Password)
	}
	if cfg.SeaweedFS.PublicEndpoint != "files.example.com" {
		t.Errorf("SeaweedFS.PublicEndpoint = %q, want files.example.com", cfg.SeaweedFS.PublicEndpoint)
	}
	if !cfg.SeaweedFS.PublicSecure {
		t.Error("SeaweedFS.PublicSecure = false, want true")
	}
	if cfg.Admin.LoginRateAttempts != 7 {
		t.Errorf("Admin.LoginRateAttempts = %d, want 7", cfg.Admin.LoginRateAttempts)
	}
	if cfg.Admin.LoginRateWindow != 10*time.Minute {
		t.Errorf("Admin.LoginRateWindow = %s, want 10m", cfg.Admin.LoginRateWindow)
	}
	if cfg.Admin.LoginRateBucketTTL != 2*time.Hour {
		t.Errorf("Admin.LoginRateBucketTTL = %s, want 2h", cfg.Admin.LoginRateBucketTTL)
	}
	if cfg.Player.JoinRateAttempts != 11 {
		t.Errorf("Player.JoinRateAttempts = %d, want 11", cfg.Player.JoinRateAttempts)
	}
	if cfg.Player.JoinRateWindow != 30*time.Second {
		t.Errorf("Player.JoinRateWindow = %s, want 30s", cfg.Player.JoinRateWindow)
	}
	if cfg.Player.JoinRateBucketTTL != 3*time.Hour {
		t.Errorf("Player.JoinRateBucketTTL = %s, want 3h", cfg.Player.JoinRateBucketTTL)
	}
	if got := cfg.WS.AllowedOrigins; len(got) != 2 || got[0] != "https://example.com" || got[1] != "https://api.example.com" {
		t.Errorf("WS.AllowedOrigins = %v, want configured origins", got)
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

func TestLoad_ValidationFailures(t *testing.T) {
	cases := []struct {
		name      string
		key       string
		value     string
		wantError string
	}{
		{"bad port", "HTTP_PORT", "0", "HTTP_PORT"},
		{"zero read timeout", "HTTP_READ_TIMEOUT", "0s", "HTTP_READ_TIMEOUT"},
		{"wildcard http origin", "HTTP_ALLOWED_ORIGINS", "*", "HTTP_ALLOWED_ORIGINS"},
		{"http origin with path", "HTTP_ALLOWED_ORIGINS", "https://example.com/app", "HTTP_ALLOWED_ORIGINS"},
		{"bad trusted proxy cidr", "HTTP_TRUSTED_PROXY_CIDRS", "127.0.0.1", "HTTP_TRUSTED_PROXY_CIDRS"},
		{"wildcard ws origin", "WS_ALLOWED_ORIGINS", "*", "WS_ALLOWED_ORIGINS"},
		{"ws origin with path", "WS_ALLOWED_ORIGINS", "https://example.com/app", "WS_ALLOWED_ORIGINS"},
		{"placeholder db dsn", "DB_DSN", "postgres://u:your-password-here@localhost:5432/db", "DB_DSN"},
		{"bad db pool", "DB_MAX_CONNS", "0", "DB_MAX_CONNS"},
		{"bad redis db", "REDIS_DB", "-1", "REDIS_DB"},
		{"short jwt secret", "JWT_SECRET", "short", "JWT_SECRET"},
		{"placeholder jwt secret", "JWT_SECRET", "change-me-change-me-change-me-change-me", "JWT_SECRET"},
		{"bad access ttl", "JWT_ACCESS_TTL", "0s", "JWT_ACCESS_TTL"},
		{"placeholder admin password", "ADMIN_PASSWORD", "your-password-here", "ADMIN_PASSWORD"},
		{"bad admin rate attempts", "ADMIN_LOGIN_RATE_ATTEMPTS", "0", "ADMIN_LOGIN_RATE_ATTEMPTS"},
		{"bad player rate window", "PLAYER_JOIN_RATE_WINDOW", "-1s", "PLAYER_JOIN_RATE_WINDOW"},
		{"placeholder seaweed secret", "SEAWEEDFS_SECRET_KEY", "your-secret-key", "SEAWEEDFS_SECRET_KEY"},
		{"seaweed public endpoint with scheme", "SEAWEEDFS_PUBLIC_ENDPOINT", "https://files.example.com", "SEAWEEDFS_PUBLIC_ENDPOINT"},
		{"seaweed public endpoint with path", "SEAWEEDFS_PUBLIC_ENDPOINT", "files.example.com/s3", "SEAWEEDFS_PUBLIC_ENDPOINT"},
		{"seaweed public endpoint with userinfo", "SEAWEEDFS_PUBLIC_ENDPOINT", "user@files.example.com", "SEAWEEDFS_PUBLIC_ENDPOINT"},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			setRequiredEnv(t)
			t.Setenv(tt.key, tt.value)

			_, err := Load()
			if err == nil {
				t.Fatalf("expected validation error for %s", tt.key)
			}
			if !strings.Contains(err.Error(), tt.wantError) {
				t.Fatalf("Load error = %q, want it to mention %s", err.Error(), tt.wantError)
			}
		})
	}
}

func TestLoad_HTTPAllowedOrigins(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  []string
	}{
		{
			name:  "empty",
			value: "",
			want:  nil,
		},
		{
			name:  "multiple with trim",
			value: " http://localhost:3000, https://example.com ",
			want:  []string{"http://localhost:3000", "https://example.com"},
		},
		{
			name:  "duplicates removed",
			value: "https://example.com,https://example.com",
			want:  []string{"https://example.com"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clearConfigEnv(t)
			setRequiredEnv(t)
			t.Setenv("HTTP_ALLOWED_ORIGINS", tt.value)

			cfg, err := Load()
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if strings.Join(cfg.HTTP.AllowedOrigins, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("HTTP.AllowedOrigins = %v, want %v", cfg.HTTP.AllowedOrigins, tt.want)
			}
		})
	}
}

func TestLoad_WSAllowedOrigins(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("WS_ALLOWED_ORIGINS", " https://example.com,https://example.com,http://localhost:3000 ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(cfg.WS.AllowedOrigins, ","); got != "https://example.com,http://localhost:3000" {
		t.Fatalf("WS.AllowedOrigins = %v, want normalized allowlist", cfg.WS.AllowedOrigins)
	}
}

func TestLoad_HTTPTrustedProxyCIDRs(t *testing.T) {
	clearConfigEnv(t)
	setRequiredEnv(t)
	t.Setenv("HTTP_TRUSTED_PROXY_CIDRS", " 127.0.0.0/8,127.0.0.0/8,172.18.0.0/16 ")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := strings.Join(cfg.HTTP.TrustedProxyCIDRs, ","); got != "127.0.0.0/8,172.18.0.0/16" {
		t.Fatalf("HTTP.TrustedProxyCIDRs = %v, want normalized CIDRs", cfg.HTTP.TrustedProxyCIDRs)
	}
}

func TestLoadMigration_OnlyRequiresDBDSN(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DB_DSN", "postgres://u:p@localhost:5432/db?sslmode=disable")

	cfg, err := LoadMigration()
	if err != nil {
		t.Fatalf("LoadMigration: %v", err)
	}
	if cfg.DB.DSN != "postgres://u:p@localhost:5432/db?sslmode=disable" {
		t.Errorf("Migration DB.DSN = %q, want configured DSN", cfg.DB.DSN)
	}
}

func TestLoadMigration_FailsWithoutDBDSN(t *testing.T) {
	clearConfigEnv(t)

	if _, err := LoadMigration(); err == nil {
		t.Fatal("expected LoadMigration to fail without DB_DSN")
	}
}

func TestLoadMigration_FailsWithPlaceholderDBDSN(t *testing.T) {
	clearConfigEnv(t)
	t.Setenv("DB_DSN", "postgres://u:your-password-here@localhost:5432/db?sslmode=disable")

	_, err := LoadMigration()
	if err == nil {
		t.Fatal("expected LoadMigration to reject placeholder DB_DSN")
	}
	if !strings.Contains(err.Error(), "DB_DSN") {
		t.Fatalf("LoadMigration error = %q, want it to mention DB_DSN", err.Error())
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
