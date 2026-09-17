package replay_test

import (
	"testing"
	"time"

	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/replay/mocks"
)

func newReplayFixedClock(t *testing.T, now time.Time) *gamemocks.MockReplayClock {
	t.Helper()
	clock := gamemocks.NewMockReplayClock(t)
	clock.EXPECT().Now().Return(now).Maybe()
	return clock
}
