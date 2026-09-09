package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type GoldenAttemptState string

const (
	GoldenAttemptStatePlanned      GoldenAttemptState = "planned"
	GoldenAttemptStateWaitingReady GoldenAttemptState = "waiting_ready"
	GoldenAttemptStateActive       GoldenAttemptState = "active"
	GoldenAttemptStateCompleted    GoldenAttemptState = "completed"
	GoldenAttemptStateVoid         GoldenAttemptState = "void"
	GoldenAttemptStateCancelled    GoldenAttemptState = "cancelled"
)

var (
	ErrInvalidGoldenGroup          = errors.New("invalid golden group")
	ErrInvalidGoldenAttempt        = errors.New("invalid golden attempt")
	ErrGoldenParticipantNotFound   = errors.New("golden participant not found")
	ErrGoldenAttemptNotFound       = errors.New("golden attempt not found")
	ErrGoldenAttemptAlreadyStarted = errors.New("golden attempt already started")
)

type GoldenMember struct {
	ParticipantID uuid.UUID
	Excluded      bool
}

type GoldenAttempt struct {
	ID                uuid.UUID
	GroupID           uuid.UUID
	GroupRevisionID   DerivedRevisionID
	AttemptNo         int
	PreviousAttemptID *uuid.UUID
	State             GoldenAttemptState
	ParticipantIDs    []uuid.UUID
	RetainedAt        *time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
}

type GoldenGroupState struct {
	ID                         uuid.UUID
	TournamentID               uuid.UUID
	RevisionID                 DerivedRevisionID
	SourceProjectionRevisionID DerivedRevisionID
	PositionFrom               int
	PositionTo                 int
	ParticipationEstablished   bool
	Members                    []GoldenMember
	Attempts                   []GoldenAttempt
}

type GoldenGroup struct {
	state GoldenGroupState
}

func (s GoldenAttemptState) IsValid() bool {
	switch s {
	case GoldenAttemptStatePlanned,
		GoldenAttemptStateWaitingReady,
		GoldenAttemptStateActive,
		GoldenAttemptStateCompleted,
		GoldenAttemptStateVoid,
		GoldenAttemptStateCancelled:
		return true
	}
	return false
}

func (s GoldenAttemptState) IsTerminal() bool {
	return s == GoldenAttemptStateCompleted || s == GoldenAttemptStateVoid || s == GoldenAttemptStateCancelled
}

func (a GoldenAttempt) Validate() error {
	if a.ID == uuid.Nil || a.GroupID == uuid.Nil || a.GroupRevisionID.IsZero() || a.AttemptNo < 1 {
		return fmt.Errorf("%w: missing attempt identity", ErrInvalidGoldenAttempt)
	}
	if !a.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidGoldenAttempt, a.State)
	}
	if err := validateGoldenAttemptParticipants(a.ParticipantIDs); err != nil {
		return err
	}
	if a.AttemptNo == 1 && a.PreviousAttemptID != nil {
		return fmt.Errorf("%w: first attempt has a predecessor", ErrInvalidGoldenAttempt)
	}
	if a.AttemptNo > 1 && (a.PreviousAttemptID == nil || *a.PreviousAttemptID == uuid.Nil || *a.PreviousAttemptID == a.ID) {
		return fmt.Errorf("%w: later attempt has invalid predecessor", ErrInvalidGoldenAttempt)
	}
	if err := a.validateStateEvidence(); err != nil {
		return err
	}
	return a.validateRetainedEvidence()
}

func (a GoldenAttempt) validateStateEvidence() error {
	switch a.State {
	case GoldenAttemptStatePlanned, GoldenAttemptStateWaitingReady:
		if a.StartedAt != nil || a.FinishedAt != nil {
			return fmt.Errorf("%w: unstarted attempt has execution timestamps", ErrInvalidGoldenAttempt)
		}
	case GoldenAttemptStateActive:
		if !validGoldenTime(a.StartedAt) || a.FinishedAt != nil {
			return fmt.Errorf("%w: active attempt has invalid timestamps", ErrInvalidGoldenAttempt)
		}
	case GoldenAttemptStateCompleted, GoldenAttemptStateVoid:
		if !validGoldenInterval(a.StartedAt, a.FinishedAt) {
			return fmt.Errorf("%w: terminal attempt has invalid timestamps", ErrInvalidGoldenAttempt)
		}
	case GoldenAttemptStateCancelled:
		if !validGoldenTime(a.FinishedAt) {
			return fmt.Errorf("%w: cancelled attempt requires terminal timestamp", ErrInvalidGoldenAttempt)
		}
		if a.StartedAt != nil && !validGoldenInterval(a.StartedAt, a.FinishedAt) {
			return fmt.Errorf("%w: cancelled attempt has invalid timestamps", ErrInvalidGoldenAttempt)
		}
	}
	return nil
}

func (a GoldenAttempt) validateRetainedEvidence() error {
	if a.RetainedAt == nil {
		return nil
	}
	if !validGoldenTime(a.RetainedAt) {
		return fmt.Errorf("%w: invalid retained timestamp", ErrInvalidGoldenAttempt)
	}
	if a.StartedAt != nil && a.RetainedAt.After(*a.StartedAt) {
		return fmt.Errorf("%w: attempt was retained after start", ErrInvalidGoldenAttempt)
	}
	if a.FinishedAt != nil && a.RetainedAt.After(*a.FinishedAt) {
		return fmt.Errorf("%w: attempt was retained after finish", ErrInvalidGoldenAttempt)
	}
	return nil
}

func NewGoldenGroup(state GoldenGroupState) (GoldenGroup, error) {
	group := GoldenGroup{state: cloneGoldenGroupState(state)}
	if err := group.Validate(); err != nil {
		return GoldenGroup{}, err
	}
	return group, nil
}

func (g GoldenGroup) Validate() error {
	state := g.state
	if state.ID == uuid.Nil || state.TournamentID == uuid.Nil || state.RevisionID.IsZero() || state.SourceProjectionRevisionID.IsZero() {
		return fmt.Errorf("%w: missing group or revision identity", ErrInvalidGoldenGroup)
	}
	if state.RevisionID == state.SourceProjectionRevisionID {
		return fmt.Errorf("%w: group revision reuses its source identity", ErrInvalidGoldenGroup)
	}
	if state.PositionFrom < 1 || state.PositionTo < state.PositionFrom || state.PositionTo-state.PositionFrom+1 != len(state.Members) {
		return fmt.Errorf("%w: affected position range does not match members", ErrInvalidGoldenGroup)
	}
	memberIDs, err := validateGoldenMembers(state.Members)
	if err != nil {
		return err
	}
	if !state.ParticipationEstablished && goldenHasExecutionEvidence(state.Attempts) {
		return fmt.Errorf("%w: execution exists before participation was established", ErrInvalidGoldenGroup)
	}
	return validateGoldenAttempts(state, memberIDs)
}

func (g GoldenGroup) Snapshot() GoldenGroupState {
	return cloneGoldenGroupState(g.state)
}

func (g GoldenGroup) ActiveParticipantIDs() []uuid.UUID {
	active := make([]uuid.UUID, 0, len(g.state.Members))
	for _, member := range g.state.Members {
		if !member.Excluded {
			active = append(active, member.ParticipantID)
		}
	}
	return active
}

func (g *GoldenGroup) EstablishParticipation() (bool, error) {
	if g == nil {
		return false, fmt.Errorf("%w: nil group", ErrInvalidGoldenGroup)
	}
	if err := g.Validate(); err != nil {
		return false, err
	}
	if g.state.ParticipationEstablished {
		return false, nil
	}
	g.state.ParticipationEstablished = true
	return true, nil
}

func (g *GoldenGroup) ExcludeParticipant(participantID uuid.UUID) (bool, error) {
	if g == nil {
		return false, fmt.Errorf("%w: nil group", ErrInvalidGoldenGroup)
	}
	if err := g.Validate(); err != nil {
		return false, err
	}
	for i := range g.state.Members {
		if g.state.Members[i].ParticipantID != participantID {
			continue
		}
		if g.state.Members[i].Excluded {
			return false, nil
		}
		g.state.Members[i].Excluded = true
		return true, nil
	}
	return false, ErrGoldenParticipantNotFound
}

func (g *GoldenGroup) RetainAttemptBeforeStart(attemptID uuid.UUID, retainedAt time.Time) (bool, error) {
	if g == nil {
		return false, fmt.Errorf("%w: nil group", ErrInvalidGoldenGroup)
	}
	if err := g.Validate(); err != nil {
		return false, err
	}
	for i := range g.state.Attempts {
		attempt := &g.state.Attempts[i]
		if attempt.ID != attemptID {
			continue
		}
		if attempt.StartedAt != nil || (attempt.State != GoldenAttemptStatePlanned && attempt.State != GoldenAttemptStateWaitingReady) {
			return false, ErrGoldenAttemptAlreadyStarted
		}
		if attempt.RetainedAt != nil {
			return false, nil
		}
		if retainedAt.IsZero() || retainedAt.Location() != time.UTC {
			return false, fmt.Errorf("%w: retained timestamp must be UTC", ErrInvalidGoldenAttempt)
		}
		attempt.RetainedAt = cloneTimePointer(&retainedAt)
		return true, nil
	}
	return false, ErrGoldenAttemptNotFound
}

func validateGoldenMembers(members []GoldenMember) (map[uuid.UUID]struct{}, error) {
	if len(members) < 2 {
		return nil, fmt.Errorf("%w: group requires at least two members", ErrInvalidGoldenGroup)
	}
	ids := make(map[uuid.UUID]struct{}, len(members))
	for _, member := range members {
		if member.ParticipantID == uuid.Nil {
			return nil, fmt.Errorf("%w: missing participant identity", ErrInvalidGoldenGroup)
		}
		if _, exists := ids[member.ParticipantID]; exists {
			return nil, fmt.Errorf("%w: duplicate participant", ErrInvalidGoldenGroup)
		}
		ids[member.ParticipantID] = struct{}{}
	}
	return ids, nil
}

func validateGoldenAttemptParticipants(participantIDs []uuid.UUID) error {
	if len(participantIDs) < 2 {
		return fmt.Errorf("%w: attempt requires at least two participants", ErrInvalidGoldenAttempt)
	}
	seen := make(map[uuid.UUID]struct{}, len(participantIDs))
	for _, participantID := range participantIDs {
		if participantID == uuid.Nil {
			return fmt.Errorf("%w: missing participant identity", ErrInvalidGoldenAttempt)
		}
		if _, exists := seen[participantID]; exists {
			return fmt.Errorf("%w: duplicate participant", ErrInvalidGoldenAttempt)
		}
		seen[participantID] = struct{}{}
	}
	return nil
}

func validateGoldenAttempts(state GoldenGroupState, memberIDs map[uuid.UUID]struct{}) error {
	seenAttemptIDs := make(map[uuid.UUID]struct{}, len(state.Attempts))
	var previous GoldenAttempt
	for i, attempt := range state.Attempts {
		if err := attempt.Validate(); err != nil {
			return err
		}
		if attempt.GroupID != state.ID || attempt.GroupRevisionID != state.RevisionID || attempt.AttemptNo != i+1 {
			return fmt.Errorf("%w: attempt belongs to another group revision or position", ErrInvalidGoldenAttempt)
		}
		if _, exists := seenAttemptIDs[attempt.ID]; exists {
			return fmt.Errorf("%w: duplicate attempt identity", ErrInvalidGoldenAttempt)
		}
		for _, participantID := range attempt.ParticipantIDs {
			if _, exists := memberIDs[participantID]; !exists {
				return fmt.Errorf("%w: attempt includes a foreign participant", ErrInvalidGoldenAttempt)
			}
		}
		if i > 0 {
			if attempt.PreviousAttemptID == nil || *attempt.PreviousAttemptID != previous.ID || !previous.State.IsTerminal() {
				return fmt.Errorf("%w: broken predecessor chain", ErrInvalidGoldenAttempt)
			}
			if !goldenParticipantsAreSubset(attempt.ParticipantIDs, previous.ParticipantIDs) {
				return fmt.Errorf("%w: later attempt adds a participant", ErrInvalidGoldenAttempt)
			}
		}
		seenAttemptIDs[attempt.ID] = struct{}{}
		previous = attempt
	}
	return nil
}

func goldenParticipantsAreSubset(current, previous []uuid.UUID) bool {
	previousIDs := make(map[uuid.UUID]struct{}, len(previous))
	for _, participantID := range previous {
		previousIDs[participantID] = struct{}{}
	}
	for _, participantID := range current {
		if _, exists := previousIDs[participantID]; !exists {
			return false
		}
	}
	return true
}

func goldenHasExecutionEvidence(attempts []GoldenAttempt) bool {
	for _, attempt := range attempts {
		if attempt.StartedAt != nil || attempt.State == GoldenAttemptStateActive || attempt.State == GoldenAttemptStateCompleted || attempt.State == GoldenAttemptStateVoid {
			return true
		}
	}
	return false
}

func validGoldenTime(value *time.Time) bool {
	return value != nil && !value.IsZero() && value.Location() == time.UTC
}

func validGoldenInterval(startedAt, finishedAt *time.Time) bool {
	return validGoldenTime(startedAt) && validGoldenTime(finishedAt) && !finishedAt.Before(*startedAt)
}

func cloneGoldenGroupState(state GoldenGroupState) GoldenGroupState {
	clone := state
	clone.Members = append([]GoldenMember(nil), state.Members...)
	clone.Attempts = make([]GoldenAttempt, len(state.Attempts))
	for i := range state.Attempts {
		clone.Attempts[i] = cloneGoldenAttempt(state.Attempts[i])
	}
	return clone
}

func cloneGoldenAttempt(attempt GoldenAttempt) GoldenAttempt {
	clone := attempt
	clone.PreviousAttemptID = cloneUUIDPointer(attempt.PreviousAttemptID)
	clone.ParticipantIDs = append([]uuid.UUID(nil), attempt.ParticipantIDs...)
	clone.RetainedAt = cloneTimePointer(attempt.RetainedAt)
	clone.StartedAt = cloneTimePointer(attempt.StartedAt)
	clone.FinishedAt = cloneTimePointer(attempt.FinishedAt)
	return clone
}

func cloneUUIDPointer(value *uuid.UUID) *uuid.UUID {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
