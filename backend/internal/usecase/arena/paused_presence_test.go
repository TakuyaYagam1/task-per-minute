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

func TestPausedPresenceUpdates(t *testing.T) {
	t.Parallel()

	changedAt := time.Date(2026, time.August, 31, 12, 0, 0, 0, time.UTC)

	t.Run("flips only durable Presence under an active normal pause", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		before := clonePausedPresenceAuthority(authority)
		repository := newPausedPresenceRepositoryFake(authority)
		useCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt.In(time.FixedZone("operator", 3*60*60))})

		record, changed, err := useCase.Change(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Change() error = %v, changed = %v", err, changed)
		}
		presence := record.Authority.Presence
		if presence.State != arena.PresenceStateDisconnected || presence.PresenceEpoch != before.Presence.PresenceEpoch+1 ||
			presence.Revision != before.Presence.Revision+1 || presence.DisconnectedAt == nil || !presence.DisconnectedAt.Equal(changedAt) ||
			!presence.UpdatedAt.Equal(changedAt) {
			t.Fatalf("Presence = %+v", presence)
		}
		if !pausedPresenceImmutableEqual(before, record.Authority) {
			t.Fatal("Presence change altered reconnect, counters, clocks, pause, or terminal evidence")
		}

		connect := command
		connect.CommandID = uuid.New()
		connect.NextState = arena.PresenceStateConnected
		connect.ExpectedGraphRevision = record.Authority.Pause.Graph.Revision
		connect.ExpectedPauseRevision = record.Authority.Pause.Revision
		connect.ExpectedPresenceEpoch = record.Authority.Presence.PresenceEpoch
		connect.ExpectedPresenceRevision = record.Authority.Presence.Revision
		connected, changed, err := useCase.Change(t.Context(), connect)
		if err != nil || !changed || connected.Authority.Presence.State != arena.PresenceStateConnected ||
			connected.Authority.Presence.DisconnectedAt != nil || connected.Authority.Presence.PresenceEpoch != presence.PresenceEpoch+1 {
			t.Fatalf("connect error = %v, changed = %v, record = %+v", err, changed, connected)
		}

		record.Authority.Reconnect = append(record.Authority.Reconnect, arena.PauseReconnectInterval{ID: uuid.New()})
		retried, changed, err := useCase.Change(t.Context(), command)
		if err != nil || changed || len(retried.Authority.Reconnect) != len(before.Reconnect) || repository.writeCount() != 2 {
			t.Fatalf("retry error = %v, changed = %v, record = %+v, writes = %d", err, changed, retried, repository.writeCount())
		}
	})

	t.Run("fails closed before a Presence write", func(t *testing.T) {
		t.Parallel()

		tests := []struct {
			name   string
			mutate func(*arena.PausedPresenceAuthority, *arena.PausedPresenceCommand)
			want   error
		}{
			{name: "disconnect pause", mutate: func(a *arena.PausedPresenceAuthority, _ *arena.PausedPresenceCommand) {
				a.Pause.Reason = arena.PauseReasonDisconnect
			}, want: arena.ErrPausedPresenceSuppression},
			{name: "resolved pause", mutate: func(a *arena.PausedPresenceAuthority, _ *arena.PausedPresenceCommand) {
				a.Pause.State = arena.PauseStateResumed
			}, want: arena.ErrPausedPresenceSuppression},
			{name: "pause record reuses actor identity", mutate: func(a *arena.PausedPresenceAuthority, _ *arena.PausedPresenceCommand) {
				a.Pause.ActorID = a.Pause.CommandID
			}, want: arena.ErrPausedPresenceSuppression},
			{name: "foreign participant", mutate: func(_ *arena.PausedPresenceAuthority, c *arena.PausedPresenceCommand) { c.ParticipantID = uuid.New() }, want: arena.ErrPausedPresenceConflict},
			{name: "foreign Presence row", mutate: func(a *arena.PausedPresenceAuthority, _ *arena.PausedPresenceCommand) { a.Presence.ID = uuid.New() }, want: arena.ErrPausedPresenceConflict},
			{name: "Presence belongs to another Tournament", mutate: func(a *arena.PausedPresenceAuthority, _ *arena.PausedPresenceCommand) {
				a.Presence.TournamentID = uuid.New()
			}, want: arena.ErrPausedPresenceConflict},
			{name: "Presence belongs to another Roster", mutate: func(a *arena.PausedPresenceAuthority, _ *arena.PausedPresenceCommand) {
				a.Presence.RosterID = uuid.New()
			}, want: arena.ErrPausedPresenceConflict},
			{name: "Presence belongs to another Series", mutate: func(a *arena.PausedPresenceAuthority, _ *arena.PausedPresenceCommand) {
				a.Presence.SeriesID = uuid.New()
			}, want: arena.ErrPausedPresenceConflict},
			{name: "stale graph", mutate: func(_ *arena.PausedPresenceAuthority, c *arena.PausedPresenceCommand) { c.ExpectedGraphRevision++ }, want: arena.ErrPausedPresenceConflict},
			{name: "stale epoch", mutate: func(_ *arena.PausedPresenceAuthority, c *arena.PausedPresenceCommand) { c.Scope.Authority.Epoch++ }, want: arena.ErrPausedPresenceConflict},
			{name: "same state", mutate: func(_ *arena.PausedPresenceAuthority, c *arena.PausedPresenceCommand) {
				c.NextState = arena.PresenceStateConnected
			}, want: arena.ErrPausedPresenceState},
			{name: "loaded Presence is behind the pause snapshot", mutate: func(a *arena.PausedPresenceAuthority, c *arena.PausedPresenceCommand) {
				a.Presence.PresenceEpoch--
				a.Presence.Revision--
				c.ExpectedPresenceEpoch = a.Presence.PresenceEpoch
				c.ExpectedPresenceRevision = a.Presence.Revision
			}, want: arena.ErrPausedPresenceConflict},
			{name: "loaded Presence epoch and revision deltas differ", mutate: func(a *arena.PausedPresenceAuthority, c *arena.PausedPresenceCommand) {
				a.Presence.PresenceEpoch++
				c.ExpectedPresenceEpoch = a.Presence.PresenceEpoch
			}, want: arena.ErrPausedPresenceConflict},
			{name: "unchanged Presence differs from snapshot", mutate: func(a *arena.PausedPresenceAuthority, _ *arena.PausedPresenceCommand) {
				a.Presence.UpdatedAt = changedAt.Add(-time.Second)
			}, want: arena.ErrPausedPresenceConflict},
			{name: "advanced Presence predates the active pause", mutate: func(a *arena.PausedPresenceAuthority, c *arena.PausedPresenceCommand) {
				a.Presence.PresenceEpoch++
				a.Presence.Revision++
				a.Presence.UpdatedAt = a.Pause.PausedAt.Add(-time.Second)
				c.ExpectedPresenceEpoch = a.Presence.PresenceEpoch
				c.ExpectedPresenceRevision = a.Presence.Revision
			}, want: arena.ErrPausedPresenceConflict},
			{name: "counter overflow", mutate: func(a *arena.PausedPresenceAuthority, c *arena.PausedPresenceCommand) {
				snapshot := &a.Pause.Graph.Presence[0]
				snapshot.Revision = snapshot.PresenceEpoch
				a.Pause.Expected.Presence[0].Revision = snapshot.Revision
				a.Presence.PresenceEpoch = math.MaxInt64
				a.Presence.Revision = math.MaxInt64
				a.Presence.UpdatedAt = a.Pause.PausedAt
				c.ExpectedPresenceEpoch = math.MaxInt64
				c.ExpectedPresenceRevision = a.Presence.Revision
			}, want: arena.ErrPausedPresenceOverflow},
		}
		for _, test := range tests {
			t.Run(test.name, func(t *testing.T) {
				t.Parallel()
				authority, command := pausedPresenceFixture(t, changedAt)
				test.mutate(&authority, &command)
				repository := newPausedPresenceRepositoryFake(authority)
				useCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt})
				if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("rejects a server timestamp before the pause or prior Presence update", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		repository := newPausedPresenceRepositoryFake(authority)
		tooEarly := authority.Pause.PausedAt.Add(-time.Second)
		useCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: tooEarly})
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, arena.ErrInvalidPausedPresence) || changed || repository.writeCount() != 0 {
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
		repository := newPausedPresenceRepositoryFake(authority)
		useCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: between})
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, arena.ErrInvalidPausedPresence) || changed || repository.writeCount() != 0 {
			t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects malformed stored Presence event time", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		leaderRepository := newPausedPresenceRepositoryFake(authority)
		leader := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: changedAt})
		stored, changed, err := leader.Change(t.Context(), command)
		if err != nil || !changed || stored.Authority.Presence.DisconnectedAt == nil {
			t.Fatalf("prepare stored Presence: error = %v, changed = %v", err, changed)
		}
		wrongAt := stored.ChangedAt.Add(-time.Second)
		stored.Authority.Presence.DisconnectedAt = &wrongAt
		repository := newPausedPresenceRepositoryFake(authority)
		repository.storeCommand(*stored)
		useCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt.Add(time.Second)})
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, arena.ErrPausedPresenceCommandReuse) || changed || repository.writeCount() != 0 {
			t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("rejects malformed stored CONNECT event time", func(t *testing.T) {
		t.Parallel()

		authority, disconnect := pausedPresenceFixture(t, changedAt)
		repository := newPausedPresenceRepositoryFake(authority)
		disconnectUseCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt})
		disconnected, changed, err := disconnectUseCase.Change(t.Context(), disconnect)
		if err != nil || !changed {
			t.Fatalf("prepare disconnect: error = %v, changed = %v", err, changed)
		}
		connect := disconnect
		connect.CommandID = uuid.New()
		connect.NextState = arena.PresenceStateConnected
		connect.ExpectedPresenceEpoch = disconnected.Authority.Presence.PresenceEpoch
		connect.ExpectedPresenceRevision = disconnected.Authority.Presence.Revision
		connectedAt := changedAt.Add(time.Second)
		connectUseCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: connectedAt})
		stored, changed, err := connectUseCase.Change(t.Context(), connect)
		if err != nil || !changed || stored.Authority.Presence.State != arena.PresenceStateConnected {
			t.Fatalf("prepare connect: error = %v, changed = %v", err, changed)
		}
		wrongAt := stored.ChangedAt.Add(-time.Second)
		stored.Authority.Presence.ConnectedAt = wrongAt
		replayRepository := newPausedPresenceRepositoryFake(authority)
		replayRepository.storeCommand(*stored)
		replayUseCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, replayRepository, fixedArenaClock{now: connectedAt.Add(time.Second)})
		if _, changed, err := replayUseCase.Change(t.Context(), connect); !errors.Is(err, arena.ErrPausedPresenceCommandReuse) || changed || replayRepository.writeCount() != 0 {
			t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, replayRepository.writeCount())
		}
	})

	t.Run("uses a second command lookup after locking authority", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		leaderRepository := newPausedPresenceRepositoryFake(authority)
		leader := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, leaderRepository, fixedArenaClock{now: changedAt})
		stored, changed, err := leader.Change(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("prepare stored Presence: error = %v, changed = %v", err, changed)
		}

		followerRepository := newPausedPresenceRepositoryFake(authority)
		followerRepository.blockNextLoad()
		follower := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, followerRepository, fixedArenaClock{now: changedAt.Add(time.Second)})
		type result struct {
			record  *arena.PausedPresenceRecord
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
		repository := newPausedPresenceRepositoryFake(authority)
		repository.conflictsRemaining = 2
		transactions := &countingArenaTransactionManager{}
		clock := &sequenceArenaClock{times: []time.Time{changedAt, changedAt.Add(time.Second)}}
		useCase := arena.NewPausedPresenceUseCase(transactions, repository, clock)
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, arena.ErrPausedPresenceConflict) || changed || repository.loadCount != 2 || transactions.count() != 2 || clock.count() != 2 {
			t.Fatalf("Change() error = %v, changed = %v, loads = %d, transactions = %d, clock calls = %d", err, changed, repository.loadCount, transactions.count(), clock.count())
		}
	})

	t.Run("binds the CAS to the exact Presence row owner", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		repository := newPausedPresenceRepositoryFake(authority)
		repository.replacePresenceOnCommit = true
		useCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt})
		if _, changed, err := useCase.Change(t.Context(), command); !errors.Is(err, arena.ErrPausedPresenceConflict) || changed || repository.writeCount() != 0 {
			t.Fatalf("Change() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("retries one CAS conflict, rejects command reuse, and wraps errors", func(t *testing.T) {
		t.Parallel()

		authority, command := pausedPresenceFixture(t, changedAt)
		repository := newPausedPresenceRepositoryFake(authority)
		repository.conflictOnce = true
		useCase := arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt})
		if _, changed, err := useCase.Change(t.Context(), command); err != nil || !changed || repository.loadCount != 2 {
			t.Fatalf("retry error = %v, changed = %v, loads = %d", err, changed, repository.loadCount)
		}
		reused := command
		reused.NextState = arena.PresenceStateConnected
		if _, changed, err := useCase.Change(t.Context(), reused); !errors.Is(err, arena.ErrPausedPresenceCommandReuse) || changed {
			t.Fatalf("reuse error = %v, changed = %v", err, changed)
		}

		cause := errors.New("presence storage unavailable")
		authority, command = pausedPresenceFixture(t, changedAt)
		repository = newPausedPresenceRepositoryFake(authority)
		repository.loadErr = cause
		useCase = arena.NewPausedPresenceUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: changedAt})
		if _, _, err := useCase.Change(t.Context(), command); !errors.Is(err, cause) {
			t.Fatalf("Change() error = %v, want wrapped cause", err)
		}
	})
}

type pausedPresenceRepositoryFake struct {
	mu                      sync.Mutex
	authority               arena.PausedPresenceAuthority
	commands                map[uuid.UUID]arena.PausedPresenceRecord
	conflictOnce            bool
	conflictsRemaining      int
	loadErr                 error
	loadCount               int
	writes                  int
	commitCalls             int
	loadStarted             chan struct{}
	loadRelease             chan struct{}
	loadOnce                sync.Once
	replacePresenceOnCommit bool
}

func newPausedPresenceRepositoryFake(authority arena.PausedPresenceAuthority) *pausedPresenceRepositoryFake {
	return &pausedPresenceRepositoryFake{authority: clonePausedPresenceAuthority(authority), commands: make(map[uuid.UUID]arena.PausedPresenceRecord)}
}

func (f *pausedPresenceRepositoryFake) FindPausedPresenceCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*arena.PausedPresenceRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.commands[commandID]
	if !ok || record.Command.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	clone := clonePausedPresenceRecord(record)
	return &clone, nil
}

func (f *pausedPresenceRepositoryFake) LoadPausedPresenceAuthority(_ context.Context, _ arena.PauseGraphScope, _ uuid.UUID) (arena.PausedPresenceAuthority, error) {
	f.mu.Lock()
	f.loadCount++
	if f.loadErr != nil {
		f.mu.Unlock()
		return arena.PausedPresenceAuthority{}, f.loadErr
	}
	started, release := f.loadStarted, f.loadRelease
	f.mu.Unlock()
	if started != nil {
		f.loadOnce.Do(func() { close(started) })
		<-release
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return clonePausedPresenceAuthority(f.authority), nil
}

func (f *pausedPresenceRepositoryFake) CommitPausedPresence(_ context.Context, expected arena.PausedPresenceExpectation, record arena.PausedPresenceRecord) (*arena.PausedPresenceRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if existing, ok := f.commands[record.Command.CommandID]; ok {
		clone := clonePausedPresenceRecord(existing)
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
	current := f.authority
	if f.replacePresenceOnCommit {
		current.Presence.ID = uuid.New()
		f.authority = current
	}
	presenceRevision := arena.PausePresenceRevision{
		ID: current.Presence.ID, TournamentID: current.Presence.TournamentID, RosterID: current.Presence.RosterID,
		SeriesID: current.Presence.SeriesID, ParticipantID: current.Presence.ParticipantID,
		PresenceEpoch: current.Presence.PresenceEpoch, Revision: current.Presence.Revision,
	}
	if expected.PauseID != current.Pause.PauseID || expected.GraphRevision != current.Pause.Graph.Revision ||
		expected.PauseRevision != current.Pause.Revision || expected.Presence != presenceRevision || expected.Authority != current.Pause.Scope.Authority {
		return nil, false, domain.ErrConflict
	}
	f.writes++
	f.authority = clonePausedPresenceAuthority(record.Authority)
	f.commands[record.Command.CommandID] = clonePausedPresenceRecord(record)
	clone := clonePausedPresenceRecord(record)
	return &clone, true, nil
}

func (f *pausedPresenceRepositoryFake) blockNextLoad() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.loadStarted = make(chan struct{})
	f.loadRelease = make(chan struct{})
}

func (f *pausedPresenceRepositoryFake) storeCommand(record arena.PausedPresenceRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands[record.Command.CommandID] = clonePausedPresenceRecord(record)
}

func (f *pausedPresenceRepositoryFake) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func pausedPresenceFixture(t *testing.T, now time.Time) (arena.PausedPresenceAuthority, arena.PausedPresenceCommand) {
	t.Helper()
	authority, pauseCommand := normalPauseFixture(now.Add(-time.Minute))
	repository := newNormalPauseRepositoryFake(authority)
	useCase := arena.NewNormalPauseGraphUseCase(directArenaTransactionManager{}, repository, fixedArenaClock{now: now.Add(-time.Minute)})
	pause, changed, err := useCase.Enter(t.Context(), pauseCommand)
	if err != nil || !changed {
		t.Fatalf("enter fixture pause: error = %v, changed = %v", err, changed)
	}
	presence := pause.Graph.Presence[0]
	authorityRecord := arena.PausedPresenceAuthority{
		Pause: *pause, Presence: presence,
		Reconnect:              append([]arena.PauseReconnectInterval(nil), pause.Graph.Reconnect...),
		Counters:               append([]arena.PauseReconnectCounter(nil), pause.Graph.Counters...),
		FrozenDeadlines:        append([]arena.PauseFrozenDeadline(nil), pause.Graph.FrozenDeadlines...),
		TerminalActionRevision: 8,
	}
	command := arena.PausedPresenceCommand{
		Scope: pause.Scope, PauseID: pause.PauseID, CommandID: uuid.New(), ParticipantID: presence.ParticipantID,
		ExpectedGraphRevision: pause.Graph.Revision, ExpectedPauseRevision: pause.Revision,
		ExpectedPresenceEpoch: presence.PresenceEpoch, ExpectedPresenceRevision: presence.Revision,
		NextState: arena.PresenceStateDisconnected,
	}
	return authorityRecord, command
}

func clonePausedPresenceAuthority(value arena.PausedPresenceAuthority) arena.PausedPresenceAuthority {
	value.Pause = cloneNormalPauseRecord(value.Pause)
	value.Presence = clonePausePresence(value.Presence)
	value.Reconnect = append([]arena.PauseReconnectInterval(nil), value.Reconnect...)
	for index := range value.Reconnect {
		if value.Reconnect[index].ClosedAt != nil {
			closedAt := *value.Reconnect[index].ClosedAt
			value.Reconnect[index].ClosedAt = &closedAt
		}
	}
	value.Counters = append([]arena.PauseReconnectCounter(nil), value.Counters...)
	value.FrozenDeadlines = append([]arena.PauseFrozenDeadline(nil), value.FrozenDeadlines...)
	for index := range value.FrozenDeadlines {
		if value.FrozenDeadlines[index].ResumedAt != nil {
			resumedAt := *value.FrozenDeadlines[index].ResumedAt
			value.FrozenDeadlines[index].ResumedAt = &resumedAt
		}
		if value.FrozenDeadlines[index].ResumedDeadline != nil {
			deadline := *value.FrozenDeadlines[index].ResumedDeadline
			value.FrozenDeadlines[index].ResumedDeadline = &deadline
		}
	}
	return value
}

func clonePausedPresenceRecord(value arena.PausedPresenceRecord) arena.PausedPresenceRecord {
	value.Authority = clonePausedPresenceAuthority(value.Authority)
	return value
}

func clonePausePresence(value arena.PausePresence) arena.PausePresence {
	if value.DisconnectedAt != nil {
		at := *value.DisconnectedAt
		value.DisconnectedAt = &at
	}
	return value
}

func pausedPresenceImmutableEqual(first, second arena.PausedPresenceAuthority) bool {
	return reflect.DeepEqual(first.Pause, second.Pause) && reflect.DeepEqual(first.Reconnect, second.Reconnect) &&
		reflect.DeepEqual(first.Counters, second.Counters) && reflect.DeepEqual(first.FrozenDeadlines, second.FrozenDeadlines) &&
		first.TerminalActionRevision == second.TerminalActionRevision
}

var _ arena.PausedPresenceRepository = (*pausedPresenceRepositoryFake)(nil)
