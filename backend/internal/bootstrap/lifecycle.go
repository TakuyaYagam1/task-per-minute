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
	defaultShutdownTimeout    = 30 * time.Second
	revocationJanitorInterval = 5 * time.Minute
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

// RevocationJanitor is implemented by stores that need periodic eviction of
// expired entries (in-memory) and is a no-op for stores with native TTL
// (Redis). The App runs Cleanup on a ticker bound to the runtime context.
type RevocationJanitor interface {
	Cleanup()
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

	a.startRevocationJanitor(a.runtime.Context()) //nolint:contextcheck // The janitor must stop on runtime cancellation during shutdown.

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
