package pause_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func TestPauseTimeArithmetic(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 5, 9, 0, 0, 0, time.UTC)
	deadline, ok := pausedomain.AddTime(now, time.Minute)
	require.True(t, ok)
	require.Equal(t, now.Add(time.Minute), deadline)
	require.True(t, pausedomain.TimeAtOrBefore(deadline, now))
	require.False(t, pausedomain.TimeAtOrBefore(now, deadline))

	_, ok = pausedomain.AddTime(now, 0)
	require.False(t, ok)
}

func TestPauseGameClockValidation(t *testing.T) {
	t.Parallel()

	frozenAt := time.Date(2026, time.September, 5, 9, 0, 0, 0, time.UTC)
	clock := pausedomain.PauseResumeGameClock{
		PauseID: uuid.New(), GameID: uuid.New(), OriginalDeadline: frozenAt.Add(time.Minute),
		FrozenAt: frozenAt, Remaining: time.Minute, Revision: 1,
	}
	require.NoError(t, clock.Validate(true))

	resumedAt := frozenAt.Add(10 * time.Minute)
	resumedDeadline := resumedAt.Add(clock.Remaining)
	clock.ResumedAt = &resumedAt
	clock.ResumedDeadline = &resumedDeadline
	require.NoError(t, clock.Validate(false))
	require.ErrorIs(t, clock.Validate(true), pausedomain.ErrInvalidGameClock)
}
