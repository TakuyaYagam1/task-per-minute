package redis

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/observability"
)

func TestRuntimeWorkerHeartbeatsFailClosedWithoutSharedStore(t *testing.T) {
	t.Parallel()

	store := NewRuntimeWorkerHeartbeats(nil)
	heartbeat := observability.RuntimeWorkerHeartbeat{
		InstanceID: uuid.MustParse("22000000-0000-4000-8000-000000000001"),
		Worker:     "event-delivery",
		ObservedAt: time.Date(2026, time.September, 7, 15, 0, 0, 0, time.UTC),
	}
	require.ErrorIs(t, store.ReportRuntimeWorkerHeartbeat(t.Context(), heartbeat), ErrNilRuntimeWorkerHeartbeatClient)
	_, err := store.RuntimeWorkerHeartbeatStatus(t.Context(), "event-delivery", heartbeat.ObservedAt)
	require.ErrorIs(t, err, ErrNilRuntimeWorkerHeartbeatClient)
}
