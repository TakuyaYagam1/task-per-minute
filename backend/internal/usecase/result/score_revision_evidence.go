package result

import (
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func newSeriesScoreRevision(
	scope SeriesScoreRevisionScope,
	id domain.SeriesScoreRevisionID,
	previousRevisionID *domain.SeriesScoreRevisionID,
	ordinal int,
	operation SeriesScoreRevisionOperation,
	commandID uuid.UUID,
	actor domain.ResultActor,
	commandAttempt *SeriesScoreAttemptReference,
	series domain.Series,
	attempts []SeriesScoreAttemptReference,
	sourceProjection domain.DerivedRevision,
	recordedAt time.Time,
) SeriesScoreRevision {
	return SeriesScoreRevision{
		scope:               scope,
		id:                  id,
		previousRevisionID:  cloneSeriesScoreRevisionIDPointer(previousRevisionID),
		ordinal:             ordinal,
		operation:           operation,
		commandID:           commandID,
		actor:               cloneResultActor(actor),
		commandAttempt:      cloneSeriesScoreAttemptReferencePointer(commandAttempt),
		firstParticipantID:  series.FirstParticipantID,
		secondParticipantID: series.SecondParticipantID,
		format:              series.Format,
		score:               series.Score,
		attempts:            cloneSeriesScoreAttemptReferences(attempts),
		sourceProjection:    cloneDerivedRevision(sourceProjection),
		recordedAt:          recordedAt,
	}
}

func newSeriesScoreRevisionCondition(
	authority SeriesScoreRevisionAuthority,
) SeriesScoreRevisionCondition {
	return SeriesScoreRevisionCondition{
		scope:                    authority.Scope,
		expectedSeries:           cloneRevisionSeries(authority.PersistedSeries),
		expectedCurrentHead:      cloneSeriesScoreRevisionHeadPointer(authority.CurrentHead),
		expectedSourceProjection: cloneDerivedRevision(authority.SourceProjection),
		expectedSeriesRevision:   authority.SeriesRevision,
		expectedAttemptRevision:  authority.AttemptRevision,
	}
}

func seriesScoreAttemptReferencesFromSeries(
	series domain.Series,
) ([]SeriesScoreAttemptReference, error) {
	slots := append([]domain.GameSlot(nil), series.Slots...)
	sort.Slice(slots, func(first, second int) bool {
		return slots[first].Position < slots[second].Position
	})
	references := make([]SeriesScoreAttemptReference, 0)
	for _, slot := range slots {
		for _, game := range slot.Attempts {
			if !game.State.IsTerminal() {
				continue
			}
			if game.ResultRevisionID == nil || game.ResultRevisionID.IsZero() {
				return nil, invalidSeriesScoreRevision("terminal Game has no official result revision")
			}
			references = append(references, SeriesScoreAttemptReference{
				SlotID:                      slot.ID,
				SlotPosition:                slot.Position,
				GameID:                      game.ID,
				AttemptNo:                   game.AttemptNo,
				State:                       game.State,
				WinnerID:                    cloneUUIDPointer(game.WinnerID),
				Reason:                      game.ResultReason,
				CurrentGameResultRevisionID: *game.ResultRevisionID,
			})
		}
	}
	if err := validateSeriesScoreAttemptReferences(
		references,
		series.FirstParticipantID,
		series.SecondParticipantID,
	); err != nil {
		return nil, err
	}
	return references, nil
}

// BuildSeriesScoreAttemptReferences derives the ordered terminal-attempt
// evidence consumed by result coordinators.
func BuildSeriesScoreAttemptReferences(
	series domain.Series,
) ([]SeriesScoreAttemptReference, error) {
	return seriesScoreAttemptReferencesFromSeries(series)
}

func validateSeriesScoreAttemptReferences(
	references []SeriesScoreAttemptReference,
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
			return invalidSeriesScoreRevision("attempt winner is not a participant")
		}
		if _, exists := games[reference.GameID]; exists {
			return invalidSeriesScoreRevision("duplicate Game attempt reference")
		}
		if _, exists := results[reference.CurrentGameResultRevisionID]; exists {
			return invalidSeriesScoreRevision("duplicate official result reference")
		}
		if index > 0 && !seriesScoreAttemptFollows(references[index-1], reference) {
			return invalidSeriesScoreRevision("terminal attempt references are not ordered")
		}
		games[reference.GameID] = struct{}{}
		results[reference.CurrentGameResultRevisionID] = struct{}{}
	}
	return nil
}

func seriesScoreAttemptFollows(
	previous SeriesScoreAttemptReference,
	next SeriesScoreAttemptReference,
) bool {
	if next.SlotPosition > previous.SlotPosition {
		return true
	}
	return next.SlotPosition == previous.SlotPosition && next.SlotID == previous.SlotID &&
		next.AttemptNo == previous.AttemptNo+1
}

func scoreFromAttemptReferences(
	references []SeriesScoreAttemptReference,
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
			return domain.SeriesScore{}, invalidSeriesScoreRevision("attempt winner is not a participant")
		}
	}
	if err := score.Validate(format); err != nil {
		return domain.SeriesScore{}, invalidSeriesScoreRevision("terminal attempts produce an invalid score")
	}
	return score, nil
}

func sameSeriesScoreAttemptPosition(
	first SeriesScoreAttemptReference,
	second SeriesScoreAttemptReference,
) bool {
	return first.SlotID == second.SlotID && first.SlotPosition == second.SlotPosition &&
		first.GameID == second.GameID && first.AttemptNo == second.AttemptNo
}

func containsSeriesScoreAttempt(
	references []SeriesScoreAttemptReference,
	want SeriesScoreAttemptReference,
) bool {
	for _, reference := range references {
		if seriesScoreAttemptReferenceEqual(reference, want) {
			return true
		}
	}
	return false
}
