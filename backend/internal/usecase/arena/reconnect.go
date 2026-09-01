package arena

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidReconnectMutation = errors.New("invalid arena reconnect mutation")
	ErrReconnectConflict        = errors.New("arena reconnect authority conflict")
	ErrReconnectCommandReuse    = errors.New("arena reconnect command was reused")
	ErrReconnectDeadline        = errors.New("arena reconnect deadline reached")
	ErrReconnectUnavailable     = errors.New("arena reconnect mutation is unavailable")
)

type ReconnectMutationKind string

const (
	ReconnectMutationReconnect  ReconnectMutationKind = "reconnect"
	ReconnectMutationTimeout    ReconnectMutationKind = "timeout"
	ReconnectMutationDisconnect ReconnectMutationKind = "disconnect"
)

type ReconnectAuthority struct {
	Scope                        PauseGraphScope
	Revision                     int64
	PauseID                      uuid.UUID
	GameRevision                 int64
	SeriesRevision               int64
	CurrentOrdinal               int
	CurrentProjectionRevision    int64
	CurrentGameResultRevisionIDs []domain.ArenaOfficialResultRevisionID
	Game                         domain.ArenaGame
	Series                       domain.ArenaSeries
	GameClock                    PauseResumeGameClock
	Presence                     []PausePresence
	Reconnect                    []PauseReconnectInterval
	Counters                     []PauseReconnectCounter
	Current                      *ReconnectTerminalOutcome
}

type ReconnectCommand struct {
	Scope         PauseGraphScope
	CommandID     uuid.UUID
	ParticipantID uuid.UUID
	IntervalID    uuid.UUID
	Settlement    ReconnectSettlementIDs
}

type ReconnectSettlementIDs struct {
	GameResultRevisionID   domain.ArenaOfficialResultRevisionID
	ScoreRevisionID        domain.ArenaSeriesScoreRevisionID
	SeriesResultRevisionID domain.ArenaOfficialResultRevisionID
	ReplayRouteID          uuid.UUID
	AuditEventID           uuid.UUID
	OutboxEventID          uuid.UUID
	ProjectionRevisionID   uuid.UUID
}

type ReconnectTerminalOutcome struct {
	GameResultRevision     *ForfeitGameRevision
	VoidGameResultRevision *FailedGameResultRevision
	ScoreRevision          ArenaSettlementScoreRevision
	SeriesResultRevision   *ForfeitSeriesRevision
	ReplayRoute            *FailedWaveMemberRoute
	Evidence               ArenaSettlementEvidence
	TerminalizedAt         time.Time
}

type ReconnectTimeoutCommand struct {
	Scope         PauseGraphScope
	CommandID     uuid.UUID
	ParticipantID uuid.UUID
	IntervalID    uuid.UUID
	Settlement    ReconnectSettlementIDs
}

type DisconnectCommand struct {
	Scope           PauseGraphScope
	CommandID       uuid.UUID
	ParticipantID   uuid.UUID
	IntervalID      uuid.UUID
	Deadline        time.Time
	ContinuedFromID *uuid.UUID
	Settlement      ReconnectSettlementIDs
}

type ReconnectRecord struct {
	Kind                      ReconnectMutationKind
	ReconnectCommand          *ReconnectCommand
	TimeoutCommand            *ReconnectTimeoutCommand
	DisconnectCommand         *DisconnectCommand
	ExpectedAuthorityRevision int64
	Authority                 ReconnectAuthority
	GameResultRevision        *ForfeitGameRevision
	VoidGameResultRevision    *FailedGameResultRevision
	ScoreRevision             *ArenaSettlementScoreRevision
	SeriesResultRevision      *ForfeitSeriesRevision
	ReplayRoute               *FailedWaveMemberRoute
	Evidence                  *ArenaSettlementEvidence
	RecordedAt                time.Time
}

// ReconnectRepository is the sole authority for reconnect receipts and the
// aggregate compare-and-set. The commit persists every state change in one
// transaction and returns domain.ErrConflict when the expected revision lost.
type ReconnectRepository interface {
	FindReconnectCommand(ctx context.Context, tournamentID, commandID uuid.UUID) (*ReconnectRecord, error)
	LoadReconnectAuthority(ctx context.Context, scope PauseGraphScope) (ReconnectAuthority, error)
	CommitReconnectMutation(ctx context.Context, expectedRevision int64, record ReconnectRecord) (*ReconnectRecord, bool, error)
}

type ReconnectUseCase struct {
	repository ReconnectRepository
	clock      Clock
}

func NewReconnectUseCase(repository ReconnectRepository, clock Clock) *ReconnectUseCase {
	return &ReconnectUseCase{repository: repository, clock: clock}
}

func (u *ReconnectUseCase) Reconnect(ctx context.Context, command ReconnectCommand) (*ReconnectRecord, bool, error) {
	command = cloneReconnectCommand(command)
	if u == nil || u.repository == nil || u.clock == nil || !validReconnectCommand(command) {
		return nil, false, domain.ErrValidation
	}
	return runClockedReconnectMutation(ctx, u.repository, u.clock, command.Scope, command.CommandID,
		func(record ReconnectRecord) bool {
			return record.Kind == ReconnectMutationReconnect && record.ReconnectCommand != nil && reconnectCommandsEqual(*record.ReconnectCommand, command)
		}, reconnectMutationBuilder(command))
}

func reconnectMutationBuilder(command ReconnectCommand) reconnectRecordBuilder {
	return func(authority ReconnectAuthority, now time.Time) (ReconnectRecord, bool, error) {
		if authority.Current != nil {
			return reconnectTerminalReplayRecord(authority, now, ReconnectMutationReconnect, &command, nil, nil), false, nil
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
		return ReconnectAuthority{}, ErrReconnectDeadline
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

func validateReconnectWritable(revision int64, presence PausePresence, interval PauseReconnectInterval, now time.Time) error {
	if revision == math.MaxInt64 || presence.PresenceEpoch == math.MaxInt64 || presence.Revision == math.MaxInt64 || interval.Revision == math.MaxInt64 {
		return reconnectError("reconnect revision overflow")
	}
	if !now.After(presence.UpdatedAt) || !now.After(interval.UpdatedAt) {
		return reconnectError("reconnect mutation timestamp did not advance")
	}
	return nil
}

func markReconnectConnected(presence *PausePresence, previousEpoch int64, now time.Time) {
	presence.State = PresenceStateConnected
	presence.PresenceEpoch = previousEpoch + 1
	presence.Revision++
	presence.ConnectedAt = now
	presence.DisconnectedAt = nil
	presence.UpdatedAt = now
}

func markReconnectIntervalClosed(interval *PauseReconnectInterval, now time.Time) {
	interval.State = ReconnectStateReconnected
	interval.ClosedAt = cloneArenaTimePointer(&now)
	interval.Revision++
	interval.UpdatedAt = now
}

func finishReconnectMutation(expectedRevision int64, next ReconnectAuthority, command ReconnectCommand, now time.Time) (ReconnectRecord, bool, error) {
	if reconnectOpponentExpired(next, command.ParticipantID) {
		if err := validateExpiredOpponentTimestamp(next, command.ParticipantID, now); err != nil {
			return ReconnectRecord{}, false, err
		}
		return buildReconnectTerminalRecord(next, reconnectOpponentMust(next.Series, command.ParticipantID), command.Settlement, now,
			ReconnectMutationReconnect, &command, nil, nil)
	}
	if reconnectOpponentConnected(next, command.ParticipantID) {
		if err := resumeReconnectGame(&next, now); err != nil {
			return ReconnectRecord{}, false, err
		}
	}
	advanceReconnectAuthority(&next)
	return ReconnectRecord{Kind: ReconnectMutationReconnect, ReconnectCommand: &command,
		ExpectedAuthorityRevision: expectedRevision, Authority: next, RecordedAt: now}, true, nil
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

func reconnectParticipantAuthority(authority ReconnectAuthority, participantID, intervalID uuid.UUID) (PausePresence, PauseReconnectInterval, error) {
	if err := validateReconnectAuthority(authority); err != nil {
		return PausePresence{}, PauseReconnectInterval{}, err
	}
	presence := reconnectPresenceByParticipant(authority.Presence, participantID)
	interval := reconnectMutationIntervalByID(authority.Reconnect, intervalID)
	if presence == nil || interval == nil || presence.State != PresenceStateDisconnected ||
		interval.ParticipantID != participantID || interval.State != ReconnectStateOpen ||
		interval.PresenceEpoch != presence.PresenceEpoch {
		return PausePresence{}, PauseReconnectInterval{}, ErrReconnectUnavailable
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
	deadline, ok := safePauseTimeAdd(now, clock.Remaining)
	if !ok {
		return reconnectError("resumed Game deadline overflows")
	}
	clock.ResumedAt = cloneArenaTimePointer(&now)
	clock.ResumedDeadline = cloneArenaTimePointer(&deadline)
	clock.Revision++
	if authority.Game.State == domain.ArenaGameStatePaused {
		if err := transitionReconnectGame(authority, domain.ArenaGameStateActive, nil); err != nil {
			return err
		}
	}
	return nil
}

func validReconnectCommand(command ReconnectCommand) bool {
	return validPauseGraphScope(command.Scope) && command.CommandID != uuid.Nil &&
		command.ParticipantID != uuid.Nil && command.IntervalID != uuid.Nil && validReconnectSettlementIDs(command.Settlement)
}

func reconnectCommandsEqual(first, second ReconnectCommand) bool {
	return first.Scope == second.Scope && first.CommandID == second.CommandID && first.ParticipantID == second.ParticipantID &&
		first.IntervalID == second.IntervalID && first.Settlement == second.Settlement
}

func reconnectError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReconnectMutation, fmt.Sprintf(format, arguments...))
}
