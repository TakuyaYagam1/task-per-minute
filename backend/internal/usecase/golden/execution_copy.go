package golden

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func CloneExecution(input domain.Wave) domain.Wave {
	clone := input
	clone.Members = append([]domain.WaveMember(nil), input.Members...)
	if input.ReadyWindow != nil {
		window := *input.ReadyWindow
		window.ConsumedAt = waveCloneTimePointer(input.ReadyWindow.ConsumedAt)
		clone.ReadyWindow = &window
	}
	clone.StartedAt = waveCloneTimePointer(input.StartedAt)
	clone.PausedAt = waveCloneTimePointer(input.PausedAt)
	return clone
}

func CloneExecutionExpectation(input GoldenWaveExecutionExpectation) GoldenWaveExecutionExpectation {
	clone := input
	clone.Source = CloneExpectation(input.Source)
	return clone
}

func waveCloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func waveCloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
