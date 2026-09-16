package reconnect

import (
	"reflect"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func validateReconnectTerminalOutcome(authority ReconnectAuthority, outcome TerminalOutcome) error {
	if err := validateReconnectTerminalHeader(authority, outcome); err != nil {
		return err
	}
	winnerRoute := outcome.GameResultRevision != nil && outcome.VoidGameResultRevision == nil && outcome.ReplayRoute == nil
	replayRoute := outcome.GameResultRevision == nil && outcome.VoidGameResultRevision != nil && outcome.SeriesResultRevision == nil && outcome.ReplayRoute != nil
	if !winnerRoute && !replayRoute {
		return reconnectError("ambiguous terminal route")
	}
	if winnerRoute {
		if err := validateReconnectWinnerOutcome(authority, outcome); err != nil {
			return err
		}
	} else if err := validateReconnectReplayOutcome(authority, outcome); err != nil {
		return err
	}
	if err := validateReconnectTerminalLineage(authority, outcome); err != nil {
		return err
	}
	return validateReconnectTerminalIdentities(authority, outcome)
}

func validateReconnectTerminalHeader(authority ReconnectAuthority, outcome TerminalOutcome) error {
	if !validReconnectTerminalEvidenceHeader(authority, outcome) || !validReconnectTerminalScoreHeader(authority, outcome.ScoreRevision) ||
		outcome.ScoreRevision.ScoreBefore.Validate(outcome.ScoreRevision.Format) != nil ||
		outcome.ScoreRevision.ScoreAfter.Validate(outcome.ScoreRevision.Format) != nil {
		return reconnectError("invalid terminal outcome header")
	}
	if outcome.ScoreRevision.PreviousRevisionID != nil &&
		(outcome.ScoreRevision.PreviousRevisionID.IsZero() || *outcome.ScoreRevision.PreviousRevisionID == outcome.ScoreRevision.ID) {
		return reconnectError("invalid score previous revision")
	}
	return nil
}

func validReconnectTerminalEvidenceHeader(authority ReconnectAuthority, outcome TerminalOutcome) bool {
	return reconnectValidServerTime(outcome.TerminalizedAt) && outcome.Evidence.ProjectionRevision == authority.CurrentProjectionRevision &&
		outcome.Evidence.SourceProjectionRevision+1 == outcome.Evidence.ProjectionRevision && outcome.Evidence.RecordedAt.Equal(outcome.TerminalizedAt)
}

func validReconnectTerminalScoreHeader(authority ReconnectAuthority, score seriesdomain.ScoreRevision) bool {
	return !score.ID.IsZero() && reflect.DeepEqual(score.GameResultRevisionIDs, authority.CurrentGameResultRevisionIDs) &&
		score.SeriesID == authority.Series.ID && score.FirstParticipantID == authority.Series.FirstParticipantID &&
		score.SecondParticipantID == authority.Series.SecondParticipantID && score.Format == authority.Series.Format &&
		score.ScoreAfter == authority.Series.Score && authority.Series.CurrentScoreRevisionID != nil &&
		*authority.Series.CurrentScoreRevisionID == score.ID && len(score.GameResultRevisionIDs) > 0
}

func validateReconnectWinnerOutcome(authority ReconnectAuthority, outcome TerminalOutcome) error {
	if !validReconnectWinnerGame(authority, outcome) {
		return reconnectError("invalid winner terminal route")
	}
	if !validReconnectWinnerScoreDelta(authority, outcome.ScoreRevision) {
		return reconnectError("winner score delta is invalid")
	}
	if authority.Series.State == domain.SeriesStateCompleted {
		return validateReconnectCompletedSeries(authority, outcome)
	}
	if outcome.SeriesResultRevision != nil || authority.Series.CurrentResultRevisionID != nil || authority.CurrentOrdinal != outcome.ScoreRevision.Ordinal {
		return reconnectError("ongoing Series carries terminal revision")
	}
	return nil
}

func validReconnectWinnerGame(authority ReconnectAuthority, outcome TerminalOutcome) bool {
	return authority.Game.State == domain.GameStateCompleted && authority.Game.ResultReason == domain.GameResultReasonOperatorForfeit &&
		authority.Game.WinnerID != nil && outcome.GameResultRevision.ID == *authority.Game.ResultRevisionID &&
		outcome.GameResultRevision.GameID == authority.Game.ID && outcome.GameResultRevision.WinnerID == *authority.Game.WinnerID &&
		outcome.GameResultRevision.Reason == domain.GameResultReasonOperatorForfeit &&
		outcome.GameResultRevision.Ordinal+1 == outcome.ScoreRevision.Ordinal &&
		outcome.GameResultRevision.RecordedAt.Equal(outcome.TerminalizedAt)
}

func validReconnectWinnerScoreDelta(authority ReconnectAuthority, score seriesdomain.ScoreRevision) bool {
	firstDelta := score.ScoreAfter.FirstParticipantWins - score.ScoreBefore.FirstParticipantWins
	secondDelta := score.ScoreAfter.SecondParticipantWins - score.ScoreBefore.SecondParticipantWins
	switch *authority.Game.WinnerID {
	case authority.Series.FirstParticipantID:
		return firstDelta == 1 && secondDelta == 0
	case authority.Series.SecondParticipantID:
		return firstDelta == 0 && secondDelta == 1
	default:
		return false
	}
}

func validateReconnectCompletedSeries(authority ReconnectAuthority, outcome TerminalOutcome) error {
	if !validReconnectCompletedSeries(authority, outcome) {
		return reconnectError("invalid completed Series result")
	}
	previous := outcome.SeriesResultRevision.PreviousRevisionID
	if previous != nil && (previous.IsZero() || *previous == outcome.SeriesResultRevision.ID) {
		return reconnectError("invalid Series previous revision")
	}
	return nil
}

func validReconnectCompletedSeries(authority ReconnectAuthority, outcome TerminalOutcome) bool {
	result := outcome.SeriesResultRevision
	return result != nil && result.Ordinal == outcome.ScoreRevision.Ordinal+1 && result.State == domain.SeriesStateCompleted &&
		authority.Series.CurrentResultRevisionID != nil && *authority.Series.CurrentResultRevisionID == result.ID &&
		result.ID != outcome.GameResultRevision.ID && result.SeriesID == authority.Series.ID && result.WinnerID != nil &&
		authority.Series.WinnerID != nil && *result.WinnerID == *authority.Series.WinnerID &&
		result.ScoreRevisionID == outcome.ScoreRevision.ID && result.Reason == domain.GameResultReasonOperatorForfeit &&
		result.RecordedAt.Equal(outcome.TerminalizedAt) && authority.CurrentOrdinal == result.Ordinal
}

func validateReconnectReplayOutcome(authority ReconnectAuthority, outcome TerminalOutcome) error {
	if !validReconnectVoidGame(authority, outcome) || !validReconnectReplaySeries(authority, outcome) {
		return reconnectError("invalid replay terminal route")
	}
	slot, ok := reconnectCurrentGameSlot(authority.Series, authority.Game.ID)
	if !ok || outcome.ReplayRoute.SlotID != slot.ID || outcome.ReplayRoute.Category != slot.Category {
		return reconnectError("replay route changed Game category")
	}
	return nil
}

func validReconnectVoidGame(authority ReconnectAuthority, outcome TerminalOutcome) bool {
	result := outcome.VoidGameResultRevision
	return authority.Game.State == domain.GameStateVoid && authority.Game.ResultReason == domain.GameResultReasonDisconnect &&
		authority.Game.WinnerID == nil && result.ID == *authority.Game.ResultRevisionID && result.GameID == authority.Game.ID &&
		result.Reason == domain.GameResultReasonDisconnect && result.RecordedAt.Equal(outcome.TerminalizedAt) &&
		result.Ordinal+1 == outcome.ScoreRevision.Ordinal
}

func validReconnectReplaySeries(authority ReconnectAuthority, outcome TerminalOutcome) bool {
	return authority.Series.State == domain.SeriesStateReplayRequired && outcome.ScoreRevision.ScoreBefore == outcome.ScoreRevision.ScoreAfter &&
		authority.CurrentOrdinal == outcome.ScoreRevision.Ordinal && outcome.ReplayRoute.WaveID == authority.Scope.WaveID &&
		outcome.ReplayRoute.SeriesID == authority.Series.ID && outcome.ReplayRoute.GameID == authority.Game.ID &&
		outcome.ReplayRoute.RoutedAt.Equal(outcome.TerminalizedAt)
}

func validateReconnectTerminalLineage(authority ReconnectAuthority, outcome TerminalOutcome) error {
	lastResultID := authority.CurrentGameResultRevisionIDs[len(authority.CurrentGameResultRevisionIDs)-1]
	if authority.Game.ResultRevisionID == nil || *authority.Game.ResultRevisionID != lastResultID ||
		!outcome.ScoreRevision.RecordedAt.Equal(outcome.TerminalizedAt) {
		return reconnectError("terminal result lineage is not current")
	}
	return nil
}

func validateReconnectTerminalIdentities(authority ReconnectAuthority, outcome TerminalOutcome) error {
	lastResultID := authority.CurrentGameResultRevisionIDs[len(authority.CurrentGameResultRevisionIDs)-1]
	identities := []uuid.UUID{lastResultID.UUID(), outcome.ScoreRevision.ID.UUID(), outcome.Evidence.AuditEventID,
		outcome.Evidence.OutboxEventID, outcome.Evidence.ProjectionRevisionID}
	if outcome.SeriesResultRevision != nil {
		identities = append(identities, outcome.SeriesResultRevision.ID.UUID())
	}
	if outcome.ReplayRoute != nil {
		identities = append(identities, outcome.ReplayRoute.ID)
	}
	if !reconnectUniqueNonZeroUUIDs(identities) {
		return reconnectError("terminal evidence identities collide")
	}
	return nil
}

func reconnectUniqueNonZeroUUIDs(values []uuid.UUID) bool {
	seen := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value == uuid.Nil {
			return false
		}
		if _, exists := seen[value]; exists {
			return false
		}
		seen[value] = struct{}{}
	}
	return true
}

func reconnectRecordSettlementMatches(record ReconnectRecord) bool {
	ids, ok := reconnectRecordSettlementIDs(record)
	if !ok || !reconnectCommonSettlementMatches(record, ids) {
		return false
	}
	if record.GameResultRevision != nil {
		return reconnectWinnerSettlementMatches(record, ids)
	}
	return reconnectReplaySettlementMatches(record, ids)
}

func reconnectRecordSettlementIDs(record ReconnectRecord) (SettlementIDs, bool) {
	switch {
	case record.ReconnectCommand != nil:
		return record.ReconnectCommand.Settlement, true
	case record.TimeoutCommand != nil:
		return record.TimeoutCommand.Settlement, true
	case record.DisconnectCommand != nil:
		return record.DisconnectCommand.Settlement, true
	default:
		return SettlementIDs{}, false
	}
}

func reconnectCommonSettlementMatches(record ReconnectRecord, ids SettlementIDs) bool {
	return record.ScoreRevision != nil && record.ScoreRevision.ID == ids.ScoreRevisionID && record.Evidence != nil &&
		record.Evidence.AuditEventID == ids.AuditEventID && record.Evidence.OutboxEventID == ids.OutboxEventID &&
		record.Evidence.ProjectionRevisionID == ids.ProjectionRevisionID
}

func reconnectWinnerSettlementMatches(record ReconnectRecord, ids SettlementIDs) bool {
	if record.GameResultRevision.ID != ids.GameResultRevisionID || record.ReplayRoute != nil {
		return false
	}
	return record.SeriesResultRevision == nil || record.SeriesResultRevision.ID == ids.SeriesResultRevisionID
}

func reconnectReplaySettlementMatches(record ReconnectRecord, ids SettlementIDs) bool {
	return record.VoidGameResultRevision != nil && record.VoidGameResultRevision.ID == ids.GameResultRevisionID &&
		record.SeriesResultRevision == nil && record.ReplayRoute != nil && record.ReplayRoute.ID == ids.ReplayRouteID
}
