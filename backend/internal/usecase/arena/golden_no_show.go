package arena

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

var (
	ErrInvalidGoldenNoShow           = errors.New("invalid Golden no-show resolution")
	ErrGoldenNoShowCutoff            = errors.New("golden no-show cutoff has not passed")
	ErrGoldenNoShowAuthorityConflict = errors.New("golden no-show authority conflict")
	ErrGoldenNoShowConflict          = errors.New("golden no-show commit conflict")
)

type GoldenNoShowCommand struct {
	Scope          GoldenStateScope
	CommandID      uuid.UUID
	AttemptID      uuid.UUID
	WindowID       uuid.UUID
	ExpectedState  GoldenStateExpectation
	ExpectedWindow GoldenReadyWindowExpectation

	NextStateRevisionID      uuid.UUID
	NextWindowRevisionID     uuid.UUID
	NextMembershipRevisionID uuid.UUID
}

type GoldenNoShowResolution struct {
	CommandID      uuid.UUID
	Scope          GoldenStateScope
	AttemptID      uuid.UUID
	AttemptNo      int
	WindowID       uuid.UUID
	ExpectedState  GoldenStateExpectation
	ExpectedWindow GoldenReadyWindowExpectation

	ResultStateRevisionID      uuid.UUID
	ResultWindowRevisionID     uuid.UUID
	ResultMembershipRevisionID uuid.UUID
	Deadline                   time.Time
	ResolvedAt                 time.Time

	ReadinessRevisionID    uuid.UUID
	ReadinessRevision      int64
	ReadinessDigest        [sha256.Size]byte
	ReadyParticipantIDs    []uuid.UUID
	PresenceRevisionID     uuid.UUID
	PresenceRevision       int64
	PresenceDigest         [sha256.Size]byte
	PresentParticipantIDs  []uuid.UUID
	ExcludedParticipantIDs []uuid.UUID
}

type GoldenNoShowUseCase struct {
	repository GoldenStateRepository
	clock      Clock
}

func NewGoldenNoShowUseCase(repository GoldenStateRepository, clock Clock) *GoldenNoShowUseCase {
	return &GoldenNoShowUseCase{repository: repository, clock: clock}
}

func (u *GoldenNoShowUseCase) Resolve(
	ctx context.Context,
	command GoldenNoShowCommand,
) (*GoldenState, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateGoldenNoShowCommand(command); err != nil {
		return nil, false, err
	}
	resolvedAt := u.clock.Now().Round(0).UTC()
	if !validArenaServerTime(resolvedAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenStateAttempts {
		state, changed, retry, err := u.resolveAttempt(ctx, command, resolvedAt)
		if retry {
			continue
		}
		return state, changed, err
	}
	return nil, false, ErrGoldenNoShowConflict
}

func (u *GoldenNoShowUseCase) resolveAttempt(
	ctx context.Context,
	command GoldenNoShowCommand,
	resolvedAt time.Time,
) (*GoldenState, bool, bool, error) {
	authority, err := u.repository.LoadGoldenState(ctx, command.Scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenNoShowUseCase - load state: %w", err)
	}
	if err := authority.Validate(); err != nil {
		return nil, false, false, domain.ErrInternal
	}
	if resolution, found := goldenNoShowByCommand(authority.NoShows, command.CommandID); found {
		state, reconcileErr := reconcileGoldenNoShow(authority, command, resolution)
		return state, false, false, reconcileErr
	}
	if goldenNoShowCommandUsedElsewhere(authority, command.CommandID) {
		return nil, false, false, ErrGoldenCommandReuse
	}
	if !authority.Expectation().Equal(command.ExpectedState) {
		return nil, false, false, ErrGoldenNoShowAuthorityConflict
	}
	windowIndex := goldenWindowIndex(authority.Windows, command.WindowID)
	if windowIndex < 0 {
		return nil, false, false, ErrGoldenNoShowAuthorityConflict
	}
	window := authority.Windows[windowIndex]
	if goldenAny(window.Expectation() != command.ExpectedWindow, window.AttemptID != command.AttemptID) {
		return nil, false, false, ErrGoldenNoShowAuthorityConflict
	}
	if window.State != GoldenReadyWindowOpen {
		return nil, false, false, ErrGoldenNoShowAuthorityConflict
	}
	attempt, found := goldenAttempt(authority.Group.Attempts, command.AttemptID)
	validAttemptState := attempt.State == domain.ArenaGoldenAttemptStatePlanned ||
		attempt.State == domain.ArenaGoldenAttemptStateWaitingReady
	if goldenAny(!found, !validAttemptState) {
		return nil, false, false, ErrGoldenNoShowAuthorityConflict
	}
	if !equalGoldenCanonicalIDs(attempt.ParticipantIDs, authority.ActiveParticipantIDs()) {
		return nil, false, false, ErrGoldenNoShowAuthorityConflict
	}
	if !resolvedAt.After(window.Deadline) {
		return nil, false, false, ErrGoldenNoShowCutoff
	}
	if err := validateGoldenFreshIDs(
		authority,
		command.CommandID,
		command.NextStateRevisionID,
		command.NextWindowRevisionID,
		command.NextMembershipRevisionID,
	); err != nil {
		return nil, false, false, err
	}
	next, err := buildGoldenNoShowSuccessor(authority, windowIndex, command, resolvedAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitGoldenNoShow(ctx, command, authority.Expectation(), next)
}

func goldenNoShowCommandUsedElsewhere(state GoldenState, commandID uuid.UUID) bool {
	if _, found := goldenReadyEventByCommand(state.ReadyEvents, commandID); found {
		return true
	}
	return state.Allocation != nil && state.Allocation.CommandID == commandID
}

func buildGoldenNoShowSuccessor(
	authority GoldenState,
	windowIndex int,
	command GoldenNoShowCommand,
	resolvedAt time.Time,
) (GoldenState, error) {
	window := authority.Windows[windowIndex]
	if goldenAny(
		authority.Revision == math.MaxInt64,
		window.Revision == math.MaxInt64,
		authority.Membership.Revision == math.MaxInt64,
	) {
		return GoldenState{}, fmt.Errorf("%w: no-show successor", ErrGoldenRevisionOverflow)
	}
	attempt, found := goldenAttempt(authority.Group.Attempts, command.AttemptID)
	if goldenAny(!found, attempt.AttemptNo != window.AttemptNo) {
		return GoldenState{}, ErrGoldenNoShowAuthorityConflict
	}
	excluded := make([]uuid.UUID, 0, len(attempt.ParticipantIDs))
	for _, participantID := range attempt.ParticipantIDs {
		member, belongs := goldenStateMember(authority.Group.Members, participantID)
		if goldenAny(!belongs, member.Excluded) {
			continue
		}
		if !goldenIDsContain(window.ReadyParticipantIDs, participantID) {
			excluded = append(excluded, participantID)
		}
	}
	canonicalGoldenIDs(excluded)

	next := authority.Snapshot()
	nextWindow := &next.Windows[windowIndex]
	nextWindow.PreviousRevisionID = goldenUUID(nextWindow.RevisionID)
	nextWindow.RevisionID = command.NextWindowRevisionID
	nextWindow.Revision++
	nextWindow.State = GoldenReadyWindowExpired

	group, err := domain.NewArenaGoldenGroup(next.Group)
	if err != nil {
		return GoldenState{}, goldenNoShowError("group: %v", err)
	}
	for _, participantID := range excluded {
		if _, err := group.ExcludeParticipant(participantID); err != nil {
			return GoldenState{}, goldenNoShowError("exclude participant: %v", err)
		}
	}
	next.Group = group.Snapshot()
	if err := cancelGoldenExcludedAttempt(&next.Group, command.AttemptID, excluded, resolvedAt); err != nil {
		return GoldenState{}, err
	}

	next.Membership.PreviousRevisionID = goldenUUID(next.Membership.RevisionID)
	next.Membership.RevisionID = command.NextMembershipRevisionID
	next.Membership.Revision++
	next.PreviousRevisionID = goldenUUID(next.RevisionID)
	next.RevisionID = command.NextStateRevisionID
	next.Revision++
	next.NoShows = append(next.NoShows, GoldenNoShowResolution{
		CommandID: command.CommandID, Scope: command.Scope, AttemptID: command.AttemptID,
		AttemptNo: window.AttemptNo, WindowID: window.ID,
		ExpectedState: authority.Expectation(), ExpectedWindow: window.Expectation(),
		ResultStateRevisionID:      command.NextStateRevisionID,
		ResultWindowRevisionID:     command.NextWindowRevisionID,
		ResultMembershipRevisionID: command.NextMembershipRevisionID,
		Deadline:                   window.Deadline, ResolvedAt: resolvedAt,
		ReadinessRevisionID: window.ReadinessRevisionID, ReadinessRevision: window.ReadinessRevision,
		ReadinessDigest:     window.ReadinessDigest,
		ReadyParticipantIDs: append([]uuid.UUID(nil), window.ReadyParticipantIDs...),
		PresenceRevisionID:  window.PresenceRevisionID, PresenceRevision: window.PresenceRevision,
		PresenceDigest:         window.PresenceDigest,
		PresentParticipantIDs:  append([]uuid.UUID(nil), window.PresentParticipantIDs...),
		ExcludedParticipantIDs: append([]uuid.UUID(nil), excluded...),
	})
	return BuildGoldenState(next)
}

func cancelGoldenExcludedAttempt(
	group *domain.ArenaGoldenGroupState,
	attemptID uuid.UUID,
	excluded []uuid.UUID,
	resolvedAt time.Time,
) error {
	if len(excluded) == 0 {
		return nil
	}
	for index := range group.Attempts {
		attempt := &group.Attempts[index]
		if attempt.ID != attemptID {
			continue
		}
		if attempt.State != domain.ArenaGoldenAttemptStatePlanned &&
			attempt.State != domain.ArenaGoldenAttemptStateWaitingReady {
			return ErrGoldenNoShowAuthorityConflict
		}
		attempt.State = domain.ArenaGoldenAttemptStateCancelled
		attempt.FinishedAt = cloneGoldenTime(&resolvedAt)
	}
	return nil
}

func (u *GoldenNoShowUseCase) commitGoldenNoShow(
	ctx context.Context,
	command GoldenNoShowCommand,
	expected GoldenStateExpectation,
	next GoldenState,
) (*GoldenState, bool, bool, error) {
	committed, changed, err := u.repository.CommitGoldenState(ctx, GoldenStateCommit{Expected: expected, Next: next})
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenNoShowUseCase - commit state: %w", err)
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	resolution, found := goldenNoShowByCommand(committed.NoShows, command.CommandID)
	result, reconcileErr := reconcileGoldenNoShow(*committed, command, resolution)
	if !found || reconcileErr != nil || (changed && !committed.Expectation().Equal(next.Expectation())) {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func reconcileGoldenNoShow(
	state GoldenState,
	command GoldenNoShowCommand,
	resolution GoldenNoShowResolution,
) (*GoldenState, error) {
	if resolution.CommandID != command.CommandID || resolution.Scope != command.Scope ||
		resolution.AttemptID != command.AttemptID || resolution.WindowID != command.WindowID ||
		!resolution.ExpectedState.Equal(command.ExpectedState) || resolution.ExpectedWindow != command.ExpectedWindow ||
		resolution.ResultStateRevisionID != command.NextStateRevisionID ||
		resolution.ResultWindowRevisionID != command.NextWindowRevisionID ||
		resolution.ResultMembershipRevisionID != command.NextMembershipRevisionID {
		return nil, ErrGoldenCommandReuse
	}
	clone := state.Snapshot()
	return &clone, nil
}

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
	attempt domain.ArenaGoldenAttempt,
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
	attempt domain.ArenaGoldenAttempt,
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
		!resolution.Deadline.Equal(window.Deadline), !validArenaServerTime(resolution.ResolvedAt),
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
	attempt domain.ArenaGoldenAttempt,
	excluded []uuid.UUID,
	resolvedAt time.Time,
) error {
	if len(excluded) > 0 {
		if goldenAny(
			attempt.State != domain.ArenaGoldenAttemptStateCancelled,
			attempt.FinishedAt == nil,
			attempt.FinishedAt != nil && !attempt.FinishedAt.Equal(resolvedAt),
		) {
			return goldenNoShowError("excluded attempt was not cancelled atomically")
		}
		return nil
	}
	if attempt.State != domain.ArenaGoldenAttemptStatePlanned &&
		attempt.State != domain.ArenaGoldenAttemptStateWaitingReady {
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

func validateGoldenNoShowCommand(command GoldenNoShowCommand) error {
	if !validGoldenStateScope(command.Scope) || command.CommandID == uuid.Nil || command.AttemptID == uuid.Nil ||
		command.WindowID == uuid.Nil || command.NextStateRevisionID == uuid.Nil ||
		command.NextWindowRevisionID == uuid.Nil || command.NextMembershipRevisionID == uuid.Nil {
		return goldenNoShowError("invalid command identity")
	}
	return nil
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
