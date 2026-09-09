package golden

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

type GoldenIndividualConnectionReplay struct {
	Receipt     GoldenIndividualConnectionReceipt
	Connections GoldenIndividualConnectionLedger
}

func (r GoldenIndividualConnectionReplay) Snapshot() GoldenIndividualConnectionReplay {
	clone := r
	clone.Receipt.Expected.Execution = CloneExecutionExpectation(r.Receipt.Expected.Execution)
	clone.Connections = r.Connections.Snapshot()
	return clone
}

type GoldenIndividualDisconnectAuthority struct {
	Execution   GoldenWaveExecution
	Submissions GoldenSubmissionLedger
	Connections GoldenIndividualConnectionLedger
}

type GoldenIndividualConnectionCommit struct {
	ExpectedExecution   GoldenWaveExecutionExpectation
	ExpectedSubmissions GoldenSubmissionLedgerExpectation
	ExpectedConnections GoldenIndividualConnectionExpectation
	NewIdentityIDs      []uuid.UUID
	Next                GoldenIndividualConnectionLedger
}

type GoldenIndividualDisconnectCommand struct {
	Scope                    GoldenSubmissionScope
	CommandID                uuid.UUID
	ParticipantID            uuid.UUID
	IntervalID               uuid.UUID
	ExpectedExecution        GoldenWaveExecutionExpectation
	ExpectedSubmissions      GoldenSubmissionLedgerExpectation
	ExpectedConnections      GoldenIndividualConnectionExpectation
	NextConnectionRevisionID uuid.UUID
}

type GoldenIndividualReconnectCommand struct {
	Scope                    GoldenSubmissionScope
	CommandID                uuid.UUID
	ActorParticipantID       uuid.UUID
	ParticipantID            uuid.UUID
	IntervalID               uuid.UUID
	ExpectedExecution        GoldenWaveExecutionExpectation
	ExpectedSubmissions      GoldenSubmissionLedgerExpectation
	ExpectedConnections      GoldenIndividualConnectionExpectation
	NextConnectionRevisionID uuid.UUID
}

type GoldenIndividualDisconnectUseCase struct {
	repository ConnectionRepository
	clock      ConnectionClock
}

func NewGoldenIndividualDisconnectUseCase(
	repository ConnectionRepository,
	clock ConnectionClock,
) *GoldenIndividualDisconnectUseCase {
	return &GoldenIndividualDisconnectUseCase{repository: repository, clock: clock}
}

func (u *GoldenIndividualDisconnectUseCase) Disconnect(
	ctx context.Context,
	command GoldenIndividualDisconnectCommand,
) (*GoldenIndividualConnectionLedger, bool, error) {
	operation := goldenIndividualConnectionOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		intervalID: command.IntervalID, expectedExecution: CloneExecutionExpectation(command.ExpectedExecution),
		expectedSubmissions: command.ExpectedSubmissions, expectedConnections: command.ExpectedConnections,
		nextRevisionID: command.NextConnectionRevisionID, kind: GoldenIndividualConnectionDisconnected,
	}
	return u.apply(ctx, operation)
}

func (u *GoldenIndividualDisconnectUseCase) Reconnect(
	ctx context.Context,
	command GoldenIndividualReconnectCommand,
) (*GoldenIndividualConnectionLedger, bool, error) {
	if command.ActorParticipantID == uuid.Nil || command.ActorParticipantID != command.ParticipantID {
		return nil, false, domain.ErrAssignmentParticipant
	}
	operation := goldenIndividualConnectionOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		intervalID: command.IntervalID, expectedExecution: CloneExecutionExpectation(command.ExpectedExecution),
		expectedSubmissions: command.ExpectedSubmissions, expectedConnections: command.ExpectedConnections,
		nextRevisionID: command.NextConnectionRevisionID, kind: GoldenIndividualConnectionReconnected,
	}
	return u.apply(ctx, operation)
}

type goldenIndividualConnectionOperation struct {
	scope               GoldenSubmissionScope
	commandID           uuid.UUID
	participantID       uuid.UUID
	intervalID          uuid.UUID
	expectedExecution   GoldenWaveExecutionExpectation
	expectedSubmissions GoldenSubmissionLedgerExpectation
	expectedConnections GoldenIndividualConnectionExpectation
	nextRevisionID      uuid.UUID
	kind                GoldenIndividualConnectionKind
}

func (u *GoldenIndividualDisconnectUseCase) apply(
	ctx context.Context,
	operation goldenIndividualConnectionOperation,
) (*GoldenIndividualConnectionLedger, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil || !validGoldenIndividualOperation(operation) {
		return nil, false, domain.ErrValidation
	}
	digest := goldenIndividualOperationDigest(operation)
	if replay, err := u.repository.FindGoldenIndividualConnectionCommand(ctx, operation.scope.State.TournamentID, operation.commandID); err != nil {
		return nil, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - find replay: %w", err)
	} else if replay != nil {
		return reconcileGoldenIndividualReplay(*replay, operation, digest)
	}
	now := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(now) {
		return nil, false, domain.ErrValidation
	}
	for range goldenIndividualConnectionAttempts {
		ledger, changed, retry, err := u.applyAttempt(ctx, operation, digest, now)
		if retry {
			replay, replayErr := u.repository.FindGoldenIndividualConnectionCommand(
				ctx, operation.scope.State.TournamentID, operation.commandID,
			)
			if replayErr != nil {
				return nil, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - find retry replay: %w", replayErr)
			}
			if replay != nil {
				return reconcileGoldenIndividualReplay(*replay, operation, digest)
			}
			continue
		}
		return ledger, changed, err
	}
	return nil, false, ErrGoldenIndividualConnectionConflict
}

func (u *GoldenIndividualDisconnectUseCase) applyAttempt(
	ctx context.Context,
	operation goldenIndividualConnectionOperation,
	digest [sha256.Size]byte,
	now time.Time,
) (*GoldenIndividualConnectionLedger, bool, bool, error) {
	if replay, err := u.repository.FindGoldenIndividualConnectionCommand(
		ctx, operation.scope.State.TournamentID, operation.commandID,
	); err != nil {
		return nil, false, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - find attempt replay: %w", err)
	} else if replay != nil {
		result, changed, replayErr := reconcileGoldenIndividualReplay(*replay, operation, digest)
		return result, changed, false, replayErr
	}
	authority, err := u.repository.LoadGoldenIndividualDisconnectAuthority(ctx, operation.scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - load authority: %w", err)
	}
	if err := validateGoldenIndividualMutationAuthority(authority, operation, now); err != nil {
		return nil, false, false, err
	}
	next, ids, err := buildGoldenIndividualConnectionSuccessor(authority, operation, digest, now)
	if err != nil {
		return nil, false, false, err
	}
	commit := GoldenIndividualConnectionCommit{
		ExpectedExecution: operation.expectedExecution, ExpectedSubmissions: operation.expectedSubmissions,
		ExpectedConnections: operation.expectedConnections, NewIdentityIDs: ids, Next: next,
	}
	committed, changed, err := u.repository.CommitGoldenIndividualConnection(ctx, commit)
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenIndividualDisconnectUseCase - commit connection: %w", err)
	}
	if err := validateGoldenIndividualCommittedMutation(committed, next, operation, digest, changed); err != nil {
		return nil, false, false, err
	}
	clone := committed.Snapshot()
	return &clone, changed, false, nil
}
