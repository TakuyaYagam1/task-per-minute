package execution

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	tournamentadmin "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/admin/execution"
)

func TestWaveControlOutboxIDsAreDeterministicAndDistinct(t *testing.T) {
	t.Parallel()

	commandID := uuid.New()
	eventID := tournamentAdminExecutionID(commandID, "wave-control-outbox-event")
	retryEventID := tournamentAdminExecutionID(commandID, "wave-control-outbox-event")
	idempotencyKey := tournamentAdminExecutionID(commandID, "wave-control-outbox-idempotency")

	require.Equal(t, eventID, retryEventID)
	require.NotEqual(t, uuid.Nil, eventID)
	require.NotEqual(t, eventID, idempotencyKey)
}

func TestWaveControlOutboxPayloadMatchesExactControl(t *testing.T) {
	t.Parallel()

	waveID := uuid.New()
	payload := []byte(`{"schema":"wave-control-changed-v1","action":"pause","wave_id":"` + waveID.String() + `"}`)

	require.True(t, waveControlOutboxPayloadMatches(payload, tournamentadmin.WaveActionPause, waveID))
	require.False(t, waveControlOutboxPayloadMatches(payload, tournamentadmin.WaveActionResume, waveID))
	require.False(t, waveControlOutboxPayloadMatches(payload, tournamentadmin.WaveActionPause, uuid.New()))
	require.False(t, waveControlOutboxPayloadMatches([]byte(`{"action":"pause"}`), tournamentadmin.WaveActionPause, waveID))
}
