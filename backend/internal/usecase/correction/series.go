package correction

import (
	"fmt"
	"sort"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
)

func findCorrectionSeriesGame(series domain.Series, gameID uuid.UUID) (domain.Game, bool) {
	for _, slot := range series.Slots {
		for _, game := range slot.Attempts {
			if game.ID == gameID {
				clone := game
				clone.WinnerID = cloneCorrectionUUIDPointer(game.WinnerID)
				clone.ResultRevisionID = cloneCorrectionOfficialResultRevisionIDPointer(game.ResultRevisionID)
				return clone, true
			}
		}
	}
	return domain.Game{}, false
}

func findCorrectionSeriesGamePointer(series *domain.Series, gameID uuid.UUID) (*domain.Game, bool) {
	for slotIndex := range series.Slots {
		for gameIndex := range series.Slots[slotIndex].Attempts {
			game := &series.Slots[slotIndex].Attempts[gameIndex]
			if game.ID == gameID {
				return game, true
			}
		}
	}
	return nil, false
}

func correctionSeriesScoreAttemptReferencesFromSeries(
	series domain.Series,
) ([]resultusecase.SeriesScoreAttemptReference, error) {
	slots := append([]domain.GameSlot(nil), series.Slots...)
	sort.Slice(slots, func(first, second int) bool {
		return slots[first].Position < slots[second].Position
	})
	references := make([]resultusecase.SeriesScoreAttemptReference, 0)
	for _, slot := range slots {
		for _, game := range slot.Attempts {
			if !game.State.IsTerminal() {
				continue
			}
			if game.ResultRevisionID == nil || game.ResultRevisionID.IsZero() {
				return nil, invalidCorrectionSeriesScoreRevision("terminal Game has no official result revision")
			}
			references = append(references, resultusecase.SeriesScoreAttemptReference{
				SlotID: slot.ID, SlotPosition: slot.Position, GameID: game.ID,
				AttemptNo: game.AttemptNo, State: game.State,
				WinnerID: cloneCorrectionUUIDPointer(game.WinnerID), Reason: game.ResultReason,
				CurrentGameResultRevisionID: *game.ResultRevisionID,
			})
		}
	}
	if err := validateCorrectionSeriesScoreAttemptReferences(
		references,
		series.FirstParticipantID,
		series.SecondParticipantID,
	); err != nil {
		return nil, err
	}
	return references, nil
}

func validateCorrectionSeriesScoreAttemptReferences(
	references []resultusecase.SeriesScoreAttemptReference,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
) error {
	games := make(map[uuid.UUID]struct{}, len(references))
	results := make(map[domain.OfficialResultRevisionID]struct{}, len(references))
	for index := range references {
		reference := references[index]
		if err := reference.Validate(); err != nil {
			return err
		}
		if reference.WinnerID != nil && *reference.WinnerID != firstParticipantID &&
			*reference.WinnerID != secondParticipantID {
			return invalidCorrectionSeriesScoreRevision("attempt winner is not a participant")
		}
		if _, exists := games[reference.GameID]; exists {
			return invalidCorrectionSeriesScoreRevision("duplicate Game attempt reference")
		}
		if _, exists := results[reference.CurrentGameResultRevisionID]; exists {
			return invalidCorrectionSeriesScoreRevision("duplicate official result reference")
		}
		if index > 0 && !correctionSeriesScoreAttemptFollows(references[index-1], reference) {
			return invalidCorrectionSeriesScoreRevision("terminal attempt references are not ordered")
		}
		games[reference.GameID] = struct{}{}
		results[reference.CurrentGameResultRevisionID] = struct{}{}
	}
	return nil
}

func correctionSeriesScoreAttemptFollows(
	previous resultusecase.SeriesScoreAttemptReference,
	next resultusecase.SeriesScoreAttemptReference,
) bool {
	if next.SlotPosition > previous.SlotPosition {
		return true
	}
	return next.SlotPosition == previous.SlotPosition && next.SlotID == previous.SlotID &&
		next.AttemptNo == previous.AttemptNo+1
}

func correctionScoreFromAttemptReferences(
	references []resultusecase.SeriesScoreAttemptReference,
	firstParticipantID uuid.UUID,
	secondParticipantID uuid.UUID,
	format domain.SeriesFormat,
) (domain.SeriesScore, error) {
	score := domain.SeriesScore{}
	for _, reference := range references {
		if reference.State != domain.GameStateCompleted || reference.WinnerID == nil {
			continue
		}
		switch *reference.WinnerID {
		case firstParticipantID:
			score.FirstParticipantWins++
		case secondParticipantID:
			score.SecondParticipantWins++
		default:
			return domain.SeriesScore{}, invalidCorrectionSeriesScoreRevision("attempt winner is not a participant")
		}
	}
	if err := score.Validate(format); err != nil {
		return domain.SeriesScore{}, invalidCorrectionSeriesScoreRevision("terminal attempts produce an invalid score")
	}
	return score, nil
}

func cloneCorrectionSeriesScoreAttemptReference(
	reference resultusecase.SeriesScoreAttemptReference,
) resultusecase.SeriesScoreAttemptReference {
	clone := reference
	clone.WinnerID = cloneCorrectionUUIDPointer(reference.WinnerID)
	return clone
}

func correctionSeriesScoreAttemptReferencesEqual(
	first []resultusecase.SeriesScoreAttemptReference,
	second []resultusecase.SeriesScoreAttemptReference,
) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if !correctionSeriesScoreAttemptReferenceEqual(first[index], second[index]) {
			return false
		}
	}
	return true
}

func correctionSeriesScoreAttemptReferenceEqual(
	first resultusecase.SeriesScoreAttemptReference,
	second resultusecase.SeriesScoreAttemptReference,
) bool {
	return first.SlotID == second.SlotID && first.SlotPosition == second.SlotPosition &&
		first.GameID == second.GameID && first.AttemptNo == second.AttemptNo &&
		first.State == second.State && correctionUUIDPointersEqual(first.WinnerID, second.WinnerID) &&
		first.Reason == second.Reason &&
		first.CurrentGameResultRevisionID == second.CurrentGameResultRevisionID
}

func correctionSeriesScoreAttemptReferencePointersEqual(
	first *resultusecase.SeriesScoreAttemptReference,
	second *resultusecase.SeriesScoreAttemptReference,
) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return correctionSeriesScoreAttemptReferenceEqual(*first, *second)
}

func persistedSeriesScoreHeadMatches(authority resultusecase.SeriesScoreRevisionAuthority) bool {
	head := authority.PersistedSeries.CurrentScoreRevisionID
	if authority.CurrentHead == nil {
		return head == nil
	}
	current := authority.CurrentHead
	if current.Validate() != nil || current.Scope != authority.Scope ||
		head == nil || *head != current.ID ||
		current.FirstParticipantID != authority.PersistedSeries.FirstParticipantID ||
		current.SecondParticipantID != authority.PersistedSeries.SecondParticipantID ||
		current.Format != authority.PersistedSeries.Format ||
		current.Score != authority.PersistedSeries.Score {
		return false
	}
	persistedAttempts, err := correctionSeriesScoreAttemptReferencesFromSeries(authority.PersistedSeries)
	return err == nil && correctionSeriesScoreAttemptReferencesEqual(current.Attempts, persistedAttempts)
}

func correctionSeriesScoreRevisionHeadsEqual(
	first resultusecase.SeriesScoreRevisionHead,
	second resultusecase.SeriesScoreRevisionHead,
) bool {
	return first.Scope == second.Scope && first.ID == second.ID &&
		correctionSeriesScoreRevisionIDPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.Ordinal == second.Ordinal && first.Operation == second.Operation &&
		first.CommandID == second.CommandID && correctionResultActorsEqual(first.Actor, second.Actor) &&
		correctionSeriesScoreAttemptReferencePointersEqual(first.CommandAttempt, second.CommandAttempt) &&
		first.FirstParticipantID == second.FirstParticipantID &&
		first.SecondParticipantID == second.SecondParticipantID && first.Format == second.Format &&
		first.Score == second.Score && correctionSeriesScoreAttemptReferencesEqual(first.Attempts, second.Attempts) &&
		correctionDerivedRevisionsEqual(first.SourceProjection, second.SourceProjection) &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func invalidCorrectionSeriesScoreRevision(message string) error {
	return fmt.Errorf("%w: %s", resultusecase.ErrInvalidSeriesScoreRevision, message)
}
