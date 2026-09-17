package state

import "github.com/google/uuid"

func removeGoldenID(values []uuid.UUID, target uuid.UUID) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func validateGoldenOperationIDs(state GoldenState, operation goldenParticipationOperation) error {
	return stateValidateGoldenFreshIDs(
		state,
		operation.commandID,
		operation.nextStateRevisionID,
		operation.nextWindowRevisionID,
		operation.nextReadinessRevisionID,
		operation.nextPresenceRevisionID,
	)
}

func stateValidateGoldenFreshIDs(state GoldenState, candidates ...uuid.UUID) error {
	reserved := make(map[uuid.UUID]struct{})
	for _, value := range RetainedIdentityValues(state) {
		reserved[value] = struct{}{}
	}
	seen := make(map[uuid.UUID]struct{}, len(candidates))
	for _, candidate := range candidates {
		if candidate == uuid.Nil {
			continue
		}
		if _, exists := reserved[candidate]; exists {
			return goldenStateError("command identity aliases retained authority")
		}
		if _, exists := seen[candidate]; exists {
			return goldenStateError("command identities alias each other")
		}
		seen[candidate] = struct{}{}
	}
	return nil
}

func goldenCommandIDRetainedOutsideReady(state GoldenState, commandID uuid.UUID) bool {
	if _, found := goldenNoShowByCommand(state.NoShows, commandID); found {
		return true
	}
	return state.Allocation != nil && state.Allocation.CommandID == commandID
}

func validateGoldenReadyCommand(command GoldenReadyCommand) error {
	if !validGoldenStateScope(command.Scope) || command.CommandID == uuid.Nil || command.ParticipantID == uuid.Nil ||
		command.ActorParticipantID == uuid.Nil || command.AttemptID == uuid.Nil || command.WindowID == uuid.Nil ||
		command.NextStateRevisionID == uuid.Nil || command.NextWindowRevisionID == uuid.Nil ||
		command.NextReadinessRevisionID == uuid.Nil {
		return goldenStateError("invalid ready command identity")
	}
	return nil
}

func validateGoldenDisconnectCommand(command GoldenDisconnectCommand) error {
	if !validGoldenStateScope(command.Scope) || command.CommandID == uuid.Nil || command.ParticipantID == uuid.Nil ||
		command.AttemptID == uuid.Nil || command.WindowID == uuid.Nil || command.NextStateRevisionID == uuid.Nil ||
		command.NextWindowRevisionID == uuid.Nil || command.NextReadinessRevisionID == uuid.Nil ||
		command.NextPresenceRevisionID == uuid.Nil {
		return goldenStateError("invalid disconnect command identity")
	}
	return nil
}

func validateGoldenNoShowCommand(command GoldenNoShowCommand) error {
	if !validGoldenStateScope(command.Scope) || command.CommandID == uuid.Nil || command.AttemptID == uuid.Nil ||
		command.WindowID == uuid.Nil || command.NextStateRevisionID == uuid.Nil ||
		command.NextWindowRevisionID == uuid.Nil || command.NextMembershipRevisionID == uuid.Nil {
		return goldenNoShowError("invalid command identity")
	}
	return nil
}

func validateGoldenFallbackCommand(command GoldenFallbackCommand) error {
	if !validGoldenStateScope(command.Scope) || command.CommandID == uuid.Nil || command.AllocationID == uuid.Nil ||
		command.NextStateRevisionID == uuid.Nil || command.CommandID == command.AllocationID ||
		command.CommandID == command.NextStateRevisionID || command.AllocationID == command.NextStateRevisionID {
		return goldenFallbackError("invalid command identity")
	}
	return nil
}
