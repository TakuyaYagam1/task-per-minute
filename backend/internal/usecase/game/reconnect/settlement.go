package reconnect

import (
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gamedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/game"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

func buildReconnectTerminalRecord(
	authority ReconnectAuthority,
	loserID uuid.UUID,
	ids SettlementIDs,
	now time.Time,
	kind MutationKind,
	reconnectCommand *ReconnectCommand,
	timeoutCommand *TimeoutCommand,
	disconnectCommand *DisconnectCommand,
) (ReconnectRecord, bool, error) {
	if err := validateReconnectSettlementStart(authority, ids); err != nil {
		return ReconnectRecord{}, false, err
	}
	previousScore := authority.Series.CurrentScoreRevisionID
	previousSeriesResult := authority.Series.CurrentResultRevisionID
	gameOrdinal := authority.CurrentOrdinal + 1
	scoreOrdinal := authority.CurrentOrdinal + 2
	before := authority.Series.Score
	winnerID, state, reason, err := reconnectTerminalRoute(authority.Series, loserID)
	if err != nil {
		return ReconnectRecord{}, false, err
	}
	if err := transitionReconnectTerminalGame(&authority, winnerID, state, reason, ids.GameResultRevisionID); err != nil {
		return ReconnectRecord{}, false, err
	}
	after := reconnectScoreAfter(before, authority.Series, winnerID)
	seriesResult, err := settleReconnectSeries(&authority, after, winnerID, ids, reason, previousSeriesResult, scoreOrdinal, now)
	if err != nil {
		return ReconnectRecord{}, false, err
	}
	resultID := ids.GameResultRevisionID
	gameResultIDs := append([]domain.OfficialResultRevisionID(nil), authority.CurrentGameResultRevisionIDs...)
	gameResultIDs = append(gameResultIDs, resultID)
	authority.CurrentGameResultRevisionIDs = append([]domain.OfficialResultRevisionID(nil), gameResultIDs...)
	gameResult, voidGameResult := reconnectGameResultRevisions(authority.Game.ID, resultID, winnerID, reason, gameOrdinal, now)
	score := seriesdomain.ScoreRevision{ID: ids.ScoreRevisionID, SeriesID: authority.Series.ID,
		FirstParticipantID: authority.Series.FirstParticipantID, SecondParticipantID: authority.Series.SecondParticipantID,
		PreviousRevisionID: reconnectCloneSeriesScoreRevisionIDPointer(previousScore), Ordinal: scoreOrdinal,
		Format: authority.Series.Format, ScoreBefore: before, ScoreAfter: after,
		GameResultRevisionIDs: gameResultIDs, RecordedAt: now}
	replayRoute, err := reconnectReplayRoute(authority, ids.ReplayRouteID, winnerID, now)
	if err != nil {
		return ReconnectRecord{}, false, err
	}
	evidence := newReconnectSettlementEvidence(authority.CurrentProjectionRevision, ids, now)
	authority.CurrentProjectionRevision++
	outcome := TerminalOutcome{GameResultRevision: gameResult, VoidGameResultRevision: voidGameResult,
		ScoreRevision: score, SeriesResultRevision: seriesResult, ReplayRoute: replayRoute, Evidence: evidence, TerminalizedAt: now}
	authority.Current = cloneReconnectTerminalOutcome(&outcome)
	advanceReconnectAuthority(&authority)
	return ReconnectRecord{Kind: kind, ReconnectCommand: reconnectCommand, TimeoutCommand: timeoutCommand,
		DisconnectCommand: disconnectCommand, ExpectedAuthorityRevision: authority.Revision - 1,
		ReconnectAuthority: authority, GameResultRevision: gameResult, VoidGameResultRevision: voidGameResult,
		ScoreRevision: &score, SeriesResultRevision: seriesResult, ReplayRoute: replayRoute,
		Evidence: &evidence, RecordedAt: now}, true, nil
}

func validateReconnectSettlementStart(authority ReconnectAuthority, ids SettlementIDs) error {
	if !validReconnectSettlementIDs(ids) || authority.Current != nil || authority.Game.State.IsTerminal() {
		return domain.ErrValidation
	}
	for _, currentID := range authority.CurrentGameResultRevisionIDs {
		if currentID == ids.GameResultRevisionID {
			return reconnectError("Game result revision identity is already current")
		}
	}
	if authority.Series.CurrentScoreRevisionID != nil && *authority.Series.CurrentScoreRevisionID == ids.ScoreRevisionID {
		return reconnectError("score revision identity self-links")
	}
	if authority.Series.CurrentResultRevisionID != nil && *authority.Series.CurrentResultRevisionID == ids.SeriesResultRevisionID {
		return reconnectError("Series result revision identity self-links")
	}
	maxInt := int(^uint(0) >> 1)
	if authority.CurrentOrdinal > maxInt-3 || authority.CurrentProjectionRevision == math.MaxInt64 {
		return reconnectError("terminal lineage overflow")
	}
	return nil
}

func reconnectTerminalRoute(series domain.Series, loserID uuid.UUID) (uuid.UUID, domain.GameState, domain.GameResultReason, error) {
	if loserID == uuid.Nil {
		return uuid.Nil, domain.GameStateVoid, domain.GameResultReasonDisconnect, nil
	}
	winnerID, ok := reconnectOpponentID(series, loserID)
	if !ok {
		return uuid.Nil, "", "", ErrUnavailable
	}
	return winnerID, domain.GameStateCompleted, domain.GameResultReasonOperatorForfeit, nil
}

func transitionReconnectTerminalGame(authority *ReconnectAuthority, winnerID uuid.UUID, state domain.GameState, reason domain.GameResultReason, resultID domain.OfficialResultRevisionID) error {
	evidence := &gamedomain.TerminalEvidence{Reason: reason, ResultRevisionID: &resultID}
	if winnerID != uuid.Nil {
		evidence.WinnerID = &winnerID
	}
	return transitionReconnectGame(authority, state, evidence)
}

func reconnectScoreAfter(before domain.SeriesScore, series domain.Series, winnerID uuid.UUID) domain.SeriesScore {
	after := before
	switch winnerID {
	case series.FirstParticipantID:
		after.FirstParticipantWins++
	case series.SecondParticipantID:
		after.SecondParticipantWins++
	}
	return after
}

func settleReconnectSeries(authority *ReconnectAuthority, after domain.SeriesScore, winnerID uuid.UUID, ids SettlementIDs, reason domain.GameResultReason, previousResult *domain.OfficialResultRevisionID, scoreOrdinal int, now time.Time) (*SeriesRevision, error) {
	winner := after.Winner(authority.Series.FirstParticipantID, authority.Series.SecondParticipantID, authority.Series.Format)
	if winner != nil {
		return completeReconnectSeries(authority, after, winner, ids, reason, previousResult, scoreOrdinal, now)
	}
	authority.Series.Score = after
	authority.Series.CurrentScoreRevisionID = &ids.ScoreRevisionID
	authority.CurrentOrdinal = scoreOrdinal
	if winnerID != uuid.Nil {
		if authority.Series.Validate() != nil {
			return nil, reconnectError("invalid ongoing Series")
		}
		return nil, nil
	}
	transitioned, changed, err := seriesdomain.Transition(seriesdomain.Execution{Series: authority.Series}, seriesdomain.TransitionCommand{
		NextState: domain.SeriesStateReplayRequired,
	})
	if err != nil || !changed {
		return nil, reconnectError("route Series replay: %v", err)
	}
	authority.Series = transitioned.Series
	return nil, nil
}

func completeReconnectSeries(authority *ReconnectAuthority, after domain.SeriesScore, winner *uuid.UUID, ids SettlementIDs, reason domain.GameResultReason, previousResult *domain.OfficialResultRevisionID, scoreOrdinal int, now time.Time) (*SeriesRevision, error) {
	transitioned, changed, err := seriesdomain.Transition(seriesdomain.Execution{Series: authority.Series}, seriesdomain.TransitionCommand{
		NextState: domain.SeriesStateCompleted,
		Terminal:  &seriesdomain.TerminalEvidence{Score: after, WinnerID: winner, ScoreRevisionID: &ids.ScoreRevisionID, ResultRevisionID: &ids.SeriesResultRevisionID},
	})
	if err != nil || !changed {
		return nil, reconnectError("complete Series: %v", err)
	}
	authority.Series = transitioned.Series
	authority.CurrentOrdinal = scoreOrdinal + 1
	return &SeriesRevision{Ordinal: scoreOrdinal + 1, ID: ids.SeriesResultRevisionID, SeriesID: authority.Series.ID,
		PreviousRevisionID: reconnectCloneOfficialResultRevisionIDPointer(previousResult), State: domain.SeriesStateCompleted,
		WinnerID: reconnectCloneUUIDPointer(winner), ScoreRevisionID: ids.ScoreRevisionID, Reason: reason, RecordedAt: now}, nil
}

func reconnectGameResultRevisions(gameID uuid.UUID, resultID domain.OfficialResultRevisionID, winnerID uuid.UUID, reason domain.GameResultReason, ordinal int, now time.Time) (*GameRevision, *AttemptGameResultRevision) {
	if winnerID == uuid.Nil {
		return nil, &AttemptGameResultRevision{Ordinal: ordinal, ID: resultID, GameID: gameID, Reason: reason, RecordedAt: now}
	}
	return &GameRevision{Ordinal: ordinal, ID: resultID, GameID: gameID, WinnerID: winnerID, Reason: reason, RecordedAt: now}, nil
}

func reconnectReplayRoute(authority ReconnectAuthority, routeID, winnerID uuid.UUID, now time.Time) (*WaveMemberRoute, error) {
	if winnerID != uuid.Nil {
		return nil, nil
	}
	slot, ok := reconnectCurrentGameSlot(authority.Series, authority.Game.ID)
	if !ok {
		return nil, reconnectError("current Game slot is missing")
	}
	return &WaveMemberRoute{ID: routeID, WaveID: authority.Scope.WaveID, SeriesID: authority.Series.ID,
		SlotID: slot.ID, GameID: authority.Game.ID, Category: slot.Category, RoutedAt: now}, nil
}

func newReconnectSettlementEvidence(currentRevision int64, ids SettlementIDs, now time.Time) seriesdomain.SettlementEvidence {
	return seriesdomain.SettlementEvidence{AuditEventID: ids.AuditEventID, OutboxEventID: ids.OutboxEventID,
		ProjectionRevisionID: ids.ProjectionRevisionID, SourceProjectionRevision: currentRevision,
		ProjectionRevision: currentRevision + 1, RecordedAt: now}
}

func reconnectTerminalReplayRecord(
	authority ReconnectAuthority,
	_ time.Time,
	kind MutationKind,
	reconnectCommand *ReconnectCommand,
	timeoutCommand *TimeoutCommand,
	disconnectCommand *DisconnectCommand,
) ReconnectRecord {
	current := cloneReconnectTerminalOutcome(authority.Current)
	record := ReconnectRecord{Kind: kind, ReconnectCommand: reconnectCommand, TimeoutCommand: timeoutCommand,
		DisconnectCommand: disconnectCommand, ExpectedAuthorityRevision: authority.Revision,
		ReconnectAuthority: cloneReconnectAuthority(authority), RecordedAt: current.TerminalizedAt}
	record.GameResultRevision = current.GameResultRevision
	record.VoidGameResultRevision = current.VoidGameResultRevision
	score := cloneSettlementScoreRevision(current.ScoreRevision)
	record.ScoreRevision = &score
	record.SeriesResultRevision = current.SeriesResultRevision
	record.ReplayRoute = current.ReplayRoute
	evidence := current.Evidence
	record.Evidence = &evidence
	return record
}
