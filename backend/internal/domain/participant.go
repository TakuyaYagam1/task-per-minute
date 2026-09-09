package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type AttendanceState string

const (
	AttendanceStateInvited    AttendanceState = "invited"
	AttendanceStateRegistered AttendanceState = "registered"
	AttendanceStateCheckedIn  AttendanceState = "checked_in"
	AttendanceStateWithdrawn  AttendanceState = "withdrawn"
)

var (
	ErrInvalidParticipant     = errors.New("invalid tournament participant")
	ErrInvalidRoster          = errors.New("invalid tournament roster")
	ErrAttendanceTransition   = errors.New("attendance transition is not allowed")
	ErrRosterLocked           = errors.New("tournament roster is locked")
	ErrRosterExecutionStarted = errors.New("tournament roster execution has started")
)

type Participant struct {
	ID           uuid.UUID
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Seed         int
	Attendance   AttendanceState
}

type Roster struct {
	TournamentID     uuid.UUID
	Participants     []Participant
	Locked           bool
	ExecutionStarted bool
}

func (s AttendanceState) IsValid() bool {
	switch s {
	case AttendanceStateInvited,
		AttendanceStateRegistered,
		AttendanceStateCheckedIn,
		AttendanceStateWithdrawn:
		return true
	}
	return false
}

func (s AttendanceState) CanTransitionTo(next AttendanceState) bool {
	if !s.IsValid() || !next.IsValid() {
		return false
	}
	if s == next {
		return true
	}
	switch s {
	case AttendanceStateInvited:
		return next == AttendanceStateRegistered || next == AttendanceStateWithdrawn
	case AttendanceStateRegistered:
		return next == AttendanceStateCheckedIn || next == AttendanceStateWithdrawn
	case AttendanceStateCheckedIn:
		return next == AttendanceStateWithdrawn
	case AttendanceStateWithdrawn:
		return false
	}
	return false
}

func (p Participant) Validate() error {
	if p.ID == uuid.Nil || p.TournamentID == uuid.Nil || p.PlayerID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidParticipant)
	}
	if p.Seed < 1 {
		return fmt.Errorf("%w: seed must be positive", ErrInvalidParticipant)
	}
	if !p.Attendance.IsValid() {
		return fmt.Errorf("%w: unknown attendance %q", ErrInvalidParticipant, p.Attendance)
	}
	return nil
}

func (r Roster) Validate() error {
	if r.TournamentID == uuid.Nil {
		return fmt.Errorf("%w: missing tournament identity", ErrInvalidRoster)
	}
	ids := make(map[uuid.UUID]struct{}, len(r.Participants))
	players := make(map[uuid.UUID]struct{}, len(r.Participants))
	seeds := make(map[int]struct{}, len(r.Participants))
	for _, participant := range r.Participants {
		if err := participant.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRoster, err)
		}
		if participant.TournamentID != r.TournamentID {
			return fmt.Errorf("%w: participant belongs to another tournament", ErrInvalidRoster)
		}
		if _, exists := ids[participant.ID]; exists {
			return fmt.Errorf("%w: duplicate participant identity", ErrInvalidRoster)
		}
		if _, exists := players[participant.PlayerID]; exists {
			return fmt.Errorf("%w: duplicate player", ErrInvalidRoster)
		}
		if _, exists := seeds[participant.Seed]; exists {
			return fmt.Errorf("%w: duplicate seed", ErrInvalidRoster)
		}
		ids[participant.ID] = struct{}{}
		players[participant.PlayerID] = struct{}{}
		seeds[participant.Seed] = struct{}{}
	}
	if r.ExecutionStarted && !r.Locked {
		return fmt.Errorf("%w: started roster must remain locked", ErrInvalidRoster)
	}
	return nil
}

func (r *Roster) ChangeAttendance(playerID uuid.UUID, next AttendanceState) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("%w: nil roster", ErrInvalidRoster)
	}
	if err := r.Validate(); err != nil {
		return false, err
	}
	if r.ExecutionStarted {
		return false, ErrRosterExecutionStarted
	}
	if r.Locked {
		return false, ErrRosterLocked
	}
	for i := range r.Participants {
		if r.Participants[i].PlayerID != playerID {
			continue
		}
		current := r.Participants[i].Attendance
		if current == next {
			return false, nil
		}
		if !current.CanTransitionTo(next) {
			return false, fmt.Errorf("%w: %s -> %s", ErrAttendanceTransition, current, next)
		}
		r.Participants[i].Attendance = next
		return true, nil
	}
	return false, fmt.Errorf("%w: player not found", ErrInvalidRoster)
}

func (r *Roster) Lock() (bool, error) {
	if r == nil {
		return false, fmt.Errorf("%w: nil roster", ErrInvalidRoster)
	}
	if err := r.Validate(); err != nil {
		return false, err
	}
	if r.Locked {
		return false, nil
	}
	r.Locked = true
	return true, nil
}

func (r *Roster) Unlock() (bool, error) {
	if r == nil {
		return false, fmt.Errorf("%w: nil roster", ErrInvalidRoster)
	}
	if err := r.Validate(); err != nil {
		return false, err
	}
	if r.ExecutionStarted {
		return false, ErrRosterExecutionStarted
	}
	if !r.Locked {
		return false, nil
	}
	r.Locked = false
	return true, nil
}

func (r *Roster) MarkExecutionStarted() (bool, error) {
	if r == nil {
		return false, fmt.Errorf("%w: nil roster", ErrInvalidRoster)
	}
	if err := r.Validate(); err != nil {
		return false, err
	}
	if !r.Locked {
		return false, ErrRosterLocked
	}
	if r.ExecutionStarted {
		return false, nil
	}
	r.ExecutionStarted = true
	return true, nil
}
