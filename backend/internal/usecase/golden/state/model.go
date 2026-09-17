package state

import (
	"bytes"
	"crypto/sha256"
	"encoding/gob"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	goldenplan "github.com/TakuyaYagam1/task-per-minute/internal/usecase/golden/plan"
)

var (
	ErrInvalidGoldenState        = errors.New("invalid Golden state")
	ErrInvalidGoldenNoShow       = errors.New("invalid Golden no-show resolution")
	ErrInvalidGoldenFallback     = errors.New("invalid Golden fallback allocation")
	ErrGoldenFallbackNotRequired = errors.New("golden fallback allocation is not required")
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
	GoldenReadyWindowOpen     GoldenReadyWindowState = "open"
	GoldenReadyWindowExpired  GoldenReadyWindowState = "expired"
	GoldenReadyWindowConsumed GoldenReadyWindowState = "consumed"
)

type GoldenStateScope struct {
	TournamentID    uuid.UUID
	GroupID         uuid.UUID
	GroupRevisionID domain.DerivedRevisionID
}

type GoldenPlanStateBinding struct {
	PlanID                     uuid.UUID
	RevisionID                 uuid.UUID
	Expected                   goldenplan.Expectation
	GroupID                    uuid.UUID
	GroupRevisionID            domain.DerivedRevisionID
	SourceProjectionRevisionID domain.DerivedRevisionID
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
	SourceProjectionRevisionID    domain.DerivedRevisionID
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

type GoldenPositionKind string

const (
	GoldenPositionDirect         GoldenPositionKind = "direct"
	GoldenPositionNoShowFallback GoldenPositionKind = "no_show_fallback"
)

type GoldenPositionAllocation struct {
	Position      int
	ParticipantID uuid.UUID
	Kind          GoldenPositionKind
}

type GoldenFallbackOrderingInput struct {
	ParticipantID     uuid.UUID
	Points            int
	Buchholz          int
	HeadToHeadPoints  int
	HeadToHeadApplied bool
	EffectiveTime     time.Duration
	AcceptedSolveTime *time.Duration
	Seed              int
}

type GoldenAllocation struct {
	ID                    uuid.UUID
	CommandID             uuid.UUID
	Scope                 GoldenStateScope
	ExpectedState         GoldenStateExpectation
	ResultStateRevisionID uuid.UUID
	AllocatedAt           time.Time
	OrderingInputs        []GoldenFallbackOrderingInput
	Positions             []GoldenPositionAllocation
	PayloadDigest         [sha256.Size]byte
}

type GoldenState struct {
	Scope              GoldenStateScope
	Topology           goldenplan.GroupRevision
	ExactPlan          goldenplan.ExactPlan
	Group              domain.GoldenGroupState
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

func ValidStateScope(scope GoldenStateScope) bool {
	return !goldenAny(
		scope.TournamentID == uuid.Nil,
		scope.GroupID == uuid.Nil,
		scope.GroupRevisionID.IsZero(),
		scope.TournamentID == scope.GroupID,
		scope.TournamentID == scope.GroupRevisionID.UUID(),
		scope.GroupID == scope.GroupRevisionID.UUID(),
	)
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
	clone.Group = CloneGroup(s.Group)
	clone.Membership.PreviousRevisionID = cloneGoldenUUID(s.Membership.PreviousRevisionID)
	clone.PreviousRevisionID = cloneGoldenUUID(s.PreviousRevisionID)
	clone.Windows = make([]GoldenReadyWindow, len(s.Windows))
	for index, window := range s.Windows {
		clone.Windows[index] = CloneReadyWindow(window)
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
		MembershipRevisionsEqual(e.Membership, other.Membership) &&
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
		Group              domain.GoldenGroupState
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

func goldenMembershipDigest(members []domain.GoldenMember) [sha256.Size]byte {
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

func goldenAttempt(attempts []domain.GoldenAttempt, attemptID uuid.UUID) (domain.GoldenAttempt, bool) {
	for _, attempt := range attempts {
		if attempt.ID == attemptID {
			return attempt, true
		}
	}
	return domain.GoldenAttempt{}, false
}

func goldenAttemptContains(attempts []domain.GoldenAttempt, attemptID, participantID uuid.UUID) bool {
	attempt, found := goldenAttempt(attempts, attemptID)
	return found && goldenIDsContain(attempt.ParticipantIDs, participantID)
}

func findMember(members []domain.GoldenMember, participantID uuid.UUID) (domain.GoldenMember, bool) {
	for _, member := range members {
		if member.ParticipantID == participantID {
			return member, true
		}
	}
	return domain.GoldenMember{}, false
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

func goldenReadyEventChangesWindow(eventType GoldenReadyEventType) bool {
	return eventType == GoldenReadyEventAccepted || eventType == GoldenReadyEventDisconnected
}

func CloneGroup(state domain.GoldenGroupState) domain.GoldenGroupState {
	clone := state
	clone.Members = append([]domain.GoldenMember(nil), state.Members...)
	clone.Attempts = make([]domain.GoldenAttempt, len(state.Attempts))
	for index, attempt := range state.Attempts {
		clone.Attempts[index] = CloneAttempt(attempt)
	}
	return clone
}

func CloneAttempt(attempt domain.GoldenAttempt) domain.GoldenAttempt {
	clone := attempt
	clone.PreviousAttemptID = cloneGoldenUUID(attempt.PreviousAttemptID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), attempt.ParticipantIDs...)
	clone.RetainedAt = cloneGoldenTime(attempt.RetainedAt)
	clone.StartedAt = cloneGoldenTime(attempt.StartedAt)
	clone.FinishedAt = cloneGoldenTime(attempt.FinishedAt)
	return clone
}

func CloneReadyWindow(window GoldenReadyWindow) GoldenReadyWindow {
	clone := window
	clone.PreviousRevisionID = cloneGoldenUUID(window.PreviousRevisionID)
	clone.ReadinessPreviousRevisionID = cloneGoldenUUID(window.ReadinessPreviousRevisionID)
	clone.PresencePreviousRevisionID = cloneGoldenUUID(window.PresencePreviousRevisionID)
	clone.ReadyParticipantIDs = append([]uuid.UUID(nil), window.ReadyParticipantIDs...)
	clone.BasePresentParticipantIDs = append([]uuid.UUID(nil), window.BasePresentParticipantIDs...)
	clone.PresentParticipantIDs = append([]uuid.UUID(nil), window.PresentParticipantIDs...)
	return clone
}

func CloneExpectation(input GoldenStateExpectation) GoldenStateExpectation {
	clone := input
	clone.Membership.PreviousRevisionID = cloneGoldenUUID(input.Membership.PreviousRevisionID)
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
