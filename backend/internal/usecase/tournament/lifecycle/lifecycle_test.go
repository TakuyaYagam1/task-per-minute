package lifecycle_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	lifecycleusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle"
	tournamentmocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/tournament/lifecycle/mocks"
)

func TestLifecycleActiveSlot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 21, 0, 0, 0, time.UTC)
	t.Run("approved matrix", func(t *testing.T) {
		t.Parallel()

		transitions := []struct {
			from domain.TournamentState
			to   domain.TournamentState
		}{
			{domain.TournamentStateDraft, domain.TournamentStateRegistration},
			{domain.TournamentStateRegistration, domain.TournamentStateRosterLocked},
			{domain.TournamentStateRosterLocked, domain.TournamentStateRegistration},
			{domain.TournamentStateRosterLocked, domain.TournamentStateSwiss},
			{domain.TournamentStateSwiss, domain.TournamentStateGolden},
			{domain.TournamentStateSwiss, domain.TournamentStatePlayoffs},
			{domain.TournamentStateGolden, domain.TournamentStatePlayoffs},
			{domain.TournamentStatePlayoffs, domain.TournamentStateCompleted},
		}
		for index, transition := range transitions {
			t.Run(transition.from.String()+" to "+transition.to.String(), func(t *testing.T) {
				t.Parallel()

				id := uuid.MustParse(lifecycleTournamentIDs[index])
				record := lifecycleLifecycleTournamentRecord(id, transition.from, 4, now)
				repository := newLifecycleRepository(t, 1, 1, record)
				useCase := lifecycleusecase.NewTournamentLifecycleUseCase(
					repository,
					lifecycleNewFixedTournamentClock(t, now, 1),
				)
				updated, changed, err := useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
					TournamentID: id, ExpectedRevision: record.Revision, NextState: transition.to,
				})
				if err != nil || !changed {
					t.Fatalf("Transition() error = %v, changed = %v", err, changed)
				}
				if updated.State != transition.to || updated.Revision != record.Revision+1 {
					t.Fatalf("Transition() record = %+v", *updated)
				}
				if transition.to == domain.TournamentStateCompleted && updated.FinishedAt == nil {
					t.Fatal("completed tournament has no finished timestamp")
				}
			})
		}
	})

	t.Run("normalizes transition time to postgres precision", func(t *testing.T) {
		t.Parallel()

		id := uuid.MustParse("41000000-0000-0000-0000-000000000109")
		nanosecondTime := time.Date(2026, time.August, 28, 21, 0, 0, 123456789, time.UTC)
		record := lifecycleLifecycleTournamentRecord(id, domain.TournamentStateDraft, 1, nanosecondTime)
		repository := newLifecycleRepository(t, 1, 1, record)
		useCase := lifecycleusecase.NewTournamentLifecycleUseCase(
			repository,
			lifecycleNewFixedTournamentClock(t, nanosecondTime, 1),
		)

		updated, changed, err := useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
			TournamentID: id, ExpectedRevision: 1, NextState: domain.TournamentStateRegistration,
		})
		if err != nil || !changed {
			t.Fatalf("Transition() error = %v, changed = %v", err, changed)
		}
		wantUpdatedAt := time.Date(2026, time.August, 28, 21, 0, 0, 123456000, time.UTC)
		if !updated.UpdatedAt.Equal(wantUpdatedAt) {
			t.Fatalf("Transition() updated_at = %s, want %s", updated.UpdatedAt, wantUpdatedAt)
		}
	})

	t.Run("one concurrent owner and release", func(t *testing.T) {
		t.Parallel()

		firstID := uuid.MustParse("41000000-0000-0000-0000-000000000001")
		secondID := uuid.MustParse("41000000-0000-0000-0000-000000000002")
		repository := newLifecycleRepository(
			t,
			6,
			5,
			lifecycleLifecycleTournamentRecord(firstID, domain.TournamentStateRosterLocked, 3, now),
			lifecycleLifecycleTournamentRecord(secondID, domain.TournamentStateRosterLocked, 3, now),
		)
		useCase := lifecycleusecase.NewTournamentLifecycleUseCase(
			repository,
			lifecycleNewFixedTournamentClock(t, now, 5),
		)
		type result struct {
			id      uuid.UUID
			record  *lifecycleusecase.LifecycleTournamentRecord
			changed bool
			err     error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		for _, id := range []uuid.UUID{firstID, secondID} {
			go func() {
				<-start
				record, changed, err := useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
					TournamentID: id, ExpectedRevision: 3, NextState: domain.TournamentStateSwiss,
				})
				results <- result{id: id, record: record, changed: changed, err: err}
			}()
		}
		close(start)
		firstResult, secondResult := <-results, <-results
		var winner, loser result
		for _, candidate := range []result{firstResult, secondResult} {
			if candidate.changed {
				winner = candidate
			} else {
				loser = candidate
			}
		}
		if winner.record == nil || winner.err != nil || winner.record.State != domain.TournamentStateSwiss {
			t.Fatalf("winner = %+v", winner)
		}
		if !errors.Is(loser.err, domain.ErrConflict) || loser.changed {
			t.Fatalf("loser = %+v, want conflict", loser)
		}

		retried, changed, err := useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
			TournamentID: winner.id, ExpectedRevision: 3, NextState: domain.TournamentStateSwiss,
		})
		if err != nil || changed || retried == nil || retried.ID != winner.id {
			t.Fatalf("retry error = %v, changed = %v, record = %+v", err, changed, retried)
		}

		playoffs, changed, err := useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
			TournamentID: winner.id, ExpectedRevision: 4, NextState: domain.TournamentStatePlayoffs,
		})
		if err != nil || !changed {
			t.Fatalf("playoffs error = %v, changed = %v", err, changed)
		}
		_, changed, err = useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
			TournamentID: winner.id, ExpectedRevision: playoffs.Revision,
			NextState: domain.TournamentStateCompleted,
		})
		if err != nil || !changed {
			t.Fatalf("complete error = %v, changed = %v", err, changed)
		}
		started, changed, err := useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
			TournamentID: loser.id, ExpectedRevision: 3, NextState: domain.TournamentStateSwiss,
		})
		if err != nil || !changed || started == nil {
			t.Fatalf("start after release error = %v, changed = %v", err, changed)
		}
	})

	t.Run("guarded and illegal transitions", func(t *testing.T) {
		t.Parallel()

		id := uuid.MustParse("41000000-0000-0000-0000-000000000010")
		record := lifecycleLifecycleTournamentRecord(id, domain.TournamentStateSwiss, 2, now)
		repository := newLifecycleRepository(t, 3, 0, record)
		useCase := lifecycleusecase.NewTournamentLifecycleUseCase(
			repository,
			lifecycleNewFixedTournamentClock(t, now, 0),
		)
		for _, next := range []domain.TournamentState{
			domain.TournamentStateTechnicalPause,
			domain.TournamentStateCancelled,
		} {
			if _, changed, err := useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
				TournamentID: id, ExpectedRevision: 2, NextState: next,
			}); !errors.Is(err, lifecycleusecase.ErrTournamentGuardedTransition) || changed {
				t.Fatalf("guarded transition to %s error = %v, changed = %v", next, err, changed)
			}
		}
		if _, changed, err := useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
			TournamentID: id, ExpectedRevision: 2, NextState: domain.TournamentStateRegistration,
		}); !errors.Is(err, domain.ErrTournamentTransition) || changed {
			t.Fatalf("illegal transition error = %v, changed = %v", err, changed)
		}
	})
}

var lifecycleTournamentIDs = []string{
	"41000000-0000-0000-0000-000000000101",
	"41000000-0000-0000-0000-000000000102",
	"41000000-0000-0000-0000-000000000103",
	"41000000-0000-0000-0000-000000000104",
	"41000000-0000-0000-0000-000000000105",
	"41000000-0000-0000-0000-000000000106",
	"41000000-0000-0000-0000-000000000107",
	"41000000-0000-0000-0000-000000000108",
}

type lifecycleRepositoryState struct {
	mu      sync.Mutex
	records map[uuid.UUID]lifecycleusecase.LifecycleTournamentRecord
	active  *uuid.UUID
}

func TestLifecycleActiveSlotConflictPreservesSpecificError(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 21, 0, 0, 0, time.UTC)
	id := uuid.MustParse("41000000-0000-0000-0000-000000000110")
	record := lifecycleLifecycleTournamentRecord(id, domain.TournamentStateRosterLocked, 3, now)
	repository := tournamentmocks.NewMockTournamentLifecycleRepository(t)
	repository.EXPECT().GetTournament(mock.Anything, id).Return(lifecycleCloneTournamentRecord(record), nil)
	repository.EXPECT().TransitionTournament(mock.Anything, mock.Anything).
		Return(nil, false, errors.Join(lifecycleusecase.ErrActiveTournamentConflict, domain.ErrConflict))
	clock := lifecycleNewFixedTournamentClock(t, now, 1)
	useCase := lifecycleusecase.NewTournamentLifecycleUseCase(repository, clock)

	updated, changed, err := useCase.Transition(t.Context(), lifecycleusecase.TournamentLifecycleCommand{
		TournamentID: id, ExpectedRevision: record.Revision, NextState: domain.TournamentStateSwiss,
	})

	require.Nil(t, updated)
	require.False(t, changed)
	require.ErrorIs(t, err, lifecycleusecase.ErrActiveTournamentConflict)
	require.ErrorIs(t, err, domain.ErrConflict)
}

func newLifecycleRepository(
	t *testing.T,
	getCalls int,
	transitionCalls int,
	records ...lifecycleusecase.LifecycleTournamentRecord,
) *tournamentmocks.MockTournamentLifecycleRepository {
	t.Helper()
	state := &lifecycleRepositoryState{
		records: make(map[uuid.UUID]lifecycleusecase.LifecycleTournamentRecord, len(records)),
	}
	for _, record := range records {
		state.records[record.ID] = *lifecycleCloneTournamentRecord(record)
		if lifecycleStateIsActive(record.State) {
			id := record.ID
			state.active = &id
		}
	}
	repository := tournamentmocks.NewMockTournamentLifecycleRepository(t)
	if getCalls > 0 {
		repository.EXPECT().
			GetTournament(mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, id uuid.UUID) (*lifecycleusecase.LifecycleTournamentRecord, error) {
				state.mu.Lock()
				defer state.mu.Unlock()
				record, ok := state.records[id]
				if !ok {
					return nil, lifecycleusecase.ErrTournamentNotFound
				}
				return lifecycleCloneTournamentRecord(record), nil
			}).
			Times(getCalls)
	}
	if transitionCalls > 0 {
		repository.EXPECT().
			TransitionTournament(mock.Anything, mock.Anything).
			RunAndReturn(func(
				_ context.Context,
				in lifecycleusecase.TournamentLifecycleTransitionInput,
			) (*lifecycleusecase.LifecycleTournamentRecord, bool, error) {
				state.mu.Lock()
				defer state.mu.Unlock()
				record, ok := state.records[in.TournamentID]
				if !ok {
					return nil, false, lifecycleusecase.ErrTournamentNotFound
				}
				if record.Revision != in.ExpectedRevision || record.State != in.ExpectedState {
					return nil, false, nil
				}
				if lifecycleStateIsActive(in.NextState) && !lifecycleStateIsActive(record.State) &&
					state.active != nil && *state.active != record.ID {
					return nil, false, domain.ErrConflict
				}
				if lifecycleStateIsActive(in.NextState) {
					id := record.ID
					state.active = &id
				} else if lifecycleStateIsActive(record.State) &&
					state.active != nil && *state.active == record.ID {
					state.active = nil
				}
				record.State = in.NextState
				record.PausedFromState = cloneStatePointer(in.PausedFromState)
				record.Revision++
				record.UpdatedAt = in.TransitionedAt
				record.StartedAt = lifecycleCloneTestTimePointer(in.StartedAt)
				record.FinishedAt = lifecycleCloneTestTimePointer(in.FinishedAt)
				state.records[record.ID] = record
				return lifecycleCloneTournamentRecord(record), true, nil
			}).
			Times(transitionCalls)
	}
	return repository
}

func lifecycleLifecycleTournamentRecord(
	id uuid.UUID,
	state domain.TournamentState,
	revision int64,
	now time.Time,
) lifecycleusecase.LifecycleTournamentRecord {
	record := lifecycleusecase.LifecycleTournamentRecord{
		ID: id, State: state, Revision: revision, UpdatedAt: now.Add(-time.Hour),
	}
	if lifecycleStateIsActive(state) || state == domain.TournamentStateCompleted {
		startedAt := now.Add(-90 * time.Minute)
		record.StartedAt = &startedAt
	}
	if state == domain.TournamentStateTechnicalPause {
		origin := domain.TournamentStateSwiss
		record.PausedFromState = &origin
	}
	if state == domain.TournamentStateCompleted {
		finishedAt := now.Add(-30 * time.Minute)
		record.FinishedAt = &finishedAt
	}
	return record
}

func lifecycleStateIsActive(state domain.TournamentState) bool {
	switch state {
	case domain.TournamentStateSwiss,
		domain.TournamentStateGolden,
		domain.TournamentStatePlayoffs,
		domain.TournamentStateTechnicalPause:
		return true
	case domain.TournamentStateDraft,
		domain.TournamentStateRegistration,
		domain.TournamentStateRosterLocked,
		domain.TournamentStateCompleted,
		domain.TournamentStateCancelled:
		return false
	}
	return false
}

func cloneStatePointer(value *domain.TournamentState) *domain.TournamentState {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func lifecycleCloneTestTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
