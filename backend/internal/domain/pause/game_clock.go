package pause

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

var ErrInvalidGameClock = errors.New("invalid pause game clock")

type PauseResumeGameClock struct {
	PauseID          uuid.UUID
	GameID           uuid.UUID
	OriginalDeadline time.Time
	FrozenAt         time.Time
	Remaining        time.Duration
	ResumedAt        *time.Time
	ResumedDeadline  *time.Time
	Revision         int64
}

func (clock PauseResumeGameClock) Validate(active bool) error {
	if !validFrozenGameClock(clock) || !validResumedGameClock(clock, active) {
		return ErrInvalidGameClock
	}
	return nil
}

func validFrozenGameClock(clock PauseResumeGameClock) bool {
	return clock.PauseID != uuid.Nil && clock.GameID != uuid.Nil &&
		clock.Remaining > 0 && clock.Revision >= 1 &&
		!clock.OriginalDeadline.IsZero() && !clock.FrozenAt.IsZero() &&
		clock.OriginalDeadline.After(clock.FrozenAt) &&
		clock.OriginalDeadline.Sub(clock.FrozenAt) == clock.Remaining
}

func validResumedGameClock(clock PauseResumeGameClock, active bool) bool {
	if active {
		return clock.ResumedAt == nil && clock.ResumedDeadline == nil
	}
	return clock.ResumedAt != nil && clock.ResumedDeadline != nil &&
		!clock.ResumedAt.IsZero() && !clock.ResumedDeadline.IsZero() &&
		clock.ResumedDeadline.Sub(*clock.ResumedAt) == clock.Remaining
}
