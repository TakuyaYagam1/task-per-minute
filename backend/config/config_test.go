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
	"INCIDENT_EXPORT_HMAC_KEY_ID", "INCIDENT_EXPORT_HMAC_SECRET",
	"ADMIN_PASSWORD", "ADMIN_LOGIN_RATE_ATTEMPTS", "ADMIN_LOGIN_RATE_WINDOW",
	"ADMIN_REFRESH_RATE_ATTEMPTS", "ADMIN_REFRESH_RATE_WINDOW",
	"PLAYER_JOIN_RATE_ATTEMPTS", "PLAYER_JOIN_RATE_WINDOW", "PLAYER_SESSION_TTL",
	"LEADERBOARD_RATE_ATTEMPTS", "LEADERBOARD_RATE_WINDOW",
	"TOURNAMENT_PUBLIC_READ_RATE_ATTEMPTS", "TOURNAMENT_PUBLIC_READ_RATE_WINDOW",
	"TOURNAMENT_OPERATOR_READ_RATE_ATTEMPTS", "TOURNAMENT_OPERATOR_READ_RATE_WINDOW",
	"TOURNAMENT_OPERATOR_MUTATION_RATE_ATTEMPTS", "TOURNAMENT_OPERATOR_MUTATION_RATE_WINDOW",
	"TOURNAMENT_PARTICIPANT_READ_RATE_ATTEMPTS", "TOURNAMENT_PARTICIPANT_READ_RATE_WINDOW",
	"TOURNAMENT_PARTICIPANT_MUTATION_RATE_ATTEMPTS", "TOURNAMENT_PARTICIPANT_MUTATION_RATE_WINDOW",
	"WS_ALLOWED_ORIGINS", "WS_REQUIRE_ORIGIN", "WS_HANDSHAKE_RATE_ATTEMPTS", "WS_HANDSHAKE_RATE_WINDOW",
	"WS_DELIVERY_RECEIPT_RETENTION", "WS_DELIVERY_RECEIPT_CLEANUP_INTERVAL", "WS_DELIVERY_RECEIPT_CLEANUP_BATCH_SIZE",
	"WS_MAX_CONNECTIONS", "WS_MAX_CONNECTIONS_PER_PRINCIPAL",
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
	t.Setenv("INCIDENT_EXPORT_HMAC_KEY_ID", "incident-2026-09")
	t.Setenv("INCIDENT_EXPORT_HMAC_SECRET", "incident-export-hmac-secret-0123456789")
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
	if cfg.Incident.HMACKeyID != "incident-2026-09" {
		t.Errorf("Incident.HMACKeyID = %q, want incident-2026-09", cfg.Incident.HMACKeyID)
	}
	if cfg.Admin.LoginRateAttempts != 5 {
		t.Errorf("Admin.LoginRateAttempts = %d, want 5", cfg.Admin.LoginRateAttempts)
	}
	if cfg.Admin.LoginRateWindow != 15*time.Minute {
		t.Errorf("Admin.LoginRateWindow = %s, want 15m", cfg.Admin.LoginRateWindow)
	}
	if cfg.Admin.RefreshRateAttempts != cfg.Admin.LoginRateAttempts {
		t.Errorf("Admin.RefreshRateAttempts = %d, want login fallback %d", cfg.Admin.RefreshRateAttempts, cfg.Admin.LoginRateAttempts)
	}
	if cfg.Admin.RefreshRateWindow != cfg.Admin.LoginRateWindow {
		t.Errorf("Admin.RefreshRateWindow = %s, want login fallback %s", cfg.Admin.RefreshRateWindow, cfg.Admin.LoginRateWindow)
	}
	if cfg.Player.JoinRateAttempts != 20 {
		t.Errorf("Player.JoinRateAttempts = %d, want 20", cfg.Player.JoinRateAttempts)
	}
	if cfg.Player.JoinRateWindow != 5*time.Minute {
		t.Errorf("Player.JoinRateWindow = %s, want 5m", cfg.Player.JoinRateWindow)
	}
	if cfg.Player.SessionTTL != 24*time.Hour {
		t.Errorf("Player.SessionTTL = %s, want 24h", cfg.Player.SessionTTL)
	}
	if cfg.Leaderboard.RateAttempts != 120 {
		t.Errorf("Leaderboard.RateAttempts = %d, want 120", cfg.Leaderboard.RateAttempts)
	}
	if cfg.Leaderboard.RateWindow != time.Minute {
		t.Errorf("Leaderboard.RateWindow = %s, want 1m", cfg.Leaderboard.RateWindow)
	}
	assertTournamentRatePolicyDefaults(t, cfg)
	if len(cfg.WS.AllowedOrigins) != 0 {
		t.Errorf("WS.AllowedOrigins = %v, want empty", cfg.WS.AllowedOrigins)
	}
	if cfg.WS.RequireOrigin {
		t.Error("WS.RequireOrigin = true, want false default")
	}
	if cfg.WS.HandshakeRateAttempts != 60 {
		t.Errorf("WS.HandshakeRateAttempts = %d, want 60", cfg.WS.HandshakeRateAttempts)
	}
	if cfg.WS.HandshakeRateWindow != time.Minute {
		t.Errorf("WS.HandshakeRateWindow = %s, want 1m", cfg.WS.HandshakeRateWindow)
	}
	if cfg.WS.MaxConnections != 512 {
		t.Errorf("WS.MaxConnections = %d, want 512", cfg.WS.MaxConnections)
	}
	if cfg.WS.MaxConnectionsPerPrincipal != 4 {
		t.Errorf("WS.MaxConnectionsPerPrincipal = %d, want 4", cfg.WS.MaxConnectionsPerPrincipal)
	}
	if cfg.WS.DeliveryReceiptRetention != 30*24*time.Hour {
		t.Errorf("WS.DeliveryReceiptRetention = %s, want 720h", cfg.WS.DeliveryReceiptRetention)
	}
	if cfg.WS.DeliveryReceiptCleanupInterval != 5*time.Minute {
		t.Errorf("WS.DeliveryReceiptCleanupInterval = %s, want 5m", cfg.WS.DeliveryReceiptCleanupInterval)
	}
	if cfg.WS.DeliveryReceiptCleanupBatchSize != 128 {
		t.Errorf("WS.DeliveryReceiptCleanupBatchSize = %d, want 128", cfg.WS.DeliveryReceiptCleanupBatchSize)
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
	t.Setenv("INCIDENT_EXPORT_HMAC_KEY_ID", " incident-2026-10 ")
	t.Setenv("INCIDENT_EXPORT_HMAC_SECRET", "incident-export-hmac-secret-abcdefghij")
	t.Setenv("ADMIN_LOGIN_RATE_ATTEMPTS", "7")
	t.Setenv("ADMIN_LOGIN_RATE_WINDOW", "10m")
	t.Setenv("ADMIN_REFRESH_RATE_ATTEMPTS", "13")
	t.Setenv("ADMIN_REFRESH_RATE_WINDOW", "20m")
	t.Setenv("PLAYER_JOIN_RATE_ATTEMPTS", "11")
	t.Setenv("PLAYER_JOIN_RATE_WINDOW", "30s")
	t.Setenv("PLAYER_SESSION_TTL", "12h")
	t.Setenv("LEADERBOARD_RATE_ATTEMPTS", "31")
	t.Setenv("LEADERBOARD_RATE_WINDOW", "25s")
	t.Setenv("TOURNAMENT_PUBLIC_READ_RATE_ATTEMPTS", "41")
	t.Setenv("TOURNAMENT_PUBLIC_READ_RATE_WINDOW", "20s")
	t.Setenv("TOURNAMENT_OPERATOR_READ_RATE_ATTEMPTS", "42")
	t.Setenv("TOURNAMENT_OPERATOR_READ_RATE_WINDOW", "21s")
	t.Setenv("TOURNAMENT_OPERATOR_MUTATION_RATE_ATTEMPTS", "43")
	t.Setenv("TOURNAMENT_OPERATOR_MUTATION_RATE_WINDOW", "22s")
	t.Setenv("TOURNAMENT_PARTICIPANT_READ_RATE_ATTEMPTS", "44")
	t.Setenv("TOURNAMENT_PARTICIPANT_READ_RATE_WINDOW", "23s")
	t.Setenv("TOURNAMENT_PARTICIPANT_MUTATION_RATE_ATTEMPTS", "45")
	t.Setenv("TOURNAMENT_PARTICIPANT_MUTATION_RATE_WINDOW", "24s")
	t.Setenv("WS_ALLOWED_ORIGINS", "https://example.com,https://api.example.com")
	t.Setenv("WS_REQUIRE_ORIGIN", "true")
	t.Setenv("WS_HANDSHAKE_RATE_ATTEMPTS", "17")
	t.Setenv("WS_HANDSHAKE_RATE_WINDOW", "45s")
	t.Setenv("WS_MAX_CONNECTIONS", "321")
	t.Setenv("WS_MAX_CONNECTIONS_PER_PRINCIPAL", "3")
	t.Setenv("WS_DELIVERY_RECEIPT_RETENTION", "168h")
	t.Setenv("WS_DELIVERY_RECEIPT_CLEANUP_INTERVAL", "30m")
	t.Setenv("WS_DELIVERY_RECEIPT_CLEANUP_BATCH_SIZE", "64")

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
	if cfg.Incident.HMACKeyID != "incident-2026-10" {
		t.Errorf("Incident.HMACKeyID = %q, want incident-2026-10", cfg.Incident.HMACKeyID)
	}
	if cfg.Admin.LoginRateAttempts != 7 {
		t.Errorf("Admin.LoginRateAttempts = %d, want 7", cfg.Admin.LoginRateAttempts)
	}
	if cfg.Admin.LoginRateWindow != 10*time.Minute {
		t.Errorf("Admin.LoginRateWindow = %s, want 10m", cfg.Admin.LoginRateWindow)
	}
	if cfg.Admin.RefreshRateAttempts != 13 {
		t.Errorf("Admin.RefreshRateAttempts = %d, want 13", cfg.Admin.RefreshRateAttempts)
	}
	if cfg.Admin.RefreshRateWindow != 20*time.Minute {
		t.Errorf("Admin.RefreshRateWindow = %s, want 20m", cfg.Admin.RefreshRateWindow)
	}
	if cfg.Player.JoinRateAttempts != 11 {
		t.Errorf("Player.JoinRateAttempts = %d, want 11", cfg.Player.JoinRateAttempts)
	}
	if cfg.Player.JoinRateWindow != 30*time.Second {
		t.Errorf("Player.JoinRateWindow = %s, want 30s", cfg.Player.JoinRateWindow)
	}
	if cfg.Player.SessionTTL != 12*time.Hour {
		t.Errorf("Player.SessionTTL = %s, want 12h", cfg.Player.SessionTTL)
	}
	if cfg.Leaderboard.RateAttempts != 31 {
		t.Errorf("Leaderboard.RateAttempts = %d, want 31", cfg.Leaderboard.RateAttempts)
	}
	if cfg.Leaderboard.RateWindow != 25*time.Second {
		t.Errorf("Leaderboard.RateWindow = %s, want 25s", cfg.Leaderboard.RateWindow)
	}
	assertTournamentRatePolicyOverrides(t, cfg)
	if got := cfg.WS.AllowedOrigins; len(got) != 2 || got[0] != "https://example.com" || got[1] != "https://api.example.com" {
		t.Errorf("WS.AllowedOrigins = %v, want configured origins", got)
	}
	if !cfg.WS.RequireOrigin {
		t.Error("WS.RequireOrigin = false, want true")
	}
	if cfg.WS.HandshakeRateAttempts != 17 {
		t.Errorf("WS.HandshakeRateAttempts = %d, want 17", cfg.WS.HandshakeRateAttempts)
	}
	if cfg.WS.HandshakeRateWindow != 45*time.Second {
		t.Errorf("WS.HandshakeRateWindow = %s, want 45s", cfg.WS.HandshakeRateWindow)
	}
	if cfg.WS.MaxConnections != 321 {
		t.Errorf("WS.MaxConnections = %d, want 321", cfg.WS.MaxConnections)
	}
	if cfg.WS.MaxConnectionsPerPrincipal != 3 {
		t.Errorf("WS.MaxConnectionsPerPrincipal = %d, want 3", cfg.WS.MaxConnectionsPerPrincipal)
	}
	if cfg.WS.DeliveryReceiptRetention != 168*time.Hour {
		t.Errorf("WS.DeliveryReceiptRetention = %s, want 168h", cfg.WS.DeliveryReceiptRetention)
	}
	if cfg.WS.DeliveryReceiptCleanupInterval != 30*time.Minute {
		t.Errorf("WS.DeliveryReceiptCleanupInterval = %s, want 30m", cfg.WS.DeliveryReceiptCleanupInterval)
	}
	if cfg.WS.DeliveryReceiptCleanupBatchSize != 64 {
		t.Errorf("WS.DeliveryReceiptCleanupBatchSize = %d, want 64", cfg.WS.DeliveryReceiptCleanupBatchSize)
	}
}

func assertTournamentRatePolicyDefaults(t *testing.T, cfg *Config) {
	t.Helper()
	policies := []struct {
		name     string
		attempts int
		window   time.Duration
	}{
		{"public read", cfg.Tournament.PublicReadRateAttempts, cfg.Tournament.PublicReadRateWindow},
		{"operator read", cfg.Tournament.OperatorReadRateAttempts, cfg.Tournament.OperatorReadRateWindow},
		{"operator mutation", cfg.Tournament.OperatorMutationRateAttempts, cfg.Tournament.OperatorMutationRateWindow},
		{"participant read", cfg.Tournament.ParticipantReadRateAttempts, cfg.Tournament.ParticipantReadRateWindow},
		{"participant mutation", cfg.Tournament.ParticipantMutationRateAttempts, cfg.Tournament.ParticipantMutationRateWindow},
	}
	for _, policy := range policies {
		if policy.attempts != 120 {
			t.Errorf("Tournament %s attempts = %d, want 120", policy.name, policy.attempts)
		}
		if policy.window != time.Minute {
			t.Errorf("Tournament %s window = %s, want 1m", policy.name, policy.window)
		}
	}
}

func assertTournamentRatePolicyOverrides(t *testing.T, cfg *Config) {
	t.Helper()
	policies := []struct {
		name         string
		attempts     int
		wantAttempts int
		window       time.Duration
		wantWindow   time.Duration
	}{
		{"public read", cfg.Tournament.PublicReadRateAttempts, 41, cfg.Tournament.PublicReadRateWindow, 20 * time.Second},
		{"operator read", cfg.Tournament.OperatorReadRateAttempts, 42, cfg.Tournament.OperatorReadRateWindow, 21 * time.Second},
		{"operator mutation", cfg.Tournament.OperatorMutationRateAttempts, 43, cfg.Tournament.OperatorMutationRateWindow, 22 * time.Second},
		{"participant read", cfg.Tournament.ParticipantReadRateAttempts, 44, cfg.Tournament.ParticipantReadRateWindow, 23 * time.Second},
		{"participant mutation", cfg.Tournament.ParticipantMutationRateAttempts, 45, cfg.Tournament.ParticipantMutationRateWindow, 24 * time.Second},
	}
	for _, policy := range policies {
		if policy.attempts != policy.wantAttempts {
			t.Errorf("Tournament %s attempts = %d, want %d", policy.name, policy.attempts, policy.wantAttempts)
		}
		if policy.window != policy.wantWindow {
			t.Errorf("Tournament %s window = %s, want %s", policy.name, policy.window, policy.wantWindow)
		}
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
	if cfg.Incident.HMACKeyID == "" || cfg.Incident.HMACSecret == "" {
		t.Error("Incident HMAC material must be loaded from env")
	}
	if cfg.Admin.Password == "" {
		t.Error("Admin.Password must be loaded from env")
	}
	if cfg.SeaweedFS.AccessKey == "" || cfg.SeaweedFS.SecretKey == "" {
		t.Error("SeaweedFS keys must be loaded from env")
	}
}

func TestLoad_RequiresDedicatedIncidentExportHMACMaterial(t *testing.T) {
	for _, missing := range []string{
		"INCIDENT_EXPORT_HMAC_KEY_ID",
		"INCIDENT_EXPORT_HMAC_SECRET",
	} {
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

func TestLoad_RejectsInvalidIncidentExportHMACMaterial(t *testing.T) {
	tests := []struct {
		name   string
		keyID  string
		secret string
	}{
		{name: "malformed key id", keyID: "incident key", secret: "incident-export-hmac-secret-0123456789"},
		{name: "short secret", keyID: "incident-2026-09", secret: "short"},
		{name: "jwt secret reuse", keyID: "incident-2026-09", secret: "0123456789abcdef0123456789abcdef"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			clearConfigEnv(t)
			setRequiredEnv(t)
			t.Setenv("INCIDENT_EXPORT_HMAC_KEY_ID", test.keyID)
			t.Setenv("INCIDENT_EXPORT_HMAC_SECRET", test.secret)

			_, err := Load()
			if err == nil {
				t.Fatal("Load() error = nil, want fail-closed incident HMAC validation")
			}
		})
	}
}

func TestLoad_FailsWhenAnyRequiredMissing(t *testing.T) {
	cases := []string{
		"DB_DSN",
		"JWT_SECRET",
		"INCIDENT_EXPORT_HMAC_KEY_ID",
		"INCIDENT_EXPORT_HMAC_SECRET",
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
		{"wildcard http subdomain", "HTTP_ALLOWED_ORIGINS", "https://*.example.com", "HTTP_ALLOWED_ORIGINS"},
		{"http origin with path", "HTTP_ALLOWED_ORIGINS", "https://example.com/app", "HTTP_ALLOWED_ORIGINS"},
		{"bad trusted proxy cidr", "HTTP_TRUSTED_PROXY_CIDRS", "127.0.0.1", "HTTP_TRUSTED_PROXY_CIDRS"},
		{"wildcard ws origin", "WS_ALLOWED_ORIGINS", "*", "WS_ALLOWED_ORIGINS"},
		{"wildcard ws subdomain", "WS_ALLOWED_ORIGINS", "https://*.example.com", "WS_ALLOWED_ORIGINS"},
		{"ws origin with path", "WS_ALLOWED_ORIGINS", "https://example.com/app", "WS_ALLOWED_ORIGINS"},
		{"placeholder db dsn", "DB_DSN", "postgres://u:your-password-here@localhost:5432/db", "DB_DSN"},
		{"bad db pool", "DB_MAX_CONNS", "0", "DB_MAX_CONNS"},
		{"bad redis db", "REDIS_DB", "-1", "REDIS_DB"},
		{"short jwt secret", "JWT_SECRET", "short", "JWT_SECRET"},
		{"placeholder jwt secret", "JWT_SECRET", "change-me-change-me-change-me-change-me", "JWT_SECRET"},
		{"bad access ttl", "JWT_ACCESS_TTL", "0s", "JWT_ACCESS_TTL"},
		{"placeholder admin password", "ADMIN_PASSWORD", "your-password-here", "ADMIN_PASSWORD"},
		{"bad admin rate attempts", "ADMIN_LOGIN_RATE_ATTEMPTS", "0", "ADMIN_LOGIN_RATE_ATTEMPTS"},
		{"bad admin refresh rate attempts", "ADMIN_REFRESH_RATE_ATTEMPTS", "-1", "ADMIN_REFRESH_RATE_ATTEMPTS"},
		{"bad admin refresh rate window", "ADMIN_REFRESH_RATE_WINDOW", "-1s", "ADMIN_REFRESH_RATE_WINDOW"},
		{"bad player rate window", "PLAYER_JOIN_RATE_WINDOW", "-1s", "PLAYER_JOIN_RATE_WINDOW"},
		{"bad player session ttl", "PLAYER_SESSION_TTL", "0s", "PLAYER_SESSION_TTL"},
		{"bad leaderboard rate attempts", "LEADERBOARD_RATE_ATTEMPTS", "0", "LEADERBOARD_RATE_ATTEMPTS"},
		{"bad leaderboard rate window", "LEADERBOARD_RATE_WINDOW", "0s", "LEADERBOARD_RATE_WINDOW"},
		{"bad tournament public read attempts", "TOURNAMENT_PUBLIC_READ_RATE_ATTEMPTS", "0", "TOURNAMENT_PUBLIC_READ_RATE_ATTEMPTS"},
		{"bad tournament operator read window", "TOURNAMENT_OPERATOR_READ_RATE_WINDOW", "0s", "TOURNAMENT_OPERATOR_READ_RATE_WINDOW"},
		{"bad tournament operator mutation attempts", "TOURNAMENT_OPERATOR_MUTATION_RATE_ATTEMPTS", "0", "TOURNAMENT_OPERATOR_MUTATION_RATE_ATTEMPTS"},
		{"bad tournament participant read window", "TOURNAMENT_PARTICIPANT_READ_RATE_WINDOW", "0s", "TOURNAMENT_PARTICIPANT_READ_RATE_WINDOW"},
		{"bad tournament participant mutation window below redis precision", "TOURNAMENT_PARTICIPANT_MUTATION_RATE_WINDOW", "1ns", "TOURNAMENT_PARTICIPANT_MUTATION_RATE_WINDOW"},
		{"bad ws handshake rate attempts", "WS_HANDSHAKE_RATE_ATTEMPTS", "0", "WS_HANDSHAKE_RATE_ATTEMPTS"},
		{"bad ws handshake rate window", "WS_HANDSHAKE_RATE_WINDOW", "0s", "WS_HANDSHAKE_RATE_WINDOW"},
		{"bad ws max connections", "WS_MAX_CONNECTIONS", "0", "WS_MAX_CONNECTIONS"},
		{"bad ws per-principal connections", "WS_MAX_CONNECTIONS_PER_PRINCIPAL", "0", "WS_MAX_CONNECTIONS_PER_PRINCIPAL"},
		{"bad delivery receipt retention", "WS_DELIVERY_RECEIPT_RETENTION", "0s", "WS_DELIVERY_RECEIPT_RETENTION"},
		{"bad delivery receipt cleanup interval", "WS_DELIVERY_RECEIPT_CLEANUP_INTERVAL", "0s", "WS_DELIVERY_RECEIPT_CLEANUP_INTERVAL"},
		{"bad delivery receipt cleanup batch", "WS_DELIVERY_RECEIPT_CLEANUP_BATCH_SIZE", "0", "WS_DELIVERY_RECEIPT_CLEANUP_BATCH_SIZE"},
		{"ws per-principal connections exceed total", "WS_MAX_CONNECTIONS_PER_PRINCIPAL", "513", "WS_MAX_CONNECTIONS_PER_PRINCIPAL"},
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
