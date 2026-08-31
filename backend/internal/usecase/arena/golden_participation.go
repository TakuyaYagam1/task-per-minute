package arena

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/gob"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
)

const goldenStateAttempts = 2

var (
	ErrInvalidGoldenState                   = errors.New("invalid Golden state")
	ErrGoldenParticipationAuthorityConflict = errors.New("golden participation authority conflict")
	ErrGoldenParticipationConflict          = errors.New("golden participation commit conflict")
	ErrGoldenCommandReuse                   = errors.New("golden command identifier was reused")
	ErrGoldenRevisionOverflow               = errors.New("golden revision overflow")
	ErrGoldenParticipantExcluded            = errors.New("golden participant is excluded")
	ErrGoldenReadyWindowClosed              = errors.New("golden ready window is closed")
)

type GoldenReadyEventType string

const (
	GoldenReadyEventAccepted      GoldenReadyEventType = "accepted"
	GoldenReadyEventDisconnected  GoldenReadyEventType = "disconnected"
	GoldenReadyEventAlreadyReady  GoldenReadyEventType = "already_ready"
	GoldenReadyEventAlreadyAbsent GoldenReadyEventType = "already_absent"
)

type GoldenReadyWindowState string

const (
	GoldenReadyWindowOpen    GoldenReadyWindowState = "open"
	GoldenReadyWindowExpired GoldenReadyWindowState = "expired"
)

type GoldenStateScope struct {
	TournamentID    uuid.UUID
	GroupID         uuid.UUID
	GroupRevisionID domain.ArenaDerivedRevisionID
}

type GoldenPlanStateBinding struct {
	PlanID                     uuid.UUID
	RevisionID                 uuid.UUID
	Expected                   GoldenExactPlanExpectation
	GroupID                    uuid.UUID
	GroupRevisionID            domain.ArenaDerivedRevisionID
	SourceProjectionRevisionID domain.ArenaDerivedRevisionID
	PositionFrom               int
	PositionTo                 int
	ParticipantDigest          [sha256.Size]byte
	EdgeDigest                 [sha256.Size]byte
	ProofDigest                [sha256.Size]byte
}

type GoldenMembershipRevision struct {
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	PayloadDigest      [sha256.Size]byte
}

type GoldenReadyWindow struct {
	ID                 uuid.UUID
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	AttemptID          uuid.UUID
	AttemptNo          int
	OpenedAt           time.Time
	Deadline           time.Time
	State              GoldenReadyWindowState

	ReadinessRevisionID         uuid.UUID
	ReadinessRevision           int64
	ReadinessPreviousRevisionID *uuid.UUID
	ReadinessDigest             [sha256.Size]byte
	ReadyParticipantIDs         []uuid.UUID

	PresenceRevisionID         uuid.UUID
	PresenceRevision           int64
	PresencePreviousRevisionID *uuid.UUID
	PresenceDigest             [sha256.Size]byte
	BasePresentParticipantIDs  []uuid.UUID
	PresentParticipantIDs      []uuid.UUID
}

type GoldenReadyWindowExpectation struct {
	WindowID            uuid.UUID
	RevisionID          uuid.UUID
	Revision            int64
	ReadinessRevisionID uuid.UUID
	ReadinessRevision   int64
	ReadinessDigest     [sha256.Size]byte
	PresenceRevisionID  uuid.UUID
	PresenceRevision    int64
	PresenceDigest      [sha256.Size]byte
}

type GoldenStateExpectation struct {
	Scope                         GoldenStateScope
	RevisionID                    uuid.UUID
	Revision                      int64
	PayloadDigest                 [sha256.Size]byte
	Plan                          GoldenPlanStateBinding
	Membership                    GoldenMembershipRevision
	SourceProjectionRevisionID    domain.ArenaDerivedRevisionID
	SourceProjectionPayloadDigest [sha256.Size]byte
	TopologyPayloadDigest         [sha256.Size]byte
}

type GoldenReadyEvent struct {
	CommandID      uuid.UUID
	Scope          GoldenStateScope
	ParticipantID  uuid.UUID
	Type           GoldenReadyEventType
	AttemptID      uuid.UUID
	WindowID       uuid.UUID
	ExpectedState  GoldenStateExpectation
	ExpectedWindow GoldenReadyWindowExpectation
	CommandDigest  [sha256.Size]byte

	ResultStateRevisionID     uuid.UUID
	ResultWindowRevisionID    uuid.UUID
	ResultReadinessRevisionID uuid.UUID
	ResultPresenceRevisionID  uuid.UUID
	OccurredAt                time.Time
}

type GoldenState struct {
	Scope              GoldenStateScope
	Topology           GoldenGroupRevision
	ExactPlan          GoldenExactPlan
	Group              domain.ArenaGoldenGroupState
	Plan               GoldenPlanStateBinding
	Membership         GoldenMembershipRevision
	RevisionID         uuid.UUID
	Revision           int64
	PreviousRevisionID *uuid.UUID
	Windows            []GoldenReadyWindow
	ReadyEvents        []GoldenReadyEvent
	NoShows            []GoldenNoShowResolution
	Allocation         *GoldenAllocation
	PayloadDigest      [sha256.Size]byte
}

type GoldenStateCommit struct {
	Expected GoldenStateExpectation
	Next     GoldenState
}

// GoldenStateRepository owns the group-level compare-and-set. Commit must
// compare every identity, revision and digest in Expected before storing Next.
type GoldenStateRepository interface {
	LoadGoldenState(ctx context.Context, scope GoldenStateScope) (GoldenState, error)
	CommitGoldenState(ctx context.Context, commit GoldenStateCommit) (*GoldenState, bool, error)
}

type GoldenReadyCommand struct {
	Scope              GoldenStateScope
	CommandID          uuid.UUID
	ActorParticipantID uuid.UUID
	ParticipantID      uuid.UUID
	AttemptID          uuid.UUID
	WindowID           uuid.UUID
	ExpectedState      GoldenStateExpectation
	ExpectedWindow     GoldenReadyWindowExpectation

	NextStateRevisionID     uuid.UUID
	NextWindowRevisionID    uuid.UUID
	NextReadinessRevisionID uuid.UUID
}

type GoldenDisconnectCommand struct {
	Scope          GoldenStateScope
	CommandID      uuid.UUID
	ParticipantID  uuid.UUID
	AttemptID      uuid.UUID
	WindowID       uuid.UUID
	ExpectedState  GoldenStateExpectation
	ExpectedWindow GoldenReadyWindowExpectation

	NextStateRevisionID     uuid.UUID
	NextWindowRevisionID    uuid.UUID
	NextReadinessRevisionID uuid.UUID
	NextPresenceRevisionID  uuid.UUID
}

type GoldenParticipationUseCase struct {
	repository GoldenStateRepository
	clock      Clock
}

func NewGoldenParticipationUseCase(
	repository GoldenStateRepository,
	clock Clock,
) *GoldenParticipationUseCase {
	return &GoldenParticipationUseCase{repository: repository, clock: clock}
}

func BuildGoldenState(input GoldenState) (GoldenState, error) {
	state := input.Snapshot()
	plan, err := goldenPlanBinding(state.ExactPlan, state.Scope)
	if err != nil {
		return GoldenState{}, err
	}
	if state.Plan == (GoldenPlanStateBinding{}) {
		state.Plan = plan
	} else if state.Plan != plan {
		return GoldenState{}, goldenStateError("exact plan binding changed")
	}
	for index := range state.Windows {
		window := &state.Windows[index]
		if window.BasePresentParticipantIDs == nil {
			window.BasePresentParticipantIDs = append([]uuid.UUID(nil), window.PresentParticipantIDs...)
		}
		canonicalGoldenIDs(window.BasePresentParticipantIDs)
		canonicalGoldenIDs(window.ReadyParticipantIDs)
		canonicalGoldenIDs(window.PresentParticipantIDs)
		window.ReadinessDigest = goldenParticipantSetDigest(window.ReadyParticipantIDs)
		window.PresenceDigest = goldenParticipantSetDigest(window.PresentParticipantIDs)
	}
	state.Membership.PayloadDigest = goldenMembershipDigest(state.Group.Members)
	payload, err := goldenStatePayload(state)
	if err != nil {
		return GoldenState{}, goldenStateError("encode payload: %v", err)
	}
	state.PayloadDigest = sha256.Sum256(payload)
	if err := state.Validate(); err != nil {
		return GoldenState{}, err
	}
	return state.Snapshot(), nil
}

func (s GoldenState) Validate() error {
	if err := validateGoldenStateIdentity(s); err != nil {
		return err
	}
	if err := validateGoldenIdentityRoles(s); err != nil {
		return err
	}
	if err := validateGoldenStateTopology(s); err != nil {
		return err
	}
	if err := validateGoldenStateWindows(s); err != nil {
		return err
	}
	if err := validateGoldenReadyEvents(s); err != nil {
		return err
	}
	if err := validateGoldenNoShows(s); err != nil {
		return err
	}
	if err := validateGoldenAttemptCoverage(s); err != nil {
		return err
	}
	if err := validateGoldenAllocation(s); err != nil {
		return err
	}
	if err := validateGoldenTransitionChain(s); err != nil {
		return err
	}
	payload, err := goldenStatePayload(s)
	if err != nil || s.PayloadDigest == [sha256.Size]byte{} || sha256.Sum256(payload) != s.PayloadDigest {
		return goldenStateError("payload digest changed")
	}
	return nil
}

func (s GoldenState) Snapshot() GoldenState {
	clone := s
	clone.Topology = s.Topology.Snapshot()
	clone.ExactPlan = s.ExactPlan.Snapshot()
	clone.Group = cloneGoldenStateGroup(s.Group)
	clone.Membership.PreviousRevisionID = cloneGoldenUUID(s.Membership.PreviousRevisionID)
	clone.PreviousRevisionID = cloneGoldenUUID(s.PreviousRevisionID)
	clone.Windows = make([]GoldenReadyWindow, len(s.Windows))
	for index, window := range s.Windows {
		clone.Windows[index] = cloneGoldenWindow(window)
	}
	clone.ReadyEvents = make([]GoldenReadyEvent, len(s.ReadyEvents))
	for index, event := range s.ReadyEvents {
		clone.ReadyEvents[index] = event
		clone.ReadyEvents[index].ExpectedState.Membership.PreviousRevisionID = cloneGoldenUUID(
			event.ExpectedState.Membership.PreviousRevisionID,
		)
	}
	clone.NoShows = cloneGoldenNoShows(s.NoShows)
	clone.Allocation = cloneGoldenAllocation(s.Allocation)
	return clone
}

func (s GoldenState) Expectation() GoldenStateExpectation {
	return GoldenStateExpectation{
		Scope: s.Scope, RevisionID: s.RevisionID, Revision: s.Revision,
		PayloadDigest: s.PayloadDigest, Plan: s.Plan, Membership: s.Membership,
		SourceProjectionRevisionID:    s.Topology.SourceProjectionRevisionID(),
		SourceProjectionPayloadDigest: s.Topology.SourceProjectionPayloadDigest(),
		TopologyPayloadDigest:         s.Topology.PayloadDigest(),
	}
}

func (e GoldenStateExpectation) Equal(other GoldenStateExpectation) bool {
	return e.Scope == other.Scope && e.RevisionID == other.RevisionID && e.Revision == other.Revision &&
		e.PayloadDigest == other.PayloadDigest && e.Plan == other.Plan &&
		goldenMembershipRevisionsEqual(e.Membership, other.Membership) &&
		e.SourceProjectionRevisionID == other.SourceProjectionRevisionID &&
		e.SourceProjectionPayloadDigest == other.SourceProjectionPayloadDigest &&
		e.TopologyPayloadDigest == other.TopologyPayloadDigest
}

func (w GoldenReadyWindow) Expectation() GoldenReadyWindowExpectation {
	return GoldenReadyWindowExpectation{
		WindowID: w.ID, RevisionID: w.RevisionID, Revision: w.Revision,
		ReadinessRevisionID: w.ReadinessRevisionID, ReadinessRevision: w.ReadinessRevision,
		ReadinessDigest:    w.ReadinessDigest,
		PresenceRevisionID: w.PresenceRevisionID, PresenceRevision: w.PresenceRevision,
		PresenceDigest: w.PresenceDigest,
	}
}

func (s GoldenState) ActiveParticipantIDs() []uuid.UUID {
	active := make([]uuid.UUID, 0, len(s.Group.Members))
	for _, member := range s.Group.Members {
		if !member.Excluded {
			active = append(active, member.ParticipantID)
		}
	}
	return active
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
		return nil, false, domain.ErrArenaAssignmentParticipant
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
	if !validArenaServerTime(occurredAt) {
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
	member, found := goldenStateMember(authority.Group.Members, operation.participantID)
	if goldenAny(!found, !goldenAttemptContains(authority.Group.Attempts, operation.attemptID, operation.participantID)) {
		return nil, false, false, domain.ErrArenaAssignmentParticipant
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
		group, err := domain.NewArenaGoldenGroup(next.Group)
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

func validateGoldenOperationIDs(state GoldenState, operation goldenParticipationOperation) error {
	return validateGoldenFreshIDs(
		state,
		operation.commandID,
		operation.nextStateRevisionID,
		operation.nextWindowRevisionID,
		operation.nextReadinessRevisionID,
		operation.nextPresenceRevisionID,
	)
}

func validateGoldenFreshIDs(state GoldenState, candidates ...uuid.UUID) error {
	roles := goldenCoreIdentityRoles(state)
	roles = append(roles, goldenPlanIdentityRoles(state)...)
	roles = append(roles, goldenWindowIdentityRoles(state)...)
	roles = append(roles, goldenTransitionIdentityRoles(state)...)
	reserved := make(map[uuid.UUID]struct{}, len(roles))
	for _, role := range roles {
		reserved[role.value] = struct{}{}
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

func validateGoldenStateIdentity(state GoldenState) error {
	invalidStatePredecessor := state.Revision == 1 && state.PreviousRevisionID != nil
	if state.Revision > 1 {
		invalidStatePredecessor = state.PreviousRevisionID == nil
		if state.PreviousRevisionID != nil {
			invalidStatePredecessor = *state.PreviousRevisionID == uuid.Nil || *state.PreviousRevisionID == state.RevisionID
		}
	}
	if goldenAny(
		!validGoldenStateScope(state.Scope), state.RevisionID == uuid.Nil, state.Revision < 1,
		invalidStatePredecessor, state.Plan.PlanID == uuid.Nil, state.Plan.RevisionID == uuid.Nil,
		state.Plan.PlanID == state.Plan.RevisionID, state.Plan.ProofDigest == [sha256.Size]byte{},
		state.Plan.EdgeDigest == [sha256.Size]byte{}, state.Plan.ParticipantDigest == [sha256.Size]byte{},
		state.Membership.RevisionID == uuid.Nil, state.Membership.Revision < 1,
	) {
		return goldenStateError("invalid identity or revision lineage")
	}
	if state.Membership.PayloadDigest != goldenMembershipDigest(state.Group.Members) {
		return goldenStateError("membership digest changed")
	}
	invalidMembershipPredecessor := state.Membership.Revision == 1 && state.Membership.PreviousRevisionID != nil
	if state.Membership.Revision > 1 {
		invalidMembershipPredecessor = state.Membership.PreviousRevisionID == nil
		if state.Membership.PreviousRevisionID != nil {
			invalidMembershipPredecessor = *state.Membership.PreviousRevisionID == uuid.Nil ||
				*state.Membership.PreviousRevisionID == state.Membership.RevisionID
		}
	}
	if invalidMembershipPredecessor {
		return goldenStateError("invalid membership revision lineage")
	}
	return nil
}

type goldenIdentityRole struct {
	value uuid.UUID
	role  string
}

func validateGoldenIdentityRoles(state GoldenState) error {
	roles := goldenCoreIdentityRoles(state)
	roles = append(roles, goldenPlanIdentityRoles(state)...)
	roles = append(roles, goldenWindowIdentityRoles(state)...)
	roles = append(roles, goldenTransitionIdentityRoles(state)...)
	owners := make(map[uuid.UUID]string, len(roles))
	for _, identity := range roles {
		if identity.value == uuid.Nil {
			continue
		}
		if existing, found := owners[identity.value]; found && existing != identity.role {
			return goldenStateError("identity is reused across Golden authority roles")
		}
		owners[identity.value] = identity.role
	}
	return nil
}

func goldenCoreIdentityRoles(state GoldenState) []goldenIdentityRole {
	roles := []goldenIdentityRole{
		{state.Scope.TournamentID, "tournament"},
		{state.Scope.GroupID, goldenEntityRole("group", state.Scope.GroupID)},
		{state.Scope.GroupRevisionID.UUID(), goldenEntityRole("group-revision", state.Scope.GroupRevisionID.UUID())},
		{state.Topology.SourceProjectionRevisionID().UUID(), "source-projection-revision"},
		{state.RevisionID, goldenStateRevisionRole(state.Revision)},
		{state.Membership.RevisionID, goldenMembershipRevisionRole(state.Membership.Revision)},
		{state.Plan.PlanID, "plan"}, {state.Plan.RevisionID, "plan-revision"},
		{state.ExactPlan.Scope.PlanSetID, "plan-set"},
	}
	if state.PreviousRevisionID != nil {
		roles = append(roles, goldenIdentityRole{*state.PreviousRevisionID, goldenStateRevisionRole(state.Revision - 1)})
	}
	if state.Membership.PreviousRevisionID != nil {
		roles = append(roles, goldenIdentityRole{
			*state.Membership.PreviousRevisionID,
			goldenMembershipRevisionRole(state.Membership.Revision - 1),
		})
	}
	for _, member := range state.Group.Members {
		roles = append(roles, goldenIdentityRole{member.ParticipantID, goldenEntityRole("participant", member.ParticipantID)})
	}
	for _, attempt := range state.Group.Attempts {
		roles = append(roles, goldenIdentityRole{attempt.ID, goldenEntityRole("attempt", attempt.ID)})
		if attempt.PreviousAttemptID != nil {
			roles = append(roles, goldenIdentityRole{*attempt.PreviousAttemptID, goldenEntityRole("attempt", *attempt.PreviousAttemptID)})
		}
	}
	return roles
}

func goldenPlanIdentityRoles(state GoldenState) []goldenIdentityRole {
	revisions := state.ExactPlan.Expected.Revisions
	roles := []goldenIdentityRole{
		{revisions.SourceProjectionRevisionID.UUID(), "source-projection-revision"},
		{revisions.GroupSetRevisionID, "plan-group-set-revision"},
		{revisions.PoolRevisionID, "plan-pool-revision"},
		{revisions.HistoryRevisionID, "plan-history-revision"},
		{revisions.TaskHealthRevisionID, "plan-health-revision"},
		{revisions.ArtifactRevisionID, "plan-artifact-revision"},
		{revisions.ReservationRevisionID, "plan-reservation-revision"},
		{revisions.MembershipRevisionID, "plan-membership-revision"},
		{state.ExactPlan.Authority.Pool.ID, "plan-pool-revision"},
	}
	for _, group := range state.ExactPlan.Groups {
		roles = append(roles,
			goldenIdentityRole{group.GroupID, goldenEntityRole("group", group.GroupID)},
			goldenIdentityRole{group.GroupRevisionID.UUID(), goldenEntityRole("group-revision", group.GroupRevisionID.UUID())},
			goldenIdentityRole{group.SourceProjectionRevisionID.UUID(), "source-projection-revision"},
		)
		for _, participantID := range group.ParticipantIDs {
			roles = append(roles, goldenIdentityRole{participantID, goldenEntityRole("participant", participantID)})
		}
		for _, edge := range group.Edges {
			roles = append(roles,
				goldenIdentityRole{edge.ID, goldenEntityRole("plan-edge", edge.ID)},
				goldenIdentityRole{edge.ReservationID, goldenEntityRole("task-reservation", edge.ReservationID)},
				goldenIdentityRole{edge.Snapshot.SnapshotID, goldenEntityRole("task-snapshot", edge.Snapshot.SnapshotID)},
				goldenIdentityRole{edge.Snapshot.TaskID, goldenEntityRole("task", edge.Snapshot.TaskID)},
			)
		}
	}
	for _, authorityGroup := range state.ExactPlan.Authority.Groups {
		revision := authorityGroup.Revision
		roles = append(roles,
			goldenIdentityRole{revision.GroupID(), goldenEntityRole("group", revision.GroupID())},
			goldenIdentityRole{revision.RevisionID().UUID(), goldenEntityRole("group-revision", revision.RevisionID().UUID())},
		)
		for _, member := range revision.Members() {
			roles = append(roles, goldenIdentityRole{member.ParticipantID, goldenEntityRole("participant", member.ParticipantID)})
		}
	}
	return roles
}

func goldenWindowIdentityRoles(state GoldenState) []goldenIdentityRole {
	roles := make([]goldenIdentityRole, 0, len(state.Windows)*7)
	for _, window := range state.Windows {
		roles = append(roles,
			goldenIdentityRole{window.ID, goldenEntityRole("window", window.ID)},
			goldenIdentityRole{window.RevisionID, goldenWindowRevisionRole(window.ID, "state", window.Revision)},
			goldenIdentityRole{window.ReadinessRevisionID, goldenWindowRevisionRole(window.ID, "readiness", window.ReadinessRevision)},
			goldenIdentityRole{window.PresenceRevisionID, goldenWindowRevisionRole(window.ID, "presence", window.PresenceRevision)},
		)
		if window.PreviousRevisionID != nil {
			roles = append(roles, goldenIdentityRole{*window.PreviousRevisionID, goldenWindowRevisionRole(window.ID, "state", window.Revision-1)})
		}
		if window.ReadinessPreviousRevisionID != nil {
			roles = append(roles, goldenIdentityRole{*window.ReadinessPreviousRevisionID, goldenWindowRevisionRole(window.ID, "readiness", window.ReadinessRevision-1)})
		}
		if window.PresencePreviousRevisionID != nil {
			roles = append(roles, goldenIdentityRole{*window.PresencePreviousRevisionID, goldenWindowRevisionRole(window.ID, "presence", window.PresenceRevision-1)})
		}
	}
	return roles
}

func goldenTransitionIdentityRoles(state GoldenState) []goldenIdentityRole {
	roles := make([]goldenIdentityRole, 0, len(state.ReadyEvents)*7+len(state.NoShows)*6+4)
	for _, event := range state.ReadyEvents {
		roles = append(roles, goldenExpectationIdentityRoles(event.ExpectedState)...)
		roles = append(roles, goldenWindowExpectationIdentityRoles(event.ExpectedWindow)...)
		windowStep := int64(0)
		if goldenReadyEventChangesWindow(event.Type) {
			windowStep = 1
		}
		presenceStep := int64(0)
		if event.Type == GoldenReadyEventDisconnected {
			presenceStep = 1
		}
		roles = append(roles,
			goldenIdentityRole{event.CommandID, goldenEntityRole("command", event.CommandID)},
			goldenIdentityRole{event.ResultStateRevisionID, goldenStateRevisionRole(event.ExpectedState.Revision + 1)},
			goldenIdentityRole{event.ResultWindowRevisionID, goldenWindowRevisionRole(event.WindowID, "state", event.ExpectedWindow.Revision+windowStep)},
			goldenIdentityRole{event.ResultReadinessRevisionID, goldenWindowRevisionRole(event.WindowID, "readiness", event.ExpectedWindow.ReadinessRevision+windowStep)},
			goldenIdentityRole{event.ResultPresenceRevisionID, goldenWindowRevisionRole(event.WindowID, "presence", event.ExpectedWindow.PresenceRevision+presenceStep)},
		)
	}
	for _, resolution := range state.NoShows {
		roles = append(roles, goldenExpectationIdentityRoles(resolution.ExpectedState)...)
		roles = append(roles, goldenWindowExpectationIdentityRoles(resolution.ExpectedWindow)...)
		roles = append(roles,
			goldenIdentityRole{resolution.CommandID, goldenEntityRole("command", resolution.CommandID)},
			goldenIdentityRole{resolution.ResultStateRevisionID, goldenStateRevisionRole(resolution.ExpectedState.Revision + 1)},
			goldenIdentityRole{resolution.ResultWindowRevisionID, goldenWindowRevisionRole(resolution.WindowID, "state", resolution.ExpectedWindow.Revision+1)},
			goldenIdentityRole{resolution.ResultMembershipRevisionID, goldenMembershipRevisionRole(resolution.ExpectedState.Membership.Revision + 1)},
		)
	}
	if state.Allocation != nil {
		roles = append(roles, goldenExpectationIdentityRoles(state.Allocation.ExpectedState)...)
		roles = append(roles,
			goldenIdentityRole{state.Allocation.ID, goldenEntityRole("allocation", state.Allocation.ID)},
			goldenIdentityRole{state.Allocation.CommandID, goldenEntityRole("command", state.Allocation.CommandID)},
			goldenIdentityRole{state.Allocation.ResultStateRevisionID, goldenStateRevisionRole(state.Allocation.ExpectedState.Revision + 1)},
		)
	}
	return roles
}

func goldenExpectationIdentityRoles(expected GoldenStateExpectation) []goldenIdentityRole {
	roles := []goldenIdentityRole{
		{expected.RevisionID, goldenStateRevisionRole(expected.Revision)},
		{expected.Membership.RevisionID, goldenMembershipRevisionRole(expected.Membership.Revision)},
	}
	if expected.Membership.PreviousRevisionID != nil {
		roles = append(roles, goldenIdentityRole{
			*expected.Membership.PreviousRevisionID,
			goldenMembershipRevisionRole(expected.Membership.Revision - 1),
		})
	}
	return roles
}

func goldenWindowExpectationIdentityRoles(expected GoldenReadyWindowExpectation) []goldenIdentityRole {
	return []goldenIdentityRole{
		{expected.RevisionID, goldenWindowRevisionRole(expected.WindowID, "state", expected.Revision)},
		{expected.ReadinessRevisionID, goldenWindowRevisionRole(expected.WindowID, "readiness", expected.ReadinessRevision)},
		{expected.PresenceRevisionID, goldenWindowRevisionRole(expected.WindowID, "presence", expected.PresenceRevision)},
	}
}

func goldenEntityRole(kind string, value uuid.UUID) string {
	return kind + ":" + value.String()
}

func goldenStateRevisionRole(revision int64) string {
	return fmt.Sprintf("state-revision:%d", revision)
}

func goldenMembershipRevisionRole(revision int64) string {
	return fmt.Sprintf("membership-revision:%d", revision)
}

func goldenWindowRevisionRole(windowID uuid.UUID, kind string, revision int64) string {
	return fmt.Sprintf("window:%s:%s-revision:%d", windowID, kind, revision)
}

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
	group, err := domain.NewArenaGoldenGroup(state.Group)
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

func goldenPlanBinding(plan GoldenExactPlan, scope GoldenStateScope) (GoldenPlanStateBinding, error) {
	if err := plan.Validate(); err != nil || plan.Scope.TournamentID != scope.TournamentID {
		return GoldenPlanStateBinding{}, goldenStateError("invalid exact plan")
	}
	var group *GoldenExactGroupPlan
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
		if err := validateGoldenWindowIdentity(window); err != nil {
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

func validateGoldenWindowIdentity(window GoldenReadyWindow) error {
	if goldenAny(
		window.ID == uuid.Nil, window.RevisionID == uuid.Nil, window.Revision < 1,
		window.AttemptID == uuid.Nil, window.AttemptNo < 1,
		!validArenaServerTime(window.OpenedAt), !validArenaServerTime(window.Deadline),
		!window.Deadline.After(window.OpenedAt),
		window.State != GoldenReadyWindowOpen && window.State != GoldenReadyWindowExpired,
		window.ReadinessRevisionID == uuid.Nil, window.ReadinessRevision < 1,
		window.PresenceRevisionID == uuid.Nil, window.PresenceRevision < 1,
	) {
		return goldenStateError("invalid ready-window identity or interval")
	}
	if goldenAny(
		!validGoldenRevisionPredecessor(window.RevisionID, window.Revision, window.PreviousRevisionID),
		!validGoldenRevisionPredecessor(
			window.ReadinessRevisionID,
			window.ReadinessRevision,
			window.ReadinessPreviousRevisionID,
		), !validGoldenRevisionPredecessor(
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
	_, memberFound := goldenStateMember(state.Group.Members, event.ParticipantID)
	if goldenAny(
		!windowFound, event.CommandID == uuid.Nil, event.CommandDigest == [sha256.Size]byte{},
		event.Scope != state.Scope, event.ParticipantID == uuid.Nil, !memberFound,
		event.AttemptID != window.AttemptID,
		!goldenAttemptContains(state.Group.Attempts, event.AttemptID, event.ParticipantID),
		!validArenaServerTime(event.OccurredAt), event.OccurredAt.Before(window.OpenedAt),
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

func applyGoldenReadyEvent(
	event GoldenReadyEvent,
	ready map[uuid.UUID]bool,
	present map[uuid.UUID]bool,
) (bool, error) {
	switch event.Type {
	case GoldenReadyEventAccepted:
		if goldenAny(event.ResultPresenceRevisionID != event.ExpectedWindow.PresenceRevisionID, !present[event.ParticipantID]) {
			return false, goldenStateError("invalid accepted-ready receipt")
		}
		ready[event.ParticipantID] = true
		return true, nil
	case GoldenReadyEventDisconnected:
		if event.ResultPresenceRevisionID == uuid.Nil {
			return false, goldenStateError("disconnect lacks presence revision")
		}
		delete(ready, event.ParticipantID)
		delete(present, event.ParticipantID)
	case GoldenReadyEventAlreadyReady:
		if goldenAny(
			!ready[event.ParticipantID],
			event.ResultWindowRevisionID != event.ExpectedWindow.RevisionID,
			event.ResultReadinessRevisionID != event.ExpectedWindow.ReadinessRevisionID,
			event.ResultPresenceRevisionID != event.ExpectedWindow.PresenceRevisionID,
		) {
			return false, goldenStateError("invalid already-ready receipt")
		}
	case GoldenReadyEventAlreadyAbsent:
		if goldenAny(
			present[event.ParticipantID],
			event.ResultWindowRevisionID != event.ExpectedWindow.RevisionID,
			event.ResultReadinessRevisionID != event.ExpectedWindow.ReadinessRevisionID,
			event.ResultPresenceRevisionID != event.ExpectedWindow.PresenceRevisionID,
		) {
			return false, goldenStateError("invalid already-absent receipt")
		}
	default:
		return false, goldenStateError("unknown retained ready event")
	}
	return false, nil
}

func validateGoldenReadyEventSuccessor(event GoldenReadyEvent) error {
	changesWindow := goldenReadyEventChangesWindow(event.Type)
	if goldenAny(
		event.ExpectedState.Revision == math.MaxInt64,
		event.ResultStateRevisionID == event.ExpectedState.RevisionID,
		changesWindow && event.ExpectedWindow.Revision == math.MaxInt64,
		changesWindow && event.ExpectedWindow.ReadinessRevision == math.MaxInt64,
		changesWindow && event.ResultWindowRevisionID == event.ExpectedWindow.RevisionID,
		changesWindow && event.ResultReadinessRevisionID == event.ExpectedWindow.ReadinessRevisionID,
	) {
		return goldenStateError("invalid retained ready revision successor")
	}
	return nil
}

func validateGoldenReadyEventChain(event, prior GoldenReadyEvent) error {
	windowStep := int64(0)
	if goldenReadyEventChangesWindow(prior.Type) {
		windowStep = 1
	}
	if goldenAny(
		event.ExpectedState.RevisionID != prior.ResultStateRevisionID,
		event.ExpectedState.Revision != prior.ExpectedState.Revision+1,
		event.ExpectedWindow.RevisionID != prior.ResultWindowRevisionID,
		event.ExpectedWindow.Revision != prior.ExpectedWindow.Revision+windowStep,
		event.ExpectedWindow.ReadinessRevisionID != prior.ResultReadinessRevisionID,
		event.ExpectedWindow.ReadinessRevision != prior.ExpectedWindow.ReadinessRevision+windowStep,
	) {
		return goldenStateError("retained ready revision chain is broken")
	}
	presenceStep := int64(0)
	if prior.Type == GoldenReadyEventDisconnected {
		presenceStep = 1
	}
	if goldenAny(
		event.ExpectedWindow.PresenceRevisionID != prior.ResultPresenceRevisionID,
		event.ExpectedWindow.PresenceRevision != prior.ExpectedWindow.PresenceRevision+presenceStep,
	) {
		return goldenStateError("retained presence revision chain is broken")
	}
	return nil
}

type goldenRetainedTransition struct {
	expected GoldenStateExpectation
	result   uuid.UUID
	ready    *GoldenReadyEvent
	noShow   *GoldenNoShowResolution
	allocate *GoldenAllocation
}

func validateGoldenTransitionChain(state GoldenState) error {
	transitions := goldenRetainedTransitions(state)
	if len(transitions) == 0 {
		return validateGoldenEmptyTransitionState(state)
	}
	sort.Slice(transitions, func(i, j int) bool {
		return transitions[i].expected.Revision < transitions[j].expected.Revision
	})
	if goldenAny(
		transitions[0].expected.Revision < 1,
		transitions[0].expected.Revision > math.MaxInt64-int64(len(transitions)),
		transitions[0].expected.Revision+int64(len(transitions)) != state.Revision,
	) {
		return goldenStateError("retained state transition count is inconsistent")
	}
	members := cloneGoldenStateGroup(state.Group).Members
	for index := range members {
		members[index].Excluded = false
	}
	membership := transitions[0].expected.Membership
	if membership.PayloadDigest != goldenMembershipDigest(members) {
		return goldenStateError("initial retained membership digest is inconsistent")
	}
	for index, transition := range transitions {
		if err := validateGoldenTransitionAuthority(state, transition, membership); err != nil {
			return err
		}
		if index > 0 {
			prior := transitions[index-1]
			if goldenAny(
				transition.expected.Revision != prior.expected.Revision+1,
				transition.expected.RevisionID != prior.result,
			) {
				return goldenStateError("retained state transition chain is broken")
			}
		}
		if goldenTransitionHasExcludedReady(transition, members) {
			return goldenNoShowError("excluded participant has a later ready receipt")
		}
		if transition.noShow != nil {
			var err error
			membership, err = advanceGoldenTransitionMembership(membership, members, *transition.noShow)
			if err != nil {
				return err
			}
		}
	}
	final := transitions[len(transitions)-1]
	if goldenAny(
		final.result != state.RevisionID,
		final.expected.Revision+1 != state.Revision,
		goldenWrongPreviousState(state.PreviousRevisionID, final.expected.RevisionID),
		!goldenMembershipRevisionsEqual(membership, state.Membership),
	) {
		return goldenStateError("final retained transition does not link current state")
	}
	return nil
}

func validateGoldenEmptyTransitionState(state GoldenState) error {
	if state.Revision != 1 {
		return goldenStateError("state revision has no retained transition")
	}
	return nil
}

func goldenTransitionHasExcludedReady(
	transition goldenRetainedTransition,
	members []domain.ArenaGoldenMember,
) bool {
	return transition.ready != nil && goldenMemberIsExcluded(members, transition.ready.ParticipantID)
}

func goldenWrongPreviousState(previous *uuid.UUID, expected uuid.UUID) bool {
	return previous == nil || *previous != expected
}

func goldenRetainedTransitions(state GoldenState) []goldenRetainedTransition {
	transitions := make([]goldenRetainedTransition, 0, len(state.ReadyEvents)+len(state.NoShows)+1)
	for index := range state.ReadyEvents {
		event := &state.ReadyEvents[index]
		transitions = append(transitions, goldenRetainedTransition{
			expected: event.ExpectedState, result: event.ResultStateRevisionID, ready: event,
		})
	}
	for index := range state.NoShows {
		resolution := &state.NoShows[index]
		transitions = append(transitions, goldenRetainedTransition{
			expected: resolution.ExpectedState, result: resolution.ResultStateRevisionID, noShow: resolution,
		})
	}
	if state.Allocation != nil {
		transitions = append(transitions, goldenRetainedTransition{
			expected: state.Allocation.ExpectedState,
			result:   state.Allocation.ResultStateRevisionID,
			allocate: state.Allocation,
		})
	}
	return transitions
}

func validateGoldenTransitionAuthority(
	state GoldenState,
	transition goldenRetainedTransition,
	membership GoldenMembershipRevision,
) error {
	expected := transition.expected
	if goldenAny(
		expected.Scope != state.Scope, expected.Plan != state.Plan,
		expected.SourceProjectionRevisionID != state.Topology.SourceProjectionRevisionID(),
		expected.SourceProjectionPayloadDigest != state.Topology.SourceProjectionPayloadDigest(),
		expected.TopologyPayloadDigest != state.Topology.PayloadDigest(),
		expected.PayloadDigest == [sha256.Size]byte{}, transition.result == uuid.Nil,
		transition.result == expected.RevisionID,
		!goldenMembershipRevisionsEqual(expected.Membership, membership),
	) {
		return goldenStateError("retained transition authority changed")
	}
	return nil
}

func goldenMemberIsExcluded(members []domain.ArenaGoldenMember, participantID uuid.UUID) bool {
	for _, member := range members {
		if member.ParticipantID == participantID {
			return member.Excluded
		}
	}
	return false
}

func advanceGoldenTransitionMembership(
	membership GoldenMembershipRevision,
	members []domain.ArenaGoldenMember,
	resolution GoldenNoShowResolution,
) (GoldenMembershipRevision, error) {
	if goldenAny(
		membership.Revision == math.MaxInt64,
		resolution.ResultMembershipRevisionID == membership.RevisionID,
	) {
		return GoldenMembershipRevision{}, goldenNoShowError("invalid retained membership successor")
	}
	previousRevisionID := membership.RevisionID
	membership.RevisionID = resolution.ResultMembershipRevisionID
	membership.Revision++
	membership.PreviousRevisionID = goldenUUID(previousRevisionID)
	for _, participantID := range resolution.ExcludedParticipantIDs {
		for memberIndex := range members {
			if members[memberIndex].ParticipantID == participantID {
				members[memberIndex].Excluded = true
			}
		}
	}
	membership.PayloadDigest = goldenMembershipDigest(members)
	return membership, nil
}

func validGoldenRevisionPredecessor(current uuid.UUID, revision int64, previous *uuid.UUID) bool {
	return (revision == 1 && previous == nil) ||
		(revision > 1 && previous != nil && *previous != uuid.Nil && *previous != current)
}

func goldenMembershipRevisionsEqual(first, second GoldenMembershipRevision) bool {
	if first.RevisionID != second.RevisionID || first.Revision != second.Revision ||
		first.PayloadDigest != second.PayloadDigest {
		return false
	}
	if first.PreviousRevisionID == nil || second.PreviousRevisionID == nil {
		return first.PreviousRevisionID == nil && second.PreviousRevisionID == nil
	}
	return *first.PreviousRevisionID == *second.PreviousRevisionID
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

func goldenAny(conditions ...bool) bool {
	for _, condition := range conditions {
		if condition {
			return true
		}
	}
	return false
}

func goldenStatePayload(state GoldenState) ([]byte, error) {
	type payloadDocument struct {
		Scope              GoldenStateScope
		TopologyDigest     [sha256.Size]byte
		Group              domain.ArenaGoldenGroupState
		Plan               GoldenPlanStateBinding
		Membership         GoldenMembershipRevision
		RevisionID         uuid.UUID
		Revision           int64
		PreviousRevisionID *uuid.UUID
		Windows            []GoldenReadyWindow
		ReadyEvents        []GoldenReadyEvent
		NoShows            []GoldenNoShowResolution
		Allocation         *GoldenAllocation
	}
	return goldenEncode(payloadDocument{
		Scope: state.Scope, TopologyDigest: state.Topology.PayloadDigest(), Group: state.Group,
		Plan: state.Plan, Membership: state.Membership, RevisionID: state.RevisionID,
		Revision: state.Revision, PreviousRevisionID: state.PreviousRevisionID,
		Windows: state.Windows, ReadyEvents: state.ReadyEvents, NoShows: state.NoShows,
		Allocation: state.Allocation,
	})
}

func goldenMembershipDigest(members []domain.ArenaGoldenMember) [sha256.Size]byte {
	type memberDocument struct {
		ParticipantID uuid.UUID
		Excluded      bool
	}
	document := make([]memberDocument, len(members))
	for index, member := range members {
		document[index] = memberDocument{ParticipantID: member.ParticipantID, Excluded: member.Excluded}
	}
	sort.Slice(document, func(i, j int) bool {
		return bytes.Compare(document[i].ParticipantID[:], document[j].ParticipantID[:]) < 0
	})
	payload, _ := goldenEncode(document)
	return sha256.Sum256(payload)
}

func goldenParticipantSetDigest(participantIDs []uuid.UUID) [sha256.Size]byte {
	canonical := append([]uuid.UUID(nil), participantIDs...)
	canonicalGoldenIDs(canonical)
	payload, _ := goldenEncode(canonical)
	return sha256.Sum256(payload)
}

func goldenEncode(value any) ([]byte, error) {
	var buffer bytes.Buffer
	if err := gob.NewEncoder(&buffer).Encode(value); err != nil {
		return nil, err
	}
	return buffer.Bytes(), nil
}

func canonicalGoldenIDs(values []uuid.UUID) {
	sort.Slice(values, func(i, j int) bool { return bytes.Compare(values[i][:], values[j][:]) < 0 })
}

func goldenIDsAreCanonical(values []uuid.UUID) bool {
	for index, value := range values {
		if value == uuid.Nil || (index > 0 && bytes.Compare(values[index-1][:], value[:]) >= 0) {
			return false
		}
	}
	return true
}

func goldenIDsSubset(values, superset []uuid.UUID) bool {
	for _, value := range values {
		if !goldenIDsContain(superset, value) {
			return false
		}
	}
	return true
}

func equalGoldenIDs(first, second []uuid.UUID) bool {
	if len(first) != len(second) {
		return false
	}
	for index := range first {
		if first[index] != second[index] {
			return false
		}
	}
	return true
}

func goldenIDsContain(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func removeGoldenID(values []uuid.UUID, target uuid.UUID) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		if value != target {
			result = append(result, value)
		}
	}
	return result
}

func goldenIDState(values []uuid.UUID) map[uuid.UUID]bool {
	result := make(map[uuid.UUID]bool, len(values))
	for _, value := range values {
		result[value] = true
	}
	return result
}

func goldenStateIDs(values map[uuid.UUID]bool) []uuid.UUID {
	result := make([]uuid.UUID, 0, len(values))
	for value, present := range values {
		if present {
			result = append(result, value)
		}
	}
	canonicalGoldenIDs(result)
	return result
}

func goldenStateMember(members []domain.ArenaGoldenMember, participantID uuid.UUID) (domain.ArenaGoldenMember, bool) {
	for _, member := range members {
		if member.ParticipantID == participantID {
			return member, true
		}
	}
	return domain.ArenaGoldenMember{}, false
}

func goldenAttempt(attempts []domain.ArenaGoldenAttempt, attemptID uuid.UUID) (domain.ArenaGoldenAttempt, bool) {
	for _, attempt := range attempts {
		if attempt.ID == attemptID {
			return attempt, true
		}
	}
	return domain.ArenaGoldenAttempt{}, false
}

func goldenAttemptContains(attempts []domain.ArenaGoldenAttempt, attemptID, participantID uuid.UUID) bool {
	attempt, found := goldenAttempt(attempts, attemptID)
	return found && goldenIDsContain(attempt.ParticipantIDs, participantID)
}

func goldenWindowIndex(windows []GoldenReadyWindow, windowID uuid.UUID) int {
	for index := range windows {
		if windows[index].ID == windowID {
			return index
		}
	}
	return -1
}

func goldenReadyEventByCommand(events []GoldenReadyEvent, commandID uuid.UUID) (GoldenReadyEvent, bool) {
	for _, event := range events {
		if event.CommandID == commandID {
			return event, true
		}
	}
	return GoldenReadyEvent{}, false
}

func goldenCommandIDRetainedOutsideReady(state GoldenState, commandID uuid.UUID) bool {
	if _, found := goldenNoShowByCommand(state.NoShows, commandID); found {
		return true
	}
	return state.Allocation != nil && state.Allocation.CommandID == commandID
}

func cloneGoldenStateGroup(state domain.ArenaGoldenGroupState) domain.ArenaGoldenGroupState {
	clone := state
	clone.Members = append([]domain.ArenaGoldenMember(nil), state.Members...)
	clone.Attempts = make([]domain.ArenaGoldenAttempt, len(state.Attempts))
	for index, attempt := range state.Attempts {
		clone.Attempts[index] = attempt
		clone.Attempts[index].PreviousAttemptID = cloneGoldenUUID(attempt.PreviousAttemptID)
		clone.Attempts[index].ParticipantIDs = append([]uuid.UUID(nil), attempt.ParticipantIDs...)
		clone.Attempts[index].RetainedAt = cloneGoldenTime(attempt.RetainedAt)
		clone.Attempts[index].StartedAt = cloneGoldenTime(attempt.StartedAt)
		clone.Attempts[index].FinishedAt = cloneGoldenTime(attempt.FinishedAt)
	}
	return clone
}

func cloneGoldenWindow(window GoldenReadyWindow) GoldenReadyWindow {
	clone := window
	clone.PreviousRevisionID = cloneGoldenUUID(window.PreviousRevisionID)
	clone.ReadinessPreviousRevisionID = cloneGoldenUUID(window.ReadinessPreviousRevisionID)
	clone.PresencePreviousRevisionID = cloneGoldenUUID(window.PresencePreviousRevisionID)
	clone.ReadyParticipantIDs = append([]uuid.UUID(nil), window.ReadyParticipantIDs...)
	clone.BasePresentParticipantIDs = append([]uuid.UUID(nil), window.BasePresentParticipantIDs...)
	clone.PresentParticipantIDs = append([]uuid.UUID(nil), window.PresentParticipantIDs...)
	return clone
}

func cloneGoldenUUID(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGoldenTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func goldenUUID(value uuid.UUID) *uuid.UUID { return &value }

func goldenStateError(format string, arguments ...any) error {
	return fmt.Errorf("%w: %s", ErrInvalidGoldenState, fmt.Sprintf(format, arguments...))
}
