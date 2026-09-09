//go:build integration

package integration_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestRuntimeWorkerHealthAggregatesReplicasAndRecovers(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	client := sharedRedis(t).client
	worker := "event-delivery-" + uuid.NewString()
	otherWorker := "deadline-recovery-" + uuid.NewString()
	t.Cleanup(func() {
		_ = client.Del(context.Background(), "runtime-worker-heartbeat:"+worker).Err()
		_ = client.Del(context.Background(), "runtime-worker-heartbeat:"+otherWorker).Err()
	})

	firstReplica := uuid.MustParse("23000000-0000-4000-8000-000000000101")
	secondReplica := uuid.MustParse("23000000-0000-4000-8000-000000000102")
	storeOne := redisadapter.NewRuntimeWorkerHeartbeats(client)
	storeTwo := redisadapter.NewRuntimeWorkerHeartbeats(client)
	now := time.Now().UTC().Truncate(time.Millisecond)
	report := func(store *redisadapter.RuntimeWorkerHeartbeats, instanceID uuid.UUID, worker string, observedAt time.Time) {
		t.Helper()
		require.NoError(t, store.ReportRuntimeWorkerHeartbeat(ctx, observability.RuntimeWorkerHeartbeat{
			InstanceID: instanceID,
			Worker:     worker,
			ObservedAt: observedAt,
		}))
	}

	report(storeOne, firstReplica, worker, now)
	report(storeTwo, secondReplica, worker, now)
	status, err := storeOne.RuntimeWorkerHeartbeatStatus(ctx, worker, now.Add(-time.Millisecond))
	require.NoError(t, err)
	require.Equal(t, observability.RuntimeWorkerHeartbeatStatus{Worker: worker, FreshInstances: 2}, status)
	require.True(t, status.Healthy())

	encoded, err := json.Marshal(status)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), firstReplica.String())
	require.NotContains(t, string(encoded), secondReplica.String())
	require.NotContains(t, string(encoded), "observed_at")

	staleAfter := now.Add(time.Millisecond)
	stale, err := storeOne.RuntimeWorkerHeartbeatStatus(ctx, worker, staleAfter)
	require.NoError(t, err)
	require.Equal(t, 0, stale.FreshInstances)

	report(storeOne, firstReplica, otherWorker, staleAfter.Add(time.Millisecond))
	isolation, err := storeTwo.RuntimeWorkerHeartbeatStatus(ctx, worker, staleAfter)
	require.NoError(t, err)
	require.Equal(t, 0, isolation.FreshInstances)

	report(storeOne, firstReplica, worker, staleAfter.Add(time.Millisecond))
	partialRecovery, err := storeTwo.RuntimeWorkerHeartbeatStatus(ctx, worker, staleAfter)
	require.NoError(t, err)
	require.Equal(t, 1, partialRecovery.FreshInstances)

	report(storeTwo, secondReplica, worker, staleAfter.Add(time.Millisecond))
	recovered, err := storeOne.RuntimeWorkerHeartbeatStatus(ctx, worker, staleAfter)
	require.NoError(t, err)
	require.Equal(t, 2, recovered.FreshInstances)
}
