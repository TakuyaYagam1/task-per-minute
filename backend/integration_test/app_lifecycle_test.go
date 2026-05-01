//go:build integration

package integration_test

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/app"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func TestAppLifecycle_StartsHealthAndStopsOnCancel(t *testing.T) {
	port := reservePort(t)
	setAppEnv(t, port)

	ctx, cancel := context.WithCancel(context.Background())
	cfg, err := config.Load()
	require.NoError(t, err)
	application, cleanup, err := app.Initialize(ctx, cfg, logkit.Noop())
	require.NoError(t, err)
	defer cleanup()

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run(ctx)
	}()

	waitForHealth(t, port, errCh)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("app did not stop within shutdown timeout")
	}
}

func TestAppLifecycle_StartupRecoveryFinishesActiveDuel(t *testing.T) {
	f := newDuelFixture()
	ctx := context.Background()

	alice := f.makePlayer(t, uniq("alice"))
	bob := f.makePlayer(t, uniq("bob"))
	duel, err := f.duels.Create(ctx, alice.ID, bob.ID, time.Now().Add(5*time.Minute).UTC())
	require.NoError(t, err)
	_, err = f.players.UpdateStatus(ctx, alice.ID, domain.PlayerStatusInDuel)
	require.NoError(t, err)
	_, err = f.players.UpdateStatus(ctx, bob.ID, domain.PlayerStatusInDuel)
	require.NoError(t, err)

	port := reservePort(t)
	setAppEnv(t, port)

	appCtx, cancel := context.WithCancel(context.Background())
	cfg, err := config.Load()
	require.NoError(t, err)
	application, cleanup, err := app.Initialize(appCtx, cfg, logkit.Noop())
	require.NoError(t, err)
	defer cleanup()

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run(appCtx)
	}()

	waitForHealth(t, port, errCh)

	recovered, err := f.duels.GetByID(ctx, duel.ID)
	require.NoError(t, err)
	require.Equal(t, domain.DuelStatusFinished, recovered.Status)
	require.Nil(t, recovered.WinnerID)
	require.NotNil(t, recovered.FinishedAt)

	alice, err = f.players.GetByID(ctx, alice.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, alice.Status)
	bob, err = f.players.GetByID(ctx, bob.ID)
	require.NoError(t, err)
	require.Equal(t, domain.PlayerStatusIdle, bob.Status)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("app did not stop within shutdown timeout")
	}
}

func setAppEnv(t *testing.T, port int) {
	t.Helper()
	redis := sharedRedis(t)
	seaweed := sharedSeaweed(t)

	t.Setenv("HTTP_HOST", "127.0.0.1")
	t.Setenv("HTTP_PORT", fmt.Sprintf("%d", port))
	t.Setenv("HTTP_READ_TIMEOUT", "5s")
	t.Setenv("HTTP_WRITE_TIMEOUT", "5s")
	t.Setenv("HTTP_SHUTDOWN_TIMEOUT", "2s")
	t.Setenv("DB_DSN", sharedPool.Config().ConnString())
	t.Setenv("DB_MAX_CONNS", "5")
	t.Setenv("REDIS_ADDR", redis.client.Options().Addr)
	t.Setenv("REDIS_PASSWORD", "")
	t.Setenv("REDIS_DB", "0")
	t.Setenv("SEAWEEDFS_ENDPOINT", seaweed.endpoint)
	t.Setenv("SEAWEEDFS_ACCESS_KEY", "tpm")
	t.Setenv("SEAWEEDFS_SECRET_KEY", "tpm-secret")
	t.Setenv("SEAWEEDFS_BUCKET", seaweed.bucket)
	t.Setenv("SEAWEEDFS_SECURE", "false")
	t.Setenv("JWT_SECRET", "01234567890123456789012345678901")
	t.Setenv("JWT_ACCESS_TTL", "15m")
	t.Setenv("JWT_REFRESH_TTL", "168h")
	t.Setenv("ADMIN_PASSWORD", "admin-password")
}

func reservePort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func waitForHealth(t *testing.T, port int, errCh <-chan error) {
	t.Helper()
	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-errCh:
			require.NoError(t, err)
			t.Fatal("app exited before health became ready")
		default:
		}

		reqCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, url, nil)
		require.NoError(t, err)
		resp, err := http.DefaultClient.Do(req)
		cancel()
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("health endpoint did not become ready at %s", url)
}
