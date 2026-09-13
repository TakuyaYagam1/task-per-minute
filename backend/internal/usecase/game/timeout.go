package game

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

type TimeoutUseCase struct {
	repository ReconnectRepository
	clock      ReconnectClock
	observer   Observer
}

func NewTimeoutUseCase(
	repository ReconnectRepository,
	clock ReconnectClock,
	observers ...Observer,
) *TimeoutUseCase {
	return &TimeoutUseCase{
		repository: repository,
		clock:      clock,
		observer:   firstReconnectObserver(observers...),
	}
}

func (u *TimeoutUseCase) Expire(ctx context.Context, command TimeoutCommand) (*ReconnectRecord, bool, error) {
	command = cloneReconnectTimeoutCommand(command)
	if u == nil || u.repository == nil || u.clock == nil || !validReconnectTimeoutCommand(command) {
		return nil, false, domain.ErrValidation
	}
	measurement := newReconnectEventMeasurement(u.clock, u.observer)
	record, changed, err := runClockedReconnectMutation(ctx, u.repository, u.clock, command.Scope, command.CommandID, command.ParticipantID,
		func(record ReconnectRecord) bool {
			return record.Kind == MutationTimeout && record.TimeoutCommand != nil && *record.TimeoutCommand == command
		}, reconnectTimeoutMutationBuilder(command))
	u.observeReconnectTimeout(ctx, measurement, command, record, changed, err)
	return record, changed, err
}

func (u *TimeoutUseCase) observeReconnectTimeout(
	ctx context.Context,
	measurement reconnectEventMeasurement,
	command TimeoutCommand,
	record *ReconnectRecord,
	changed bool,
	err error,
) {
	emitReconnectEvent(ctx, measurement, TimeoutTerminalEvent(command, record, changed, err))
}

// TimeoutTerminalEvent returns the sanitized terminal observation for a
// reconnect timeout outcome. The caller controls when it is emitted so an
// outer transaction can defer it until its durable commit has succeeded.
func TimeoutTerminalEvent(
	command TimeoutCommand,
	record *ReconnectRecord,
	changed bool,
	err error,
) ReconnectEvent {
	outcome, reason := reconnectEventResult(changed, err, ErrConflict)
	switch {
	case errors.Is(err, ErrDeadline):
		outcome, reason = OutcomeRejected, "deadline_not_reached"
	case errors.Is(err, ErrCommandReuse):
		outcome, reason = OutcomeRejected, "command_reused"
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrInvalidMutation):
		outcome, reason = OutcomeRejected, "reconnect_unavailable"
	}
	revision := int64(0)
	if record != nil {
		revision = record.ReconnectAuthority.Revision
	}
	event := ReconnectEvent{
		ReconnectEvent: "tournament.command.reconnect_timeout", Outcome: outcome,
		CommandID: command.CommandID, TournamentID: command.Scope.TournamentID,
		ParticipantID: command.ParticipantID, Stage: "deadline", Transition: "expire_reconnect",
		ReasonCode: reason, Revision: revision,
	}
	if record != nil && changed {
		for _, interval := range record.ReconnectAuthority.Reconnect {
			if interval.ID == command.IntervalID && !record.RecordedAt.Before(interval.Deadline) {
				event.HasDeadlineLag = true
				event.DeadlineLag = record.RecordedAt.Sub(interval.Deadline)
				break
			}
		}
	}
	return event
}

func reconnectTimeoutMutationBuilder(command TimeoutCommand) reconnectRecordBuilder {
	return func(authority ReconnectAuthority, now time.Time) (ReconnectRecord, bool, error) {
		if authority.Current != nil {
			return reconnectTerminalReplayRecord(authority, now, MutationTimeout, nil, &command, nil), false, nil
		}
		next, err := prepareReconnectExpiry(authority, command, now)
		if err != nil {
			return ReconnectRecord{}, false, err
		}
		return finishReconnectExpiry(authority.Revision, next, command, now)
	}
}

func prepareReconnectExpiry(authority ReconnectAuthority, command TimeoutCommand, now time.Time) (ReconnectAuthority, error) {
	_, interval, err := reconnectParticipantAuthority(authority, command.ParticipantID, command.IntervalID)
	if err != nil {
		return ReconnectAuthority{}, err
	}
	if now.Compare(interval.Deadline) < 0 {
		return ReconnectAuthority{}, ErrDeadline
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

func finishReconnectExpiry(expectedRevision int64, next ReconnectAuthority, command TimeoutCommand, now time.Time) (ReconnectRecord, bool, error) {
	if reconnectOpponentConnected(next, command.ParticipantID) {
		return buildReconnectTerminalRecord(next, command.ParticipantID, command.Settlement, now,
			MutationTimeout, nil, &command, nil)
	}
	opponentInterval := reconnectCurrentInterval(next, reconnectOpponentMust(next.Series, command.ParticipantID))
	if opponentInterval == nil {
		return ReconnectRecord{}, false, ErrUnavailable
	}
	if opponentInterval.State == pause.ReconnectStateOpen && now.Before(opponentInterval.Deadline) {
		advanceReconnectAuthority(&next)
		return ReconnectRecord{Kind: MutationTimeout, TimeoutCommand: &command,
			ExpectedAuthorityRevision: expectedRevision, ReconnectAuthority: next, RecordedAt: now}, true, nil
	}
	if err := expireOpponentReconnectInterval(opponentInterval, now); err != nil {
		return ReconnectRecord{}, false, err
	}
	return buildReconnectTerminalRecord(next, uuid.Nil, command.Settlement, now,
		MutationTimeout, nil, &command, nil)
}

func expireOpponentReconnectInterval(interval *pause.PauseReconnectInterval, now time.Time) error {
	if interval.State == pause.ReconnectStateExpired {
		return nil
	}
	if interval.State != pause.ReconnectStateOpen {
		return ErrUnavailable
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

func reconnectCurrentInterval(authority ReconnectAuthority, participantID uuid.UUID) *pause.PauseReconnectInterval {
	presence := reconnectPresenceByParticipant(authority.Presence, participantID)
	if presence == nil {
		return nil
	}
	var current *pause.PauseReconnectInterval
	for index := range authority.Reconnect {
		if authority.Reconnect[index].ParticipantID == participantID && authority.Reconnect[index].PresenceEpoch == presence.PresenceEpoch &&
			authority.Reconnect[index].GameID == authority.Game.ID &&
			(authority.Reconnect[index].State == pause.ReconnectStateOpen || authority.Reconnect[index].State == pause.ReconnectStateExpired) {
			if current == nil || authority.Reconnect[index].ContinuationNumber > current.ContinuationNumber {
				current = &authority.Reconnect[index]
			}
		}
	}
	return current
}

func reconnectCurrentGameSlot(series domain.Series, gameID uuid.UUID) (domain.GameSlot, bool) {
	for _, slot := range series.Slots {
		if len(slot.Attempts) > 0 && slot.Attempts[len(slot.Attempts)-1].ID == gameID {
			return slot, true
		}
	}
	return domain.GameSlot{}, false
}

func expireReconnectInterval(interval *pause.PauseReconnectInterval, now time.Time) {
	if interval == nil || interval.State != pause.ReconnectStateOpen {
		return
	}
	interval.State = pause.ReconnectStateExpired
	interval.ClosedAt = reconnectCloneTimePointer(&now)
	interval.Revision++
	interval.UpdatedAt = now
}

func validReconnectTimeoutCommand(command TimeoutCommand) bool {
	return command.Scope.Validate() == nil && command.CommandID != uuid.Nil && command.ParticipantID != uuid.Nil &&
		command.IntervalID != uuid.Nil && validReconnectSettlementIDs(command.Settlement)
}

func validReconnectSettlementIDs(ids SettlementIDs) bool {
	values := []uuid.UUID{ids.GameResultRevisionID.UUID(), ids.ScoreRevisionID.UUID(), ids.SeriesResultRevisionID.UUID(),
		ids.ReplayRouteID, ids.AuditEventID, ids.OutboxEventID, ids.ProjectionRevisionID}
	return reconnectUniqueNonZeroUUIDs(values)
}
