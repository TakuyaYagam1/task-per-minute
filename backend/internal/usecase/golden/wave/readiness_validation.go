package golden

import (
	"crypto/sha256"

	"github.com/google/uuid"
)

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
	return ValidStateScope(scope) && expectedState.Scope == scope && expectedExecution.Scope == scope &&
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
	if err := ValidateFreshIdentityIDs(state, candidates...); err != nil {
		return err
	}
	reserved := RetainedIdentitySet(execution)
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
		SortIDs(identities)
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
	payload, _ := Encode(document{
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
