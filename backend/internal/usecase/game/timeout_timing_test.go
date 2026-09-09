package game_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func testReconnectTimeoutTiming(t *testing.T, base time.Time) {
	t.Helper()

	t.Run("staggered_deadlines_defer_until_second_outcome", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		firstDeadline := authority.Reconnect[0].Deadline
		authority.Reconnect[1].Deadline = firstDeadline.Add(20 * time.Second)
		first := gameusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(80),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(81)}
		repository := newTask045RepositoryHarness(t, authority)
		deferred, changed, err := gameusecase.NewTimeoutUseCase(repository, newReconnectClock(t, firstDeadline)).Expire(t.Context(), first)
		if err != nil || !changed || repository.writeCount() != 1 {
			t.Fatalf("Expire(first staggered) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		if deferred.ReconnectAuthority.Game.State != domain.GameStatePaused || deferred.ReconnectAuthority.Current != nil ||
			task045Interval(t, deferred.ReconnectAuthority, authority.Reconnect[0].ID).State != pause.ReconnectStateExpired ||
			task045Interval(t, deferred.ReconnectAuthority, authority.Reconnect[1].ID).State != pause.ReconnectStateOpen ||
			deferred.ScoreRevision != nil {
			t.Fatalf("first staggered deadline terminalized early: %+v", deferred)
		}

		reconnectAt := firstDeadline.Add(10 * time.Second)
		reconnected, changed, err := gameusecase.ReconnectNewUseCase(repository, newReconnectClock(t, reconnectAt)).Reconnect(t.Context(), gameusecase.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(90), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(91),
		})
		if err != nil || !changed || repository.writeCount() != 2 || reconnected.ReconnectAuthority.Current == nil ||
			reconnected.ReconnectAuthority.Game.State != domain.GameStateCompleted || reconnected.ReconnectAuthority.Game.WinnerID == nil ||
			*reconnected.ReconnectAuthority.Game.WinnerID != authority.Series.SecondParticipantID {
			t.Fatalf("Reconnect(after opponent expiry) error = %v, changed = %v, record = %+v", err, changed, reconnected)
		}
	})

	t.Run("deferred_expiry_rejects_clock_rollback", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		firstDeadline := authority.Reconnect[0].Deadline
		authority.Reconnect[1].Deadline = firstDeadline.Add(20 * time.Second)
		repository := newTask045RepositoryHarness(t, authority)
		first := gameusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(860),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(861)}
		if _, changed, err := gameusecase.NewTimeoutUseCase(repository, newReconnectClock(t, firstDeadline)).Expire(t.Context(), first); err != nil || !changed {
			t.Fatalf("Expire(seed deferred) error = %v, changed = %v", err, changed)
		}
		rollbackAt := firstDeadline.Add(-5 * time.Second)
		_, changed, err := gameusecase.ReconnectNewUseCase(repository, newReconnectClock(t, rollbackAt)).Reconnect(t.Context(), gameusecase.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(870), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(871),
		})
		if !errors.Is(err, gameusecase.ErrInvalidMutation) || !strings.Contains(err.Error(), "clock rollback") ||
			changed || repository.writeCount() != 1 || repository.commitCount() != 1 {
			t.Fatalf("Reconnect(deferred rollback) error = %v, changed = %v, writes = %d, commits = %d", err, changed, repository.writeCount(), repository.commitCount())
		}
		stored := repository.snapshot()
		if stored.Current != nil || task045Interval(t, stored, authority.Reconnect[1].ID).State != pause.ReconnectStateOpen {
			t.Fatalf("deferred rollback mutated authority: %+v", stored)
		}
	})

	t.Run("staggered_second_timeout_routes_replay", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		firstDeadline := authority.Reconnect[0].Deadline
		secondDeadline := firstDeadline.Add(20 * time.Second)
		authority.Reconnect[1].Deadline = secondDeadline
		repository := newTask045RepositoryHarness(t, authority)
		first := gameusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(300),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(301)}
		if _, changed, err := gameusecase.NewTimeoutUseCase(repository, newReconnectClock(t, firstDeadline)).Expire(t.Context(), first); err != nil || !changed {
			t.Fatalf("Expire(first staggered) error = %v, changed = %v", err, changed)
		}
		second := gameusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(310),
			ParticipantID: authority.Series.SecondParticipantID, IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(311)}
		terminal, changed, err := gameusecase.NewTimeoutUseCase(repository, newReconnectClock(t, secondDeadline)).Expire(t.Context(), second)
		if err != nil || !changed || repository.writeCount() != 2 || terminal.ReconnectAuthority.Current == nil ||
			terminal.ReconnectAuthority.Game.State != domain.GameStateVoid || terminal.ReconnectAuthority.Series.State != domain.SeriesStateReplayRequired {
			t.Fatalf("Expire(second staggered) error = %v, changed = %v, record = %+v", err, changed, terminal)
		}
	})

	t.Run("equal_deadline_race_commits_one_replay", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		deadline := authority.Reconnect[0].Deadline
		repository := newTask045RepositoryHarness(t, authority)
		repository.barrier = newTask045Barrier()
		commands := []gameusecase.TimeoutCommand{
			{Scope: authority.Scope, CommandID: task045ID(500), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(501)},
			{Scope: authority.Scope, CommandID: task045ID(510), ParticipantID: authority.Series.SecondParticipantID,
				IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(511)},
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		type result struct {
			record  *gameusecase.ReconnectRecord
			changed bool
			err     error
		}
		results := make(chan result, 2)
		for _, command := range commands {
			go func() {
				record, changed, err := gameusecase.NewTimeoutUseCase(repository, newReconnectClock(t, deadline)).Expire(ctx, command)
				results <- result{record: record, changed: changed, err: err}
			}()
		}
		if err := repository.barrier.await(ctx); err != nil {
			t.Fatalf("await equal deadlines: %v", err)
		}
		changedCount := 0
		var resultID domain.OfficialResultRevisionID
		for range commands {
			select {
			case result := <-results:
				if result.err != nil || result.record == nil || result.record.VoidGameResultRevision == nil || result.record.ScoreRevision == nil {
					t.Fatalf("Expire(equal race) error = %v, record = %+v", result.err, result.record)
				}
				if result.changed {
					changedCount++
				}
				if resultID.IsZero() {
					resultID = result.record.VoidGameResultRevision.ID
				} else if resultID != result.record.VoidGameResultRevision.ID {
					t.Fatal("equal deadline workers observed different terminal results")
				}
			case <-ctx.Done():
				t.Fatalf("wait equal deadlines: %v", ctx.Err())
			}
		}
		if changedCount != 1 || repository.writeCount() != 1 || repository.conflictCount() != 1 {
			t.Fatalf("equal race changes = %d, writes = %d, conflicts = %d", changedCount, repository.writeCount(), repository.conflictCount())
		}
	})
}
