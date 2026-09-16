package game

import (
	"fmt"
	"math"
	"reflect"

	"github.com/google/uuid"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
)

func validatePauseResumeCommand(command PauseResumeCommand) error {
	if !validPauseGraphScope(command.Scope) || command.PauseID == uuid.Nil || command.CommandID == uuid.Nil || command.ActorID == uuid.Nil ||
		command.PauseID == command.CommandID || command.CommandID == command.ActorID || command.PauseID == command.ActorID {
		return pauseResumeError("invalid command identity")
	}
	if err := validatePauseResumeExpectation(command.Expected); err != nil {
		return err
	}
	if command.Expected.PauseID != command.PauseID || command.Expected.Authority != command.Scope.Authority {
		return pauseResumeError("command expectation does not match scope")
	}
	if !validDraftResultRevisionIdentity(command.Expected.Draft, command.Expected.DraftPreviousRevisionID, command.DraftResultRevisionID,
		command.CommandID, command.PauseID, command.ActorID) {
		return pauseResumeError("invalid Draft result revision identity")
	}
	return nil
}

func validatePauseResumeAuthority(authority PauseResumeAuthority) error {
	if err := validatePauseResumeFrozenRevisions(authority.FrozenDeadlines); err != nil {
		return err
	}
	for _, interval := range authority.Reconnect {
		if interval.State == pausedomain.ReconnectStateOpen {
			return ErrPauseResumePresence
		}
	}
	if validateNormalPauseRecord(authority.Pause) != nil || authority.Pause.State != PauseStateActive {
		return pauseResumeError("pause is not active")
	}
	if err := validateLivePausePresence(authority); err != nil {
		return err
	}
	if err := validateLivePauseReconnect(authority); err != nil {
		return err
	}
	if !reflect.DeepEqual(authority.Counters, authority.Pause.Graph.Counters) ||
		!reflect.DeepEqual(authority.FrozenDeadlines, authority.Pause.Graph.FrozenDeadlines) ||
		authority.TerminalActionRevision != authority.Pause.Graph.TerminalActionRevision {
		return ErrPauseResumeIncomplete
	}
	return nil
}

func validatePauseResumeFrozenRevisions(values []PauseFrozenDeadline) error {
	for _, frozen := range values {
		if frozen.Revision == math.MaxInt64 {
			return ErrPauseResumeOverflow
		}
	}
	return nil
}

func validateLivePausePresence(authority PauseResumeAuthority) error {
	required := make(map[uuid.UUID]pausedomain.PausePresence, len(authority.Pause.Graph.Presence))
	for _, snapshot := range authority.Pause.Graph.Presence {
		required[snapshot.ParticipantID] = snapshot
	}
	seen := make(map[uuid.UUID]struct{}, len(authority.Presence))
	for _, presence := range authority.Presence {
		snapshot, exists := required[presence.ParticipantID]
		if err := validateLivePausePresenceRow(authority, snapshot, presence, exists); err != nil {
			return err
		}
		if _, duplicate := seen[presence.ParticipantID]; duplicate {
			return ErrPauseResumeIncomplete
		}
		seen[presence.ParticipantID] = struct{}{}
	}
	if len(seen) != len(required) {
		return ErrPauseResumeIncomplete
	}
	return nil
}

func validateLivePausePresenceRow(authority PauseResumeAuthority, snapshot, presence pausedomain.PausePresence, exists bool) error {
	if validatePausePresence(presence) != nil || presence.TournamentID != authority.Pause.Scope.TournamentID ||
		presence.RosterID != authority.Pause.Scope.RosterID {
		return pauseResumeError("invalid live Presence")
	}
	if !exists || !samePausePresenceIdentity(snapshot, presence) || presence.PresenceEpoch < snapshot.PresenceEpoch ||
		presence.Revision < snapshot.Revision || presence.PresenceEpoch-snapshot.PresenceEpoch != presence.Revision-snapshot.Revision {
		return ErrPauseResumeIncomplete
	}
	if presence.PresenceEpoch == snapshot.PresenceEpoch && !reflect.DeepEqual(presence, snapshot) {
		return ErrPauseResumeIncomplete
	}
	if presence.PresenceEpoch > snapshot.PresenceEpoch && presence.UpdatedAt.Before(authority.Pause.PausedAt) {
		return ErrPauseResumeIncomplete
	}
	return nil
}

func validateLivePauseReconnect(authority PauseResumeAuthority) error {
	pausedReconnect := make(map[uuid.UUID]pausedomain.PauseReconnectInterval, len(authority.Pause.Graph.Reconnect))
	for _, interval := range authority.Pause.Graph.Reconnect {
		pausedReconnect[interval.ID] = interval
	}
	seenReconnect := make(map[uuid.UUID]struct{}, len(authority.Reconnect))
	for _, interval := range authority.Reconnect {
		snapshot, exists := pausedReconnect[interval.ID]
		if validatePauseReconnect(interval) != nil {
			return pauseResumeError("invalid Reconnect evidence")
		}
		if !exists || !reflect.DeepEqual(interval, snapshot) {
			return ErrPauseResumeIncomplete
		}
		if _, duplicate := seenReconnect[interval.ID]; duplicate {
			return ErrPauseResumeIncomplete
		}
		seenReconnect[interval.ID] = struct{}{}
	}
	if len(seenReconnect) != len(pausedReconnect) {
		return ErrPauseResumeIncomplete
	}
	return nil
}

func pauseResumeError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidPauseResume, fmt.Sprintf(format, arguments...))
}
