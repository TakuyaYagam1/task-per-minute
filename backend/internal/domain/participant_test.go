package domain_test

import (
	"errors"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestParticipantAttendanceTransitions(t *testing.T) {
	t.Parallel()

	states := []domain.AttendanceState{
		domain.AttendanceStateInvited,
		domain.AttendanceStateRegistered,
		domain.AttendanceStateCheckedIn,
		domain.AttendanceStateWithdrawn,
	}
	allowed := map[domain.AttendanceState]map[domain.AttendanceState]bool{
		domain.AttendanceStateInvited: {
			domain.AttendanceStateRegistered: true,
			domain.AttendanceStateWithdrawn:  true,
		},
		domain.AttendanceStateRegistered: {
			domain.AttendanceStateCheckedIn: true,
			domain.AttendanceStateWithdrawn: true,
		},
		domain.AttendanceStateCheckedIn: {
			domain.AttendanceStateWithdrawn: true,
		},
	}

	for _, from := range states {
		for _, to := range states {
			want := from == to || allowed[from][to]
			if got := from.CanTransitionTo(to); got != want {
				t.Errorf("%s.CanTransitionTo(%s) = %v, want %v", from, to, got, want)
			}
		}
	}
}

func TestParticipantRosterRejectsDuplicates(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	playerID := uuid.New()
	roster := domain.Roster{
		TournamentID: tournamentID,
		Participants: []domain.Participant{
			{ID: uuid.New(), TournamentID: tournamentID, PlayerID: playerID, Seed: 1, Attendance: domain.AttendanceStateInvited},
			{ID: uuid.New(), TournamentID: tournamentID, PlayerID: playerID, Seed: 2, Attendance: domain.AttendanceStateRegistered},
		},
	}

	if err := roster.Validate(); !errors.Is(err, domain.ErrInvalidRoster) {
		t.Fatalf("duplicate player error = %v, want ErrInvalidTournamentRoster", err)
	}

	roster.Participants[1].PlayerID = uuid.New()
	roster.Participants[1].Seed = 1
	if err := roster.Validate(); !errors.Is(err, domain.ErrInvalidRoster) {
		t.Fatalf("duplicate seed error = %v, want ErrInvalidTournamentRoster", err)
	}
}

func TestParticipantRosterLockBoundary(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	playerID := uuid.New()
	roster := domain.Roster{
		TournamentID: tournamentID,
		Participants: []domain.Participant{{
			ID:           uuid.New(),
			TournamentID: tournamentID,
			PlayerID:     playerID,
			Seed:         1,
			Attendance:   domain.AttendanceStateInvited,
		}},
	}

	changed, err := roster.ChangeAttendance(playerID, domain.AttendanceStateRegistered)
	if err != nil || !changed {
		t.Fatalf("register error = %v, changed = %v", err, changed)
	}
	changed, err = roster.Lock()
	if err != nil || !changed {
		t.Fatalf("lock error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.ChangeAttendance(playerID, domain.AttendanceStateCheckedIn); !errors.Is(err, domain.ErrRosterLocked) || changed {
		t.Fatalf("locked attendance error = %v, changed = %v", err, changed)
	}
	changed, err = roster.Unlock()
	if err != nil || !changed {
		t.Fatalf("pre-start unlock error = %v, changed = %v", err, changed)
	}
	changed, err = roster.ChangeAttendance(playerID, domain.AttendanceStateCheckedIn)
	if err != nil || !changed {
		t.Fatalf("check-in error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.Lock(); err != nil || !changed {
		t.Fatalf("second lock error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.MarkExecutionStarted(); err != nil || !changed {
		t.Fatalf("start error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.Unlock(); !errors.Is(err, domain.ErrRosterExecutionStarted) || changed {
		t.Fatalf("post-start unlock error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.ChangeAttendance(playerID, domain.AttendanceStateWithdrawn); !errors.Is(err, domain.ErrRosterExecutionStarted) || changed {
		t.Fatalf("post-start attendance error = %v, changed = %v", err, changed)
	}
}

func TestParticipantRejectsAttendanceRollback(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	playerID := uuid.New()
	roster := domain.Roster{
		TournamentID: tournamentID,
		Participants: []domain.Participant{{
			ID:           uuid.New(),
			TournamentID: tournamentID,
			PlayerID:     playerID,
			Seed:         1,
			Attendance:   domain.AttendanceStateCheckedIn,
		}},
	}

	changed, err := roster.ChangeAttendance(playerID, domain.AttendanceStateRegistered)
	if !errors.Is(err, domain.ErrAttendanceTransition) || changed {
		t.Fatalf("attendance rollback error = %v, changed = %v", err, changed)
	}
}
