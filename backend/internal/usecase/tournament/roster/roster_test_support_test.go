package roster_test

import (
	"testing"
	"time"

	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster/mocks"
)

func rosterNewFixedTournamentClock(t *testing.T, now time.Time, calls int) *tournamentmocks.MockRosterClock {
	t.Helper()
	clock := tournamentmocks.NewMockRosterClock(t)
	if calls > 0 {
		clock.EXPECT().Now().Return(now).Times(calls)
	}
	return clock
}
