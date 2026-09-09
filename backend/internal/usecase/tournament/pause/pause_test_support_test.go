package pause_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentpause "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/pause/mocks"
)

func pauseNewFixedTournamentClock(t *testing.T, now time.Time, calls int) *tournamentmocks.MockPauseClock {
	t.Helper()
	clock := tournamentmocks.NewMockPauseClock(t)
	if calls > 0 {
		clock.EXPECT().Now().Return(now).Times(calls)
	}
	return clock
}

func newDirectTournamentTransactionManager(
	t *testing.T,
	calls int,
) *tournamentmocks.MockPauseTransactionManager {
	t.Helper()
	transactions := tournamentmocks.NewMockPauseTransactionManager(t)
	if calls > 0 {
		transactions.EXPECT().
			Do(mock.Anything, mock.Anything).
			RunAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
				return fn(ctx)
			}).
			Times(calls)
	}
	return transactions
}

func pauseLifecycleTournamentRecord(
	id uuid.UUID,
	state domain.TournamentState,
	revision int64,
	now time.Time,
) tournamentpause.PauseTournamentRecord {
	record := tournamentpause.PauseTournamentRecord{
		ID: id, State: state, Revision: revision, UpdatedAt: now.Add(-time.Hour),
	}
	if state == domain.TournamentStateTechnicalPause {
		origin := domain.TournamentStateSwiss
		record.PausedFromState = &origin
	}
	return record
}

func pauseCloneTournamentRecord(record tournamentpause.PauseTournamentRecord) *tournamentpause.PauseTournamentRecord {
	cloned := record
	if record.PausedFromState != nil {
		state := *record.PausedFromState
		cloned.PausedFromState = &state
	}
	return &cloned
}
