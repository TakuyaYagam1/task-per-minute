package pause_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func testNormalPauseGraphEntryStoredRecords(t *testing.T, pausedAt time.Time) {
	t.Helper()

	t.Run("rejects an unused cancelled root with zero suspension identity", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		leaderRepository := newNormalPauseRepositoryHarness(t, authority)
		leader := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, pausedAt))
		stored, changed, err := leader.Enter(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored pause: error = %v, changed = %v", err, changed)
		}
		zero := uuid.Nil
		stored.Graph.Reconnect[0].State = pausedomain.ReconnectStateCancelled
		stored.Graph.Reconnect[0].SuspendedByPauseID = &zero
		repository := newNormalPauseRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt.Add(time.Second)))
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects structurally valid stored pause records with wrong transitions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*gameusecase.NormalPauseRecord)
		}{
			{name: "Tournament did not pause", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.Graph.Tournament.State = domain.TournamentStateSwiss
				record.Graph.Tournament.PausedFromState = nil
			}},
			{name: "Tournament pause time was not published", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.Graph.Tournament.UpdatedAt = record.PausedAt.Add(-time.Second)
			}},
			{name: "Tournament paused from another state", mutate: func(record *gameusecase.NormalPauseRecord) {
				state := domain.TournamentStatePlayoffs
				record.Graph.Tournament.PausedFromState = &state
			}},
			{name: "Wave state changed without revision", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.Graph.Wave.Revision = record.Expected.WaveRevision
			}},
			{name: "Series state changed without revision", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.Graph.Series[0].Revision = record.Expected.Series[0].Revision
			}},
			{name: "Game state changed without revision", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.Graph.Games[0].Revision = record.Expected.Games[0].Revision
			}},
			{name: "open Reconnect was captured", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.Graph.Reconnect[0].State = pausedomain.ReconnectStateOpen
				record.Graph.Reconnect[0].ClosedAt = nil
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := normalPauseFixture(pausedAt)
				leaderRepository := newNormalPauseRepositoryHarness(t, authority)
				leader := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, pausedAt))
				stored, changed, err := leader.Enter(t.Context(), command)
				if err != nil || !changed {
					t.Fatalf("prepare stored pause: error = %v, changed = %v", err, changed)
				}
				test.mutate(stored)
				repository := newNormalPauseRepositoryHarness(t, authority)
				repository.storeCommand(*stored)
				useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt.Add(time.Second)))
				if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseCommandReuse) || changed {
					t.Fatalf("Enter() error = %v, changed = %v", err, changed)
				}
			})
		}
	})

	t.Run("rejects malformed stored reconnect suspension evidence", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*gameusecase.NormalPauseRecord)
		}{
			{name: "missing suspension", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.SuspendedReconnect = nil
			}},
			{name: "wrong source revision", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.SuspendedReconnect[0].Revision++
			}},
			{name: "duplicate suspension", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.SuspendedReconnect = append(record.SuspendedReconnect, record.SuspendedReconnect[0])
			}},
			{name: "wrong cancellation time", mutate: func(record *gameusecase.NormalPauseRecord) {
				closedAt := record.PausedAt.Add(time.Second)
				record.Graph.Reconnect[0].ClosedAt = &closedAt
				record.Graph.Reconnect[0].UpdatedAt = closedAt
			}},
			{name: "source deadline reached at pause", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.Graph.Reconnect[0].Deadline = record.PausedAt
			}},
			{name: "source opened at pause", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.Graph.Reconnect[0].OpenedAt = record.PausedAt
			}},
			{name: "current pause provenance is omitted at unchanged revision", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.SuspendedReconnect = nil
				record.Graph.Reconnect[0].Revision--
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := normalPauseFixture(pausedAt)
				makeReconnectOpenBeforePause(&authority, &command, pausedAt, 40*time.Second)
				leaderRepository := newNormalPauseRepositoryHarness(t, authority)
				leader := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, pausedAt))
				stored, changed, err := leader.Enter(t.Context(), command)
				if err != nil || !changed {
					t.Fatalf("prepare stored pause: error = %v, changed = %v", err, changed)
				}
				test.mutate(stored)
				repository := newNormalPauseRepositoryHarness(t, authority)
				repository.storeCommand(*stored)
				useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt.Add(time.Second)))
				if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseCommandReuse) || changed || repository.writeCount() != 0 {
					t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})
}
