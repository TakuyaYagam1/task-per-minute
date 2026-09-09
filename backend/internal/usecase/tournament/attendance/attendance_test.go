package attendance_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	attendanceusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/attendance/mocks"
)

func TestAttendanceWorkflow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 19, 0, 0, 0, time.UTC)
	rosterID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	participantID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	playerID := uuid.MustParse("20000000-0000-0000-0000-000000000003")
	state, repository := newAttendanceRepository(t, rosterID)
	useCase := attendanceusecase.NewAttendanceUseCase(
		repository,
		attendanceNewFixedTournamentClock(t, now, 9),
	)

	invited, changed, err := useCase.InviteParticipant(t.Context(), attendanceusecase.ParticipantInvitationCommand{
		ParticipantID: participantID,
		RosterID:      rosterID,
		PlayerID:      playerID,
		Seed:          3,
	})
	if err != nil || !changed {
		t.Fatalf("InviteParticipant() error = %v, changed = %v", err, changed)
	}
	if invited.Attendance != domain.AttendanceStateInvited || invited.Seed != 3 || !invited.CreatedAt.Equal(now) {
		t.Fatalf("invited participant = %+v", *invited)
	}

	for _, transition := range []struct {
		expected domain.AttendanceState
		next     domain.AttendanceState
	}{
		{domain.AttendanceStateInvited, domain.AttendanceStateRegistered},
		{domain.AttendanceStateRegistered, domain.AttendanceStateCheckedIn},
		{domain.AttendanceStateCheckedIn, domain.AttendanceStateWithdrawn},
	} {
		updated, transitionChanged, transitionErr := useCase.ChangeAttendance(
			t.Context(),
			attendanceusecase.AttendanceChangeCommand{
				ParticipantID: participantID,
				Expected:      transition.expected,
				Next:          transition.next,
			},
		)
		if transitionErr != nil || !transitionChanged {
			t.Fatalf("ChangeAttendance(%s -> %s) error = %v, changed = %v",
				transition.expected, transition.next, transitionErr, transitionChanged)
		}
		if updated.Attendance != transition.next {
			t.Fatalf("ChangeAttendance() state = %s, want %s", updated.Attendance, transition.next)
		}
	}

	replacementID := uuid.MustParse("20000000-0000-0000-0000-000000000004")
	replacementPlayerID := uuid.MustParse("20000000-0000-0000-0000-000000000005")
	replacement, changed, err := useCase.ReplaceWithdrawnParticipant(
		t.Context(),
		attendanceusecase.ParticipantReplacementCommand{
			WithdrawnParticipantID:   participantID,
			ReplacementParticipantID: replacementID,
			RosterID:                 rosterID,
			ReplacementPlayerID:      replacementPlayerID,
		},
	)
	if err != nil || !changed {
		t.Fatalf("ReplaceWithdrawnParticipant() error = %v, changed = %v", err, changed)
	}
	if replacement.ID != replacementID || replacement.PlayerID != replacementPlayerID || replacement.Seed != 3 ||
		replacement.Attendance != domain.AttendanceStateInvited {
		t.Fatalf("replacement = %+v", *replacement)
	}

	secondID := uuid.MustParse("20000000-0000-0000-0000-000000000006")
	_, changed, err = useCase.InviteParticipant(t.Context(), attendanceusecase.ParticipantInvitationCommand{
		ParticipantID: secondID,
		RosterID:      rosterID,
		PlayerID:      uuid.MustParse("20000000-0000-0000-0000-000000000007"),
		Seed:          4,
	})
	if err != nil || !changed {
		t.Fatalf("InviteParticipant(second) error = %v, changed = %v", err, changed)
	}
	_, changed, err = useCase.ChangeAttendance(t.Context(), attendanceusecase.AttendanceChangeCommand{
		ParticipantID: secondID,
		Expected:      domain.AttendanceStateInvited,
		Next:          domain.AttendanceStateWithdrawn,
	})
	if err != nil || !changed {
		t.Fatalf("ChangeAttendance(second withdrawal) error = %v, changed = %v", err, changed)
	}
	if _, _, err = useCase.ReplaceWithdrawnParticipant(t.Context(), attendanceusecase.ParticipantReplacementCommand{
		WithdrawnParticipantID:   secondID,
		ReplacementParticipantID: uuid.MustParse("20000000-0000-0000-0000-000000000008"),
		RosterID:                 rosterID,
		ReplacementPlayerID:      replacementPlayerID,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ReplaceWithdrawnParticipant(duplicate player) error = %v, want conflict", err)
	}

	state.locked = true
	if _, _, err = useCase.ChangeAttendance(t.Context(), attendanceusecase.AttendanceChangeCommand{
		ParticipantID: replacementID,
		Expected:      domain.AttendanceStateInvited,
		Next:          domain.AttendanceStateRegistered,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ChangeAttendance(locked) error = %v, want conflict", err)
	}
}

type attendanceRepositoryState struct {
	rosterID     uuid.UUID
	participants map[uuid.UUID]attendanceusecase.ParticipantRecord
	locked       bool
}

func newAttendanceRepository(
	t *testing.T,
	rosterID uuid.UUID,
) (*attendanceRepositoryState, *tournamentmocks.MockAttendanceRepository) {
	t.Helper()
	state := &attendanceRepositoryState{
		rosterID:     rosterID,
		participants: make(map[uuid.UUID]attendanceusecase.ParticipantRecord),
	}
	repository := tournamentmocks.NewMockAttendanceRepository(t)
	repository.EXPECT().
		InviteParticipant(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, in attendanceusecase.ParticipantInput) (*attendanceusecase.ParticipantRecord, bool, error) {
			if state.locked || in.RosterID != state.rosterID {
				return nil, false, domain.ErrConflict
			}
			for _, participant := range state.participants {
				if participant.PlayerID == in.PlayerID || participant.Seed == int(in.Seed) {
					return nil, false, domain.ErrConflict
				}
			}
			record := attendanceusecase.ParticipantRecord{
				ID: in.ID, RosterID: in.RosterID, PlayerID: in.PlayerID, Seed: int(in.Seed),
				Attendance: in.Attendance, CreatedAt: in.CreatedAt, UpdatedAt: in.CreatedAt,
			}
			state.participants[record.ID] = record
			return cloneParticipantRecord(record), true, nil
		}).
		Times(2)
	repository.EXPECT().
		ChangeAttendance(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			participantID uuid.UUID,
			expected domain.AttendanceState,
			next domain.AttendanceState,
			updatedAt time.Time,
		) (*attendanceusecase.ParticipantRecord, bool, error) {
			if state.locked {
				return nil, false, domain.ErrConflict
			}
			record, exists := state.participants[participantID]
			if !exists || record.Attendance != expected {
				return nil, false, nil
			}
			record.Attendance = next
			record.UpdatedAt = updatedAt
			state.participants[participantID] = record
			return cloneParticipantRecord(record), true, nil
		}).
		Times(5)
	repository.EXPECT().
		ReplaceWithdrawnParticipant(mock.Anything, mock.Anything).
		RunAndReturn(func(
			_ context.Context,
			in attendanceusecase.ParticipantReplacementInput,
		) (*attendanceusecase.ParticipantRecord, bool, error) {
			if state.locked || in.RosterID != state.rosterID {
				return nil, false, domain.ErrConflict
			}
			current, exists := state.participants[in.WithdrawnParticipantID]
			if !exists || current.Attendance != domain.AttendanceStateWithdrawn {
				return nil, false, nil
			}
			for id, participant := range state.participants {
				if id != current.ID && participant.PlayerID == in.ReplacementPlayerID {
					return nil, false, domain.ErrConflict
				}
			}
			delete(state.participants, current.ID)
			current.ID = in.ReplacementParticipantID
			current.PlayerID = in.ReplacementPlayerID
			current.Attendance = domain.AttendanceStateInvited
			current.UpdatedAt = in.ReplacedAt
			state.participants[current.ID] = current
			return cloneParticipantRecord(current), true, nil
		}).
		Times(2)
	repository.EXPECT().
		ListRosterParticipants(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, rosterID uuid.UUID) ([]attendanceusecase.ParticipantRecord, error) {
			if rosterID != state.rosterID {
				return nil, attendanceusecase.ErrRosterNotFound
			}
			out := make([]attendanceusecase.ParticipantRecord, 0, len(state.participants))
			for _, participant := range state.participants {
				out = append(out, participant)
			}
			sort.Slice(out, func(i, j int) bool { return out[i].Seed < out[j].Seed })
			return out, nil
		}).
		Times(2)
	return state, repository
}

func cloneParticipantRecord(record attendanceusecase.ParticipantRecord) *attendanceusecase.ParticipantRecord {
	cloned := record
	return &cloned
}
