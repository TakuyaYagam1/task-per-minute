package game_test

import (
	"testing"
	"time"

	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
)

func newReconnectClock(t *testing.T, now time.Time) *gamemocks.MockReconnectClock {
	t.Helper()
	clock := gamemocks.NewMockReconnectClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}
