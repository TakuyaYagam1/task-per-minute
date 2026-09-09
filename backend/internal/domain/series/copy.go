package series

import (
	"errors"
	"slices"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func uuidPointersEqual(first, second *uuid.UUID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func seriesScoreRevisionPointersEqual(first, second *domain.SeriesScoreRevisionID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func officialResultRevisionPointersEqual(first, second *domain.OfficialResultRevisionID) bool {
	if first == nil || second == nil {
		return first == nil && second == nil
	}
	return *first == *second
}

func currentSeriesGame(series domain.Series) (domain.Game, bool) {
	if len(series.Slots) == 0 {
		return domain.Game{}, false
	}
	slot := series.Slots[len(series.Slots)-1]
	if len(slot.Attempts) == 0 {
		return domain.Game{}, false
	}
	return cloneGame(slot.Attempts[len(slot.Attempts)-1]), true
}

func validateCurrentGameResultRevisionIDs(revisionIDs []domain.OfficialResultRevisionID) error {
	seen := make(map[domain.OfficialResultRevisionID]struct{}, len(revisionIDs))
	for _, revisionID := range revisionIDs {
		if revisionID.IsZero() {
			return errors.New("empty retained Game result revision")
		}
		if _, duplicate := seen[revisionID]; duplicate {
			return errors.New("duplicate retained Game result revision")
		}
		seen[revisionID] = struct{}{}
	}
	return nil
}

func ValidateGameResultRevisionIDs(revisionIDs []domain.OfficialResultRevisionID) error {
	return validateCurrentGameResultRevisionIDs(revisionIDs)
}

func seriesExecutionsEqual(first, second Execution) bool {
	if first.ResumeState == nil || second.ResumeState == nil {
		if first.ResumeState != second.ResumeState {
			return false
		}
	} else if *first.ResumeState != *second.ResumeState {
		return false
	}
	if !seriesHeadersEqual(first.Series, second.Series) || len(first.Series.Slots) != len(second.Series.Slots) {
		return false
	}
	for index := range first.Series.Slots {
		if !gameSlotsEqual(first.Series.Slots[index], second.Series.Slots[index]) {
			return false
		}
	}
	return true
}

func ExecutionsEqual(first, second Execution) bool {
	return seriesExecutionsEqual(first, second)
}

func seriesHeadersEqual(first, second domain.Series) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID &&
		first.FirstParticipantID == second.FirstParticipantID &&
		first.SecondParticipantID == second.SecondParticipantID && first.Format == second.Format &&
		first.State == second.State && first.Score == second.Score &&
		uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		seriesScoreRevisionPointersEqual(first.CurrentScoreRevisionID, second.CurrentScoreRevisionID) &&
		officialResultRevisionPointersEqual(first.CurrentResultRevisionID, second.CurrentResultRevisionID)
}

func gameSlotsEqual(first, second domain.GameSlot) bool {
	if first.ID != second.ID || first.SeriesID != second.SeriesID || first.Position != second.Position ||
		first.Category != second.Category || first.ScoreBefore != second.ScoreBefore || len(first.Attempts) != len(second.Attempts) {
		return false
	}
	for index := range first.Attempts {
		if !settlementGamesEqual(first.Attempts[index], second.Attempts[index]) {
			return false
		}
	}
	return true
}

func settlementGamesEqual(first, second domain.Game) bool {
	return first.ID == second.ID && first.SlotID == second.SlotID && first.AttemptNo == second.AttemptNo &&
		first.State == second.State && first.ResultReason == second.ResultReason &&
		uuidPointersEqual(first.WinnerID, second.WinnerID) &&
		officialResultRevisionPointersEqual(first.ResultRevisionID, second.ResultRevisionID)
}

func scoreRevisionsEqual(first, second ScoreRevision) bool {
	return first.ID == second.ID && first.SeriesID == second.SeriesID &&
		first.FirstParticipantID == second.FirstParticipantID && first.SecondParticipantID == second.SecondParticipantID &&
		seriesScoreRevisionPointersEqual(first.PreviousRevisionID, second.PreviousRevisionID) &&
		first.Ordinal == second.Ordinal && first.Format == second.Format && first.ScoreBefore == second.ScoreBefore &&
		first.ScoreAfter == second.ScoreAfter && slices.Equal(first.GameResultRevisionIDs, second.GameResultRevisionIDs) &&
		first.RecordedAt.Equal(second.RecordedAt)
}

func ScoreRevisionsEqual(first, second ScoreRevision) bool {
	return scoreRevisionsEqual(first, second)
}

func cloneGameSlot(slot domain.GameSlot) domain.GameSlot {
	cloned := slot
	cloned.Attempts = make([]domain.Game, len(slot.Attempts))
	for index := range slot.Attempts {
		cloned.Attempts[index] = cloneGame(slot.Attempts[index])
	}
	return cloned
}

func cloneGame(game domain.Game) domain.Game {
	cloned := game
	cloned.WinnerID = cloneUUIDPointer(game.WinnerID)
	cloned.ResultRevisionID = cloneOfficialResultRevisionIDPointer(game.ResultRevisionID)
	return cloned
}

func cloneWaveExecution(wave domain.Wave) domain.Wave {
	cloned := wave
	cloned.Members = append([]domain.WaveMember(nil), wave.Members...)
	if wave.ReadyWindow != nil {
		window := *wave.ReadyWindow
		window.ConsumedAt = cloneTimePointer(wave.ReadyWindow.ConsumedAt)
		cloned.ReadyWindow = &window
	}
	cloned.StartedAt = cloneTimePointer(wave.StartedAt)
	cloned.PausedAt = cloneTimePointer(wave.PausedAt)
	return cloned
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
