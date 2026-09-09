package bootstrap

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
)

const (
	defaultShutdownTimeout = 30 * time.Second
)

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

//nolint:gocyclo // One transactional workflow keeps ordering, rollback, and fail-closed branches explicit.
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
	workerErrors, err := a.workers.Start(ctx)
	if err != nil {
		shutdownErr := a.Shutdown(context.WithoutCancel(ctx))
		return errors.Join(
			fmt.Errorf("App - Run - start runtime workers: %w", err),
			wrapLifecycleError("App - Run - shutdown after runtime worker startup", shutdownErr),
		)
	}

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
	case err := <-workerErrors:
		shutdownErr := a.Shutdown(context.WithoutCancel(ctx))
		return errors.Join(
			wrapLifecycleError("App - Run - runtime worker", err),
			wrapLifecycleError("App - Run - shutdown after runtime worker exit", shutdownErr),
		)
	case err := <-errCh:
		shutdownErr := a.Shutdown(context.WithoutCancel(ctx))
		if err != nil || shutdownErr != nil {
			return errors.Join(
				wrapLifecycleError("App - Run - http server", err),
				wrapLifecycleError("App - Run - shutdown after http server exit", shutdownErr),
			)
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

func wrapLifecycleError(operation string, err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s: %w", operation, err)
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

	var shutdownErrors []error
	if a.server != nil {
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("App - Shutdown - HTTPServer.Shutdown: %w", err))
		}
	}
	if a.websocket != nil {
		a.websocket.Shutdown(shutdownCtx)
	}
	if a.runtime != nil {
		a.runtime.Cancel()
	}
	if err := a.workers.Wait(shutdownCtx); err != nil {
		shutdownErrors = append(shutdownErrors, fmt.Errorf("App - Shutdown - runtime workers: %w", err))
	}
	if a.log != nil {
		a.log.Info("http server stopped")
	}
	return errors.Join(shutdownErrors...)
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
	return nil
}
