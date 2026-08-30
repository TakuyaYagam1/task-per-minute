package arena

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const readinessAttempts = 2

var (
	ErrInvalidReadiness           = errors.New("invalid Arena readiness")
	ErrReadinessAuthorityConflict = errors.New("arena readiness authority conflict")
	ErrReadinessConflict          = errors.New("arena readiness commit conflict")
)

type ReadinessEventType string

const (
	ReadinessEventReady   ReadinessEventType = "ready"
	ReadinessEventCleared ReadinessEventType = "cleared"
)

type ReadinessScope struct {
	WaveID   uuid.UUID
	WindowID uuid.UUID
}

type ReadinessEvent struct {
	CommandID     uuid.UUID
	Scope         ReadinessScope
	ParticipantID uuid.UUID
	Type          ReadinessEventType
	OccurredAt    time.Time
}

type ReadinessAuthority struct {
	Scope    ReadinessScope
	Revision int64
	Wave     domain.ArenaWave
	Events   []ReadinessEvent
}

type ReadyCommand struct {
	Scope                    ReadinessScope
	CommandID                uuid.UUID
	ActorParticipantID       uuid.UUID
	ParticipantID            uuid.UUID
	ExpectedWaveRevisionID   domain.ArenaWaveRevisionID
	ExpectedWindowRevisionID domain.ArenaReadyWindowRevisionID
}

type DisconnectReadinessCommand struct {
	Scope                    ReadinessScope
	CommandID                uuid.UUID
	ParticipantID            uuid.UUID
	ExpectedWaveRevisionID   domain.ArenaWaveRevisionID
	ExpectedWindowRevisionID domain.ArenaReadyWindowRevisionID
}

type ReadinessCommit struct {
	Scope                    ReadinessScope
	ExpectedRevision         int64
	ExpectedWaveRevisionID   domain.ArenaWaveRevisionID
	ExpectedWindowRevisionID domain.ArenaReadyWindowRevisionID
	Wave                     domain.ArenaWave
	Event                    ReadinessEvent
}

type ReadinessRecord struct {
	Scope    ReadinessScope
	Revision int64
	Wave     domain.ArenaWave
	Events   []ReadinessEvent
}

// ReadinessRepository owns the participant-scoped compare-and-set. It must
// compare the current Wave and ready-window revisions before changing one
// participant and appending the command event.
type ReadinessRepository interface {
	LoadReadinessAuthority(ctx context.Context, scope ReadinessScope) (ReadinessAuthority, error)
	CommitReadiness(ctx context.Context, commit ReadinessCommit) (*ReadinessRecord, bool, error)
}

type ReadinessUseCase struct {
	repository ReadinessRepository
	clock      Clock
}

func NewReadinessUseCase(repository ReadinessRepository, clock Clock) *ReadinessUseCase {
	return &ReadinessUseCase{repository: repository, clock: clock}
}

func (u *ReadinessUseCase) MarkReady(
	ctx context.Context,
	command ReadyCommand,
) (*ReadinessRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if command.ActorParticipantID != command.ParticipantID || command.ParticipantID == uuid.Nil {
		return nil, false, domain.ErrArenaAssignmentParticipant
	}
	if err := validateReadinessCommand(
		command.Scope,
		command.CommandID,
		command.ParticipantID,
		command.ExpectedWaveRevisionID,
		command.ExpectedWindowRevisionID,
	); err != nil {
		return nil, false, err
	}
	return u.apply(ctx, readinessOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		expectedWaveRevisionID:   command.ExpectedWaveRevisionID,
		expectedWindowRevisionID: command.ExpectedWindowRevisionID,
		eventType:                ReadinessEventReady,
	})
}

func (u *ReadinessUseCase) ClearOnDisconnect(
	ctx context.Context,
	command DisconnectReadinessCommand,
) (*ReadinessRecord, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateReadinessCommand(
		command.Scope,
		command.CommandID,
		command.ParticipantID,
		command.ExpectedWaveRevisionID,
		command.ExpectedWindowRevisionID,
	); err != nil {
		return nil, false, err
	}
	return u.apply(ctx, readinessOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		expectedWaveRevisionID:   command.ExpectedWaveRevisionID,
		expectedWindowRevisionID: command.ExpectedWindowRevisionID,
		eventType:                ReadinessEventCleared,
	})
}

type readinessOperation struct {
	scope                    ReadinessScope
	commandID                uuid.UUID
	participantID            uuid.UUID
	expectedWaveRevisionID   domain.ArenaWaveRevisionID
	expectedWindowRevisionID domain.ArenaReadyWindowRevisionID
	eventType                ReadinessEventType
}

func (u *ReadinessUseCase) apply(
	ctx context.Context,
	operation readinessOperation,
) (*ReadinessRecord, bool, error) {
	occurredAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(occurredAt) {
		return nil, false, domain.ErrValidation
	}
	for range readinessAttempts {
		record, changed, retry, err := u.applyAttempt(ctx, operation, occurredAt)
		if retry {
			continue
		}
		return record, changed, err
	}
	return nil, false, ErrReadinessConflict
}

func (u *ReadinessUseCase) applyAttempt(
	ctx context.Context,
	operation readinessOperation,
	occurredAt time.Time,
) (*ReadinessRecord, bool, bool, error) {
	authority, err := u.repository.LoadReadinessAuthority(ctx, operation.scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("ReadinessUseCase - load authority: %w", err)
	}
	if err := validateReadinessAuthority(authority); err != nil {
		return nil, false, false, err
	}
	if event, found := readinessEventByCommand(authority.Events, operation.commandID); found {
		return reconcileReadinessEvent(authority, operation, event)
	}
	if !readinessAuthorityMatchesOperation(authority, operation) {
		return nil, false, false, ErrReadinessAuthorityConflict
	}
	commit, changed, err := buildReadinessCommit(authority, operation, occurredAt)
	if err != nil {
		return nil, false, false, err
	}
	if !changed {
		record := readinessRecordFromAuthority(authority)
		return &record, false, false, nil
	}
	return u.commitReadiness(ctx, commit)
}

func reconcileReadinessEvent(
	authority ReadinessAuthority,
	operation readinessOperation,
	event ReadinessEvent,
) (*ReadinessRecord, bool, bool, error) {
	if event.Scope != operation.scope || event.ParticipantID != operation.participantID ||
		event.Type != operation.eventType {
		return nil, false, false, ErrReadinessAuthorityConflict
	}
	record := readinessRecordFromAuthority(authority)
	return &record, false, false, nil
}

func (u *ReadinessUseCase) commitReadiness(
	ctx context.Context,
	commit ReadinessCommit,
) (*ReadinessRecord, bool, bool, error) {
	committed, changed, err := u.repository.CommitReadiness(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("ReadinessUseCase - commit event: %w", err)
	}
	if committed == nil || validateReadinessRecord(*committed) != nil ||
		!readinessRecordContainsEvent(*committed, commit.Event) {
		return nil, false, false, domain.ErrInternal
	}
	result := cloneReadinessRecord(*committed)
	return &result, changed, false, nil
}

func buildReadinessCommit(
	authority ReadinessAuthority,
	operation readinessOperation,
	occurredAt time.Time,
) (ReadinessCommit, bool, error) {
	wave := cloneArenaWaveExecution(authority.Wave)
	var changed bool
	var err error
	switch operation.eventType {
	case ReadinessEventReady:
		changed, err = wave.MarkReady(operation.scope.WindowID, operation.participantID, occurredAt)
	case ReadinessEventCleared:
		changed, err = clearParticipantReadiness(&wave, operation.participantID)
	default:
		return ReadinessCommit{}, false, readinessError("unknown operation")
	}
	if err != nil {
		return ReadinessCommit{}, false, readinessError("apply event: %v", err)
	}
	if !changed {
		return ReadinessCommit{}, false, nil
	}
	event := ReadinessEvent{
		CommandID: operation.commandID, Scope: operation.scope,
		ParticipantID: operation.participantID, Type: operation.eventType, OccurredAt: occurredAt,
	}
	commit := ReadinessCommit{
		Scope: operation.scope, ExpectedRevision: authority.Revision,
		ExpectedWaveRevisionID:   operation.expectedWaveRevisionID,
		ExpectedWindowRevisionID: operation.expectedWindowRevisionID,
		Wave:                     wave, Event: event,
	}
	if err := validateReadinessCommit(commit); err != nil {
		return ReadinessCommit{}, false, err
	}
	return commit, true, nil
}

func clearParticipantReadiness(wave *domain.ArenaWave, participantID uuid.UUID) (bool, error) {
	if wave == nil || wave.StartedAt != nil || wave.ReadyWindow == nil ||
		wave.ReadyWindow.State != domain.ArenaReadyWindowStateOpen ||
		(wave.State != domain.ArenaWaveStateReadyWindowOpen && wave.State != domain.ArenaWaveStateReady) {
		return false, ErrReadinessAuthorityConflict
	}
	for index := range wave.Members {
		if wave.Members[index].ParticipantID != participantID {
			continue
		}
		if !wave.Members[index].Ready {
			return false, nil
		}
		wave.Members[index].Ready = false
		wave.State = domain.ArenaWaveStateReadyWindowOpen
		return true, nil
	}
	return false, domain.ErrArenaAssignmentParticipant
}

func validateReadinessCommand(
	scope ReadinessScope,
	commandID uuid.UUID,
	participantID uuid.UUID,
	waveRevisionID domain.ArenaWaveRevisionID,
	windowRevisionID domain.ArenaReadyWindowRevisionID,
) error {
	if !validReadinessScope(scope) || commandID == uuid.Nil || participantID == uuid.Nil ||
		waveRevisionID.IsZero() || windowRevisionID.IsZero() {
		return readinessError("invalid command identity or revisions")
	}
	return nil
}

func validateReadinessAuthority(authority ReadinessAuthority) error {
	if !validReadinessScope(authority.Scope) || authority.Revision < 1 {
		return readinessError("invalid authority identity")
	}
	if err := authority.Wave.Validate(); err != nil {
		return readinessError("authority Wave: %v", err)
	}
	if authority.Wave.ID != authority.Scope.WaveID || authority.Wave.ReadyWindow == nil ||
		authority.Wave.ReadyWindow.ID != authority.Scope.WindowID ||
		authority.Wave.ReadyWindow.State != domain.ArenaReadyWindowStateOpen ||
		authority.Wave.StartedAt != nil {
		return ErrReadinessAuthorityConflict
	}
	return validateReadinessEvents(authority)
}

func validateReadinessEvents(authority ReadinessAuthority) error {
	states := make(map[uuid.UUID]bool, len(authority.Wave.Members))
	for _, member := range authority.Wave.Members {
		states[member.ParticipantID] = false
	}
	commands := make(map[uuid.UUID]struct{}, len(authority.Events))
	var previous time.Time
	for _, event := range authority.Events {
		if event.CommandID == uuid.Nil || event.Scope != authority.Scope ||
			!validArenaServerTime(event.OccurredAt) ||
			(!previous.IsZero() && event.OccurredAt.Before(previous)) {
			return readinessError("invalid retained event")
		}
		if _, duplicate := commands[event.CommandID]; duplicate {
			return readinessError("duplicate command event")
		}
		if _, exists := states[event.ParticipantID]; !exists {
			return readinessError("event participant is not a Wave member")
		}
		switch event.Type {
		case ReadinessEventReady:
			states[event.ParticipantID] = true
		case ReadinessEventCleared:
			states[event.ParticipantID] = false
		default:
			return readinessError("unknown retained event")
		}
		commands[event.CommandID] = struct{}{}
		previous = event.OccurredAt
	}
	for _, member := range authority.Wave.Members {
		if member.Ready != states[member.ParticipantID] {
			return readinessError("event history does not match Wave readiness")
		}
	}
	return nil
}

func validateReadinessCommit(commit ReadinessCommit) error {
	if !validReadinessScope(commit.Scope) || commit.ExpectedRevision < 1 ||
		commit.ExpectedWaveRevisionID.IsZero() || commit.ExpectedWindowRevisionID.IsZero() ||
		commit.Event.Scope != commit.Scope || commit.Event.CommandID == uuid.Nil ||
		commit.Event.ParticipantID == uuid.Nil || !validArenaServerTime(commit.Event.OccurredAt) {
		return readinessError("invalid commit identity")
	}
	if err := commit.Wave.Validate(); err != nil {
		return readinessError("commit Wave: %v", err)
	}
	return nil
}

func validateReadinessRecord(record ReadinessRecord) error {
	return validateReadinessAuthority(ReadinessAuthority(record))
}

func readinessAuthorityMatchesOperation(
	authority ReadinessAuthority,
	operation readinessOperation,
) bool {
	return authority.Scope == operation.scope && authority.Wave.RevisionID == operation.expectedWaveRevisionID &&
		authority.Wave.ReadyWindow != nil &&
		authority.Wave.ReadyWindow.RevisionID == operation.expectedWindowRevisionID
}

func readinessEventByCommand(events []ReadinessEvent, commandID uuid.UUID) (ReadinessEvent, bool) {
	for _, event := range events {
		if event.CommandID == commandID {
			return event, true
		}
	}
	return ReadinessEvent{}, false
}

func readinessRecordFromAuthority(authority ReadinessAuthority) ReadinessRecord {
	return ReadinessRecord{
		Scope: authority.Scope, Revision: authority.Revision,
		Wave:   cloneArenaWaveExecution(authority.Wave),
		Events: append([]ReadinessEvent(nil), authority.Events...),
	}
}

func readinessRecordContainsEvent(record ReadinessRecord, expected ReadinessEvent) bool {
	event, found := readinessEventByCommand(record.Events, expected.CommandID)
	return found && event == expected
}

func cloneReadinessRecord(record ReadinessRecord) ReadinessRecord {
	clone := record
	clone.Wave = cloneArenaWaveExecution(record.Wave)
	clone.Events = append([]ReadinessEvent(nil), record.Events...)
	return clone
}

func validReadinessScope(scope ReadinessScope) bool {
	return scope.WaveID != uuid.Nil && scope.WindowID != uuid.Nil
}

func readinessError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidReadiness, fmt.Sprintf(format, arguments...))
}
