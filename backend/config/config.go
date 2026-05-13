package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

const minJWTSecretBytes = 32

var placeholderFragments = []string{
	"change-me",
	"changeme",
	"replace-me",
	"placeholder",
	"your-",
}

type Config struct {
	HTTP      HTTP      `env-prefix:"HTTP_"`
	DB        DB        `env-prefix:"DB_"`
	Redis     Redis     `env-prefix:"REDIS_"`
	SeaweedFS SeaweedFS `env-prefix:"SEAWEEDFS_"`
	JWT       JWT       `env-prefix:"JWT_"`
	Admin     Admin     `env-prefix:"ADMIN_"`
	Player    Player    `env-prefix:"PLAYER_"`
	WS        WebSocket `env-prefix:"WS_"`
}

type MigrationConfig struct {
	DB MigrationDB `env-prefix:"DB_"`
}

type MigrationDB struct {
	DSN string `env:"DSN" env-required:"true"`
}

type HTTP struct {
	Host              string        `env:"HOST"                env-default:"0.0.0.0"`
	Port              int           `env:"PORT"                env-default:"8080"`
	ReadTimeout       time.Duration `env:"READ_TIMEOUT"        env-default:"15s"`
	WriteTimeout      time.Duration `env:"WRITE_TIMEOUT"       env-default:"15s"`
	ShutdownTimeout   time.Duration `env:"SHUTDOWN_TIMEOUT"    env-default:"30s"`
	AllowedOrigins    []string      `env:"ALLOWED_ORIGINS"     env-separator:","`
	TrustedProxyCIDRs []string      `env:"TRUSTED_PROXY_CIDRS" env-separator:","`
}

type DB struct {
	DSN      string `env:"DSN"       env-required:"true"`
	MaxConns int32  `env:"MAX_CONNS" env-default:"20"`
}

type Redis struct {
	Addr     string `env:"ADDR"     env-default:"localhost:6379"`
	Password string `env:"PASSWORD"`
	DB       int    `env:"DB"       env-default:"0"`
}

type SeaweedFS struct {
	Endpoint       string `env:"ENDPOINT"        env-required:"true"`
	PublicEndpoint string `env:"PUBLIC_ENDPOINT"`
	AccessKey      string `env:"ACCESS_KEY"      env-required:"true"`
	SecretKey      string `env:"SECRET_KEY"      env-required:"true"`
	Bucket         string `env:"BUCKET"          env-default:"task-per-minute"`
	Secure         bool   `env:"SECURE"          env-default:"false"`
	PublicSecure   bool   `env:"PUBLIC_SECURE"   env-default:"false"`
}

type JWT struct {
	Secret     string        `env:"SECRET"      env-required:"true"`
	AccessTTL  time.Duration `env:"ACCESS_TTL"  env-default:"15m"`
	RefreshTTL time.Duration `env:"REFRESH_TTL" env-default:"168h"`
}

type Admin struct {
	Password             string        `env:"PASSWORD"              env-required:"true"`
	LoginRateAttempts    int           `env:"LOGIN_RATE_ATTEMPTS"   env-default:"5"`
	LoginRateWindow      time.Duration `env:"LOGIN_RATE_WINDOW"     env-default:"15m"`
	LoginRateBucketTTL   time.Duration `env:"LOGIN_RATE_BUCKET_TTL" env-default:"1h"`
	RefreshRateAttempts  int           `env:"REFRESH_RATE_ATTEMPTS"`
	RefreshRateWindow    time.Duration `env:"REFRESH_RATE_WINDOW"`
	RefreshRateBucketTTL time.Duration `env:"REFRESH_RATE_BUCKET_TTL"`
}

type Player struct {
	JoinRateAttempts  int           `env:"JOIN_RATE_ATTEMPTS"   env-default:"20"`
	JoinRateWindow    time.Duration `env:"JOIN_RATE_WINDOW"     env-default:"5m"`
	JoinRateBucketTTL time.Duration `env:"JOIN_RATE_BUCKET_TTL" env-default:"1h"`
}

type WebSocket struct {
	AllowedOrigins         []string      `env:"ALLOWED_ORIGINS"           env-separator:","`
	RequireOrigin          bool          `env:"REQUIRE_ORIGIN"            env-default:"false"`
	HandshakeRateAttempts  int           `env:"HANDSHAKE_RATE_ATTEMPTS"   env-default:"60"`
	HandshakeRateWindow    time.Duration `env:"HANDSHAKE_RATE_WINDOW"     env-default:"1m"`
	HandshakeRateBucketTTL time.Duration `env:"HANDSHAKE_RATE_BUCKET_TTL" env-default:"15m"`
}

func Load() (*Config, error) {
	var cfg Config
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return nil, fmt.Errorf("config - Load - cleanenv.ReadEnv: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config - Load - validate: %w", err)
	}
	return &cfg, nil
}

func LoadMigration() (*MigrationConfig, error) {
	var cfg MigrationConfig
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return nil, fmt.Errorf("config - LoadMigration - cleanenv.ReadEnv: %w", err)
	}
	if err := validateDBDSN(cfg.DB.DSN); err != nil {
		return nil, fmt.Errorf("config - LoadMigration - validate DB_DSN: %w", err)
	}
	return &cfg, nil
}

func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("nil config")
	}
	if err := validateHTTP(&c.HTTP); err != nil {
		return err
	}
	if err := validateDB(c.DB); err != nil {
		return err
	}
	if err := validateRedis(c.Redis); err != nil {
		return err
	}
	if err := validateSeaweedFS(&c.SeaweedFS); err != nil {
		return err
	}
	if err := validateJWT(c.JWT); err != nil {
		return err
	}
	if err := validateAdmin(&c.Admin); err != nil {
		return err
	}
	if err := validatePlayer(c.Player); err != nil {
		return err
	}
	if err := validateWS(&c.WS); err != nil {
		return err
	}
	return nil
}

func validateHTTP(cfg *HTTP) error {
	if cfg == nil {
		return fmt.Errorf("HTTP config must not be nil")
	}
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("HTTP_PORT must be between 1 and 65535")
	}
	if err := positiveDuration("HTTP_READ_TIMEOUT", cfg.ReadTimeout); err != nil {
		return err
	}
	if err := positiveDuration("HTTP_WRITE_TIMEOUT", cfg.WriteTimeout); err != nil {
		return err
	}
	if err := positiveDuration("HTTP_SHUTDOWN_TIMEOUT", cfg.ShutdownTimeout); err != nil {
		return err
	}
	origins, err := normalizeAllowedOrigins("HTTP_ALLOWED_ORIGINS", cfg.AllowedOrigins)
	if err != nil {
		return err
	}
	cfg.AllowedOrigins = origins
	cidrs, err := normalizeTrustedProxyCIDRs("HTTP_TRUSTED_PROXY_CIDRS", cfg.TrustedProxyCIDRs)
	if err != nil {
		return err
	}
	cfg.TrustedProxyCIDRs = cidrs
	return nil
}

func validateDB(cfg DB) error {
	if err := validateDBDSN(cfg.DSN); err != nil {
		return err
	}
	if cfg.MaxConns <= 0 {
		return fmt.Errorf("DB_MAX_CONNS must be positive")
	}
	return nil
}

func validateDBDSN(dsn string) error {
	if strings.TrimSpace(dsn) == "" {
		return fmt.Errorf("DB_DSN must not be empty")
	}
	if hasPlaceholder(dsn) {
		return fmt.Errorf("DB_DSN must not use a placeholder value")
	}
	return nil
}

func validateRedis(cfg Redis) error {
	if strings.TrimSpace(cfg.Addr) == "" {
		return fmt.Errorf("REDIS_ADDR must not be empty")
	}
	if cfg.DB < 0 {
		return fmt.Errorf("REDIS_DB must be non-negative")
	}
	if cfg.Password != "" && hasPlaceholder(cfg.Password) {
		return fmt.Errorf("REDIS_PASSWORD must not use a placeholder value")
	}
	return nil
}

func validateSeaweedFS(cfg *SeaweedFS) error {
	if cfg == nil {
		return fmt.Errorf("SeaweedFS config must not be nil")
	}
	cfg.Endpoint = strings.TrimSpace(cfg.Endpoint)
	cfg.PublicEndpoint = strings.TrimSpace(cfg.PublicEndpoint)
	cfg.Bucket = strings.TrimSpace(cfg.Bucket)

	if cfg.Endpoint == "" {
		return fmt.Errorf("SEAWEEDFS_ENDPOINT must not be empty")
	}
	if err := validateOptionalHostEndpoint("SEAWEEDFS_PUBLIC_ENDPOINT", cfg.PublicEndpoint); err != nil {
		return err
	}
	if invalidSecret(cfg.AccessKey) {
		return fmt.Errorf("SEAWEEDFS_ACCESS_KEY must not be empty or placeholder")
	}
	if invalidSecret(cfg.SecretKey) {
		return fmt.Errorf("SEAWEEDFS_SECRET_KEY must not be empty or placeholder")
	}
	if cfg.Bucket == "" {
		return fmt.Errorf("SEAWEEDFS_BUCKET must not be empty")
	}
	return nil
}

func validateOptionalHostEndpoint(name, endpoint string) error {
	value := strings.TrimSpace(endpoint)
	if value == "" {
		return nil
	}
	if strings.Contains(value, "://") {
		return fmt.Errorf("%s must be host[:port] without scheme", name)
	}
	parsed, err := url.Parse("//" + value)
	if err != nil || parsed.Host == "" || parsed.User != nil ||
		parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return fmt.Errorf("%s contains invalid endpoint %q", name, endpoint)
	}
	return nil
}

func validateJWT(cfg JWT) error {
	if len([]byte(cfg.Secret)) < minJWTSecretBytes {
		return fmt.Errorf("JWT_SECRET must be at least %d bytes", minJWTSecretBytes)
	}
	if hasPlaceholder(cfg.Secret) {
		return fmt.Errorf("JWT_SECRET must not use a placeholder value")
	}
	if err := positiveDuration("JWT_ACCESS_TTL", cfg.AccessTTL); err != nil {
		return err
	}
	return positiveDuration("JWT_REFRESH_TTL", cfg.RefreshTTL)
}

func validateAdmin(cfg *Admin) error {
	if cfg == nil {
		return fmt.Errorf("Admin config must not be nil")
	}
	if invalidSecret(cfg.Password) {
		return fmt.Errorf("ADMIN_PASSWORD must not be empty or placeholder")
	}
	if cfg.LoginRateAttempts <= 0 {
		return fmt.Errorf("ADMIN_LOGIN_RATE_ATTEMPTS must be positive")
	}
	if err := positiveDuration("ADMIN_LOGIN_RATE_WINDOW", cfg.LoginRateWindow); err != nil {
		return err
	}
	if err := positiveDuration("ADMIN_LOGIN_RATE_BUCKET_TTL", cfg.LoginRateBucketTTL); err != nil {
		return err
	}
	if cfg.RefreshRateAttempts == 0 {
		cfg.RefreshRateAttempts = cfg.LoginRateAttempts
	}
	if cfg.RefreshRateWindow == 0 {
		cfg.RefreshRateWindow = cfg.LoginRateWindow
	}
	if cfg.RefreshRateBucketTTL == 0 {
		cfg.RefreshRateBucketTTL = cfg.LoginRateBucketTTL
	}
	if cfg.RefreshRateAttempts < 0 {
		return fmt.Errorf("ADMIN_REFRESH_RATE_ATTEMPTS must be positive")
	}
	if err := positiveDuration("ADMIN_REFRESH_RATE_WINDOW", cfg.RefreshRateWindow); err != nil {
		return err
	}
	return positiveDuration("ADMIN_REFRESH_RATE_BUCKET_TTL", cfg.RefreshRateBucketTTL)
}

func validatePlayer(cfg Player) error {
	if cfg.JoinRateAttempts <= 0 {
		return fmt.Errorf("PLAYER_JOIN_RATE_ATTEMPTS must be positive")
	}
	if err := positiveDuration("PLAYER_JOIN_RATE_WINDOW", cfg.JoinRateWindow); err != nil {
		return err
	}
	return positiveDuration("PLAYER_JOIN_RATE_BUCKET_TTL", cfg.JoinRateBucketTTL)
}

func validateWS(cfg *WebSocket) error {
	if cfg == nil {
		return fmt.Errorf("WS config must not be nil")
	}
	if cfg.HandshakeRateAttempts <= 0 {
		return fmt.Errorf("WS_HANDSHAKE_RATE_ATTEMPTS must be positive")
	}
	if err := positiveDuration("WS_HANDSHAKE_RATE_WINDOW", cfg.HandshakeRateWindow); err != nil {
		return err
	}
	if err := positiveDuration("WS_HANDSHAKE_RATE_BUCKET_TTL", cfg.HandshakeRateBucketTTL); err != nil {
		return err
	}
	origins, err := normalizeAllowedOrigins("WS_ALLOWED_ORIGINS", cfg.AllowedOrigins)
	if err != nil {
		return err
	}
	cfg.AllowedOrigins = origins
	return nil
}

func positiveDuration(name string, value time.Duration) error {
	if value <= 0 {
		return fmt.Errorf("%s must be positive", name)
	}
	return nil
}

func normalizeAllowedOrigins(name string, origins []string) ([]string, error) {
	normalized := make([]string, 0, len(origins))
	seen := make(map[string]struct{}, len(origins))
	for _, raw := range origins {
		origin := strings.TrimSpace(raw)
		if origin == "" {
			continue
		}
		if origin == "*" {
			return nil, fmt.Errorf("%s must not use wildcard origin", name)
		}
		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" {
			return nil, fmt.Errorf("%s contains invalid origin %q", name, origin)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return nil, fmt.Errorf("%s origin %q must use http or https", name, origin)
		}
		if parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
			return nil, fmt.Errorf("%s origin %q must not contain path, query, or fragment", name, origin)
		}
		if _, ok := seen[origin]; ok {
			continue
		}
		seen[origin] = struct{}{}
		normalized = append(normalized, origin)
	}
	return normalized, nil
}

func normalizeTrustedProxyCIDRs(name string, cidrs []string) ([]string, error) {
	normalized := make([]string, 0, len(cidrs))
	seen := make(map[string]struct{}, len(cidrs))
	for _, raw := range cidrs {
		cidr := strings.TrimSpace(raw)
		if cidr == "" {
			continue
		}
		_, network, err := net.ParseCIDR(cidr)
		if err != nil {
			return nil, fmt.Errorf("%s contains invalid CIDR %q", name, cidr)
		}
		canonical := network.String()
		if _, ok := seen[canonical]; ok {
			continue
		}
		seen[canonical] = struct{}{}
		normalized = append(normalized, canonical)
	}
	return normalized, nil
}

func invalidSecret(value string) bool {
	return strings.TrimSpace(value) == "" || hasPlaceholder(value)
}

func hasPlaceholder(value string) bool {
	normalized := strings.ToLower(strings.TrimSpace(value))
	for _, fragment := range placeholderFragments {
		if strings.Contains(normalized, fragment) {
			return true
		}
	}
	return false
}

func MustLoad() *Config {
	cfg, err := Load()
	if err != nil {
		panic(err)
	}
	return cfg
}
