package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

type WaveState string

const (
	WaveStatePlanned            WaveState = "planned"
	WaveStateReadyWindowOpen    WaveState = "ready_window_open"
	WaveStateReady              WaveState = "ready"
	WaveStateActive             WaveState = "active"
	WaveStatePaused             WaveState = "paused"
	WaveStateCompleted          WaveState = "completed"
	WaveStateReadyWindowExpired WaveState = "ready_window_expired"
	WaveStateSuperseded         WaveState = "superseded"
)

type ReadyWindowState string

const (
	ReadyWindowStateOpen       ReadyWindowState = "open"
	ReadyWindowStateConsumed   ReadyWindowState = "consumed"
	ReadyWindowStateExpired    ReadyWindowState = "expired"
	ReadyWindowStateSuperseded ReadyWindowState = "superseded"
)

type WaveRevisionID uuid.UUID
type ReadyWindowRevisionID uuid.UUID

var (
	ErrInvalidWave          = errors.New("invalid execution wave")
	ErrWaveTransition       = errors.New("invalid execution wave transition")
	ErrReadyWindowStale     = errors.New("stale ready window")
	ErrReadyWindowDeadline  = errors.New("ready window deadline passed")
	ErrWaveAlreadyStarted   = errors.New("execution wave already started")
	ErrWaveMemberNotFound   = errors.New("execution wave member not found")
	ErrWaveReplacementReuse = errors.New("execution wave replacement reuses identity")
)

type WaveMember struct {
	ParticipantID uuid.UUID
	Ready         bool
}

type ReadyWindow struct {
	ID         uuid.UUID
	WaveID     uuid.UUID
	RevisionID ReadyWindowRevisionID
	State      ReadyWindowState
	OpenedAt   time.Time
	Deadline   time.Time
	ConsumedAt *time.Time
}

type Wave struct {
	ID           uuid.UUID
	TournamentID uuid.UUID
	RevisionID   WaveRevisionID
	State        WaveState
	Members      []WaveMember
	ReadyWindow  *ReadyWindow
	StartedAt    *time.Time
	PausedAt     *time.Time
}

func (s WaveState) IsValid() bool {
	switch s {
	case WaveStatePlanned,
		WaveStateReadyWindowOpen,
		WaveStateReady,
		WaveStateActive,
		WaveStatePaused,
		WaveStateCompleted,
		WaveStateReadyWindowExpired,
		WaveStateSuperseded:
		return true
	}
	return false
}

func (s ReadyWindowState) IsValid() bool {
	switch s {
	case ReadyWindowStateOpen,
		ReadyWindowStateConsumed,
		ReadyWindowStateExpired,
		ReadyWindowStateSuperseded:
		return true
	}
	return false
}

func (id WaveRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id WaveRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (id ReadyWindowRevisionID) IsZero() bool {
	return uuid.UUID(id) == uuid.Nil
}

func (id ReadyWindowRevisionID) UUID() uuid.UUID {
	return uuid.UUID(id)
}

func (w ReadyWindow) Validate() error {
	if w.ID == uuid.Nil || w.WaveID == uuid.Nil || w.RevisionID.IsZero() {
		return fmt.Errorf("%w: missing ready-window identity", ErrInvalidWave)
	}
	if !w.State.IsValid() {
		return fmt.Errorf("%w: unknown ready-window state %q", ErrInvalidWave, w.State)
	}
	if w.OpenedAt.IsZero() || !w.Deadline.After(w.OpenedAt) {
		return fmt.Errorf("%w: invalid ready-window interval", ErrInvalidWave)
	}
	if w.State == ReadyWindowStateConsumed {
		if w.ConsumedAt == nil || w.ConsumedAt.Before(w.OpenedAt) || w.ConsumedAt.After(w.Deadline) {
			return fmt.Errorf("%w: invalid ready-window consumption", ErrInvalidWave)
		}
		return nil
	}
	if w.ConsumedAt != nil {
		return fmt.Errorf("%w: unconsumed ready window has consumption time", ErrInvalidWave)
	}
	return nil
}

func (w Wave) Validate() error {
	if w.ID == uuid.Nil || w.TournamentID == uuid.Nil || w.RevisionID.IsZero() {
		return fmt.Errorf("%w: missing wave identity", ErrInvalidWave)
	}
	if !w.State.IsValid() {
		return fmt.Errorf("%w: unknown state %q", ErrInvalidWave, w.State)
	}
	if err := w.validateMembers(); err != nil {
		return err
	}
	if w.ReadyWindow != nil {
		if err := w.ReadyWindow.Validate(); err != nil {
			return err
		}
		if w.ReadyWindow.WaveID != w.ID {
			return fmt.Errorf("%w: ready window belongs to another wave", ErrInvalidWave)
		}
	}
	return w.validateStateEvidence()
}

func (w Wave) validateMembers() error {
	if len(w.Members) < 2 {
		return fmt.Errorf("%w: wave requires at least two members", ErrInvalidWave)
	}
	seen := make(map[uuid.UUID]struct{}, len(w.Members))
	for _, member := range w.Members {
		if member.ParticipantID == uuid.Nil {
			return fmt.Errorf("%w: missing member identity", ErrInvalidWave)
		}
		if _, exists := seen[member.ParticipantID]; exists {
			return fmt.Errorf("%w: duplicate member", ErrInvalidWave)
		}
		seen[member.ParticipantID] = struct{}{}
	}
	return nil
}

func (w Wave) validateStateEvidence() error {
	switch w.State {
	case WaveStatePlanned:
		return w.validatePlannedEvidence()
	case WaveStateReadyWindowOpen, WaveStateReady:
		return w.validateOpenWindowEvidence()
	case WaveStateActive, WaveStateCompleted:
		return w.validateStartedEvidence()
	case WaveStatePaused:
		return w.validatePausedEvidence()
	case WaveStateReadyWindowExpired:
		return w.validateExpiredEvidence()
	case WaveStateSuperseded:
		return w.validateSupersededEvidence()
	}
	return nil
}

func (w Wave) validatePlannedEvidence() error {
	if w.ReadyWindow != nil || w.StartedAt != nil || w.PausedAt != nil || anyMemberReady(w.Members) {
		return fmt.Errorf("%w: planned wave has execution evidence", ErrInvalidWave)
	}
	return nil
}

func (w Wave) validateOpenWindowEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ReadyWindowStateOpen || w.StartedAt != nil || w.PausedAt != nil {
		return fmt.Errorf("%w: invalid open ready-window evidence", ErrInvalidWave)
	}
	if (w.State == WaveStateReady) != w.allMembersReady() {
		return fmt.Errorf("%w: ready state does not match member readiness", ErrInvalidWave)
	}
	return nil
}

func (w Wave) validateStartedEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ReadyWindowStateConsumed || w.StartedAt == nil || w.PausedAt != nil || !w.allMembersReady() {
		return fmt.Errorf("%w: invalid started wave evidence", ErrInvalidWave)
	}
	return nil
}

func (w Wave) validatePausedEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ReadyWindowStateConsumed || w.StartedAt == nil || w.PausedAt == nil {
		return fmt.Errorf("%w: invalid paused wave evidence", ErrInvalidWave)
	}
	if w.PausedAt.Before(*w.StartedAt) || !w.allMembersReady() {
		return fmt.Errorf("%w: invalid paused wave evidence", ErrInvalidWave)
	}
	return nil
}

func (w Wave) validateExpiredEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ReadyWindowStateExpired || w.StartedAt != nil || w.PausedAt != nil || anyMemberReady(w.Members) {
		return fmt.Errorf("%w: invalid expired ready-window evidence", ErrInvalidWave)
	}
	return nil
}

func (w Wave) validateSupersededEvidence() error {
	if w.ReadyWindow == nil || w.ReadyWindow.State != ReadyWindowStateSuperseded || w.PausedAt != nil || anyMemberReady(w.Members) {
		return fmt.Errorf("%w: invalid superseded wave evidence", ErrInvalidWave)
	}
	return nil
}

func (w *Wave) OpenReadyWindow(id uuid.UUID, revisionID ReadyWindowRevisionID, openedAt, deadline time.Time) error {
	if w == nil {
		return fmt.Errorf("%w: nil wave", ErrInvalidWave)
	}
	if w.State != WaveStatePlanned {
		return fmt.Errorf("%w: %s -> %s", ErrWaveTransition, w.State, WaveStateReadyWindowOpen)
	}
	if err := w.Validate(); err != nil {
		return err
	}
	window := ReadyWindow{ID: id, WaveID: w.ID, RevisionID: revisionID, State: ReadyWindowStateOpen, OpenedAt: openedAt, Deadline: deadline}
	if err := window.Validate(); err != nil {
		return err
	}
	w.ReadyWindow = &window
	w.State = WaveStateReadyWindowOpen
	return nil
}

func (w *Wave) MarkReady(windowID, participantID uuid.UUID, at time.Time) (bool, error) {
	if w == nil {
		return false, fmt.Errorf("%w: nil wave", ErrInvalidWave)
	}
	if w.State != WaveStateReadyWindowOpen && w.State != WaveStateReady {
		return false, fmt.Errorf("%w: wave is not accepting readiness", ErrWaveTransition)
	}
	if w.ReadyWindow == nil || w.ReadyWindow.ID != windowID || w.ReadyWindow.State != ReadyWindowStateOpen {
		return false, ErrReadyWindowStale
	}
	if at.Before(w.ReadyWindow.OpenedAt) || at.After(w.ReadyWindow.Deadline) {
		return false, ErrReadyWindowDeadline
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
			w.State = WaveStateReady
		}
		return true, nil
	}
	return false, ErrWaveMemberNotFound
}

func (w *Wave) Start(windowID uuid.UUID, at time.Time) (bool, error) {
	if w == nil {
		return false, fmt.Errorf("%w: nil wave", ErrInvalidWave)
	}
	if w.StartedAt != nil {
		return false, ErrWaveAlreadyStarted
	}
	if w.State != WaveStateReady || w.ReadyWindow == nil || w.ReadyWindow.State != ReadyWindowStateOpen {
		return false, fmt.Errorf("%w: wave is not ready", ErrWaveTransition)
	}
	if w.ReadyWindow.ID != windowID {
		return false, ErrReadyWindowStale
	}
	if at.Before(w.ReadyWindow.OpenedAt) || at.After(w.ReadyWindow.Deadline) {
		return false, ErrReadyWindowDeadline
	}
	w.StartedAt = &at
	w.ReadyWindow.State = ReadyWindowStateConsumed
	w.ReadyWindow.ConsumedAt = &at
	w.State = WaveStateActive
	return true, nil
}

func (w *Wave) Pause(at time.Time) error {
	if w == nil {
		return fmt.Errorf("%w: nil wave", ErrInvalidWave)
	}
	if w.State != WaveStateActive || w.StartedAt == nil || at.Before(*w.StartedAt) {
		return fmt.Errorf("%w: cannot pause wave", ErrWaveTransition)
	}
	w.PausedAt = &at
	w.State = WaveStatePaused
	return nil
}

func (w *Wave) Resume() error {
	if w == nil {
		return fmt.Errorf("%w: nil wave", ErrInvalidWave)
	}
	if w.State != WaveStatePaused {
		return fmt.Errorf("%w: cannot resume wave", ErrWaveTransition)
	}
	w.PausedAt = nil
	w.State = WaveStateActive
	return nil
}

func (w *Wave) ExpireReadyWindow(windowID uuid.UUID, at time.Time) error {
	if w == nil {
		return fmt.Errorf("%w: nil wave", ErrInvalidWave)
	}
	if w.State != WaveStateReadyWindowOpen || w.ReadyWindow == nil || w.ReadyWindow.State != ReadyWindowStateOpen {
		return fmt.Errorf("%w: ready window cannot expire", ErrWaveTransition)
	}
	if w.ReadyWindow.ID != windowID {
		return ErrReadyWindowStale
	}
	if !at.After(w.ReadyWindow.Deadline) {
		return fmt.Errorf("%w: deadline has not passed", ErrWaveTransition)
	}
	clearMemberReadiness(w.Members)
	w.ReadyWindow.State = ReadyWindowStateExpired
	w.State = WaveStateReadyWindowExpired
	return nil
}

func (w *Wave) SupersedeWithReplacement(
	replacementID uuid.UUID,
	replacementRevisionID WaveRevisionID,
	windowID uuid.UUID,
	windowRevisionID ReadyWindowRevisionID,
	openedAt time.Time,
	deadline time.Time,
) (Wave, error) {
	if w == nil {
		return Wave{}, fmt.Errorf("%w: nil wave", ErrInvalidWave)
	}
	if err := w.Validate(); err != nil {
		return Wave{}, err
	}
	if w.State == WaveStateCompleted || w.State == WaveStateSuperseded || w.ReadyWindow == nil {
		return Wave{}, fmt.Errorf("%w: wave cannot be superseded", ErrWaveTransition)
	}
	if replacementID == w.ID || replacementRevisionID == w.RevisionID || windowID == w.ReadyWindow.ID || windowRevisionID == w.ReadyWindow.RevisionID {
		return Wave{}, ErrWaveReplacementReuse
	}
	members := make([]WaveMember, len(w.Members))
	for i, member := range w.Members {
		members[i] = WaveMember{ParticipantID: member.ParticipantID}
	}
	replacement := Wave{
		ID:           replacementID,
		TournamentID: w.TournamentID,
		RevisionID:   replacementRevisionID,
		State:        WaveStatePlanned,
		Members:      members,
	}
	if err := replacement.OpenReadyWindow(windowID, windowRevisionID, openedAt, deadline); err != nil {
		return Wave{}, err
	}
	if err := replacement.Validate(); err != nil {
		return Wave{}, err
	}
	clearMemberReadiness(w.Members)
	w.ReadyWindow.State = ReadyWindowStateSuperseded
	w.ReadyWindow.ConsumedAt = nil
	w.PausedAt = nil
	w.State = WaveStateSuperseded
	return replacement, nil
}

func (w Wave) allMembersReady() bool {
	return len(w.Members) > 0 && !hasUnreadyMember(w.Members)
}

func hasUnreadyMember(members []WaveMember) bool {
	for _, member := range members {
		if !member.Ready {
			return true
		}
	}
	return false
}

func anyMemberReady(members []WaveMember) bool {
	for _, member := range members {
		if member.Ready {
			return true
		}
	}
	return false
}

func clearMemberReadiness(members []WaveMember) {
	for i := range members {
		members[i].Ready = false
	}
}
