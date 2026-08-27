package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type ArenaWaveState string

const (
	ArenaWaveStatePlanned            ArenaWaveState = "planned"
	ArenaWaveStateReadyWindowOpen    ArenaWaveState = "ready_window_open"
	ArenaWaveStateReady              ArenaWaveState = "ready"
	ArenaWaveStateActive             ArenaWaveState = "active"
	ArenaWaveStatePaused             ArenaWaveState = "paused"
	ArenaWaveStateCompleted          ArenaWaveState = "completed"
	ArenaWaveStateReadyWindowExpired ArenaWaveState = "ready_window_expired"
	ArenaWaveStateSuperseded         ArenaWaveState = "superseded"
)

type ArenaReadyWindowState string

const (
	ArenaReadyWindowStateOpen       ArenaReadyWindowState = "open"
	ArenaReadyWindowStateConsumed   ArenaReadyWindowState = "consumed"
	ArenaReadyWindowStateExpired    ArenaReadyWindowState = "expired"
	ArenaReadyWindowStateSuperseded ArenaReadyWindowState = "superseded"
)

type ArenaWaveRevisionID uuid.UUID
type ArenaReadyWindowRevisionID uuid.UUID

var (
	ErrInvalidArenaWave          = errors.New("invalid arena wave")
	ErrArenaWaveTransition       = errors.New("invalid arena wave transition")
	ErrArenaReadyWindowStale     = errors.New("stale arena ready window")
	ErrArenaReadyWindowDeadline  = errors.New("arena ready window deadline passed")
	ErrArenaWaveAlreadyStarted   = errors.New("arena wave already started")
	ErrArenaWaveMemberNotFound   = errors.New("arena wave member not found")
	ErrArenaWaveReplacementReuse = errors.New("arena wave replacement reuses identity")
)

type ArenaWaveMember struct {
	ParticipantID uuid.UUID
	Ready         bool
}

type ArenaReadyWindow struct {
	ID         uuid.UUID
	WaveID     uuid.UUID
	RevisionID ArenaReadyWindowRevisionID
	State      ArenaReadyWindowState
	OpenedAt   time.Time
	Deadline   time.Time
	ConsumedAt *time.Time
}

type ArenaWave struct {
	ID           uuid.UUID
	TournamentID uuid.UUID
	RevisionID   ArenaWaveRevisionID
	State        ArenaWaveState
	Members      []ArenaWaveMember
	ReadyWindow  *ArenaReadyWindow
	StartedAt    *time.Time
	PausedAt     *time.Time
}

func (s ArenaWaveState) IsValid() bool {
	switch s {
	case ArenaWaveStatePlanned,
		ArenaWaveStateReadyWindowOpen,
		ArenaWaveStateReady,
		ArenaWaveStateActive,
		ArenaWaveStatePaused,
		ArenaWaveStateCompleted,
		ArenaWaveStateReadyWindowExpired,
		ArenaWaveStateSuperseded:
		return true
	}
	return false
}

func (s ArenaReadyWindowState) IsValid() bool {
	switch s {
	case ArenaReadyWindowStateOpen,
		ArenaReadyWindowStateConsumed,
		ArenaReadyWindowStateExpired,
		ArenaReadyWindowStateSuperseded:
		return true
	}
	return false
}

func (id ArenaWaveRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id ArenaWaveRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (id ArenaReadyWindowRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id ArenaReadyWindowRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (w ArenaReadyWindow) Validate() error {
	if w.ID == uuid.Nil || w.WaveID == uuid.Nil || w.RevisionID.IsZero() {
		return fmt.Errorf("%w: missing ready-window identity", ErrInvalidArenaWave)
	}
	if !w.State.IsValid() {
		return fmt.Errorf("%w: unknown ready-window state %q", ErrInvalidArenaWave, w.State)
	}
	if w.OpenedAt.IsZero() || !w.Deadline.After(w.OpenedAt) {
		return fmt.Errorf("%w: invalid ready-window interval", ErrInvalidArenaWave)
	}
	if w.State == ArenaReadyWindowStateConsumed {
		if w.ConsumedAt == nil || w.ConsumedAt.Before(w.OpenedAt) || w.ConsumedAt.After(w.Deadline) {
			return fmt.Errorf("%w: invalid ready-window consumption", ErrInvalidArenaWave)
		}
		return nil
	}
	if w.ConsumedAt != nil {
		return fmt.Errorf("%w: unconsumed ready window has consumption time", ErrInvalidArenaWave)
	}
	return nil
}

func (w ArenaWave) Validate() error {
	if w.ID == uuid.Nil || w.TournamentID == uuid.Nil || w.RevisionID.IsZero() {
		return fmt.Errorf("%w: missing wave identity", ErrInvalidArenaWave)
	}
	if !w.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidArenaWave, w.State)
	}
	if err := w.validateMembers(); err != nil {
		return err
	}
	if w.ReadyWindow != nil {
		if err := w.ReadyWindow.Validate(); err != nil {
			return err
		}
		if w.ReadyWindow.WaveID != w.ID {
			return fmt.Errorf("%w: ready window belongs to another wave", ErrInvalidArenaWave)
		}
	}
	return w.validateStateEvidence()
}

func (w ArenaWave) validateMembers() error {
	if len(w.Members) < 2 {
		return fmt.Errorf("%w: wave requires at least two members", ErrInvalidArenaWave)
	}
	seen := make(map[uuid.UUID]struct{}, len(w.Members))
	for _, member := range w.Members {
		if member.ParticipantID == uuid.Nil {
			return fmt.Errorf("%w: missing member identity", ErrInvalidArenaWave)
		}
		if _, exists := seen[member.ParticipantID]; exists {
			return fmt.Errorf("%w: duplicate member", ErrInvalidArenaWave)
		}
		seen[member.ParticipantID] = struct{}{}
	}
	return nil
}

func (w ArenaWave) validateStateEvidence() error {
	switch w.State {
	case ArenaWaveStatePlanned:
		return w.validatePlannedEvidence()
	case ArenaWaveStateReadyWindowOpen, ArenaWaveStateReady:
		return w.validateOpenWindowEvidence()
	case ArenaWaveStateActive, ArenaWaveStateCompleted:
		return w.validateStartedEvidence()
	case ArenaWaveStatePaused:
		return w.validatePausedEvidence()
	case ArenaWaveStateReadyWindowExpired:
		return w.validateExpiredEvidence()
	case ArenaWaveStateSuperseded:
		return w.validateSupersededEvidence()
	}
	return nil
}

func (w ArenaWave) validatePlannedEvidence() error {
	if w.ReadyWindow != nil || w.StartedAt != nil || w.PausedAt != nil || anyMemberReady(w.Members) {
		return fmt.Errorf("%w: planned wave has execution evidence", ErrInvalidArenaWave)
	}
	return nil
}

func (w ArenaWave) validateOpenWindowEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ArenaReadyWindowStateOpen || w.StartedAt != nil || w.PausedAt != nil {
		return fmt.Errorf("%w: invalid open ready-window evidence", ErrInvalidArenaWave)
	}
	if (w.State == ArenaWaveStateReady) != w.allMembersReady() {
		return fmt.Errorf("%w: ready state does not match member readiness", ErrInvalidArenaWave)
	}
	return nil
}

func (w ArenaWave) validateStartedEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ArenaReadyWindowStateConsumed || w.StartedAt == nil || w.PausedAt != nil || !w.allMembersReady() {
		return fmt.Errorf("%w: invalid started wave evidence", ErrInvalidArenaWave)
	}
	return nil
}

func (w ArenaWave) validatePausedEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ArenaReadyWindowStateConsumed || w.StartedAt == nil || w.PausedAt == nil {
		return fmt.Errorf("%w: invalid paused wave evidence", ErrInvalidArenaWave)
	}
	if w.PausedAt.Before(*w.StartedAt) || !w.allMembersReady() {
		return fmt.Errorf("%w: invalid paused wave evidence", ErrInvalidArenaWave)
	}
	return nil
}

func (w ArenaWave) validateExpiredEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ArenaReadyWindowStateExpired || w.StartedAt != nil || w.PausedAt != nil || anyMemberReady(w.Members) {
		return fmt.Errorf("%w: invalid expired ready-window evidence", ErrInvalidArenaWave)
	}
	return nil
}

func (w ArenaWave) validateSupersededEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ArenaReadyWindowStateSuperseded || w.PausedAt != nil || anyMemberReady(w.Members) {
		return fmt.Errorf("%w: invalid superseded wave evidence", ErrInvalidArenaWave)
	}
	return nil
}

func (w *ArenaWave) OpenReadyWindow(id uuid.UUID, revisionID ArenaReadyWindowRevisionID, openedAt, deadline time.Time) error {
	if w == nil {
		return fmt.Errorf("%w: nil wave", ErrInvalidArenaWave)
	}
	if w.State != ArenaWaveStatePlanned {
		return fmt.Errorf("%w: %s -> %s", ErrArenaWaveTransition, w.State, ArenaWaveStateReadyWindowOpen)
	}
	if err := w.Validate(); err != nil {
		return err
	}
	window := ArenaReadyWindow{ID: id, WaveID: w.ID, RevisionID: revisionID, State: ArenaReadyWindowStateOpen, OpenedAt: openedAt, Deadline: deadline}
	if err := window.Validate(); err != nil {
		return err
	}
	w.ReadyWindow = &window
	w.State = ArenaWaveStateReadyWindowOpen
	return nil
}

func (w *ArenaWave) MarkReady(windowID, participantID uuid.UUID, at time.Time) (bool, error) {
	if w == nil {
		return false, fmt.Errorf("%w: nil wave", ErrInvalidArenaWave)
	}
	if w.State != ArenaWaveStateReadyWindowOpen && w.State != ArenaWaveStateReady {
		return false, fmt.Errorf("%w: wave is not accepting readiness", ErrArenaWaveTransition)
	}
	if w.ReadyWindow == nil || w.ReadyWindow.ID != windowID || w.ReadyWindow.State != ArenaReadyWindowStateOpen {
		return false, ErrArenaReadyWindowStale
	}
	if at.Before(w.ReadyWindow.OpenedAt) || at.After(w.ReadyWindow.Deadline) {
		return false, ErrArenaReadyWindowDeadline
	}
	for i := range w.Members {
		if w.Members[i].ParticipantID != participantID {
			continue
		}
		if w.Members[i].Ready {
			return false, nil
		}
		w.Members[i].Ready = true
		if w.allMembersReady() {
			w.State = ArenaWaveStateReady
		}
		return true, nil
	}
	return false, ErrArenaWaveMemberNotFound
}

func (w *ArenaWave) Start(windowID uuid.UUID, at time.Time) (bool, error) {
	if w == nil {
		return false, fmt.Errorf("%w: nil wave", ErrInvalidArenaWave)
	}
	if w.StartedAt != nil {
		return false, ErrArenaWaveAlreadyStarted
	}
	if w.State != ArenaWaveStateReady || w.ReadyWindow == nil || w.ReadyWindow.State != ArenaReadyWindowStateOpen {
		return false, fmt.Errorf("%w: wave is not ready", ErrArenaWaveTransition)
	}
	if w.ReadyWindow.ID != windowID {
		return false, ErrArenaReadyWindowStale
	}
	if at.Before(w.ReadyWindow.OpenedAt) || at.After(w.ReadyWindow.Deadline) {
		return false, ErrArenaReadyWindowDeadline
	}
	w.StartedAt = &at
	w.ReadyWindow.State = ArenaReadyWindowStateConsumed
	w.ReadyWindow.ConsumedAt = &at
	w.State = ArenaWaveStateActive
	return true, nil
}

func (w *ArenaWave) Pause(at time.Time) error {
	if w == nil {
		return fmt.Errorf("%w: nil wave", ErrInvalidArenaWave)
	}
	if w.State != ArenaWaveStateActive || w.StartedAt == nil || at.Before(*w.StartedAt) {
		return fmt.Errorf("%w: cannot pause wave", ErrArenaWaveTransition)
	}
	w.PausedAt = &at
	w.State = ArenaWaveStatePaused
	return nil
}

func (w *ArenaWave) Resume() error {
	if w == nil {
		return fmt.Errorf("%w: nil wave", ErrInvalidArenaWave)
	}
	if w.State != ArenaWaveStatePaused {
		return fmt.Errorf("%w: cannot resume wave", ErrArenaWaveTransition)
	}
	w.PausedAt = nil
	w.State = ArenaWaveStateActive
	return nil
}

func (w *ArenaWave) ExpireReadyWindow(windowID uuid.UUID, at time.Time) error {
	if w == nil {
		return fmt.Errorf("%w: nil wave", ErrInvalidArenaWave)
	}
	if w.State != ArenaWaveStateReadyWindowOpen || w.ReadyWindow == nil || w.ReadyWindow.State != ArenaReadyWindowStateOpen {
		return fmt.Errorf("%w: ready window cannot expire", ErrArenaWaveTransition)
	}
	if w.ReadyWindow.ID != windowID {
		return ErrArenaReadyWindowStale
	}
	if !at.After(w.ReadyWindow.Deadline) {
		return fmt.Errorf("%w: deadline has not passed", ErrArenaWaveTransition)
	}
	clearMemberReadiness(w.Members)
	w.ReadyWindow.State = ArenaReadyWindowStateExpired
	w.State = ArenaWaveStateReadyWindowExpired
	return nil
}

func (w *ArenaWave) SupersedeWithReplacement(
	replacementID uuid.UUID,
	replacementRevisionID ArenaWaveRevisionID,
	windowID uuid.UUID,
	windowRevisionID ArenaReadyWindowRevisionID,
	openedAt time.Time,
	deadline time.Time,
) (ArenaWave, error) {
	if w == nil {
		return ArenaWave{}, fmt.Errorf("%w: nil wave", ErrInvalidArenaWave)
	}
	if err := w.Validate(); err != nil {
		return ArenaWave{}, err
	}
	if w.State == ArenaWaveStateCompleted || w.State == ArenaWaveStateSuperseded || w.ReadyWindow == nil {
		return ArenaWave{}, fmt.Errorf("%w: wave cannot be superseded", ErrArenaWaveTransition)
	}
	if replacementID == w.ID || replacementRevisionID == w.RevisionID || windowID == w.ReadyWindow.ID || windowRevisionID == w.ReadyWindow.RevisionID {
		return ArenaWave{}, ErrArenaWaveReplacementReuse
	}
	members := make([]ArenaWaveMember, len(w.Members))
	for i, member := range w.Members {
		members[i] = ArenaWaveMember{ParticipantID: member.ParticipantID}
	}
	replacement := ArenaWave{
		ID:           replacementID,
		TournamentID: w.TournamentID,
		RevisionID:   replacementRevisionID,
		State:        ArenaWaveStatePlanned,
		Members:      members,
	}
	if err := replacement.OpenReadyWindow(windowID, windowRevisionID, openedAt, deadline); err != nil {
		return ArenaWave{}, err
	}
	if err := replacement.Validate(); err != nil {
		return ArenaWave{}, err
	}
	clearMemberReadiness(w.Members)
	w.ReadyWindow.State = ArenaReadyWindowStateSuperseded
	w.ReadyWindow.ConsumedAt = nil
	w.PausedAt = nil
	w.State = ArenaWaveStateSuperseded
	return replacement, nil
}

func (w ArenaWave) allMembersReady() bool {
	return len(w.Members) > 0 && !hasUnreadyMember(w.Members)
}

func hasUnreadyMember(members []ArenaWaveMember) bool {
	for _, member := range members {
		if !member.Ready {
			return true
		}
	}
	return false
}

func anyMemberReady(members []ArenaWaveMember) bool {
	for _, member := range members {
		if member.Ready {
			return true
		}
	}
	return false
}

func clearMemberReadiness(members []ArenaWaveMember) {
	for i := range members {
		members[i].Ready = false
	}
}
