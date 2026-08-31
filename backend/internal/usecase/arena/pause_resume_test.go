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

func TestPauseResumeCAS(t *testing.T) {
	t.Parallel()

	resumedAt := time.Date(2026, time.August, 31, 13, 0, 0, 0, time.UTC)

	t.Run("restores the graph and shifts each frozen deadline once", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		liveAt := resumedAt.Add(-30 * time.Second)
		authority.Presence[0].PresenceEpoch++
		authority.Presence[0].Revision++
		authority.Presence[0].ConnectedAt = liveAt
		authority.Presence[0].UpdatedAt = liveAt
		command.Expected = arena.PauseResumeExpectationFrom(authority)
		repository := newPauseResumeRepositoryFake(authority)
		useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt})

		record, changed, err := useCase.Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Resume() error = %v, changed = %v", err, changed)
		}
		if record.State != arena.PauseStateResumed || record.Graph.Tournament.State != domain.ArenaTournamentStateSwiss ||
			record.Graph.Tournament.PausedFromState != nil || record.Graph.Wave.Wave.State != domain.ArenaWaveStateActive ||
			record.Graph.Series[0].Execution.Series.State != domain.ArenaSeriesStateActive || record.Graph.Series[0].Execution.ResumeState != nil ||
			record.Graph.Games[0].Game.State != domain.ArenaGameStateActive || record.Graph.Games[0].ResumeState != nil ||
			record.Graph.Games[0].Deadline == nil || !record.Graph.Games[0].Deadline.Equal(resumedAt.Add(time.Minute)) ||
			record.Graph.DeadlinesSuppressed || len(record.Graph.FrozenDeadlines) != 1 ||
			record.Graph.FrozenDeadlines[0].ResumedAt == nil || !record.Graph.FrozenDeadlines[0].ResumedAt.Equal(resumedAt) ||
			!reflect.DeepEqual(record.Graph.Presence, authority.Presence) || !reflect.DeepEqual(record.Graph.Reconnect, authority.Reconnect) ||
			!reflect.DeepEqual(record.Graph.Counters, authority.Counters) || record.Graph.TerminalActionRevision != authority.TerminalActionRevision {
			t.Fatalf("resumed record = %+v", record)
		}
		deadline := *record.Graph.Games[0].Deadline
		record.Graph.Games[0].Deadline = nil
		retried, changed, err := useCase.Resume(t.Context(), command)
		if err != nil || changed || retried.Graph.Games[0].Deadline == nil || !retried.Graph.Games[0].Deadline.Equal(deadline) || repository.writeCount() != 1 {
			t.Fatalf("retry error = %v, changed = %v, record = %+v, writes = %d", err, changed, retried, repository.writeCount())
		}
	})

	t.Run("shifts an open ready-window deadline exactly once", func(t *testing.T) {
		t.Parallel()

		authority, command := readyWindowPauseResumeFixture(t, resumedAt)
		remaining := authority.FrozenDeadlines[0].Remaining
		repository := newPauseResumeRepositoryFake(authority)
		useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt})
		record, changed, err := useCase.Resume(t.Context(), command)
		if err != nil || !changed || record.Graph.Wave.Wave.ReadyWindow == nil ||
			!record.Graph.Wave.Wave.ReadyWindow.Deadline.Equal(resumedAt.Add(remaining)) ||
			len(record.Graph.FrozenDeadlines) != 1 || record.Graph.FrozenDeadlines[0].Kind != arena.PauseDeadlineReadyWindow {
			t.Fatalf("Resume() error = %v, changed = %v, graph = %+v", err, changed, record)
		}
	})

	t.Run("restores an active Draft and fences its revision", func(t *testing.T) {
		t.Parallel()

		authority, command := draftPauseResumeFixture(t, resumedAt)
		remaining := authority.FrozenDeadlines[0].Remaining
		repository := newPauseResumeRepositoryFake(authority)
		useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt})
		record, changed, err := useCase.Resume(t.Context(), command)
		if err != nil || !changed || record.Graph.Draft == nil || record.Graph.Draft.State != arena.DraftExecutionStateActive ||
			record.Graph.Draft.AbsoluteDeadline == nil || !record.Graph.Draft.AbsoluteDeadline.Equal(resumedAt.Add(remaining)) ||
			record.Graph.Draft.RevisionID != command.DraftResultRevisionID ||
			len(record.Graph.FrozenDeadlines) != 1 || record.Graph.FrozenDeadlines[0].Kind != arena.PauseDeadlineDraft {
			t.Fatalf("Resume() error = %v, changed = %v, graph = %+v", err, changed, record)
		}

		authority, command = draftPauseResumeFixture(t, resumedAt)
		invalidIdentity := command
		invalidIdentity.DraftResultRevisionID = command.CommandID
		invalidRepository := newPauseResumeRepositoryFake(authority)
		invalidUseCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, invalidRepository, fixedArenaClock{now: resumedAt})
		if _, changed, err := invalidUseCase.Resume(t.Context(), invalidIdentity); !errors.Is(err, arena.ErrInvalidPauseResume) || changed {
			t.Fatalf("invalid Draft result identity error = %v, changed = %v", err, changed)
		}
		invalidDraftIdentity := command
		invalidDraftIdentity.DraftResultRevisionID = authority.Pause.Graph.Draft.ID
		invalidRepository = newPauseResumeRepositoryFake(authority)
		invalidUseCase = arena.NewPauseResumeUseCase(directArenaTransactionManager{}, invalidRepository, fixedArenaClock{now: resumedAt})
		if _, changed, err := invalidUseCase.Resume(t.Context(), invalidDraftIdentity); !errors.Is(err, arena.ErrInvalidPauseResume) || changed {
			t.Fatalf("Draft-owned result identity error = %v, changed = %v", err, changed)
		}
		command.Expected.Draft.Revision++
		repository = newPauseResumeRepositoryFake(authority)
		useCase = arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt})
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, arena.ErrPauseResumeConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("stale Draft Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}

		cyclePauseAuthority, cyclePauseCommand := draftRevisionTwoNormalPauseFixture(t, resumedAt.Add(-2*time.Minute))
		cycleAuthority, cycleCommand := pauseResumeFromNormalAuthority(t, cyclePauseAuthority, cyclePauseCommand, resumedAt.Add(-2*time.Minute))
		if cycleCommand.Expected.DraftPreviousRevisionID != cycleAuthority.Pause.Graph.Draft.PreviousRevisionID {
			t.Fatalf("expected previous Draft revision = %s, want %s", cycleCommand.Expected.DraftPreviousRevisionID, cycleAuthority.Pause.Graph.Draft.PreviousRevisionID)
		}
		cycleIDs := []uuid.UUID{
			cycleAuthority.Pause.Graph.Draft.ID,
			cycleAuthority.Pause.Graph.Draft.RevisionID,
			cycleAuthority.Pause.Graph.Draft.PreviousRevisionID,
		}
		for _, cycleID := range cycleIDs {
			candidate := cycleCommand
			candidate.DraftResultRevisionID = cycleID
			repository := newPauseResumeRepositoryFake(cycleAuthority)
			useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt})
			if _, changed, err := useCase.Resume(t.Context(), candidate); !errors.Is(err, arena.ErrInvalidPauseResume) || changed || repository.writeCount() != 0 {
				t.Fatalf("cycle identity %s error = %v, changed = %v, writes = %d", cycleID, err, changed, repository.writeCount())
			}
		}
		stalePrevious := cycleCommand
		stalePrevious.Expected.DraftPreviousRevisionID = uuid.New()
		repository = newPauseResumeRepositoryFake(cycleAuthority)
		useCase = arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt})
		if _, changed, err := useCase.Resume(t.Context(), stalePrevious); !errors.Is(err, arena.ErrPauseResumeConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("stale previous Draft revision error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("requires complete connected current evidence", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*arena.PauseResumeAuthority, *arena.PauseResumeCommand)
			want   error
		}{
			{name: "stale Game revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.Games[0].Revision++ }, want: arena.ErrPauseResumeConflict},
			{name: "stale graph revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.GraphRevision++ }, want: arena.ErrPauseResumeConflict},
			{name: "stale pause revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.PauseRevision++ }, want: arena.ErrPauseResumeConflict},
			{name: "wrong pause identity", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.PauseID = uuid.New() }, want: arena.ErrInvalidPauseResume},
			{name: "stale Tournament revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.TournamentRevision++ }, want: arena.ErrPauseResumeConflict},
			{name: "wrong Tournament resume state", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				c.Expected.TournamentState = domain.ArenaTournamentStatePlayoffs
			}, want: arena.ErrPauseResumeConflict},
			{name: "stale Wave revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.WaveRevision++ }, want: arena.ErrPauseResumeConflict},
			{name: "stale Series revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.Series[0].Revision++ }, want: arena.ErrPauseResumeConflict},
			{name: "stale Presence epoch", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				c.Expected.Presence[0].PresenceEpoch++
			}, want: arena.ErrPauseResumeConflict},
			{name: "incomplete expected Presence identity", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				c.Expected.Presence[0].ID = uuid.Nil
			}, want: arena.ErrInvalidPauseResume},
			{name: "stale Reconnect revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.Reconnect[0].Revision++ }, want: arena.ErrPauseResumeConflict},
			{name: "stale counter revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.Counters[0].Revision++ }, want: arena.ErrPauseResumeConflict},
			{name: "stale frozen deadline revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				c.Expected.FrozenDeadlines[0].Revision++
			}, want: arena.ErrPauseResumeConflict},
			{name: "stale terminal action revision", mutate: func(_ *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) { c.Expected.TerminalActionRevision++ }, want: arena.ErrPauseResumeConflict},
			{name: "missing participant", mutate: func(a *arena.PauseResumeAuthority, _ *arena.PauseResumeCommand) { a.Presence = a.Presence[:1] }, want: arena.ErrPauseResumeIncomplete},
			{name: "foreign Presence identity", mutate: func(a *arena.PauseResumeAuthority, _ *arena.PauseResumeCommand) { a.Presence[0].ID = uuid.New() }, want: arena.ErrPauseResumeIncomplete},
			{name: "foreign Presence Series", mutate: func(a *arena.PauseResumeAuthority, _ *arena.PauseResumeCommand) { a.Presence[0].SeriesID = uuid.New() }, want: arena.ErrPauseResumeIncomplete},
			{name: "disconnected participant", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				a.Presence[0].State = arena.PresenceStateDisconnected
				at := resumedAt.Add(-time.Second)
				a.Presence[0].DisconnectedAt = &at
				a.Presence[0].UpdatedAt = at
				a.Presence[0].PresenceEpoch++
				a.Presence[0].Revision++
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrPauseResumePresence},
			{name: "open reconnect interval", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				a.Reconnect[0].State = arena.ReconnectStateOpen
				a.Reconnect[0].ClosedAt = nil
				a.Reconnect[0].Deadline = resumedAt.Add(time.Minute)
				a.Pause.Graph.Reconnect[0] = a.Reconnect[0]
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrPauseResumePresence},
			{name: "terminal Reconnect update precedes its close", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				updatedAt := a.Reconnect[0].ClosedAt.Add(-time.Second)
				a.Reconnect[0].UpdatedAt = updatedAt
				a.Pause.Graph.Reconnect[0].UpdatedAt = updatedAt
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrInvalidPauseResume},
			{name: "missing terminal Reconnect row", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				a.Reconnect = nil
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrPauseResumeIncomplete},
			{name: "extra Reconnect row", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				extra := a.Reconnect[0]
				extra.ID = uuid.New()
				a.Reconnect = append(a.Reconnect, extra)
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrPauseResumeIncomplete},
			{name: "duplicate Reconnect row", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				a.Reconnect = append(a.Reconnect, a.Reconnect[0])
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrInvalidPauseResume},
			{name: "foreign Reconnect owner", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				a.Reconnect[0].ParticipantID = uuid.New()
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrPauseResumeIncomplete},
			{name: "missing frozen deadline", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				a.FrozenDeadlines = nil
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrPauseResumeIncomplete},
			{name: "frozen deadline revision overflow", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				a.Pause.Graph.FrozenDeadlines[0].Revision = math.MaxInt64
				a.FrozenDeadlines[0].Revision = math.MaxInt64
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrPauseResumeOverflow},
			{name: "Game revision overflow", mutate: func(a *arena.PauseResumeAuthority, c *arena.PauseResumeCommand) {
				a.Pause.Expected.Games[0].Revision = math.MaxInt64 - 1
				a.Pause.Graph.Games[0].Revision = math.MaxInt64
				c.Expected = arena.PauseResumeExpectationFrom(*a)
			}, want: arena.ErrPauseResumeOverflow},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := pauseResumeFixture(t, resumedAt)
				test.mutate(&authority, &command)
				repository := newPauseResumeRepositoryFake(authority)
				useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt})
				if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("rejects shifted deadline Unix-nanosecond overflow", func(t *testing.T) {
		t.Parallel()

		boundary := time.Unix(0, math.MaxInt64-int64(30*time.Second)).UTC()
		authority, command := pauseResumeFixture(t, boundary)
		repository := newPauseResumeRepositoryFake(authority)
		useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: boundary})
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, arena.ErrPauseResumeOverflow) || changed || repository.writeCount() != 0 {
			t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects resume time before durable history", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*arena.PauseResumeAuthority)
			now    func(arena.PauseResumeAuthority) time.Time
		}{
			{name: "active pause and Tournament", now: func(a arena.PauseResumeAuthority) time.Time {
				return a.Pause.Graph.Tournament.UpdatedAt.Add(-time.Second)
			}},
			{name: "Reconnect update", mutate: func(a *arena.PauseResumeAuthority) {
				future := resumedAt.Add(time.Second)
				a.Reconnect[0].ClosedAt = &future
				a.Reconnect[0].UpdatedAt = future
				a.Pause.Graph.Reconnect[0] = a.Reconnect[0]
			}, now: func(arena.PauseResumeAuthority) time.Time { return resumedAt }},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := pauseResumeFixture(t, resumedAt)
				if test.mutate != nil {
					test.mutate(&authority)
					command.Expected = arena.PauseResumeExpectationFrom(authority)
				}
				repository := newPauseResumeRepositoryFake(authority)
				useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: test.now(authority)})
				if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, arena.ErrInvalidPauseResume) || changed || repository.writeCount() != 0 {
					t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("uses a second command lookup after locking authority", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		leaderRepository := newPauseResumeRepositoryFake(authority)
		leader := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: resumedAt})
		stored, changed, err := leader.Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored resume: error = %v, changed = %v", err, changed)
		}

		followerRepository := newPauseResumeRepositoryFake(authority)
		followerRepository.blockNextLoad()
		follower := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, followerRepository, fixedArenaClock{now: resumedAt.Add(time.Second)})
		type result struct {
			record  *arena.PauseResumeRecord
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
		repository := newPauseResumeRepositoryFake(authority)
		repository.conflictsRemaining = 2
		transactions := &countingArenaTransactionManager{}
		clock := &sequenceArenaClock{times: []time.Time{resumedAt, resumedAt.Add(time.Second)}}
		useCase := arena.NewPauseResumeUseCase(transactions, repository, clock)
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, arena.ErrPauseResumeConflict) || changed || repository.loadCount != 2 || transactions.count() != 2 || clock.count() != 2 {
			t.Fatalf("Resume() error = %v, changed = %v, loads = %d, transactions = %d, clock calls = %d", err, changed, repository.loadCount, transactions.count(), clock.count())
		}
	})

	t.Run("rejects a malformed stored resume graph", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		leaderRepository := newPauseResumeRepositoryFake(authority)
		leader := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: resumedAt})
		stored, changed, err := leader.Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored resume: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Presence[0].PresenceEpoch++
		stored.Graph.Presence[0].Revision++
		stored.Graph.Presence[0].ConnectedAt = resumedAt
		stored.Graph.Presence[0].UpdatedAt = resumedAt
		repository := newPauseResumeRepositoryFake(authority)
		repository.storeCommand(*stored)
		useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt.Add(time.Second)})
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, arena.ErrPauseResumeCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects structurally valid stored resume records with wrong transitions", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*arena.PauseResumeRecord)
		}{
			{name: "Tournament time was not updated", mutate: func(record *arena.PauseResumeRecord) {
				record.Graph.Tournament.UpdatedAt = record.ResumedAt.Add(-time.Second)
			}},
			{name: "Tournament restored to another origin", mutate: func(record *arena.PauseResumeRecord) {
				record.Graph.Tournament.State = domain.ArenaTournamentStatePlayoffs
			}},
			{name: "Wave state changed without revision", mutate: func(record *arena.PauseResumeRecord) {
				record.Graph.Wave.Revision = record.Expected.WaveRevision
			}},
			{name: "Series state changed without revision", mutate: func(record *arena.PauseResumeRecord) {
				record.Graph.Series[0].Revision = record.Expected.Series[0].Revision
			}},
			{name: "Game state changed without revision", mutate: func(record *arena.PauseResumeRecord) {
				record.Graph.Games[0].Revision = record.Expected.Games[0].Revision
			}},
			{name: "Tournament scope differs from record", mutate: func(record *arena.PauseResumeRecord) {
				record.Graph.Scope.Authority.HolderID = uuid.New()
			}},
			{name: "same-revision Presence is disconnected", mutate: func(record *arena.PauseResumeRecord) {
				disconnectedAt := record.ResumedAt
				record.Graph.Presence[0].State = arena.PresenceStateDisconnected
				record.Graph.Presence[0].DisconnectedAt = &disconnectedAt
				record.Graph.Presence[0].UpdatedAt = disconnectedAt
			}},
			{name: "same-revision Reconnect is open", mutate: func(record *arena.PauseResumeRecord) {
				record.Graph.Reconnect[0].State = arena.ReconnectStateOpen
				record.Graph.Reconnect[0].ClosedAt = nil
			}},
			{name: "frozen deadline shifted at another time", mutate: func(record *arena.PauseResumeRecord) {
				shiftedAt := record.ResumedAt.Add(-time.Second)
				shiftedDeadline := shiftedAt.Add(record.Graph.FrozenDeadlines[0].Remaining)
				record.Graph.FrozenDeadlines[0].ResumedAt = &shiftedAt
				record.Graph.FrozenDeadlines[0].ResumedDeadline = &shiftedDeadline
				record.Graph.Games[0].Deadline = &shiftedDeadline
			}},
			{name: "Wave contains future durable evidence", mutate: func(record *arena.PauseResumeRecord) {
				startedAt := record.ResumedAt.Add(time.Second)
				record.Graph.Wave.Wave.StartedAt = &startedAt
			}},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := pauseResumeFixture(t, resumedAt)
				leaderRepository := newPauseResumeRepositoryFake(authority)
				leader := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: resumedAt})
				stored, changed, err := leader.Resume(t.Context(), command)
				if err != nil || !changed {
					t.Fatalf("prepare stored resume: error = %v, changed = %v", err, changed)
				}
				test.mutate(stored)
				repository := newPauseResumeRepositoryFake(authority)
				repository.storeCommand(*stored)
				useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt.Add(time.Second)})
				if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, arena.ErrPauseResumeCommandReuse) || changed {
					t.Fatalf("Resume() error = %v, changed = %v", err, changed)
				}
			})
		}
	})

	t.Run("rejects a stored Draft resume without its exact revision lineage", func(t *testing.T) {
		t.Parallel()

		authority, command := draftPauseResumeFixture(t, resumedAt)
		leaderRepository := newPauseResumeRepositoryFake(authority)
		leader := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: resumedAt})
		stored, changed, err := leader.Resume(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored Draft resume: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Draft.Revision = stored.Expected.Draft.Revision
		stored.Graph.Draft.RevisionID = stored.Expected.Draft.RevisionID
		stored.Graph.Draft.PreviousRevisionID = uuid.New()
		repository := newPauseResumeRepositoryFake(authority)
		repository.storeCommand(*stored)
		useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt.Add(time.Second)})
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, arena.ErrPauseResumeCommandReuse) || changed {
			t.Fatalf("Resume() error = %v, changed = %v", err, changed)
		}

		authority, command = draftPauseResumeFixture(t, resumedAt)
		leaderRepository = newPauseResumeRepositoryFake(authority)
		leader = arena.NewPauseResumeUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: resumedAt})
		stored, changed, err = leader.Resume(t.Context(), command)
		if err != nil || !changed || stored.Graph.Draft == nil || stored.Graph.Draft.Transition == nil {
			t.Fatalf("prepare stored Draft transition: error = %v, changed = %v", err, changed)
		}
		stored.Graph.Draft.Transition.Reason = "wrong reason"
		repository = newPauseResumeRepositoryFake(authority)
		repository.storeCommand(*stored)
		useCase = arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt.Add(time.Second)})
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, arena.ErrPauseResumeCommandReuse) || changed {
			t.Fatalf("transition replay error = %v, changed = %v", err, changed)
		}
	})

	t.Run("rejects changed lineage for a no-op Draft resume", func(t *testing.T) {
		t.Parallel()

		pausedAt := resumedAt.Add(-2 * time.Minute)
		pauseAuthority, pauseCommand := completedDraftNormalPauseFixture(t, pausedAt)
		authority, command := pauseResumeFromNormalAuthority(t, pauseAuthority, pauseCommand, pausedAt)
		leaderRepository := newPauseResumeRepositoryFake(authority)
		leader := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: resumedAt})
		stored, changed, err := leader.Resume(t.Context(), command)
		if err != nil || !changed || stored.Graph.Draft == nil || stored.Graph.Draft.State != arena.DraftExecutionStateCompleted {
			t.Fatalf("prepare stored completed Draft: error = %v, changed = %v", err, changed)
		}
		if stored.Expected.DraftPreviousRevisionID != stored.Graph.Draft.PreviousRevisionID {
			t.Fatalf("stored lineage = %s, expected = %s", stored.Graph.Draft.PreviousRevisionID, stored.Expected.DraftPreviousRevisionID)
		}
		stored.Graph.Draft.PreviousRevisionID = uuid.New()
		repository := newPauseResumeRepositoryFake(authority)
		repository.storeCommand(*stored)
		useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt.Add(time.Second)})
		if _, changed, err := useCase.Resume(t.Context(), command); !errors.Is(err, arena.ErrPauseResumeCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Resume() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("retries fresh authority and rejects command reuse", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		repository := newPauseResumeRepositoryFake(authority)
		repository.conflictOnce = true
		useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt})
		if _, changed, err := useCase.Resume(t.Context(), command); err != nil || !changed || repository.loadCount != 2 {
			t.Fatalf("retry error = %v, changed = %v, loads = %d", err, changed, repository.loadCount)
		}
		reused := command
		reused.ActorID = uuid.New()
		if _, changed, err := useCase.Resume(t.Context(), reused); !errors.Is(err, arena.ErrPauseResumeCommandReuse) || changed {
			t.Fatalf("reuse error = %v, changed = %v", err, changed)
		}
	})

	t.Run("wraps repository errors", func(t *testing.T) {
		t.Parallel()

		authority, command := pauseResumeFixture(t, resumedAt)
		cause := errors.New("resume storage unavailable")
		repository := newPauseResumeRepositoryFake(authority)
		repository.loadErr = cause
		useCase := arena.NewPauseResumeUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: resumedAt})
		if _, _, err := useCase.Resume(t.Context(), command); !errors.Is(err, cause) {
			t.Fatalf("Resume() error = %v, want wrapped cause", err)
		}
	})
}

type pauseResumeRepositoryFake struct {
	mu                 sync.Mutex
	authority          arena.PauseResumeAuthority
	commands           map[uuid.UUID]arena.PauseResumeRecord
	conflictOnce       bool
	conflictsRemaining int
	loadErr            error
	loadCount          int
	writes             int
	commitCalls        int
	loadStarted        chan struct{}
	loadRelease        chan struct{}
	loadOnce           sync.Once
}

func newPauseResumeRepositoryFake(authority arena.PauseResumeAuthority) *pauseResumeRepositoryFake {
	return &pauseResumeRepositoryFake{authority: clonePauseResumeAuthority(authority), commands: make(map[uuid.UUID]arena.PauseResumeRecord)}
}

func (f *pauseResumeRepositoryFake) FindPauseResumeCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*arena.PauseResumeRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.commands[commandID]
	if !ok || record.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	clone := clonePauseResumeRecord(record)
	return &clone, nil
}

func (f *pauseResumeRepositoryFake) LoadPauseResumeAuthority(_ context.Context, _ arena.PauseGraphScope, _ uuid.UUID) (arena.PauseResumeAuthority, error) {
	f.mu.Lock()
	f.loadCount++
	if f.loadErr != nil {
		f.mu.Unlock()
		return arena.PauseResumeAuthority{}, f.loadErr
	}
	started, release := f.loadStarted, f.loadRelease
	f.mu.Unlock()
	if started != nil {
		f.loadOnce.Do(func() { close(started) })
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return clonePauseResumeAuthority(f.authority), nil
}

func (f *pauseResumeRepositoryFake) CommitPauseResume(_ context.Context, expected arena.PauseResumeExpectation, record arena.PauseResumeRecord) (*arena.PauseResumeRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if existing, ok := f.commands[record.CommandID]; ok {
		clone := clonePauseResumeRecord(existing)
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
	current := arena.PauseResumeExpectationFrom(f.authority)
	normalizePauseResumeExpectation(&expected)
	normalizePauseResumeExpectation(&current)
	if !reflect.DeepEqual(expected, current) {
		return nil, false, domain.ErrConflict
	}
	f.writes++
	f.commands[record.CommandID] = clonePauseResumeRecord(record)
	f.authority.Pause = arena.NormalPauseRecord{Scope: record.Scope, PauseID: record.PauseID, State: record.State, Revision: record.Revision, Graph: clonePauseGraph(record.Graph)}
	clone := clonePauseResumeRecord(record)
	return &clone, true, nil
}

func normalizePauseResumeExpectation(value *arena.PauseResumeExpectation) {
	if len(value.Series) == 0 {
		value.Series = nil
	}
	if len(value.Games) == 0 {
		value.Games = nil
	}
	if len(value.Presence) == 0 {
		value.Presence = nil
	}
	if len(value.Reconnect) == 0 {
		value.Reconnect = nil
	}
	if len(value.Counters) == 0 {
		value.Counters = nil
	}
	if len(value.FrozenDeadlines) == 0 {
		value.FrozenDeadlines = nil
	}
}

func (f *pauseResumeRepositoryFake) blockNextLoad() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadStarted = make(chan struct{})
	f.loadRelease = make(chan struct{})
}

func (f *pauseResumeRepositoryFake) storeCommand(record arena.PauseResumeRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[record.CommandID] = clonePauseResumeRecord(record)
}

func (f *pauseResumeRepositoryFake) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func pauseResumeFixture(t *testing.T, now time.Time) (arena.PauseResumeAuthority, arena.PauseResumeCommand) {
	t.Helper()
	authority, command := normalPauseFixture(now.Add(-2 * time.Minute))
	repository := newNormalPauseRepositoryFake(authority)
	useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: now.Add(-2 * time.Minute)})
	pause, changed, err := useCase.Enter(t.Context(), command)
	if err != nil || !changed {
		t.Fatalf("enter fixture pause: error = %v, changed = %v", err, changed)
	}
	resumeAuthority := arena.PauseResumeAuthority{
		Pause:                  *pause,
		Presence:               append([]arena.PausePresence(nil), pause.Graph.Presence...),
		Reconnect:              append([]arena.PauseReconnectInterval(nil), pause.Graph.Reconnect...),
		Counters:               append([]arena.PauseReconnectCounter(nil), pause.Graph.Counters...),
		FrozenDeadlines:        append([]arena.PauseFrozenDeadline(nil), pause.Graph.FrozenDeadlines...),
		TerminalActionRevision: pause.Graph.TerminalActionRevision,
	}
	resumeCommand := arena.PauseResumeCommand{
		Scope: pause.Scope, PauseID: pause.PauseID, CommandID: uuid.New(), ActorID: uuid.New(),
		Expected: arena.PauseResumeExpectationFrom(resumeAuthority),
	}
	return resumeAuthority, resumeCommand
}

func readyWindowPauseResumeFixture(t *testing.T, resumedAt time.Time) (arena.PauseResumeAuthority, arena.PauseResumeCommand) {
	t.Helper()
	pausedAt := resumedAt.Add(-2 * time.Minute)
	authority, command := normalPauseFixture(pausedAt)
	windowOpenedAt := pausedAt.Add(-time.Minute)
	windowDeadline := pausedAt.Add(3 * time.Minute)
	authority.Graph.Wave.Wave.State = domain.ArenaWaveStateReadyWindowOpen
	authority.Graph.Wave.Wave.StartedAt = nil
	authority.Graph.Wave.Wave.PausedAt = nil
	for index := range authority.Graph.Wave.Wave.Members {
		authority.Graph.Wave.Wave.Members[index].Ready = false
	}
	authority.Graph.Wave.Wave.ReadyWindow.State = domain.ArenaReadyWindowStateOpen
	authority.Graph.Wave.Wave.ReadyWindow.OpenedAt = windowOpenedAt
	authority.Graph.Wave.Wave.ReadyWindow.Deadline = windowDeadline
	authority.Graph.Wave.Wave.ReadyWindow.ConsumedAt = nil
	authority.Graph.Series[0].Execution.Series.State = domain.ArenaSeriesStatePlanned
	authority.Graph.Series[0].CurrentGameID = nil
	authority.Graph.Series[0].Execution.Series.Slots[0].Attempts[0].State = domain.ArenaGameStatePlanned
	authority.Graph.Games = nil
	authority.Graph.Reconnect = nil
	authority.Graph.Counters[0].Used = 0
	refreshNormalPauseRevisions(&authority, &command)
	return pauseResumeFromNormalAuthority(t, authority, command, pausedAt)
}

func draftPauseResumeFixture(t *testing.T, resumedAt time.Time) (arena.PauseResumeAuthority, arena.PauseResumeCommand) {
	t.Helper()
	pausedAt := resumedAt.Add(-2 * time.Minute)
	authority, command := draftNormalPauseFixture(t, pausedAt)
	return pauseResumeFromNormalAuthority(t, authority, command, pausedAt)
}

func draftNormalPauseFixture(t *testing.T, pausedAt time.Time) (arena.NormalPauseAuthority, arena.NormalPauseCommand) {
	t.Helper()
	authority, command := normalPauseFixture(pausedAt)
	draft := task030Draft(t, pausedAt)
	series := &authority.Graph.Series[0]
	series.Execution.Series.ID = draft.SeriesID
	series.Execution.Series.FirstParticipantID = draft.FirstParticipantID
	series.Execution.Series.SecondParticipantID = draft.SecondParticipantID
	series.Execution.Series.State = domain.ArenaSeriesStateDraft
	series.Execution.Series.Slots[0].SeriesID = draft.SeriesID
	series.Execution.Series.Slots[0].Attempts[0].State = domain.ArenaGameStatePlanned
	series.CurrentGameID = nil
	authority.Graph.Wave.Wave.Members[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Wave.Wave.Members[1].ParticipantID = draft.SecondParticipantID
	authority.Graph.Presence[0].SeriesID = draft.SeriesID
	authority.Graph.Presence[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Presence[1].SeriesID = draft.SeriesID
	authority.Graph.Presence[1].ParticipantID = draft.SecondParticipantID
	authority.Graph.Games = nil
	authority.Graph.Reconnect = nil
	authority.Graph.Counters[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Counters[0].Used = 0
	authority.Graph.Draft = &draft
	refreshNormalPauseRevisions(&authority, &command)
	command.DraftResultRevisionID = uuid.New()
	return authority, command
}

func draftRevisionTwoNormalPauseFixture(t *testing.T, pausedAt time.Time) (arena.NormalPauseAuthority, arena.NormalPauseCommand) {
	t.Helper()
	authority, command := draftNormalPauseFixture(t, pausedAt)
	initial := task030Draft(t, pausedAt.Add(-2*time.Second))
	repository := newDraftRepositoryFake(initial)
	useCase := arena.NewDraftActionUseCase(repository, task030Clock{at: pausedAt.Add(-time.Second)})
	result, err := useCase.Apply(t.Context(), task030ActionCommand(initial, uuid.New(), domain.CategoryWeb))
	if err != nil || !result.Changed || result.Draft.Revision != 2 {
		t.Fatalf("prepare revision-two Draft: result = %+v, error = %v", result, err)
	}
	series := &authority.Graph.Series[0].Execution.Series
	series.FirstParticipantID = result.Draft.FirstParticipantID
	series.SecondParticipantID = result.Draft.SecondParticipantID
	authority.Graph.Wave.Wave.Members[0].ParticipantID = result.Draft.FirstParticipantID
	authority.Graph.Wave.Wave.Members[1].ParticipantID = result.Draft.SecondParticipantID
	authority.Graph.Presence[0].ParticipantID = result.Draft.FirstParticipantID
	authority.Graph.Presence[1].ParticipantID = result.Draft.SecondParticipantID
	if result.Draft.SeriesID != series.ID || result.Draft.FirstParticipantID != series.FirstParticipantID ||
		result.Draft.SecondParticipantID != series.SecondParticipantID || result.Draft.State != arena.DraftExecutionStateActive {
		t.Fatalf("revision-two Draft ownership = %+v, Series = %+v", result.Draft, *series)
	}
	authority.Graph.Draft = &result.Draft
	refreshNormalPauseRevisions(&authority, &command)
	command.DraftResultRevisionID = uuid.New()
	return authority, command
}

func completedDraftNormalPauseFixture(t *testing.T, pausedAt time.Time) (arena.NormalPauseAuthority, arena.NormalPauseCommand) {
	t.Helper()
	authority, command := draftNormalPauseFixture(t, pausedAt)
	startedAt := pausedAt.Add(-3 * time.Second)
	draft := completeTask030Draft(t, task030Draft(t, startedAt), startedAt)
	series := &authority.Graph.Series[0].Execution.Series
	series.FirstParticipantID = draft.FirstParticipantID
	series.SecondParticipantID = draft.SecondParticipantID
	authority.Graph.Wave.Wave.Members[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Wave.Wave.Members[1].ParticipantID = draft.SecondParticipantID
	authority.Graph.Presence[0].ParticipantID = draft.FirstParticipantID
	authority.Graph.Presence[1].ParticipantID = draft.SecondParticipantID
	authority.Graph.Draft = &draft
	refreshNormalPauseRevisions(&authority, &command)
	command.DraftResultRevisionID = uuid.New()
	return authority, command
}

func pauseResumeFromNormalAuthority(t *testing.T, authority arena.NormalPauseAuthority, pauseCommand arena.NormalPauseCommand, pausedAt time.Time) (arena.PauseResumeAuthority, arena.PauseResumeCommand) {
	t.Helper()
	repository := newNormalPauseRepositoryFake(authority)
	useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: pausedAt})
	pause, changed, err := useCase.Enter(t.Context(), pauseCommand)
	if err != nil || !changed {
		t.Fatalf("enter fixture pause: error = %v, changed = %v", err, changed)
	}
	resumeAuthority := pauseResumeAuthorityFromRecord(*pause)
	resumeCommand := arena.PauseResumeCommand{
		Scope: pause.Scope, PauseID: pause.PauseID, CommandID: uuid.New(), ActorID: uuid.New(),
		Expected: arena.PauseResumeExpectationFrom(resumeAuthority),
	}
	if resumeAuthority.Pause.Graph.Draft != nil {
		resumeCommand.DraftResultRevisionID = uuid.New()
	}
	return resumeAuthority, resumeCommand
}

func pauseResumeAuthorityFromRecord(pause arena.NormalPauseRecord) arena.PauseResumeAuthority {
	return arena.PauseResumeAuthority{
		Pause:                  pause,
		Presence:               append([]arena.PausePresence(nil), pause.Graph.Presence...),
		Reconnect:              append([]arena.PauseReconnectInterval(nil), pause.Graph.Reconnect...),
		Counters:               append([]arena.PauseReconnectCounter(nil), pause.Graph.Counters...),
		FrozenDeadlines:        append([]arena.PauseFrozenDeadline(nil), pause.Graph.FrozenDeadlines...),
		TerminalActionRevision: pause.Graph.TerminalActionRevision,
	}
}

func clonePauseResumeAuthority(value arena.PauseResumeAuthority) arena.PauseResumeAuthority {
	value.Pause = cloneNormalPauseRecord(value.Pause)
	value.Presence = append([]arena.PausePresence(nil), value.Presence...)
	value.Reconnect = append([]arena.PauseReconnectInterval(nil), value.Reconnect...)
	value.Counters = append([]arena.PauseReconnectCounter(nil), value.Counters...)
	value.FrozenDeadlines = append([]arena.PauseFrozenDeadline(nil), value.FrozenDeadlines...)
	return value
}

func clonePauseResumeRecord(value arena.PauseResumeRecord) arena.PauseResumeRecord {
	value.Expected = clonePauseResumeExpectation(value.Expected)
	value.Graph = clonePauseGraph(value.Graph)
	return value
}

func clonePauseResumeExpectation(value arena.PauseResumeExpectation) arena.PauseResumeExpectation {
	value.Games = append([]arena.PauseChildRevision(nil), value.Games...)
	value.Series = append([]arena.PauseChildRevision(nil), value.Series...)
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

var _ arena.PauseResumeRepository = (*pauseResumeRepositoryFake)(nil)
