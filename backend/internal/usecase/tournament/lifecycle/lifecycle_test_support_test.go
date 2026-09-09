package lifecycle_test

import (
	"testing"
	"time"

	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle/mocks"
)

func lifecycleNewFixedTournamentClock(t *testing.T, now time.Time, calls int) *tournamentmocks.MockLifecycleClock {
	t.Helper()
	clock := tournamentmocks.NewMockLifecycleClock(t)
	if calls > 0 {
		clock.EXPECT().Now().Return(now).Times(calls)
	}
	return clock
}

func lifecycleCloneTournamentRecord(record lifecycleusecase.LifecycleTournamentRecord) *lifecycleusecase.LifecycleTournamentRecord {
	cloned := record
	if record.PausedFromState != nil {
		state := *record.PausedFromState
		cloned.PausedFromState = &state
	}
	cloned.StartedAt = lifecycleCloneTestTimePointer(record.StartedAt)
	cloned.FinishedAt = lifecycleCloneTestTimePointer(record.FinishedAt)
	return &cloned
}
