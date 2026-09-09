// Package app owns the process boot boundary.
package app

import (
	"context"
	"fmt"
	"os"

	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/bootstrap"
)

// Run builds the dependency graph and serves the application until shutdown.
func Run(cfg *config.Config, log logkit.Logger) error {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	server, cleanup, err := bootstrap.Initialize(ctx, cfg, log)
	if err != nil {
		logError(log, err)
		return err
	}
	defer cleanup()

	if err := server.Run(ctx); err != nil {
		logError(log, err)
		return err
	}

	return nil
}

func logError(log logkit.Logger, err error) {
	if err == nil {
		return
	}
	if log != nil {
		log.WithError(err).Error("application stopped")
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "task-per-minute backend: %v\n", err)
}
