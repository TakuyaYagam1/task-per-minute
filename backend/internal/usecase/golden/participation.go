package golden

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

type GoldenParticipationUseCase struct {
	repository StateRepository
	clock      StateClock
}

func NewGoldenParticipationUseCase(
	repository StateRepository,
	clock StateClock,
) *GoldenParticipationUseCase {
	return &GoldenParticipationUseCase{repository: repository, clock: clock}
}

func (u *GoldenParticipationUseCase) AcceptReady(
	ctx context.Context,
	command GoldenReadyCommand,
) (*GoldenState, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateGoldenReadyCommand(command); err != nil {
		return nil, false, err
	}
	if command.ActorParticipantID != command.ParticipantID {
		return nil, false, domain.ErrAssignmentParticipant
	}
	return u.apply(ctx, goldenParticipationOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		attemptID: command.AttemptID, windowID: command.WindowID,
		expectedState: command.ExpectedState, expectedWindow: command.ExpectedWindow,
		nextStateRevisionID:     command.NextStateRevisionID,
		nextWindowRevisionID:    command.NextWindowRevisionID,
		nextReadinessRevisionID: command.NextReadinessRevisionID,
		typeValue:               GoldenReadyEventAccepted,
	})
}

func (u *GoldenParticipationUseCase) ClearOnDisconnect(
	ctx context.Context,
	command GoldenDisconnectCommand,
) (*GoldenState, bool, error) {
	if u == nil || u.repository == nil || u.clock == nil {
		return nil, false, domain.ErrValidation
	}
	if err := validateGoldenDisconnectCommand(command); err != nil {
		return nil, false, err
	}
	return u.apply(ctx, goldenParticipationOperation{
		scope: command.Scope, commandID: command.CommandID, participantID: command.ParticipantID,
		attemptID: command.AttemptID, windowID: command.WindowID,
		expectedState: command.ExpectedState, expectedWindow: command.ExpectedWindow,
		nextStateRevisionID:     command.NextStateRevisionID,
		nextWindowRevisionID:    command.NextWindowRevisionID,
		nextReadinessRevisionID: command.NextReadinessRevisionID,
		nextPresenceRevisionID:  command.NextPresenceRevisionID,
		typeValue:               GoldenReadyEventDisconnected,
	})
}

type goldenParticipationOperation struct {
	scope          GoldenStateScope
	commandID      uuid.UUID
	participantID  uuid.UUID
	attemptID      uuid.UUID
	windowID       uuid.UUID
	expectedState  GoldenStateExpectation
	expectedWindow GoldenReadyWindowExpectation

	nextStateRevisionID     uuid.UUID
	nextWindowRevisionID    uuid.UUID
	nextReadinessRevisionID uuid.UUID
	nextPresenceRevisionID  uuid.UUID
	typeValue               GoldenReadyEventType
}

func (u *GoldenParticipationUseCase) apply(
	ctx context.Context,
	operation goldenParticipationOperation,
) (*GoldenState, bool, error) {
	occurredAt := u.clock.Now().Round(0).UTC()
	if !domain.IsValidServerTime(occurredAt) {
		return nil, false, domain.ErrValidation
	}
	for range goldenStateAttempts {
		state, changed, retry, err := u.applyAttempt(ctx, operation, occurredAt)
		if retry {
			continue
		}
		return state, changed, err
	}
	return nil, false, ErrGoldenParticipationConflict
}

func (u *GoldenParticipationUseCase) applyAttempt(
	ctx context.Context,
	operation goldenParticipationOperation,
	occurredAt time.Time,
) (*GoldenState, bool, bool, error) {
	authority, err := u.repository.LoadGoldenState(ctx, operation.scope)
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenParticipationUseCase - load state: %w", err)
	}
	if err := authority.Validate(); err != nil {
		return nil, false, false, domain.ErrInternal
	}
	if event, found := goldenReadyEventByCommand(authority.ReadyEvents, operation.commandID); found {
		state, reconcileErr := reconcileGoldenReadyEvent(authority, operation, event)
		return state, false, false, reconcileErr
	}
	if goldenCommandIDRetainedOutsideReady(authority, operation.commandID) {
		return nil, false, false, ErrGoldenCommandReuse
	}
	if !authority.Expectation().Equal(operation.expectedState) {
		return nil, false, false, ErrGoldenParticipationAuthorityConflict
	}
	windowIndex := goldenWindowIndex(authority.Windows, operation.windowID)
	if windowIndex < 0 {
		return nil, false, false, ErrGoldenParticipationAuthorityConflict
	}
	window := authority.Windows[windowIndex]
	if goldenAny(
		window.Expectation() != operation.expectedWindow,
		window.AttemptID != operation.attemptID,
	) {
		return nil, false, false, ErrGoldenParticipationAuthorityConflict
	}
	if goldenAny(
		window.State != GoldenReadyWindowOpen,
		occurredAt.Before(window.OpenedAt),
		occurredAt.After(window.Deadline),
	) {
		return nil, false, false, ErrGoldenReadyWindowClosed
	}
	member, found := FindMember(authority.Group.Members, operation.participantID)
	if goldenAny(!found, !goldenAttemptContains(authority.Group.Attempts, operation.attemptID, operation.participantID)) {
		return nil, false, false, domain.ErrAssignmentParticipant
	}
	if member.Excluded {
		return nil, false, false, ErrGoldenParticipantExcluded
	}
	if operation.typeValue == GoldenReadyEventAccepted &&
		!goldenIDsContain(window.PresentParticipantIDs, operation.participantID) {
		return nil, false, false, ErrGoldenParticipationAuthorityConflict
	}
	if err := validateGoldenOperationIDs(authority, operation); err != nil {
		return nil, false, false, err
	}
	next, err := buildGoldenParticipationSuccessor(authority, windowIndex, operation, occurredAt)
	if err != nil {
		return nil, false, false, err
	}
	return u.commitParticipation(ctx, operation, authority.Expectation(), next)
}

func buildGoldenParticipationSuccessor(
	authority GoldenState,
	windowIndex int,
	operation goldenParticipationOperation,
	occurredAt time.Time,
) (GoldenState, error) {
	windowBefore := authority.Windows[windowIndex]
	resultType := operation.typeValue
	if operation.typeValue == GoldenReadyEventAccepted && goldenIDsContain(windowBefore.ReadyParticipantIDs, operation.participantID) {
		resultType = GoldenReadyEventAlreadyReady
	}
	if operation.typeValue == GoldenReadyEventDisconnected && !goldenIDsContain(windowBefore.PresentParticipantIDs, operation.participantID) {
		resultType = GoldenReadyEventAlreadyAbsent
	}
	changesWindow := goldenReadyEventChangesWindow(resultType)
	if goldenAny(
		authority.Revision == math.MaxInt64,
		changesWindow && windowBefore.Revision == math.MaxInt64,
		changesWindow && windowBefore.ReadinessRevision == math.MaxInt64,
		resultType == GoldenReadyEventDisconnected && windowBefore.PresenceRevision == math.MaxInt64,
	) {
		return GoldenState{}, fmt.Errorf("%w: participation successor", ErrGoldenRevisionOverflow)
	}
	next := authority.Snapshot()
	window := &next.Windows[windowIndex]
	if changesWindow {
		window.PreviousRevisionID = goldenUUID(window.RevisionID)
		window.RevisionID = operation.nextWindowRevisionID
		window.Revision++
		window.ReadinessPreviousRevisionID = goldenUUID(window.ReadinessRevisionID)
		window.ReadinessRevisionID = operation.nextReadinessRevisionID
		window.ReadinessRevision++
	}
	switch resultType {
	case GoldenReadyEventAccepted:
		window.ReadyParticipantIDs = append(window.ReadyParticipantIDs, operation.participantID)
		canonicalGoldenIDs(window.ReadyParticipantIDs)
		group, err := domain.NewGoldenGroup(next.Group)
		if err != nil {
			return GoldenState{}, goldenStateError("group: %v", err)
		}
		if _, err := group.EstablishParticipation(); err != nil {
			return GoldenState{}, goldenStateError("establish participation: %v", err)
		}
		next.Group = group.Snapshot()
	case GoldenReadyEventDisconnected:
		window.ReadyParticipantIDs = removeGoldenID(window.ReadyParticipantIDs, operation.participantID)
		window.PresencePreviousRevisionID = goldenUUID(window.PresenceRevisionID)
		window.PresenceRevisionID = operation.nextPresenceRevisionID
		window.PresenceRevision++
		window.PresentParticipantIDs = removeGoldenID(window.PresentParticipantIDs, operation.participantID)
	case GoldenReadyEventAlreadyReady, GoldenReadyEventAlreadyAbsent:
	}
	next.PreviousRevisionID = goldenUUID(next.RevisionID)
	next.RevisionID = operation.nextStateRevisionID
	next.Revision++
	next.ReadyEvents = append(next.ReadyEvents, GoldenReadyEvent{
		CommandID: operation.commandID, Scope: operation.scope, ParticipantID: operation.participantID,
		Type: resultType, AttemptID: operation.attemptID, WindowID: operation.windowID,
		ExpectedState: authority.Expectation(), ExpectedWindow: authority.Windows[windowIndex].Expectation(),
		CommandDigest:             goldenParticipationCommandDigest(operation),
		ResultStateRevisionID:     operation.nextStateRevisionID,
		ResultWindowRevisionID:    window.RevisionID,
		ResultReadinessRevisionID: window.ReadinessRevisionID,
		ResultPresenceRevisionID:  goldenParticipationResultPresence(*window),
		OccurredAt:                occurredAt,
	})
	return BuildGoldenState(next)
}

func (u *GoldenParticipationUseCase) commitParticipation(
	ctx context.Context,
	operation goldenParticipationOperation,
	expected GoldenStateExpectation,
	next GoldenState,
) (*GoldenState, bool, bool, error) {
	committed, changed, err := u.repository.CommitGoldenState(ctx, GoldenStateCommit{Expected: expected, Next: next})
	if errors.Is(err, domain.ErrConflict) {
		return nil, false, true, nil
	}
	if err != nil {
		return nil, false, false, fmt.Errorf("GoldenParticipationUseCase - commit state: %w", err)
	}
	if committed == nil || committed.Validate() != nil {
		return nil, false, false, domain.ErrInternal
	}
	event, found := goldenReadyEventByCommand(committed.ReadyEvents, operation.commandID)
	result, reconcileErr := reconcileGoldenReadyEvent(*committed, operation, event)
	if !found || reconcileErr != nil || (changed && !committed.Expectation().Equal(next.Expectation())) {
		return nil, false, false, domain.ErrInternal
	}
	return result, changed, false, nil
}

func reconcileGoldenReadyEvent(
	state GoldenState,
	operation goldenParticipationOperation,
	event GoldenReadyEvent,
) (*GoldenState, error) {
	if event.CommandID != operation.commandID || event.Scope != operation.scope ||
		event.ParticipantID != operation.participantID || !goldenReadyEventMatchesOperation(event.Type, operation.typeValue) ||
		event.AttemptID != operation.attemptID || event.WindowID != operation.windowID ||
		!event.ExpectedState.Equal(operation.expectedState) || event.ExpectedWindow != operation.expectedWindow ||
		event.ResultStateRevisionID != operation.nextStateRevisionID ||
		event.CommandDigest != goldenParticipationCommandDigest(operation) {
		return nil, ErrGoldenCommandReuse
	}
	clone := state.Snapshot()
	return &clone, nil
}

func goldenReadyEventMatchesOperation(result, requested GoldenReadyEventType) bool {
	return result == requested ||
		(requested == GoldenReadyEventAccepted && result == GoldenReadyEventAlreadyReady) ||
		(requested == GoldenReadyEventDisconnected && result == GoldenReadyEventAlreadyAbsent)
}

func goldenReadyEventChangesWindow(eventType GoldenReadyEventType) bool {
	return eventType == GoldenReadyEventAccepted || eventType == GoldenReadyEventDisconnected
}

func goldenParticipationResultPresence(window GoldenReadyWindow) uuid.UUID {
	return window.PresenceRevisionID
}

func goldenParticipationCommandDigest(operation goldenParticipationOperation) [sha256.Size]byte {
	type commandDocument struct {
		Scope                   GoldenStateScope             `json:"scope"`
		CommandID               uuid.UUID                    `json:"command_id"`
		ParticipantID           uuid.UUID                    `json:"participant_id"`
		AttemptID               uuid.UUID                    `json:"attempt_id"`
		WindowID                uuid.UUID                    `json:"window_id"`
		ExpectedState           GoldenStateExpectation       `json:"expected_state"`
		ExpectedWindow          GoldenReadyWindowExpectation `json:"expected_window"`
		NextStateRevisionID     uuid.UUID                    `json:"next_state_revision_id"`
		NextWindowRevisionID    uuid.UUID                    `json:"next_window_revision_id"`
		NextReadinessRevisionID uuid.UUID                    `json:"next_readiness_revision_id"`
		NextPresenceRevisionID  uuid.UUID                    `json:"next_presence_revision_id"`
		RequestedTransition     GoldenReadyEventType         `json:"requested_transition"`
	}
	payload, _ := goldenEncode(commandDocument{
		Scope: operation.scope, CommandID: operation.commandID, ParticipantID: operation.participantID,
		AttemptID: operation.attemptID, WindowID: operation.windowID,
		ExpectedState: operation.expectedState, ExpectedWindow: operation.expectedWindow,
		NextStateRevisionID: operation.nextStateRevisionID, NextWindowRevisionID: operation.nextWindowRevisionID,
		NextReadinessRevisionID: operation.nextReadinessRevisionID,
		NextPresenceRevisionID:  operation.nextPresenceRevisionID, RequestedTransition: operation.typeValue,
	})
	return sha256.Sum256(payload)
}
