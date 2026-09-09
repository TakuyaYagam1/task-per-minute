package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
)

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

type App struct {
	cfg       *config.Config
	log       logkit.Logger
	runtime   *RuntimeContext
	storage   BucketEnsurer
	migrator  *Migrator
	server    *http.Server
	websocket WebSocketShutdowner
	workers   *runtimeWorkers
}

func NewApplication(
	cfg *config.Config,
	log logkit.Logger,
	runtime *RuntimeContext,
	storage BucketEnsurer,
	migrator *Migrator,
	server *http.Server,
	websocket WebSocketShutdowner,
	workers *runtimeWorkers,
) *App {
	return newApplication(
		cfg,
		log,
		runtime,
		storage,
		migrator,
		server,
		websocket,
		workers,
	)
}

func newApplication(
	cfg *config.Config,
	log logkit.Logger,
	runtime *RuntimeContext,
	storage BucketEnsurer,
	migrator *Migrator,
	server *http.Server,
	websocket WebSocketShutdowner,
	workers *runtimeWorkers,
) *App {
	return &App{
		cfg:       cfg,
		log:       log,
		runtime:   runtime,
		storage:   storage,
		migrator:  migrator,
		server:    server,
		websocket: websocket,
		workers:   workers,
	}
}
