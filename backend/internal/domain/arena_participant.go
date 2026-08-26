package domain

import (
	"errors"
	"fmt"

	"github.com/google/uuid"
)

type ArenaAttendanceState string

const (
	ArenaAttendanceStateInvited    ArenaAttendanceState = "invited"
	ArenaAttendanceStateRegistered ArenaAttendanceState = "registered"
	ArenaAttendanceStateCheckedIn  ArenaAttendanceState = "checked_in"
	ArenaAttendanceStateWithdrawn  ArenaAttendanceState = "withdrawn"
)

var (
	ErrInvalidArenaParticipant     = errors.New("invalid arena participant")
	ErrInvalidArenaRoster          = errors.New("invalid arena roster")
	ErrArenaAttendanceTransition   = errors.New("arena attendance transition is not allowed")
	ErrArenaRosterLocked           = errors.New("arena roster is locked")
	ErrArenaRosterExecutionStarted = errors.New("arena roster execution has started")
)

type ArenaParticipant struct {
	ID           uuid.UUID
	TournamentID uuid.UUID
	PlayerID     uuid.UUID
	Seed         int
	Attendance   ArenaAttendanceState
}

type ArenaRoster struct {
	TournamentID     uuid.UUID
	Participants     []ArenaParticipant
	Locked           bool
	ExecutionStarted bool
}

func (s ArenaAttendanceState) IsValid() bool {
	switch s {
	case ArenaAttendanceStateInvited,
		ArenaAttendanceStateRegistered,
		ArenaAttendanceStateCheckedIn,
		ArenaAttendanceStateWithdrawn:
		return true
	}
	return false
}

func (s ArenaAttendanceState) CanTransitionTo(next ArenaAttendanceState) bool {
	if !s.IsValid() || !next.IsValid() {
		return false
	}
	if s == next {
		return true
	}
	switch s {
	case ArenaAttendanceStateInvited:
		return next == ArenaAttendanceStateRegistered || next == ArenaAttendanceStateWithdrawn
	case ArenaAttendanceStateRegistered:
		return next == ArenaAttendanceStateCheckedIn || next == ArenaAttendanceStateWithdrawn
	case ArenaAttendanceStateCheckedIn:
		return next == ArenaAttendanceStateWithdrawn
	case ArenaAttendanceStateWithdrawn:
		return false
	}
	return false
}

func (p ArenaParticipant) Validate() error {
	if p.ID == uuid.Nil || p.TournamentID == uuid.Nil || p.PlayerID == uuid.Nil {
		return fmt.Errorf("%w: missing identity", ErrInvalidArenaParticipant)
	}
	if p.Seed < 1 {
		return fmt.Errorf("%w: seed must be positive", ErrInvalidArenaParticipant)
	}
	if !p.Attendance.IsValid() {
		return fmt.Errorf("%w: unknown attendance %q", ErrInvalidArenaParticipant, p.Attendance)
	}
	return nil
}

func (r ArenaRoster) Validate() error {
	if r.TournamentID == uuid.Nil {
		return fmt.Errorf("%w: missing tournament identity", ErrInvalidArenaRoster)
	}
	ids := make(map[uuid.UUID]struct{}, len(r.Participants))
	players := make(map[uuid.UUID]struct{}, len(r.Participants))
	seeds := make(map[int]struct{}, len(r.Participants))
	for _, participant := range r.Participants {
		if err := participant.Validate(); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidArenaRoster, err)
		}
		if participant.TournamentID != r.TournamentID {
			return fmt.Errorf("%w: participant belongs to another tournament", ErrInvalidArenaRoster)
		}
		if _, exists := ids[participant.ID]; exists {
			return fmt.Errorf("%w: duplicate participant identity", ErrInvalidArenaRoster)
		}
		if _, exists := players[participant.PlayerID]; exists {
			return fmt.Errorf("%w: duplicate player", ErrInvalidArenaRoster)
		}
		if _, exists := seeds[participant.Seed]; exists {
			return fmt.Errorf("%w: duplicate seed", ErrInvalidArenaRoster)
		}
		ids[participant.ID] = struct{}{}
		players[participant.PlayerID] = struct{}{}
		seeds[participant.Seed] = struct{}{}
	}
	if r.ExecutionStarted && !r.Locked {
		return fmt.Errorf("%w: started roster must remain locked", ErrInvalidArenaRoster)
	}
	return nil
}

func (r *ArenaRoster) ChangeAttendance(playerID uuid.UUID, next ArenaAttendanceState) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("%w: nil roster", ErrInvalidArenaRoster)
	}
	if err := r.Validate(); err != nil {
		return false, err
	}
	if r.ExecutionStarted {
		return false, ErrArenaRosterExecutionStarted
	}
	if r.Locked {
		return false, ErrArenaRosterLocked
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
			return false, fmt.Errorf("%w: %s -> %s", ErrArenaAttendanceTransition, current, next)
		}
		r.Participants[i].Attendance = next
		return true, nil
	}
	return false, fmt.Errorf("%w: player not found", ErrInvalidArenaRoster)
}

func (r *ArenaRoster) Lock() (bool, error) {
	if r == nil {
		return false, fmt.Errorf("%w: nil roster", ErrInvalidArenaRoster)
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

func (r *ArenaRoster) Unlock() (bool, error) {
	if r == nil {
		return false, fmt.Errorf("%w: nil roster", ErrInvalidArenaRoster)
	}
	if err := r.Validate(); err != nil {
		return false, err
	}
	if r.ExecutionStarted {
		return false, ErrArenaRosterExecutionStarted
	}
	if !r.Locked {
		return false, nil
	}
	r.Locked = false
	return true, nil
}

func (r *ArenaRoster) MarkExecutionStarted() (bool, error) {
	if r == nil {
		return false, fmt.Errorf("%w: nil roster", ErrInvalidArenaRoster)
	}
	if err := r.Validate(); err != nil {
		return false, err
	}
	if !r.Locked {
		return false, ErrArenaRosterLocked
	}
	if r.ExecutionStarted {
		return false, nil
	}
	r.ExecutionStarted = true
	return true, nil
}
