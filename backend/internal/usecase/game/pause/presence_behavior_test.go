package pause_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	pausedomain "github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/pause"
	"github.com/google/uuid"
)

func TestPausedPresenceUpdates(t *testing.T) {
	t.Parallel()

	changedAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)

	t.Run("flips only durable Presence under an active normal pause", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		before := clonePausedPresenceAuthority(authority)
		repository := newPausedPresenceRepositoryHarness(t, authority)
		useCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, changedAt.In(time.FixedZone("operator", 3*60*60))))

		record, changed, err := useCase.Change(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Change() error = %v, changed = %v", err, changed)
		}
		presence := record.Authority.Presence
		if presence.State != pausedomain.PresenceStateDisconnected || presence.PresenceEpoch != before.Presence.PresenceEpoch+1 ||
			presence.Revision != before.Presence.Revision+1 || presence.DisconnectedAt == nil || !presence.DisconnectedAt.Equal(changedAt) ||
			!presence.UpdatedAt.Equal(changedAt) {
			t.Fatalf("Presence = %+v", presence)
		}
		if !pausedPresenceImmutableEqual(before, record.Authority) {
			t.Fatal("Presence change altered reconnect, counters, clocks, pause, or terminal evidence")
		}

		connect := command
		connect.CommandID = uuid.New()
		connect.NextState = pausedomain.PresenceStateConnected
		connect.ExpectedGraphRevision = record.Authority.Pause.Graph.Revision
		connect.ExpectedPauseRevision = record.Authority.Pause.Revision
		connect.ExpectedPresenceEpoch = record.Authority.Presence.PresenceEpoch
		connect.ExpectedPresenceRevision = record.Authority.Presence.Revision
		connected, changed, err := useCase.Change(t.Context(), connect)
		if err != nil || !changed || connected.Authority.Presence.State != pausedomain.PresenceStateConnected ||
			connected.Authority.Presence.DisconnectedAt != nil || connected.Authority.Presence.PresenceEpoch != presence.PresenceEpoch+1 {
			t.Fatalf("connect error = %v, changed = %v, record = %+v", err, changed, connected)
		}

		record.Authority.Reconnect = append(record.Authority.Reconnect, pausedomain.PauseReconnectInterval{ID: uuid.New()})
		retried, changed, err := useCase.Change(t.Context(), command)
		if err != nil || changed || len(retried.Authority.Reconnect) != len(before.Reconnect) || repository.writeCount() != 2 {
			t.Fatalf("retry error = %v, changed = %v, record = %+v, writes = %d", err, changed, retried, repository.writeCount())
		}
	})

	t.Run("fails closed before a Presence write", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*gameusecase.PausedPresenceAuthority, *gameusecase.PausedPresenceCommand)
			want   error
		}{
			{name: "disconnect pause", mutate: func(a *gameusecase.PausedPresenceAuthority, _ *gameusecase.PausedPresenceCommand) {
				a.Pause.Reason = gameusecase.PauseReasonDisconnect
			}, want: gameusecase.ErrPausedPresenceSuppression},
			{name: "resolved pause", mutate: func(a *gameusecase.PausedPresenceAuthority, _ *gameusecase.PausedPresenceCommand) {
				a.Pause.State = gameusecase.PauseStateResumed
			}, want: gameusecase.ErrPausedPresenceSuppression},
			{name: "pause record reuses actor identity", mutate: func(a *gameusecase.PausedPresenceAuthority, _ *gameusecase.PausedPresenceCommand) {
				a.Pause.ActorID = a.Pause.CommandID
			}, want: gameusecase.ErrPausedPresenceSuppression},
			{name: "foreign participant", mutate: func(_ *gameusecase.PausedPresenceAuthority, c *gameusecase.PausedPresenceCommand) {
				c.ParticipantID = uuid.New()
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "foreign Presence row", mutate: func(a *gameusecase.PausedPresenceAuthority, _ *gameusecase.PausedPresenceCommand) {
				a.Presence.ID = uuid.New()
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "Presence belongs to another Tournament", mutate: func(a *gameusecase.PausedPresenceAuthority, _ *gameusecase.PausedPresenceCommand) {
				a.Presence.TournamentID = uuid.New()
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "Presence belongs to another Roster", mutate: func(a *gameusecase.PausedPresenceAuthority, _ *gameusecase.PausedPresenceCommand) {
				a.Presence.RosterID = uuid.New()
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "Presence belongs to another Series", mutate: func(a *gameusecase.PausedPresenceAuthority, _ *gameusecase.PausedPresenceCommand) {
				a.Presence.SeriesID = uuid.New()
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "stale graph", mutate: func(_ *gameusecase.PausedPresenceAuthority, c *gameusecase.PausedPresenceCommand) {
				c.ExpectedGraphRevision++
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "stale epoch", mutate: func(_ *gameusecase.PausedPresenceAuthority, c *gameusecase.PausedPresenceCommand) {
				c.Scope.Authority.Epoch++
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "same state", mutate: func(_ *gameusecase.PausedPresenceAuthority, c *gameusecase.PausedPresenceCommand) {
				c.NextState = pausedomain.PresenceStateConnected
			}, want: gameusecase.ErrPausedPresenceState},
			{name: "loaded Presence is behind the pause snapshot", mutate: func(a *gameusecase.PausedPresenceAuthority, c *gameusecase.PausedPresenceCommand) {
				a.Presence.PresenceEpoch--
				a.Presence.Revision--
				c.ExpectedPresenceEpoch = a.Presence.PresenceEpoch
				c.ExpectedPresenceRevision = a.Presence.Revision
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "loaded Presence epoch and revision deltas differ", mutate: func(a *gameusecase.PausedPresenceAuthority, c *gameusecase.PausedPresenceCommand) {
				a.Presence.PresenceEpoch++
				c.ExpectedPresenceEpoch = a.Presence.PresenceEpoch
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "unchanged Presence differs from snapshot", mutate: func(a *gameusecase.PausedPresenceAuthority, _ *gameusecase.PausedPresenceCommand) {
				a.Presence.UpdatedAt = changedAt.Add(-time.Second)
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "advanced Presence predates the active pause", mutate: func(a *gameusecase.PausedPresenceAuthority, c *gameusecase.PausedPresenceCommand) {
				a.Presence.PresenceEpoch++
				a.Presence.Revision++
				a.Presence.UpdatedAt = a.Pause.PausedAt.Add(-time.Second)
				c.ExpectedPresenceEpoch = a.Presence.PresenceEpoch
				c.ExpectedPresenceRevision = a.Presence.Revision
			}, want: gameusecase.ErrPausedPresenceConflict},
			{name: "counter overflow", mutate: func(a *gameusecase.PausedPresenceAuthority, c *gameusecase.PausedPresenceCommand) {
				snapshot := &a.Pause.Graph.Presence[0]
				snapshot.Revision = snapshot.PresenceEpoch
				a.Pause.Expected.Presence[0].Revision = snapshot.Revision
				a.Presence.PresenceEpoch = math.MaxInt64
				a.Presence.Revision = math.MaxInt64
				a.Presence.UpdatedAt = a.Pause.PausedAt
				c.ExpectedPresenceEpoch = math.MaxInt64
				c.ExpectedPresenceRevision = a.Presence.Revision
			}, want: gameusecase.ErrPausedPresenceOverflow},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := pausedPresenceFixture(t, changedAt)
				test.mutate(&authority, &command)
				repository := newPausedPresenceRepositoryHarness(t, authority)
				useCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, changedAt))
				if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("rejects a server timestamp before the pause or prior Presence update", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		repository := newPausedPresenceRepositoryHarness(t, authority)
		tooEarly := authority.Pause.PausedAt.Add(-time.Second)
		useCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, tooEarly))
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, gameusecase.ErrInvalidPausedPresence) || changed || repository.writeCount() != 0 {
			t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects changedAt before an advanced live Presence update", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		authority.Presence.PresenceEpoch++
		authority.Presence.Revision++
		authority.Presence.UpdatedAt = authority.Pause.PausedAt.Add(40 * time.Second)
		command.ExpectedPresenceEpoch = authority.Presence.PresenceEpoch
		command.ExpectedPresenceRevision = authority.Presence.Revision
		between := authority.Pause.PausedAt.Add(20 * time.Second)
		repository := newPausedPresenceRepositoryHarness(t, authority)
		useCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, between))
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, gameusecase.ErrInvalidPausedPresence) || changed || repository.writeCount() != 0 {
			t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects malformed stored Presence event time", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		leaderRepository := newPausedPresenceRepositoryHarness(t, authority)
		leader := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, changedAt))
		stored, changed, err := leader.Change(t.Context(), command)
		if err != nil || !changed || stored.Authority.Presence.DisconnectedAt == nil {
			t.Fatalf("prepare stored Presence: error = %v, changed = %v", err, changed)
		}
		wrongAt := stored.ChangedAt.Add(-time.Second)
		stored.Authority.Presence.DisconnectedAt = &wrongAt
		repository := newPausedPresenceRepositoryHarness(t, authority)
		repository.storeCommand(*stored)
		useCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, changedAt.Add(time.Second)))
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, gameusecase.ErrPausedPresenceCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects malformed stored CONNECT event time", func(t *testing.T) {
		t.Parallel()

		authority, disconnect := pausedPresenceFixture(t, changedAt)
		repository := newPausedPresenceRepositoryHarness(t, authority)
		disconnectUseCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, changedAt))
		disconnected, changed, err := disconnectUseCase.Change(t.Context(), disconnect)
		if err != nil || !changed {
			t.Fatalf("prepare disconnect: error = %v, changed = %v", err, changed)
		}
		connect := disconnect
		connect.CommandID = uuid.New()
		connect.NextState = pausedomain.PresenceStateConnected
		connect.ExpectedPresenceEpoch = disconnected.Authority.Presence.PresenceEpoch
		connect.ExpectedPresenceRevision = disconnected.Authority.Presence.Revision
		connectedAt := changedAt.Add(time.Second)
		connectUseCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, connectedAt))
		stored, changed, err := connectUseCase.Change(t.Context(), connect)
		if err != nil || !changed || stored.Authority.Presence.State != pausedomain.PresenceStateConnected {
			t.Fatalf("prepare connect: error = %v, changed = %v", err, changed)
		}
		wrongAt := stored.ChangedAt.Add(-time.Second)
		stored.Authority.Presence.ConnectedAt = wrongAt
		replayRepository := newPausedPresenceRepositoryHarness(t, authority)
		replayRepository.storeCommand(*stored)
		replayUseCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), replayRepository, newPauseClock(t, connectedAt.Add(time.Second)))
		if _, changed, err := replayUseCase.Change(t.Context(), connect); !errors.Is(err, gameusecase.ErrPausedPresenceCommandReuse) || changed || replayRepository.writeCount() != 0 {
			t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, replayRepository.writeCount())
		}
	})

	t.Run("uses a second command lookup after locking authority", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		leaderRepository := newPausedPresenceRepositoryHarness(t, authority)
		leader := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), leaderRepository, newPauseClock(t, changedAt))
		stored, changed, err := leader.Change(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored Presence: error = %v, changed = %v", err, changed)
		}

		followerRepository := newPausedPresenceRepositoryHarness(t, authority)
		followerRepository.blockNextLoad()
		follower := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), followerRepository, newPauseClock(t, changedAt.Add(time.Second)))
		type result struct {
			record  *gameusecase.PausedPresenceRecord
			changed bool
			err     error
		}
		resultCh := make(chan result, 1)
		go func() {
			record, changed, err := follower.Change(context.Background(), command)
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

		authority, command := pausedPresenceFixture(t, changedAt)
		repository := newPausedPresenceRepositoryHarness(t, authority)
		repository.conflictsRemaining = 2
		transactions := newCountingTransactionManager(t)
		clock := newSequenceClock(t, changedAt, changedAt.Add(time.Second))
		useCase := gameusecase.NewPausedPresenceUseCase(transactions, repository, clock)
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, gameusecase.ErrPausedPresenceConflict) || changed || repository.loadCount != 2 || transactions.count() != 2 || clock.count() != 2 {
			t.Fatalf("Change() error = %v, changed = %v, loads = %d, transactions = %d, clock calls = %d", err, changed, repository.loadCount, transactions.count(), clock.count())
		}
	})

	t.Run("binds the CAS to the exact Presence row owner", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		repository := newPausedPresenceRepositoryHarness(t, authority)
		repository.replacePresenceOnCommit = true
		useCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, changedAt))
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, gameusecase.ErrPausedPresenceConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("retries one CAS conflict, rejects command reuse, and wraps errors", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		repository := newPausedPresenceRepositoryHarness(t, authority)
		repository.conflictOnce = true
		useCase := gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, changedAt))
		if _, changed, err := useCase.Change(t.Context(), command); err != nil || !changed || repository.loadCount != 2 {
			t.Fatalf("retry error = %v, changed = %v, loads = %d", err, changed, repository.loadCount)
		}
		reused := command
		reused.NextState = pausedomain.PresenceStateConnected
		if _, changed, err := useCase.Change(t.Context(), reused); !errors.Is(err, gameusecase.ErrPausedPresenceCommandReuse) || changed {
			t.Fatalf("reuse error = %v, changed = %v", err, changed)
		}

		cause := errors.New("presence storage unavailable")
		authority, command = pausedPresenceFixture(t, changedAt)
		repository = newPausedPresenceRepositoryHarness(t, authority)
		repository.loadErr = cause
		useCase = gameusecase.NewPausedPresenceUseCase(newPauseTransactionManager(t), repository, newPauseClock(t, changedAt))
		if _, _, err := useCase.Change(t.Context(), command); !errors.Is(err, cause) {
			t.Fatalf("Change() error = %v, want wrapped cause", err)
		}
	})
}
