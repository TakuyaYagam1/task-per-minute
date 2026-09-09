package game

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var ErrInvalidStartedGame = errors.New("invalid started game")

func ValidateStarted(scope SubmissionScope, started Started) error {
	if !validStartedMetadata(scope, started) {
		return fmt.Errorf("%w: invalid authority metadata", ErrInvalidStartedGame)
	}
	if !validStartedSeries(scope, started) {
		return fmt.Errorf("%w: invalid series authority", ErrInvalidStartedGame)
	}
	if _, _, found := FindAttempt(started, scope.Game); !found {
		return fmt.Errorf("%w: game is not current", ErrInvalidStartedGame)
	}
	return nil
}

func validStartedMetadata(scope SubmissionScope, started Started) bool {
	deadline := started.StartedAt.Add(time.Duration(started.DeadlineSeconds) * time.Second)
	return scope.IsValid() && started.Scope == scope.Game && started.AssignmentID == scope.AssignmentID &&
		started.AssignmentRevision >= 1 && started.PlanRevisionID != uuid.Nil && started.SnapshotID != uuid.Nil &&
		started.ContentDigest != [sha256.Size]byte{} && started.DeadlineSeconds >= 1 && started.DeliveryEnabled &&
		domain.IsValidServerTime(started.StartedAt) && domain.IsValidServerTime(started.Deadline) &&
		started.Deadline.Equal(deadline)
}

func validStartedSeries(scope SubmissionScope, started Started) bool {
	if err := started.Series.Validate(); err != nil {
		return false
	}
	series := started.Series.Series
	return series.ID == scope.Game.SeriesID && series.TournamentID == scope.Game.TournamentID &&
		started.ParticipantIDs[0] == series.FirstParticipantID &&
		started.ParticipantIDs[1] == series.SecondParticipantID
}

func FindAttempt(started Started, scope Scope) (domain.Game, int, bool) {
	for slotIndex, slot := range started.Series.Series.Slots {
		if slot.ID != scope.SlotID || len(slot.Attempts) == 0 {
			continue
		}
		attempt := slot.Attempts[len(slot.Attempts)-1]
		if attempt.ID == scope.GameID {
			return attempt, slotIndex, true
		}
	}
	return domain.Game{}, 0, false
}

func StartedState(started Started) domain.GameState {
	game, _, found := FindAttempt(started, started.Scope)
	if !found {
		return ""
	}
	return game.State
}
