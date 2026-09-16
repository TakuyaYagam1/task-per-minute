package pause_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
)

func testPauseResumeConflicts(t *testing.T, resumedAt time.Time) {
	t.Helper()

	t.Run("uses a second command lookup after locking authority", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		leaderRepository := newPauseResumeRepositoryHarness(t, authority)
		leader := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, resumedAt))
		stored, changed, err := leader.Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored resume: error = %v, changed = %v", err, changed)
		}

		followerRepository := newPauseResumeRepositoryHarness(t, authority)
		followerRepository.blockNextLoad()
		follower := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), followerRepository, newPauseClock(t, resumedAt.Add(time.Second)))
		type result struct {
			record  *gameusecase.PauseResumeRecord
			changed bool
			err     error
		}
		resultCh := make(chan result, 1)
		go func() {
			record, changed, err := follower.Resume(context.Background(), command)
			resultCh <- result{record: record, changed: changed, err: err}
		}()
		<-followerRepository.loadStarted
		followerRepository.storeCommand(*stored)
		close(followerRepository.loadRelease)
		got := <-resultCh
		if got.err != nil || got.changed || !reflect.DeepEqual(got.record, stored) || followerRepository.commitCalls != 0 {
			t.Fatalf("follower error = %v, changed = %v, commit calls = %d, record = %+v", got.err, got.changed, followerRepository.commitCalls, got.record)
		}
	})

	t.Run("stops after two fresh CAS conflicts", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		repository := newPauseResumeRepositoryHarness(t, authority)
		repository.conflictsRemaining = 2
		transactions := newCountingTransactionManager(t)
		clock := newSequenceClock(t, resumedAt, resumedAt.Add(time.Second))
		useCase := gameusecase.NewPauseResumeUseCase(transactions, repository, clock)
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrPauseResumeConflict) || changed || repository.loadCount != 2 || transactions.count() != 2 || clock.count() != 2 {
			t.Fatalf("Resume() error = %v, changed = %v, loads = %d, transactions = %d, clock calls = %d", err, changed, repository.loadCount, transactions.count(), clock.count())
		}
	})

	t.Run("rejects a malformed stored resume graph", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		leaderRepository := newPauseResumeRepositoryHarness(t, authority)
		leader := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, resumedAt))
		stored, changed, err := leader.Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored resume: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Presence[0].PresenceEpoch++
		stored.Graph.Presence[0].Revision++
		stored.Graph.Presence[0].ConnectedAt = resumedAt
		stored.Graph.Presence[0].UpdatedAt = resumedAt
		repository := newPauseResumeRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase := gameusecase.NewPauseResumeUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, resumedAt.Add(time.Second)))
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, gameusecase.ErrPauseResumeCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})
}
