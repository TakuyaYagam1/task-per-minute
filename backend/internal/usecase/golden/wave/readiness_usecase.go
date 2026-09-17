package golden

import (
	"context"
	"crypto/sha256"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	"github.com/google/uuid"
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
	repository WaveRepository
	clock      WaveClock
}

func NewGoldenReadinessUseCase(repository WaveRepository, clock WaveClock) *GoldenReadinessUseCase {
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
		return nil, false, domain.ErrAssignmentParticipant
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
		return nil, false, domain.ErrAssignmentParticipant
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
	if !domain.IsValidServerTime(occurredAt) {
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
	SortIDs(commit.NewIdentityIDs)
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
	if !ContainsID(execution.Membership.ParticipantIDs, operation.participantID) {
		return domain.ErrAssignmentParticipant
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
		execution.Attempt.State == domain.GoldenAttemptStateWaitingReady &&
		execution.Window.State == GoldenReadyWindowOpen &&
		!occurredAt.Before(execution.OpenedAt) && !occurredAt.After(execution.Deadline)
}
