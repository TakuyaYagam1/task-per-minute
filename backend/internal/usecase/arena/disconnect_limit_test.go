package arena_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestDisconnectCycleLimit(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, time.September, 1, 14, 0, 0, 0, time.UTC)

	for _, test := range []struct {
		name       string
		used       int
		wantNumber int
	}{
		{name: "first_cycle", used: 0, wantNumber: 1},
		{name: "second_cycle", used: 1, wantNumber: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			authority := task045Authority(now, false, false)
			if test.used > 0 {
				task045AddCompletedRoots(&authority, 0, test.used, now)
				task045SetResumedClock(&authority, now)
			}
			command := arena.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(100 + test.used*10),
				ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(101 + test.used*10),
				Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(102 + test.used*10)}
			repository := newTask045RepositoryFake(authority)
			useCase := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now})
			disconnected, changed, err := useCase.Disconnect(t.Context(), command)
			if err != nil || !changed || repository.writeCount() != 1 {
				t.Fatalf("Disconnect() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
			}
			presence := task045Presence(t, disconnected.Authority, command.ParticipantID)
			if presence.State != arena.PresenceStateDisconnected || presence.PresenceEpoch != authority.Presence[0].PresenceEpoch+1 ||
				presence.Revision != authority.Presence[0].Revision+1 || presence.DisconnectedAt == nil || !presence.DisconnectedAt.Equal(now) {
				t.Fatalf("disconnected Presence = %+v", presence)
			}
			counter := task045Counter(t, disconnected.Authority, command.ParticipantID)
			if counter.Used != test.wantNumber || counter.Limit != arena.ReconnectCycleLimit || counter.Revision != authority.Counters[0].Revision+1 {
				t.Fatalf("reconnect counter = %+v", counter)
			}
			interval := task045Interval(t, disconnected.Authority, command.IntervalID)
			if interval.Number != test.wantNumber || interval.ContinuationNumber != 0 || interval.ContinuedFromID != nil ||
				interval.PresenceEpoch != presence.PresenceEpoch || interval.State != arena.ReconnectStateOpen ||
				!interval.OpenedAt.Equal(now) || !interval.Deadline.Equal(command.Deadline) {
				t.Fatalf("root reconnect interval = %+v", interval)
			}
			if disconnected.Authority.Game.State != domain.ArenaGameStatePaused || disconnected.GameResultRevision != nil || disconnected.ScoreRevision != nil {
				t.Fatalf("allowed cycle terminalized Game: %+v", disconnected.Authority.Game)
			}
			if test.used == 1 && (disconnected.Authority.GameClock.OriginalDeadline != now.Add(90*time.Second) ||
				disconnected.Authority.GameClock.Remaining != 90*time.Second || disconnected.Authority.GameClock.ResumedDeadline != nil) {
				t.Fatalf("second freeze ignored current resumed deadline: %+v", disconnected.Authority.GameClock)
			}
			if got := task045Presence(t, disconnected.Authority, authority.Series.SecondParticipantID); !task045PresenceEqual(got, authority.Presence[1]) {
				t.Fatalf("opponent Presence changed: got %+v want %+v", got, authority.Presence[1])
			}
		})
	}

	t.Run("third_fresh_disconnect_is_immediate_autoloss", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		task045AddCompletedRoots(&authority, 0, arena.ReconnectCycleLimit, now)
		task045SetResumedClock(&authority, now)
		beforeClock := authority.GameClock
		command := arena.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(130),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(131),
			Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(132)}
		repository := newTask045RepositoryFake(authority)
		terminal, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), command)
		if err != nil || !changed || repository.writeCount() != 1 {
			t.Fatalf("Disconnect(third) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		if len(terminal.Authority.Reconnect) != arena.ReconnectCycleLimit || task045Counter(t, terminal.Authority, command.ParticipantID).Used != arena.ReconnectCycleLimit {
			t.Fatalf("third disconnect consumed another slot: intervals = %d, counter = %+v", len(terminal.Authority.Reconnect), task045Counter(t, terminal.Authority, command.ParticipantID))
		}
		if task045Presence(t, terminal.Authority, command.ParticipantID).State != arena.PresenceStateDisconnected ||
			terminal.Authority.Game.State != domain.ArenaGameStateCompleted || terminal.Authority.Game.ResultReason != domain.ArenaGameResultReasonOperatorForfeit ||
			terminal.Authority.Game.WinnerID == nil || *terminal.Authority.Game.WinnerID != authority.Series.SecondParticipantID ||
			terminal.ScoreRevision == nil || terminal.ScoreRevision.ScoreAfter.SecondParticipantWins != 1 {
			t.Fatalf("third disconnect terminal = %+v", terminal)
		}
		clock := terminal.Authority.GameClock
		if clock.ResumedAt != nil || clock.ResumedDeadline != nil || !clock.FrozenAt.Equal(now) ||
			!clock.OriginalDeadline.Equal(*beforeClock.ResumedDeadline) || clock.Remaining != beforeClock.ResumedDeadline.Sub(now) ||
			clock.Revision != beforeClock.Revision+1 || terminal.Authority.GameRevision != authority.GameRevision+1 ||
			terminal.Authority.SeriesRevision != authority.SeriesRevision+1 {
			t.Fatalf("terminal clock was not frozen exactly once: before = %+v, after = %+v", beforeClock, clock)
		}
	})

	t.Run("replacement_game_keeps_stable_disconnect_quota", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		task045AddCompletedRoots(&authority, 0, arena.ReconnectCycleLimit, now)
		prior := task045ReplaceCurrentGame(&authority, task045ID(825), now)
		if authority.Counters[0].Used != arena.ReconnectCycleLimit || authority.Game.AttemptNo != 2 ||
			len(authority.Series.Slots[0].Attempts) != 2 || authority.Series.Slots[0].Attempts[0].State != domain.ArenaGameStateVoid {
			t.Fatalf("replacement fixture is not durable: authority = %+v", authority)
		}
		for _, interval := range authority.Reconnect {
			if interval.ParticipantID == authority.Series.FirstParticipantID && interval.GameID != prior.ID {
				t.Fatalf("completed cycle was not retained on predecessor Game: %+v", interval)
			}
		}
		command := arena.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(826),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(827),
			Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(828)}
		repository := newTask045RepositoryFake(authority)
		terminal, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), command)
		if err != nil || !changed || repository.writeCount() != 1 || terminal.Authority.Game.WinnerID == nil ||
			*terminal.Authority.Game.WinnerID != authority.Series.SecondParticipantID ||
			task045Counter(t, terminal.Authority, command.ParticipantID).Used != arena.ReconnectCycleLimit ||
			len(terminal.Authority.Reconnect) != arena.ReconnectCycleLimit {
			t.Fatalf("Disconnect(replacement third) error = %v, changed = %v, writes = %d, record = %+v", err, changed, repository.writeCount(), terminal)
		}
	})

	t.Run("replacement_history_rejects_invalid_binding", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*arena.ReconnectAuthority, domain.ArenaGame)
		}{
			{name: "foreign_game", mutate: func(authority *arena.ReconnectAuthority, _ domain.ArenaGame) {
				authority.Reconnect[0].GameID = task045ID(840)
			}},
			{name: "future_presence_epoch", mutate: func(authority *arena.ReconnectAuthority, _ domain.ArenaGame) {
				authority.Reconnect[0].PresenceEpoch = authority.Presence[0].PresenceEpoch + 1
			}},
			{name: "duplicate_root_presence_epoch", mutate: func(authority *arena.ReconnectAuthority, _ domain.ArenaGame) {
				authority.Reconnect[1].PresenceEpoch = authority.Reconnect[0].PresenceEpoch
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, arena.ReconnectCycleLimit, now)
				prior := task045ReplaceCurrentGame(&authority, task045ID(841), now)
				test.mutate(&authority, prior)
				repository := newTask045RepositoryFake(authority)
				_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), arena.DisconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(842), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: task045ID(843), Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(844),
				})
				if !errors.Is(err, arena.ErrInvalidReconnectMutation) || changed || repository.commitCount() != 0 {
					t.Fatalf("Disconnect(%s) error = %v, changed = %v, commits = %d", test.name, err, changed, repository.commitCount())
				}
			})
		}

		t.Run("open_interval_on_predecessor_game", func(t *testing.T) {
			authority := task045Authority(now, true, false)
			task045ReplaceCurrentGame(&authority, task045ID(848), now)
			authority.Game.State = domain.ArenaGameStatePaused
			authority.Series.Slots[0].Attempts[1] = authority.Game
			authority.GameClock.FrozenAt = now.Add(-5 * time.Second)
			authority.GameClock.Remaining = 40 * time.Second
			authority.GameClock.OriginalDeadline = authority.GameClock.FrozenAt.Add(authority.GameClock.Remaining)
			authority.GameClock.Revision = 2
			repository := newTask045RepositoryFake(authority)
			_, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: now}).Reconnect(t.Context(), arena.ReconnectCommand{
				Scope: authority.Scope, CommandID: task045ID(849), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(850),
			})
			if !errors.Is(err, arena.ErrInvalidReconnectMutation) || changed || repository.commitCount() != 0 {
				t.Fatalf("Reconnect(open predecessor Game) error = %v, changed = %v, commits = %d", err, changed, repository.commitCount())
			}
		})
	})

	t.Run("resumed_clock_rejects_rollback", func(t *testing.T) {
		for _, offset := range []time.Duration{-time.Nanosecond, 0} {
			t.Run(offset.String(), func(t *testing.T) {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, 1, now)
				if authority.GameRevision != 6 || authority.SeriesRevision != 5 || authority.GameClock.Revision != 3 {
					t.Fatalf("one completed cycle revisions = Game %d, Series %d, clock %d", authority.GameRevision, authority.SeriesRevision, authority.GameClock.Revision)
				}
				if !authority.Presence[1].UpdatedAt.Before(*authority.GameClock.ResumedAt) || authority.Counters[1].Used != 0 {
					t.Fatalf("rollback participant is not independent from completed cycle: Presence %+v, counter %+v", authority.Presence[1], authority.Counters[1])
				}
				at := authority.GameClock.ResumedAt.Add(offset)
				beforeRemaining := authority.GameClock.Remaining
				repository := newTask045RepositoryFake(authority)
				_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: at}).Disconnect(t.Context(), arena.DisconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(850), ParticipantID: authority.Series.SecondParticipantID,
					IntervalID: task045ID(851), Deadline: at.Add(time.Minute), Settlement: task045SettlementIDs(852),
				})
				if !errors.Is(err, arena.ErrInvalidReconnectMutation) || !strings.Contains(err.Error(), "clock rollback") ||
					changed || repository.commitCount() != 0 || repository.snapshot().GameClock.Remaining != beforeRemaining {
					t.Fatalf("Disconnect(clock rollback %s) error = %v, changed = %v, commits = %d", offset, err, changed, repository.commitCount())
				}
			})
		}
	})

	t.Run("third_disconnect_while_both_absent_routes_replay_and_cancels_open_opponent", func(t *testing.T) {
		authority := task045Authority(now, false, true)
		task045AddCompletedRoots(&authority, 0, arena.ReconnectCycleLimit, now)
		opponentInterval := authority.Reconnect[0]
		command := arena.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(135),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(136),
			Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(137)}
		repository := newTask045RepositoryFake(authority)
		terminal, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), command)
		if err != nil || !changed || terminal.Authority.Game.State != domain.ArenaGameStateVoid ||
			terminal.Authority.Series.State != domain.ArenaSeriesStateReplayRequired || terminal.ReplayRoute == nil {
			t.Fatalf("Disconnect(third both absent) error = %v, changed = %v, record = %+v", err, changed, terminal)
		}
		closed := task045Interval(t, terminal.Authority, opponentInterval.ID)
		if closed.State != arena.ReconnectStateCancelled || closed.ClosedAt == nil || !closed.ClosedAt.Equal(now) ||
			!closed.Deadline.After(now) {
			t.Fatalf("opponent interval was expired early or left open: %+v", closed)
		}
	})

	t.Run("continuation_keeps_stable_used_slot", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		task045AddPriorRootForCurrentCycle(&authority, 0, now)
		source := &authority.Reconnect[0]
		if source.ParticipantID != authority.Series.FirstParticipantID {
			source = &authority.Reconnect[1]
		}
		source.State = arena.ReconnectStateCancelled
		closedAt := now.Add(-time.Second)
		source.ClosedAt = &closedAt
		suspendedBy := task045ID(145)
		source.SuspendedByPauseID = &suspendedBy
		source.UpdatedAt = closedAt
		source.Revision++
		continuedFrom := source.ID
		remaining := source.Deadline.Sub(closedAt)
		command := arena.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(140),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(141), Deadline: now.Add(remaining),
			ContinuedFromID: &continuedFrom, Settlement: task045SettlementIDs(142)}
		repository := newTask045RepositoryFake(authority)
		continued, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Disconnect(continuation) error = %v, changed = %v", err, changed)
		}
		counter := task045Counter(t, continued.Authority, command.ParticipantID)
		interval := task045Interval(t, continued.Authority, command.IntervalID)
		if counter.Used != arena.ReconnectCycleLimit || counter.Revision != authority.Counters[0].Revision ||
			interval.Number != arena.ReconnectCycleLimit || interval.ContinuationNumber != 1 ||
			interval.ContinuedFromID == nil || *interval.ContinuedFromID != source.ID ||
			continued.DisconnectCommand.ContinuedFromID == nil || *continued.DisconnectCommand.ContinuedFromID != source.ID {
			t.Fatalf("continuation reset slot or retained mutable input: counter %+v, interval %+v, command %+v", counter, interval, continued.DisconnectCommand)
		}
		writes := repository.writeCount()
		if _, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), command); err != nil || changed || repository.writeCount() != writes {
			t.Fatalf("Disconnect(continuation replay) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		continuedFrom = task045ID(199)
		if continued.DisconnectCommand.ContinuedFromID == nil || *continued.DisconnectCommand.ContinuedFromID != source.ID {
			t.Fatal("mutating continuation input changed returned receipt")
		}
		reused := command
		if _, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), reused); !errors.Is(err, arena.ErrReconnectCommandReuse) || changed {
			t.Fatalf("Disconnect(continuation command reuse) error = %v, changed = %v", err, changed)
		}
	})

	t.Run("continuation_rejects_expired_and_extension_without_write", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			state     arena.ReconnectState
			extension time.Duration
			want      error
		}{
			{name: "expired", state: arena.ReconnectStateExpired, want: arena.ErrReconnectUnavailable},
			{name: "extension", state: arena.ReconnectStateCancelled, extension: time.Nanosecond, want: arena.ErrReconnectDeadline},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := task045Authority(now, true, false)
				authority.Counters[0].Used = 1
				source := &authority.Reconnect[0]
				source.State = test.state
				closedAt := now.Add(-time.Second)
				source.ClosedAt = &closedAt
				source.UpdatedAt = closedAt
				source.Revision++
				if test.state == arena.ReconnectStateCancelled {
					suspendedBy := task045ID(160)
					source.SuspendedByPauseID = &suspendedBy
				}
				continuedFrom := source.ID
				command := arena.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(161),
					ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(162),
					Deadline: now.Add(source.Deadline.Sub(closedAt)).Add(test.extension), ContinuedFromID: &continuedFrom,
					Settlement: task045SettlementIDs(163)}
				repository := newTask045RepositoryFake(authority)
				_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), command)
				if !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Disconnect(%s) error = %v, changed = %v, writes = %d", test.name, err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("continuation_fork_serializes_one_child", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		source := &authority.Reconnect[0]
		source.State = arena.ReconnectStateCancelled
		closedAt := now.Add(-time.Second)
		source.ClosedAt = &closedAt
		source.UpdatedAt = closedAt
		suspendedBy := task045ID(170)
		source.SuspendedByPauseID = &suspendedBy
		source.Revision++
		continuedFrom := source.ID
		deadline := now.Add(source.Deadline.Sub(closedAt))
		commands := []arena.DisconnectCommand{
			{Scope: authority.Scope, CommandID: task045ID(171), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: task045ID(172), Deadline: deadline, ContinuedFromID: &continuedFrom, Settlement: task045SettlementIDs(173)},
			{Scope: authority.Scope, CommandID: task045ID(181), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: task045ID(182), Deadline: deadline, ContinuedFromID: &continuedFrom, Settlement: task045SettlementIDs(183)},
		}
		repository := newTask045RepositoryFake(authority)
		repository.barrier = newTask045Barrier()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		results := make(chan error, 2)
		for _, command := range commands {
			go func() {
				_, _, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(ctx, command)
				results <- err
			}()
		}
		if err := repository.barrier.await(ctx); err != nil {
			t.Fatalf("await continuation fork: %v", err)
		}
		successes, rejected := 0, 0
		for range commands {
			select {
			case err := <-results:
				switch {
				case err == nil:
					successes++
				case errors.Is(err, arena.ErrReconnectUnavailable):
					rejected++
				default:
					t.Fatalf("continuation fork error = %v", err)
				}
			case <-ctx.Done():
				t.Fatalf("wait continuation fork: %v", ctx.Err())
			}
		}
		if successes != 1 || rejected != 1 || repository.writeCount() != 1 || repository.conflictCount() != 1 {
			t.Fatalf("fork successes = %d, rejected = %d, writes = %d, conflicts = %d", successes, rejected, repository.writeCount(), repository.conflictCount())
		}
	})

	t.Run("expired_window_has_no_write", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		command := arena.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(150),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(151), Deadline: now,
			Settlement: task045SettlementIDs(152)}
		repository := newTask045RepositoryFake(authority)
		_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), command)
		if !errors.Is(err, arena.ErrReconnectDeadline) || changed || repository.writeCount() != 0 {
			t.Fatalf("Disconnect(expired window) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("fresh_disconnect_rejects_duplicate_malformed_and_overflow_without_write", func(t *testing.T) {
		baseAuthority := task045Authority(now, false, false)
		task045AddCompletedRoots(&baseAuthority, 0, 1, now)
		oldIntervalID := baseAuthority.Reconnect[0].ID
		for _, test := range []struct {
			name       string
			mutate     func(*arena.ReconnectAuthority)
			intervalID uuid.UUID
			want       error
			detail     string
		}{
			{name: "duplicate interval", intervalID: oldIntervalID, want: arena.ErrReconnectUnavailable},
			{name: "malformed counter", intervalID: task045ID(196), mutate: func(value *arena.ReconnectAuthority) { value.Counters[0].Limit = 3 }, want: arena.ErrInvalidReconnectMutation},
			{name: "authority overflow", intervalID: task045ID(197), mutate: func(value *arena.ReconnectAuthority) { value.Revision = math.MaxInt64 }, want: arena.ErrInvalidReconnectMutation, detail: "revision overflow"},
			{name: "counter overflow", intervalID: task045ID(199), mutate: func(value *arena.ReconnectAuthority) { value.Counters[0].Revision = math.MaxInt64 }, want: arena.ErrInvalidReconnectMutation, detail: "counter revision overflow"},
			{name: "ordinal overflow", intervalID: task045ID(198), mutate: func(value *arena.ReconnectAuthority) {
				task045AddCompletedRoots(value, 0, arena.ReconnectCycleLimit, now)
				value.CurrentOrdinal = int(^uint(0) >> 1)
			}, want: arena.ErrInvalidReconnectMutation, detail: "terminal lineage overflow"},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := cloneTask045Authority(baseAuthority)
				if test.mutate != nil {
					test.mutate(&authority)
				}
				repository := newTask045RepositoryFake(authority)
				_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), arena.DisconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(190), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: test.intervalID, Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(191),
				})
				if !errors.Is(err, test.want) || (test.detail != "" && !strings.Contains(err.Error(), test.detail)) ||
					changed || repository.writeCount() != 0 || repository.commitCount() != 0 {
					t.Fatalf("Disconnect(%s) error = %v, changed = %v, writes = %d, commits = %d", test.name, err, changed, repository.writeCount(), repository.commitCount())
				}
			})
		}
	})

	t.Run("mutations_require_strictly_newer_timestamps", func(t *testing.T) {
		t.Run("presence", func(t *testing.T) {
			authority := task045Authority(now, false, false)
			authority.Presence[0].UpdatedAt = now
			repository := newTask045RepositoryFake(authority)
			_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), arena.DisconnectCommand{
				Scope: authority.Scope, CommandID: task045ID(750), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: task045ID(751), Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(752),
			})
			if !errors.Is(err, arena.ErrInvalidReconnectMutation) || changed || repository.writeCount() != 0 {
				t.Fatalf("Disconnect(same Presence timestamp) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
			}
		})

		t.Run("cancelled_opponent_interval", func(t *testing.T) {
			authority := task045Authority(now, false, true)
			task045AddCompletedRoots(&authority, 0, arena.ReconnectCycleLimit, now)
			authority.Reconnect[0].UpdatedAt = now
			repository := newTask045RepositoryFake(authority)
			_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), arena.DisconnectCommand{
				Scope: authority.Scope, CommandID: task045ID(760), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: task045ID(761), Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(762),
			})
			if !errors.Is(err, arena.ErrInvalidReconnectMutation) || changed || repository.writeCount() != 0 {
				t.Fatalf("Disconnect(same interval timestamp) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
			}
		})
	})

	t.Run("clock_revision_overflow_has_no_write", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		authority.GameClock.Revision = math.MaxInt64
		repository := newTask045RepositoryFake(authority)
		_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), arena.DisconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(770), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: task045ID(771), Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(772),
		})
		if !errors.Is(err, arena.ErrInvalidReconnectMutation) || !strings.Contains(err.Error(), "Game clock revision overflow") ||
			changed || repository.writeCount() != 0 || repository.commitCount() != 0 {
			t.Fatalf("Disconnect(clock overflow) error = %v, changed = %v, writes = %d, commits = %d", err, changed, repository.writeCount(), repository.commitCount())
		}
	})

	t.Run("terminal_settlement_rejects_current_revision_ids", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*arena.ReconnectAuthority, *arena.ReconnectSettlementIDs)
			detail string
		}{
			{name: "game_result_already_in_lineage", mutate: func(authority *arena.ReconnectAuthority, ids *arena.ReconnectSettlementIDs) {
				ids.GameResultRevisionID = authority.CurrentGameResultRevisionIDs[0]
			}, detail: "Game result revision identity is already current"},
			{name: "score_self_link", mutate: func(authority *arena.ReconnectAuthority, ids *arena.ReconnectSettlementIDs) {
				current := domain.ArenaSeriesScoreRevisionID(task045ID(780))
				authority.Series.CurrentScoreRevisionID = &current
				ids.ScoreRevisionID = current
			}, detail: "score revision identity self-links"},
			{name: "series_result_self_link", mutate: func(authority *arena.ReconnectAuthority, ids *arena.ReconnectSettlementIDs) {
				currentScore := domain.ArenaSeriesScoreRevisionID(task045ID(779))
				currentResult := domain.ArenaOfficialResultRevisionID(task045ID(780))
				authority.Series.State = domain.ArenaSeriesStateCancelled
				authority.Series.CurrentScoreRevisionID = &currentScore
				authority.Series.CurrentResultRevisionID = &currentResult
				ids.SeriesResultRevisionID = currentResult
			}, detail: "Series result revision identity self-links"},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, arena.ReconnectCycleLimit, now)
				ids := task045SettlementIDs(781)
				test.mutate(&authority, &ids)
				repository := newTask045RepositoryFake(authority)
				_, changed, err := arena.NewDisconnectUseCase(repository, fixedArenaClock{now: now}).Disconnect(t.Context(), arena.DisconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(790), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: task045ID(791), Deadline: now.Add(time.Minute), Settlement: ids,
				})
				if !errors.Is(err, arena.ErrInvalidReconnectMutation) || !strings.Contains(err.Error(), test.detail) ||
					changed || repository.writeCount() != 0 || repository.commitCount() != 0 {
					t.Fatalf("Disconnect(%s) error = %v, changed = %v, writes = %d, commits = %d", test.name, err, changed, repository.writeCount(), repository.commitCount())
				}
			})
		}
	})
}
