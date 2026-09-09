package readiness_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	readinessmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/readiness/mocks"
)

func newFixedClock(t *testing.T, now time.Time, calls int) *readinessmocks.MockClock {
	t.Helper()
	clock := readinessmocks.NewMockClock(t)
	clock.EXPECT().Now().Return(now).Times(calls)
	return clock
}

func task036ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("36000000-0000-0000-0000-%012d", value))
}

func cloneTask036Wave(value domain.Wave) domain.Wave {
	clone := value
	clone.Members = append([]domain.WaveMember(nil), value.Members...)
	if value.ReadyWindow != nil {
		window := *value.ReadyWindow
		if value.ReadyWindow.ConsumedAt != nil {
			consumedAt := *value.ReadyWindow.ConsumedAt
			window.ConsumedAt = &consumedAt
		}
		clone.ReadyWindow = &window
	}
	if value.StartedAt != nil {
		startedAt := *value.StartedAt
		clone.StartedAt = &startedAt
	}
	if value.PausedAt != nil {
		pausedAt := *value.PausedAt
		clone.PausedAt = &pausedAt
	}
	return clone
}
