package pause

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	authoritydomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/authority"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause/model"
)

const pausedPresenceAttempts = 2

var (
	ErrInvalidPausedPresence      = errors.New("invalid paused presence update")
	ErrPausedPresenceConflict     = errors.New("paused presence conflict")
	ErrPausedPresenceCommandReuse = errors.New("paused presence command reuse")
	ErrPausedPresenceSuppression  = errors.New("paused presence suppression is inactive")
	ErrPausedPresenceState        = errors.New("paused presence state does not change")
	ErrPausedPresenceOverflow     = errors.New("paused presence revision overflow")
)

type PausedPresenceExpectation struct {
	PauseID       uuid.UUID
	GraphRevision int64
	PauseRevision int64
	Presence      PausePresenceRevision
	Authority     authoritydomain.Identity
}

type PausedPresenceCommand struct {
	Scope                    pausedomain.GraphScope
	PauseID                  uuid.UUID
	CommandID                uuid.UUID
	ParticipantID            uuid.UUID
	ExpectedGraphRevision    int64
	ExpectedPauseRevision    int64
	ExpectedPresenceEpoch    int64
	ExpectedPresenceRevision int64
	NextState                pausedomain.PresenceState
}

type PausedPresenceAuthority struct {
	Pause                  NormalPauseRecord
	Presence               pausedomain.PausePresence
	Reconnect              []pausedomain.PauseReconnectInterval
	Counters               []pausedomain.PauseReconnectCounter
	FrozenDeadlines        []PauseFrozenDeadline
	TerminalActionRevision int64
}

type PausedPresenceRecord struct {
	Command   PausedPresenceCommand
	Authority PausedPresenceAuthority
	ChangedAt time.Time
}

type PausedPresenceUseCase struct {
	transactions TransactionManager
	repository   PausedPresenceRepository
	clock        PauseClock
}

func NewPausedPresenceUseCase(transactions TransactionManager, repository PausedPresenceRepository, clock PauseClock) *PausedPresenceUseCase {
	return &PausedPresenceUseCase{transactions: transactions, repository: repository, clock: clock}
}

func (u *PausedPresenceUseCase) Change(ctx context.Context, command PausedPresenceCommand) (*PausedPresenceRecord, bool, error) {
	if u == nil || u.transactions == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validatePausedPresenceCommand(command); err != nil {
		return nil, false, err
	}
	for range pausedPresenceAttempts {
		record, changed, retry, err := u.changeAttempt(ctx, command)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrPausedPresenceConflict
}

func (u *PausedPresenceUseCase) changeAttempt(ctx context.Context, command PausedPresenceCommand) (*PausedPresenceRecord, bool, bool, error) {
	var outcome pausedPresenceAttemptOutcome
	err := u.transactions.Do(ctx, func(txCtx context.Context) error {
		var err error
		outcome, err = u.changeLocked(txCtx, command)
		return err
	})
	if err != nil {
		return nil, false, false, fmt.Errorf("paused presence - transaction: %w", err)
	}
	return outcome.record, outcome.changed, outcome.retry, nil
}

type pausedPresenceAttemptOutcome struct {
	record  *PausedPresenceRecord
	changed bool
	retry   bool
}

func (u *PausedPresenceUseCase) changeLocked(ctx context.Context, command PausedPresenceCommand) (pausedPresenceAttemptOutcome, error) {
	recorded, err := u.findPausedPresenceCommand(ctx, command, "find command")
	if err != nil || recorded != nil {
		return reconcilePausedPresenceOutcome(recorded, command, err)
	}
	authority, err := u.repository.LoadPausedPresenceAuthority(ctx, command.Scope, command.ParticipantID)
	if err != nil {
		return pausedPresenceAttemptOutcome{}, fmt.Errorf("paused presence - load authority: %w", err)
	}
	recorded, err = u.findPausedPresenceCommand(ctx, command, "find locked command")
	if err != nil || recorded != nil {
		return reconcilePausedPresenceOutcome(recorded, command, err)
	}
	changedAt := u.clock.Now().Round(0).UTC()
	if !pauseValidServerTime(changedAt) {
		return pausedPresenceAttemptOutcome{}, domain.ErrValidation
	}
	if err := validatePausedPresenceAuthority(authority); err != nil {
		return pausedPresenceAttemptOutcome{}, err
	}
	if err := matchPausedPresenceCommand(authority, command); err != nil {
		return pausedPresenceAttemptOutcome{}, err
	}
	built, err := buildPausedPresenceRecord(authority, command, changedAt)
	if err != nil {
		return pausedPresenceAttemptOutcome{}, err
	}
	return u.commitPausedPresence(ctx, authority, command, built)
}

func (u *PausedPresenceUseCase) findPausedPresenceCommand(ctx context.Context, command PausedPresenceCommand, operation string) (*PausedPresenceRecord, error) {
	recorded, err := u.repository.FindPausedPresenceCommand(ctx, command.Scope.TournamentID, command.CommandID)
	if err != nil {
		return nil, fmt.Errorf("paused presence - %s: %w", operation, err)
	}
	return recorded, nil
}

func reconcilePausedPresenceOutcome(recorded *PausedPresenceRecord, command PausedPresenceCommand, err error) (pausedPresenceAttemptOutcome, error) {
	if err != nil {
		return pausedPresenceAttemptOutcome{}, err
	}
	result, err := reconcilePausedPresence(*recorded, command)
	return pausedPresenceAttemptOutcome{record: result}, err
}

func (u *PausedPresenceUseCase) commitPausedPresence(
	ctx context.Context,
	authority PausedPresenceAuthority,
	command PausedPresenceCommand,
	built PausedPresenceRecord,
) (pausedPresenceAttemptOutcome, error) {
	committed, changed, err := u.repository.CommitPausedPresence(ctx, pausedPresenceExpectation(authority, command), built)
	if errors.Is(err, domain.ErrConflict) {
		return pausedPresenceAttemptOutcome{retry: true}, nil
	}
	if err != nil {
		return pausedPresenceAttemptOutcome{}, fmt.Errorf("paused presence - commit Presence: %w", err)
	}
	if committed == nil {
		return pausedPresenceAttemptOutcome{}, domain.ErrInternal
	}
	result, err := reconcilePausedPresence(*committed, command)
	if err != nil || (changed && !pausedPresenceRecordsEqual(*result, built)) {
		return pausedPresenceAttemptOutcome{}, domain.ErrInternal
	}
	return pausedPresenceAttemptOutcome{record: result, changed: changed}, nil
}

func validatePausedPresenceCommand(command PausedPresenceCommand) error {
	if !validPauseGraphScope(command.Scope) || command.PauseID == uuid.Nil || command.CommandID == uuid.Nil ||
		command.ParticipantID == uuid.Nil || command.ExpectedGraphRevision < 1 || command.ExpectedPauseRevision < 1 ||
		command.ExpectedPresenceEpoch < 1 || command.ExpectedPresenceRevision < 1 ||
		(command.NextState != pausedomain.PresenceStateConnected && command.NextState != pausedomain.PresenceStateDisconnected) {
		return pausedPresenceError("invalid command identity, state, or expectation")
	}
	return nil
}

func validatePausedPresenceAuthority(authority PausedPresenceAuthority) error {
	if validateNormalPauseRecord(authority.Pause) != nil || authority.Pause.State != PauseStateActive ||
		!model.AllowsNormalPause(authority.Pause.Reason) || authority.TerminalActionRevision < 0 ||
		validatePausePresence(authority.Presence) != nil {
		return ErrPausedPresenceSuppression
	}
	if err := validatePausedPresenceImmutable(authority); err != nil {
		return err
	}
	snapshot, found := pausePresenceSnapshot(authority.Pause.Graph.Presence, authority.Presence.ParticipantID)
	if !found || !pausePresenceTracksSnapshot(snapshot, authority.Presence, authority.Pause.PausedAt) {
		return ErrPausedPresenceConflict
	}
	return nil
}

func pausePresenceTracksSnapshot(snapshot, current pausedomain.PausePresence, pausedAt time.Time) bool {
	if !samePausePresenceIdentity(snapshot, current) || current.PresenceEpoch < snapshot.PresenceEpoch ||
		current.Revision < snapshot.Revision {
		return false
	}
	epochDelta := current.PresenceEpoch - snapshot.PresenceEpoch
	revisionDelta := current.Revision - snapshot.Revision
	if epochDelta != revisionDelta {
		return false
	}
	if epochDelta == 0 {
		return reflect.DeepEqual(snapshot, current)
	}
	return !current.UpdatedAt.Before(pausedAt)
}

func validatePausedPresenceImmutable(authority PausedPresenceAuthority) error {
	if !reflect.DeepEqual(authority.Reconnect, authority.Pause.Graph.Reconnect) ||
		!reflect.DeepEqual(authority.Counters, authority.Pause.Graph.Counters) ||
		!reflect.DeepEqual(authority.FrozenDeadlines, authority.Pause.Graph.FrozenDeadlines) ||
		authority.TerminalActionRevision != authority.Pause.Graph.TerminalActionRevision {
		return pausedPresenceError("mutable pause evidence")
	}
	for _, interval := range authority.Reconnect {
		if err := validatePauseReconnect(interval); err != nil {
			return pausedPresenceError("Reconnect evidence: %v", err)
		}
	}
	for _, counter := range authority.Counters {
		if !validPauseReconnectCounter(counter) {
			return pausedPresenceError("invalid reconnect counter")
		}
	}
	for _, frozen := range authority.FrozenDeadlines {
		if err := validateFrozenDeadline(frozen, true); err != nil {
			return pausedPresenceError("frozen deadline: %v", err)
		}
	}
	return nil
}

func matchPausedPresenceCommand(authority PausedPresenceAuthority, command PausedPresenceCommand) error {
	pause := authority.Pause
	if pause.Scope != command.Scope || pause.PauseID != command.PauseID || pause.Graph.Revision != command.ExpectedGraphRevision ||
		pause.Revision != command.ExpectedPauseRevision || pause.Scope.Authority != command.Scope.Authority ||
		authority.Presence.ParticipantID != command.ParticipantID || authority.Presence.PresenceEpoch != command.ExpectedPresenceEpoch ||
		authority.Presence.Revision != command.ExpectedPresenceRevision {
		return ErrPausedPresenceConflict
	}
	found := false
	for _, snapshot := range pause.Graph.Presence {
		if snapshot.ParticipantID == command.ParticipantID {
			found = samePausePresenceIdentity(snapshot, authority.Presence)
			break
		}
	}
	if !found {
		return ErrPausedPresenceConflict
	}
	if authority.Presence.State == command.NextState {
		return ErrPausedPresenceState
	}
	return nil
}

func buildPausedPresenceRecord(authority PausedPresenceAuthority, command PausedPresenceCommand, changedAt time.Time) (PausedPresenceRecord, error) {
	if changedAt.Before(authority.Pause.PausedAt) || changedAt.Before(authority.Presence.UpdatedAt) {
		return PausedPresenceRecord{}, pausedPresenceError("change time precedes durable evidence")
	}
	if authority.Presence.PresenceEpoch == math.MaxInt64 || authority.Presence.Revision == math.MaxInt64 {
		return PausedPresenceRecord{}, ErrPausedPresenceOverflow
	}
	next := clonePausedPresenceAuthority(authority)
	next.Presence.PresenceEpoch++
	next.Presence.Revision++
	next.Presence.State = command.NextState
	next.Presence.UpdatedAt = changedAt
	switch command.NextState {
	case pausedomain.PresenceStateConnected:
		next.Presence.ConnectedAt = changedAt
		next.Presence.DisconnectedAt = nil
	case pausedomain.PresenceStateDisconnected:
		next.Presence.DisconnectedAt = pauseCloneTimePointer(&changedAt)
	default:
		return PausedPresenceRecord{}, pausedPresenceError("unknown next state")
	}
	if err := validatePausePresence(next.Presence); err != nil {
		return PausedPresenceRecord{}, err
	}
	if !pausedPresenceImmutableEqual(authority, next) {
		return PausedPresenceRecord{}, domain.ErrInternal
	}
	record := PausedPresenceRecord{Command: command, Authority: next, ChangedAt: changedAt}
	if err := validatePausedPresenceRecord(record); err != nil {
		return PausedPresenceRecord{}, err
	}
	return clonePausedPresenceRecord(record), nil
}

func validatePausedPresenceRecord(record PausedPresenceRecord) error {
	if validatePausedPresenceCommand(record.Command) != nil || !pauseValidServerTime(record.ChangedAt) ||
		validatePausedPresenceAuthority(record.Authority) != nil || record.Authority.Presence.State != record.Command.NextState ||
		record.Authority.Pause.Scope != record.Command.Scope || record.Authority.Pause.PauseID != record.Command.PauseID ||
		record.Authority.Pause.Graph.Revision != record.Command.ExpectedGraphRevision || record.Authority.Pause.Revision != record.Command.ExpectedPauseRevision ||
		record.Authority.Presence.ParticipantID != record.Command.ParticipantID ||
		record.Authority.Presence.PresenceEpoch != record.Command.ExpectedPresenceEpoch+1 ||
		record.Authority.Presence.Revision != record.Command.ExpectedPresenceRevision+1 ||
		!record.Authority.Presence.UpdatedAt.Equal(record.ChangedAt) ||
		!pausedPresenceEventTimeMatches(record.Authority.Presence, record.Command.NextState, record.ChangedAt) {
		return pausedPresenceError("invalid committed record")
	}
	return nil
}

func pausedPresenceEventTimeMatches(presence pausedomain.PausePresence, state pausedomain.PresenceState, changedAt time.Time) bool {
	switch state {
	case pausedomain.PresenceStateConnected:
		return presence.DisconnectedAt == nil && presence.ConnectedAt.Equal(changedAt)
	case pausedomain.PresenceStateDisconnected:
		return presence.DisconnectedAt != nil && presence.DisconnectedAt.Equal(changedAt)
	default:
		return false
	}
}

func pausePresenceSnapshot(values []pausedomain.PausePresence, participantID uuid.UUID) (pausedomain.PausePresence, bool) {
	for _, value := range values {
		if value.ParticipantID == participantID {
			return value, true
		}
	}
	return pausedomain.PausePresence{}, false
}

func samePausePresenceIdentity(first, second pausedomain.PausePresence) bool {
	return first.ID == second.ID && first.TournamentID == second.TournamentID && first.RosterID == second.RosterID &&
		first.SeriesID == second.SeriesID && first.ParticipantID == second.ParticipantID
}

func reconcilePausedPresence(record PausedPresenceRecord, command PausedPresenceCommand) (*PausedPresenceRecord, error) {
	if validatePausedPresenceRecord(record) != nil || record.Command != command {
		return nil, ErrPausedPresenceCommandReuse
	}
	clone := clonePausedPresenceRecord(record)
	return &clone, nil
}

func pausedPresenceExpectation(authority PausedPresenceAuthority, command PausedPresenceCommand) PausedPresenceExpectation {
	presence := authority.Presence
	return PausedPresenceExpectation{
		PauseID: command.PauseID, GraphRevision: command.ExpectedGraphRevision,
		PauseRevision: command.ExpectedPauseRevision, Authority: command.Scope.Authority,
		Presence: PausePresenceRevision{
			ID: presence.ID, TournamentID: presence.TournamentID, RosterID: presence.RosterID,
			SeriesID: presence.SeriesID, ParticipantID: presence.ParticipantID,
			PresenceEpoch: command.ExpectedPresenceEpoch, Revision: command.ExpectedPresenceRevision,
		},
	}
}

func pausedPresenceImmutableEqual(first, second PausedPresenceAuthority) bool {
	return reflect.DeepEqual(first.Pause, second.Pause) && reflect.DeepEqual(first.Reconnect, second.Reconnect) &&
		reflect.DeepEqual(first.Counters, second.Counters) && reflect.DeepEqual(first.FrozenDeadlines, second.FrozenDeadlines) &&
		first.TerminalActionRevision == second.TerminalActionRevision
}

func pausedPresenceRecordsEqual(first, second PausedPresenceRecord) bool {
	return first.Command == second.Command && first.ChangedAt.Equal(second.ChangedAt) && reflect.DeepEqual(first.Authority, second.Authority)
}

func clonePausedPresenceAuthority(value PausedPresenceAuthority) PausedPresenceAuthority {
	clone := value
	clone.Pause = cloneNormalPauseRecord(value.Pause)
	clone.Presence = value.Presence
	clone.Presence.DisconnectedAt = pauseCloneTimePointer(value.Presence.DisconnectedAt)
	clone.Reconnect = append([]pausedomain.PauseReconnectInterval(nil), value.Reconnect...)
	for index := range clone.Reconnect {
		clone.Reconnect[index].ClosedAt = pauseCloneTimePointer(value.Reconnect[index].ClosedAt)
	}
	clone.Counters = append([]pausedomain.PauseReconnectCounter(nil), value.Counters...)
	clone.FrozenDeadlines = append([]PauseFrozenDeadline(nil), value.FrozenDeadlines...)
	for index := range clone.FrozenDeadlines {
		clone.FrozenDeadlines[index].ResumedAt = pauseCloneTimePointer(value.FrozenDeadlines[index].ResumedAt)
		clone.FrozenDeadlines[index].ResumedDeadline = pauseCloneTimePointer(value.FrozenDeadlines[index].ResumedDeadline)
	}
	return clone
}

func clonePausedPresenceRecord(value PausedPresenceRecord) PausedPresenceRecord {
	clone := value
	clone.Authority = clonePausedPresenceAuthority(value.Authority)
	return clone
}

func pausedPresenceError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPausedPresence, fmt.Sprintf(format, arguments...))
}
