package bootstrap

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	recoveryusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func TestAppBootstrapStageOrder(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "lifecycle.go", nil, 0)
	require.NoError(t, err)

	var calls []string
	for _, declaration := range file.Decls {
		function, ok := declaration.(*ast.FuncDecl)
		if !ok || function.Name.Name != "bootstrap" || function.Recv == nil {
			continue
		}
		ast.Inspect(function.Body, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			selector, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			switch selector.Sel.Name {
			case "EnsureBucket", "Up", "Recover":
				calls = append(calls, selector.Sel.Name)
			}
			return true
		})
	}

	require.Equal(t, []string{"EnsureBucket", "Up", "Recover"}, calls)
}

func TestAppBootstrapShortCircuitsAndPropagatesStageErrors(t *testing.T) {
	t.Run("bucket", func(t *testing.T) {
		order := &lifecycleOrder{}
		stageErr := errors.New("bucket unavailable")
		app := &App{
			storage:  &recordingBucket{order: order, err: stageErr},
			migrator: NewMigrator("://invalid", t.TempDir()),
			recovery: newRecordingRecoverer(order, errors.New("unexpected recovery")),
		}

		err := app.bootstrap(t.Context())

		require.ErrorIs(t, err, stageErr)
		require.ErrorContains(t, err, "BucketEnsurer.EnsureBucket")
		require.Equal(t, []string{"bucket"}, order.snapshot())
	})

	t.Run("migrations", func(t *testing.T) {
		order := &lifecycleOrder{}
		app := &App{
			storage:  &recordingBucket{order: order},
			migrator: NewMigrator("://invalid", t.TempDir()),
			recovery: newRecordingRecoverer(order, errors.New("unexpected recovery")),
		}

		err := app.bootstrap(t.Context())

		require.Error(t, err)
		require.ErrorContains(t, err, "Migrator.Up")
		require.Equal(t, []string{"bucket"}, order.snapshot(), "recovery must not run after migration failure")
	})

	t.Run("recovery", func(t *testing.T) {
		order := &lifecycleOrder{}
		stageErr := errors.New("recovery failed")
		app := &App{
			storage:  &recordingBucket{order: order},
			recovery: newRecordingRecoverer(order, stageErr),
		}

		err := app.bootstrap(t.Context())

		require.ErrorIs(t, err, stageErr)
		require.ErrorContains(t, err, "StartupRecoverer.Recover")
		require.Equal(t, []string{"bucket", "recovery"}, order.snapshot())
	})
}

func TestAppBootstrapRunsAvailableStagesInOrder(t *testing.T) {
	t.Parallel()

	order := &lifecycleOrder{}
	app := &App{
		storage:  &recordingBucket{order: order},
		recovery: newRecordingRecoverer(order, nil),
	}

	require.NoError(t, app.bootstrap(t.Context()))
	require.Equal(t, []string{"bucket", "recovery"}, order.snapshot())
}

func TestAppShutdownOrdersWebSocketRuntimeAndHTTP(t *testing.T) {
	t.Parallel()

	order := &lifecycleOrder{}
	runtimeContext := NewRuntimeContext(t.Context())
	listener := newLifecycleListener(runtimeContext.Context(), order, nil)
	server, serveErr := startLifecycleHTTPServer(t, listener)
	websocket := &recordingWebSocketShutdowner{order: order, runtime: runtimeContext.Context()}
	app := &App{
		cfg:       lifecycleConfig(),
		runtime:   runtimeContext,
		server:    server,
		websocket: websocket,
	}

	require.NoError(t, app.Shutdown(context.Background()))
	require.False(t, websocket.runtimeCancelledAtCall())
	require.True(t, listener.runtimeCancelledAtClose())
	require.Equal(t, []string{"websocket", "runtime", "http"}, order.snapshot())
	require.ErrorIs(t, receiveServeError(t, serveErr), http.ErrServerClosed)
}

func TestAppShutdownPropagatesHTTPErrorAfterEarlierStages(t *testing.T) {
	t.Parallel()

	order := &lifecycleOrder{}
	runtimeContext := NewRuntimeContext(t.Context())
	stageErr := errors.New("listener close failed")
	listener := newLifecycleListener(runtimeContext.Context(), order, stageErr)
	server, serveErr := startLifecycleHTTPServer(t, listener)
	websocket := &recordingWebSocketShutdowner{order: order, runtime: runtimeContext.Context()}
	app := &App{
		cfg:       lifecycleConfig(),
		runtime:   runtimeContext,
		server:    server,
		websocket: websocket,
	}

	err := app.Shutdown(context.Background())

	require.ErrorIs(t, err, stageErr)
	require.ErrorContains(t, err, "HTTPServer.Shutdown")
	require.False(t, websocket.runtimeCancelledAtCall())
	require.True(t, listener.runtimeCancelledAtClose())
	require.Equal(t, []string{"websocket", "runtime", "http"}, order.snapshot())
	require.ErrorIs(t, receiveServeError(t, serveErr), http.ErrServerClosed)
}

func TestAppShutdownRejectsNilContextBeforeSideEffects(t *testing.T) {
	t.Parallel()

	order := &lifecycleOrder{}
	runtimeContext := NewRuntimeContext(t.Context())
	websocket := &recordingWebSocketShutdowner{order: order, runtime: runtimeContext.Context()}
	app := &App{runtime: runtimeContext, websocket: websocket}

	err := app.Shutdown(nil) //nolint:staticcheck // Explicitly verifies the public nil-context rejection contract.

	require.EqualError(t, err, "app: nil context")
	require.NoError(t, runtimeContext.Context().Err())
	require.Empty(t, order.snapshot())
}

func TestRuntimeContextCancellation(t *testing.T) {
	t.Parallel()

	t.Run("explicit cancel is synchronous and idempotent", func(t *testing.T) {
		runtimeContext := NewRuntimeContext(context.Background())
		require.NoError(t, runtimeContext.Context().Err())

		runtimeContext.Cancel()
		require.ErrorIs(t, runtimeContext.Context().Err(), context.Canceled)

		runtimeContext.Cancel()
		require.ErrorIs(t, runtimeContext.Context().Err(), context.Canceled)
	})

	t.Run("parent cancellation propagates", func(t *testing.T) {
		parent, cancel := context.WithCancel(context.Background())
		runtimeContext := NewRuntimeContext(parent)

		cancel()

		require.ErrorIs(t, runtimeContext.Context().Err(), context.Canceled)
	})

	t.Run("nil receiver is safe", func(t *testing.T) {
		var runtimeContext *RuntimeContext

		require.NotNil(t, runtimeContext.Context())
		runtimeContext.Cancel()
	})
}

type lifecycleOrder struct {
	mu    sync.Mutex
	steps []string
}

func (o *lifecycleOrder) add(step string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.steps = append(o.steps, step)
}

func (o *lifecycleOrder) snapshot() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return append([]string(nil), o.steps...)
}

type recordingBucket struct {
	order *lifecycleOrder
	err   error
}

func (b *recordingBucket) EnsureBucket(context.Context) error {
	b.order.add("bucket")
	return b.err
}

type recordingQueueCleaner struct {
	order *lifecycleOrder
	err   error
}

func (c *recordingQueueCleaner) Clear(context.Context) error {
	c.order.add("recovery")
	return c.err
}

type emptyActiveDuelRepository struct{}

func (emptyActiveDuelRepository) ListActive(context.Context) ([]*domain.Duel, error) {
	return nil, nil
}

func (emptyActiveDuelRepository) Finish(
	context.Context,
	uuid.UUID,
	*uuid.UUID,
	time.Time,
	domain.DuelStatus,
) (*domain.Duel, error) {
	panic("unexpected Finish call")
}

type fixedLifecycleClock struct{}

func (fixedLifecycleClock) Now() time.Time {
	return time.Unix(0, 0).UTC()
}

func newRecordingRecoverer(order *lifecycleOrder, err error) *recoveryusecase.StartupRecoverer {
	return recoveryusecase.NewStartupRecoverer(
		nil,
		emptyActiveDuelRepository{},
		nil,
		nil,
		nil,
		&recordingQueueCleaner{order: order, err: err},
		nil,
		nil,
		nil,
		fixedLifecycleClock{},
		nil,
	)
}

type recordingWebSocketShutdowner struct {
	mu                  sync.Mutex
	order               *lifecycleOrder
	runtime             context.Context
	runtimeWasCancelled bool
}

func (s *recordingWebSocketShutdowner) Shutdown(context.Context) {
	s.mu.Lock()
	s.runtimeWasCancelled = s.runtime.Err() != nil
	s.mu.Unlock()
	s.order.add("websocket")
}

func (s *recordingWebSocketShutdowner) runtimeCancelledAtCall() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.runtimeWasCancelled
}

type lifecycleListener struct {
	order               *lifecycleOrder
	runtime             context.Context
	closeErr            error
	acceptCalled        chan struct{}
	closed              chan struct{}
	acceptOnce          sync.Once
	closeOnce           sync.Once
	mu                  sync.Mutex
	runtimeWasCancelled bool
}

func newLifecycleListener(runtimeContext context.Context, order *lifecycleOrder, closeErr error) *lifecycleListener {
	return &lifecycleListener{
		order:        order,
		runtime:      runtimeContext,
		closeErr:     closeErr,
		acceptCalled: make(chan struct{}),
		closed:       make(chan struct{}),
	}
}

func (l *lifecycleListener) Accept() (net.Conn, error) {
	l.acceptOnce.Do(func() { close(l.acceptCalled) })
	<-l.closed
	return nil, net.ErrClosed
}

func (l *lifecycleListener) Close() error {
	l.closeOnce.Do(func() {
		l.mu.Lock()
		l.runtimeWasCancelled = l.runtime.Err() != nil
		l.mu.Unlock()
		l.order.add("runtime")
		l.order.add("http")
		close(l.closed)
	})
	return l.closeErr
}

func (l *lifecycleListener) Addr() net.Addr {
	return lifecycleAddr("bootstrap-lifecycle")
}

func (l *lifecycleListener) runtimeCancelledAtClose() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.runtimeWasCancelled
}

type lifecycleAddr string

func (lifecycleAddr) Network() string { return "memory" }

func (a lifecycleAddr) String() string { return string(a) }

func startLifecycleHTTPServer(t *testing.T, listener *lifecycleListener) (*http.Server, <-chan error) {
	t.Helper()

	server := &http.Server{Handler: http.NotFoundHandler()}
	errCh := make(chan error, 1)
	go func() {
		errCh <- server.Serve(listener)
	}()
	t.Cleanup(func() { _ = listener.Close() })

	select {
	case <-listener.acceptCalled:
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not start accepting")
	}
	return server, errCh
}

func receiveServeError(t *testing.T, errCh <-chan error) error {
	t.Helper()
	select {
	case err := <-errCh:
		return err
	case <-time.After(time.Second):
		t.Fatal("HTTP server did not stop")
		return nil
	}
}

func lifecycleConfig() *config.Config {
	return &config.Config{HTTP: config.HTTP{ShutdownTimeout: time.Second}}
}
