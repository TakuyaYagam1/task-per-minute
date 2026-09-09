//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	logkit "github.com/wahrwelt-kit/go-logkit"

	"github.com/TakuyaYagam1/task-per-minute/config"
	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	"github.com/TakuyaYagam1/task-per-minute/internal/bootstrap"
)

func TestBootstrapInitializeReportsSharedRuntimeHealth(t *testing.T) {
	port := reservePort(t)
	setAppEnv(t, port)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cfg, err := config.Load()
	require.NoError(t, err)
	application, cleanup, err := bootstrap.Initialize(ctx, cfg, logkit.Noop())
	require.NoError(t, err)
	defer cleanup()

	errCh := make(chan error, 1)
	go func() {
		errCh <- application.Run(ctx)
	}()

	waitForHealth(t, port, errCh)

	requestCtx, requestCancel := context.WithTimeout(context.Background(), time.Second)
	defer requestCancel()
	request, err := http.NewRequestWithContext(
		requestCtx,
		http.MethodGet,
		fmt.Sprintf("http://127.0.0.1:%d/health", port),
		nil,
	)
	require.NoError(t, err)
	response, err := http.DefaultClient.Do(request)
	require.NoError(t, err)
	defer response.Body.Close()
	require.Equal(t, http.StatusOK, response.StatusCode)

	var health api.HealthResponse
	require.NoError(t, json.NewDecoder(response.Body).Decode(&health))
	require.Equal(t, api.HealthResponseStatusOk, health.Status)
	require.Equal(t, api.HealthResponseDbOk, health.Db)
	require.Equal(t, api.HealthResponseRedisOk, health.Redis)
	require.Equal(t, api.HealthResponseSeaweedfsOk, health.Seaweedfs)
	require.Positive(t, health.SchemaVersion)
	require.Equal(t, api.DependencyStatusHealthHealthy, health.TournamentAuthority.Health)
	require.Equal(t, api.DependencyStatusReadinessReady, health.TournamentAuthority.Readiness)
	require.Equal(t, api.DependencyStatusHealthHealthy, health.TournamentSubmission.Health)
	require.Equal(t, api.DependencyStatusReadinessReady, health.TournamentSubmission.Readiness)

	cancel()
	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(5 * time.Second):
		t.Fatal("app did not stop within shutdown timeout")
	}
}
