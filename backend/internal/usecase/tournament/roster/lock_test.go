package roster_test

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	rosterusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/roster/mocks"
)

func TestRosterLockUnlock(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 20, 0, 0, 0, time.UTC)
	rosterID := uuid.MustParse("30000000-0000-0000-0000-000000000002")
	playerIDs := []uuid.UUID{
		uuid.MustParse("30000000-0000-0000-0000-000000000013"),
		uuid.MustParse("30000000-0000-0000-0000-000000000011"),
		uuid.MustParse("30000000-0000-0000-0000-000000000014"),
		uuid.MustParse("30000000-0000-0000-0000-000000000012"),
	}
	state, repository := newRosterLockRepository(
		t,
		rosterID,
		playerIDs,
		rosterLockRepositoryCalls{get: 4, lock: 3, unlock: 1},
	)
	useCase := rosterusecase.NewRosterLockUseCase(repository, rosterNewFixedTournamentClock(t, now, 4))
	evidence := rosterusecase.RosterPreflightEvidence{
		RosterID: rosterID, RosterRevision: 1, CheckedInPlayerIDs: playerIDs, Approved: true,
	}

	locked, changed, err := useCase.LockRoster(t.Context(), rosterusecase.RosterLockCommand{Preflight: evidence})
	if err != nil || !changed {
		t.Fatalf("LockRoster() error = %v, changed = %v", err, changed)
	}
	if locked.Revision != 2 || locked.LockedAt == nil || !locked.LockedAt.Equal(now) {
		t.Fatalf("locked roster = %+v", *locked)
	}
	if len(state.reservations) != len(playerIDs) {
		t.Fatalf("reservation count = %d, want %d", len(state.reservations), len(playerIDs))
	}
	if !slices.IsSortedFunc(state.lastExpectedPlayers, compareUUID) {
		t.Fatalf("preflight player evidence was not canonical: %v", state.lastExpectedPlayers)
	}
	if _, repeated, repeatErr := useCase.LockRoster(
		t.Context(), rosterusecase.RosterLockCommand{Preflight: evidence},
	); !errors.Is(repeatErr, domain.ErrConflict) || repeated {
		t.Fatalf("LockRoster(repeated evidence) error = %v, changed = %v, want conflict", repeatErr, repeated)
	}

	unlocked, changed, err := useCase.UnlockRoster(t.Context(), rosterusecase.RosterUnlockCommand{
		RosterID: rosterID, ExpectedRevision: locked.Revision,
		ActorID: uuid.MustParse("30000000-0000-0000-0000-000000000020"), Reason: "participant replacement",
	})
	if err != nil || !changed {
		t.Fatalf("UnlockRoster() error = %v, changed = %v", err, changed)
	}
	if unlocked.Revision != 3 || unlocked.LockedAt != nil {
		t.Fatalf("unlocked roster = %+v", *unlocked)
	}
	if len(state.reservations) != 0 {
		t.Fatalf("reservation count after unlock = %d, want 0", len(state.reservations))
	}
	if _, _, err = useCase.LockRoster(t.Context(), rosterusecase.RosterLockCommand{Preflight: evidence}); !errors.Is(err, domain.ErrConflict) {
		t.Fatalf("LockRoster(stale preflight) error = %v, want conflict", err)
	}

	state.roster.LockedAt = timePointer(now.Add(time.Minute))
	state.roster.ExecutionStartedAt = timePointer(now.Add(2 * time.Minute))
	state.roster.Revision = 4
	if _, _, err = useCase.UnlockRoster(t.Context(), rosterusecase.RosterUnlockCommand{
		RosterID: rosterID, ExpectedRevision: 4,
		ActorID: uuid.MustParse("30000000-0000-0000-0000-000000000020"), Reason: "too late",
	}); !errors.Is(err, domain.ErrRosterExecutionStarted) {
		t.Fatalf("UnlockRoster(started) error = %v, want ErrTournamentRosterExecutionStarted", err)
	}

	conflictState, conflictRepository := newRosterLockRepository(
		t,
		rosterID,
		playerIDs,
		rosterLockRepositoryCalls{lock: 1},
	)
	conflictState.reservationConflict = true
	conflictUseCase := rosterusecase.NewRosterLockUseCase(
		conflictRepository,
		rosterNewFixedTournamentClock(t, now, 1),
	)
	if _, changed, err = conflictUseCase.LockRoster(t.Context(), rosterusecase.RosterLockCommand{Preflight: evidence}); !errors.Is(err, domain.ErrConflict) || changed {
		t.Fatalf("LockRoster(reservation conflict) error = %v, changed = %v, want conflict", err, changed)
	}
	if conflictState.roster.LockedAt != nil || len(conflictState.reservations) != 0 {
		t.Fatalf("reservation conflict leaked state: roster = %+v, reservations = %v",
			conflictState.roster, conflictState.reservations)
	}
}

type rosterLockRepositoryState struct {
	roster              rosterusecase.RosterRosterRecord
	checkedInPlayers    []uuid.UUID
	reservations        map[uuid.UUID]struct{}
	lastExpectedPlayers []uuid.UUID
	reservationConflict bool
}

type rosterLockRepositoryCalls struct {
	get    int
	lock   int
	unlock int
}

func newRosterLockRepository(
	t *testing.T,
	rosterID uuid.UUID,
	checkedInPlayers []uuid.UUID,
	calls rosterLockRepositoryCalls,
) (*rosterLockRepositoryState, *tournamentmocks.MockRosterLockRepository) {
	t.Helper()
	state := &rosterLockRepositoryState{
		roster: rosterusecase.RosterRosterRecord{
			ID: rosterID, Revision: 1,
		},
		checkedInPlayers: append([]uuid.UUID(nil), checkedInPlayers...),
		reservations:     make(map[uuid.UUID]struct{}),
	}
	repository := tournamentmocks.NewMockRosterLockRepository(t)
	if calls.get > 0 {
		repository.EXPECT().
			GetRosterSnapshot(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, id uuid.UUID) (*rosterusecase.RosterRosterRecord, error) {
				if id != state.roster.ID {
					return nil, rosterusecase.ErrRosterNotFound
				}
				return cloneRosterRecord(state.roster), nil
			}).
			Times(calls.get)
	}
	if calls.lock > 0 {
		repository.EXPECT().
			LockRosterAndReserveExpected(
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
			).
			RunAndReturn(func(
				_ context.Context,
				rosterID uuid.UUID,
				expectedRevision int64,
				expectedPlayerIDs []uuid.UUID,
				lockedAt time.Time,
			) (*rosterusecase.RosterRosterRecord, bool, error) {
				state.lastExpectedPlayers = append([]uuid.UUID(nil), expectedPlayerIDs...)
				wantPlayers := append([]uuid.UUID(nil), state.checkedInPlayers...)
				slices.SortFunc(wantPlayers, compareUUID)
				if rosterID != state.roster.ID || state.roster.Revision != expectedRevision ||
					state.roster.LockedAt != nil || !slices.Equal(wantPlayers, expectedPlayerIDs) {
					return nil, false, nil
				}
				if state.reservationConflict {
					return nil, false, domain.ErrConflict
				}
				for _, playerID := range expectedPlayerIDs {
					state.reservations[playerID] = struct{}{}
				}
				state.roster.Revision++
				state.roster.LockedAt = timePointer(lockedAt)
				return cloneRosterRecord(state.roster), true, nil
			}).
			Times(calls.lock)
	}
	if calls.unlock > 0 {
		repository.EXPECT().
			UnlockRosterAndReleaseExpected(
				mock.Anything,
				mock.Anything,
				mock.Anything,
				mock.Anything,
			).
			RunAndReturn(func(
				_ context.Context,
				rosterID uuid.UUID,
				expectedRevision int64,
				_ time.Time,
			) (*rosterusecase.RosterRosterRecord, bool, error) {
				if rosterID != state.roster.ID || state.roster.Revision != expectedRevision ||
					state.roster.LockedAt == nil || state.roster.ExecutionStartedAt != nil {
					return nil, false, nil
				}
				state.roster.Revision++
				state.roster.LockedAt = nil
				clear(state.reservations)
				return cloneRosterRecord(state.roster), true, nil
			}).
			Times(calls.unlock)
	}
	return state, repository
}

func cloneRosterRecord(record rosterusecase.RosterRosterRecord) *rosterusecase.RosterRosterRecord {
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
