package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
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

// Initialize creates the application and all of its dependencies. The
// returned cleanup function cancels runtime-owned work before closing the
// outbound clients produced by Wire.
func Initialize(ctx context.Context, cfg *config.Config, log logkit.Logger) (*App, func(), error) {
	if ctx == nil {
		return nil, nil, errors.New("app: nil context")
	}
	if cfg == nil {
		return nil, nil, errors.New("app: nil config")
	}

	runtime := NewRuntimeContext(ctx)
	application, cleanup, err := initializeApp(runtime, cfg, log) //nolint:contextcheck // RuntimeContext carries the cancellable child of ctx into Wire.
	if err != nil {
		runtime.Cancel()
		return nil, nil, fmt.Errorf("initialize app: %w", err)
	}

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

type App struct {
	cfg        *config.Config
	log        logkit.Logger
	runtime    *RuntimeContext
	storage    BucketEnsurer
	migrator   *Migrator
	recovery   *recovery.StartupRecoverer
	arena      *arenaCore
	server     *http.Server
	websocket  WebSocketShutdowner
	revocation RevocationJanitor
}

func NewApplication(
	cfg *config.Config,
	log logkit.Logger,
	runtime *RuntimeContext,
	storage BucketEnsurer,
	migrator *Migrator,
	recovery *recovery.StartupRecoverer,
	server *http.Server,
	websocket WebSocketShutdowner,
	revocation RevocationJanitor,
) *App {
	return newArenaApplication(
		cfg,
		log,
		runtime,
		storage,
		migrator,
		recovery,
		nil,
		server,
		websocket,
		revocation,
	)
}

func newArenaApplication(
	cfg *config.Config,
	log logkit.Logger,
	runtime *RuntimeContext,
	storage BucketEnsurer,
	migrator *Migrator,
	recovery *recovery.StartupRecoverer,
	arena *arenaCore,
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
		arena:      arena,
		server:     server,
		websocket:  websocket,
		revocation: revocation,
	}
}
