package config

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
)

const (
	minJWTSecretBytes          = 32
	minIncidentHMACSecretBytes = 32
	maxIncidentHMACKeyIDBytes  = 64
)

var placeholderFragments = []string{
	"change-me",
	"changeme",
	"replace-me",
	"placeholder",
	"your-",
}

type Config struct {
	HTTP        HTTP        `env-prefix:"HTTP_"`
	DB          DB          `env-prefix:"DB_"`
	Redis       Redis       `env-prefix:"REDIS_"`
	SeaweedFS   SeaweedFS   `env-prefix:"SEAWEEDFS_"`
	JWT         JWT         `env-prefix:"JWT_"`
	Incident    Incident    `env-prefix:"INCIDENT_EXPORT_"`
	Admin       Admin       `env-prefix:"ADMIN_"`
	Player      Player      `env-prefix:"PLAYER_"`
	Leaderboard Leaderboard `env-prefix:"LEADERBOARD_"`
	Tournament  Tournament  `env-prefix:"TOURNAMENT_"`
	WS          WebSocket   `env-prefix:"WS_"`
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

type Incident struct {
	HMACKeyID  string `env:"HMAC_KEY_ID" env-required:"true"`
	HMACSecret string `env:"HMAC_SECRET" env-required:"true"`
}

type Admin struct {
	Password            string        `env:"PASSWORD"                env-required:"true"`
	LoginRateAttempts   int           `env:"LOGIN_RATE_ATTEMPTS"     env-default:"5"`
	LoginRateWindow     time.Duration `env:"LOGIN_RATE_WINDOW"       env-default:"15m"`
	RefreshRateAttempts int           `env:"REFRESH_RATE_ATTEMPTS"`
	RefreshRateWindow   time.Duration `env:"REFRESH_RATE_WINDOW"`
}

type Player struct {
	JoinRateAttempts int           `env:"JOIN_RATE_ATTEMPTS"   env-default:"20"`
	JoinRateWindow   time.Duration `env:"JOIN_RATE_WINDOW"     env-default:"5m"`
	SessionTTL       time.Duration `env:"SESSION_TTL"          env-default:"24h"`
}

type Leaderboard struct {
	RateAttempts int           `env:"RATE_ATTEMPTS"   env-default:"120"`
	RateWindow   time.Duration `env:"RATE_WINDOW"     env-default:"1m"`
}

type Tournament struct {
	PublicReadRateAttempts          int           `env:"PUBLIC_READ_RATE_ATTEMPTS"           env-default:"120"`
	PublicReadRateWindow            time.Duration `env:"PUBLIC_READ_RATE_WINDOW"             env-default:"1m"`
	OperatorReadRateAttempts        int           `env:"OPERATOR_READ_RATE_ATTEMPTS"         env-default:"120"`
	OperatorReadRateWindow          time.Duration `env:"OPERATOR_READ_RATE_WINDOW"           env-default:"1m"`
	OperatorMutationRateAttempts    int           `env:"OPERATOR_MUTATION_RATE_ATTEMPTS"     env-default:"120"`
	OperatorMutationRateWindow      time.Duration `env:"OPERATOR_MUTATION_RATE_WINDOW"       env-default:"1m"`
	ParticipantReadRateAttempts     int           `env:"PARTICIPANT_READ_RATE_ATTEMPTS"      env-default:"120"`
	ParticipantReadRateWindow       time.Duration `env:"PARTICIPANT_READ_RATE_WINDOW"        env-default:"1m"`
	ParticipantMutationRateAttempts int           `env:"PARTICIPANT_MUTATION_RATE_ATTEMPTS"  env-default:"120"`
	ParticipantMutationRateWindow   time.Duration `env:"PARTICIPANT_MUTATION_RATE_WINDOW"    env-default:"1m"`
}

type WebSocket struct {
	AllowedOrigins                  []string      `env:"ALLOWED_ORIGINS"               env-separator:","`
	RequireOrigin                   bool          `env:"REQUIRE_ORIGIN"                env-default:"false"`
	HandshakeRateAttempts           int           `env:"HANDSHAKE_RATE_ATTEMPTS"       env-default:"60"`
	HandshakeRateWindow             time.Duration `env:"HANDSHAKE_RATE_WINDOW"         env-default:"1m"`
	MaxConnections                  int           `env:"MAX_CONNECTIONS"               env-default:"512"`
	MaxConnectionsPerPrincipal      int           `env:"MAX_CONNECTIONS_PER_PRINCIPAL" env-default:"4"`
	DeliveryReceiptRetention        time.Duration `env:"DELIVERY_RECEIPT_RETENTION"    env-default:"720h"`
	DeliveryReceiptCleanupInterval  time.Duration `env:"DELIVERY_RECEIPT_CLEANUP_INTERVAL" env-default:"5m"`
	DeliveryReceiptCleanupBatchSize int32         `env:"DELIVERY_RECEIPT_CLEANUP_BATCH_SIZE" env-default:"128"`
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
	if err := validateIncident(&c.Incident, c.JWT.Secret); err != nil {
		return err
	}
	if err := validateAdmin(&c.Admin); err != nil {
		return err
	}
	if err := validatePlayer(c.Player); err != nil {
		return err
	}
	if err := validateLeaderboard(c.Leaderboard); err != nil {
		return err
	}
	if err := validateTournament(c.Tournament); err != nil {
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

func validateIncident(cfg *Incident, jwtSecret string) error {
	if cfg == nil {
		return fmt.Errorf("incident export config must not be nil")
	}
	cfg.HMACKeyID = strings.TrimSpace(cfg.HMACKeyID)
	if !validIncidentHMACKeyID(cfg.HMACKeyID) {
		return fmt.Errorf("INCIDENT_EXPORT_HMAC_KEY_ID must be 1 to %d ASCII key-id characters", maxIncidentHMACKeyIDBytes)
	}
	if len([]byte(cfg.HMACSecret)) < minIncidentHMACSecretBytes {
		return fmt.Errorf("INCIDENT_EXPORT_HMAC_SECRET must be at least %d bytes", minIncidentHMACSecretBytes)
	}
	if hasPlaceholder(cfg.HMACSecret) {
		return fmt.Errorf("INCIDENT_EXPORT_HMAC_SECRET must not use a placeholder value")
	}
	if cfg.HMACSecret == jwtSecret {
		return fmt.Errorf("INCIDENT_EXPORT_HMAC_SECRET must not reuse JWT_SECRET")
	}
	return nil
}

func validIncidentHMACKeyID(value string) bool {
	if len(value) == 0 || len(value) > maxIncidentHMACKeyIDBytes {
		return false
	}
	for index := range len(value) {
		current := value[index]
		isLetter := (current >= 'a' && current <= 'z') || (current >= 'A' && current <= 'Z')
		isDigit := current >= '0' && current <= '9'
		if (index == 0 && !isLetter && !isDigit) ||
			(index > 0 && !isLetter && !isDigit && current != '.' && current != '_' && current != '-') {
			return false
		}
	}
	return true
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
	if err := rateLimitDuration("ADMIN_LOGIN_RATE_WINDOW", cfg.LoginRateWindow); err != nil {
		return err
	}
	if cfg.RefreshRateAttempts == 0 {
		cfg.RefreshRateAttempts = cfg.LoginRateAttempts
	}
	if cfg.RefreshRateWindow == 0 {
		cfg.RefreshRateWindow = cfg.LoginRateWindow
	}
	if cfg.RefreshRateAttempts < 0 {
		return fmt.Errorf("ADMIN_REFRESH_RATE_ATTEMPTS must be non-negative")
	}
	if err := rateLimitDuration("ADMIN_REFRESH_RATE_WINDOW", cfg.RefreshRateWindow); err != nil {
		return err
	}
	return nil
}

func validatePlayer(cfg Player) error {
	if cfg.JoinRateAttempts <= 0 {
		return fmt.Errorf("PLAYER_JOIN_RATE_ATTEMPTS must be positive")
	}
	if err := rateLimitDuration("PLAYER_JOIN_RATE_WINDOW", cfg.JoinRateWindow); err != nil {
		return err
	}
	return positiveDuration("PLAYER_SESSION_TTL", cfg.SessionTTL)
}

func validateLeaderboard(cfg Leaderboard) error {
	if cfg.RateAttempts <= 0 {
		return fmt.Errorf("LEADERBOARD_RATE_ATTEMPTS must be positive")
	}
	if err := rateLimitDuration("LEADERBOARD_RATE_WINDOW", cfg.RateWindow); err != nil {
		return err
	}
	return nil
}

func validateTournament(cfg Tournament) error {
	policies := []struct {
		name     string
		attempts int
		window   time.Duration
	}{
		{"TOURNAMENT_PUBLIC_READ_RATE", cfg.PublicReadRateAttempts, cfg.PublicReadRateWindow},
		{"TOURNAMENT_OPERATOR_READ_RATE", cfg.OperatorReadRateAttempts, cfg.OperatorReadRateWindow},
		{"TOURNAMENT_OPERATOR_MUTATION_RATE", cfg.OperatorMutationRateAttempts, cfg.OperatorMutationRateWindow},
		{"TOURNAMENT_PARTICIPANT_READ_RATE", cfg.ParticipantReadRateAttempts, cfg.ParticipantReadRateWindow},
		{"TOURNAMENT_PARTICIPANT_MUTATION_RATE", cfg.ParticipantMutationRateAttempts, cfg.ParticipantMutationRateWindow},
	}
	for _, policy := range policies {
		if policy.attempts <= 0 {
			return fmt.Errorf("%s_ATTEMPTS must be positive", policy.name)
		}
		if err := rateLimitDuration(policy.name+"_WINDOW", policy.window); err != nil {
			return err
		}
	}
	return nil
}

func validateWS(cfg *WebSocket) error {
	if cfg == nil {
		return fmt.Errorf("WS config must not be nil")
	}
	if cfg.HandshakeRateAttempts <= 0 {
		return fmt.Errorf("WS_HANDSHAKE_RATE_ATTEMPTS must be positive")
	}
	if err := rateLimitDuration("WS_HANDSHAKE_RATE_WINDOW", cfg.HandshakeRateWindow); err != nil {
		return err
	}
	if cfg.MaxConnections <= 0 {
		return fmt.Errorf("WS_MAX_CONNECTIONS must be positive")
	}
	if cfg.MaxConnectionsPerPrincipal <= 0 {
		return fmt.Errorf("WS_MAX_CONNECTIONS_PER_PRINCIPAL must be positive")
	}
	if cfg.MaxConnectionsPerPrincipal > cfg.MaxConnections {
		return fmt.Errorf("WS_MAX_CONNECTIONS_PER_PRINCIPAL must not exceed WS_MAX_CONNECTIONS")
	}
	if err := positiveDuration("WS_DELIVERY_RECEIPT_RETENTION", cfg.DeliveryReceiptRetention); err != nil {
		return err
	}
	if err := positiveDuration("WS_DELIVERY_RECEIPT_CLEANUP_INTERVAL", cfg.DeliveryReceiptCleanupInterval); err != nil {
		return err
	}
	if cfg.DeliveryReceiptCleanupBatchSize < 1 || cfg.DeliveryReceiptCleanupBatchSize > 256 {
		return fmt.Errorf("WS_DELIVERY_RECEIPT_CLEANUP_BATCH_SIZE must be between 1 and 256")
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

func rateLimitDuration(name string, value time.Duration) error {
	if value.Milliseconds() < 1 {
		return fmt.Errorf("%s must be at least 1ms", name)
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
		if strings.ContainsAny(origin, "*?[\\") {
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
