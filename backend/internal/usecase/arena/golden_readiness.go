package arena

import (
	"context"
	"crypto/sha256"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenMarkReadyCommand struct {
	Scope              GoldenStateScope
	CommandID          uuid.UUID
	ActorParticipantID uuid.UUID
	ParticipantID      uuid.UUID
	AttemptID          uuid.UUID
	WaveID             uuid.UUID
	WindowID           uuid.UUID
	ExpectedState      GoldenStateExpectation
	ExpectedExecution  GoldenWaveExecutionExpectation

	NextExecutionRevisionID uuid.UUID
	NextWindowRevisionID    uuid.UUID
	NextReadinessRevisionID uuid.UUID
}

type GoldenReadyDisconnectCommand struct {
	Scope             GoldenStateScope
	CommandID         uuid.UUID
	ParticipantID     uuid.UUID
	AttemptID         uuid.UUID
	WaveID            uuid.UUID
	WindowID          uuid.UUID
	ExpectedState     GoldenStateExpectation
	ExpectedExecution GoldenWaveExecutionExpectation

	NextExecutionRevisionID uuid.UUID
	NextWindowRevisionID    uuid.UUID
	NextReadinessRevisionID uuid.UUID
	NextPresenceRevisionID  uuid.UUID
}

type GoldenReconnectCommand struct {
	Scope              GoldenStateScope
	CommandID          uuid.UUID
	ActorParticipantID uuid.UUID
	ParticipantID      uuid.UUID
	AttemptID          uuid.UUID
	WaveID             uuid.UUID
	WindowID           uuid.UUID
	ExpectedState      GoldenStateExpectation
	ExpectedExecution  GoldenWaveExecutionExpectation

	NextExecutionRevisionID uuid.UUID
	NextWindowRevisionID    uuid.UUID
	NextPresenceRevisionID  uuid.UUID
}

type GoldenReadinessUseCase struct {
	repository GoldenWaveRepository
	clock      Clock
}

func NewGoldenReadinessUseCase(repository GoldenWaveRepository, clock Clock) *GoldenReadinessUseCase {
	return &GoldenReadinessUseCase{repository: repository, clock: clock}
}

func (u *GoldenReadinessUseCase) MarkReady(
	ctx context.Context,
	command GoldenMarkReadyCommand,
) (*GoldenWaveExecution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateGoldenMarkReadyCommand(command); err != nil {
		return nil, false, err
	}
	if command.ActorParticipantID != command.ParticipantID {
		return nil, false, domain.ErrArenaAssignmentParticipant
	}
	return u.apply(ctx, goldenReadinessOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		attemptID: command.AttemptID, waveID: command.WaveID, windowID: command.WindowID,
		expectedState: command.ExpectedState, expectedExecution: command.ExpectedExecution,
		nextExecutionRevisionID: command.NextExecutionRevisionID,
		nextWindowRevisionID:    command.NextWindowRevisionID,
		nextReadinessRevisionID: command.NextReadinessRevisionID,
		kind:                    GoldenWaveCommandReady,
	})
}

func (u *GoldenReadinessUseCase) Disconnect(
	ctx context.Context,
	command GoldenReadyDisconnectCommand,
) (*GoldenWaveExecution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateGoldenReadyDisconnectCommand(command); err != nil {
		return nil, false, err
	}
	return u.apply(ctx, goldenReadinessOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		attemptID: command.AttemptID, waveID: command.WaveID, windowID: command.WindowID,
		expectedState: command.ExpectedState, expectedExecution: command.ExpectedExecution,
		nextExecutionRevisionID: command.NextExecutionRevisionID,
		nextWindowRevisionID:    command.NextWindowRevisionID,
		nextReadinessRevisionID: command.NextReadinessRevisionID,
		nextPresenceRevisionID:  command.NextPresenceRevisionID,
		kind:                    GoldenWaveCommandDisconnected,
	})
}

func (u *GoldenReadinessUseCase) Reconnect(
	ctx context.Context,
	command GoldenReconnectCommand,
) (*GoldenWaveExecution, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateGoldenReconnectCommand(command); err != nil {
		return nil, false, err
	}
	if command.ActorParticipantID != command.ParticipantID {
		return nil, false, domain.ErrArenaAssignmentParticipant
	}
	return u.apply(ctx, goldenReadinessOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		attemptID: command.AttemptID, waveID: command.WaveID, windowID: command.WindowID,
		expectedState: command.ExpectedState, expectedExecution: command.ExpectedExecution,
		nextExecutionRevisionID: command.NextExecutionRevisionID,
		nextWindowRevisionID:    command.NextWindowRevisionID,
		nextPresenceRevisionID:  command.NextPresenceRevisionID,
		kind:                    GoldenWaveCommandReconnected,
	})
}

type goldenReadinessOperation struct {
	scope             GoldenStateScope
	commandID         uuid.UUID
	participantID     uuid.UUID
	attemptID         uuid.UUID
	waveID            uuid.UUID
	windowID          uuid.UUID
	expectedState     GoldenStateExpectation
	expectedExecution GoldenWaveExecutionExpectation

	nextExecutionRevisionID uuid.UUID
	nextWindowRevisionID    uuid.UUID
	nextReadinessRevisionID uuid.UUID
	nextPresenceRevisionID  uuid.UUID
	kind                    GoldenWaveCommandKind
}

func (u *GoldenReadinessUseCase) apply(
	ctx context.Context,
	operation goldenReadinessOperation,
) (*GoldenWaveExecution, bool, error) {
	digest := goldenReadinessCommandDigest(operation)
	if replay, found, err := findGoldenWaveReplay(ctx, u.repository, operation.scope, operation.commandID); err != nil {
		return nil, false, err
	} else if found {
		return reconcileGoldenWaveReplay(operation.scope, operation.commandID,
			operation.kind, digest, replay)
	}
	occurredAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(occurredAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenWaveCommitAttempts {
		execution, changed, retry, err := u.applyAttempt(ctx, operation, digest, occurredAt)
		if retry {
			continue
		}
		return execution, changed, err
	}
	return nil, false, ErrGoldenWaveCommitConflict
}

func (u *GoldenReadinessUseCase) applyAttempt(
	ctx context.Context,
	operation goldenReadinessOperation,
	digest [sha256.Size]byte,
	occurredAt time.Time,
) (*GoldenWaveExecution, bool, bool, error) {
	loaded, err := loadGoldenWaveCommandAttempt(
		ctx, u.repository, operation.scope, operation.commandID, operation.kind, digest,
	)
	if err != nil {
		return nil, false, false, err
	}
	if loaded.Replay != nil {
		return loaded.Replay, false, false, nil
	}
	execution := loaded.Execution
	if execution == nil {
		return nil, false, false, ErrGoldenWaveExecutionNotFound
	}
	state, err := loadValidGoldenState(ctx, u.repository, operation.scope)
	if err != nil {
		return nil, false, false, err
	}
	if err := validateGoldenReadinessAttempt(state, *execution, operation, occurredAt); err != nil {
		return nil, false, false, err
	}
	next, err := buildGoldenReadinessSuccessor(*execution, operation, digest, occurredAt)
	if err != nil {
		return nil, false, false, err
	}
	expected := execution.Expectation()
	commit := GoldenWaveExecutionCommit{
		ExpectedState: operation.expectedState, ExpectedExecution: &expected,
		NewIdentityIDs: goldenReadinessOperationIdentityIDs(operation), Next: next,
	}
	canonicalGoldenIDs(commit.NewIdentityIDs)
	return commitGoldenWaveExecution(ctx, u.repository, commit, operation.commandID, operation.kind, digest)
}

func validateGoldenReadinessAttempt(
	state GoldenState,
	execution GoldenWaveExecution,
	operation goldenReadinessOperation,
	occurredAt time.Time,
) error {
	if !goldenReadinessExpectationMatches(state, execution, operation) {
		return ErrGoldenWaveAuthorityConflict
	}
	if err := validateGoldenExecutionSource(state, execution); err != nil {
		return err
	}
	if !goldenReadinessWindowOpen(execution, occurredAt) {
		return ErrGoldenReadyWindowClosed
	}
	if !goldenIDsContain(execution.Membership.ParticipantIDs, operation.participantID) {
		return domain.ErrArenaAssignmentParticipant
	}
	return validateGoldenReadinessFreshIDs(state, execution, operation)
}

func goldenReadinessExpectationMatches(
	state GoldenState,
	execution GoldenWaveExecution,
	operation goldenReadinessOperation,
) bool {
	return state.Expectation().Equal(operation.expectedState) && execution.Source.Equal(operation.expectedState) &&
		execution.Expectation().Equal(operation.expectedExecution) &&
		execution.Attempt.ID == operation.attemptID && execution.Wave.ID == operation.waveID &&
		execution.Window.ID == operation.windowID
}

func goldenReadinessWindowOpen(execution GoldenWaveExecution, occurredAt time.Time) bool {
	return execution.Wave.StartedAt == nil &&
		execution.Attempt.State == domain.ArenaGoldenAttemptStateWaitingReady &&
		execution.Window.State == GoldenReadyWindowOpen &&
		!occurredAt.Before(execution.OpenedAt) && !occurredAt.After(execution.Deadline)
}

func buildGoldenReadinessSuccessor(
	execution GoldenWaveExecution,
	operation goldenReadinessOperation,
	digest [sha256.Size]byte,
	occurredAt time.Time,
) (GoldenWaveExecution, error) {
	if execution.Revision == math.MaxInt64 {
		return GoldenWaveExecution{}, ErrGoldenWaveRevisionOverflow
	}
	next := execution.Snapshot()
	expected := execution.Expectation()
	next.PreviousRevisionID = goldenUUID(next.RevisionID)
	next.RevisionID = operation.nextExecutionRevisionID
	next.Revision++
	var err error
	switch operation.kind {
	case GoldenWaveCommandReady:
		err = applyGoldenMarkReady(&next, operation, occurredAt)
	case GoldenWaveCommandDisconnected:
		err = applyGoldenDisconnect(&next, operation)
	case GoldenWaveCommandReconnected:
		err = applyGoldenReconnect(&next, operation)
	case GoldenWaveCommandOpened, GoldenWaveCommandStarted:
		err = goldenWaveError("readiness operation uses a non-readiness command kind")
	default:
		err = goldenWaveError("unknown readiness operation")
	}
	if err != nil {
		return GoldenWaveExecution{}, err
	}
	next.Window.ReadinessDigest = goldenParticipantSetDigest(next.Window.ReadyParticipantIDs)
	next.Window.PresenceDigest = goldenParticipantSetDigest(next.Window.PresentParticipantIDs)
	next.Receipts = append(next.Receipts, GoldenWaveCommandReceipt{
		CommandID: operation.commandID, Scope: operation.scope, Kind: operation.kind,
		CommandDigest: digest, Expected: &expected, Result: next.Expectation(),
		OccurredAt: occurredAt, ParticipantID: operation.participantID,
		UnusedIdentityIDs: goldenReadinessUnusedIdentities(operation, expected, next.Expectation()),
	})
	if err := sealGoldenWaveExecution(&next); err != nil {
		return GoldenWaveExecution{}, err
	}
	if err := next.Validate(); err != nil {
		return GoldenWaveExecution{}, err
	}
	return next.Snapshot(), nil
}

func applyGoldenMarkReady(
	execution *GoldenWaveExecution,
	operation goldenReadinessOperation,
	occurredAt time.Time,
) error {
	if !goldenIDsContain(execution.Window.PresentParticipantIDs, operation.participantID) {
		return ErrGoldenWaveAuthorityConflict
	}
	if goldenIDsContain(execution.Window.ReadyParticipantIDs, operation.participantID) {
		return nil
	}
	if execution.Window.Revision == math.MaxInt64 || execution.Window.ReadinessRevision == math.MaxInt64 {
		return ErrGoldenWaveRevisionOverflow
	}
	changed, err := execution.Wave.MarkReady(execution.Window.ID, operation.participantID, occurredAt)
	if err != nil || !changed {
		return goldenWaveError("mark Wave ready: %v", err)
	}
	advanceGoldenWindowState(&execution.Window, operation.nextWindowRevisionID)
	advanceGoldenReadiness(&execution.Window, operation.nextReadinessRevisionID)
	execution.Window.ReadyParticipantIDs = append(execution.Window.ReadyParticipantIDs, operation.participantID)
	canonicalGoldenIDs(execution.Window.ReadyParticipantIDs)
	return nil
}

func applyGoldenDisconnect(execution *GoldenWaveExecution, operation goldenReadinessOperation) error {
	if !goldenIDsContain(execution.Window.PresentParticipantIDs, operation.participantID) {
		return nil
	}
	if !goldenIDsContain(execution.Window.ReadyParticipantIDs, operation.participantID) {
		return nil
	}
	if execution.Window.Revision == math.MaxInt64 || execution.Window.PresenceRevision == math.MaxInt64 {
		return ErrGoldenWaveRevisionOverflow
	}
	advanceGoldenWindowState(&execution.Window, operation.nextWindowRevisionID)
	execution.Window.PresencePreviousRevisionID = goldenUUID(execution.Window.PresenceRevisionID)
	execution.Window.PresenceRevisionID = operation.nextPresenceRevisionID
	execution.Window.PresenceRevision++
	execution.Window.PresentParticipantIDs = removeGoldenID(
		execution.Window.PresentParticipantIDs,
		operation.participantID,
	)
	if execution.Window.ReadinessRevision == math.MaxInt64 {
		return ErrGoldenWaveRevisionOverflow
	}
	advanceGoldenReadiness(&execution.Window, operation.nextReadinessRevisionID)
	execution.Window.ReadyParticipantIDs = removeGoldenID(execution.Window.ReadyParticipantIDs, operation.participantID)
	for index := range execution.Wave.Members {
		if execution.Wave.Members[index].ParticipantID == operation.participantID {
			execution.Wave.Members[index].Ready = false
			execution.Wave.State = domain.ArenaWaveStateReadyWindowOpen
			return nil
		}
	}
	return domain.ErrArenaAssignmentParticipant
}

func applyGoldenReconnect(execution *GoldenWaveExecution, operation goldenReadinessOperation) error {
	if goldenIDsContain(execution.Window.PresentParticipantIDs, operation.participantID) {
		return nil
	}
	if goldenIDsContain(execution.Window.ReadyParticipantIDs, operation.participantID) {
		return goldenWaveError("absent participant retained readiness")
	}
	if execution.Window.Revision == math.MaxInt64 || execution.Window.PresenceRevision == math.MaxInt64 {
		return ErrGoldenWaveRevisionOverflow
	}
	advanceGoldenWindowState(&execution.Window, operation.nextWindowRevisionID)
	execution.Window.PresencePreviousRevisionID = goldenUUID(execution.Window.PresenceRevisionID)
	execution.Window.PresenceRevisionID = operation.nextPresenceRevisionID
	execution.Window.PresenceRevision++
	execution.Window.PresentParticipantIDs = append(execution.Window.PresentParticipantIDs, operation.participantID)
	canonicalGoldenIDs(execution.Window.PresentParticipantIDs)
	return nil
}

func advanceGoldenWindowState(window *GoldenReadyWindow, revisionID uuid.UUID) {
	window.PreviousRevisionID = goldenUUID(window.RevisionID)
	window.RevisionID = revisionID
	window.Revision++
}

func advanceGoldenReadiness(window *GoldenReadyWindow, revisionID uuid.UUID) {
	window.ReadinessPreviousRevisionID = goldenUUID(window.ReadinessRevisionID)
	window.ReadinessRevisionID = revisionID
	window.ReadinessRevision++
}

func validateGoldenMarkReadyCommand(command GoldenMarkReadyCommand) error {
	if !validGoldenReadinessCommandBase(
		command.Scope, command.CommandID, command.ParticipantID, command.AttemptID,
		command.WaveID, command.WindowID, command.ExpectedState, command.ExpectedExecution,
		command.NextExecutionRevisionID, command.NextWindowRevisionID,
	) || command.ActorParticipantID == uuid.Nil || command.NextReadinessRevisionID == uuid.Nil {
		return goldenWaveError("invalid ready command identity")
	}
	return nil
}

func validateGoldenReadyDisconnectCommand(command GoldenReadyDisconnectCommand) error {
	if !validGoldenReadinessCommandBase(
		command.Scope, command.CommandID, command.ParticipantID, command.AttemptID,
		command.WaveID, command.WindowID, command.ExpectedState, command.ExpectedExecution,
		command.NextExecutionRevisionID, command.NextWindowRevisionID,
	) || command.NextReadinessRevisionID == uuid.Nil || command.NextPresenceRevisionID == uuid.Nil {
		return goldenWaveError("invalid disconnect command identity")
	}
	return nil
}

func validateGoldenReconnectCommand(command GoldenReconnectCommand) error {
	if !validGoldenReadinessCommandBase(
		command.Scope, command.CommandID, command.ParticipantID, command.AttemptID,
		command.WaveID, command.WindowID, command.ExpectedState, command.ExpectedExecution,
		command.NextExecutionRevisionID, command.NextWindowRevisionID,
	) || command.ActorParticipantID == uuid.Nil || command.NextPresenceRevisionID == uuid.Nil {
		return goldenWaveError("invalid reconnect command identity")
	}
	return nil
}

func validGoldenReadinessCommandBase(
	scope GoldenStateScope,
	commandID uuid.UUID,
	participantID uuid.UUID,
	attemptID uuid.UUID,
	waveID uuid.UUID,
	windowID uuid.UUID,
	expectedState GoldenStateExpectation,
	expectedExecution GoldenWaveExecutionExpectation,
	nextExecutionRevisionID uuid.UUID,
	nextWindowRevisionID uuid.UUID,
) bool {
	return validGoldenStateScope(scope) && expectedState.Scope == scope && expectedExecution.Scope == scope &&
		commandID != uuid.Nil && participantID != uuid.Nil && attemptID != uuid.Nil && waveID != uuid.Nil &&
		windowID != uuid.Nil && nextExecutionRevisionID != uuid.Nil && nextWindowRevisionID != uuid.Nil
}

func validateGoldenReadinessFreshIDs(
	state GoldenState,
	execution GoldenWaveExecution,
	operation goldenReadinessOperation,
) error {
	candidates := []uuid.UUID{
		operation.commandID, operation.nextExecutionRevisionID, operation.nextWindowRevisionID,
		operation.nextReadinessRevisionID, operation.nextPresenceRevisionID,
	}
	if err := validateGoldenFreshExecutionIDs(state, candidates...); err != nil {
		return err
	}
	reserved := goldenExecutionIdentitySet(execution)
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == uuid.Nil {
			continue
		}
		if _, exists := reserved[candidate]; exists {
			return goldenWaveError("command identity aliases execution authority")
		}
		if _, exists := seen[candidate]; exists {
			return goldenWaveError("command identities alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func goldenReadinessOperationIdentityIDs(operation goldenReadinessOperation) []uuid.UUID {
	identities := []uuid.UUID{
		operation.commandID, operation.nextExecutionRevisionID, operation.nextWindowRevisionID,
		operation.nextReadinessRevisionID, operation.nextPresenceRevisionID,
	}
	result := identities[:0]
	for _, identity := range identities {
		if identity != uuid.Nil {
			result = append(result, identity)
		}
	}
	return result
}

func goldenExecutionIdentitySet(execution GoldenWaveExecution) map[uuid.UUID]struct{} {
	values := []uuid.UUID{
		execution.RevisionID, execution.Attempt.ID, execution.Wave.ID, execution.Wave.RevisionID.UUID(),
		execution.Window.ID, execution.Wave.ReadyWindow.RevisionID.UUID(), execution.Window.RevisionID,
		execution.Window.ReadinessRevisionID, execution.Window.PresenceRevisionID,
		execution.Membership.ID, execution.Membership.RevisionID,
		execution.Assignment.ID, execution.Assignment.RevisionID, execution.Assignment.EdgeID,
		execution.Assignment.ReservationID, execution.Assignment.Snapshot.SnapshotID,
		execution.Assignment.Snapshot.TaskID,
	}
	if execution.PreviousRevisionID != nil {
		values = append(values, *execution.PreviousRevisionID)
	}
	for _, private := range execution.Assignment.Private {
		values = append(values, private.ID)
	}
	for _, receipt := range execution.Receipts {
		values = append(values, receipt.CommandID, receipt.Result.RevisionID,
			receipt.Result.Window.RevisionID, receipt.Result.Window.ReadinessRevisionID,
			receipt.Result.Window.PresenceRevisionID)
		values = append(values, receipt.UnusedIdentityIDs...)
		if receipt.Expected != nil {
			values = append(values, receipt.Expected.RevisionID,
				receipt.Expected.Window.RevisionID, receipt.Expected.Window.ReadinessRevisionID,
				receipt.Expected.Window.PresenceRevisionID)
		}
	}
	result := make(map[uuid.UUID]struct{}, len(values))
	for _, value := range values {
		if value != uuid.Nil {
			result[value] = struct{}{}
		}
	}
	return result
}

func goldenReadinessUnusedIdentities(
	operation goldenReadinessOperation,
	expected GoldenWaveExecutionExpectation,
	result GoldenWaveExecutionExpectation,
) []uuid.UUID {
	identities := make([]uuid.UUID, 0, 3)
	if result.Window.RevisionID != operation.nextWindowRevisionID {
		identities = append(identities, operation.nextWindowRevisionID)
	}
	if operation.nextReadinessRevisionID != uuid.Nil &&
		result.Window.ReadinessRevisionID != operation.nextReadinessRevisionID {
		identities = append(identities, operation.nextReadinessRevisionID)
	}
	if operation.nextPresenceRevisionID != uuid.Nil &&
		result.Window.PresenceRevisionID != operation.nextPresenceRevisionID {
		identities = append(identities, operation.nextPresenceRevisionID)
	}
	if result.Window == expected.Window {
		canonicalGoldenIDs(identities)
		return identities
	}
	return nil
}

func goldenReadinessCommandDigest(operation goldenReadinessOperation) [sha256.Size]byte {
	type document struct {
		Scope                   GoldenStateScope
		CommandID               uuid.UUID
		ParticipantID           uuid.UUID
		AttemptID               uuid.UUID
		WaveID                  uuid.UUID
		WindowID                uuid.UUID
		ExpectedState           GoldenStateExpectation
		ExpectedExecution       GoldenWaveExecutionExpectation
		NextExecutionRevisionID uuid.UUID
		NextWindowRevisionID    uuid.UUID
		NextReadinessRevisionID uuid.UUID
		NextPresenceRevisionID  uuid.UUID
		Kind                    GoldenWaveCommandKind
	}
	payload, _ := goldenEncode(document{
		Scope: operation.scope, CommandID: operation.commandID, ParticipantID: operation.participantID,
		AttemptID: operation.attemptID, WaveID: operation.waveID, WindowID: operation.windowID,
		ExpectedState: operation.expectedState, ExpectedExecution: operation.expectedExecution,
		NextExecutionRevisionID: operation.nextExecutionRevisionID,
		NextWindowRevisionID:    operation.nextWindowRevisionID,
		NextReadinessRevisionID: operation.nextReadinessRevisionID,
		NextPresenceRevisionID:  operation.nextPresenceRevisionID, Kind: operation.kind,
	})
	return sha256.Sum256(payload)
}
