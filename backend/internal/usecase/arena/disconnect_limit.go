package arena

import (
	"context"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const ReconnectCycleLimit = 2

type DisconnectUseCase struct {
	repository ReconnectRepository
	clock      Clock
}

func NewDisconnectUseCase(repository ReconnectRepository, clock Clock) *DisconnectUseCase {
	return &DisconnectUseCase{repository: repository, clock: clock}
}

func (u *DisconnectUseCase) Disconnect(ctx context.Context, command DisconnectCommand) (*ReconnectRecord, bool, error) {
	command = cloneDisconnectCommand(command)
	if u == nil || u.repository == nil || u.clock == nil || !validDisconnectCommand(command) {
		return nil, false, domain.ErrValidation
	}
	return runClockedReconnectMutation(ctx, u.repository, u.clock, command.Scope, command.CommandID,
		func(record ReconnectRecord) bool {
			return record.Kind == ReconnectMutationDisconnect && record.DisconnectCommand != nil && disconnectCommandsEqual(*record.DisconnectCommand, command)
		}, disconnectMutationBuilder(command))
}

type reconnectDisconnectContext struct {
	authority ReconnectAuthority
	presence  *PausePresence
	counter   *PauseReconnectCounter
}

func disconnectMutationBuilder(command DisconnectCommand) reconnectRecordBuilder {
	return func(authority ReconnectAuthority, now time.Time) (ReconnectRecord, bool, error) {
		if authority.Current != nil {
			return reconnectTerminalReplayRecord(authority, now, ReconnectMutationDisconnect, nil, nil, &command), false, nil
		}
		prepared, err := prepareDisconnectMutation(authority, command, now)
		if err != nil {
			return ReconnectRecord{}, false, err
		}
		if command.ContinuedFromID != nil {
			return buildReconnectContinuation(prepared.authority, command, now, prepared.presence, prepared.counter)
		}
		return buildFreshDisconnect(authority.Revision, prepared, command, now)
	}
}

func prepareDisconnectMutation(authority ReconnectAuthority, command DisconnectCommand, now time.Time) (reconnectDisconnectContext, error) {
	if command.Deadline.Compare(now) <= 0 {
		return reconnectDisconnectContext{}, ErrReconnectDeadline
	}
	if reconnectMutationIntervalByID(authority.Reconnect, command.IntervalID) != nil {
		return reconnectDisconnectContext{}, ErrReconnectUnavailable
	}
	if authority.Revision == math.MaxInt64 {
		return reconnectDisconnectContext{}, reconnectError("revision overflow")
	}
	next := cloneReconnectAuthority(authority)
	presence := reconnectPresenceByParticipant(next.Presence, command.ParticipantID)
	counter := reconnectCounterByParticipant(next.Counters, command.ParticipantID)
	if !validDisconnectParticipant(next, presence, counter) {
		return reconnectDisconnectContext{}, ErrReconnectUnavailable
	}
	return reconnectDisconnectContext{authority: next, presence: presence, counter: counter}, nil
}

func validDisconnectParticipant(authority ReconnectAuthority, presence *PausePresence, counter *PauseReconnectCounter) bool {
	return presence != nil && counter != nil && counter.PauseID == authority.PauseID && counter.Limit == ReconnectCycleLimit &&
		counter.Used >= 0 && counter.Used <= counter.Limit
}

func buildFreshDisconnect(expectedRevision int64, prepared reconnectDisconnectContext, command DisconnectCommand, now time.Time) (ReconnectRecord, bool, error) {
	if err := validateFreshDisconnectPresence(*prepared.presence, now); err != nil {
		return ReconnectRecord{}, false, err
	}
	markReconnectDisconnected(prepared.presence, now)
	if prepared.counter.Used >= ReconnectCycleLimit {
		return buildTerminalDisconnect(prepared.authority, command, now)
	}
	return buildOpenDisconnect(expectedRevision, prepared, command, now)
}

func validateFreshDisconnectPresence(presence PausePresence, now time.Time) error {
	if presence.State != PresenceStateConnected {
		return ErrReconnectUnavailable
	}
	if presence.PresenceEpoch == math.MaxInt64 || presence.Revision == math.MaxInt64 {
		return reconnectError("Presence revision overflow")
	}
	if !now.After(presence.UpdatedAt) {
		return reconnectError("disconnect Presence timestamp did not advance")
	}
	return nil
}

func buildTerminalDisconnect(authority ReconnectAuthority, command DisconnectCommand, now time.Time) (ReconnectRecord, bool, error) {
	loserID := command.ParticipantID
	if !reconnectOpponentConnected(authority, command.ParticipantID) {
		loserID = uuid.Nil
		if err := cancelOpenReconnectIntervals(&authority, now); err != nil {
			return ReconnectRecord{}, false, err
		}
	}
	if authority.Game.State == domain.ArenaGameStateActive {
		if err := freezeReconnectClock(&authority, now); err != nil {
			return ReconnectRecord{}, false, err
		}
	}
	return buildReconnectTerminalRecord(authority, loserID, command.Settlement, now,
		ReconnectMutationDisconnect, nil, nil, &command)
}

func buildOpenDisconnect(expectedRevision int64, prepared reconnectDisconnectContext, command DisconnectCommand, now time.Time) (ReconnectRecord, bool, error) {
	if prepared.counter.Revision == math.MaxInt64 {
		return ReconnectRecord{}, false, reconnectError("counter revision overflow")
	}
	prepared.counter.Used++
	prepared.counter.Revision++
	prepared.authority.Reconnect = append(prepared.authority.Reconnect, newReconnectRoot(prepared.authority, *prepared.presence, *prepared.counter, command, now))
	if prepared.authority.Game.State == domain.ArenaGameStateActive {
		if err := freezeReconnectGame(&prepared.authority, now); err != nil {
			return ReconnectRecord{}, false, err
		}
	}
	advanceReconnectAuthority(&prepared.authority)
	return ReconnectRecord{Kind: ReconnectMutationDisconnect, DisconnectCommand: &command,
		ExpectedAuthorityRevision: expectedRevision, Authority: prepared.authority, RecordedAt: now}, true, nil
}

func newReconnectRoot(authority ReconnectAuthority, presence PausePresence, counter PauseReconnectCounter, command DisconnectCommand, now time.Time) PauseReconnectInterval {
	return PauseReconnectInterval{ID: command.IntervalID, PauseID: authority.PauseID, RosterID: command.Scope.RosterID,
		SeriesID: authority.Series.ID, GameID: authority.Game.ID, ParticipantID: command.ParticipantID,
		PresenceEpoch: presence.PresenceEpoch, Number: counter.Used, State: ReconnectStateOpen,
		OpenedAt: now, Deadline: command.Deadline, Revision: 1, UpdatedAt: now}
}

func buildReconnectContinuation(
	authority ReconnectAuthority,
	command DisconnectCommand,
	now time.Time,
	presence *PausePresence,
	counter *PauseReconnectCounter,
) (ReconnectRecord, bool, error) {
	predecessor, err := reconnectContinuationPredecessor(authority, command, *presence, *counter)
	if err != nil {
		return ReconnectRecord{}, false, err
	}
	if err := validateReconnectContinuationWindow(*predecessor, command.Deadline, now); err != nil {
		return ReconnectRecord{}, false, err
	}
	if reconnectContinuationBlocked(authority.Reconnect, *predecessor, command.ParticipantID) {
		return ReconnectRecord{}, false, ErrReconnectUnavailable
	}
	continuedFrom := predecessor.ID
	authority.Reconnect = append(authority.Reconnect, PauseReconnectInterval{
		ID: command.IntervalID, PauseID: authority.PauseID, RosterID: command.Scope.RosterID,
		SeriesID: authority.Series.ID, GameID: authority.Game.ID, ParticipantID: command.ParticipantID,
		PresenceEpoch: presence.PresenceEpoch, Number: predecessor.Number,
		ContinuationNumber: predecessor.ContinuationNumber + 1, ContinuedFromID: &continuedFrom,
		State: ReconnectStateOpen, OpenedAt: now, Deadline: command.Deadline, Revision: 1, UpdatedAt: now,
	})
	advanceReconnectAuthority(&authority)
	return ReconnectRecord{Kind: ReconnectMutationDisconnect, DisconnectCommand: &command,
		ExpectedAuthorityRevision: authority.Revision - 1, Authority: authority, RecordedAt: now}, true, nil
}

func reconnectContinuationPredecessor(authority ReconnectAuthority, command DisconnectCommand, presence PausePresence, counter PauseReconnectCounter) (*PauseReconnectInterval, error) {
	predecessor := reconnectMutationIntervalByID(authority.Reconnect, *command.ContinuedFromID)
	if predecessor == nil || predecessor.ParticipantID != command.ParticipantID || predecessor.State != ReconnectStateCancelled ||
		predecessor.ClosedAt == nil || predecessor.SuspendedByPauseID == nil || presence.State != PresenceStateDisconnected ||
		predecessor.Number != counter.Used || predecessor.PresenceEpoch != presence.PresenceEpoch ||
		reconnectMutationIntervalByID(authority.Reconnect, command.IntervalID) != nil {
		return nil, ErrReconnectUnavailable
	}
	return predecessor, nil
}

func validateReconnectContinuationWindow(predecessor PauseReconnectInterval, deadline, now time.Time) error {
	if !now.After(predecessor.UpdatedAt) || !now.After(*predecessor.ClosedAt) {
		return reconnectError("continuation timestamp did not advance")
	}
	remaining := predecessor.Deadline.Sub(*predecessor.ClosedAt)
	wantDeadline, ok := safePauseTimeAdd(now, remaining)
	if remaining <= 0 || !ok || !deadline.Equal(wantDeadline) {
		return ErrReconnectDeadline
	}
	if predecessor.ContinuationNumber == int(^uint(0)>>1) {
		return reconnectError("continuation number overflow")
	}
	return nil
}

func reconnectContinuationBlocked(intervals []PauseReconnectInterval, predecessor PauseReconnectInterval, participantID uuid.UUID) bool {
	for _, interval := range intervals {
		if interval.ContinuedFromID != nil && *interval.ContinuedFromID == predecessor.ID {
			return true
		}
		if interval.ParticipantID == participantID && interval.Number == predecessor.Number && interval.State == ReconnectStateOpen {
			return true
		}
	}
	return false
}

func cancelOpenReconnectIntervals(authority *ReconnectAuthority, now time.Time) error {
	for index := range authority.Reconnect {
		interval := &authority.Reconnect[index]
		if interval.GameID != authority.Game.ID || interval.State != ReconnectStateOpen {
			continue
		}
		if interval.Revision == math.MaxInt64 {
			return reconnectError("reconnect interval revision overflow")
		}
		if !now.After(interval.UpdatedAt) {
			return reconnectError("cancelled reconnect timestamp did not advance")
		}
		interval.State = ReconnectStateCancelled
		interval.ClosedAt = cloneArenaTimePointer(&now)
		interval.Revision++
		interval.UpdatedAt = now
	}
	return nil
}

func markReconnectDisconnected(presence *PausePresence, now time.Time) {
	presence.State = PresenceStateDisconnected
	presence.PresenceEpoch++
	presence.Revision++
	presence.DisconnectedAt = cloneArenaTimePointer(&now)
	presence.UpdatedAt = now
}

func freezeReconnectGame(authority *ReconnectAuthority, now time.Time) error {
	if err := freezeReconnectClock(authority, now); err != nil {
		return err
	}
	return transitionReconnectGame(authority, domain.ArenaGameStatePaused, nil)
}

func freezeReconnectClock(authority *ReconnectAuthority, now time.Time) error {
	deadline := authority.GameClock.OriginalDeadline
	if authority.GameClock.ResumedDeadline != nil {
		if authority.GameClock.ResumedAt == nil || !now.After(*authority.GameClock.ResumedAt) {
			return reconnectError("Game clock rollback")
		}
		deadline = *authority.GameClock.ResumedDeadline
	}
	if deadline.Compare(now) <= 0 {
		return ErrReconnectDeadline
	}
	if authority.GameClock.Revision == math.MaxInt64 {
		return reconnectError("Game clock revision overflow")
	}
	authority.GameClock.OriginalDeadline = deadline
	authority.GameClock.FrozenAt = now
	authority.GameClock.Remaining = deadline.Sub(now)
	authority.GameClock.ResumedAt = nil
	authority.GameClock.ResumedDeadline = nil
	authority.GameClock.Revision++
	return nil
}

func validDisconnectCommand(command DisconnectCommand) bool {
	return validPauseGraphScope(command.Scope) && command.CommandID != uuid.Nil && command.ParticipantID != uuid.Nil &&
		command.IntervalID != uuid.Nil && validArenaServerTime(command.Deadline) && validReconnectSettlementIDs(command.Settlement)
}

func disconnectCommandsEqual(first, second DisconnectCommand) bool {
	if first.Scope != second.Scope || first.CommandID != second.CommandID || first.ParticipantID != second.ParticipantID ||
		first.IntervalID != second.IntervalID || !first.Deadline.Equal(second.Deadline) || first.Settlement != second.Settlement {
		return false
	}
	if first.ContinuedFromID == nil || second.ContinuedFromID == nil {
		return first.ContinuedFromID == nil && second.ContinuedFromID == nil
	}
	return *first.ContinuedFromID == *second.ContinuedFromID
}
