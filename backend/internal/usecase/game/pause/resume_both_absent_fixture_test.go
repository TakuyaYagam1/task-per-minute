package pause_test

import (
	"testing"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func pauseChildRevisionContains(values []gameusecase.PauseChildRevision, id uuid.UUID) bool {
	for _, value := range values {
		if value.ID == id {
			return true
		}
	}
	return false
}

func reconnectIntervalPointerByID(tb testing.TB, values []pausedomain.PauseReconnectInterval, id uuid.UUID) *pausedomain.PauseReconnectInterval {
	tb.Helper()
	for index := range values {
		if values[index].ID == id {
			return &values[index]
		}
	}
	tb.Fatalf("Reconnect %s not found", id)
	return nil
}

func reversePauseResumeEvidence(authority *gameusecase.PauseResumePresenceAuthority) {
	for left, right := 0, len(authority.Resume.Presence)-1; left < right; left, right = left+1, right-1 {
		authority.Resume.Presence[left], authority.Resume.Presence[right] = authority.Resume.Presence[right], authority.Resume.Presence[left]
	}
	for left, right := 0, len(authority.Resume.Reconnect)-1; left < right; left, right = left+1, right-1 {
		authority.Resume.Reconnect[left], authority.Resume.Reconnect[right] = authority.Resume.Reconnect[right], authority.Resume.Reconnect[left]
	}
	for left, right := 0, len(authority.Resume.Counters)-1; left < right; left, right = left+1, right-1 {
		authority.Resume.Counters[left], authority.Resume.Counters[right] = authority.Resume.Counters[right], authority.Resume.Counters[left]
	}
}
