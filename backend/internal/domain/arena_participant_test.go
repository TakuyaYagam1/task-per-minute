package domain_test

import (
	"errors"
	"testing"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
)

func TestArenaParticipantAttendanceTransitions(t *testing.T) {
	t.Parallel()

	states := []domain.ArenaAttendanceState{
		domain.ArenaAttendanceStateInvited,
		domain.ArenaAttendanceStateRegistered,
		domain.ArenaAttendanceStateCheckedIn,
		domain.ArenaAttendanceStateWithdrawn,
	}
	allowed := map[domain.ArenaAttendanceState]map[domain.ArenaAttendanceState]bool{
		domain.ArenaAttendanceStateInvited: {
			domain.ArenaAttendanceStateRegistered: true,
			domain.ArenaAttendanceStateWithdrawn:  true,
		},
		domain.ArenaAttendanceStateRegistered: {
			domain.ArenaAttendanceStateCheckedIn: true,
			domain.ArenaAttendanceStateWithdrawn: true,
		},
		domain.ArenaAttendanceStateCheckedIn: {
			domain.ArenaAttendanceStateWithdrawn: true,
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

func TestArenaParticipantRosterRejectsDuplicates(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	playerID := uuid.New()
	roster := domain.ArenaRoster{
		TournamentID: tournamentID,
		Participants: []domain.ArenaParticipant{
			{ID: uuid.New(), TournamentID: tournamentID, PlayerID: playerID, Seed: 1, Attendance: domain.ArenaAttendanceStateInvited},
			{ID: uuid.New(), TournamentID: tournamentID, PlayerID: playerID, Seed: 2, Attendance: domain.ArenaAttendanceStateRegistered},
		},
	}

	if err := roster.Validate(); !errors.Is(err, domain.ErrInvalidArenaRoster) {
		t.Fatalf("duplicate player error = %v, want ErrInvalidArenaRoster", err)
	}

	roster.Participants[1].PlayerID = uuid.New()
	roster.Participants[1].Seed = 1
	if err := roster.Validate(); !errors.Is(err, domain.ErrInvalidArenaRoster) {
		t.Fatalf("duplicate seed error = %v, want ErrInvalidArenaRoster", err)
	}
}

func TestArenaParticipantRosterLockBoundary(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	playerID := uuid.New()
	roster := domain.ArenaRoster{
		TournamentID: tournamentID,
		Participants: []domain.ArenaParticipant{{
			ID:           uuid.New(),
			TournamentID: tournamentID,
			PlayerID:     playerID,
			Seed:         1,
			Attendance:   domain.ArenaAttendanceStateInvited,
		}},
	}

	changed, err := roster.ChangeAttendance(playerID, domain.ArenaAttendanceStateRegistered)
	if err != nil || !changed {
		t.Fatalf("register error = %v, changed = %v", err, changed)
	}
	changed, err = roster.Lock()
	if err != nil || !changed {
		t.Fatalf("lock error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.ChangeAttendance(playerID, domain.ArenaAttendanceStateCheckedIn); !errors.Is(err, domain.ErrArenaRosterLocked) || changed {
		t.Fatalf("locked attendance error = %v, changed = %v", err, changed)
	}
	changed, err = roster.Unlock()
	if err != nil || !changed {
		t.Fatalf("pre-start unlock error = %v, changed = %v", err, changed)
	}
	changed, err = roster.ChangeAttendance(playerID, domain.ArenaAttendanceStateCheckedIn)
	if err != nil || !changed {
		t.Fatalf("check-in error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.Lock(); err != nil || !changed {
		t.Fatalf("second lock error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.MarkExecutionStarted(); err != nil || !changed {
		t.Fatalf("start error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.Unlock(); !errors.Is(err, domain.ErrArenaRosterExecutionStarted) || changed {
		t.Fatalf("post-start unlock error = %v, changed = %v", err, changed)
	}
	if changed, err = roster.ChangeAttendance(playerID, domain.ArenaAttendanceStateWithdrawn); !errors.Is(err, domain.ErrArenaRosterExecutionStarted) || changed {
		t.Fatalf("post-start attendance error = %v, changed = %v", err, changed)
	}
}

func TestArenaParticipantRejectsAttendanceRollback(t *testing.T) {
	t.Parallel()

	tournamentID := uuid.New()
	playerID := uuid.New()
	roster := domain.ArenaRoster{
		TournamentID: tournamentID,
		Participants: []domain.ArenaParticipant{{
			ID:           uuid.New(),
			TournamentID: tournamentID,
			PlayerID:     playerID,
			Seed:         1,
			Attendance:   domain.ArenaAttendanceStateCheckedIn,
		}},
	}

	changed, err := roster.ChangeAttendance(playerID, domain.ArenaAttendanceStateRegistered)
	if !errors.Is(err, domain.ErrArenaAttendanceTransition) || changed {
		t.Fatalf("attendance rollback error = %v, changed = %v", err, changed)
	}
}
