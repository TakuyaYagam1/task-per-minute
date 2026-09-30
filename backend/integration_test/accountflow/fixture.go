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
	postgresadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres"
	accountrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/account"
	playerrepo "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/postgres/player"
	playerusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player"
	accountusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/player/account"
)

const accountFlowStartupTimeout = 90 * time.Second

type CapturedVerification struct {
	Recipient       string
	VerificationURL string
}

type MemoryMailer struct {
	mu    sync.Mutex
	queue []CapturedVerification
	wake  chan struct{}
}

func NewMemoryMailer() *MemoryMailer {
	return &MemoryMailer{wake: make(chan struct{}, 1)}
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
	accounts, err := accountusecase.NewUseCase(accountusecase.Config{
		VerificationPageURL: strings.TrimRight(frontendOrigin, "/") + "/verify-email",
		VerificationTTL:     24 * time.Hour,
		ResendCooldown:      time.Minute,
		SessionTTL:          24 * time.Hour,
	}, tx, accountsRepo, authadapter.NewPasswordHasher(), mailer, clock)
	if err != nil {
		stopDB()
		return nil, fmt.Errorf("create account use case: %w", err)
	}
	players := playerusecase.SessionNewUseCase(tx, playersRepo, clock, playerusecase.WithSessionTTL(24*time.Hour))
	validator, err := middleware.OpenAPIRequestValidator(ctx, logkit.Noop())
	if err != nil {
		stopDB()
		return nil, fmt.Errorf("create REST request validator: %w", err)
	}
	server := restv1.New(restv1.Dependencies{
		Players:        players,
		PlayerAccounts: accounts,
		Now:            clock.Now,
	})
	apiHandler := restv1.NewHandler(server, restv1.HandlerOptions{
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
	root.Handle("/", apiHandler)
	fixture.Handler = middleware.CORS([]string{frontendOrigin})(root)
	return fixture, nil
}

func startAccountFlowPostgres(migrationsDir string) (*pgxpool.Pool, func(), error) {
	return testkit.StartPostgres(testkit.PostgresConfig{
		MigrationsDir:  migrationsDir,
		StartupTimeout: accountFlowStartupTimeout,
	})
}

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
