package state

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

type GoldenNoShowUseCase struct {
	repository StateRepository
	clock      StateClock
}

func NewGoldenNoShowUseCase(repository StateRepository, clock StateClock) *GoldenNoShowUseCase {
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
	if !domain.IsValidServerTime(resolvedAt) {
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
	validAttemptState := attempt.State == domain.GoldenAttemptStatePlanned ||
		attempt.State == domain.GoldenAttemptStateWaitingReady
	if goldenAny(!found, !validAttemptState) {
		return nil, false, false, ErrGoldenNoShowAuthorityConflict
	}
	if !equalGoldenCanonicalIDs(attempt.ParticipantIDs, authority.ActiveParticipantIDs()) {
		return nil, false, false, ErrGoldenNoShowAuthorityConflict
	}
	if !resolvedAt.After(window.Deadline) {
		return nil, false, false, ErrGoldenNoShowCutoff
	}
	if err := stateValidateGoldenFreshIDs(
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
		member, belongs := findMember(authority.Group.Members, participantID)
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

	group, err := domain.NewGoldenGroup(next.Group)
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
	group *domain.GoldenGroupState,
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
		if attempt.State != domain.GoldenAttemptStatePlanned &&
			attempt.State != domain.GoldenAttemptStateWaitingReady {
			return ErrGoldenNoShowAuthorityConflict
		}
		attempt.State = domain.GoldenAttemptStateCancelled
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
