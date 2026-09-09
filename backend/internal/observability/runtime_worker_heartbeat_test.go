package observability

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRuntimeWorkerHeartbeatValidatesPayloadFreeSharedIdentity(t *testing.T) {
	t.Parallel()

	heartbeat := RuntimeWorkerHeartbeat{
		InstanceID: uuid.MustParse("21000000-0000-4000-8000-000000000001"),
		Worker:     "event-delivery",
		ObservedAt: time.Date(2026, time.September, 7, 15, 0, 0, 0, time.UTC),
	}
	require.NoError(t, heartbeat.Validate())

	heartbeat.Worker = "event-delivery/private"
	require.Error(t, heartbeat.Validate())
}

func TestRuntimeWorkerHeartbeatStatusAggregatesReplicasWithoutInstanceData(t *testing.T) {
	t.Parallel()

	status := RuntimeWorkerHeartbeatStatus{Worker: "deadline-recovery", FreshInstances: 2}
	require.True(t, status.Healthy())
	require.Equal(t, "deadline-recovery:replicas:2", status.Revision())

	status.FreshInstances = 0
	require.False(t, status.Healthy())
	require.Equal(t, "deadline-recovery:replicas:0", status.Revision())
}
