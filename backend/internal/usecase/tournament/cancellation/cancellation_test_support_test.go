package cancellation_test

import (
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	tournamentcancellation "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/cancellation/mocks"
)

func cancellationNewFixedTournamentClock(t *testing.T, now time.Time, calls int) *tournamentmocks.MockCancellationClock {
	t.Helper()
	clock := tournamentmocks.NewMockCancellationClock(t)
	if calls > 0 {
		clock.EXPECT().Now().Return(now).Times(calls)
	}
	return clock
}

func cancellationLifecycleTournamentRecord(
	id uuid.UUID,
	state domain.TournamentState,
	revision int64,
	now time.Time,
) tournamentcancellation.CancellationTournamentRecord {
	record := tournamentcancellation.CancellationTournamentRecord{
		ID: id, State: state, Revision: revision, UpdatedAt: now.Add(-time.Hour),
	}
	if state == domain.TournamentStateCompleted {
		finishedAt := now.Add(-time.Minute)
		record.FinishedAt = &finishedAt
	}
	if state == domain.TournamentStateTechnicalPause {
		origin := domain.TournamentStateSwiss
		record.PausedFromState = &origin
	}
	return record
}

func cancellationCloneTournamentRecord(record tournamentcancellation.CancellationTournamentRecord) *tournamentcancellation.CancellationTournamentRecord {
	cloned := record
	if record.PausedFromState != nil {
		state := *record.PausedFromState
		cloned.PausedFromState = &state
	}
	cloned.FinishedAt = cancellationCloneTestTimePointer(record.FinishedAt)
	return &cloned
}

func cancellationCloneTestTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
