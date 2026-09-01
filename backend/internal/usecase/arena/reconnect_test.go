package arena_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestReconnectSuccess(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 1, 12, 0, 0, 0, time.UTC)
	authority := task045Authority(now, true, false)
	interval := authority.Reconnect[0]
	command := arena.ReconnectCommand{Scope: authority.Scope, CommandID: task045ID(30),
		ParticipantID: authority.Series.FirstParticipantID, IntervalID: interval.ID,
		Settlement: task045SettlementIDs(31)}
	repository := newTask045RepositoryFake(authority)
	useCase := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now})

	reconnected, changed, err := useCase.Reconnect(t.Context(), command)
	if err != nil || !changed {
		t.Fatalf("Reconnect() error = %v, changed = %v, want success", err, changed)
	}
	if reconnected.ExpectedAuthorityRevision != authority.Revision || reconnected.Authority.Revision != authority.Revision+1 {
		t.Fatalf("authority revisions = (%d, %d), want (%d, %d)", reconnected.ExpectedAuthorityRevision, reconnected.Authority.Revision, authority.Revision, authority.Revision+1)
	}
	gotPresence := task045Presence(t, reconnected.Authority, command.ParticipantID)
	if gotPresence.State != arena.PresenceStateConnected || gotPresence.PresenceEpoch != 3 || gotPresence.Revision != 3 ||
		gotPresence.DisconnectedAt != nil || !gotPresence.ConnectedAt.Equal(now) || !gotPresence.UpdatedAt.Equal(now) {
		t.Fatalf("reconnected Presence = %+v", gotPresence)
	}
	gotInterval := task045Interval(t, reconnected.Authority, interval.ID)
	if gotInterval.State != arena.ReconnectStateReconnected || gotInterval.Revision != interval.Revision+1 ||
		gotInterval.ClosedAt == nil || !gotInterval.ClosedAt.Equal(now) || !gotInterval.UpdatedAt.Equal(now) {
		t.Fatalf("closed reconnect interval = %+v", gotInterval)
	}
	if reconnected.Authority.Game.State != domain.ArenaGameStateActive || reconnected.Authority.GameRevision != authority.GameRevision+1 {
		t.Fatalf("Game = %+v, revision = %d", reconnected.Authority.Game, reconnected.Authority.GameRevision)
	}
	if reconnected.Authority.SeriesRevision != authority.SeriesRevision+1 ||
		reconnected.Authority.Series.Slots[0].Attempts[0] != reconnected.Authority.Game {
		t.Fatalf("Series did not mirror resumed Game: Series revision %d, Game %+v, slot %+v", reconnected.Authority.SeriesRevision, reconnected.Authority.Game, reconnected.Authority.Series.Slots[0].Attempts[0])
	}
	wantDeadline := now.Add(authority.GameClock.Remaining)
	clock := reconnected.Authority.GameClock
	if clock.ResumedAt == nil || !clock.ResumedAt.Equal(now) || clock.ResumedDeadline == nil ||
		!clock.ResumedDeadline.Equal(wantDeadline) || clock.Revision != authority.GameClock.Revision+1 {
		t.Fatalf("resumed Game clock = %+v, want deadline %v", clock, wantDeadline)
	}
	if got := task045Counter(t, reconnected.Authority, command.ParticipantID); got != authority.Counters[0] {
		t.Fatalf("counter changed on reconnect: got %+v want %+v", got, authority.Counters[0])
	}
	if got := task045Presence(t, reconnected.Authority, authority.Series.SecondParticipantID); !task045PresenceEqual(got, authority.Presence[1]) {
		t.Fatalf("opponent Presence changed: got %+v want %+v", got, authority.Presence[1])
	}
	if reconnected.GameResultRevision != nil || reconnected.ScoreRevision != nil || reconnected.Evidence != nil {
		t.Fatal("timely reconnect wrote terminal evidence")
	}

	writes := repository.writeCount()
	replayed, changed, err := useCase.Reconnect(t.Context(), command)
	if err != nil || changed || repository.writeCount() != writes {
		t.Fatalf("Reconnect(replay) error = %v, changed = %v, writes = %d, want no write", err, changed, repository.writeCount())
	}
	replayed.Authority.Presence[0].State = arena.PresenceStateDisconnected
	replayed.Authority.Reconnect[0].State = arena.ReconnectStateExpired
	stored := repository.snapshot()
	if task045Presence(t, stored, command.ParticipantID).State != arena.PresenceStateConnected || task045Interval(t, stored, interval.ID).State != arena.ReconnectStateReconnected {
		t.Fatal("mutating returned receipt changed repository authority")
	}

	reused := command
	reused.ParticipantID = authority.Series.SecondParticipantID
	_, changed, err = useCase.Reconnect(t.Context(), reused)
	if !errors.Is(err, arena.ErrReconnectCommandReuse) || changed || repository.writeCount() != writes {
		t.Fatalf("Reconnect(command reuse) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
	}

	t.Run("deadline_is_exclusive", func(t *testing.T) {
		boundary := task045Authority(now, true, false)
		boundaryNow := boundary.Reconnect[0].Deadline
		repo := newTask045RepositoryFake(boundary)
		uc := arena.NewReconnectUseCase(repo, fixedArenaClock{now: boundaryNow})
		_, changed, err := uc.Reconnect(t.Context(), arena.ReconnectCommand{Scope: boundary.Scope, CommandID: task045ID(38),
			ParticipantID: boundary.Series.FirstParticipantID, IntervalID: boundary.Reconnect[0].ID, Settlement: task045SettlementIDs(39)})
		if !errors.Is(err, arena.ErrReconnectDeadline) || changed || repo.writeCount() != 0 {
			t.Fatalf("Reconnect(at deadline) error = %v, changed = %v, writes = %d", err, changed, repo.writeCount())
		}
	})

	t.Run("both_open_resume_only_after_second_reconnect", func(t *testing.T) {
		both := task045Authority(now, true, true)
		repository := newTask045RepositoryFake(both)
		first, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now}).Reconnect(t.Context(), arena.ReconnectCommand{
			Scope: both.Scope, CommandID: task045ID(320), ParticipantID: both.Series.FirstParticipantID,
			IntervalID: both.Reconnect[0].ID, Settlement: task045SettlementIDs(321),
		})
		if err != nil || !changed || first.Authority.Game.State != domain.ArenaGameStatePaused ||
			first.Authority.GameClock.ResumedDeadline != nil || first.Authority.GameRevision != both.GameRevision ||
			first.Authority.SeriesRevision != both.SeriesRevision {
			t.Fatalf("first of both reconnects resumed early: error = %v, record = %+v", err, first)
		}
		secondAt := now.Add(time.Second)
		second, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: secondAt}).Reconnect(t.Context(), arena.ReconnectCommand{
			Scope: both.Scope, CommandID: task045ID(330), ParticipantID: both.Series.SecondParticipantID,
			IntervalID: both.Reconnect[1].ID, Settlement: task045SettlementIDs(331),
		})
		wantResumedDeadline := secondAt.Add(both.GameClock.Remaining)
		if err != nil || !changed || second.Authority.Game.State != domain.ArenaGameStateActive ||
			second.Authority.GameClock.ResumedDeadline == nil || !second.Authority.GameClock.ResumedDeadline.Equal(wantResumedDeadline) ||
			second.Authority.GameClock.ResumedDeadline.Equal(both.GameClock.OriginalDeadline) ||
			second.Authority.GameClock.Revision != both.GameClock.Revision+1 {
			t.Fatalf("second reconnect did not resume exact Remaining: error = %v, clock = %+v", err, second.Authority.GameClock)
		}
	})

	t.Run("repository_owned_memory_is_not_returned", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		command := arena.ReconnectCommand{Scope: authority.Scope, CommandID: task045ID(340),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(341)}
		repository := newTask045RepositoryFake(authority)
		record, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now}).Reconnect(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Reconnect(aliasing fake) error = %v, changed = %v", err, changed)
		}
		repository.mutateReturned()
		if task045Presence(t, record.Authority, command.ParticipantID).State != arena.PresenceStateConnected ||
			task045Interval(t, record.Authority, command.IntervalID).State != arena.ReconnectStateReconnected {
			t.Fatal("use case returned repository-owned nested memory")
		}
	})

	t.Run("bad_commit_and_bad_receipt_are_internal_errors", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		command := arena.ReconnectCommand{Scope: authority.Scope, CommandID: task045ID(350),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(351)}
		t.Run("bad_commit", func(t *testing.T) {
			repository := newTask045RepositoryFake(authority)
			repository.mutateCommitted = func(record *arena.ReconnectRecord) { record.RecordedAt = record.RecordedAt.Add(time.Nanosecond) }
			_, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now}).Reconnect(t.Context(), command)
			if !errors.Is(err, domain.ErrInternal) || changed {
				t.Fatalf("Reconnect(bad commit) error = %v, changed = %v", err, changed)
			}
		})
		t.Run("bad_receipt", func(t *testing.T) {
			repository := newTask045RepositoryFake(authority)
			useCase := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now})
			if _, changed, err := useCase.Reconnect(t.Context(), command); err != nil || !changed {
				t.Fatalf("Reconnect(seed receipt) error = %v, changed = %v", err, changed)
			}
			repository.corruptReceipt(command.CommandID)
			_, changed, err := useCase.Reconnect(t.Context(), command)
			if !errors.Is(err, domain.ErrInternal) || changed {
				t.Fatalf("Reconnect(bad receipt) error = %v, changed = %v", err, changed)
			}
		})
	})

	t.Run("mutation_timestamps_must_advance", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		at := authority.Reconnect[0].UpdatedAt
		repository := newTask045RepositoryFake(authority)
		_, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: at}).Reconnect(t.Context(), arena.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(380), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(381),
		})
		if !errors.Is(err, arena.ErrInvalidReconnectMutation) || changed || repository.writeCount() != 0 {
			t.Fatalf("Reconnect(same timestamp) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("durable_history_invariants_reject_without_write", func(t *testing.T) {
		for _, test := range []struct {
			name string
			make func() arena.ReconnectAuthority
		}{
			{name: "counter_without_root", make: func() arena.ReconnectAuthority {
				authority := task045Authority(now, false, false)
				authority.Counters[0].Used = 1
				return authority
			}},
			{name: "duplicate_cycle_segment", make: func() arena.ReconnectAuthority {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, 1, now)
				duplicate := authority.Reconnect[0]
				duplicate.ID = task045ID(390)
				authority.Reconnect = append(authority.Reconnect, duplicate)
				return authority
			}},
			{name: "multiple_open_intervals", make: func() arena.ReconnectAuthority {
				authority := task045Authority(now, true, false)
				second := authority.Reconnect[0]
				second.ID = task045ID(391)
				second.Number = 2
				authority.Reconnect = append(authority.Reconnect, second)
				authority.Counters[0].Used = 2
				authority.Counters[0].Revision = 3
				return authority
			}},
			{name: "open_epoch_does_not_match_presence", make: func() arena.ReconnectAuthority {
				authority := task045Authority(now, true, false)
				authority.Reconnect[0].PresenceEpoch--
				return authority
			}},
			{name: "dangling_continuation", make: func() arena.ReconnectAuthority {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, 1, now)
				dangling := task045ID(392)
				openedAt, closedAt := now.Add(-40*time.Second), now.Add(-30*time.Second)
				authority.Reconnect = append(authority.Reconnect, arena.PauseReconnectInterval{
					ID: task045ID(393), PauseID: authority.PauseID, RosterID: authority.Scope.RosterID, SeriesID: authority.Series.ID,
					GameID: authority.Game.ID, ParticipantID: authority.Series.FirstParticipantID, PresenceEpoch: 2, Number: 1,
					ContinuationNumber: 1, ContinuedFromID: &dangling, State: arena.ReconnectStateReconnected,
					OpenedAt: openedAt, Deadline: now.Add(time.Minute), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt,
				})
				return authority
			}},
			{name: "cyclic_continuation", make: func() arena.ReconnectAuthority {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, 1, now)
				firstID, secondID := task045ID(394), task045ID(395)
				openedAt, closedAt := now.Add(-40*time.Second), now.Add(-30*time.Second)
				suspended := task045ID(396)
				authority.Reconnect = append(authority.Reconnect,
					arena.PauseReconnectInterval{ID: firstID, PauseID: authority.PauseID, RosterID: authority.Scope.RosterID, SeriesID: authority.Series.ID,
						GameID: authority.Game.ID, ParticipantID: authority.Series.FirstParticipantID, PresenceEpoch: 2, Number: 1,
						ContinuationNumber: 1, ContinuedFromID: &secondID, SuspendedByPauseID: &suspended,
						State: arena.ReconnectStateCancelled, OpenedAt: openedAt, Deadline: now.Add(time.Minute), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt},
					arena.PauseReconnectInterval{ID: secondID, PauseID: authority.PauseID, RosterID: authority.Scope.RosterID, SeriesID: authority.Series.ID,
						GameID: authority.Game.ID, ParticipantID: authority.Series.FirstParticipantID, PresenceEpoch: 2, Number: 1,
						ContinuationNumber: 2, ContinuedFromID: &firstID, SuspendedByPauseID: &suspended,
						State: arena.ReconnectStateCancelled, OpenedAt: openedAt, Deadline: now.Add(time.Minute), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt})
				return authority
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := test.make()
				intervalID := task045ID(396)
				if len(authority.Reconnect) > 0 {
					intervalID = authority.Reconnect[0].ID
				}
				repository := newTask045RepositoryFake(authority)
				_, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now}).Reconnect(t.Context(), arena.ReconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(397), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: intervalID, Settlement: task045SettlementIDs(398),
				})
				if !errors.Is(err, arena.ErrInvalidReconnectMutation) || changed || repository.writeCount() != 0 || repository.commitCount() != 0 {
					t.Fatalf("Reconnect(%s) error = %v, changed = %v, writes = %d, commits = %d", test.name, err, changed, repository.writeCount(), repository.commitCount())
				}
			})
		}
	})

	t.Run("persistent_conflict_is_bounded_to_two_attempts", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		repository := newTask045RepositoryFake(authority)
		repository.alwaysConflict = true
		command := arena.ReconnectCommand{Scope: authority.Scope, CommandID: task045ID(360),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(361)}
		_, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now}).Reconnect(t.Context(), command)
		if !errors.Is(err, arena.ErrReconnectConflict) || changed || repository.commitCount() != 2 || repository.writeCount() != 0 {
			t.Fatalf("Reconnect(persistent conflict) error = %v, changed = %v, commits = %d, writes = %d", err, changed, repository.commitCount(), repository.writeCount())
		}
	})

	t.Run("expired_current_continuation_is_not_hidden_by_cancelled_predecessor", func(t *testing.T) {
		authority := task045Authority(now, true, true)
		root := &authority.Reconnect[0]
		closedAt := now
		root.State = arena.ReconnectStateCancelled
		root.ClosedAt = &closedAt
		root.UpdatedAt = closedAt
		root.Revision++
		suspendedBy := task045ID(370)
		root.SuspendedByPauseID = &suspendedBy
		rootID := root.ID
		continuationOpened := now.Add(time.Second)
		continuationDeadline := now.Add(21 * time.Second)
		authority.Reconnect[1].Deadline = now.Add(40 * time.Second)
		authority.Reconnect = append(authority.Reconnect, arena.PauseReconnectInterval{
			ID: task045ID(371), PauseID: authority.PauseID, RosterID: authority.Scope.RosterID,
			SeriesID: authority.Series.ID, GameID: authority.Game.ID, ParticipantID: authority.Series.FirstParticipantID,
			PresenceEpoch: authority.Presence[0].PresenceEpoch, Number: 1, ContinuationNumber: 1, ContinuedFromID: &rootID,
			State: arena.ReconnectStateExpired, OpenedAt: continuationOpened, Deadline: continuationDeadline,
			ClosedAt: &continuationDeadline, Revision: 2, UpdatedAt: continuationDeadline,
		})
		reconnectAt := now.Add(25 * time.Second)
		repository := newTask045RepositoryFake(authority)
		terminal, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: reconnectAt}).Reconnect(t.Context(), arena.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(372), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(373),
		})
		if err != nil || !changed || terminal.GameResultRevision == nil ||
			terminal.GameResultRevision.WinnerID != authority.Series.SecondParticipantID {
			t.Fatalf("Reconnect(current continuation expired) error = %v, changed = %v, record = %+v", err, changed, terminal)
		}
	})

	t.Run("current_epoch_expiry_cannot_cross_replacement_game", func(t *testing.T) {
		authority := task045Authority(now, true, true)
		prior := task045ReplaceCurrentGame(&authority, task045ID(960), now)
		authority.Game.State = domain.ArenaGameStatePaused
		authority.Series.Slots[0].Attempts[1] = authority.Game
		authority.GameClock.FrozenAt = now.Add(-5 * time.Second)
		authority.GameClock.Remaining = 40 * time.Second
		authority.GameClock.OriginalDeadline = authority.GameClock.FrozenAt.Add(authority.GameClock.Remaining)
		authority.GameClock.Revision = 2
		first := &authority.Reconnect[0]
		second := &authority.Reconnect[1]
		first.GameID = authority.Game.ID
		second.GameID = prior.ID
		second.State = arena.ReconnectStateExpired
		second.Deadline = now.Add(-time.Second)
		second.ClosedAt = &second.Deadline
		second.UpdatedAt = second.Deadline
		second.Revision = 2
		repository := newTask045RepositoryFake(authority)
		_, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now}).Reconnect(t.Context(), arena.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(961), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: first.ID, Settlement: task045SettlementIDs(962),
		})
		if !errors.Is(err, arena.ErrInvalidReconnectMutation) || !strings.Contains(err.Error(), "current reconnect interval belongs to predecessor Game") ||
			changed || repository.writeCount() != 0 || repository.commitCount() != 0 {
			t.Fatalf("Reconnect(cross-Game current expiry) error = %v, changed = %v, writes = %d, commits = %d", err, changed, repository.writeCount(), repository.commitCount())
		}
	})
}

type task045RepositoryFake struct {
	mu              sync.Mutex
	authority       arena.ReconnectAuthority
	records         map[uuid.UUID]*arena.ReconnectRecord
	writes          int
	commitCalls     int
	conflicts       int
	loads           int
	barrier         *task045Barrier
	alwaysConflict  bool
	mutateCommitted func(*arena.ReconnectRecord)
	returned        *arena.ReconnectRecord
	shareOwned      bool
}

func newTask045RepositoryFake(authority arena.ReconnectAuthority) *task045RepositoryFake {
	return &task045RepositoryFake{authority: cloneTask045Authority(authority), records: make(map[uuid.UUID]*arena.ReconnectRecord)}
}

func (f *task045RepositoryFake) FindReconnectCommand(_ context.Context, tournamentID, commandID uuid.UUID) (*arena.ReconnectRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record, ok := f.records[commandID]
	if !ok || record.Authority.Scope.TournamentID != tournamentID {
		return nil, nil
	}
	if f.shareOwned {
		return record, nil
	}
	clone := cloneTask045Record(*record)
	return &clone, nil
}

func (f *task045RepositoryFake) LoadReconnectAuthority(ctx context.Context, _ arena.PauseGraphScope) (arena.ReconnectAuthority, error) {
	f.mu.Lock()
	f.loads++
	load := f.loads
	barrier := f.barrier
	clone := cloneTask045Authority(f.authority)
	f.mu.Unlock()
	if barrier != nil && load <= barrier.parties {
		if err := barrier.wait(ctx); err != nil {
			return arena.ReconnectAuthority{}, err
		}
	}
	return clone, nil
}

func (f *task045RepositoryFake) CommitReconnectMutation(_ context.Context, expectedRevision int64, record arena.ReconnectRecord) (*arena.ReconnectRecord, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commitCalls++
	if f.alwaysConflict {
		f.conflicts++
		return nil, false, domain.ErrConflict
	}
	if f.authority.Revision != expectedRevision {
		f.conflicts++
		return nil, false, domain.ErrConflict
	}
	if record.ExpectedAuthorityRevision != expectedRevision || record.Authority.Revision != expectedRevision+1 {
		return nil, false, domain.ErrValidation
	}
	commandID := task045RecordCommandID(record)
	if commandID == uuid.Nil {
		return nil, false, domain.ErrValidation
	}
	stored := cloneTask045Record(record)
	f.authority = cloneTask045Authority(record.Authority)
	f.records[commandID] = &stored
	f.writes++
	if f.shareOwned {
		f.returned = f.records[commandID]
		return f.returned, true, nil
	}
	clone := cloneTask045Record(stored)
	if f.mutateCommitted != nil {
		f.mutateCommitted(&clone)
	}
	f.returned = &clone
	return f.returned, true, nil
}

func (f *task045RepositoryFake) snapshot() arena.ReconnectAuthority {
	f.mu.Lock()
	defer f.mu.Unlock()
	return cloneTask045Authority(f.authority)
}

func (f *task045RepositoryFake) writeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.writes
}

func (f *task045RepositoryFake) conflictCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conflicts
}

func (f *task045RepositoryFake) commitCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.commitCalls
}

func (f *task045RepositoryFake) mutateReturned() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.returned != nil && len(f.returned.Authority.Presence) > 0 {
		f.returned.Authority.Presence[0].State = arena.PresenceStateDisconnected
		if f.returned.ScoreRevision != nil && len(f.returned.ScoreRevision.GameResultRevisionIDs) > 0 {
			f.returned.ScoreRevision.GameResultRevisionIDs[0] = domain.ArenaOfficialResultRevisionID(task045ID(998))
		}
	}
}

func (f *task045RepositoryFake) corruptReceipt(commandID uuid.UUID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := cloneTask045Record(*f.records[commandID])
	record.Authority.Game.State = domain.ArenaGameStateVoid
	f.records[commandID] = &record
}

func (f *task045RepositoryFake) mutateReceipt(commandID uuid.UUID, mutate func(*arena.ReconnectRecord)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	record := cloneTask045Record(*f.records[commandID])
	mutate(&record)
	f.records[commandID] = &record
}

type task045Barrier struct {
	parties int
	arrived chan struct{}
	release chan struct{}
}

func newTask045Barrier() *task045Barrier {
	const parties = 2
	return &task045Barrier{parties: parties, arrived: make(chan struct{}, parties), release: make(chan struct{})}
}

func (b *task045Barrier) wait(ctx context.Context) error {
	select {
	case b.arrived <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-b.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (b *task045Barrier) await(ctx context.Context) error {
	for range b.parties {
		select {
		case <-b.arrived:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	close(b.release)
	return nil
}

func task045Authority(now time.Time, firstDisconnected, secondDisconnected bool) arena.ReconnectAuthority {
	tournamentID, rosterID, waveID := task045ID(1), task045ID(2), task045ID(3)
	seriesID, slotID, gameID, pauseID := task045ID(4), task045ID(5), task045ID(6), task045ID(7)
	firstID, secondID := task045ID(8), task045ID(9)
	game := domain.ArenaGame{ID: gameID, SlotID: slotID, AttemptNo: 1, State: domain.ArenaGameStateActive}
	series := domain.ArenaSeries{ID: seriesID, TournamentID: tournamentID, FirstParticipantID: firstID, SecondParticipantID: secondID,
		Format: domain.ArenaSeriesFormatBO1, State: domain.ArenaSeriesStateActive,
		Slots: []domain.ArenaGameSlot{{ID: slotID, SeriesID: seriesID, Position: 1, Category: domain.CategoryWeb,
			ScoreBefore: domain.ArenaSeriesScore{}, Attempts: []domain.ArenaGame{game}}}}
	authority := arena.ReconnectAuthority{
		Scope: arena.PauseGraphScope{TournamentID: tournamentID, RosterID: rosterID, WaveID: waveID,
			Authority: arena.ExecutionAuthorityIdentity{TournamentID: tournamentID, HolderID: task045ID(20), LeaseID: task045ID(21), Epoch: 1, ProcessKind: arena.ExecutionProcessKindAuthority}},
		Revision: 10, PauseID: pauseID, GameRevision: 4, SeriesRevision: 3,
		CurrentOrdinal: 7, CurrentProjectionRevision: 11,
		CurrentGameResultRevisionIDs: []domain.ArenaOfficialResultRevisionID{domain.ArenaOfficialResultRevisionID(task045ID(22))},
		Game:                         game, Series: series,
		GameClock: arena.PauseResumeGameClock{PauseID: pauseID, GameID: gameID, OriginalDeadline: now.Add(90 * time.Second), Revision: 1},
		Presence: []arena.PausePresence{
			{ID: task045ID(10), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: firstID, State: arena.PresenceStateConnected, PresenceEpoch: 1, Revision: 1, ConnectedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)},
			{ID: task045ID(11), TournamentID: tournamentID, RosterID: rosterID, SeriesID: seriesID, ParticipantID: secondID, State: arena.PresenceStateConnected, PresenceEpoch: 1, Revision: 1, ConnectedAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Minute)}},
		Counters: []arena.PauseReconnectCounter{
			{PauseID: pauseID, RosterID: rosterID, ParticipantID: firstID, Limit: arena.ReconnectCycleLimit, Revision: 1},
			{PauseID: pauseID, RosterID: rosterID, ParticipantID: secondID, Limit: arena.ReconnectCycleLimit, Revision: 1}}}
	if firstDisconnected {
		task045SetDisconnected(&authority, 0, task045ID(12), now)
	}
	if secondDisconnected {
		task045SetDisconnected(&authority, 1, task045ID(13), now)
	}
	if firstDisconnected || secondDisconnected {
		authority.Game.State = domain.ArenaGameStatePaused
		authority.Series.Slots[0].Attempts[0] = authority.Game
		authority.GameClock.FrozenAt = now.Add(-5 * time.Second)
		authority.GameClock.Remaining = 40 * time.Second
		authority.GameClock.OriginalDeadline = authority.GameClock.FrozenAt.Add(authority.GameClock.Remaining)
		authority.GameClock.Revision = 2
		authority.GameRevision++
		authority.SeriesRevision++
	}
	return authority
}

func task045SetDisconnected(authority *arena.ReconnectAuthority, index int, intervalID uuid.UUID, now time.Time) {
	presence := &authority.Presence[index]
	disconnectedAt := now.Add(-5 * time.Second)
	presence.State = arena.PresenceStateDisconnected
	presence.PresenceEpoch = 2
	presence.Revision = 2
	presence.DisconnectedAt = &disconnectedAt
	presence.UpdatedAt = disconnectedAt
	authority.Counters[index].Used = 1
	authority.Counters[index].Revision = 2
	authority.Reconnect = append(authority.Reconnect, arena.PauseReconnectInterval{ID: intervalID, PauseID: authority.PauseID,
		RosterID: authority.Scope.RosterID, SeriesID: authority.Series.ID, GameID: authority.Game.ID,
		ParticipantID: presence.ParticipantID, PresenceEpoch: presence.PresenceEpoch, Number: 1,
		State: arena.ReconnectStateOpen, OpenedAt: disconnectedAt, Deadline: now.Add(20 * time.Second), Revision: 1, UpdatedAt: disconnectedAt})
}

func task045AddCompletedRoots(authority *arena.ReconnectAuthority, index, count int, now time.Time) {
	presence := &authority.Presence[index]
	kept := authority.Reconnect[:0]
	for _, interval := range authority.Reconnect {
		if interval.ParticipantID != presence.ParticipantID {
			kept = append(kept, interval)
		}
	}
	authority.Reconnect = kept
	for cycle := 1; cycle <= count; cycle++ {
		openedAt := now.Add(time.Duration(-120+cycle*20+index*5) * time.Second)
		closedAt := openedAt.Add(5 * time.Second)
		authority.Reconnect = append(authority.Reconnect, arena.PauseReconnectInterval{
			ID: task045ID(700 + index*20 + cycle), PauseID: authority.PauseID, RosterID: authority.Scope.RosterID,
			SeriesID: authority.Series.ID, GameID: authority.Game.ID, ParticipantID: presence.ParticipantID,
			PresenceEpoch: int64(cycle * 2), Number: cycle, State: arena.ReconnectStateReconnected,
			OpenedAt: openedAt, Deadline: openedAt.Add(30 * time.Second), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt,
		})
		presence.ConnectedAt = closedAt
		presence.UpdatedAt = closedAt
	}
	presence.State = arena.PresenceStateConnected
	presence.PresenceEpoch = int64(count*2 + 1)
	presence.Revision = int64(count*2 + 1)
	presence.DisconnectedAt = nil
	authority.Counters[index].Used = count
	authority.Counters[index].Revision = int64(count + 1)
	task045RecalculateLifecycleRevisions(authority, now)
}

func task045AddPriorRootForCurrentCycle(authority *arena.ReconnectAuthority, index int, now time.Time) {
	presence := &authority.Presence[index]
	for intervalIndex := range authority.Reconnect {
		interval := &authority.Reconnect[intervalIndex]
		if interval.ParticipantID == presence.ParticipantID {
			interval.Number = 2
			interval.PresenceEpoch = 4
		}
	}
	openedAt := now.Add(-60 * time.Second)
	closedAt := now.Add(-50 * time.Second)
	authority.Reconnect = append(authority.Reconnect, arena.PauseReconnectInterval{
		ID: task045ID(740 + index), PauseID: authority.PauseID, RosterID: authority.Scope.RosterID,
		SeriesID: authority.Series.ID, GameID: authority.Game.ID, ParticipantID: presence.ParticipantID,
		PresenceEpoch: 2, Number: 1, State: arena.ReconnectStateReconnected, OpenedAt: openedAt,
		Deadline: now.Add(-40 * time.Second), ClosedAt: &closedAt, Revision: 2, UpdatedAt: closedAt,
	})
	presence.PresenceEpoch = 4
	presence.Revision = 4
	authority.Counters[index].Used = 2
	authority.Counters[index].Revision = 3
	task045RecalculateLifecycleRevisions(authority, now)
}

func task045SetResumedClock(authority *arena.ReconnectAuthority, now time.Time) {
	task045RecalculateLifecycleRevisions(authority, now)
}

func task045RecalculateLifecycleRevisions(authority *arena.ReconnectAuthority, now time.Time) {
	completed := 0
	for _, interval := range authority.Reconnect {
		if interval.ContinuationNumber == 0 && interval.State == arena.ReconnectStateReconnected {
			completed++
		}
	}
	disconnected := false
	for _, presence := range authority.Presence {
		if presence.State == arena.PresenceStateDisconnected {
			disconnected = true
			break
		}
	}
	authority.GameRevision = 4 + int64(completed*2)
	authority.SeriesRevision = 3 + int64(completed*2)
	authority.GameClock.Revision = 1 + int64(completed*2)
	if disconnected {
		authority.GameRevision++
		authority.SeriesRevision++
		authority.GameClock.FrozenAt = now.Add(-5 * time.Second)
		authority.GameClock.Remaining = 40 * time.Second
		authority.GameClock.OriginalDeadline = authority.GameClock.FrozenAt.Add(authority.GameClock.Remaining)
		authority.GameClock.ResumedAt = nil
		authority.GameClock.ResumedDeadline = nil
		authority.GameClock.Revision++
		return
	}
	if completed == 0 {
		authority.GameClock.OriginalDeadline = now.Add(90 * time.Second)
		authority.GameClock.FrozenAt = time.Time{}
		authority.GameClock.Remaining = 0
		authority.GameClock.ResumedAt = nil
		authority.GameClock.ResumedDeadline = nil
		return
	}
	resumedAt := now.Add(-10 * time.Second)
	resumedDeadline := now.Add(90 * time.Second)
	authority.GameClock.OriginalDeadline = now
	authority.GameClock.FrozenAt = now.Add(-100 * time.Second)
	authority.GameClock.Remaining = 100 * time.Second
	authority.GameClock.ResumedAt = &resumedAt
	authority.GameClock.ResumedDeadline = &resumedDeadline
}

func task045ReplaceCurrentGame(authority *arena.ReconnectAuthority, replacementID uuid.UUID, now time.Time) domain.ArenaGame {
	prior := authority.Game
	resultID := authority.CurrentGameResultRevisionIDs[0]
	prior.State = domain.ArenaGameStateVoid
	prior.ResultReason = domain.ArenaGameResultReasonDisconnect
	prior.ResultRevisionID = &resultID
	replacement := domain.ArenaGame{ID: replacementID, SlotID: prior.SlotID, AttemptNo: prior.AttemptNo + 1, State: domain.ArenaGameStateActive}
	for slotIndex := range authority.Series.Slots {
		if authority.Series.Slots[slotIndex].ID == prior.SlotID {
			authority.Series.Slots[slotIndex].Attempts = []domain.ArenaGame{prior, replacement}
		}
	}
	authority.Game = replacement
	priorScoreID := domain.ArenaSeriesScoreRevisionID(task045ID(24))
	authority.Series.CurrentScoreRevisionID = &priorScoreID
	authority.GameClock = arena.PauseResumeGameClock{PauseID: authority.PauseID, GameID: replacement.ID,
		OriginalDeadline: now.Add(2 * time.Minute), Revision: 1}
	authority.GameRevision++
	authority.SeriesRevision++
	return prior
}

func task045SettlementIDs(base int) arena.ReconnectSettlementIDs {
	return arena.ReconnectSettlementIDs{GameResultRevisionID: domain.ArenaOfficialResultRevisionID(task045ID(base)),
		ScoreRevisionID:        domain.ArenaSeriesScoreRevisionID(task045ID(base + 1)),
		SeriesResultRevisionID: domain.ArenaOfficialResultRevisionID(task045ID(base + 2)), ReplayRouteID: task045ID(base + 3),
		AuditEventID: task045ID(base + 4), OutboxEventID: task045ID(base + 5), ProjectionRevisionID: task045ID(base + 6)}
}

func task045ID(value int) uuid.UUID {
	return uuid.MustParse(fmt.Sprintf("45000000-0000-0000-0000-%012d", value))
}

func task045Presence(t *testing.T, authority arena.ReconnectAuthority, participantID uuid.UUID) arena.PausePresence {
	t.Helper()
	for _, value := range authority.Presence {
		if value.ParticipantID == participantID {
			return value
		}
	}
	t.Fatalf("Presence for participant %s not found", participantID)
	return arena.PausePresence{}
}

func task045Counter(t *testing.T, authority arena.ReconnectAuthority, participantID uuid.UUID) arena.PauseReconnectCounter {
	t.Helper()
	for _, value := range authority.Counters {
		if value.ParticipantID == participantID {
			return value
		}
	}
	t.Fatalf("counter for participant %s not found", participantID)
	return arena.PauseReconnectCounter{}
}

func task045Interval(t *testing.T, authority arena.ReconnectAuthority, intervalID uuid.UUID) arena.PauseReconnectInterval {
	t.Helper()
	for _, value := range authority.Reconnect {
		if value.ID == intervalID {
			return value
		}
	}
	t.Fatalf("reconnect interval %s not found", intervalID)
	return arena.PauseReconnectInterval{}
}

func task045PresenceEqual(first, second arena.PausePresence) bool {
	if first.ID != second.ID || first.TournamentID != second.TournamentID || first.RosterID != second.RosterID ||
		first.SeriesID != second.SeriesID || first.ParticipantID != second.ParticipantID || first.State != second.State ||
		first.PresenceEpoch != second.PresenceEpoch || first.Revision != second.Revision ||
		!first.ConnectedAt.Equal(second.ConnectedAt) || !first.UpdatedAt.Equal(second.UpdatedAt) {
		return false
	}
	if first.DisconnectedAt == nil || second.DisconnectedAt == nil {
		return first.DisconnectedAt == nil && second.DisconnectedAt == nil
	}
	return first.DisconnectedAt.Equal(*second.DisconnectedAt)
}

func task045GameEqual(first, second domain.ArenaGame) bool {
	if first.ID != second.ID || first.SlotID != second.SlotID || first.AttemptNo != second.AttemptNo ||
		first.State != second.State || first.ResultReason != second.ResultReason {
		return false
	}
	if first.WinnerID == nil || second.WinnerID == nil {
		if first.WinnerID != nil || second.WinnerID != nil {
			return false
		}
	} else if *first.WinnerID != *second.WinnerID {
		return false
	}
	if first.ResultRevisionID == nil || second.ResultRevisionID == nil {
		return first.ResultRevisionID == nil && second.ResultRevisionID == nil
	}
	return *first.ResultRevisionID == *second.ResultRevisionID
}

func task045RecordCommandID(record arena.ReconnectRecord) uuid.UUID {
	switch {
	case record.ReconnectCommand != nil:
		return record.ReconnectCommand.CommandID
	case record.TimeoutCommand != nil:
		return record.TimeoutCommand.CommandID
	case record.DisconnectCommand != nil:
		return record.DisconnectCommand.CommandID
	default:
		return uuid.Nil
	}
}

func cloneTask045Record(value arena.ReconnectRecord) arena.ReconnectRecord {
	clone := value
	clone.Authority = cloneTask045Authority(value.Authority)
	if value.ReconnectCommand != nil {
		command := *value.ReconnectCommand
		clone.ReconnectCommand = &command
	}
	if value.TimeoutCommand != nil {
		command := *value.TimeoutCommand
		clone.TimeoutCommand = &command
	}
	if value.DisconnectCommand != nil {
		command := *value.DisconnectCommand
		if command.ContinuedFromID != nil {
			id := *command.ContinuedFromID
			command.ContinuedFromID = &id
		}
		clone.DisconnectCommand = &command
	}
	if value.GameResultRevision != nil {
		revision := *value.GameResultRevision
		clone.GameResultRevision = &revision
	}
	if value.VoidGameResultRevision != nil {
		revision := *value.VoidGameResultRevision
		clone.VoidGameResultRevision = &revision
	}
	if value.ScoreRevision != nil {
		revision := *value.ScoreRevision
		if revision.PreviousRevisionID != nil {
			previous := *revision.PreviousRevisionID
			revision.PreviousRevisionID = &previous
		}
		revision.GameResultRevisionIDs = append([]domain.ArenaOfficialResultRevisionID(nil), revision.GameResultRevisionIDs...)
		clone.ScoreRevision = &revision
	}
	if value.SeriesResultRevision != nil {
		revision := *value.SeriesResultRevision
		if revision.PreviousRevisionID != nil {
			previous := *revision.PreviousRevisionID
			revision.PreviousRevisionID = &previous
		}
		if revision.WinnerID != nil {
			winner := *revision.WinnerID
			revision.WinnerID = &winner
		}
		clone.SeriesResultRevision = &revision
	}
	if value.ReplayRoute != nil {
		route := *value.ReplayRoute
		clone.ReplayRoute = &route
	}
	if value.Evidence != nil {
		evidence := *value.Evidence
		clone.Evidence = &evidence
	}
	return clone
}

func cloneTask045Authority(value arena.ReconnectAuthority) arena.ReconnectAuthority {
	clone := value
	clone.Presence = append([]arena.PausePresence(nil), value.Presence...)
	for index := range clone.Presence {
		if value.Presence[index].DisconnectedAt != nil {
			at := *value.Presence[index].DisconnectedAt
			clone.Presence[index].DisconnectedAt = &at
		}
	}
	clone.Reconnect = append([]arena.PauseReconnectInterval(nil), value.Reconnect...)
	for index := range clone.Reconnect {
		if value.Reconnect[index].ContinuedFromID != nil {
			id := *value.Reconnect[index].ContinuedFromID
			clone.Reconnect[index].ContinuedFromID = &id
		}
		if value.Reconnect[index].ClosedAt != nil {
			at := *value.Reconnect[index].ClosedAt
			clone.Reconnect[index].ClosedAt = &at
		}
	}
	clone.Counters = append([]arena.PauseReconnectCounter(nil), value.Counters...)
	clone.CurrentGameResultRevisionIDs = append([]domain.ArenaOfficialResultRevisionID(nil), value.CurrentGameResultRevisionIDs...)
	clone.Series.Slots = append([]domain.ArenaGameSlot(nil), value.Series.Slots...)
	for slotIndex := range clone.Series.Slots {
		clone.Series.Slots[slotIndex].Attempts = append([]domain.ArenaGame(nil), value.Series.Slots[slotIndex].Attempts...)
	}
	if value.GameClock.ResumedAt != nil {
		at := *value.GameClock.ResumedAt
		clone.GameClock.ResumedAt = &at
	}
	if value.GameClock.ResumedDeadline != nil {
		at := *value.GameClock.ResumedDeadline
		clone.GameClock.ResumedDeadline = &at
	}
	if value.Current != nil {
		current := *value.Current
		if value.Current.GameResultRevision != nil {
			revision := *value.Current.GameResultRevision
			current.GameResultRevision = &revision
		}
		if value.Current.VoidGameResultRevision != nil {
			revision := *value.Current.VoidGameResultRevision
			current.VoidGameResultRevision = &revision
		}
		current.ScoreRevision.GameResultRevisionIDs = append([]domain.ArenaOfficialResultRevisionID(nil), value.Current.ScoreRevision.GameResultRevisionIDs...)
		if value.Current.ScoreRevision.PreviousRevisionID != nil {
			previous := *value.Current.ScoreRevision.PreviousRevisionID
			current.ScoreRevision.PreviousRevisionID = &previous
		}
		if value.Current.SeriesResultRevision != nil {
			revision := *value.Current.SeriesResultRevision
			if revision.PreviousRevisionID != nil {
				previous := *revision.PreviousRevisionID
				revision.PreviousRevisionID = &previous
			}
			if revision.WinnerID != nil {
				winner := *revision.WinnerID
				revision.WinnerID = &winner
			}
			current.SeriesResultRevision = &revision
		}
		if value.Current.ReplayRoute != nil {
			route := *value.Current.ReplayRoute
			current.ReplayRoute = &route
		}
		clone.Current = &current
	}
	return clone
}
