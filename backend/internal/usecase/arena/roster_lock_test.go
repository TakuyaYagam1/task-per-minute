package arena_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestRosterLockUnlock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 20, 0, 0, 0, time.UTC)
	tournamentID := uuid.MustParse("30000000-0000-0000-0000-000000000001")
	rosterID := uuid.MustParse("30000000-0000-0000-0000-000000000002")
	playerIDs := []uuid.UUID{
		uuid.MustParse("30000000-0000-0000-0000-000000000013"),
		uuid.MustParse("30000000-0000-0000-0000-000000000011"),
		uuid.MustParse("30000000-0000-0000-0000-000000000014"),
		uuid.MustParse("30000000-0000-0000-0000-000000000012"),
	}
	repo := newRosterLockRepositoryFake(tournamentID, rosterID, playerIDs, now.Add(-time.Hour))
	useCase := arena.NewRosterLockUseCase(repo, fixedArenaClock{now: now})
	evidence := arena.RosterPreflightEvidence{
		RosterID: rosterID, RosterRevision: 1, CheckedInPlayerIDs: playerIDs, Approved: true,
	}

	locked, changed, err := useCase.LockRoster(t.Context(), arena.RosterLockCommand{Preflight: evidence})
	if err != nil || !changed {
		t.Fatalf("LockRoster() error = %v, changed = %v", err, changed)
	}
	if locked.Revision != 2 || locked.LockedAt == nil || !locked.LockedAt.Equal(now) {
		t.Fatalf("locked roster = %+v", *locked)
	}
	if len(repo.reservations) != len(playerIDs) {
		t.Fatalf("reservation count = %d, want %d", len(repo.reservations), len(playerIDs))
	}
	if !slices.IsSortedFunc(repo.lastExpectedPlayers, compareUUID) {
		t.Fatalf("preflight player evidence was not canonical: %v", repo.lastExpectedPlayers)
	}
	if _, repeated, repeatErr := useCase.LockRoster(
		t.Context(), arena.RosterLockCommand{Preflight: evidence},
	); !errors.Is(repeatErr, domain.ErrConflict) || repeated {
		t.Fatalf("LockRoster(repeated evidence) error = %v, changed = %v, want conflict", repeatErr, repeated)
	}

	unlocked, changed, err := useCase.UnlockRoster(t.Context(), arena.RosterUnlockCommand{
		RosterID: rosterID, ExpectedRevision: locked.Revision,
		ActorID: uuid.MustParse("30000000-0000-0000-0000-000000000020"), Reason: "participant replacement",
	})
	if err != nil || !changed {
		t.Fatalf("UnlockRoster() error = %v, changed = %v", err, changed)
	}
	if unlocked.Revision != 3 || unlocked.LockedAt != nil {
		t.Fatalf("unlocked roster = %+v", *unlocked)
	}
	if len(repo.reservations) != 0 {
		t.Fatalf("reservation count after unlock = %d, want 0", len(repo.reservations))
	}
	if _, _, err = useCase.LockRoster(t.Context(), arena.RosterLockCommand{Preflight: evidence}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("LockRoster(stale preflight) error = %v, want conflict", err)
	}

	repo.roster.LockedAt = timePointer(now.Add(time.Minute))
	repo.roster.ExecutionStartedAt = timePointer(now.Add(2 * time.Minute))
	repo.roster.Revision = 4
	if _, _, err = useCase.UnlockRoster(t.Context(), arena.RosterUnlockCommand{
		RosterID: rosterID, ExpectedRevision: 4,
		ActorID: uuid.MustParse("30000000-0000-0000-0000-000000000020"), Reason: "too late",
	}); !errors.Is(err, domain.ErrArenaRosterExecutionStarted) {
		t.Fatalf("UnlockRoster(started) error = %v, want ErrArenaRosterExecutionStarted", err)
	}

	conflictRepo := newRosterLockRepositoryFake(tournamentID, rosterID, playerIDs, now.Add(-time.Hour))
	conflictRepo.reservationConflict = true
	conflictUseCase := arena.NewRosterLockUseCase(conflictRepo, fixedArenaClock{now: now})
	if _, changed, err = conflictUseCase.LockRoster(t.Context(), arena.RosterLockCommand{Preflight: evidence}); !errors.Is(err, domain.ErrConflict) || changed {
		t.Fatalf("LockRoster(reservation conflict) error = %v, changed = %v, want conflict", err, changed)
	}
	if conflictRepo.roster.LockedAt != nil || len(conflictRepo.reservations) != 0 {
		t.Fatalf("reservation conflict leaked state: roster = %+v, reservations = %v",
			conflictRepo.roster, conflictRepo.reservations)
	}
}

type rosterLockRepositoryFake struct {
	roster              arena.RosterRecord
	checkedInPlayers    []uuid.UUID
	reservations        map[uuid.UUID]struct{}
	lastExpectedPlayers []uuid.UUID
	reservationConflict bool
}

func newRosterLockRepositoryFake(
	tournamentID uuid.UUID,
	rosterID uuid.UUID,
	checkedInPlayers []uuid.UUID,
	createdAt time.Time,
) *rosterLockRepositoryFake {
	return &rosterLockRepositoryFake{
		roster: arena.RosterRecord{
			ID: rosterID, TournamentID: tournamentID, Revision: 1, CreatedAt: createdAt, UpdatedAt: createdAt,
		},
		checkedInPlayers: append([]uuid.UUID(nil), checkedInPlayers...),
		reservations:     make(map[uuid.UUID]struct{}),
	}
}

func (f *rosterLockRepositoryFake) GetRosterSnapshot(_ context.Context, id uuid.UUID) (*arena.RosterRecord, error) {
	if id != f.roster.ID {
		return nil, arena.ErrRosterNotFound
	}
	return cloneRosterRecord(f.roster), nil
}

func (f *rosterLockRepositoryFake) LockRosterAndReserveExpected(
	_ context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	expectedPlayerIDs []uuid.UUID,
	lockedAt time.Time,
) (*arena.RosterRecord, bool, error) {
	f.lastExpectedPlayers = append([]uuid.UUID(nil), expectedPlayerIDs...)
	wantPlayers := append([]uuid.UUID(nil), f.checkedInPlayers...)
	slices.SortFunc(wantPlayers, compareUUID)
	if rosterID != f.roster.ID || f.roster.Revision != expectedRevision || f.roster.LockedAt != nil ||
		!slices.Equal(wantPlayers, expectedPlayerIDs) {
		return nil, false, nil
	}
	if f.reservationConflict {
		return nil, false, domain.ErrConflict
	}
	for _, playerID := range expectedPlayerIDs {
		f.reservations[playerID] = struct{}{}
	}
	f.roster.Revision++
	f.roster.LockedAt = timePointer(lockedAt)
	f.roster.UpdatedAt = lockedAt
	return cloneRosterRecord(f.roster), true, nil
}

func (f *rosterLockRepositoryFake) UnlockRosterAndReleaseExpected(
	_ context.Context,
	rosterID uuid.UUID,
	expectedRevision int64,
	updatedAt time.Time,
) (*arena.RosterRecord, bool, error) {
	if rosterID != f.roster.ID || f.roster.Revision != expectedRevision || f.roster.LockedAt == nil ||
		f.roster.ExecutionStartedAt != nil {
		return nil, false, nil
	}
	f.roster.Revision++
	f.roster.LockedAt = nil
	f.roster.UpdatedAt = updatedAt
	clear(f.reservations)
	return cloneRosterRecord(f.roster), true, nil
}

func cloneRosterRecord(record arena.RosterRecord) *arena.RosterRecord {
	cloned := record
	if record.LockedAt != nil {
		cloned.LockedAt = timePointer(*record.LockedAt)
	}
	if record.ExecutionStartedAt != nil {
		cloned.ExecutionStartedAt = timePointer(*record.ExecutionStartedAt)
	}
	return &cloned
}

func timePointer(value time.Time) *time.Time {
	return &value
}

func compareUUID(first, second uuid.UUID) int {
	return bytes.Compare(first[:], second[:])
}
