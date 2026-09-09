package pause_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func TestReconnectLineageValidation(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 5, 9, 0, 0, 0, time.UTC)
	root := validReconnectInterval(now)
	require.NoError(t, root.Validate())
	require.NoError(t, pausedomain.ValidateReconnectLineage([]pausedomain.PauseReconnectInterval{root}))

	closedAt := now.Add(10 * time.Second)
	suspendingPauseID := uuid.New()
	root.State = pausedomain.ReconnectStateCancelled
	root.ClosedAt = &closedAt
	root.SuspendedByPauseID = &suspendingPauseID
	root.UpdatedAt = closedAt
	continuation := root
	continuation.ID = uuid.New()
	continuation.ContinuationNumber = 1
	continuation.ContinuedFromID = &root.ID
	continuation.SuspendedByPauseID = nil
	continuation.State = pausedomain.ReconnectStateOpen
	continuation.OpenedAt = closedAt.Add(time.Second)
	continuation.Deadline = continuation.OpenedAt.Add(root.Deadline.Sub(closedAt))
	continuation.ClosedAt = nil
	continuation.UpdatedAt = continuation.OpenedAt
	require.NoError(t, root.Validate())
	require.NoError(t, continuation.Validate())
	require.NoError(t, pausedomain.ValidateReconnectLineage([]pausedomain.PauseReconnectInterval{root, continuation}))

	fork := continuation
	fork.ID = uuid.New()
	require.ErrorIs(t, pausedomain.ValidateReconnectLineage([]pausedomain.PauseReconnectInterval{root, continuation, fork}), pausedomain.ErrInvalidReconnectLineage)
}

func TestReconnectCounterValidation(t *testing.T) {
	t.Parallel()

	counter := pausedomain.PauseReconnectCounter{
		PauseID: uuid.New(), RosterID: uuid.New(), ParticipantID: uuid.New(), Limit: 2, Used: 1, Revision: 1,
	}
	require.NoError(t, counter.Validate())
	counter.Used = 3
	require.ErrorIs(t, counter.Validate(), pausedomain.ErrInvalidReconnectCounter)
}

func validReconnectInterval(now time.Time) pausedomain.PauseReconnectInterval {
	return pausedomain.PauseReconnectInterval{
		ID: uuid.New(), PauseID: uuid.New(), RosterID: uuid.New(), SeriesID: uuid.New(), GameID: uuid.New(),
		ParticipantID: uuid.New(), PresenceEpoch: 1, Number: 1, State: pausedomain.ReconnectStateOpen,
		OpenedAt: now, Deadline: now.Add(time.Minute), Revision: 1, UpdatedAt: now,
	}
}
