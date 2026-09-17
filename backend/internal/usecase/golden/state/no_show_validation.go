package state

import (
	"crypto/sha256"
	"fmt"
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func validateGoldenNoShows(state GoldenState) error {
	active := make(map[uuid.UUID]bool, len(state.Group.Members))
	for _, member := range state.Group.Members {
		active[member.ParticipantID] = true
	}
	seenCommands := make(map[uuid.UUID]struct{}, len(state.ReadyEvents)+len(state.NoShows))
	for _, event := range state.ReadyEvents {
		seenCommands[event.CommandID] = struct{}{}
	}
	seenWindows := make(map[uuid.UUID]struct{}, len(state.NoShows))
	var previous time.Time
	for _, resolution := range state.NoShows {
		windowIndex := goldenWindowIndex(state.Windows, resolution.WindowID)
		if windowIndex < 0 {
			return goldenNoShowError("resolution window is missing")
		}
		window := state.Windows[windowIndex]
		attempt, found := goldenAttempt(state.Group.Attempts, resolution.AttemptID)
		if err := validateGoldenNoShowIdentity(state, resolution, window, attempt, found, previous); err != nil {
			return err
		}
		if _, duplicate := seenCommands[resolution.CommandID]; duplicate {
			return goldenNoShowError("no-show command identity is reused")
		}
		if _, duplicate := seenWindows[resolution.WindowID]; duplicate {
			return goldenNoShowError("ready window was resolved twice")
		}
		if err := validateGoldenNoShowEvidence(resolution, window); err != nil {
			return err
		}
		if err := validateGoldenNoShowReadyBridge(state.ReadyEvents, resolution); err != nil {
			return err
		}
		if err := applyGoldenNoShowExclusions(state, resolution, attempt, active, windowIndex); err != nil {
			return err
		}
		seenCommands[resolution.CommandID] = struct{}{}
		seenWindows[resolution.WindowID] = struct{}{}
		previous = resolution.ResolvedAt
	}
	return validateGoldenNoShowReplayResult(state, active, seenWindows)
}

func validateGoldenNoShowReadyBridge(events []GoldenReadyEvent, resolution GoldenNoShowResolution) error {
	prior, exists := goldenLastReadyEventForWindow(events, resolution.WindowID)
	if !exists {
		return nil
	}
	return validateGoldenReadyToNoShowBridge(resolution, prior)
}

func applyGoldenNoShowExclusions(
	state GoldenState,
	resolution GoldenNoShowResolution,
	attempt domain.GoldenAttempt,
	active map[uuid.UUID]bool,
	windowIndex int,
) error {
	if !equalGoldenCanonicalIDs(attempt.ParticipantIDs, goldenActiveMapIDs(active)) {
		return goldenNoShowError("attempt omits an active Golden member")
	}
	expectedExcluded := make([]uuid.UUID, 0, len(attempt.ParticipantIDs))
	for _, participantID := range attempt.ParticipantIDs {
		if active[participantID] && !goldenIDsContain(resolution.ReadyParticipantIDs, participantID) {
			expectedExcluded = append(expectedExcluded, participantID)
		}
	}
	canonicalGoldenIDs(expectedExcluded)
	if !equalGoldenIDs(expectedExcluded, resolution.ExcludedParticipantIDs) {
		return goldenNoShowError("excluded set does not match retained readiness")
	}
	if err := validateGoldenNoShowAttemptOutcome(attempt, expectedExcluded, resolution.ResolvedAt); err != nil {
		return err
	}
	for _, participantID := range expectedExcluded {
		active[participantID] = false
		if goldenParticipantReentered(state, participantID, resolution.AttemptNo, windowIndex) {
			return goldenNoShowError("excluded participant entered later Golden state")
		}
	}
	return nil
}

func validateGoldenNoShowReplayResult(
	state GoldenState,
	active map[uuid.UUID]bool,
	seenWindows map[uuid.UUID]struct{},
) error {
	for _, member := range state.Group.Members {
		if member.Excluded == active[member.ParticipantID] {
			return goldenNoShowError("group exclusion flags do not match retained resolutions")
		}
	}
	for _, window := range state.Windows {
		_, resolved := seenWindows[window.ID]
		if window.State == GoldenReadyWindowExpired && !resolved {
			return goldenNoShowError("expired ready window lacks terminal evidence")
		}
	}
	return nil
}

func validateGoldenNoShowIdentity(
	state GoldenState,
	resolution GoldenNoShowResolution,
	window GoldenReadyWindow,
	attempt domain.GoldenAttempt,
	attemptFound bool,
	previous time.Time,
) error {
	wrongPredecessor := window.PreviousRevisionID == nil
	if window.PreviousRevisionID != nil {
		wrongPredecessor = *window.PreviousRevisionID != resolution.ExpectedWindow.RevisionID
	}
	if goldenAny(
		!attemptFound, attempt.AttemptNo != resolution.AttemptNo,
		window.AttemptID != resolution.AttemptID, window.State != GoldenReadyWindowExpired,
		resolution.CommandID == uuid.Nil, resolution.Scope != state.Scope,
		resolution.ExpectedState.Scope != state.Scope,
		resolution.ExpectedState.PayloadDigest == [sha256.Size]byte{},
		resolution.ExpectedWindow.WindowID != resolution.WindowID,
		resolution.ResultStateRevisionID == uuid.Nil,
		resolution.ResultWindowRevisionID != window.RevisionID,
		resolution.ResultMembershipRevisionID == uuid.Nil,
		resolution.ResultStateRevisionID == resolution.ExpectedState.RevisionID,
		resolution.ExpectedState.Revision == math.MaxInt64,
		resolution.ExpectedWindow.Revision == math.MaxInt64,
		resolution.ResultWindowRevisionID == resolution.ExpectedWindow.RevisionID,
		window.Revision != resolution.ExpectedWindow.Revision+1, wrongPredecessor,
		!resolution.Deadline.Equal(window.Deadline), !domain.IsValidServerTime(resolution.ResolvedAt),
		!resolution.ResolvedAt.After(resolution.Deadline),
		!previous.IsZero() && resolution.ResolvedAt.Before(previous),
	) {
		return goldenNoShowError("invalid retained resolution identity or cutoff")
	}
	return nil
}

func validateGoldenNoShowEvidence(resolution GoldenNoShowResolution, window GoldenReadyWindow) error {
	if goldenAny(
		resolution.ReadinessRevisionID != window.ReadinessRevisionID,
		resolution.ReadinessRevision != window.ReadinessRevision,
		resolution.ReadinessDigest != window.ReadinessDigest,
		resolution.ExpectedWindow.ReadinessRevisionID != resolution.ReadinessRevisionID,
		resolution.ExpectedWindow.ReadinessRevision != resolution.ReadinessRevision,
		resolution.ExpectedWindow.ReadinessDigest != resolution.ReadinessDigest,
		!equalGoldenIDs(resolution.ReadyParticipantIDs, window.ReadyParticipantIDs),
		resolution.PresenceRevisionID != window.PresenceRevisionID,
		resolution.PresenceRevision != window.PresenceRevision,
		resolution.PresenceDigest != window.PresenceDigest,
		resolution.ExpectedWindow.PresenceRevisionID != resolution.PresenceRevisionID,
		resolution.ExpectedWindow.PresenceRevision != resolution.PresenceRevision,
		resolution.ExpectedWindow.PresenceDigest != resolution.PresenceDigest,
		!equalGoldenIDs(resolution.PresentParticipantIDs, window.PresentParticipantIDs),
		!goldenIDsAreCanonical(resolution.ExcludedParticipantIDs),
	) {
		return goldenNoShowError("readiness or presence evidence changed")
	}
	return nil
}

func validateGoldenReadyToNoShowBridge(resolution GoldenNoShowResolution, prior GoldenReadyEvent) error {
	windowStep := int64(0)
	if goldenReadyEventChangesWindow(prior.Type) {
		windowStep = 1
	}
	presenceStep := int64(0)
	if prior.Type == GoldenReadyEventDisconnected {
		presenceStep = 1
	}
	if goldenAny(
		resolution.ExpectedWindow.RevisionID != prior.ResultWindowRevisionID,
		resolution.ExpectedWindow.Revision != prior.ExpectedWindow.Revision+windowStep,
		resolution.ExpectedWindow.ReadinessRevisionID != prior.ResultReadinessRevisionID,
		resolution.ExpectedWindow.ReadinessRevision != prior.ExpectedWindow.ReadinessRevision+windowStep,
		resolution.ExpectedWindow.PresenceRevisionID != prior.ResultPresenceRevisionID,
		resolution.ExpectedWindow.PresenceRevision != prior.ExpectedWindow.PresenceRevision+presenceStep,
	) {
		return goldenNoShowError("ready-to-no-show window chain is broken")
	}
	return nil
}

func validateGoldenNoShowAttemptOutcome(
	attempt domain.GoldenAttempt,
	excluded []uuid.UUID,
	resolvedAt time.Time,
) error {
	if len(excluded) > 0 {
		if goldenAny(
			attempt.State != domain.GoldenAttemptStateCancelled,
			attempt.FinishedAt == nil,
			attempt.FinishedAt != nil && !attempt.FinishedAt.Equal(resolvedAt),
		) {
			return goldenNoShowError("excluded attempt was not cancelled atomically")
		}
		return nil
	}
	if attempt.State != domain.GoldenAttemptStatePlanned &&
		attempt.State != domain.GoldenAttemptStateWaitingReady {
		return goldenNoShowError("empty no-show resolution changed attempt state")
	}
	return nil
}

func validateGoldenAttemptCoverage(state GoldenState) error {
	active := make(map[uuid.UUID]bool, len(state.Group.Members))
	for _, member := range state.Group.Members {
		active[member.ParticipantID] = true
	}
	resolutions := make(map[uuid.UUID]GoldenNoShowResolution, len(state.NoShows))
	for _, resolution := range state.NoShows {
		resolutions[resolution.WindowID] = resolution
	}
	for _, window := range state.Windows {
		attempt, found := goldenAttempt(state.Group.Attempts, window.AttemptID)
		if !found {
			return goldenNoShowError("ready window attempt is missing")
		}
		activeIDs := goldenActiveMapIDs(active)
		if goldenAny(
			!equalGoldenCanonicalIDs(attempt.ParticipantIDs, activeIDs),
			!equalGoldenCanonicalIDs(window.BasePresentParticipantIDs, activeIDs),
		) {
			return goldenNoShowError("ready window omits an active Golden member")
		}
		resolution, resolved := resolutions[window.ID]
		if !resolved {
			continue
		}
		for _, participantID := range resolution.ExcludedParticipantIDs {
			active[participantID] = false
		}
	}
	return nil
}

func goldenActiveMapIDs(active map[uuid.UUID]bool) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(active))
	for participantID, isActive := range active {
		if isActive {
			result = append(result, participantID)
		}
	}
	canonicalGoldenIDs(result)
	return result
}

func equalGoldenCanonicalIDs(first, second []uuid.UUID) bool {
	firstCopy := append([]uuid.UUID(nil), first...)
	secondCopy := append([]uuid.UUID(nil), second...)
	canonicalGoldenIDs(firstCopy)
	canonicalGoldenIDs(secondCopy)
	return equalGoldenIDs(firstCopy, secondCopy)
}

func goldenLastReadyEventForWindow(
	events []GoldenReadyEvent,
	windowID uuid.UUID,
) (GoldenReadyEvent, bool) {
	for index := len(events) - 1; index >= 0; index-- {
		if events[index].WindowID == windowID {
			return events[index], true
		}
	}
	return GoldenReadyEvent{}, false
}

func goldenParticipantReentered(
	state GoldenState,
	participantID uuid.UUID,
	excludedAttemptNo int,
	excludedWindowIndex int,
) bool {
	for _, attempt := range state.Group.Attempts {
		if attempt.AttemptNo > excludedAttemptNo && goldenIDsContain(attempt.ParticipantIDs, participantID) {
			return true
		}
	}
	for index := excludedWindowIndex + 1; index < len(state.Windows); index++ {
		window := state.Windows[index]
		if goldenIDsContain(window.BasePresentParticipantIDs, participantID) ||
			goldenIDsContain(window.PresentParticipantIDs, participantID) ||
			goldenIDsContain(window.ReadyParticipantIDs, participantID) {
			return true
		}
	}
	return false
}

func goldenNoShowByCommand(
	resolutions []GoldenNoShowResolution,
	commandID uuid.UUID,
) (GoldenNoShowResolution, bool) {
	for _, resolution := range resolutions {
		if resolution.CommandID == commandID {
			return resolution, true
		}
	}
	return GoldenNoShowResolution{}, false
}

func cloneGoldenNoShows(input []GoldenNoShowResolution) []GoldenNoShowResolution {
	result := make([]GoldenNoShowResolution, len(input))
	for index, resolution := range input {
		result[index] = resolution
		result[index].ExpectedState.Membership.PreviousRevisionID = cloneGoldenUUID(
			resolution.ExpectedState.Membership.PreviousRevisionID,
		)
		result[index].ReadyParticipantIDs = append([]uuid.UUID(nil), resolution.ReadyParticipantIDs...)
		result[index].PresentParticipantIDs = append([]uuid.UUID(nil), resolution.PresentParticipantIDs...)
		result[index].ExcludedParticipantIDs = append([]uuid.UUID(nil), resolution.ExcludedParticipantIDs...)
	}
	return result
}

func goldenNoShowError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %w: %s", ErrInvalidGoldenState, ErrInvalidGoldenNoShow, fmt.Sprintf(format, arguments...))
}
