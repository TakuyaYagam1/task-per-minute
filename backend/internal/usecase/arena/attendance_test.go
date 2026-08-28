package arena_test

import (
	"context"
	"errors"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestAttendanceWorkflow(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 19, 0, 0, 0, time.UTC)
	rosterID := uuid.MustParse("20000000-0000-0000-0000-000000000001")
	participantID := uuid.MustParse("20000000-0000-0000-0000-000000000002")
	playerID := uuid.MustParse("20000000-0000-0000-0000-000000000003")
	repo := newAttendanceRepositoryFake(rosterID)
	useCase := arena.NewAttendanceUseCase(repo, fixedArenaClock{now: now})

	invited, changed, err := useCase.InviteParticipant(t.Context(), arena.ParticipantInvitationCommand{
		ParticipantID: participantID,
		RosterID:      rosterID,
		PlayerID:      playerID,
		Seed:          3,
	})
	if err != nil || !changed {
		t.Fatalf("InviteParticipant() error = %v, changed = %v", err, changed)
	}
	if invited.Attendance != domain.ArenaAttendanceStateInvited || invited.Seed != 3 || !invited.CreatedAt.Equal(now) {
		t.Fatalf("invited participant = %+v", *invited)
	}

	for _, transition := range []struct {
		expected domain.ArenaAttendanceState
		next     domain.ArenaAttendanceState
	}{
		{domain.ArenaAttendanceStateInvited, domain.ArenaAttendanceStateRegistered},
		{domain.ArenaAttendanceStateRegistered, domain.ArenaAttendanceStateCheckedIn},
		{domain.ArenaAttendanceStateCheckedIn, domain.ArenaAttendanceStateWithdrawn},
	} {
		updated, transitionChanged, transitionErr := useCase.ChangeAttendance(
			t.Context(),
			arena.AttendanceChangeCommand{
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
		arena.ParticipantReplacementCommand{
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
		replacement.Attendance != domain.ArenaAttendanceStateInvited {
		t.Fatalf("replacement = %+v", *replacement)
	}

	secondID := uuid.MustParse("20000000-0000-0000-0000-000000000006")
	_, changed, err = useCase.InviteParticipant(t.Context(), arena.ParticipantInvitationCommand{
		ParticipantID: secondID,
		RosterID:      rosterID,
		PlayerID:      uuid.MustParse("20000000-0000-0000-0000-000000000007"),
		Seed:          4,
	})
	if err != nil || !changed {
		t.Fatalf("InviteParticipant(second) error = %v, changed = %v", err, changed)
	}
	_, changed, err = useCase.ChangeAttendance(t.Context(), arena.AttendanceChangeCommand{
		ParticipantID: secondID,
		Expected:      domain.ArenaAttendanceStateInvited,
		Next:          domain.ArenaAttendanceStateWithdrawn,
	})
	if err != nil || !changed {
		t.Fatalf("ChangeAttendance(second withdrawal) error = %v, changed = %v", err, changed)
	}
	if _, _, err = useCase.ReplaceWithdrawnParticipant(t.Context(), arena.ParticipantReplacementCommand{
		WithdrawnParticipantID:   secondID,
		ReplacementParticipantID: uuid.MustParse("20000000-0000-0000-0000-000000000008"),
		RosterID:                 rosterID,
		ReplacementPlayerID:      replacementPlayerID,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ReplaceWithdrawnParticipant(duplicate player) error = %v, want conflict", err)
	}

	repo.locked = true
	if _, _, err = useCase.ChangeAttendance(t.Context(), arena.AttendanceChangeCommand{
		ParticipantID: replacementID,
		Expected:      domain.ArenaAttendanceStateInvited,
		Next:          domain.ArenaAttendanceStateRegistered,
	}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("ChangeAttendance(locked) error = %v, want conflict", err)
	}
}

type attendanceRepositoryFake struct {
	rosterID     uuid.UUID
	participants map[uuid.UUID]arena.ParticipantRecord
	locked       bool
}

func newAttendanceRepositoryFake(rosterID uuid.UUID) *attendanceRepositoryFake {
	return &attendanceRepositoryFake{
		rosterID:     rosterID,
		participants: make(map[uuid.UUID]arena.ParticipantRecord),
	}
}

func (f *attendanceRepositoryFake) InviteParticipant(
	_ context.Context,
	in arena.ParticipantInput,
) (*arena.ParticipantRecord, bool, error) {
	if f.locked || in.RosterID != f.rosterID {
		return nil, false, domain.ErrConflict
	}
	for _, participant := range f.participants {
		if participant.PlayerID == in.PlayerID || participant.Seed == int(in.Seed) {
			return nil, false, domain.ErrConflict
		}
	}
	record := arena.ParticipantRecord{
		ID: in.ID, RosterID: in.RosterID, PlayerID: in.PlayerID, Seed: int(in.Seed),
		Attendance: in.Attendance, CreatedAt: in.CreatedAt, UpdatedAt: in.CreatedAt,
	}
	f.participants[record.ID] = record
	return cloneParticipantRecord(record), true, nil
}

func (f *attendanceRepositoryFake) ChangeAttendance(
	_ context.Context,
	participantID uuid.UUID,
	expected domain.ArenaAttendanceState,
	next domain.ArenaAttendanceState,
	updatedAt time.Time,
) (*arena.ParticipantRecord, bool, error) {
	if f.locked {
		return nil, false, domain.ErrConflict
	}
	record, exists := f.participants[participantID]
	if !exists || record.Attendance != expected {
		return nil, false, nil
	}
	record.Attendance = next
	record.UpdatedAt = updatedAt
	f.participants[participantID] = record
	return cloneParticipantRecord(record), true, nil
}

func (f *attendanceRepositoryFake) ReplaceWithdrawnParticipant(
	_ context.Context,
	in arena.ParticipantReplacementInput,
) (*arena.ParticipantRecord, bool, error) {
	if f.locked || in.RosterID != f.rosterID {
		return nil, false, domain.ErrConflict
	}
	current, exists := f.participants[in.WithdrawnParticipantID]
	if !exists || current.Attendance != domain.ArenaAttendanceStateWithdrawn {
		return nil, false, nil
	}
	for id, participant := range f.participants {
		if id != current.ID && participant.PlayerID == in.ReplacementPlayerID {
			return nil, false, domain.ErrConflict
		}
	}
	delete(f.participants, current.ID)
	current.ID = in.ReplacementParticipantID
	current.PlayerID = in.ReplacementPlayerID
	current.Attendance = domain.ArenaAttendanceStateInvited
	current.UpdatedAt = in.ReplacedAt
	f.participants[current.ID] = current
	return cloneParticipantRecord(current), true, nil
}

func (f *attendanceRepositoryFake) ListRosterParticipants(
	_ context.Context,
	rosterID uuid.UUID,
) ([]arena.ParticipantRecord, error) {
	if rosterID != f.rosterID {
		return nil, arena.ErrRosterNotFound
	}
	out := make([]arena.ParticipantRecord, 0, len(f.participants))
	for _, participant := range f.participants {
		out = append(out, participant)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seed < out[j].Seed })
	return out, nil
}

func cloneParticipantRecord(record arena.ParticipantRecord) *arena.ParticipantRecord {
	cloned := record
	return &cloned
}
