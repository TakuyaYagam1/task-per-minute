package v1

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestHealthCacheReuseAndExpiry(t *testing.T) {
	for _, degraded := range []bool{false, true} {
		name := "healthy"
		if degraded {
			name = "degraded"
		}
		t.Run(name, func(t *testing.T) {
			var calls [5]atomic.Int32
			checks := countedHealthChecks(&calls)
			var failed atomic.Bool
			failed.Store(degraded)
			checks.DB = HealthCheckerFunc(func(context.Context) error {
				calls[0].Add(1)
				if failed.Load() {
					return errors.New("database unavailable")
				}
				return nil
			})
			now := time.Date(2026, time.September, 7, 12, 0, 0, 0, time.UTC)
			server := New(Dependencies{Health: checks, Now: func() time.Time { return now }})
			first := cachedHealthResponse(context.Background(), server)
			expectedStatus := http.StatusOK
			if degraded {
				expectedStatus = http.StatusServiceUnavailable
			}
			require.Equal(t, expectedStatus, first.Code)
			failed.Store(!degraded)
			now = now.Add(time.Second - time.Nanosecond)
			second := cachedHealthResponse(context.Background(), server)
			require.Equal(t, first.Code, second.Code)
			require.Equal(t, first.Body.String(), second.Body.String())
			assertHealthCalls(t, &calls, 1)
			now = now.Add(time.Nanosecond)
			third := cachedHealthResponse(context.Background(), server)
			require.NotEqual(t, first.Code, third.Code)
			assertHealthCalls(t, &calls, 2)
		})
	}
}

func TestHealthCacheConcurrentRefresh(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls [5]atomic.Int32
		checks := countedHealthChecks(&calls)
		release := make(chan struct{})
		checks.DB = HealthCheckerFunc(func(context.Context) error {
			calls[0].Add(1)
			<-release
			return nil
		})
		server := New(Dependencies{Health: checks})
		responses := make(chan *httptest.ResponseRecorder, 12)
		for range cap(responses) {
			go func() { responses <- cachedHealthResponse(context.Background(), server) }()
		}
		synctest.Wait()
		observedCalls := calls[0].Load()
		close(release)
		first := <-responses
		require.Equal(t, http.StatusOK, first.Code)
		for range cap(responses) - 1 {
			next := <-responses
			require.Equal(t, first.Code, next.Code)
			require.Equal(t, first.Body.String(), next.Body.String())
		}
		require.Equal(t, int32(1), observedCalls)
		assertHealthCalls(t, &calls, 1)
	})
}

func TestHealthCacheWaiterCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls [5]atomic.Int32
		checks := countedHealthChecks(&calls)
		release := make(chan struct{})
		checks.DB = HealthCheckerFunc(func(ctx context.Context) error {
			calls[0].Add(1)
			select {
			case <-release:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		})
		server := New(Dependencies{Health: checks})
		owner := make(chan *httptest.ResponseRecorder, 1)
		go func() { owner <- cachedHealthResponse(context.Background(), server) }()
		synctest.Wait()
		ctx, cancel := context.WithCancel(context.Background())
		waiter := make(chan *httptest.ResponseRecorder, 1)
		go func() { waiter <- cachedHealthResponse(ctx, server) }()
		synctest.Wait()
		cancel()
		synctest.Wait()
		canceled := <-waiter
		close(release)
		completed := <-owner
		require.Equal(t, http.StatusServiceUnavailable, canceled.Code)
		body := decodeHealthResponse(t, canceled)
		require.Equal(t, api.HealthResponseStatusDegraded, body.Status)
		require.Equal(t, api.HealthResponseDbError, body.Db)
		require.Equal(t, api.DependencyStatusHealthFailed, body.TournamentRecovery.Health)
		require.Equal(t, http.StatusOK, completed.Code)
		reused := cachedHealthResponse(context.Background(), server)
		require.Equal(t, completed.Body.String(), reused.Body.String())
		assertHealthCalls(t, &calls, 1)
	})
}

func TestHealthRefreshDeadline(t *testing.T) {
	for _, timeout := range []time.Duration{0, 500 * time.Millisecond, 5 * time.Second} {
		t.Run(timeout.String(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx := context.Background()
				if timeout > 0 {
					var cancel context.CancelFunc
					ctx, cancel = context.WithTimeout(ctx, timeout)
					defer cancel()
				}
				var calls [5]atomic.Int32
				checks := countedHealthChecks(&calls)
				var deadline time.Time
				var hasDeadline bool
				var done <-chan struct{}
				checks.DB = HealthCheckerFunc(func(ctx context.Context) error {
					deadline, hasDeadline = ctx.Deadline()
					done = ctx.Done()
					return nil
				})
				server := New(Dependencies{Health: checks})
				require.Equal(t, http.StatusOK, cachedHealthResponse(ctx, server).Code)
				require.True(t, hasDeadline)
				expected := 2 * time.Second
				if timeout > 0 && timeout < expected {
					expected = timeout
				}
				require.Equal(t, time.Now().Add(expected), deadline)
				select {
				case <-done:
				default:
					t.Fatal("refresh context was not canceled")
				}
			})
		})
	}
}

func TestHealthRefreshTimeoutIsCached(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls [5]atomic.Int32
		checks := countedHealthChecks(&calls)
		checks.DB = HealthCheckerFunc(func(ctx context.Context) error {
			calls[0].Add(1)
			<-ctx.Done()
			return ctx.Err()
		})
		server := New(Dependencies{Health: checks})
		start := time.Now()
		first := cachedHealthResponse(context.Background(), server)
		require.Equal(t, 2*time.Second, time.Since(start))
		require.Equal(t, http.StatusServiceUnavailable, first.Code)
		second := cachedHealthResponse(context.Background(), server)
		require.Equal(t, first.Code, second.Code)
		require.Equal(t, first.Body.String(), second.Body.String())
		assertHealthCalls(t, &calls, 1)
	})
}

func TestHealthCacheCanceledRequestDoesNotRefresh(t *testing.T) {
	var calls [5]atomic.Int32
	server := New(Dependencies{Health: countedHealthChecks(&calls)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result := cachedHealthResponse(ctx, server)
	require.Equal(t, http.StatusServiceUnavailable, result.Code)
	assertHealthCalls(t, &calls, 0)
	require.Equal(t, http.StatusOK, cachedHealthResponse(context.Background(), server).Code)
	assertHealthCalls(t, &calls, 1)
}

func TestHealthRefreshPanicReleasesWaiters(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		var calls [5]atomic.Int32
		checks := countedHealthChecks(&calls)
		release := make(chan struct{})
		checks.DB = HealthCheckerFunc(func(context.Context) error {
			if calls[0].Add(1) == 1 {
				<-release
				panic("health dependency panic")
			}
			return nil
		})
		server := New(Dependencies{Health: checks})
		panicked := make(chan any, 1)
		go func() {
			defer func() { panicked <- recover() }()
			cachedHealthResponse(context.Background(), server)
		}()
		synctest.Wait()
		waiter := make(chan *httptest.ResponseRecorder, 1)
		go func() { waiter <- cachedHealthResponse(context.Background(), server) }()
		synctest.Wait()
		close(release)
		require.Equal(t, "health dependency panic", <-panicked)
		require.Equal(t, http.StatusServiceUnavailable, (<-waiter).Code)
		require.Equal(t, http.StatusOK, cachedHealthResponse(context.Background(), server).Code)
		require.Equal(t, int32(2), calls[0].Load())
	})
}

func countedHealthChecks(calls *[5]atomic.Int32) HealthChecks {
	checker := func(index int) HealthChecker {
		return HealthCheckerFunc(func(context.Context) error {
			calls[index].Add(1)
			return nil
		})
	}
	return HealthChecks{
		DB: checker(0), Redis: checker(1), SeaweedFS: checker(2),
		SchemaVersion: SchemaVersionReaderFunc(func(context.Context) (int64, error) {
			calls[3].Add(1)
			return 1, nil
		}),
		Tournament: observability.TournamentHealthSourceFunc(func(context.Context) observability.TournamentHealthSnapshot {
			calls[4].Add(1)
			return observability.HealthyTournamentHealthSnapshot()
		}),
	}
}

func cachedHealthResponse(ctx context.Context, server *Server) *httptest.ResponseRecorder {
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/health", nil)
	result := httptest.NewRecorder()
	server.HealthCheck(result, request)
	return result
}

func assertHealthCalls(t *testing.T, calls *[5]atomic.Int32, expected int32) {
	t.Helper()
	for i := range calls {
		require.Equal(t, expected, calls[i].Load(), "dependency %d", i)
	}
}
