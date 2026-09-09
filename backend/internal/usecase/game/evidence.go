package game

import (
	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func validateForfeitGameEvidence(resolution ForfeitResolution) error {
	if resolution.Game == nil || resolution.GameRevision == nil {
		if !validForfeitWithoutGameEvidence(resolution) {
			return forfeitError("missing or partial Game forfeit evidence")
		}
		return nil
	}
	if !validForfeitCompletedGame(resolution) || !validForfeitGameRevision(resolution) {
		return forfeitError("invalid Game forfeit evidence")
	}
	return nil
}

func validForfeitWithoutGameEvidence(resolution ForfeitResolution) bool {
	if resolution.Game != nil || resolution.GameRevision != nil ||
		resolution.Source != SourceOperator {
		return false
	}
	game, _, found := currentForfeitGame(resolution.Series.Series)
	if !found {
		return resolution.ExpectedGame == nil
	}
	return resolution.ExpectedGame != nil &&
		(game.State == domain.GameStatePlanned || game.State == domain.GameStateReady) &&
		forfeitGameMatchesExpectation(game, *resolution.ExpectedGame)
}

func validForfeitCompletedGame(resolution ForfeitResolution) bool {
	game := resolution.Game
	revision := resolution.GameRevision
	return game != nil && revision != nil && game.Validate() == nil &&
		game.State == domain.GameStateCompleted && game.ResultReason == resolution.Reason &&
		game.WinnerID != nil && game.ResultRevisionID != nil &&
		resolution.Series.Series.WinnerID != nil && *game.WinnerID == *resolution.Series.Series.WinnerID &&
		*game.ResultRevisionID == revision.ID && *game.WinnerID == revision.WinnerID &&
		forfeitCompletedGameMatchesExpectation(*game, resolution.ExpectedGame)
}

func validForfeitGameRevision(resolution ForfeitResolution) bool {
	game := resolution.Game
	revision := resolution.GameRevision
	return game != nil && revision != nil && revision.Ordinal >= 1 && !revision.ID.IsZero() &&
		revision.GameID == game.ID && revision.Reason == resolution.Reason &&
		revision.RecordedAt.Equal(resolution.ResolvedAt)
}

func validateForfeitScoreRevision(resolution ForfeitResolution) error {
	revision := resolution.ScoreRevision
	series := resolution.Series.Series
	if !validForfeitScoreRevisionHeader(resolution, revision, series) {
		return forfeitError("invalid forfeit score revision")
	}
	if !forfeitScoreAddsWinner(revision, *series.WinnerID) {
		return forfeitError("forfeit score does not establish one winner")
	}
	if resolution.GameRevision != nil {
		if len(revision.GameResultRevisionIDs) == 0 ||
			revision.GameResultRevisionIDs[len(revision.GameResultRevisionIDs)-1] != resolution.GameRevision.ID ||
			revision.Ordinal != resolution.GameRevision.Ordinal+1 {
			return forfeitError("score revision does not follow Game evidence")
		}
	}
	return nil
}

func validForfeitScoreRevisionHeader(
	resolution ForfeitResolution,
	revision seriesdomain.ScoreRevision,
	series domain.Series,
) bool {
	return !revision.ID.IsZero() && revision.SeriesID == series.ID && revision.Ordinal >= 1 &&
		revision.FirstParticipantID == series.FirstParticipantID &&
		revision.SecondParticipantID == series.SecondParticipantID && revision.Format == series.Format &&
		revision.ScoreBefore.Validate(revision.Format) == nil && revision.ScoreAfter == series.Score &&
		revision.RecordedAt.Equal(resolution.ResolvedAt) && series.CurrentScoreRevisionID != nil &&
		*series.CurrentScoreRevisionID == revision.ID &&
		seriesdomain.ValidateGameResultRevisionIDs(revision.GameResultRevisionIDs) == nil
}

func validateForfeitSeriesRevision(resolution ForfeitResolution) error {
	revision := resolution.SeriesRevision
	series := resolution.Series.Series
	if revision.Ordinal < 1 || revision.ID.IsZero() ||
		revision.SeriesID != series.ID || revision.State != domain.SeriesStateCompleted ||
		revision.WinnerID == nil || series.WinnerID == nil || *revision.WinnerID != *series.WinnerID ||
		revision.ScoreRevisionID != resolution.ScoreRevision.ID || revision.Reason != resolution.Reason ||
		!revision.RecordedAt.Equal(resolution.ResolvedAt) || series.CurrentResultRevisionID == nil ||
		*series.CurrentResultRevisionID != revision.ID {
		return forfeitError("invalid forfeit Series revision")
	}
	if (revision.Ordinal == 1) != (revision.PreviousRevisionID == nil) {
		return forfeitError("invalid forfeit Series revision ordinal")
	}
	return nil
}

func validateForfeitOperatorEvidence(resolution ForfeitResolution) error {
	if resolution.Source == SourceSurrender {
		if resolution.OperatorEvidence != nil || resolution.ActorID != resolution.ForfeitingParticipantID ||
			resolution.ExpectedGame == nil {
			return forfeitError("surrender has operator evidence or a foreign actor")
		}
		return nil
	}
	if resolution.OperatorEvidence == nil || resolution.OperatorEvidence.Validate() != nil {
		return forfeitError("operator forfeit has invalid evidence")
	}
	return nil
}

func validateForfeitSettlementEvidence(resolution ForfeitResolution) error {
	evidence := resolution.Evidence
	identities := []uuid.UUID{
		resolution.CommandID,
		resolution.ScoreRevision.ID.UUID(),
		resolution.SeriesRevision.ID.UUID(),
		evidence.AuditEventID,
		evidence.OutboxEventID,
		evidence.ProjectionRevisionID,
	}
	if resolution.GameRevision != nil {
		identities = append(identities, resolution.GameRevision.ID.UUID())
	}
	if resolution.OperatorEvidence != nil {
		identities = append(identities, resolution.OperatorEvidence.EvidenceIDs...)
	}
	seen := make(map[uuid.UUID]struct{}, len(identities))
	for _, identity := range identities {
		if identity == uuid.Nil {
			return forfeitError("missing settlement evidence")
		}
		if _, duplicate := seen[identity]; duplicate {
			return forfeitError("duplicate settlement evidence")
		}
		seen[identity] = struct{}{}
	}
	if evidence.SourceProjectionRevision < 1 ||
		evidence.ProjectionRevision != evidence.SourceProjectionRevision+1 ||
		!evidence.RecordedAt.Equal(resolution.ResolvedAt) {
		return forfeitError("invalid projection evidence")
	}
	return nil
}

func forfeitParticipantsMatch(resolution ForfeitResolution) bool {
	series := resolution.Series.Series
	if series.WinnerID == nil || !seriesParticipant(series, resolution.ForfeitingParticipantID) ||
		!seriesParticipant(series, *series.WinnerID) ||
		*series.WinnerID == resolution.ForfeitingParticipantID {
		return false
	}
	return true
}

func forfeitScoreAddsWinner(revision seriesdomain.ScoreRevision, winnerID uuid.UUID) bool {
	if revision.ScoreAfter.Winner(
		revision.FirstParticipantID,
		revision.SecondParticipantID,
		revision.Format,
	) == nil {
		return false
	}
	switch winnerID {
	case revision.FirstParticipantID:
		return revision.ScoreAfter.FirstParticipantWins == revision.Format.WinsRequired() &&
			revision.ScoreAfter.FirstParticipantWins > revision.ScoreBefore.FirstParticipantWins &&
			revision.ScoreAfter.SecondParticipantWins == revision.ScoreBefore.SecondParticipantWins
	case revision.SecondParticipantID:
		return revision.ScoreAfter.SecondParticipantWins == revision.Format.WinsRequired() &&
			revision.ScoreAfter.SecondParticipantWins > revision.ScoreBefore.SecondParticipantWins &&
			revision.ScoreAfter.FirstParticipantWins == revision.ScoreBefore.FirstParticipantWins
	default:
		return false
	}
}

func forfeitWinningScore(series domain.Series, winnerID uuid.UUID) domain.SeriesScore {
	score := series.Score
	if winnerID == series.FirstParticipantID {
		score.FirstParticipantWins = series.Format.WinsRequired()
	} else {
		score.SecondParticipantWins = series.Format.WinsRequired()
	}
	return score
}

func liveForfeitSeriesState(series seriesdomain.Execution) bool {
	if series.Series.State == domain.SeriesStateActive {
		return true
	}
	return series.Series.State == domain.SeriesStateTechnicalPause &&
		series.ResumeState != nil && *series.ResumeState == domain.SeriesStateActive
}

func liveForfeitGameState(state domain.GameState) bool {
	return state == domain.GameStateActive || state == domain.GameStatePaused
}

func currentForfeitGame(series domain.Series) (domain.Game, int, bool) {
	if len(series.Slots) == 0 {
		return domain.Game{}, 0, false
	}
	slotIndex := len(series.Slots) - 1
	slot := series.Slots[slotIndex]
	if len(slot.Attempts) == 0 {
		return domain.Game{}, 0, false
	}
	return forfeitCloneGame(slot.Attempts[len(slot.Attempts)-1]), slotIndex, true
}

func forfeitGameMatchesExpectation(
	game domain.Game,
	expected GameExpectation,
) bool {
	return game.ID == expected.GameID && game.SlotID == expected.SlotID &&
		game.AttemptNo == expected.AttemptNo && game.State == expected.State
}

func seriesParticipant(series domain.Series, participantID uuid.UUID) bool {
	return participantID == series.FirstParticipantID || participantID == series.SecondParticipantID
}

func opposingSeriesParticipant(
	series domain.Series,
	participantID uuid.UUID,
) (uuid.UUID, bool) {
	switch participantID {
	case series.FirstParticipantID:
		return series.SecondParticipantID, true
	case series.SecondParticipantID:
		return series.FirstParticipantID, true
	default:
		return uuid.Nil, false
	}
}
