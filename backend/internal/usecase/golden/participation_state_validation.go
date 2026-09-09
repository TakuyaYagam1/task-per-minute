package golden

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"

	"github.com/google/uuid"
)

func validateGoldenStateTopology(state GoldenState) error {
	if goldenAny(
		state.Topology.Validate() != nil,
		state.Topology.TournamentID() != state.Scope.TournamentID,
		state.Topology.GroupID() != state.Scope.GroupID,
		state.Topology.RevisionID() != state.Scope.GroupRevisionID,
		state.Group.ID != state.Scope.GroupID,
		state.Group.TournamentID != state.Scope.TournamentID,
		state.Group.RevisionID != state.Scope.GroupRevisionID,
		state.Group.SourceProjectionRevisionID != state.Topology.SourceProjectionRevisionID(),
	) {
		return goldenStateError("topology or group binding changed")
	}
	plan, err := goldenPlanBinding(state.ExactPlan, state.Scope)
	if goldenAny(err != nil, plan != state.Plan) {
		return goldenStateError("exact plan binding changed")
	}
	from, to := state.Topology.Positions()
	if goldenAny(state.Group.PositionFrom != from, state.Group.PositionTo != to) {
		return goldenStateError("group interval does not match topology")
	}
	group, err := domain.NewGoldenGroup(state.Group)
	if err != nil {
		return goldenStateError("group: %v", err)
	}
	topologyMembers := state.Topology.Members()
	groupState := group.Snapshot()
	if len(topologyMembers) != len(groupState.Members) {
		return goldenStateError("group membership does not match topology")
	}
	wantIDs := make([]uuid.UUID, len(topologyMembers))
	gotIDs := make([]uuid.UUID, len(groupState.Members))
	for index := range topologyMembers {
		wantIDs[index] = topologyMembers[index].ParticipantID
		gotIDs[index] = groupState.Members[index].ParticipantID
	}
	canonicalGoldenIDs(wantIDs)
	canonicalGoldenIDs(gotIDs)
	if !equalGoldenIDs(wantIDs, gotIDs) {
		return goldenStateError("group membership does not match topology")
	}
	return nil
}

func goldenPlanBinding(plan ExactPlan, scope GoldenStateScope) (GoldenPlanStateBinding, error) {
	if err := plan.Validate(); err != nil || plan.Scope.TournamentID != scope.TournamentID {
		return GoldenPlanStateBinding{}, goldenStateError("invalid exact plan")
	}
	var group *Group
	for index := range plan.Groups {
		if plan.Groups[index].GroupID == scope.GroupID {
			group = &plan.Groups[index]
			break
		}
	}
	if group == nil || group.GroupRevisionID != scope.GroupRevisionID {
		return GoldenPlanStateBinding{}, goldenStateError("exact plan does not contain group revision")
	}
	proof, err := hex.DecodeString(plan.ProofHash)
	if err != nil || len(proof) != sha256.Size {
		return GoldenPlanStateBinding{}, goldenStateError("invalid exact plan proof")
	}
	var proofDigest [sha256.Size]byte
	copy(proofDigest[:], proof)
	participants, _ := goldenEncode(group.ParticipantIDs)
	edges, edgeErr := goldenEncode(group.Edges)
	if edgeErr != nil {
		return GoldenPlanStateBinding{}, goldenStateError("encode exact plan group: %v", edgeErr)
	}
	return GoldenPlanStateBinding{
		PlanID: plan.PlanID, RevisionID: plan.PlanRevisionID, Expected: plan.Expected,
		GroupID: group.GroupID, GroupRevisionID: group.GroupRevisionID,
		SourceProjectionRevisionID: group.SourceProjectionRevisionID,
		PositionFrom:               group.PositionFrom, PositionTo: group.PositionTo,
		ParticipantDigest: sha256.Sum256(participants), EdgeDigest: sha256.Sum256(edges),
		ProofDigest: proofDigest,
	}, nil
}

func validateGoldenStateWindows(state GoldenState) error {
	seenIDs := make(map[uuid.UUID]struct{}, len(state.Windows))
	seenRevisions := make(map[uuid.UUID]struct{}, len(state.Windows)*3)
	open := 0
	for index, window := range state.Windows {
		if err := ValidateReadyWindowIdentity(window); err != nil {
			return err
		}
		if _, duplicate := seenIDs[window.ID]; duplicate {
			return goldenStateError("duplicate ready window")
		}
		for _, revisionID := range []uuid.UUID{window.RevisionID, window.ReadinessRevisionID, window.PresenceRevisionID} {
			if _, duplicate := seenRevisions[revisionID]; duplicate {
				return goldenStateError("ready-window revision identity is reused")
			}
			seenRevisions[revisionID] = struct{}{}
		}
		attempt, found := goldenAttempt(state.Group.Attempts, window.AttemptID)
		if goldenAny(!found, attempt.AttemptNo != window.AttemptNo) {
			return goldenStateError("ready window attempt binding changed")
		}
		if goldenAny(
			!goldenIDsAreCanonical(window.BasePresentParticipantIDs),
			!goldenIDsAreCanonical(window.ReadyParticipantIDs),
			!goldenIDsAreCanonical(window.PresentParticipantIDs),
			window.ReadinessDigest != goldenParticipantSetDigest(window.ReadyParticipantIDs),
			window.PresenceDigest != goldenParticipantSetDigest(window.PresentParticipantIDs),
			!goldenIDsSubset(window.ReadyParticipantIDs, window.PresentParticipantIDs),
			!goldenIDsSubset(window.BasePresentParticipantIDs, attempt.ParticipantIDs),
		) {
			return goldenStateError("invalid ready or presence snapshot")
		}
		if window.State == GoldenReadyWindowOpen {
			open++
			if index != len(state.Windows)-1 {
				return goldenStateError("only the latest ready window may be open")
			}
		}
		seenIDs[window.ID] = struct{}{}
	}
	if open > 1 {
		return goldenStateError("multiple ready windows are open")
	}
	return nil
}

func ValidateReadyWindowIdentity(window GoldenReadyWindow) error {
	if goldenAny(
		window.ID == uuid.Nil, window.RevisionID == uuid.Nil, window.Revision < 1,
		window.AttemptID == uuid.Nil, window.AttemptNo < 1,
		!domain.IsValidServerTime(window.OpenedAt), !domain.IsValidServerTime(window.Deadline),
		!window.Deadline.After(window.OpenedAt),
		window.State != GoldenReadyWindowOpen && window.State != GoldenReadyWindowExpired,
		window.ReadinessRevisionID == uuid.Nil, window.ReadinessRevision < 1,
		window.PresenceRevisionID == uuid.Nil, window.PresenceRevision < 1,
	) {
		return goldenStateError("invalid ready-window identity or interval")
	}
	if goldenAny(
		!stateValidGoldenRevisionPredecessor(window.RevisionID, window.Revision, window.PreviousRevisionID),
		!stateValidGoldenRevisionPredecessor(
			window.ReadinessRevisionID,
			window.ReadinessRevision,
			window.ReadinessPreviousRevisionID,
		), !stateValidGoldenRevisionPredecessor(
			window.PresenceRevisionID,
			window.PresenceRevision,
			window.PresencePreviousRevisionID,
		),
	) {
		return goldenStateError("invalid ready-window revision lineage")
	}
	return nil
}

func validateGoldenReadyEvents(state GoldenState) error {
	windows := make(map[uuid.UUID]GoldenReadyWindow, len(state.Windows))
	ready := make(map[uuid.UUID]map[uuid.UUID]bool, len(state.Windows))
	present := make(map[uuid.UUID]map[uuid.UUID]bool, len(state.Windows))
	for _, window := range state.Windows {
		windows[window.ID] = window
		ready[window.ID] = make(map[uuid.UUID]bool)
		present[window.ID] = goldenIDState(window.BasePresentParticipantIDs)
	}
	seenCommands := make(map[uuid.UUID]struct{}, len(state.ReadyEvents))
	lastWindowEvent := make(map[uuid.UUID]GoldenReadyEvent, len(state.Windows))
	accepted := false
	var previous time.Time
	for _, event := range state.ReadyEvents {
		window, found := windows[event.WindowID]
		if err := validateGoldenReadyEventIdentity(state, event, window, found, previous); err != nil {
			return err
		}
		if _, duplicate := seenCommands[event.CommandID]; duplicate {
			return goldenStateError("duplicate ready command")
		}
		eventAccepted, err := applyGoldenReadyEvent(event, ready[event.WindowID], present[event.WindowID])
		if err != nil {
			return err
		}
		accepted = accepted || eventAccepted
		if err := validateGoldenReadyEventSuccessor(event); err != nil {
			return err
		}
		if prior, exists := lastWindowEvent[event.WindowID]; exists {
			if err := validateGoldenReadyEventChain(event, prior); err != nil {
				return err
			}
		}
		lastWindowEvent[event.WindowID] = event
		seenCommands[event.CommandID] = struct{}{}
		previous = event.OccurredAt
	}
	return validateGoldenReadyReplayResult(state, windows, ready, present, accepted)
}

func validateGoldenReadyReplayResult(
	state GoldenState,
	windows map[uuid.UUID]GoldenReadyWindow,
	ready map[uuid.UUID]map[uuid.UUID]bool,
	present map[uuid.UUID]map[uuid.UUID]bool,
	accepted bool,
) error {
	if accepted != state.Group.ParticipationEstablished {
		return goldenStateError("participation flag does not match accepted-ready evidence")
	}
	for _, window := range state.Windows {
		if goldenAny(
			!equalGoldenIDs(goldenStateIDs(ready[window.ID]), window.ReadyParticipantIDs),
			!equalGoldenIDs(goldenStateIDs(present[window.ID]), window.PresentParticipantIDs),
		) {
			return goldenStateError("ready event history does not match retained window")
		}
	}
	if len(state.ReadyEvents) > 0 && len(state.NoShows) == 0 && state.Allocation == nil {
		final := state.ReadyEvents[len(state.ReadyEvents)-1]
		window := windows[final.WindowID]
		if goldenAny(
			final.ResultStateRevisionID != state.RevisionID,
			final.ExpectedState.Revision+1 != state.Revision,
			final.ResultWindowRevisionID != window.RevisionID,
			final.ResultReadinessRevisionID != window.ReadinessRevisionID,
			final.ResultPresenceRevisionID != window.PresenceRevisionID,
		) {
			return goldenStateError("final ready event does not link current state")
		}
	}
	return nil
}

func validateGoldenReadyEventIdentity(
	state GoldenState,
	event GoldenReadyEvent,
	window GoldenReadyWindow,
	windowFound bool,
	previous time.Time,
) error {
	_, memberFound := FindMember(state.Group.Members, event.ParticipantID)
	if goldenAny(
		!windowFound, event.CommandID == uuid.Nil, event.CommandDigest == [sha256.Size]byte{},
		event.Scope != state.Scope, event.ParticipantID == uuid.Nil, !memberFound,
		event.AttemptID != window.AttemptID,
		!goldenAttemptContains(state.Group.Attempts, event.AttemptID, event.ParticipantID),
		!domain.IsValidServerTime(event.OccurredAt), event.OccurredAt.Before(window.OpenedAt),
		event.OccurredAt.After(window.Deadline), !previous.IsZero() && event.OccurredAt.Before(previous),
		event.ExpectedState.Scope != state.Scope, event.ExpectedState.PayloadDigest == [sha256.Size]byte{},
		event.ExpectedWindow.WindowID != event.WindowID,
		event.ExpectedWindow.ReadinessDigest == [sha256.Size]byte{},
		event.ExpectedWindow.PresenceDigest == [sha256.Size]byte{}, event.ResultStateRevisionID == uuid.Nil,
		event.ResultWindowRevisionID == uuid.Nil, event.ResultReadinessRevisionID == uuid.Nil,
	) {
		return goldenStateError("invalid retained ready event")
	}
	return nil
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

func validGoldenStateScope(scope GoldenStateScope) bool {
	return !goldenAny(
		scope.TournamentID == uuid.Nil,
		scope.GroupID == uuid.Nil,
		scope.GroupRevisionID.IsZero(),
		scope.TournamentID == scope.GroupID,
		scope.TournamentID == scope.GroupRevisionID.UUID(),
		scope.GroupID == scope.GroupRevisionID.UUID(),
	)
}
