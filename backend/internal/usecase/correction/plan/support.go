package plan

import (
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	cutoffusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/correction/cutoff"
	resultusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/result"
	resultprojection "github.com/TakuyaYagam1/task-per-minute/internal/usecase/resultprojection"
)

const (
	maxCorrectionCutoffEvents    = 4096
	maxCorrectionDAGProjections  = 512
	maxCorrectionDAGDependencies = 2048
	maxCorrectionDAGPayloadBytes = 512 << 10
)

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func timePointersEqual(first, second *time.Time) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return first.Equal(*second)
}

func validCorrectionServerTime(value time.Time) bool {
	return domain.IsValidServerTime(value)
}

func validCorrectionTime(value time.Time) bool {
	return validCorrectionServerTime(value) && value.Year() >= 2000 && value.Year() <= 2200
}

func canonicalCorrectionTime(value time.Time) string {
	return value.Format(time.RFC3339Nano)
}

func cloneSeriesScoreRevisionIDPointer(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneOfficialResultRevisionIDPointer(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneCorrectionDerivedRevision(value domain.DerivedRevision) domain.DerivedRevision {
	return value
}

func cloneCorrectionSeries(series domain.Series) domain.Series {
	clone := series
	clone.WinnerID = cloneUUIDPointer(series.WinnerID)
	clone.CurrentScoreRevisionID = cloneSeriesScoreRevisionIDPointer(series.CurrentScoreRevisionID)
	clone.CurrentResultRevisionID = cloneOfficialResultRevisionIDPointer(series.CurrentResultRevisionID)
	if series.Slots == nil {
		clone.Slots = nil
		return clone
	}
	clone.Slots = make([]domain.GameSlot, len(series.Slots))
	for slotIndex := range series.Slots {
		slot := series.Slots[slotIndex]
		clone.Slots[slotIndex] = slot
		if slot.Attempts == nil {
			clone.Slots[slotIndex].Attempts = nil
			continue
		}
		clone.Slots[slotIndex].Attempts = make([]domain.Game, len(slot.Attempts))
		for gameIndex := range slot.Attempts {
			game := slot.Attempts[gameIndex]
			game.WinnerID = cloneUUIDPointer(game.WinnerID)
			game.ResultRevisionID = cloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
			clone.Slots[slotIndex].Attempts[gameIndex] = game
		}
	}
	return clone
}

func derivedRevisionIDPointersEqual(first, second *domain.DerivedRevisionID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func correctionDerivedRevisionsEqual(first, second domain.DerivedRevision) bool {
	return first.ID() == second.ID() && first.TournamentID() == second.TournamentID() &&
		first.Artifact() == second.Artifact() && first.RevisionNo() == second.RevisionNo() &&
		derivedRevisionIDPointersEqual(first.PreviousRevisionID(), second.PreviousRevisionID()) &&
		first.CreatedAt().Equal(second.CreatedAt()) && first.PayloadDigest() == second.PayloadDigest()
}

func officialResultRevisionIDPointersEqual(first, second *domain.OfficialResultRevisionID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func seriesScoreRevisionIDPointersEqual(first, second *domain.SeriesScoreRevisionID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func officialResultOutcomesEqual(first, second resultusecase.OfficialResultOutcome) bool {
	return first.GameState == second.GameState && first.GameReason == second.GameReason &&
		first.SeriesState == second.SeriesState && first.SeriesReason == second.SeriesReason &&
		uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		seriesScoreRevisionIDPointersEqual(first.ScoreRevisionID, second.ScoreRevisionID)
}

func cloneRecordedProjectionDecisions(input []resultprojection.RecordedProjectionDecision) []resultprojection.RecordedProjectionDecision {
	clone := make([]resultprojection.RecordedProjectionDecision, len(input))
	for index := range input {
		clone[index] = input[index]
		clone[index].Payload = append([]byte(nil), input[index].Payload...)
	}
	return clone
}

func uuidPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func resultActorsEqual(first, second domain.ResultActor) bool {
	return first.Kind == second.Kind && uuidPointersEqual(first.PrincipalID, second.PrincipalID)
}

func cloneCorrectionUUIDPointer(value *uuid.UUID) *uuid.UUID { return cloneUUIDPointer(value) }

func cloneCorrectionSeriesScoreRevisionIDPointer(value *domain.SeriesScoreRevisionID) *domain.SeriesScoreRevisionID {
	return cloneSeriesScoreRevisionIDPointer(value)
}

func cloneCorrectionOfficialResultRevisionIDPointer(value *domain.OfficialResultRevisionID) *domain.OfficialResultRevisionID {
	return cloneOfficialResultRevisionIDPointer(value)
}

func correctionDerivedRevisionIDPointersEqual(first, second *domain.DerivedRevisionID) bool {
	return derivedRevisionIDPointersEqual(first, second)
}

func correctionOfficialResultRevisionIDPointersEqual(first, second *domain.OfficialResultRevisionID) bool {
	return officialResultRevisionIDPointersEqual(first, second)
}

func correctionSeriesScoreRevisionIDPointersEqual(first, second *domain.SeriesScoreRevisionID) bool {
	return seriesScoreRevisionIDPointersEqual(first, second)
}

func correctionOfficialResultOutcomesEqual(first, second resultusecase.OfficialResultOutcome) bool {
	return officialResultOutcomesEqual(first, second)
}

func cloneCorrectionRecordedProjectionDecisions(input []resultprojection.RecordedProjectionDecision) []resultprojection.RecordedProjectionDecision {
	return cloneRecordedProjectionDecisions(input)
}

func correctionUUIDPointersEqual(first, second *uuid.UUID) bool {
	return uuidPointersEqual(first, second)
}

func correctionResultActorsEqual(first, second domain.ResultActor) bool {
	return resultActorsEqual(first, second)
}

func validCorrectionCutoffKind(kind CutoffKind) bool {
	switch kind {
	case CutoffWaveStarted,
		CutoffTaskDelivered,
		CutoffNoShowRecorded,
		CutoffForfeitRecorded,
		CutoffGoldenAllocated:
		return true
	default:
		return false
	}
}

func preflightCorrectionDAGResults(dag resultprojection.RevisionDAG) error {
	return cutoffusecase.PreflightDAGResults(dag)
}

func preflightCorrectionCutoff(input CutoffInput, snapshot resultprojection.RevisionDAGSnapshot) error {
	return cutoffusecase.PreflightCutoff(input, snapshot)
}

func evaluateCorrectionCutoffPrepared(input CutoffInput, snapshot resultprojection.RevisionDAGSnapshot) (Cutoff, error) {
	return cutoffusecase.EvaluatePrepared(input, snapshot)
}

//nolint:gocyclo // The persisted aggregate and revision head are compared as one invariant.
func officialPersistedHeadMatches(authority resultusecase.OfficialResultRevisionAuthority) bool {
	var head *domain.OfficialResultRevisionID
	switch authority.Scope.Kind {
	case resultusecase.OfficialResultSubjectGame:
		game, found := findCorrectionSeriesGame(authority.PersistedSeries, authority.Scope.GameID)
		if !found {
			return false
		}
		head = cloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
	case resultusecase.OfficialResultSubjectSeries:
		head = cloneOfficialResultRevisionIDPointer(authority.PersistedSeries.CurrentResultRevisionID)
	default:
		return false
	}
	if authority.CurrentHead == nil {
		return head == nil
	}
	current := authority.CurrentHead
	if current.Validate() != nil || current.Scope != authority.Scope || head == nil || *head != current.ID {
		return false
	}
	outcome := current.Outcome
	switch current.Scope.Kind {
	case resultusecase.OfficialResultSubjectGame:
		game, found := findCorrectionSeriesGame(authority.PersistedSeries, current.Scope.GameID)
		return found && outcome.GameState == game.State && outcome.GameReason == game.ResultReason &&
			uuidPointersEqual(outcome.WinnerID, game.WinnerID)
	case resultusecase.OfficialResultSubjectSeries:
		return outcome.SeriesState == authority.PersistedSeries.State &&
			uuidPointersEqual(outcome.WinnerID, authority.PersistedSeries.WinnerID) &&
			seriesScoreRevisionIDPointersEqual(
				outcome.ScoreRevisionID,
				authority.PersistedSeries.CurrentScoreRevisionID,
			)
	default:
		return false
	}
}
