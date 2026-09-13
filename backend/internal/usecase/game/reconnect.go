package game

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	seriesdomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/series"
)

var (
	ErrInvalidMutation = errors.New("invalid reconnect mutation")
	ErrConflict        = errors.New("reconnect authority conflict")
	ErrCommandReuse    = errors.New("reconnect command was reused")
	ErrDeadline        = errors.New("reconnect deadline reached")
	ErrUnavailable     = errors.New("reconnect mutation is unavailable")
)

type MutationKind string

const (
	MutationReconnect  MutationKind = "reconnect"
	MutationTimeout    MutationKind = "timeout"
	MutationDisconnect MutationKind = "disconnect"
)

type ReconnectAuthority struct {
	Scope                        pause.GraphScope
	Revision                     int64
	PauseID                      uuid.UUID
	GameRevision                 int64
	SeriesRevision               int64
	CurrentOrdinal               int
	CurrentProjectionRevision    int64
	CurrentGameResultRevisionIDs []domain.OfficialResultRevisionID
	Game                         domain.Game
	Series                       domain.Series
	GameClock                    pause.PauseResumeGameClock
	Presence                     []pause.PausePresence
	Reconnect                    []pause.PauseReconnectInterval
	Counters                     []pause.PauseReconnectCounter
	Current                      *TerminalOutcome
}

type ReconnectCommand struct {
	Scope         pause.GraphScope
	CommandID     uuid.UUID
	ParticipantID uuid.UUID
	IntervalID    uuid.UUID
	Settlement    SettlementIDs
}

type SettlementIDs struct {
	GameResultRevisionID   domain.OfficialResultRevisionID
	ScoreRevisionID        domain.SeriesScoreRevisionID
	SeriesResultRevisionID domain.OfficialResultRevisionID
	ReplayRouteID          uuid.UUID
	AuditEventID           uuid.UUID
	OutboxEventID          uuid.UUID
	ProjectionRevisionID   uuid.UUID
}

type TerminalOutcome struct {
	GameResultRevision     *GameRevision
	VoidGameResultRevision *AttemptGameResultRevision
	ScoreRevision          seriesdomain.ScoreRevision
	SeriesResultRevision   *SeriesRevision
	ReplayRoute            *WaveMemberRoute
	Evidence               seriesdomain.SettlementEvidence
	TerminalizedAt         time.Time
}

type TimeoutCommand struct {
	Scope         pause.GraphScope
	CommandID     uuid.UUID
	ParticipantID uuid.UUID
	IntervalID    uuid.UUID
	Settlement    SettlementIDs
}

type DisconnectCommand struct {
	Scope           pause.GraphScope
	CommandID       uuid.UUID
	ParticipantID   uuid.UUID
	IntervalID      uuid.UUID
	Deadline        time.Time
	ContinuedFromID *uuid.UUID
	Settlement      SettlementIDs
}

type ReconnectRecord struct {
	Kind                      MutationKind
	ReconnectCommand          *ReconnectCommand
	TimeoutCommand            *TimeoutCommand
	DisconnectCommand         *DisconnectCommand
	ExpectedAuthorityRevision int64
	ReconnectAuthority        ReconnectAuthority
	GameResultRevision        *GameRevision
	VoidGameResultRevision    *AttemptGameResultRevision
	ScoreRevision             *seriesdomain.ScoreRevision
	SeriesResultRevision      *SeriesRevision
	ReplayRoute               *WaveMemberRoute
	Evidence                  *seriesdomain.SettlementEvidence
	RecordedAt                time.Time
}

type ReconnectUseCase struct {
	repository ReconnectRepository
	clock      ReconnectClock
	observer   Observer
}

func ReconnectNewUseCase(
	repository ReconnectRepository,
	clock ReconnectClock,
	observers ...Observer,
) *ReconnectUseCase {
	return &ReconnectUseCase{
		repository: repository,
		clock:      clock,
		observer:   firstReconnectObserver(observers...),
	}
}

func (u *ReconnectUseCase) Reconnect(ctx context.Context, command ReconnectCommand) (*ReconnectRecord, bool, error) {
	command = cloneReconnectCommand(command)
	if u == nil || u.repository == nil || u.clock == nil || !validReconnectCommand(command) {
		return nil, false, domain.ErrValidation
	}
	measurement := newReconnectEventMeasurement(u.clock, u.observer)
	record, changed, err := runClockedReconnectMutation(ctx, u.repository, u.clock, command.Scope, command.CommandID, command.ParticipantID,
		func(record ReconnectRecord) bool {
			return record.Kind == MutationReconnect && record.ReconnectCommand != nil && reconnectCommandsEqual(*record.ReconnectCommand, command)
		}, reconnectMutationBuilder(command))
	outcome, reason := reconnectEventResult(changed, err, ErrConflict)
	switch {
	case errors.Is(err, ErrDeadline):
		outcome, reason = OutcomeRejected, "deadline_reached"
	case errors.Is(err, ErrCommandReuse):
		outcome, reason = OutcomeRejected, "command_reused"
	case errors.Is(err, ErrUnavailable), errors.Is(err, ErrInvalidMutation):
		outcome, reason = OutcomeRejected, "reconnect_unavailable"
	}
	revision := int64(0)
	if record != nil {
		revision = record.ReconnectAuthority.Revision
	}
	emitReconnectEvent(ctx, measurement, ReconnectEvent{
		ReconnectEvent: "tournament.command.reconnect", Outcome: outcome,
		CommandID: command.CommandID, TournamentID: command.Scope.TournamentID,
		ParticipantID: command.ParticipantID, Stage: "reconnect", Transition: "reconnect",
		ReasonCode: reason, Revision: revision,
	})
	return record, changed, err
}

func reconnectMutationBuilder(command ReconnectCommand) reconnectRecordBuilder {
	return func(authority ReconnectAuthority, now time.Time) (ReconnectRecord, bool, error) {
		if authority.Current != nil {
			return reconnectTerminalReplayRecord(authority, now, MutationReconnect, &command, nil, nil), false, nil
		}
		next, err := prepareReconnectMutation(authority, command, now)
		if err != nil {
			return ReconnectRecord{}, false, err
		}
		return finishReconnectMutation(authority.Revision, next, command, now)
	}
}

func prepareReconnectMutation(authority ReconnectAuthority, command ReconnectCommand, now time.Time) (ReconnectAuthority, error) {
	presence, interval, err := reconnectParticipantAuthority(authority, command.ParticipantID, command.IntervalID)
	if err != nil {
		return ReconnectAuthority{}, err
	}
	if now.Compare(interval.Deadline) >= 0 {
		return ReconnectAuthority{}, ErrDeadline
	}
	next := cloneReconnectAuthority(authority)
	nextPresence := reconnectPresenceByParticipant(next.Presence, command.ParticipantID)
	nextInterval := reconnectMutationIntervalByID(next.Reconnect, command.IntervalID)
	if nextPresence == nil || nextInterval == nil {
		return ReconnectAuthority{}, domain.ErrInternal
	}
	if err := validateReconnectWritable(authority.Revision, *nextPresence, *nextInterval, now); err != nil {
		return ReconnectAuthority{}, err
	}
	markReconnectConnected(nextPresence, presence.PresenceEpoch, now)
	markReconnectIntervalClosed(nextInterval, now)
	return next, nil
}

func validateReconnectWritable(revision int64, presence pause.PausePresence, interval pause.PauseReconnectInterval, now time.Time) error {
	if revision == math.MaxInt64 || presence.PresenceEpoch == math.MaxInt64 || presence.Revision == math.MaxInt64 || interval.Revision == math.MaxInt64 {
		return reconnectError("reconnect revision overflow")
	}
	if !now.After(presence.UpdatedAt) || !now.After(interval.UpdatedAt) {
		return reconnectError("reconnect mutation timestamp did not advance")
	}
	return nil
}

func markReconnectConnected(presence *pause.PausePresence, previousEpoch int64, now time.Time) {
	presence.State = pause.PresenceStateConnected
	presence.PresenceEpoch = previousEpoch + 1
	presence.Revision++
	presence.ConnectedAt = now
	presence.DisconnectedAt = nil
	presence.UpdatedAt = now
}

func markReconnectIntervalClosed(interval *pause.PauseReconnectInterval, now time.Time) {
	interval.State = pause.ReconnectStateReconnected
	interval.ClosedAt = reconnectCloneTimePointer(&now)
	interval.Revision++
	interval.UpdatedAt = now
}

func finishReconnectMutation(expectedRevision int64, next ReconnectAuthority, command ReconnectCommand, now time.Time) (ReconnectRecord, bool, error) {
	if reconnectOpponentExpired(next, command.ParticipantID) {
		if err := validateExpiredOpponentTimestamp(next, command.ParticipantID, now); err != nil {
			return ReconnectRecord{}, false, err
		}
		return buildReconnectTerminalRecord(next, reconnectOpponentMust(next.Series, command.ParticipantID), command.Settlement, now,
			MutationReconnect, &command, nil, nil)
	}
	if reconnectOpponentConnected(next, command.ParticipantID) {
		if err := resumeReconnectGame(&next, now); err != nil {
			return ReconnectRecord{}, false, err
		}
	}
	advanceReconnectAuthority(&next)
	return ReconnectRecord{Kind: MutationReconnect, ReconnectCommand: &command,
		ExpectedAuthorityRevision: expectedRevision, ReconnectAuthority: next, RecordedAt: now}, true, nil
}

func validateExpiredOpponentTimestamp(authority ReconnectAuthority, participantID uuid.UUID, now time.Time) error {
	opponentInterval := reconnectCurrentInterval(authority, reconnectOpponentMust(authority.Series, participantID))
	if opponentInterval == nil || opponentInterval.ClosedAt == nil {
		return domain.ErrInternal
	}
	if !now.After(opponentInterval.UpdatedAt) || !now.After(*opponentInterval.ClosedAt) {
		return reconnectError("opponent expiry timestamp is not before reconnect")
	}
	return nil
}

func reconnectParticipantAuthority(authority ReconnectAuthority, participantID, intervalID uuid.UUID) (pause.PausePresence, pause.PauseReconnectInterval, error) {
	if err := validateReconnectAuthority(authority); err != nil {
		return pause.PausePresence{}, pause.PauseReconnectInterval{}, err
	}
	presence := reconnectPresenceByParticipant(authority.Presence, participantID)
	interval := reconnectMutationIntervalByID(authority.Reconnect, intervalID)
	if presence == nil || interval == nil || presence.State != pause.PresenceStateDisconnected ||
		interval.ParticipantID != participantID || interval.State != pause.ReconnectStateOpen ||
		interval.PresenceEpoch != presence.PresenceEpoch {
		return pause.PausePresence{}, pause.PauseReconnectInterval{}, ErrUnavailable
	}
	return *presence, *interval, nil
}

func resumeReconnectGame(authority *ReconnectAuthority, now time.Time) error {
	clock := &authority.GameClock
	if clock.ResumedAt != nil || clock.ResumedDeadline != nil || clock.Remaining <= 0 {
		return reconnectError("invalid frozen Game clock")
	}
	if clock.Revision == math.MaxInt64 {
		return reconnectError("Game clock revision overflow")
	}
	deadline, ok := pause.AddTime(now, clock.Remaining)
	if !ok {
		return reconnectError("resumed Game deadline overflows")
	}
	clock.ResumedAt = reconnectCloneTimePointer(&now)
	clock.ResumedDeadline = reconnectCloneTimePointer(&deadline)
	clock.Revision++
	if authority.Game.State == domain.GameStatePaused {
		if err := transitionReconnectGame(authority, domain.GameStateActive, nil); err != nil {
			return err
		}
	}
	return nil
}

func validReconnectCommand(command ReconnectCommand) bool {
	return command.Scope.Validate() == nil && command.CommandID != uuid.Nil &&
		command.ParticipantID != uuid.Nil && command.IntervalID != uuid.Nil && validReconnectSettlementIDs(command.Settlement)
}

func reconnectCommandsEqual(first, second ReconnectCommand) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID && first.ParticipantID == second.ParticipantID &&
		first.IntervalID == second.IntervalID && first.Settlement == second.Settlement
}

func reconnectError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidMutation, fmt.Sprintf(format, arguments...))
}
