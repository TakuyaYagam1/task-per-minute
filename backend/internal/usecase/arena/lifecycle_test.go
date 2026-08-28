package arena_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestLifecycleActiveSlot(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.August, 28, 21, 0, 0, 0, time.UTC)
	t.Run("approved matrix", func(t *testing.T) {
		t.Parallel()

		transitions := []struct {
			from domain.ArenaTournamentState
			to   domain.ArenaTournamentState
		}{
			{domain.ArenaTournamentStateDraft, domain.ArenaTournamentStateRegistration},
			{domain.ArenaTournamentStateRegistration, domain.ArenaTournamentStateRosterLocked},
			{domain.ArenaTournamentStateRosterLocked, domain.ArenaTournamentStateRegistration},
			{domain.ArenaTournamentStateRosterLocked, domain.ArenaTournamentStateSwiss},
			{domain.ArenaTournamentStateSwiss, domain.ArenaTournamentStateGolden},
			{domain.ArenaTournamentStateSwiss, domain.ArenaTournamentStatePlayoffs},
			{domain.ArenaTournamentStateGolden, domain.ArenaTournamentStatePlayoffs},
			{domain.ArenaTournamentStatePlayoffs, domain.ArenaTournamentStateCompleted},
		}
		for index, transition := range transitions {
			t.Run(transition.from.String()+" to "+transition.to.String(), func(t *testing.T) {
				t.Parallel()

				id := uuid.MustParse(lifecycleTournamentIDs[index])
				record := lifecycleTournamentRecord(id, transition.from, 4, now)
				repo := newLifecycleRepositoryFake(record)
				useCase := arena.NewTournamentLifecycleUseCase(repo, fixedArenaClock{now: now})
				updated, changed, err := useCase.Transition(t.Context(), arena.TournamentLifecycleCommand{
					TournamentID: id, ExpectedRevision: record.Revision, NextState: transition.to,
				})
				if err != nil || !changed {
					t.Fatalf("Transition() error = %v, changed = %v", err, changed)
				}
				if updated.State != transition.to || updated.Revision != record.Revision+1 {
					t.Fatalf("Transition() record = %+v", *updated)
				}
				if transition.to == domain.ArenaTournamentStateCompleted && updated.FinishedAt == nil {
					t.Fatal("completed tournament has no finished timestamp")
				}
			})
		}
	})

	t.Run("one concurrent owner and release", func(t *testing.T) {
		t.Parallel()

		firstID := uuid.MustParse("41000000-0000-0000-0000-000000000001")
		secondID := uuid.MustParse("41000000-0000-0000-0000-000000000002")
		repo := newLifecycleRepositoryFake(
			lifecycleTournamentRecord(firstID, domain.ArenaTournamentStateRosterLocked, 3, now),
			lifecycleTournamentRecord(secondID, domain.ArenaTournamentStateRosterLocked, 3, now),
		)
		useCase := arena.NewTournamentLifecycleUseCase(repo, fixedArenaClock{now: now})
		type result struct {
			id      uuid.UUID
			record  *arena.TournamentRecord
			changed bool
			err     error
		}
		start := make(chan struct{})
		results := make(chan result, 2)
		for _, id := range []uuid.UUID{firstID, secondID} {
			go func() {
				<-start
				record, changed, err := useCase.Transition(t.Context(), arena.TournamentLifecycleCommand{
					TournamentID: id, ExpectedRevision: 3, NextState: domain.ArenaTournamentStateSwiss,
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
		if winner.record == nil || winner.err != nil || winner.record.State != domain.ArenaTournamentStateSwiss {
			t.Fatalf("winner = %+v", winner)
		}
		if !errors.Is(loser.err, domain.ErrConflict) || loser.changed {
			t.Fatalf("loser = %+v, want conflict", loser)
		}

		retried, changed, err := useCase.Transition(t.Context(), arena.TournamentLifecycleCommand{
			TournamentID: winner.id, ExpectedRevision: 3, NextState: domain.ArenaTournamentStateSwiss,
		})
		if err != nil || changed || retried == nil || retried.ID != winner.id {
			t.Fatalf("retry error = %v, changed = %v, record = %+v", err, changed, retried)
		}

		playoffs, changed, err := useCase.Transition(t.Context(), arena.TournamentLifecycleCommand{
			TournamentID: winner.id, ExpectedRevision: 4, NextState: domain.ArenaTournamentStatePlayoffs,
		})
		if err != nil || !changed {
			t.Fatalf("playoffs error = %v, changed = %v", err, changed)
		}
		_, changed, err = useCase.Transition(t.Context(), arena.TournamentLifecycleCommand{
			TournamentID: winner.id, ExpectedRevision: playoffs.Revision,
			NextState: domain.ArenaTournamentStateCompleted,
		})
		if err != nil || !changed {
			t.Fatalf("complete error = %v, changed = %v", err, changed)
		}
		started, changed, err := useCase.Transition(t.Context(), arena.TournamentLifecycleCommand{
			TournamentID: loser.id, ExpectedRevision: 3, NextState: domain.ArenaTournamentStateSwiss,
		})
		if err != nil || !changed || started == nil {
			t.Fatalf("start after release error = %v, changed = %v", err, changed)
		}
	})

	t.Run("guarded and illegal transitions", func(t *testing.T) {
		t.Parallel()

		id := uuid.MustParse("41000000-0000-0000-0000-000000000010")
		record := lifecycleTournamentRecord(id, domain.ArenaTournamentStateSwiss, 2, now)
		repo := newLifecycleRepositoryFake(record)
		useCase := arena.NewTournamentLifecycleUseCase(repo, fixedArenaClock{now: now})
		for _, next := range []domain.ArenaTournamentState{
			domain.ArenaTournamentStateTechnicalPause,
			domain.ArenaTournamentStateCancelled,
		} {
			if _, changed, err := useCase.Transition(t.Context(), arena.TournamentLifecycleCommand{
				TournamentID: id, ExpectedRevision: 2, NextState: next,
			}); !errors.Is(err, arena.ErrTournamentGuardedTransition) || changed {
				t.Fatalf("guarded transition to %s error = %v, changed = %v", next, err, changed)
			}
		}
		if _, changed, err := useCase.Transition(t.Context(), arena.TournamentLifecycleCommand{
			TournamentID: id, ExpectedRevision: 2, NextState: domain.ArenaTournamentStateRegistration,
		}); !errors.Is(err, domain.ErrArenaTournamentTransition) || changed {
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

type lifecycleRepositoryFake struct {
	mu      sync.Mutex
	records map[uuid.UUID]arena.TournamentRecord
	active  *uuid.UUID
}

func newLifecycleRepositoryFake(records ...arena.TournamentRecord) *lifecycleRepositoryFake {
	fake := &lifecycleRepositoryFake{records: make(map[uuid.UUID]arena.TournamentRecord, len(records))}
	for _, record := range records {
		fake.records[record.ID] = *cloneTournamentRecord(record)
		if lifecycleStateIsActive(record.State) {
			id := record.ID
			fake.active = &id
		}
	}
	return fake
}

func (f *lifecycleRepositoryFake) GetTournament(
	_ context.Context,
	id uuid.UUID,
) (*arena.TournamentRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.records[id]
	if !ok {
		return nil, arena.ErrTournamentNotFound
	}
	return cloneTournamentRecord(record), nil
}

func (f *lifecycleRepositoryFake) TransitionTournament(
	_ context.Context,
	in arena.TournamentLifecycleTransitionInput,
) (*arena.TournamentRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.records[in.TournamentID]
	if !ok {
		return nil, false, arena.ErrTournamentNotFound
	}
	if record.Revision != in.ExpectedRevision || record.State != in.ExpectedState {
		return nil, false, nil
	}
	if lifecycleStateIsActive(in.NextState) && !lifecycleStateIsActive(record.State) &&
		f.active != nil && *f.active != record.ID {
		return nil, false, domain.ErrConflict
	}
	if lifecycleStateIsActive(in.NextState) {
		id := record.ID
		f.active = &id
	} else if lifecycleStateIsActive(record.State) && f.active != nil && *f.active == record.ID {
		f.active = nil
	}
	record.State = in.NextState
	record.PausedFromState = cloneArenaStatePointer(in.PausedFromState)
	record.Revision++
	record.UpdatedAt = in.TransitionedAt
	record.StartedAt = cloneTestTimePointer(in.StartedAt)
	record.FinishedAt = cloneTestTimePointer(in.FinishedAt)
	f.records[record.ID] = record
	return cloneTournamentRecord(record), true, nil
}

func lifecycleTournamentRecord(
	id uuid.UUID,
	state domain.ArenaTournamentState,
	revision int64,
	now time.Time,
) arena.TournamentRecord {
	createdAt := now.Add(-2 * time.Hour)
	record := arena.TournamentRecord{
		ID: id, RosterID: uuid.New(), Preset: domain.ArenaPresetV1, State: state,
		Revision: revision, RosterSize: 4, CreatedAt: createdAt, UpdatedAt: now.Add(-time.Hour),
	}
	if lifecycleStateIsActive(state) || state == domain.ArenaTournamentStateCompleted {
		startedAt := now.Add(-90 * time.Minute)
		record.StartedAt = &startedAt
	}
	if state == domain.ArenaTournamentStateTechnicalPause {
		origin := domain.ArenaTournamentStateSwiss
		record.PausedFromState = &origin
	}
	if state == domain.ArenaTournamentStateCompleted {
		finishedAt := now.Add(-30 * time.Minute)
		record.FinishedAt = &finishedAt
	}
	return record
}

func lifecycleStateIsActive(state domain.ArenaTournamentState) bool {
	switch state {
	case domain.ArenaTournamentStateSwiss,
		domain.ArenaTournamentStateGolden,
		domain.ArenaTournamentStatePlayoffs,
		domain.ArenaTournamentStateTechnicalPause:
		return true
	case domain.ArenaTournamentStateDraft,
		domain.ArenaTournamentStateRegistration,
		domain.ArenaTournamentStateRosterLocked,
		domain.ArenaTournamentStateCompleted,
		domain.ArenaTournamentStateCancelled:
		return false
	}
	return false
}

func cloneArenaStatePointer(value *domain.ArenaTournamentState) *domain.ArenaTournamentState {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

func cloneTestTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
