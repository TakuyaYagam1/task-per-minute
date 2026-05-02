package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/wire"
)

const (
	defaultShutdownTimeout    = 30 * time.Second
	migrationsDir             = "migrations"
	revocationJanitorInterval = 5 * time.Minute
)

func Run(cfg *config.Config, log logkit.Logger) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	application, cleanup, err := Initialize(ctx, cfg, log)
	if err != nil {
		logAppError(log, err)
		return err
	}
	defer cleanup()

	if err := application.Run(ctx); err != nil {
		logAppError(log, err)
		return err
	}
	return nil
}

func Migrate(ctx context.Context, cfg *config.Config, log logkit.Logger) error {
	return RunMigrations(ctx, cfg, log, "up")
}

func RunMigrations(ctx context.Context, cfg *config.Config, log logkit.Logger, command string) error {
	if ctx == nil {
		return errors.New("app migrate: nil context")
	}
	if cfg == nil {
		return errors.New("app migrate: nil config")
	}
	return RunMigrationsDSN(ctx, cfg.DB.DSN, log, command)
}

func RunMigrationsDSN(ctx context.Context, dsn string, log logkit.Logger, command string) error {
	if ctx == nil {
		return errors.New("app migrate: nil context")
	}
	if dsn == "" {
		return errors.New("app migrate: empty DB_DSN")
	}
	if command == "" {
		command = "up"
	}

	if log != nil {
		log.Info("database migrations starting", logkit.Fields{"command": command})
	}
	migrator := NewMigrator(dsn, ResolveMigrationsDir(migrationsDir))

	var err error
	switch command {
	case "up":
		err = migrator.Up(ctx)
	case "down":
		err = migrator.Down(ctx)
	case "status":
		err = migrator.Status(ctx)
	default:
		err = fmt.Errorf("unknown migration command %q", command)
	}
	if err != nil {
		return fmt.Errorf("app migrate: %w", err)
	}
	if log != nil {
		log.Info("database migrations finished", logkit.Fields{"command": command})
	}
	return nil
}

func Initialize(ctx context.Context, cfg *config.Config, log logkit.Logger) (*App, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("app: nil context")
	}
	if cfg == nil {
		return nil, nil, errors.New("app: nil config")
	}

	runtime := NewRuntimeContext(ctx)
	//nolint:contextcheck // runtime.Context() inherits from ctx and lets App.Shutdown cancel server-owned infra.
	wired, cleanup, err := wire.InitializeApp(runtime.Context(), cfg, log)
	if err != nil {
		runtime.Cancel()
		return nil, nil, fmt.Errorf("initialize app: %w", err)
	}

	migrator := NewMigrator(cfg.DB.DSN, ResolveMigrationsDir(migrationsDir))
	recovery := NewStartupRecoverer(
		wired.Tx,
		wired.Duels,
		wired.Players,
		wired.Queued,
		wired.Queue,
		wired.Broadcaster,
		wired.Clock,
		log,
	)

	application := New(
		cfg,
		log,
		runtime,
		wired.Storage,
		migrator,
		recovery,
		wired.Server,
		wired.WebSocket,
		wired.Revocation,
	)

	return application, func() {
		runtime.Cancel()
		cleanup()
	}, nil
}

func logAppError(log logkit.Logger, err error) {
	if err == nil {
		return
	}
	if log != nil {
		log.WithError(err).Error("application stopped")
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "task-per-minute backend: %v\n", err)
}

type RuntimeContext struct {
	ctx    context.Context
	cancel context.CancelFunc
}

func NewRuntimeContext(parent context.Context) *RuntimeContext { //nolint:contextcheck // nil parent means a root app lifecycle context at the composition boundary.
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	return &RuntimeContext{ctx: ctx, cancel: cancel}
}

func (c *RuntimeContext) Context() context.Context {
	if c == nil || c.ctx == nil {
		return context.Background()
	}
	return c.ctx
}

func (c *RuntimeContext) Cancel() {
	if c == nil || c.cancel == nil {
		return
	}
	c.cancel()
}

type BucketEnsurer interface {
	EnsureBucket(ctx context.Context) error
}

type WebSocketShutdowner interface {
	Shutdown(ctx context.Context)
}

// RevocationJanitor is implemented by stores that need periodic eviction of
// expired entries (in-memory) and is a no-op for stores with native TTL
// (Redis). The App runs Cleanup on a ticker bound to the runtime context.
type RevocationJanitor interface {
	Cleanup()
}

type App struct {
	cfg        *config.Config
	log        logkit.Logger
	runtime    *RuntimeContext
	storage    BucketEnsurer
	migrator   *Migrator
	recovery   *StartupRecoverer
	server     *http.Server
	websocket  WebSocketShutdowner
	revocation RevocationJanitor
}

func New(
	cfg *config.Config,
	log logkit.Logger,
	runtime *RuntimeContext,
	storage BucketEnsurer,
	migrator *Migrator,
	recovery *StartupRecoverer,
	server *http.Server,
	websocket WebSocketShutdowner,
	revocation RevocationJanitor,
) *App {
	return &App{
		cfg:        cfg,
		log:        log,
		runtime:    runtime,
		storage:    storage,
		migrator:   migrator,
		recovery:   recovery,
		server:     server,
		websocket:  websocket,
		revocation: revocation,
	}
}

func (a *App) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("app: nil context")
	}
	if a == nil || a.server == nil {
		return errors.New("app: nil http server")
	}

	if err := a.bootstrap(ctx); err != nil {
		return err
	}

	a.startRevocationJanitor(ctx)

	errCh := make(chan error, 1)
	go func() {
		if a.log != nil {
			a.log.Info("http server starting", logkit.Fields{"addr": a.server.Addr})
		}
		err := a.server.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		errCh <- err
	}()

	signalCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	select {
	case err := <-errCh:
		if err != nil {
			return fmt.Errorf("App - Run - http server: %w", err)
		}
		return nil
	case <-signalCtx.Done():
		stop()
		if a.log != nil {
			a.log.Info("shutdown signal received")
		}
		if err := a.Shutdown(context.WithoutCancel(ctx)); err != nil {
			return err
		}
		if err := <-errCh; err != nil {
			return fmt.Errorf("App - Run - http server shutdown: %w", err)
		}
		return nil
	}
}

func (a *App) Shutdown(ctx context.Context) error {
	if ctx == nil {
		return errors.New("app: nil context")
	}

	timeout := defaultShutdownTimeout
	if a.cfg != nil && a.cfg.HTTP.ShutdownTimeout > 0 {
		timeout = a.cfg.HTTP.ShutdownTimeout
	}
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if a.websocket != nil {
		a.websocket.Shutdown(shutdownCtx)
	}
	if a.runtime != nil {
		a.runtime.Cancel()
	}
	if a.server == nil {
		return nil
	}
	if err := a.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("App - Shutdown - HTTPServer.Shutdown: %w", err)
	}
	if a.log != nil {
		a.log.Info("http server stopped")
	}
	return nil
}

// startRevocationJanitor launches a background goroutine that evicts
// expired entries from the JWT revocation store on a fixed interval. The
// goroutine exits cleanly when the supplied context is cancelled (the
// caller passes the runtime context so Shutdown stops it). Without this,
// an in-memory revocation store grows unbounded over time.
func (a *App) startRevocationJanitor(ctx context.Context) {
	if a == nil || a.revocation == nil || ctx == nil {
		return
	}
	go func(cleaner RevocationJanitor, log logkit.Logger) {
		ticker := time.NewTicker(revocationJanitorInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				if log != nil {
					log.Info("revocation janitor stopped")
				}
				return
			case <-ticker.C:
				cleaner.Cleanup()
			}
		}
	}(a.revocation, a.log)
}

func (a *App) bootstrap(ctx context.Context) error {
	if a.storage != nil {
		if err := a.storage.EnsureBucket(ctx); err != nil {
			return fmt.Errorf("App - bootstrap - BucketEnsurer.EnsureBucket: %w", err)
		}
	}
	if a.migrator != nil {
		if err := a.migrator.Up(ctx); err != nil {
			return fmt.Errorf("App - bootstrap - Migrator.Up: %w", err)
		}
	}
	if a.recovery != nil {
		if err := a.recovery.Recover(ctx); err != nil {
			return fmt.Errorf("App - bootstrap - StartupRecoverer.Recover: %w", err)
		}
	}
	return nil
}
