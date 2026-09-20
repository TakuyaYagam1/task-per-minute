package pause_test

import (
	"errors"
	"math"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func testNormalPauseGraphEntryValidation(t *testing.T, pausedAt time.Time) {
	t.Helper()

	t.Run("accepts a retained reconnect root suffix", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		authority.Graph.Counters[0].Used = 2
		authority.Graph.Reconnect[0].Number = 2
		refreshNormalPauseRevisions(&authority, &command)
		repository := newNormalPauseRepositoryHarness(t, authority)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
		if record, changed, err := useCase.Enter(t.Context(), command); err != nil || !changed || record == nil || repository.writeCount() != 1 {
			t.Fatalf("Enter() record = %v, error = %v, changed = %v, writes = %d", record != nil, err, changed, repository.writeCount())
		}
	})

	t.Run("rejects a gap in the retained reconnect root suffix", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		authority.Graph.Counters[0].Used = 3
		authority.Graph.Reconnect[0].Number = 2
		refreshNormalPauseRevisions(&authority, &command)
		repository := newNormalPauseRepositoryHarness(t, authority)
		useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, gameusecase.ErrNormalPauseGraphIncomplete) || changed || repository.writeCount() != 0 {
			t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects malformed stored continuation temporal lineage", func(t *testing.T) {
		t.Parallel()

		for _, test := range []struct {
			name   string
			mutate func(*gameusecase.NormalPauseRecord)
		}{
			{name: "source has no remaining time", mutate: func(record *gameusecase.NormalPauseRecord) {
				source := &record.Graph.Reconnect[0]
				source.Deadline = *source.ClosedAt
			}},
			{name: "source has zero suspension identity", mutate: func(record *gameusecase.NormalPauseRecord) {
				zero := uuid.Nil
				record.Graph.Reconnect[0].SuspendedByPauseID = &zero
			}},
			{name: "continuation opens at source close", mutate: func(record *gameusecase.NormalPauseRecord) {
				source := &record.Graph.Reconnect[0]
				record.Graph.Reconnect[1].OpenedAt = *source.ClosedAt
			}},
			{name: "continuation deadline does not preserve remaining time", mutate: func(record *gameusecase.NormalPauseRecord) {
				record.Graph.Reconnect[1].Deadline = record.Graph.Reconnect[1].Deadline.Add(time.Second)
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := normalPauseFixture(pausedAt)
				makeReconnectContinuationBeforePause(&authority, &command, pausedAt)
				leaderRepository := newNormalPauseRepositoryHarness(t, authority)
				leader := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, pausedAt))
				stored, changed, err := leader.Enter(t.Context(), command)
				if err != nil || !changed {
					t.Fatalf("prepare stored continuation: error = %v, changed = %v", err, changed)
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

	t.Run("prevalidates every descendant before the CAS", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*gameusecase.NormalPauseAuthority, *gameusecase.NormalPauseCommand)
			want   error
		}{
			{name: "partial graph", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) { a.Complete = false }, want: gameusecase.ErrNormalPauseGraphIncomplete},
			{name: "active Golden", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) { a.ActiveGolden = true }, want: gameusecase.ErrNormalPauseGoldenActive},
			{name: "stale Wave revision", mutate: func(_ *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				c.Expected.WaveRevision++
			}, want: gameusecase.ErrNormalPauseGraphConflict},
			{name: "wrong Tournament origin state", mutate: func(_ *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				c.Expected.TournamentState = domain.TournamentStatePlayoffs
			}, want: gameusecase.ErrNormalPauseGraphConflict},
			{name: "stale Series revision", mutate: func(_ *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				c.Expected.Series[0].Revision++
			}, want: gameusecase.ErrNormalPauseGraphConflict},
			{name: "stale reconnect counter revision", mutate: func(_ *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				c.Expected.Counters[0].Revision++
			}, want: gameusecase.ErrNormalPauseGraphConflict},
			{name: "stale terminal action revision", mutate: func(_ *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				c.Expected.TerminalActionRevision++
			}, want: gameusecase.ErrNormalPauseGraphConflict},
			{name: "duplicate child identity", mutate: func(_ *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				c.Expected.Series = append(c.Expected.Series, c.Expected.Series[0])
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "missing Presence", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				a.Graph.Presence = a.Graph.Presence[:1]
			}, want: gameusecase.ErrNormalPauseGraphIncomplete},
			{name: "expired Game deadline", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				deadline := pausedAt
				a.Graph.Games[0].Deadline = &deadline
			}, want: gameusecase.ErrNormalPauseDeadline},
			{name: "Presence belongs to another Tournament", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				a.Graph.Presence[0].TournamentID = uuid.New()
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "Presence belongs to another Series", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				a.Graph.Presence[0].SeriesID = uuid.New()
			}, want: gameusecase.ErrNormalPauseGraphIncomplete},
			{name: "Series omits a Wave member", mutate: func(a *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				participantID := uuid.New()
				a.Graph.Wave.Wave.Members = append(a.Graph.Wave.Wave.Members, domain.WaveMember{ParticipantID: participantID, Ready: true})
				a.Graph.Presence = append(a.Graph.Presence, pausedomain.PausePresence{ID: uuid.New(), TournamentID: a.Scope.TournamentID, RosterID: a.Scope.RosterID, SeriesID: a.Graph.Series[0].Execution.Series.ID, ParticipantID: participantID, State: pausedomain.PresenceStateConnected, PresenceEpoch: 1, Revision: 1, ConnectedAt: pausedAt.Add(-time.Minute), UpdatedAt: pausedAt.Add(-time.Minute)})
				refreshNormalPauseRevisions(a, c)
			}, want: gameusecase.ErrNormalPauseGraphIncomplete},
			{name: "current Game differs from embedded attempt", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				a.Graph.Series[0].Execution.Series.Slots[0].Attempts[0].State = domain.GameStateReady
			}, want: gameusecase.ErrNormalPauseGraphIncomplete},
			{name: "Reconnect belongs to a foreign participant", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				a.Graph.Reconnect[0].ParticipantID = uuid.New()
			}, want: gameusecase.ErrNormalPauseGraphIncomplete},
			{name: "open Reconnect deadline is already reached", mutate: func(a *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				makeReconnectOpenBeforePause(a, c, pausedAt, 0)
			}, want: gameusecase.ErrNormalPauseDeadline},
			{name: "terminal Reconnect update precedes its close", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				a.Graph.Reconnect[0].UpdatedAt = a.Graph.Reconnect[0].ClosedAt.Add(-time.Second)
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Tournament history", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				a.Graph.Tournament.UpdatedAt = pausedAt.Add(time.Second)
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Tournament creation", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				a.Graph.Tournament.CreatedAt = pausedAt.Add(time.Second)
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Wave start", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				startedAt := pausedAt.Add(time.Second)
				a.Graph.Wave.Wave.StartedAt = &startedAt
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes ready-window evidence", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				openedAt := pausedAt.Add(time.Second)
				consumedAt := pausedAt.Add(2 * time.Second)
				a.Graph.Wave.Wave.ReadyWindow.OpenedAt = openedAt
				a.Graph.Wave.Wave.ReadyWindow.ConsumedAt = &consumedAt
				a.Graph.Wave.Wave.ReadyWindow.Deadline = pausedAt.Add(3 * time.Second)
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Presence history", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				a.Graph.Presence[0].ConnectedAt = pausedAt.Add(time.Second)
				a.Graph.Presence[0].UpdatedAt = pausedAt.Add(time.Second)
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Reconnect history", mutate: func(a *gameusecase.NormalPauseAuthority, _ *gameusecase.NormalPauseCommand) {
				future := pausedAt.Add(time.Second)
				a.Graph.Reconnect[0].ClosedAt = &future
				a.Graph.Reconnect[0].UpdatedAt = future
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "counter is invalid without an interval", mutate: func(a *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				a.Graph.Reconnect = nil
				a.Graph.Counters[0].Used = 0
				a.Graph.Counters[0].RosterID = uuid.New()
				refreshNormalPauseRevisions(a, c)
			}, want: gameusecase.ErrInvalidNormalPauseGraph},
			{name: "revision overflow", mutate: func(a *gameusecase.NormalPauseAuthority, c *gameusecase.NormalPauseCommand) {
				a.Graph.Tournament.Revision = math.MaxInt64
				a.Revisions.TournamentRevision = math.MaxInt64
				c.Expected.TournamentRevision = math.MaxInt64
			}, want: gameusecase.ErrNormalPauseOverflow},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := normalPauseFixture(pausedAt)
				test.mutate(&authority, &command)
				repository := newNormalPauseRepositoryHarness(t, authority)
				useCase := gameusecase.NewNormalPauseGraphUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, pausedAt))
				if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})
}
