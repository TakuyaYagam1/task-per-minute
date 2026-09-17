package pause_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func testNormalPauseGraphEntryConflicts(t *testing.T, pausedAt time.Time) {
	t.Helper()

	t.Run("uses a second command lookup after locking authority", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		leaderRepository := newNormalPauseRepositoryHarness(t, authority)
		leader := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, pausedAt))
		stored, changed, err := leader.Enter(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored pause: error = %v, changed = %v", err, changed)
		}

		followerRepository := newNormalPauseRepositoryHarness(t, authority)
		followerRepository.blockNextLoad()
		follower := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), followerRepository, newPauseClock(t, pausedAt.Add(time.Second)))
		type result struct {
			record  *gameusecase.NormalPauseRecord
			changed bool
			err     error
		}
		resultCh := make(chan result, 1)
		go func() {
			record, changed, err := follower.Enter(context.Background(), command)
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

		authority, command := normalPauseFixture(pausedAt)
		repository := newNormalPauseRepositoryHarness(t, authority)
		repository.conflictsRemaining = 2
		transactions := newCountingTransactionManager(t)
		clock := newSequenceClock(t, pausedAt, pausedAt.Add(time.Second))
		useCase := gameusecase.NewNormalPauseGraphUseCase(transactions, repository, clock)
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseGraphConflict) || changed || repository.loadCount != 2 || transactions.count() != 2 || clock.count() != 2 {
			t.Fatalf("Enter() error = %v, changed = %v, loads = %d, transactions = %d, clock calls = %d", err, changed, repository.loadCount, transactions.count(), clock.count())
		}
	})

	t.Run("rejects a malformed stored command graph", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		leaderRepository := newNormalPauseRepositoryHarness(t, authority)
		leader := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, pausedAt))
		stored, changed, err := leader.Enter(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored pause: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Presence[0].ID = uuid.New()
		repository := newNormalPauseRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt.Add(time.Second)))
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})
}
