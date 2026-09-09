package game_test

import (
	"testing"
	"time"

	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/mocks"
)

func waveNewGameClock(t *testing.T, now time.Time) *gamemocks.MockWaveClock {
	t.Helper()

	clock := gamemocks.NewMockWaveClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}
