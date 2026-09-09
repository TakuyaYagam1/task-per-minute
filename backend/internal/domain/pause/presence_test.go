package pause_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func TestPausePresenceValidation(t *testing.T) {
	t.Parallel()

	connectedAt := time.Date(2026, time.September, 5, 9, 0, 0, 0, time.UTC)
	presence := pausedomain.PausePresence{
		ID: uuid.New(), TournamentID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New(),
		ParticipantID: uuid.New(), State: pausedomain.PresenceStateConnected, PresenceEpoch: 1,
		Revision: 1, ConnectedAt: connectedAt, UpdatedAt: connectedAt,
	}
	require.NoError(t, presence.Validate())
	require.True(t, pausedomain.TimeCoversPresenceHistory(connectedAt, presence))

	disconnectedAt := connectedAt.Add(time.Minute)
	presence.State = pausedomain.PresenceStateDisconnected
	presence.DisconnectedAt = &disconnectedAt
	presence.UpdatedAt = disconnectedAt
	require.NoError(t, presence.Validate())
	require.False(t, pausedomain.TimeCoversPresenceHistory(connectedAt, presence))
	require.True(t, pausedomain.TimeCoversPresenceHistory(disconnectedAt, presence))
}
