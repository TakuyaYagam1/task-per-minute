package arena_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestDoubleDisconnectSerialization(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 1, 15, 0, 0, 0, time.UTC)
	authority := task045Authority(now, false, false)
	repository := newTask045RepositoryFake(authority)
	repository.barrier = newTask045Barrier()
	useCase := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now})
	commands := []arena.DisconnectCommand{
		{Scope: authority.Scope, CommandID: task045ID(200), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: task045ID(201), Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(202)},
		{Scope: authority.Scope, CommandID: task045ID(210), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: task045ID(211), Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(212)},
	}
	type result struct {
		record  *arena.ReconnectRecord
		changed bool
		err     error
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	results := make(chan result, len(commands))
	for _, command := range commands {
		go func() {
			record, changed, err := useCase.Disconnect(ctx, command)
			results <- result{record: record, changed: changed, err: err}
		}()
	}
	if err := repository.barrier.await(ctx); err != nil {
		t.Fatalf("await concurrent loads: %v", err)
	}
	for range commands {
		select {
		case result := <-results:
			if result.err != nil || !result.changed || result.record == nil {
				t.Fatalf("concurrent Disconnect() error = %v, changed = %v, record nil = %v", result.err, result.changed, result.record == nil)
			}
		case <-ctx.Done():
			t.Fatalf("wait concurrent Disconnect(): %v", ctx.Err())
		}
	}

	if repository.writeCount() != 2 || repository.conflictCount() != 1 {
		t.Fatalf("CAS writes = %d, conflicts = %d, want 2 writes and 1 retry", repository.writeCount(), repository.conflictCount())
	}
	stored := repository.snapshot()
	if stored.Revision != authority.Revision+2 || stored.Game.State != "paused" || stored.GameRevision != authority.GameRevision+1 ||
		stored.GameClock.Revision != authority.GameClock.Revision+1 || stored.GameClock.Remaining != authority.GameClock.OriginalDeadline.Sub(now) {
		t.Fatalf("serialized authority = revision %d, Game %+v, clock %+v", stored.Revision, stored.Game, stored.GameClock)
	}
	for _, command := range commands {
		presence := task045Presence(t, stored, command.ParticipantID)
		counter := task045Counter(t, stored, command.ParticipantID)
		interval := task045Interval(t, stored, command.IntervalID)
		if presence.State != arena.PresenceStateDisconnected || presence.PresenceEpoch != 2 ||
			counter.Used != 1 || counter.Revision != 2 || interval.State != arena.ReconnectStateOpen ||
			interval.Number != 1 || interval.PresenceEpoch != presence.PresenceEpoch {
			t.Fatalf("participant %s lost serialized update: Presence %+v, counter %+v, interval %+v", command.ParticipantID, presence, counter, interval)
		}
	}
	if len(stored.Reconnect) != 2 {
		t.Fatalf("reconnect interval count = %d, want 2", len(stored.Reconnect))
	}

	writes := repository.writeCount()
	for _, command := range commands {
		replayed, changed, err := useCase.Disconnect(t.Context(), command)
		if err != nil || changed || replayed == nil {
			t.Fatalf("Disconnect(replay %s) error = %v, changed = %v", command.ParticipantID, err, changed)
		}
	}
	if repository.writeCount() != writes {
		t.Fatalf("replays wrote %d additional mutations", repository.writeCount()-writes)
	}

	t.Run("exhausted_counters_commit_one_terminal_outcome", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		for index := range authority.Counters {
			task045AddCompletedRoots(&authority, index, arena.ReconnectCycleLimit, now)
		}
		task045SetResumedClock(&authority, now)
		repository := newTask045RepositoryFake(authority)
		repository.barrier = newTask045Barrier()
		useCase := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now})
		commands := []arena.DisconnectCommand{
			{Scope: authority.Scope, CommandID: task045ID(400), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: task045ID(401), Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(402)},
			{Scope: authority.Scope, CommandID: task045ID(410), ParticipantID: authority.Series.SecondParticipantID,
				IntervalID: task045ID(411), Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(412)},
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		type terminalResult struct {
			record  *arena.ReconnectRecord
			changed bool
			err     error
		}
		results := make(chan terminalResult, 2)
		for _, command := range commands {
			go func() {
				record, changed, err := useCase.Disconnect(ctx, command)
				results <- terminalResult{record: record, changed: changed, err: err}
			}()
		}
		if err := repository.barrier.await(ctx); err != nil {
			t.Fatalf("await exhausted loads: %v", err)
		}
		var records [2]*arena.ReconnectRecord
		changedCount := 0
		for index := range records {
			select {
			case result := <-results:
				if result.err != nil || result.record == nil {
					t.Fatalf("Disconnect(exhausted) error = %v, record nil = %v", result.err, result.record == nil)
				}
				records[index] = result.record
				if result.changed {
					changedCount++
				}
			case <-ctx.Done():
				t.Fatalf("wait exhausted result: %v", ctx.Err())
			}
		}
		if changedCount != 1 || repository.writeCount() != 1 || repository.conflictCount() != 1 {
			t.Fatalf("terminal changes = %d, writes = %d, conflicts = %d, want 1/1/1", changedCount, repository.writeCount(), repository.conflictCount())
		}
		stored := repository.snapshot()
		if stored.Current == nil || stored.Game.ResultRevisionID == nil || stored.Current.GameResultRevision == nil ||
			*stored.Game.ResultRevisionID != stored.Current.GameResultRevision.ID {
			t.Fatalf("stored terminal outcome missing: %+v", stored.Current)
		}
		for _, record := range records {
			if record.GameResultRevision == nil || record.ScoreRevision == nil || record.Authority.Current == nil ||
				record.GameResultRevision.ID != stored.Current.GameResultRevision.ID || record.ScoreRevision.ID != stored.Current.ScoreRevision.ID {
				t.Fatalf("losing worker did not receive committed outcome: %+v", record)
			}
		}
	})

	t.Run("disconnect_rejects_aggregate_history_rollback", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		repository := newTask045RepositoryFake(authority)
		firstDisconnectAt := now.Add(10 * time.Second)
		firstCommand := arena.DisconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(900), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: task045ID(901), Deadline: now.Add(40 * time.Second), Settlement: task045SettlementIDs(902),
		}
		if _, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: firstDisconnectAt}).Disconnect(t.Context(), firstCommand); err != nil || !changed {
			t.Fatalf("Disconnect(first at T10) error = %v, changed = %v", err, changed)
		}

		rollbackCommand := arena.DisconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(910), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: task045ID(911), Deadline: now.Add(35 * time.Second), Settlement: task045SettlementIDs(912),
		}
		_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now.Add(5 * time.Second)}).Disconnect(t.Context(), rollbackCommand)
		if !errors.Is(err, arena.ErrInvalidReconnectMutation) || !strings.Contains(err.Error(), "clock rollback") || changed || repository.commitCount() != 1 {
			t.Fatalf("Disconnect(second at T5) error = %v, changed = %v, commits = %d", err, changed, repository.commitCount())
		}

		equalCommand := rollbackCommand
		equalCommand.CommandID = task045ID(920)
		equalCommand.IntervalID = task045ID(921)
		if _, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: firstDisconnectAt}).Disconnect(t.Context(), equalCommand); err != nil || !changed {
			t.Fatalf("Disconnect(second at equal T10) error = %v, changed = %v", err, changed)
		}
		if repository.commitCount() != 2 {
			t.Fatalf("commits after equal timestamp = %d, want 2", repository.commitCount())
		}
	})

	t.Run("reconnect_rejects_aggregate_history_rollback", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		preparationRepository := newTask045RepositoryFake(authority)
		disconnectAt := now.Add(10 * time.Second)
		firstDisconnect := arena.DisconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(930), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: task045ID(931), Deadline: now.Add(40 * time.Second), Settlement: task045SettlementIDs(932),
		}
		secondDisconnect := arena.DisconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(940), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: task045ID(941), Deadline: now.Add(40 * time.Second), Settlement: task045SettlementIDs(942),
		}
		for _, command := range []arena.DisconnectCommand{firstDisconnect, secondDisconnect} {
			if _, changed, err := arena.NewDisconnectUseCase(preparationRepository, fixedArenaClock{now: disconnectAt}).Disconnect(t.Context(), command); err != nil || !changed {
				t.Fatalf("Disconnect(equal T10 %s) error = %v, changed = %v", command.ParticipantID, err, changed)
			}
		}
		firstReconnect := arena.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(950), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: firstDisconnect.IntervalID, Settlement: task045SettlementIDs(952),
		}
		if _, changed, err := arena.NewReconnectUseCase(preparationRepository, fixedArenaClock{now: now.Add(11 * time.Second)}).Reconnect(t.Context(), firstReconnect); err != nil || !changed {
			t.Fatalf("Reconnect(first at T11) error = %v, changed = %v", err, changed)
		}

		legacy := preparationRepository.snapshot()
		secondPresence := &legacy.Presence[1]
		secondInterval := &legacy.Reconnect[1]
		rolledBackAt := now.Add(5 * time.Second)
		secondPresence.DisconnectedAt = &rolledBackAt
		secondPresence.UpdatedAt = rolledBackAt
		secondInterval.OpenedAt = rolledBackAt
		secondInterval.UpdatedAt = rolledBackAt
		repository := newTask045RepositoryFake(legacy)
		secondReconnect := arena.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(970), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: secondDisconnect.IntervalID, Settlement: task045SettlementIDs(972),
		}
		_, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now.Add(6 * time.Second)}).Reconnect(t.Context(), secondReconnect)
		if !errors.Is(err, arena.ErrInvalidReconnectMutation) || !strings.Contains(err.Error(), "clock rollback") || changed || repository.commitCount() != 0 {
			t.Fatalf("Reconnect(second at T6) error = %v, changed = %v, commits = %d", err, changed, repository.commitCount())
		}
	})
}
