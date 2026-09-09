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

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/config"
	bootstrapmocks "github.com/TakuyaYagam1/task-per-minute/internal/bootstrap/mocks"
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
			case "EnsureBucket", "Up":
				calls = append(calls, selector.Sel.Name)
			}
			return true
		})
	}

	require.Equal(t, []string{"EnsureBucket", "Up"}, calls)
}

func TestAppBootstrapShortCircuitsAndPropagatesStageErrors(t *testing.T) {
	t.Run("bucket", func(t *testing.T) {
		order := &lifecycleOrder{}
		stageErr := errors.New("bucket unavailable")
		app := &App{
			storage:  newRecordingBucket(t, order, stageErr),
			migrator: NewMigrator("://invalid", t.TempDir()),
		}

		err := app.bootstrap(t.Context())

		require.ErrorIs(t, err, stageErr)
		require.ErrorContains(t, err, "BucketEnsurer.EnsureBucket")
		require.Equal(t, []string{"bucket"}, order.snapshot())
	})

	t.Run("migrations", func(t *testing.T) {
		order := &lifecycleOrder{}
		app := &App{
			storage:  newRecordingBucket(t, order, nil),
			migrator: NewMigrator("://invalid", t.TempDir()),
		}

		err := app.bootstrap(t.Context())

		require.Error(t, err)
		require.ErrorContains(t, err, "Migrator.Up")
		require.Equal(t, []string{"bucket"}, order.snapshot())
	})
}

func TestAppBootstrapRunsAvailableStagesInOrder(t *testing.T) {
	t.Parallel()

	order := &lifecycleOrder{}
	app := &App{storage: newRecordingBucket(t, order, nil)}

	require.NoError(t, app.bootstrap(t.Context()))
	require.Equal(t, []string{"bucket"}, order.snapshot())
}

func TestAppShutdownStopsHTTPBeforeWebSocketAndRuntime(t *testing.T) {
	t.Parallel()

	order := &lifecycleOrder{}
	runtimeContext := NewRuntimeContext(t.Context())
	listener := newLifecycleListener(runtimeContext.Context(), order, nil)
	server, serveErr := startLifecycleHTTPServer(t, listener)
	websocket, runtimeCancelledAtCall := newRecordingWebSocketShutdowner(t, order, runtimeContext.Context().Err)
	app := &App{
		cfg:       lifecycleConfig(),
		runtime:   runtimeContext,
		server:    server,
		websocket: websocket,
	}

	require.NoError(t, app.Shutdown(context.Background()))
	require.False(t, runtimeCancelledAtCall())
	require.False(t, listener.runtimeCancelledAtClose())
	require.ErrorIs(t, runtimeContext.Context().Err(), context.Canceled)
	require.Equal(t, []string{"http", "websocket"}, order.snapshot())
	require.ErrorIs(t, receiveServeError(t, serveErr), http.ErrServerClosed)
}

func TestAppRunShutsDownRuntimeWhenHTTPServerFails(t *testing.T) {
	t.Parallel()

	order := &lifecycleOrder{}
	runtimeContext := NewRuntimeContext(context.Background())
	websocket, runtimeCancelledAtCall := newRecordingWebSocketShutdowner(t, order, runtimeContext.Context().Err)
	app := &App{
		cfg:       lifecycleConfig(),
		runtime:   runtimeContext,
		server:    &http.Server{Addr: "://", Handler: http.NotFoundHandler()},
		websocket: websocket,
	}

	err := app.Run(context.Background())

	require.Error(t, err)
	require.ErrorContains(t, err, "App - Run - http server")
	require.False(t, runtimeCancelledAtCall())
	require.ErrorIs(t, runtimeContext.Context().Err(), context.Canceled)
	require.Equal(t, []string{"websocket"}, order.snapshot())
}

func TestAppRunReturnsStartedWorkerPanic(t *testing.T) {
	workerStarted := make(chan struct{})
	triggerPanic := make(chan struct{})
	worker := bootstrapmocks.NewMockRuntimeWorker(t)
	worker.EXPECT().Run(mock.Anything).RunAndReturn(func(context.Context) error {
		close(workerStarted)
		<-triggerPanic
		panic("started worker panic")
	}).Once()
	workers, err := newRuntimeWorkers(namedRuntimeWorker{
		name: "panicking", worker: worker, ready: func() bool { return true },
	})
	require.NoError(t, err)

	listener, err := (&net.ListenConfig{}).Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	address := listener.Addr().String()
	require.NoError(t, listener.Close())
	runtimeContext := NewRuntimeContext(t.Context())
	app := &App{
		cfg:     lifecycleConfig(),
		runtime: runtimeContext,
		server:  &http.Server{Addr: address, Handler: http.NotFoundHandler()},
		workers: workers,
	}
	result := make(chan error, 1)
	go func() {
		result <- app.Run(t.Context())
	}()
	require.Eventually(t, channelClosed(workerStarted), time.Second, time.Millisecond)
	require.Eventually(t, func() bool {
		connection, dialErr := (&net.Dialer{Timeout: 10 * time.Millisecond}).DialContext(t.Context(), "tcp", address)
		if dialErr != nil {
			return false
		}
		return connection.Close() == nil
	}, time.Second, time.Millisecond)

	close(triggerPanic)
	err = <-result
	require.ErrorContains(t, err, "runtime worker panicking")
	require.ErrorContains(t, err, "panic")
	require.ErrorIs(t, runtimeContext.Context().Err(), context.Canceled)
}

func TestAppShutdownPropagatesHTTPErrorAfterEarlierStages(t *testing.T) {
	t.Parallel()

	order := &lifecycleOrder{}
	runtimeContext := NewRuntimeContext(t.Context())
	stageErr := errors.New("listener close failed")
	listener := newLifecycleListener(runtimeContext.Context(), order, stageErr)
	server, serveErr := startLifecycleHTTPServer(t, listener)
	websocket, runtimeCancelledAtCall := newRecordingWebSocketShutdowner(t, order, runtimeContext.Context().Err)
	app := &App{
		cfg:       lifecycleConfig(),
		runtime:   runtimeContext,
		server:    server,
		websocket: websocket,
	}

	err := app.Shutdown(context.Background())

	require.ErrorIs(t, err, stageErr)
	require.ErrorContains(t, err, "HTTPServer.Shutdown")
	require.False(t, runtimeCancelledAtCall())
	require.False(t, listener.runtimeCancelledAtClose())
	require.ErrorIs(t, runtimeContext.Context().Err(), context.Canceled)
	require.Equal(t, []string{"http", "websocket"}, order.snapshot())
	require.ErrorIs(t, receiveServeError(t, serveErr), http.ErrServerClosed)
}

func TestAppShutdownRejectsNilContextBeforeSideEffects(t *testing.T) {
	t.Parallel()

	order := &lifecycleOrder{}
	runtimeContext := NewRuntimeContext(t.Context())
	websocket, _ := newRecordingWebSocketShutdowner(t, order, runtimeContext.Context().Err)
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

func newRecordingBucket(t *testing.T, order *lifecycleOrder, err error) *bootstrapmocks.MockBucketEnsurer {
	t.Helper()
	bucket := bootstrapmocks.NewMockBucketEnsurer(t)
	bucket.EXPECT().
		EnsureBucket(mock.Anything).
		Run(func(context.Context) { order.add("bucket") }).
		Return(err).
		Once()
	return bucket
}

func newRecordingWebSocketShutdowner(
	t *testing.T,
	order *lifecycleOrder,
	runtimeError func() error,
) (*bootstrapmocks.MockWebSocketShutdowner, func() bool) {
	t.Helper()
	var mu sync.Mutex
	runtimeWasCancelled := false
	shutdowner := bootstrapmocks.NewMockWebSocketShutdowner(t)
	shutdowner.EXPECT().
		Shutdown(mock.Anything).
		Run(func(_ context.Context) {
			mu.Lock()
			runtimeWasCancelled = runtimeError() != nil
			mu.Unlock()
			order.add("websocket")
		}).
		Maybe()
	return shutdowner, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runtimeWasCancelled
	}
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
