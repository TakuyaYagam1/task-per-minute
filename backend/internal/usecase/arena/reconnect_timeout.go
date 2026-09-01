package arena

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type ReconnectTimeoutUseCase struct {
	repository ReconnectRepository
	clock      Clock
}

func NewReconnectTimeoutUseCase(repository ReconnectRepository, clock Clock) *ReconnectTimeoutUseCase {
	return &ReconnectTimeoutUseCase{repository: repository, clock: clock}
}

func (u *ReconnectTimeoutUseCase) Expire(ctx context.Context, command ReconnectTimeoutCommand) (*ReconnectRecord, bool, error) {
	command = cloneReconnectTimeoutCommand(command)
	if u == nil || u.repository == nil || u.clock == nil || !validReconnectTimeoutCommand(command) {
		return nil, false, domain.ErrValidation
	}
	return runClockedReconnectMutation(ctx, u.repository, u.clock, command.Scope, command.CommandID,
		func(record ReconnectRecord) bool {
			return record.Kind == ReconnectMutationTimeout && record.TimeoutCommand != nil && *record.TimeoutCommand == command
		}, reconnectTimeoutMutationBuilder(command))
}

func reconnectTimeoutMutationBuilder(command ReconnectTimeoutCommand) reconnectRecordBuilder {
	return func(authority ReconnectAuthority, now time.Time) (ReconnectRecord, bool, error) {
		if authority.Current != nil {
			return reconnectTerminalReplayRecord(authority, now, ReconnectMutationTimeout, nil, &command, nil), false, nil
		}
		next, err := prepareReconnectExpiry(authority, command, now)
		if err != nil {
			return ReconnectRecord{}, false, err
		}
		return finishReconnectExpiry(authority.Revision, next, command, now)
	}
}

func prepareReconnectExpiry(authority ReconnectAuthority, command ReconnectTimeoutCommand, now time.Time) (ReconnectAuthority, error) {
	_, interval, err := reconnectParticipantAuthority(authority, command.ParticipantID, command.IntervalID)
	if err != nil {
		return ReconnectAuthority{}, err
	}
	if now.Compare(interval.Deadline) < 0 {
		return ReconnectAuthority{}, ErrReconnectDeadline
	}
	next := cloneReconnectAuthority(authority)
	target := reconnectMutationIntervalByID(next.Reconnect, command.IntervalID)
	if authority.Revision == math.MaxInt64 || target == nil || target.Revision == math.MaxInt64 {
		return ReconnectAuthority{}, reconnectError("timeout revision overflow")
	}
	if !now.After(target.UpdatedAt) {
		return ReconnectAuthority{}, reconnectError("timeout interval timestamp did not advance")
	}
	expireReconnectInterval(target, now)
	return next, nil
}

func finishReconnectExpiry(expectedRevision int64, next ReconnectAuthority, command ReconnectTimeoutCommand, now time.Time) (ReconnectRecord, bool, error) {
	if reconnectOpponentConnected(next, command.ParticipantID) {
		return buildReconnectTerminalRecord(next, command.ParticipantID, command.Settlement, now,
			ReconnectMutationTimeout, nil, &command, nil)
	}
	opponentInterval := reconnectCurrentInterval(next, reconnectOpponentMust(next.Series, command.ParticipantID))
	if opponentInterval == nil {
		return ReconnectRecord{}, false, ErrReconnectUnavailable
	}
	if opponentInterval.State == ReconnectStateOpen && now.Before(opponentInterval.Deadline) {
		advanceReconnectAuthority(&next)
		return ReconnectRecord{Kind: ReconnectMutationTimeout, TimeoutCommand: &command,
			ExpectedAuthorityRevision: expectedRevision, Authority: next, RecordedAt: now}, true, nil
	}
	if err := expireOpponentReconnectInterval(opponentInterval, now); err != nil {
		return ReconnectRecord{}, false, err
	}
	return buildReconnectTerminalRecord(next, uuid.Nil, command.Settlement, now,
		ReconnectMutationTimeout, nil, &command, nil)
}

func expireOpponentReconnectInterval(interval *PauseReconnectInterval, now time.Time) error {
	if interval.State == ReconnectStateExpired {
		return nil
	}
	if interval.State != ReconnectStateOpen {
		return ErrReconnectUnavailable
	}
	if interval.Revision == math.MaxInt64 {
		return reconnectError("opponent interval revision overflow")
	}
	if !now.After(interval.UpdatedAt) {
		return reconnectError("opponent timeout timestamp did not advance")
	}
	expireReconnectInterval(interval, now)
	return nil
}

func buildReconnectTerminalRecord(
	authority ReconnectAuthority,
	loserID uuid.UUID,
	ids ReconnectSettlementIDs,
	now time.Time,
	kind ReconnectMutationKind,
	reconnectCommand *ReconnectCommand,
	timeoutCommand *ReconnectTimeoutCommand,
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
	gameResultIDs := append([]domain.ArenaOfficialResultRevisionID(nil), authority.CurrentGameResultRevisionIDs...)
	gameResultIDs = append(gameResultIDs, resultID)
	authority.CurrentGameResultRevisionIDs = append([]domain.ArenaOfficialResultRevisionID(nil), gameResultIDs...)
	gameResult, voidGameResult := reconnectGameResultRevisions(authority.Game.ID, resultID, winnerID, reason, gameOrdinal, now)
	score := ArenaSettlementScoreRevision{ID: ids.ScoreRevisionID, SeriesID: authority.Series.ID,
		FirstParticipantID: authority.Series.FirstParticipantID, SecondParticipantID: authority.Series.SecondParticipantID,
		PreviousRevisionID: cloneSeriesScoreRevisionIDPointer(previousScore), Ordinal: scoreOrdinal,
		Format: authority.Series.Format, ScoreBefore: before, ScoreAfter: after,
		GameResultRevisionIDs: gameResultIDs, RecordedAt: now}
	replayRoute, err := reconnectReplayRoute(authority, ids.ReplayRouteID, winnerID, now)
	if err != nil {
		return ReconnectRecord{}, false, err
	}
	evidence := newReconnectSettlementEvidence(authority.CurrentProjectionRevision, ids, now)
	authority.CurrentProjectionRevision++
	outcome := ReconnectTerminalOutcome{GameResultRevision: gameResult, VoidGameResultRevision: voidGameResult,
		ScoreRevision: score, SeriesResultRevision: seriesResult, ReplayRoute: replayRoute, Evidence: evidence, TerminalizedAt: now}
	authority.Current = cloneReconnectTerminalOutcome(&outcome)
	advanceReconnectAuthority(&authority)
	return ReconnectRecord{Kind: kind, ReconnectCommand: reconnectCommand, TimeoutCommand: timeoutCommand,
		DisconnectCommand: disconnectCommand, ExpectedAuthorityRevision: authority.Revision - 1,
		Authority: authority, GameResultRevision: gameResult, VoidGameResultRevision: voidGameResult,
		ScoreRevision: &score, SeriesResultRevision: seriesResult, ReplayRoute: replayRoute,
		Evidence: &evidence, RecordedAt: now}, true, nil
}

func validateReconnectSettlementStart(authority ReconnectAuthority, ids ReconnectSettlementIDs) error {
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

func reconnectTerminalRoute(series domain.ArenaSeries, loserID uuid.UUID) (uuid.UUID, domain.ArenaGameState, domain.ArenaGameResultReason, error) {
	if loserID == uuid.Nil {
		return uuid.Nil, domain.ArenaGameStateVoid, domain.ArenaGameResultReasonDisconnect, nil
	}
	winnerID, ok := reconnectOpponentID(series, loserID)
	if !ok {
		return uuid.Nil, "", "", ErrReconnectUnavailable
	}
	return winnerID, domain.ArenaGameStateCompleted, domain.ArenaGameResultReasonOperatorForfeit, nil
}

func transitionReconnectTerminalGame(authority *ReconnectAuthority, winnerID uuid.UUID, state domain.ArenaGameState, reason domain.ArenaGameResultReason, resultID domain.ArenaOfficialResultRevisionID) error {
	evidence := &GameTerminalEvidence{Reason: reason, ResultRevisionID: &resultID}
	if winnerID != uuid.Nil {
		evidence.WinnerID = &winnerID
	}
	return transitionReconnectGame(authority, state, evidence)
}

func reconnectScoreAfter(before domain.ArenaSeriesScore, series domain.ArenaSeries, winnerID uuid.UUID) domain.ArenaSeriesScore {
	after := before
	switch winnerID {
	case series.FirstParticipantID:
		after.FirstParticipantWins++
	case series.SecondParticipantID:
		after.SecondParticipantWins++
	}
	return after
}

func settleReconnectSeries(authority *ReconnectAuthority, after domain.ArenaSeriesScore, winnerID uuid.UUID, ids ReconnectSettlementIDs, reason domain.ArenaGameResultReason, previousResult *domain.ArenaOfficialResultRevisionID, scoreOrdinal int, now time.Time) (*ForfeitSeriesRevision, error) {
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
	transitioned, changed, err := TransitionSeriesExecution(SeriesExecution{Series: authority.Series}, SeriesExecutionTransitionCommand{
		NextState: domain.ArenaSeriesStateReplayRequired,
	})
	if err != nil || !changed {
		return nil, reconnectError("route Series replay: %v", err)
	}
	authority.Series = transitioned.Series
	return nil, nil
}

func completeReconnectSeries(authority *ReconnectAuthority, after domain.ArenaSeriesScore, winner *uuid.UUID, ids ReconnectSettlementIDs, reason domain.ArenaGameResultReason, previousResult *domain.ArenaOfficialResultRevisionID, scoreOrdinal int, now time.Time) (*ForfeitSeriesRevision, error) {
	transitioned, changed, err := TransitionSeriesExecution(SeriesExecution{Series: authority.Series}, SeriesExecutionTransitionCommand{
		NextState: domain.ArenaSeriesStateCompleted,
		Terminal:  &SeriesTerminalEvidence{Score: after, WinnerID: winner, ScoreRevisionID: &ids.ScoreRevisionID, ResultRevisionID: &ids.SeriesResultRevisionID},
	})
	if err != nil || !changed {
		return nil, reconnectError("complete Series: %v", err)
	}
	authority.Series = transitioned.Series
	authority.CurrentOrdinal = scoreOrdinal + 1
	return &ForfeitSeriesRevision{Ordinal: scoreOrdinal + 1, ID: ids.SeriesResultRevisionID, SeriesID: authority.Series.ID,
		PreviousRevisionID: cloneOfficialResultRevisionIDPointer(previousResult), State: domain.ArenaSeriesStateCompleted,
		WinnerID: cloneUUIDPointer(winner), ScoreRevisionID: ids.ScoreRevisionID, Reason: reason, RecordedAt: now}, nil
}

func reconnectGameResultRevisions(gameID uuid.UUID, resultID domain.ArenaOfficialResultRevisionID, winnerID uuid.UUID, reason domain.ArenaGameResultReason, ordinal int, now time.Time) (*ForfeitGameRevision, *FailedGameResultRevision) {
	if winnerID == uuid.Nil {
		return nil, &FailedGameResultRevision{Ordinal: ordinal, ID: resultID, GameID: gameID, Reason: reason, RecordedAt: now}
	}
	return &ForfeitGameRevision{Ordinal: ordinal, ID: resultID, GameID: gameID, WinnerID: winnerID, Reason: reason, RecordedAt: now}, nil
}

func reconnectReplayRoute(authority ReconnectAuthority, routeID, winnerID uuid.UUID, now time.Time) (*FailedWaveMemberRoute, error) {
	if winnerID != uuid.Nil {
		return nil, nil
	}
	slot, ok := reconnectCurrentGameSlot(authority.Series, authority.Game.ID)
	if !ok {
		return nil, reconnectError("current Game slot is missing")
	}
	return &FailedWaveMemberRoute{ID: routeID, WaveID: authority.Scope.WaveID, SeriesID: authority.Series.ID,
		SlotID: slot.ID, GameID: authority.Game.ID, Category: slot.Category, RoutedAt: now}, nil
}

func newReconnectSettlementEvidence(currentRevision int64, ids ReconnectSettlementIDs, now time.Time) ArenaSettlementEvidence {
	return ArenaSettlementEvidence{AuditEventID: ids.AuditEventID, OutboxEventID: ids.OutboxEventID,
		ProjectionRevisionID: ids.ProjectionRevisionID, SourceProjectionRevision: currentRevision,
		ProjectionRevision: currentRevision + 1, RecordedAt: now}
}

func reconnectTerminalReplayRecord(
	authority ReconnectAuthority,
	_ time.Time,
	kind ReconnectMutationKind,
	reconnectCommand *ReconnectCommand,
	timeoutCommand *ReconnectTimeoutCommand,
	disconnectCommand *DisconnectCommand,
) ReconnectRecord {
	current := cloneReconnectTerminalOutcome(authority.Current)
	record := ReconnectRecord{Kind: kind, ReconnectCommand: reconnectCommand, TimeoutCommand: timeoutCommand,
		DisconnectCommand: disconnectCommand, ExpectedAuthorityRevision: authority.Revision,
		Authority: cloneReconnectAuthority(authority), RecordedAt: current.TerminalizedAt}
	record.GameResultRevision = current.GameResultRevision
	record.VoidGameResultRevision = current.VoidGameResultRevision
	score := cloneArenaSettlementScoreRevision(current.ScoreRevision)
	record.ScoreRevision = &score
	record.SeriesResultRevision = current.SeriesResultRevision
	record.ReplayRoute = current.ReplayRoute
	evidence := current.Evidence
	record.Evidence = &evidence
	return record
}

func reconnectCurrentInterval(authority ReconnectAuthority, participantID uuid.UUID) *PauseReconnectInterval {
	presence := reconnectPresenceByParticipant(authority.Presence, participantID)
	if presence == nil {
		return nil
	}
	var current *PauseReconnectInterval
	for index := range authority.Reconnect {
		if authority.Reconnect[index].ParticipantID == participantID && authority.Reconnect[index].PresenceEpoch == presence.PresenceEpoch &&
			authority.Reconnect[index].GameID == authority.Game.ID &&
			(authority.Reconnect[index].State == ReconnectStateOpen || authority.Reconnect[index].State == ReconnectStateExpired) {
			if current == nil || authority.Reconnect[index].ContinuationNumber > current.ContinuationNumber {
				current = &authority.Reconnect[index]
			}
		}
	}
	return current
}

func reconnectCurrentGameSlot(series domain.ArenaSeries, gameID uuid.UUID) (domain.ArenaGameSlot, bool) {
	for _, slot := range series.Slots {
		if len(slot.Attempts) > 0 && slot.Attempts[len(slot.Attempts)-1].ID == gameID {
			return slot, true
		}
	}
	return domain.ArenaGameSlot{}, false
}

func expireReconnectInterval(interval *PauseReconnectInterval, now time.Time) {
	if interval == nil || interval.State != ReconnectStateOpen {
		return
	}
	interval.State = ReconnectStateExpired
	interval.ClosedAt = cloneArenaTimePointer(&now)
	interval.Revision++
	interval.UpdatedAt = now
}

func validReconnectTimeoutCommand(command ReconnectTimeoutCommand) bool {
	return validPauseGraphScope(command.Scope) && command.CommandID != uuid.Nil && command.ParticipantID != uuid.Nil &&
		command.IntervalID != uuid.Nil && validReconnectSettlementIDs(command.Settlement)
}

func validReconnectSettlementIDs(ids ReconnectSettlementIDs) bool {
	values := []uuid.UUID{ids.GameResultRevisionID.UUID(), ids.ScoreRevisionID.UUID(), ids.SeriesResultRevisionID.UUID(),
		ids.ReplayRouteID, ids.AuditEventID, ids.OutboxEventID, ids.ProjectionRevisionID}
	return uniqueNonZeroUUIDs(values)
}
