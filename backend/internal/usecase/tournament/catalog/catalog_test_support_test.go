package catalog_test

import (
	"testing"
	"time"

	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/catalog/mocks"
)

func catalogNewFixedTournamentClock(t *testing.T, now time.Time, calls int) *tournamentmocks.MockCatalogClock {
	t.Helper()
	clock := tournamentmocks.NewMockCatalogClock(t)
	if calls > 0 {
		clock.EXPECT().Now().Return(now).Times(calls)
	}
	return clock
}
