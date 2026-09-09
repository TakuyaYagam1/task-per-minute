package bootstrap

import (
	"context"
	"net/http"
	"time"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/websocket"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/objectstorage"
)

type clockFunc func() time.Time

func (f clockFunc) Now() time.Time {
	return f().UTC()
}

func provideRuntimeContext(runtime *RuntimeContext) context.Context {
	return runtime.Context()
}

func provideClock() clockFunc {
	return time.Now
}

func provideMigrator(cfg *config.Config) *Migrator {
	return NewMigrator(cfg.DB.DSN, ResolveMigrationsDir(migrationsDir))
}

func provideApplication(
	cfg *config.Config,
	log logkit.Logger,
	runtime *RuntimeContext,
	seaweed *objectstorage.SeaweedStorage,
	migrator *Migrator,
	server *http.Server,
	ws *websocket.Server,
	workers *runtimeWorkers,
) *App {
	return newApplication(cfg, log, runtime, seaweed, migrator, server, ws, workers)
}
