package noshow_test

import (
	"testing"
	"time"

	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/noshow/mocks"
)

func noShowNewGameClock(t *testing.T, now time.Time) *gamemocks.MockNoShowClock {
	t.Helper()

	clock := gamemocks.NewMockNoShowClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}
