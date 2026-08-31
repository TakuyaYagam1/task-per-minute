package arena_test

import (
	"context"
	"errors"
	"math"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestNormalPauseGraphEntry(t *testing.T) {
	t.Parallel()

	pausedAt := time.Date(2026, time.August, 31, 11, 0, 0, 0, time.UTC)

	t.Run("publishes one complete paused graph and reconciles an exact retry", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		repository := newNormalPauseRepositoryFake(authority)
		useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt})

		record, changed, err := useCase.Enter(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Enter() error = %v, changed = %v", err, changed)
		}
		if record.State != arena.PauseStateActive || record.Graph.Tournament.State != domain.ArenaTournamentStateTechnicalPause ||
			record.Graph.Tournament.PausedFromState == nil || *record.Graph.Tournament.PausedFromState != domain.ArenaTournamentStateSwiss ||
			record.Graph.Wave.Wave.State != domain.ArenaWaveStatePaused || !record.Graph.DeadlinesSuppressed ||
			len(record.Graph.Series) != 1 || record.Graph.Series[0].Execution.Series.State != domain.ArenaSeriesStateTechnicalPause ||
			record.Graph.Series[0].Execution.ResumeState == nil || *record.Graph.Series[0].Execution.ResumeState != domain.ArenaSeriesStateActive ||
			len(record.Graph.Games) != 1 || record.Graph.Games[0].Game.State != domain.ArenaGameStatePaused ||
			record.Graph.Games[0].ResumeState == nil || *record.Graph.Games[0].ResumeState != domain.ArenaGameStateActive ||
			record.Graph.Games[0].Deadline != nil || len(record.Graph.FrozenDeadlines) != 1 ||
			record.Graph.FrozenDeadlines[0].Remaining != time.Minute {
			t.Fatalf("paused graph = %+v", record.Graph)
		}
		if repository.writeCount() != 1 {
			t.Fatalf("writes = %d, want 1", repository.writeCount())
		}

		record.Graph.Series[0].Revision = 99
		repository.bumpGraphRevision()
		retried, changed, err := useCase.Enter(t.Context(), command)
		if err != nil || changed || retried.Graph.Series[0].Revision == 99 || repository.writeCount() != 1 {
			t.Fatalf("retry error = %v, changed = %v, record = %+v, writes = %d", err, changed, retried, repository.writeCount())
		}
	})

	t.Run("prevalidates every descendant before the CAS", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*arena.NormalPauseAuthority, *arena.NormalPauseCommand)
			want   error
		}{
			{name: "partial graph", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) { a.Complete = false }, want: arena.ErrNormalPauseGraphIncomplete},
			{name: "active Golden", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) { a.ActiveGolden = true }, want: arena.ErrNormalPauseGoldenActive},
			{name: "stale Wave revision", mutate: func(_ *arena.NormalPauseAuthority, c *arena.NormalPauseCommand) { c.Expected.WaveRevision++ }, want: arena.ErrNormalPauseGraphConflict},
			{name: "wrong Tournament origin state", mutate: func(_ *arena.NormalPauseAuthority, c *arena.NormalPauseCommand) {
				c.Expected.TournamentState = domain.ArenaTournamentStatePlayoffs
			}, want: arena.ErrNormalPauseGraphConflict},
			{name: "stale Series revision", mutate: func(_ *arena.NormalPauseAuthority, c *arena.NormalPauseCommand) { c.Expected.Series[0].Revision++ }, want: arena.ErrNormalPauseGraphConflict},
			{name: "stale reconnect counter revision", mutate: func(_ *arena.NormalPauseAuthority, c *arena.NormalPauseCommand) { c.Expected.Counters[0].Revision++ }, want: arena.ErrNormalPauseGraphConflict},
			{name: "stale terminal action revision", mutate: func(_ *arena.NormalPauseAuthority, c *arena.NormalPauseCommand) { c.Expected.TerminalActionRevision++ }, want: arena.ErrNormalPauseGraphConflict},
			{name: "duplicate child identity", mutate: func(_ *arena.NormalPauseAuthority, c *arena.NormalPauseCommand) {
				c.Expected.Series = append(c.Expected.Series, c.Expected.Series[0])
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "missing Presence", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Presence = a.Graph.Presence[:1]
			}, want: arena.ErrNormalPauseGraphIncomplete},
			{name: "expired Game deadline", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				deadline := pausedAt
				a.Graph.Games[0].Deadline = &deadline
			}, want: arena.ErrNormalPauseDeadline},
			{name: "Presence belongs to another Tournament", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Presence[0].TournamentID = uuid.New()
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "Presence belongs to another Series", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Presence[0].SeriesID = uuid.New()
			}, want: arena.ErrNormalPauseGraphIncomplete},
			{name: "Series omits a Wave member", mutate: func(a *arena.NormalPauseAuthority, c *arena.NormalPauseCommand) {
				participantID := uuid.New()
				a.Graph.Wave.Wave.Members = append(a.Graph.Wave.Wave.Members, domain.ArenaWaveMember{ParticipantID: participantID, Ready: true})
				a.Graph.Presence = append(a.Graph.Presence, arena.PausePresence{ID: uuid.New(), TournamentID: a.Scope.TournamentID, RosterID: a.Scope.RosterID, SeriesID: a.Graph.Series[0].Execution.Series.ID, ParticipantID: participantID, State: arena.PresenceStateConnected, PresenceEpoch: 1, Revision: 1, ConnectedAt: pausedAt.Add(-time.Minute), UpdatedAt: pausedAt.Add(-time.Minute)})
				refreshNormalPauseRevisions(a, c)
			}, want: arena.ErrNormalPauseGraphIncomplete},
			{name: "current Game differs from embedded attempt", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Series[0].Execution.Series.Slots[0].Attempts[0].State = domain.ArenaGameStateReady
			}, want: arena.ErrNormalPauseGraphIncomplete},
			{name: "Reconnect belongs to a foreign participant", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Reconnect[0].ParticipantID = uuid.New()
			}, want: arena.ErrNormalPauseGraphIncomplete},
			{name: "open Reconnect would strand the pause", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Reconnect[0].State = arena.ReconnectStateOpen
				a.Graph.Reconnect[0].ClosedAt = nil
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "terminal Reconnect update precedes its close", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Reconnect[0].UpdatedAt = a.Graph.Reconnect[0].ClosedAt.Add(-time.Second)
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Tournament history", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Tournament.UpdatedAt = pausedAt.Add(time.Second)
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Tournament creation", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Tournament.CreatedAt = pausedAt.Add(time.Second)
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Wave start", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				startedAt := pausedAt.Add(time.Second)
				a.Graph.Wave.Wave.StartedAt = &startedAt
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes ready-window evidence", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				openedAt := pausedAt.Add(time.Second)
				consumedAt := pausedAt.Add(2 * time.Second)
				a.Graph.Wave.Wave.ReadyWindow.OpenedAt = openedAt
				a.Graph.Wave.Wave.ReadyWindow.ConsumedAt = &consumedAt
				a.Graph.Wave.Wave.ReadyWindow.Deadline = pausedAt.Add(3 * time.Second)
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Presence history", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				a.Graph.Presence[0].ConnectedAt = pausedAt.Add(time.Second)
				a.Graph.Presence[0].UpdatedAt = pausedAt.Add(time.Second)
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "pause clock precedes Reconnect history", mutate: func(a *arena.NormalPauseAuthority, _ *arena.NormalPauseCommand) {
				future := pausedAt.Add(time.Second)
				a.Graph.Reconnect[0].ClosedAt = &future
				a.Graph.Reconnect[0].UpdatedAt = future
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "counter is invalid without an interval", mutate: func(a *arena.NormalPauseAuthority, c *arena.NormalPauseCommand) {
				a.Graph.Reconnect = nil
				a.Graph.Counters[0].Used = 0
				a.Graph.Counters[0].RosterID = uuid.New()
				refreshNormalPauseRevisions(a, c)
			}, want: arena.ErrInvalidNormalPauseGraph},
			{name: "revision overflow", mutate: func(a *arena.NormalPauseAuthority, c *arena.NormalPauseCommand) {
				a.Graph.Tournament.Revision = math.MaxInt64
				a.Revisions.TournamentRevision = math.MaxInt64
				c.Expected.TournamentRevision = math.MaxInt64
			}, want: arena.ErrNormalPauseOverflow},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := normalPauseFixture(pausedAt)
				test.mutate(&authority, &command)
				repository := newNormalPauseRepositoryFake(authority)
				useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt})
				if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("uses a second command lookup after locking authority", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		leaderRepository := newNormalPauseRepositoryFake(authority)
		leader := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: pausedAt})
		stored, changed, err := leader.Enter(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored pause: error = %v, changed = %v", err, changed)
		}

		followerRepository := newNormalPauseRepositoryFake(authority)
		followerRepository.blockNextLoad()
		follower := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, followerRepository, fixedArenaClock{now: pausedAt.Add(time.Second)})
		type result struct {
			record  *arena.NormalPauseRecord
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
		repository := newNormalPauseRepositoryFake(authority)
		repository.conflictsRemaining = 2
		transactions := &countingArenaTransactionManager{}
		clock := &sequenceArenaClock{times: []time.Time{pausedAt, pausedAt.Add(time.Second)}}
		useCase := arena.NewNormalPauseGraphUseCase(transactions, repository, clock)
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, arena.ErrNormalPauseGraphConflict) || changed || repository.loadCount != 2 || transactions.count() != 2 || clock.count() != 2 {
			t.Fatalf("Enter() error = %v, changed = %v, loads = %d, transactions = %d, clock calls = %d", err, changed, repository.loadCount, transactions.count(), clock.count())
		}
	})

	t.Run("rejects a malformed stored command graph", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		leaderRepository := newNormalPauseRepositoryFake(authority)
		leader := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: pausedAt})
		stored, changed, err := leader.Enter(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored pause: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Presence[0].ID = uuid.New()
		repository := newNormalPauseRepositoryFake(authority)
		repository.storeCommand(*stored)
		useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt.Add(time.Second)})
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, arena.ErrNormalPauseCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects structurally valid stored pause records with wrong transitions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*arena.NormalPauseRecord)
		}{
			{name: "Tournament did not pause", mutate: func(record *arena.NormalPauseRecord) {
				record.Graph.Tournament.State = domain.ArenaTournamentStateSwiss
				record.Graph.Tournament.PausedFromState = nil
			}},
			{name: "Tournament pause time was not published", mutate: func(record *arena.NormalPauseRecord) {
				record.Graph.Tournament.UpdatedAt = record.PausedAt.Add(-time.Second)
			}},
			{name: "Tournament paused from another state", mutate: func(record *arena.NormalPauseRecord) {
				state := domain.ArenaTournamentStatePlayoffs
				record.Graph.Tournament.PausedFromState = &state
			}},
			{name: "Wave state changed without revision", mutate: func(record *arena.NormalPauseRecord) {
				record.Graph.Wave.Revision = record.Expected.WaveRevision
			}},
			{name: "Series state changed without revision", mutate: func(record *arena.NormalPauseRecord) {
				record.Graph.Series[0].Revision = record.Expected.Series[0].Revision
			}},
			{name: "Game state changed without revision", mutate: func(record *arena.NormalPauseRecord) {
				record.Graph.Games[0].Revision = record.Expected.Games[0].Revision
			}},
			{name: "open Reconnect was captured", mutate: func(record *arena.NormalPauseRecord) {
				record.Graph.Reconnect[0].State = arena.ReconnectStateOpen
				record.Graph.Reconnect[0].ClosedAt = nil
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := normalPauseFixture(pausedAt)
				leaderRepository := newNormalPauseRepositoryFake(authority)
				leader := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: pausedAt})
				stored, changed, err := leader.Enter(t.Context(), command)
				if err != nil || !changed {
					t.Fatalf("prepare stored pause: error = %v, changed = %v", err, changed)
				}
				test.mutate(stored)
				repository := newNormalPauseRepositoryFake(authority)
				repository.storeCommand(*stored)
				useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt.Add(time.Second)})
				if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, arena.ErrNormalPauseCommandReuse) || changed {
					t.Fatalf("Enter() error = %v, changed = %v", err, changed)
				}
			})
		}
	})

	t.Run("rejects a stored Draft pause without its exact revision transition", func(t *testing.T) {
		t.Parallel()

		authority, command := draftNormalPauseFixture(t, pausedAt)
		leaderRepository := newNormalPauseRepositoryFake(authority)
		leader := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: pausedAt})
		stored, changed, err := leader.Enter(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored Draft pause: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Draft.Revision = stored.Expected.Draft.Revision
		stored.Graph.Draft.RevisionID = stored.Expected.Draft.RevisionID
		stored.Graph.Draft.PreviousRevisionID = uuid.New()
		repository := newNormalPauseRepositoryFake(authority)
		repository.storeCommand(*stored)
		useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt.Add(time.Second)})
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, arena.ErrNormalPauseCommandReuse) || changed {
			t.Fatalf("Enter() error = %v, changed = %v", err, changed)
		}

		authority, command = draftNormalPauseFixture(t, pausedAt)
		leaderRepository = newNormalPauseRepositoryFake(authority)
		leader = arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: pausedAt})
		stored, changed, err = leader.Enter(t.Context(), command)
		if err != nil || !changed || stored.Graph.Draft == nil || stored.Graph.Draft.Transition == nil {
			t.Fatalf("prepare stored Draft transition: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Draft.Transition.ActorID = uuid.New()
		repository = newNormalPauseRepositoryFake(authority)
		repository.storeCommand(*stored)
		useCase = arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt.Add(time.Second)})
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, arena.ErrNormalPauseCommandReuse) || changed {
			t.Fatalf("transition replay error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects changed lineage for a no-op Draft pause", func(t *testing.T) {
		t.Parallel()

		authority, command := completedDraftNormalPauseFixture(t, pausedAt)
		leaderRepository := newNormalPauseRepositoryFake(authority)
		leader := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: pausedAt})
		stored, changed, err := leader.Enter(t.Context(), command)
		if err != nil || !changed || stored.Graph.Draft == nil || stored.Graph.Draft.State != arena.DraftExecutionStateCompleted {
			t.Fatalf("prepare stored completed Draft: error = %v, changed = %v", err, changed)
		}
		if stored.Expected.DraftPreviousRevisionID != stored.Graph.Draft.PreviousRevisionID {
			t.Fatalf("stored lineage = %s, expected = %s", stored.Graph.Draft.PreviousRevisionID, stored.Expected.DraftPreviousRevisionID)
		}
		stored.Graph.Draft.PreviousRevisionID = uuid.New()
		repository := newNormalPauseRepositoryFake(authority)
		repository.storeCommand(*stored)
		useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt.Add(time.Second)})
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, arena.ErrNormalPauseCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("fences an active Draft revision", func(t *testing.T) {
		t.Parallel()

		authority, command := draftNormalPauseFixture(t, pausedAt)
		invalidIdentity := command
		invalidIdentity.DraftResultRevisionID = command.CommandID
		invalidRepository := newNormalPauseRepositoryFake(authority)
		invalidUseCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, invalidRepository, fixedArenaClock{now: pausedAt})
		if _, changed, err := invalidUseCase.Enter(t.Context(), invalidIdentity); !errors.Is(err, arena.ErrInvalidNormalPauseGraph) || changed {
			t.Fatalf("invalid Draft result identity error = %v, changed = %v", err, changed)
		}
		invalidDraftIdentity := command
		invalidDraftIdentity.DraftResultRevisionID = authority.Graph.Draft.ID
		invalidRepository = newNormalPauseRepositoryFake(authority)
		invalidUseCase = arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, invalidRepository, fixedArenaClock{now: pausedAt})
		if _, changed, err := invalidUseCase.Enter(t.Context(), invalidDraftIdentity); !errors.Is(err, arena.ErrInvalidNormalPauseGraph) || changed {
			t.Fatalf("Draft-owned result identity error = %v, changed = %v", err, changed)
		}
		validRepository := newNormalPauseRepositoryFake(authority)
		validUseCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, validRepository, fixedArenaClock{now: pausedAt})
		record, changed, err := validUseCase.Enter(t.Context(), command)
		if err != nil || !changed || record.Graph.Draft == nil || record.Graph.Draft.State != arena.DraftExecutionStatePaused ||
			record.Graph.Draft.RevisionID != command.DraftResultRevisionID {
			t.Fatalf("valid Draft pause error = %v, changed = %v, graph = %+v", err, changed, record)
		}
		command.Expected.Draft.Revision++
		command.Expected.DraftPreviousRevisionID = uuid.New()
		repository := newNormalPauseRepositoryFake(authority)
		useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt})
		if _, changed, err := useCase.Enter(t.Context(), command); !errors.Is(err, arena.ErrNormalPauseGraphConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("Enter() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}

		cycleAuthority, cycleCommand := draftRevisionTwoNormalPauseFixture(t, pausedAt)
		if cycleCommand.Expected.DraftPreviousRevisionID != cycleAuthority.Graph.Draft.PreviousRevisionID {
			t.Fatalf("expected previous Draft revision = %s, want %s", cycleCommand.Expected.DraftPreviousRevisionID, cycleAuthority.Graph.Draft.PreviousRevisionID)
		}
		cycleIDs := []uuid.UUID{
			cycleAuthority.Graph.Draft.ID,
			cycleAuthority.Graph.Draft.RevisionID,
			cycleAuthority.Graph.Draft.PreviousRevisionID,
		}
		for _, cycleID := range cycleIDs {
			candidate := cycleCommand
			candidate.DraftResultRevisionID = cycleID
			repository := newNormalPauseRepositoryFake(cycleAuthority)
			useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt})
			if _, changed, err := useCase.Enter(t.Context(), candidate); !errors.Is(err, arena.ErrInvalidNormalPauseGraph) || changed || repository.writeCount() != 0 {
				t.Fatalf("cycle identity %s error = %v, changed = %v, writes = %d", cycleID, err, changed, repository.writeCount())
			}
		}
		stalePrevious := cycleCommand
		stalePrevious.Expected.DraftPreviousRevisionID = uuid.New()
		repository = newNormalPauseRepositoryFake(cycleAuthority)
		useCase = arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt})
		if _, changed, err := useCase.Enter(t.Context(), stalePrevious); !errors.Is(err, arena.ErrNormalPauseGraphConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("stale previous Draft revision error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}

		futureAuthority, futureCommand := draftNormalPauseFixture(t, pausedAt)
		futureAuthority.Graph.Draft.FirstActorDecision.DecidedAt = pausedAt.Add(time.Second)
		refreshNormalPauseRevisions(&futureAuthority, &futureCommand)
		futureCommand.DraftResultRevisionID = uuid.New()
		repository = newNormalPauseRepositoryFake(futureAuthority)
		useCase = arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt})
		if _, changed, err := useCase.Enter(t.Context(), futureCommand); !errors.Is(err, arena.ErrInvalidNormalPauseGraph) || changed || repository.writeCount() != 0 {
			t.Fatalf("future Draft evidence error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("retries one fresh authority conflict and rejects command reuse", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		repository := newNormalPauseRepositoryFake(authority)
		repository.conflictOnce = true
		useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt})
		if _, changed, err := useCase.Enter(t.Context(), command); err != nil || !changed || repository.loadCount != 2 {
			t.Fatalf("retry error = %v, changed = %v, loads = %d", err, changed, repository.loadCount)
		}
		reused := command
		reused.PauseID = uuid.New()
		if _, changed, err := useCase.Enter(t.Context(), reused); !errors.Is(err, arena.ErrNormalPauseCommandReuse) || changed {
			t.Fatalf("reuse error = %v, changed = %v", err, changed)
		}
	})

	t.Run("wraps repository failures", func(t *testing.T) {
		t.Parallel()

		authority, command := normalPauseFixture(pausedAt)
		cause := errors.New("storage unavailable")
		repository := newNormalPauseRepositoryFake(authority)
		repository.loadErr = cause
		useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt})
		if _, _, err := useCase.Enter(t.Context(), command); !errors.Is(err, cause) {
			t.Fatalf("Enter() error = %v, want wrapped cause", err)
		}
	})
}

type normalPauseRepositoryFake struct {
	mu                 sync.Mutex
	authority          arena.NormalPauseAuthority
	commands           map[uuid.UUID]arena.NormalPauseRecord
	conflictOnce       bool
	conflictsRemaining int
	loadErr            error
	commitErr          error
	loadCount          int
	writes             int
	commitCalls        int
	loadStarted        chan struct{}
	loadRelease        chan struct{}
	loadOnce           sync.Once
}

func newNormalPauseRepositoryFake(authority arena.NormalPauseAuthority) *normalPauseRepositoryFake {
	return &normalPauseRepositoryFake{authority: cloneNormalPauseAuthority(authority), commands: make(map[uuid.UUID]arena.NormalPauseRecord)}
}

func (f *normalPauseRepositoryFake) FindNormalPauseCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*arena.NormalPauseRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.commands[commandID]
	if !ok || record.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	clone := cloneNormalPauseRecord(record)
	return &clone, nil
}

func (f *normalPauseRepositoryFake) LoadNormalPauseAuthority(_ context.Context, _ arena.PauseGraphScope) (arena.NormalPauseAuthority, error) {
	f.mu.Lock()
	f.loadCount++
	if f.loadErr != nil {
		f.mu.Unlock()
		return arena.NormalPauseAuthority{}, f.loadErr
	}
	started, release := f.loadStarted, f.loadRelease
	f.mu.Unlock()
	if started != nil {
		f.loadOnce.Do(func() { close(started) })
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneNormalPauseAuthority(f.authority), nil
}

func (f *normalPauseRepositoryFake) CommitNormalPause(_ context.Context, expected arena.PauseGraphRevisions, record arena.NormalPauseRecord) (*arena.NormalPauseRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if f.commitErr != nil {
		return nil, false, f.commitErr
	}
	if existing, ok := f.commands[record.CommandID]; ok {
		clone := cloneNormalPauseRecord(existing)
		return &clone, false, nil
	}
	if f.conflictOnce {
		f.conflictOnce = false
		return nil, false, domain.ErrConflict
	}
	if f.conflictsRemaining > 0 {
		f.conflictsRemaining--
		return nil, false, domain.ErrConflict
	}
	if !reflect.DeepEqual(expected, f.authority.Revisions) {
		return nil, false, domain.ErrConflict
	}
	f.writes++
	f.commands[record.CommandID] = cloneNormalPauseRecord(record)
	f.authority.Graph = clonePauseGraph(record.Graph)
	f.authority.Revisions = arena.PauseGraphRevisionsFrom(record.Graph)
	clone := cloneNormalPauseRecord(record)
	return &clone, true, nil
}

func (f *normalPauseRepositoryFake) blockNextLoad() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadStarted = make(chan struct{})
	f.loadRelease = make(chan struct{})
}

func (f *normalPauseRepositoryFake) storeCommand(record arena.NormalPauseRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[record.CommandID] = cloneNormalPauseRecord(record)
}

func (f *normalPauseRepositoryFake) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *normalPauseRepositoryFake) bumpGraphRevision() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.authority.Graph.Revision++
	f.authority.Revisions.GraphRevision++
}

func normalPauseFixture(now time.Time) (arena.NormalPauseAuthority, arena.NormalPauseCommand) {
	tournamentID, rosterID, waveID := uuid.New(), uuid.New(), uuid.New()
	seriesID, slotID, gameID := uuid.New(), uuid.New(), uuid.New()
	firstID, secondID := uuid.New(), uuid.New()
	startedAt := now.Add(-5 * time.Minute)
	openedAt := startedAt.Add(-time.Minute)
	consumedAt := startedAt
	gameDeadline := now.Add(time.Minute)
	reconnectPauseID := uuid.New()
	reconnectOpenedAt := now.Add(-4 * time.Minute)
	reconnectDeadline := now.Add(-2 * time.Minute)
	reconnectClosedAt := now.Add(-3 * time.Minute)
	wave := domain.ArenaWave{
		ID: waveID, TournamentID: tournamentID, RevisionID: domain.ArenaWaveRevisionID(uuid.New()),
		State:   domain.ArenaWaveStateActive,
		Members: []domain.ArenaWaveMember{{ParticipantID: firstID, Ready: true}, {ParticipantID: secondID, Ready: true}},
		ReadyWindow: &domain.ArenaReadyWindow{
			ID: uuid.New(), WaveID: waveID, RevisionID: domain.ArenaReadyWindowRevisionID(uuid.New()),
			State: domain.ArenaReadyWindowStateConsumed, OpenedAt: openedAt, Deadline: startedAt, ConsumedAt: &consumedAt,
		},
		StartedAt: &startedAt,
	}
	game := domain.ArenaGame{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.ArenaGameStateActive}
	series := domain.ArenaSeries{
		ID: seriesID, TournamentID: tournamentID, FirstParticipantID: firstID, SecondParticipantID: secondID,
		Format: domain.ArenaSeriesFormatBO1, State: domain.ArenaSeriesStateActive,
		Slots: []domain.ArenaGameSlot{{ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb, Attempts: []domain.ArenaGame{game}}},
	}
	scope := arena.PauseGraphScope{
		TournamentID: tournamentID, RosterID: rosterID, WaveID: waveID,
		Authority: arena.ExecutionAuthorityIdentity{TournamentID: tournamentID, HolderID: uuid.New(), LeaseID: uuid.New(), Epoch: 3, ProcessKind: arena.ExecutionProcessKindAuthority},
	}
	graph := arena.PauseGraph{
		Scope: scope, Revision: 11,
		Tournament: arena.TournamentRecord{ID: tournamentID, RosterID: rosterID, Preset: domain.ArenaPresetV1, State: domain.ArenaTournamentStateSwiss, Revision: 7, RosterSize: 2, CreatedAt: now.Add(-time.Hour), UpdatedAt: now.Add(-time.Minute), StartedAt: &startedAt},
		Wave:       arena.PauseWave{Wave: wave, Revision: 4},
		Series:     []arena.PauseSeries{{Execution: arena.SeriesExecution{Series: series}, Revision: 5, CurrentGameID: &gameID}},
		Games:      []arena.PauseGame{{SeriesID: seriesID, Game: game, Revision: 6, Deadline: &gameDeadline}},
		Presence: []arena.PausePresence{
			{ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: firstID, State: arena.PresenceStateConnected, PresenceEpoch: 2, Revision: 4, ConnectedAt: startedAt, UpdatedAt: startedAt},
			{ID: uuid.New(), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: secondID, State: arena.PresenceStateConnected, PresenceEpoch: 3, Revision: 5, ConnectedAt: startedAt, UpdatedAt: startedAt},
		},
		Reconnect:              []arena.PauseReconnectInterval{{ID: uuid.New(), PauseID: reconnectPauseID, RosterID: rosterID, SeriesID: seriesID, GameID: gameID, ParticipantID: firstID, PresenceEpoch: 2, Number: 1, State: arena.ReconnectStateReconnected, OpenedAt: reconnectOpenedAt, Deadline: reconnectDeadline, ClosedAt: &reconnectClosedAt, Revision: 2, UpdatedAt: reconnectClosedAt}},
		Counters:               []arena.PauseReconnectCounter{{PauseID: reconnectPauseID, RosterID: rosterID, ParticipantID: firstID, Limit: 3, Used: 1, Revision: 2}},
		TerminalActionRevision: 8,
	}
	revisions := arena.PauseGraphRevisionsFrom(graph)
	command := arena.NormalPauseCommand{Scope: scope, CommandID: uuid.New(), PauseID: uuid.New(), ActorID: uuid.New(), Reason: arena.PauseReasonOperator, Expected: clonePauseGraphRevisions(revisions)}
	return arena.NormalPauseAuthority{Scope: scope, Revisions: revisions, Graph: graph, Complete: true}, command
}

func refreshNormalPauseRevisions(authority *arena.NormalPauseAuthority, command *arena.NormalPauseCommand) {
	authority.Revisions = arena.PauseGraphRevisionsFrom(authority.Graph)
	command.Expected = clonePauseGraphRevisions(authority.Revisions)
}

func cloneNormalPauseAuthority(value arena.NormalPauseAuthority) arena.NormalPauseAuthority {
	value.Revisions = clonePauseGraphRevisions(value.Revisions)
	value.Graph = clonePauseGraph(value.Graph)
	return value
}

func cloneNormalPauseRecord(value arena.NormalPauseRecord) arena.NormalPauseRecord {
	value.Expected = clonePauseGraphRevisions(value.Expected)
	value.Graph = clonePauseGraph(value.Graph)
	return value
}

func clonePauseGraphRevisions(value arena.PauseGraphRevisions) arena.PauseGraphRevisions {
	value.Series = append([]arena.PauseChildRevision(nil), value.Series...)
	value.Games = append([]arena.PauseChildRevision(nil), value.Games...)
	value.Presence = append([]arena.PausePresenceRevision(nil), value.Presence...)
	value.Reconnect = append([]arena.PauseChildRevision(nil), value.Reconnect...)
	value.Counters = append([]arena.PauseReconnectCounterRevision(nil), value.Counters...)
	value.FrozenDeadlines = append([]arena.PauseFrozenDeadlineRevision(nil), value.FrozenDeadlines...)
	if value.Draft != nil {
		draft := *value.Draft
		value.Draft = &draft
	}
	return value
}

type countingArenaTransactionManager struct {
	mu    sync.Mutex
	calls int
}

func (m *countingArenaTransactionManager) Do(ctx context.Context, fn func(context.Context) error) error {
	m.mu.Lock()
	m.calls++
	m.mu.Unlock()
	return fn(ctx)
}

func (m *countingArenaTransactionManager) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.calls
}

type sequenceArenaClock struct {
	mu    sync.Mutex
	times []time.Time
	calls int
}

func (c *sequenceArenaClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	index := c.calls
	if index >= len(c.times) {
		index = len(c.times) - 1
	}
	c.calls++
	return c.times[index]
}

func (c *sequenceArenaClock) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func clonePauseGraph(value arena.PauseGraph) arena.PauseGraph {
	value.Tournament = *cloneTournamentRecord(value.Tournament)
	value.Wave.Wave.Members = append([]domain.ArenaWaveMember(nil), value.Wave.Wave.Members...)
	if value.Wave.Wave.ReadyWindow != nil {
		window := *value.Wave.Wave.ReadyWindow
		if window.ConsumedAt != nil {
			consumedAt := *window.ConsumedAt
			window.ConsumedAt = &consumedAt
		}
		value.Wave.Wave.ReadyWindow = &window
	}
	value.Series = append([]arena.PauseSeries(nil), value.Series...)
	for i := range value.Series {
		value.Series[i].Execution = cloneTestSeriesExecution(value.Series[i].Execution)
		if value.Series[i].CurrentGameID != nil {
			id := *value.Series[i].CurrentGameID
			value.Series[i].CurrentGameID = &id
		}
	}
	value.Games = append([]arena.PauseGame(nil), value.Games...)
	for i := range value.Games {
		if value.Games[i].Deadline != nil {
			deadline := *value.Games[i].Deadline
			value.Games[i].Deadline = &deadline
		}
		if value.Games[i].ResumeState != nil {
			state := *value.Games[i].ResumeState
			value.Games[i].ResumeState = &state
		}
	}
	if value.Draft != nil {
		draft := *value.Draft
		value.Draft = &draft
	}
	value.Presence = append([]arena.PausePresence(nil), value.Presence...)
	value.Reconnect = append([]arena.PauseReconnectInterval(nil), value.Reconnect...)
	value.Counters = append([]arena.PauseReconnectCounter(nil), value.Counters...)
	value.FrozenDeadlines = append([]arena.PauseFrozenDeadline(nil), value.FrozenDeadlines...)
	return value
}

func cloneTestSeriesExecution(value arena.SeriesExecution) arena.SeriesExecution {
	if value.ResumeState != nil {
		state := *value.ResumeState
		value.ResumeState = &state
	}
	value.Series.Slots = append([]domain.ArenaGameSlot(nil), value.Series.Slots...)
	for i := range value.Series.Slots {
		value.Series.Slots[i].Attempts = append([]domain.ArenaGame(nil), value.Series.Slots[i].Attempts...)
	}
	return value
}

var _ arena.NormalPauseRepository = (*normalPauseRepositoryFake)(nil)
