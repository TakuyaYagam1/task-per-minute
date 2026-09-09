package pause

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidReconnectInterval = errors.New("invalid reconnect interval")
	ErrInvalidReconnectCounter  = errors.New("invalid reconnect counter")
	ErrInvalidReconnectLineage  = errors.New("invalid reconnect lineage")
)

type ReconnectState string

const (
	ReconnectStateOpen        ReconnectState = "open"
	ReconnectStateReconnected ReconnectState = "reconnected"
	ReconnectStateExpired     ReconnectState = "expired"
	ReconnectStateCancelled   ReconnectState = "cancelled"
)

type PauseReconnectInterval struct {
	ID                 uuid.UUID
	PauseID            uuid.UUID
	RosterID           uuid.UUID
	SeriesID           uuid.UUID
	GameID             uuid.UUID
	ParticipantID      uuid.UUID
	PresenceEpoch      int64
	Number             int
	ContinuationNumber int
	ContinuedFromID    *uuid.UUID
	SuspendedByPauseID *uuid.UUID
	State              ReconnectState
	OpenedAt           time.Time
	Deadline           time.Time
	ClosedAt           *time.Time
	Revision           int64
	UpdatedAt          time.Time
}

func (interval PauseReconnectInterval) Validate() error {
	if !validReconnectIdentity(interval) || !validReconnectTimeline(interval) {
		return invalidReconnectInterval("invalid identity or interval")
	}
	if err := validateReconnectState(interval); err != nil {
		return err
	}
	return validateReconnectSuspension(interval)
}

func validReconnectIdentity(interval PauseReconnectInterval) bool {
	return interval.ID != uuid.Nil && interval.PauseID != uuid.Nil &&
		interval.RosterID != uuid.Nil && interval.SeriesID != uuid.Nil &&
		interval.GameID != uuid.Nil && interval.ParticipantID != uuid.Nil &&
		interval.PresenceEpoch >= 1 && interval.Number >= 1 &&
		interval.ContinuationNumber >= 0 && interval.Revision >= 1
}

func validReconnectTimeline(interval PauseReconnectInterval) bool {
	return domain.IsValidServerTime(interval.OpenedAt) &&
		domain.IsValidServerTime(interval.Deadline) &&
		domain.IsValidServerTime(interval.UpdatedAt) &&
		interval.Deadline.After(interval.OpenedAt) &&
		!interval.UpdatedAt.Before(interval.OpenedAt)
}

func validateReconnectState(interval PauseReconnectInterval) error {
	switch interval.State {
	case ReconnectStateOpen:
		if interval.ClosedAt != nil || interval.SuspendedByPauseID != nil {
			return invalidReconnectInterval("open interval has close time")
		}
	case ReconnectStateReconnected, ReconnectStateExpired, ReconnectStateCancelled:
		if interval.ClosedAt == nil || !domain.IsValidServerTime(*interval.ClosedAt) ||
			interval.ClosedAt.Before(interval.OpenedAt) || interval.UpdatedAt.Before(*interval.ClosedAt) {
			return invalidReconnectInterval("terminal interval lacks close time")
		}
	default:
		return invalidReconnectInterval("unknown state %q", interval.State)
	}
	return nil
}

func validateReconnectSuspension(interval PauseReconnectInterval) error {
	if interval.SuspendedByPauseID != nil {
		if *interval.SuspendedByPauseID == uuid.Nil || interval.State != ReconnectStateCancelled {
			return invalidReconnectInterval("invalid pause suspension")
		}
	}
	return nil
}

type PauseReconnectCounter struct {
	PauseID       uuid.UUID
	RosterID      uuid.UUID
	ParticipantID uuid.UUID
	Limit         int
	Used          int
	Revision      int64
}

func (counter PauseReconnectCounter) Validate() error {
	if counter.PauseID == uuid.Nil || counter.RosterID == uuid.Nil || counter.ParticipantID == uuid.Nil ||
		counter.Limit < 1 || counter.Used < 0 || counter.Used > counter.Limit || counter.Revision < 1 {
		return ErrInvalidReconnectCounter
	}
	return nil
}

func ValidateReconnectLineage(intervals []PauseReconnectInterval) error {
	byID := make(map[uuid.UUID]PauseReconnectInterval, len(intervals))
	for _, interval := range intervals {
		if _, duplicate := byID[interval.ID]; duplicate {
			return invalidReconnectLineage("duplicate interval")
		}
		byID[interval.ID] = interval
	}
	continued := make(map[uuid.UUID]uuid.UUID, len(intervals))
	for _, interval := range intervals {
		if interval.ContinuationNumber == 0 {
			if interval.ContinuedFromID != nil {
				return invalidReconnectLineage("root interval has predecessor")
			}
			continue
		}
		if err := validateReconnectContinuation(interval, byID, continued); err != nil {
			return err
		}
	}
	return nil
}

func validateReconnectContinuation(interval PauseReconnectInterval, byID map[uuid.UUID]PauseReconnectInterval, continued map[uuid.UUID]uuid.UUID) error {
	if interval.ContinuedFromID == nil || *interval.ContinuedFromID == interval.ID {
		return invalidReconnectLineage("continuation lacks predecessor")
	}
	predecessorID := *interval.ContinuedFromID
	if _, fork := continued[predecessorID]; fork {
		return invalidReconnectLineage("predecessor has multiple continuations")
	}
	continued[predecessorID] = interval.ID
	predecessor, exists := byID[predecessorID]
	if !exists || predecessor.State != ReconnectStateCancelled || predecessor.SuspendedByPauseID == nil ||
		*predecessor.SuspendedByPauseID == uuid.Nil || predecessor.ClosedAt == nil ||
		!sameReconnectLineage(predecessor, interval) || !validReconnectContinuationTime(predecessor, interval) {
		return invalidReconnectLineage("invalid continuation")
	}
	return nil
}

func sameReconnectLineage(predecessor, interval PauseReconnectInterval) bool {
	return predecessor.PauseID == interval.PauseID && predecessor.RosterID == interval.RosterID &&
		predecessor.SeriesID == interval.SeriesID && predecessor.GameID == interval.GameID &&
		predecessor.ParticipantID == interval.ParticipantID && predecessor.PresenceEpoch == interval.PresenceEpoch &&
		predecessor.Number == interval.Number && predecessor.ContinuationNumber+1 == interval.ContinuationNumber
}

func validReconnectContinuationTime(predecessor, interval PauseReconnectInterval) bool {
	if predecessor.ClosedAt == nil || !predecessor.Deadline.After(*predecessor.ClosedAt) ||
		!interval.OpenedAt.After(*predecessor.ClosedAt) {
		return false
	}
	deadline, ok := AddTime(interval.OpenedAt, predecessor.Deadline.Sub(*predecessor.ClosedAt))
	return ok && interval.Deadline.Equal(deadline)
}

func TimeCoversReconnectHistory(at time.Time, interval PauseReconnectInterval) bool {
	if at.Before(interval.OpenedAt) || at.Before(interval.UpdatedAt) {
		return false
	}
	return interval.ClosedAt == nil || !at.Before(*interval.ClosedAt)
}

func invalidReconnectInterval(message string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReconnectInterval, fmt.Sprintf(message, arguments...))
}

func invalidReconnectLineage(message string) error {
	return fmt.Errorf("%w: %s", ErrInvalidReconnectLineage, message)
}
