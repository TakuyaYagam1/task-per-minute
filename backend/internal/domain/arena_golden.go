package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ArenaGoldenAttemptState string

const (
	ArenaGoldenAttemptStatePlanned      ArenaGoldenAttemptState = "planned"
	ArenaGoldenAttemptStateWaitingReady ArenaGoldenAttemptState = "waiting_ready"
	ArenaGoldenAttemptStateActive       ArenaGoldenAttemptState = "active"
	ArenaGoldenAttemptStateCompleted    ArenaGoldenAttemptState = "completed"
	ArenaGoldenAttemptStateVoid         ArenaGoldenAttemptState = "void"
	ArenaGoldenAttemptStateCancelled    ArenaGoldenAttemptState = "cancelled"
)

var (
	ErrInvalidArenaGoldenGroup          = errors.New("invalid arena Golden group")
	ErrInvalidArenaGoldenAttempt        = errors.New("invalid arena Golden attempt")
	ErrArenaGoldenParticipantNotFound   = errors.New("arena Golden participant not found")
	ErrArenaGoldenAttemptNotFound       = errors.New("arena Golden attempt not found")
	ErrArenaGoldenAttemptAlreadyStarted = errors.New("arena Golden attempt already started")
)

type ArenaGoldenMember struct {
	ParticipantID uuid.UUID
	Excluded      bool
}

type ArenaGoldenAttempt struct {
	ID                uuid.UUID
	GroupID           uuid.UUID
	GroupRevisionID   ArenaDerivedRevisionID
	AttemptNo         int
	PreviousAttemptID *uuid.UUID
	State             ArenaGoldenAttemptState
	ParticipantIDs    []uuid.UUID
	RetainedAt        *time.Time
	StartedAt         *time.Time
	FinishedAt        *time.Time
}

type ArenaGoldenGroupState struct {
	ID                         uuid.UUID
	TournamentID               uuid.UUID
	RevisionID                 ArenaDerivedRevisionID
	SourceProjectionRevisionID ArenaDerivedRevisionID
	PositionFrom               int
	PositionTo                 int
	ParticipationEstablished   bool
	Members                    []ArenaGoldenMember
	Attempts                   []ArenaGoldenAttempt
}

type ArenaGoldenGroup struct {
	state ArenaGoldenGroupState
}

func (s ArenaGoldenAttemptState) IsValid() bool {
	switch s {
	case ArenaGoldenAttemptStatePlanned,
		ArenaGoldenAttemptStateWaitingReady,
		ArenaGoldenAttemptStateActive,
		ArenaGoldenAttemptStateCompleted,
		ArenaGoldenAttemptStateVoid,
		ArenaGoldenAttemptStateCancelled:
		return true
	}
	return false
}

func (s ArenaGoldenAttemptState) IsTerminal() bool {
	return s == ArenaGoldenAttemptStateCompleted || s == ArenaGoldenAttemptStateVoid || s == ArenaGoldenAttemptStateCancelled
}

func (a ArenaGoldenAttempt) Validate() error {
	if a.ID == uuid.Nil || a.GroupID == uuid.Nil || a.GroupRevisionID.IsZero() || a.AttemptNo < 1 {
		return fmt.Errorf("%w: missing attempt identity", ErrInvalidArenaGoldenAttempt)
	}
	if !a.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidArenaGoldenAttempt, a.State)
	}
	if err := validateArenaGoldenAttemptParticipants(a.ParticipantIDs); err != nil {
		return err
	}
	if a.AttemptNo == 1 && a.PreviousAttemptID != nil {
		return fmt.Errorf("%w: first attempt has a predecessor", ErrInvalidArenaGoldenAttempt)
	}
	if a.AttemptNo > 1 && (a.PreviousAttemptID == nil || *a.PreviousAttemptID == uuid.Nil || *a.PreviousAttemptID == a.ID) {
		return fmt.Errorf("%w: later attempt has invalid predecessor", ErrInvalidArenaGoldenAttempt)
	}
	if err := a.validateStateEvidence(); err != nil {
		return err
	}
	return a.validateRetainedEvidence()
}

func (a ArenaGoldenAttempt) validateStateEvidence() error {
	switch a.State {
	case ArenaGoldenAttemptStatePlanned, ArenaGoldenAttemptStateWaitingReady:
		if a.StartedAt != nil || a.FinishedAt != nil {
			return fmt.Errorf("%w: unstarted attempt has execution timestamps", ErrInvalidArenaGoldenAttempt)
		}
	case ArenaGoldenAttemptStateActive:
		if !validArenaGoldenTime(a.StartedAt) || a.FinishedAt != nil {
			return fmt.Errorf("%w: active attempt has invalid timestamps", ErrInvalidArenaGoldenAttempt)
		}
	case ArenaGoldenAttemptStateCompleted, ArenaGoldenAttemptStateVoid:
		if !validArenaGoldenInterval(a.StartedAt, a.FinishedAt) {
			return fmt.Errorf("%w: terminal attempt has invalid timestamps", ErrInvalidArenaGoldenAttempt)
		}
	case ArenaGoldenAttemptStateCancelled:
		if !validArenaGoldenTime(a.FinishedAt) {
			return fmt.Errorf("%w: cancelled attempt requires terminal timestamp", ErrInvalidArenaGoldenAttempt)
		}
		if a.StartedAt != nil && !validArenaGoldenInterval(a.StartedAt, a.FinishedAt) {
			return fmt.Errorf("%w: cancelled attempt has invalid timestamps", ErrInvalidArenaGoldenAttempt)
		}
	}
	return nil
}

func (a ArenaGoldenAttempt) validateRetainedEvidence() error {
	if a.RetainedAt == nil {
		return nil
	}
	if !validArenaGoldenTime(a.RetainedAt) {
		return fmt.Errorf("%w: invalid retained timestamp", ErrInvalidArenaGoldenAttempt)
	}
	if a.StartedAt != nil && a.RetainedAt.After(*a.StartedAt) {
		return fmt.Errorf("%w: attempt was retained after start", ErrInvalidArenaGoldenAttempt)
	}
	if a.FinishedAt != nil && a.RetainedAt.After(*a.FinishedAt) {
		return fmt.Errorf("%w: attempt was retained after finish", ErrInvalidArenaGoldenAttempt)
	}
	return nil
}

func NewArenaGoldenGroup(state ArenaGoldenGroupState) (ArenaGoldenGroup, error) {
	group := ArenaGoldenGroup{state: cloneArenaGoldenGroupState(state)}
	if err := group.Validate(); err != nil {
		return ArenaGoldenGroup{}, err
	}
	return group, nil
}

func (g ArenaGoldenGroup) Validate() error {
	state := g.state
	if state.ID == uuid.Nil || state.TournamentID == uuid.Nil || state.RevisionID.IsZero() || state.SourceProjectionRevisionID.IsZero() {
		return fmt.Errorf("%w: missing group or revision identity", ErrInvalidArenaGoldenGroup)
	}
	if state.RevisionID == state.SourceProjectionRevisionID {
		return fmt.Errorf("%w: group revision reuses its source identity", ErrInvalidArenaGoldenGroup)
	}
	if state.PositionFrom < 1 || state.PositionTo < state.PositionFrom || state.PositionTo-state.PositionFrom+1 != len(state.Members) {
		return fmt.Errorf("%w: affected position range does not match members", ErrInvalidArenaGoldenGroup)
	}
	memberIDs, err := validateArenaGoldenMembers(state.Members)
	if err != nil {
		return err
	}
	if !state.ParticipationEstablished && arenaGoldenHasExecutionEvidence(state.Attempts) {
		return fmt.Errorf("%w: execution exists before participation was established", ErrInvalidArenaGoldenGroup)
	}
	return validateArenaGoldenAttempts(state, memberIDs)
}

func (g ArenaGoldenGroup) Snapshot() ArenaGoldenGroupState {
	return cloneArenaGoldenGroupState(g.state)
}

func (g ArenaGoldenGroup) ActiveParticipantIDs() []uuid.UUID {
	active := make([]uuid.UUID, 0, len(g.state.Members))
	for _, member := range g.state.Members {
		if !member.Excluded {
			active = append(active, member.ParticipantID)
		}
	}
	return active
}

func (g *ArenaGoldenGroup) EstablishParticipation() (bool, error) {
	if g == nil {
		return false, fmt.Errorf("%w: nil group", ErrInvalidArenaGoldenGroup)
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

func (g *ArenaGoldenGroup) ExcludeParticipant(participantID uuid.UUID) (bool, error) {
	if g == nil {
		return false, fmt.Errorf("%w: nil group", ErrInvalidArenaGoldenGroup)
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
	return false, ErrArenaGoldenParticipantNotFound
}

func (g *ArenaGoldenGroup) RetainAttemptBeforeStart(attemptID uuid.UUID, retainedAt time.Time) (bool, error) {
	if g == nil {
		return false, fmt.Errorf("%w: nil group", ErrInvalidArenaGoldenGroup)
	}
	if err := g.Validate(); err != nil {
		return false, err
	}
	for i := range g.state.Attempts {
		attempt := &g.state.Attempts[i]
		if attempt.ID != attemptID {
			continue
		}
		if attempt.StartedAt != nil || (attempt.State != ArenaGoldenAttemptStatePlanned && attempt.State != ArenaGoldenAttemptStateWaitingReady) {
			return false, ErrArenaGoldenAttemptAlreadyStarted
		}
		if attempt.RetainedAt != nil {
			return false, nil
		}
		if retainedAt.IsZero() || retainedAt.Location() != time.UTC {
			return false, fmt.Errorf("%w: retained timestamp must be UTC", ErrInvalidArenaGoldenAttempt)
		}
		attempt.RetainedAt = cloneTimePointer(&retainedAt)
		return true, nil
	}
	return false, ErrArenaGoldenAttemptNotFound
}

func validateArenaGoldenMembers(members []ArenaGoldenMember) (map[uuid.UUID]struct{}, error) {
	if len(members) < 2 {
		return nil, fmt.Errorf("%w: group requires at least two members", ErrInvalidArenaGoldenGroup)
	}
	ids := make(map[uuid.UUID]struct{}, len(members))
	for _, member := range members {
		if member.ParticipantID == uuid.Nil {
			return nil, fmt.Errorf("%w: missing participant identity", ErrInvalidArenaGoldenGroup)
		}
		if _, exists := ids[member.ParticipantID]; exists {
			return nil, fmt.Errorf("%w: duplicate participant", ErrInvalidArenaGoldenGroup)
		}
		ids[member.ParticipantID] = struct{}{}
	}
	return ids, nil
}

func validateArenaGoldenAttemptParticipants(participantIDs []uuid.UUID) error {
	if len(participantIDs) < 2 {
		return fmt.Errorf("%w: attempt requires at least two participants", ErrInvalidArenaGoldenAttempt)
	}
	seen := make(map[uuid.UUID]struct{}, len(participantIDs))
	for _, participantID := range participantIDs {
		if participantID == uuid.Nil {
			return fmt.Errorf("%w: missing participant identity", ErrInvalidArenaGoldenAttempt)
		}
		if _, exists := seen[participantID]; exists {
			return fmt.Errorf("%w: duplicate participant", ErrInvalidArenaGoldenAttempt)
		}
		seen[participantID] = struct{}{}
	}
	return nil
}

func validateArenaGoldenAttempts(state ArenaGoldenGroupState, memberIDs map[uuid.UUID]struct{}) error {
	seenAttemptIDs := make(map[uuid.UUID]struct{}, len(state.Attempts))
	var previous ArenaGoldenAttempt
	for i, attempt := range state.Attempts {
		if err := attempt.Validate(); err != nil {
			return err
		}
		if attempt.GroupID != state.ID || attempt.GroupRevisionID != state.RevisionID || attempt.AttemptNo != i+1 {
			return fmt.Errorf("%w: attempt belongs to another group revision or position", ErrInvalidArenaGoldenAttempt)
		}
		if _, exists := seenAttemptIDs[attempt.ID]; exists {
			return fmt.Errorf("%w: duplicate attempt identity", ErrInvalidArenaGoldenAttempt)
		}
		for _, participantID := range attempt.ParticipantIDs {
			if _, exists := memberIDs[participantID]; !exists {
				return fmt.Errorf("%w: attempt includes a foreign participant", ErrInvalidArenaGoldenAttempt)
			}
		}
		if i > 0 {
			if attempt.PreviousAttemptID == nil || *attempt.PreviousAttemptID != previous.ID || !previous.State.IsTerminal() {
				return fmt.Errorf("%w: broken predecessor chain", ErrInvalidArenaGoldenAttempt)
			}
			if !arenaGoldenParticipantsAreSubset(attempt.ParticipantIDs, previous.ParticipantIDs) {
				return fmt.Errorf("%w: later attempt adds a participant", ErrInvalidArenaGoldenAttempt)
			}
		}
		seenAttemptIDs[attempt.ID] = struct{}{}
		previous = attempt
	}
	return nil
}

func arenaGoldenParticipantsAreSubset(current, previous []uuid.UUID) bool {
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

func arenaGoldenHasExecutionEvidence(attempts []ArenaGoldenAttempt) bool {
	for _, attempt := range attempts {
		if attempt.StartedAt != nil || attempt.State == ArenaGoldenAttemptStateActive || attempt.State == ArenaGoldenAttemptStateCompleted || attempt.State == ArenaGoldenAttemptStateVoid {
			return true
		}
	}
	return false
}

func validArenaGoldenTime(value *time.Time) bool {
	return value != nil && !value.IsZero() && value.Location() == time.UTC
}

func validArenaGoldenInterval(startedAt, finishedAt *time.Time) bool {
	return validArenaGoldenTime(startedAt) && validArenaGoldenTime(finishedAt) && !finishedAt.Before(*startedAt)
}

func cloneArenaGoldenGroupState(state ArenaGoldenGroupState) ArenaGoldenGroupState {
	clone := state
	clone.Members = append([]ArenaGoldenMember(nil), state.Members...)
	clone.Attempts = make([]ArenaGoldenAttempt, len(state.Attempts))
	for i := range state.Attempts {
		clone.Attempts[i] = cloneArenaGoldenAttempt(state.Attempts[i])
	}
	return clone
}

func cloneArenaGoldenAttempt(attempt ArenaGoldenAttempt) ArenaGoldenAttempt {
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
