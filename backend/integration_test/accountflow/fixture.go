//go:build integration && account_e2e

package accountflow

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	logkit "github.com/wahrwelt-kit/go-logkit"

	testkit "github.com/TakuyaYagam1/task-per-minute/integration_test/internal/testkit"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/middleware"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
	authadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/auth"
	ffmpegadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/media/ffmpeg"
	postgresadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	accountrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/account"
	avatarrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/avatar"
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/auth"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	accountusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/account"
	avatarusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/avatar"
)

const (
	accountFlowStartupTimeout = 90 * time.Second
	accountFlowAdminPassword  = "Account-flow-admin-password-384"
)

type CapturedVerification struct {
	Recipient       string
	VerificationURL string
}

type MemoryMailer struct {
	mu             sync.Mutex
	queue          []CapturedVerification
	changeCodes    map[string]string
	changedNotices []CapturedEmailChangeNotice
	wake           chan struct{}
}

type CapturedEmailChangeNotice struct {
	PreviousEmail string
	NewEmail      string
}

func NewMemoryMailer() *MemoryMailer {
	return &MemoryMailer{
		changeCodes: make(map[string]string),
		wake:        make(chan struct{}, 1),
	}
}

func (m *MemoryMailer) SendVerification(ctx context.Context, recipient, verificationURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.queue = append(m.queue, CapturedVerification{Recipient: recipient, VerificationURL: verificationURL})
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	return nil
}

func (m *MemoryMailer) Receive(ctx context.Context) (CapturedVerification, error) {
	for {
		m.mu.Lock()
		if len(m.queue) > 0 {
			message := m.queue[0]
			m.queue = m.queue[1:]
			m.mu.Unlock()
			return message, nil
		}
		m.mu.Unlock()

		select {
		case <-ctx.Done():
			return CapturedVerification{}, ctx.Err()
		case <-m.wake:
		}
	}
}

func (m *MemoryMailer) SendEmailChangeCode(ctx context.Context, recipient, code string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.changeCodes[normalizeTestEmail(recipient)] = code
	m.mu.Unlock()
	return nil
}

func (m *MemoryMailer) SendEmailChangedNotice(ctx context.Context, previousEmail, newEmail string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	m.mu.Lock()
	m.changedNotices = append(m.changedNotices, CapturedEmailChangeNotice{
		PreviousEmail: normalizeTestEmail(previousEmail),
		NewEmail:      normalizeTestEmail(newEmail),
	})
	m.mu.Unlock()
	return nil
}

func (m *MemoryMailer) EmailChangeCode(recipient string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	code, ok := m.changeCodes[normalizeTestEmail(recipient)]
	return code, ok
}

func (m *MemoryMailer) EmailChangedNotices() []CapturedEmailChangeNotice {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]CapturedEmailChangeNotice(nil), m.changedNotices...)
}

type Fixture struct {
	Pool    *pgxpool.Pool
	Mailer  *MemoryMailer
	Handler http.Handler
	stopDB  func()
}

func New(ctx context.Context, frontendOrigin string) (*Fixture, error) {
	if err := validateLocalOrigin(frontendOrigin); err != nil {
		return nil, err
	}
	migrationsDir, err := migrationDir()
	if err != nil {
		return nil, err
	}
	//nolint:contextcheck // The testkit owns the bounded disposable-container startup context.
	pool, stopDB, err := startAccountFlowPostgres(migrationsDir)
	if err != nil {
		return nil, fmt.Errorf("start account-flow postgres: %w", err)
	}

	tx := postgresadapter.NewTxManager(pool)
	accountsRepo := accountrepo.NewAccountPostgres(tx)
	playersRepo := playerrepo.NewPlayerPostgres(tx)
	clock := systemClock{}
	mailer := NewMemoryMailer()
	passwordHasher := authadapter.NewPasswordHasher()
	adminAuth := authusecase.NewUseCase(authusecase.Config{
		AccessTTL:  15 * time.Minute,
		RefreshTTL: 7 * 24 * time.Hour,
	}, clock, testRevocationStore{}, authadapter.NewJWTCodec(authadapter.JWTConfig{
		Secret: []byte("account-flow-admin-signing-secret-32-bytes"),
		Now:    clock.Now,
	}), authadapter.NewPasswordVerifier([]byte(accountFlowAdminPassword)))
	accounts, err := accountusecase.NewUseCase(accountusecase.Config{
		VerificationPageURL: strings.TrimRight(frontendOrigin, "/") + "/verify-email",
		VerificationTTL:     24 * time.Hour,
		ResendCooldown:      time.Minute,
		SessionTTL:          24 * time.Hour,
	}, tx, accountsRepo, passwordHasher, mailer, clock)
	if err != nil {
		stopDB()
		return nil, fmt.Errorf("create account use case: %w", err)
	}
	accountSettings, err := accountusecase.NewAccountSettingsUseCase(
		accountusecase.SettingsConfig{SessionTTL: 24 * time.Hour},
		tx,
		accountsRepo,
		passwordHasher,
		mailer,
		nil,
		clock,
	)
	if err != nil {
		stopDB()
		return nil, fmt.Errorf("create account settings use case: %w", err)
	}
	avatarObjects := newMemoryAvatarStorage()
	videoProcessor, err := ffmpegadapter.NewProcessor(ffmpegadapter.Config{})
	if err != nil {
		stopDB()
		return nil, fmt.Errorf("create avatar video processor: %w", err)
	}
	avatars, err := avatarusecase.NewService(avatarrepo.NewRepository(tx), avatarObjects, clock, videoProcessor)
	if err != nil {
		stopDB()
		return nil, fmt.Errorf("create player avatar service: %w", err)
	}
	players := playerusecase.SessionNewUseCase(tx, playersRepo, clock, playerusecase.WithSessionTTL(24*time.Hour))
	validator, err := middleware.OpenAPIRequestValidator(ctx, logkit.Noop())
	if err != nil {
		stopDB()
		return nil, fmt.Errorf("create REST request validator: %w", err)
	}
	server := restv1.New(restv1.Dependencies{
		Players:                 players,
		PlayerAccounts:          accounts,
		AccountSettings:         accountSettings,
		PlayerAvatars:           avatars,
		AccountSensitiveLimiter: integrationRateLimiter{},
		AvatarMutationLimiter:   integrationRateLimiter{},
		AdminAuth:               adminAuth,
		AdminPlayers:            playerusecase.ManagementNewUseCase(tx, playersRepo, nil, clock),
		Now:                     clock.Now,
	})
	apiHandler := restv1.NewHandler(server, restv1.HandlerOptions{
		AdminAuth:        adminAuth,
		PlayerRepo:       playersRepo,
		RequestValidator: validator,
		Middlewares: []api.MiddlewareFunc{
			middleware.Build(logkit.Noop(), middleware.WithAllowedOrigins([]string{frontendOrigin})),
		},
	})

	fixture := &Fixture{Pool: pool, Mailer: mailer, stopDB: stopDB}
	root := http.NewServeMux()
	root.HandleFunc("/__test/ready", fixture.ready)
	root.HandleFunc("/__test/verification-link", fixture.verificationLink)
	root.HandleFunc("/__test/email-change-code", fixture.emailChangeCode)
	root.Handle("/", apiHandler)
	fixture.Handler = middleware.CORS([]string{frontendOrigin})(root)
	return fixture, nil
}

type testRevocationStore struct{}

func (testRevocationStore) Revoke(context.Context, string, time.Time) error { return nil }

func (testRevocationStore) IsRevoked(context.Context, string) (bool, error) { return false, nil }

type integrationRateLimiter struct{}

func (integrationRateLimiter) Allow(string) bool { return true }

func (integrationRateLimiter) RetryAfter() string { return "60" }

func startAccountFlowPostgres(migrationsDir string) (*pgxpool.Pool, func(), error) {
	return testkit.StartPostgres(testkit.PostgresConfig{
		DSN:            strings.TrimSpace(os.Getenv("TPM_TEST_POSTGRES_DSN")),
		MigrationsDir:  migrationsDir,
		StartupTimeout: accountFlowStartupTimeout,
	})
}

type memoryAvatarStorage struct {
	mu      sync.RWMutex
	objects map[string][]byte
}

func newMemoryAvatarStorage() *memoryAvatarStorage {
	return &memoryAvatarStorage{objects: make(map[string][]byte)}
}

func (s *memoryAvatarStorage) PutAvatar(ctx context.Context, key string, data []byte, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	s.objects[key] = append([]byte(nil), data...)
	s.mu.Unlock()
	return nil
}

func (s *memoryAvatarStorage) GetAvatar(ctx context.Context, key string, maxBytes int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	data, ok := s.objects[key]
	copyData := append([]byte(nil), data...)
	s.mu.RUnlock()
	if !ok {
		return nil, domain.ErrAvatarNotFound
	}
	if int64(len(copyData)) > maxBytes {
		return nil, domain.ErrAvatarTooLarge
	}
	return copyData, nil
}

func (s *memoryAvatarStorage) DeleteAvatar(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.objects, key)
	s.mu.Unlock()
	return nil
}

var _ avatarusecase.ObjectStorage = (*memoryAvatarStorage)(nil)

func (f *Fixture) Close() {
	if f == nil || f.stopDB == nil {
		return
	}
	f.stopDB()
	f.stopDB = nil
}

func (f *Fixture) ready(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !isLoopbackRequest(r) {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (f *Fixture) verificationLink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !isLoopbackRequest(r) {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 500*time.Millisecond)
	defer cancel()
	message, err := f.Mailer.Receive(ctx)
	if err != nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		VerificationURL string `json:"verification_url"`
	}{VerificationURL: message.VerificationURL})
}

func (f *Fixture) emailChangeCode(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet || !isLoopbackRequest(r) {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	query := r.URL.Query()
	values, ok := query["email"]
	if !ok || len(values) != 1 || len(query) != 1 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	code, exists := f.Mailer.EmailChangeCode(values[0])
	if !exists {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(struct {
		Code string `json:"code"`
	}{Code: code})
}

func normalizeTestEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

func migrationDir() (string, error) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return "", errors.New("resolve account-flow migration path")
	}
	dir, err := filepath.Abs(filepath.Join(filepath.Dir(source), "..", "..", "db", "migrations"))
	if err != nil {
		return "", fmt.Errorf("resolve account-flow migration path: %w", err)
	}
	return dir, nil
}

func validateLocalOrigin(value string) error {
	parsed, err := url.Parse(value)
	if err != nil || parsed == nil || parsed.Scheme != "http" || parsed.User != nil ||
		parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("account-flow frontend origin must be a local HTTP origin")
	}
	host := parsed.Hostname()
	if host != "localhost" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return errors.New("account-flow frontend origin must use loopback")
		}
	}
	return nil
}

func isLoopbackRequest(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

var _ accountusecase.VerificationMailer = (*MemoryMailer)(nil)
var _ accountusecase.Clock = systemClock{}
