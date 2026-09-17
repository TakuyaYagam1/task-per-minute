package reconnect_test

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	"github.com/google/uuid"
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
			command := reconnectusecase.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(100 + test.used*10),
				ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(101 + test.used*10),
				Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(102 + test.used*10)}
			repository := newTask045RepositoryHarness(t, authority)
			useCase := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now))
			disconnected, changed, err := useCase.Disconnect(t.Context(), command)
			if err != nil || !changed || repository.writeCount() != 1 {
				t.Fatalf("Disconnect() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
			}
			presence := task045Presence(t, disconnected.ReconnectAuthority, command.ParticipantID)
			if presence.State != pause.PresenceStateDisconnected || presence.PresenceEpoch != authority.Presence[0].PresenceEpoch+1 ||
				presence.Revision != authority.Presence[0].Revision+1 || presence.DisconnectedAt == nil || !presence.DisconnectedAt.Equal(now) {
				t.Fatalf("disconnected Presence = %+v", presence)
			}
			counter := task045Counter(t, disconnected.ReconnectAuthority, command.ParticipantID)
			if counter.Used != test.wantNumber || counter.Limit != domain.ReconnectCycleLimit || counter.Revision != authority.Counters[0].Revision+1 {
				t.Fatalf("reconnect counter = %+v", counter)
			}
			interval := task045Interval(t, disconnected.ReconnectAuthority, command.IntervalID)
			if interval.Number != test.wantNumber || interval.ContinuationNumber != 0 || interval.ContinuedFromID != nil ||
				interval.PresenceEpoch != presence.PresenceEpoch || interval.State != pause.ReconnectStateOpen ||
				!interval.OpenedAt.Equal(now) || !interval.Deadline.Equal(command.Deadline) {
				t.Fatalf("root reconnect interval = %+v", interval)
			}
			if disconnected.ReconnectAuthority.Game.State != domain.GameStatePaused || disconnected.GameResultRevision != nil || disconnected.ScoreRevision != nil {
				t.Fatalf("allowed cycle terminalized Game: %+v", disconnected.ReconnectAuthority.Game)
			}
			if test.used == 1 && (disconnected.ReconnectAuthority.GameClock.OriginalDeadline != now.Add(90*time.Second) ||
				disconnected.ReconnectAuthority.GameClock.Remaining != 90*time.Second || disconnected.ReconnectAuthority.GameClock.ResumedDeadline != nil) {
				t.Fatalf("second freeze ignored current resumed deadline: %+v", disconnected.ReconnectAuthority.GameClock)
			}
			if got := task045Presence(t, disconnected.ReconnectAuthority, authority.Series.SecondParticipantID); !task045PresenceEqual(got, authority.Presence[1]) {
				t.Fatalf("opponent Presence changed: got %+v want %+v", got, authority.Presence[1])
			}
		})
	}

	t.Run("third_fresh_disconnect_is_immediate_autoloss", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		task045AddCompletedRoots(&authority, 0, domain.ReconnectCycleLimit, now)
		task045SetResumedClock(&authority, now)
		beforeClock := authority.GameClock
		command := reconnectusecase.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(130),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(131),
			Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(132)}
		repository := newTask045RepositoryHarness(t, authority)
		terminal, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), command)
		if err != nil || !changed || repository.writeCount() != 1 {
			t.Fatalf("Disconnect(third) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		if len(terminal.ReconnectAuthority.Reconnect) != domain.ReconnectCycleLimit || task045Counter(t, terminal.ReconnectAuthority, command.ParticipantID).Used != domain.ReconnectCycleLimit {
			t.Fatalf("third disconnect consumed another slot: intervals = %d, counter = %+v", len(terminal.ReconnectAuthority.Reconnect), task045Counter(t, terminal.ReconnectAuthority, command.ParticipantID))
		}
		if task045Presence(t, terminal.ReconnectAuthority, command.ParticipantID).State != pause.PresenceStateDisconnected ||
			terminal.ReconnectAuthority.Game.State != domain.GameStateCompleted || terminal.ReconnectAuthority.Game.ResultReason != domain.GameResultReasonOperatorForfeit ||
			terminal.ReconnectAuthority.Game.WinnerID == nil || *terminal.ReconnectAuthority.Game.WinnerID != authority.Series.SecondParticipantID ||
			terminal.ScoreRevision == nil || terminal.ScoreRevision.ScoreAfter.SecondParticipantWins != 1 {
			t.Fatalf("third disconnect terminal = %+v", terminal)
		}
		clock := terminal.ReconnectAuthority.GameClock
		if clock.ResumedAt != nil || clock.ResumedDeadline != nil || !clock.FrozenAt.Equal(now) ||
			!clock.OriginalDeadline.Equal(*beforeClock.ResumedDeadline) || clock.Remaining != beforeClock.ResumedDeadline.Sub(now) ||
			clock.Revision != beforeClock.Revision+1 || terminal.ReconnectAuthority.GameRevision != authority.GameRevision+1 ||
			terminal.ReconnectAuthority.SeriesRevision != authority.SeriesRevision+1 {
			t.Fatalf("terminal clock was not frozen exactly once: before = %+v, after = %+v", beforeClock, clock)
		}
	})

	t.Run("replacement_game_keeps_stable_disconnect_quota", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		task045AddCompletedRoots(&authority, 0, domain.ReconnectCycleLimit, now)
		prior := task045ReplaceCurrentGame(&authority, task045ID(825), now)
		if authority.Counters[0].Used != domain.ReconnectCycleLimit || authority.Game.AttemptNo != 2 ||
			len(authority.Series.Slots[0].Attempts) != 2 || authority.Series.Slots[0].Attempts[0].State != domain.GameStateVoid {
			t.Fatalf("replacement fixture is not durable: authority = %+v", authority)
		}
		for _, interval := range authority.Reconnect {
			if interval.ParticipantID == authority.Series.FirstParticipantID && interval.GameID != prior.ID {
				t.Fatalf("completed cycle was not retained on predecessor Game: %+v", interval)
			}
		}
		command := reconnectusecase.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(826),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(827),
			Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(828)}
		repository := newTask045RepositoryHarness(t, authority)
		terminal, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), command)
		if err != nil || !changed || repository.writeCount() != 1 || terminal.ReconnectAuthority.Game.WinnerID == nil ||
			*terminal.ReconnectAuthority.Game.WinnerID != authority.Series.SecondParticipantID ||
			task045Counter(t, terminal.ReconnectAuthority, command.ParticipantID).Used != domain.ReconnectCycleLimit ||
			len(terminal.ReconnectAuthority.Reconnect) != domain.ReconnectCycleLimit {
			t.Fatalf("Disconnect(replacement third) error = %v, changed = %v, writes = %d, record = %+v", err, changed, repository.writeCount(), terminal)
		}
	})

	t.Run("replacement_history_rejects_invalid_binding", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*reconnectusecase.ReconnectAuthority, domain.Game)
		}{
			{name: "foreign_game", mutate: func(authority *reconnectusecase.ReconnectAuthority, _ domain.Game) {
				authority.Reconnect[0].GameID = task045ID(840)
			}},
			{name: "future_presence_epoch", mutate: func(authority *reconnectusecase.ReconnectAuthority, _ domain.Game) {
				authority.Reconnect[0].PresenceEpoch = authority.Presence[0].PresenceEpoch + 1
			}},
			{name: "duplicate_root_presence_epoch", mutate: func(authority *reconnectusecase.ReconnectAuthority, _ domain.Game) {
				authority.Reconnect[1].PresenceEpoch = authority.Reconnect[0].PresenceEpoch
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, domain.ReconnectCycleLimit, now)
				prior := task045ReplaceCurrentGame(&authority, task045ID(841), now)
				test.mutate(&authority, prior)
				repository := newTask045RepositoryHarness(t, authority)
				_, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), reconnectusecase.DisconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(842), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: task045ID(843), Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(844),
				})
				if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || changed || repository.commitCount() != 0 {
					t.Fatalf("Disconnect(%s) error = %v, changed = %v, commits = %d", test.name, err, changed, repository.commitCount())
				}
			})
		}

		t.Run("open_interval_on_predecessor_game", func(t *testing.T) {
			authority := task045Authority(now, true, false)
			task045ReplaceCurrentGame(&authority, task045ID(848), now)
			authority.Game.State = domain.GameStatePaused
			authority.Series.Slots[0].Attempts[1] = authority.Game
			authority.GameClock.FrozenAt = now.Add(-5 * time.Second)
			authority.GameClock.Remaining = 40 * time.Second
			authority.GameClock.OriginalDeadline = authority.GameClock.FrozenAt.Add(authority.GameClock.Remaining)
			authority.GameClock.Revision = 2
			repository := newTask045RepositoryHarness(t, authority)
			_, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, now)).Reconnect(t.Context(), reconnectusecase.ReconnectCommand{
				Scope: authority.Scope, CommandID: task045ID(849), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(850),
			})
			if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || changed || repository.commitCount() != 0 {
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
				repository := newTask045RepositoryHarness(t, authority)
				_, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, at)).Disconnect(t.Context(), reconnectusecase.DisconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(850), ParticipantID: authority.Series.SecondParticipantID,
					IntervalID: task045ID(851), Deadline: at.Add(time.Minute), Settlement: task045SettlementIDs(852),
				})
				if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || !strings.Contains(err.Error(), "clock rollback") ||
					changed || repository.commitCount() != 0 || repository.snapshot().GameClock.Remaining != beforeRemaining {
					t.Fatalf("Disconnect(clock rollback %s) error = %v, changed = %v, commits = %d", offset, err, changed, repository.commitCount())
				}
			})
		}
	})

	t.Run("third_disconnect_while_both_absent_routes_replay_and_cancels_open_opponent", func(t *testing.T) {
		authority := task045Authority(now, false, true)
		task045AddCompletedRoots(&authority, 0, domain.ReconnectCycleLimit, now)
		opponentInterval := authority.Reconnect[0]
		command := reconnectusecase.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(135),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(136),
			Deadline: now.Add(30 * time.Second), Settlement: task045SettlementIDs(137)}
		repository := newTask045RepositoryHarness(t, authority)
		terminal, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), command)
		if err != nil || !changed || terminal.ReconnectAuthority.Game.State != domain.GameStateVoid ||
			terminal.ReconnectAuthority.Series.State != domain.SeriesStateReplayRequired || terminal.ReplayRoute == nil {
			t.Fatalf("Disconnect(third both absent) error = %v, changed = %v, record = %+v", err, changed, terminal)
		}
		closed := task045Interval(t, terminal.ReconnectAuthority, opponentInterval.ID)
		if closed.State != pause.ReconnectStateCancelled || closed.ClosedAt == nil || !closed.ClosedAt.Equal(now) ||
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
		source.State = pause.ReconnectStateCancelled
		closedAt := now.Add(-time.Second)
		source.ClosedAt = &closedAt
		suspendedBy := task045ID(145)
		source.SuspendedByPauseID = &suspendedBy
		source.UpdatedAt = closedAt
		source.Revision++
		continuedFrom := source.ID
		remaining := source.Deadline.Sub(closedAt)
		command := reconnectusecase.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(140),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(141), Deadline: now.Add(remaining),
			ContinuedFromID: &continuedFrom, Settlement: task045SettlementIDs(142)}
		repository := newTask045RepositoryHarness(t, authority)
		continued, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), command)
		if err != nil || !changed {
			t.Fatalf("Disconnect(continuation) error = %v, changed = %v", err, changed)
		}
		counter := task045Counter(t, continued.ReconnectAuthority, command.ParticipantID)
		interval := task045Interval(t, continued.ReconnectAuthority, command.IntervalID)
		if counter.Used != domain.ReconnectCycleLimit || counter.Revision != authority.Counters[0].Revision ||
			interval.Number != domain.ReconnectCycleLimit || interval.ContinuationNumber != 1 ||
			interval.ContinuedFromID == nil || *interval.ContinuedFromID != source.ID ||
			continued.DisconnectCommand.ContinuedFromID == nil || *continued.DisconnectCommand.ContinuedFromID != source.ID {
			t.Fatalf("continuation reset slot or retained mutable input: counter %+v, interval %+v, command %+v", counter, interval, continued.DisconnectCommand)
		}
		writes := repository.writeCount()
		if _, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), command); err != nil || changed || repository.writeCount() != writes {
			t.Fatalf("Disconnect(continuation replay) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		continuedFrom = task045ID(199)
		if continued.DisconnectCommand.ContinuedFromID == nil || *continued.DisconnectCommand.ContinuedFromID != source.ID {
			t.Fatal("mutating continuation input changed returned receipt")
		}
		reused := command
		if _, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), reused); !errors.Is(err, reconnectusecase.ErrCommandReuse) || changed {
			t.Fatalf("Disconnect(continuation command reuse) error = %v, changed = %v", err, changed)
		}
	})

	t.Run("continuation_rejects_expired_and_extension_without_write", func(t *testing.T) {
		for _, test := range []struct {
			name      string
			state     pause.ReconnectState
			extension time.Duration
			want      error
		}{
			{name: "expired", state: pause.ReconnectStateExpired, want: reconnectusecase.ErrUnavailable},
			{name: "extension", state: pause.ReconnectStateCancelled, extension: time.Nanosecond, want: reconnectusecase.ErrDeadline},
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
				if test.state == pause.ReconnectStateCancelled {
					suspendedBy := task045ID(160)
					source.SuspendedByPauseID = &suspendedBy
				}
				continuedFrom := source.ID
				command := reconnectusecase.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(161),
					ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(162),
					Deadline: now.Add(source.Deadline.Sub(closedAt)).Add(test.extension), ContinuedFromID: &continuedFrom,
					Settlement: task045SettlementIDs(163)}
				repository := newTask045RepositoryHarness(t, authority)
				_, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), command)
				if !errors.Is(err, test.want) || changed || repository.writeCount() != 0 {
					t.Fatalf("Disconnect(%s) error = %v, changed = %v, writes = %d", test.name, err, changed, repository.writeCount())
				}
			})
		}
	})

	t.Run("continuation_fork_serializes_one_child", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		source := &authority.Reconnect[0]
		source.State = pause.ReconnectStateCancelled
		closedAt := now.Add(-time.Second)
		source.ClosedAt = &closedAt
		source.UpdatedAt = closedAt
		suspendedBy := task045ID(170)
		source.SuspendedByPauseID = &suspendedBy
		source.Revision++
		continuedFrom := source.ID
		deadline := now.Add(source.Deadline.Sub(closedAt))
		commands := []reconnectusecase.DisconnectCommand{
			{Scope: authority.Scope, CommandID: task045ID(171), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: task045ID(172), Deadline: deadline, ContinuedFromID: &continuedFrom, Settlement: task045SettlementIDs(173)},
			{Scope: authority.Scope, CommandID: task045ID(181), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: task045ID(182), Deadline: deadline, ContinuedFromID: &continuedFrom, Settlement: task045SettlementIDs(183)},
		}
		repository := newTask045RepositoryHarness(t, authority)
		repository.barrier = newTask045Barrier()
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		results := make(chan error, 2)
		for _, command := range commands {
			go func() {
				_, _, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(ctx, command)
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
				case errors.Is(err, reconnectusecase.ErrUnavailable):
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
		command := reconnectusecase.DisconnectCommand{Scope: authority.Scope, CommandID: task045ID(150),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: task045ID(151), Deadline: now,
			Settlement: task045SettlementIDs(152)}
		repository := newTask045RepositoryHarness(t, authority)
		_, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), command)
		if !errors.Is(err, reconnectusecase.ErrDeadline) || changed || repository.writeCount() != 0 {
			t.Fatalf("Disconnect(expired window) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("fresh_disconnect_rejects_duplicate_malformed_and_overflow_without_write", func(t *testing.T) {
		baseAuthority := task045Authority(now, false, false)
		task045AddCompletedRoots(&baseAuthority, 0, 1, now)
		oldIntervalID := baseAuthority.Reconnect[0].ID
		for _, test := range []struct {
			name       string
			mutate     func(*reconnectusecase.ReconnectAuthority)
			intervalID uuid.UUID
			want       error
			detail     string
		}{
			{name: "duplicate interval", intervalID: oldIntervalID, want: reconnectusecase.ErrUnavailable},
			{name: "malformed counter", intervalID: task045ID(196), mutate: func(value *reconnectusecase.ReconnectAuthority) { value.Counters[0].Limit = 3 }, want: reconnectusecase.ErrInvalidMutation},
			{name: "authority overflow", intervalID: task045ID(197), mutate: func(value *reconnectusecase.ReconnectAuthority) { value.Revision = math.MaxInt64 }, want: reconnectusecase.ErrInvalidMutation, detail: "revision overflow"},
			{name: "counter overflow", intervalID: task045ID(199), mutate: func(value *reconnectusecase.ReconnectAuthority) { value.Counters[0].Revision = math.MaxInt64 }, want: reconnectusecase.ErrInvalidMutation, detail: "counter revision overflow"},
			{name: "ordinal overflow", intervalID: task045ID(198), mutate: func(value *reconnectusecase.ReconnectAuthority) {
				task045AddCompletedRoots(value, 0, domain.ReconnectCycleLimit, now)
				value.CurrentOrdinal = int(^uint(0) >> 1)
			}, want: reconnectusecase.ErrInvalidMutation, detail: "terminal lineage overflow"},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := cloneTask045Authority(baseAuthority)
				if test.mutate != nil {
					test.mutate(&authority)
				}
				repository := newTask045RepositoryHarness(t, authority)
				_, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), reconnectusecase.DisconnectCommand{
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
			repository := newTask045RepositoryHarness(t, authority)
			_, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), reconnectusecase.DisconnectCommand{
				Scope: authority.Scope, CommandID: task045ID(750), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: task045ID(751), Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(752),
			})
			if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || changed || repository.writeCount() != 0 {
				t.Fatalf("Disconnect(same Presence timestamp) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
			}
		})

		t.Run("cancelled_opponent_interval", func(t *testing.T) {
			authority := task045Authority(now, false, true)
			task045AddCompletedRoots(&authority, 0, domain.ReconnectCycleLimit, now)
			authority.Reconnect[0].UpdatedAt = now
			repository := newTask045RepositoryHarness(t, authority)
			_, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), reconnectusecase.DisconnectCommand{
				Scope: authority.Scope, CommandID: task045ID(760), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: task045ID(761), Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(762),
			})
			if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || changed || repository.writeCount() != 0 {
				t.Fatalf("Disconnect(same interval timestamp) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
			}
		})
	})

	t.Run("clock_revision_overflow_has_no_write", func(t *testing.T) {
		authority := task045Authority(now, false, false)
		authority.GameClock.Revision = math.MaxInt64
		repository := newTask045RepositoryHarness(t, authority)
		_, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), reconnectusecase.DisconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(770), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: task045ID(771), Deadline: now.Add(time.Minute), Settlement: task045SettlementIDs(772),
		})
		if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || !strings.Contains(err.Error(), "Game clock revision overflow") ||
			changed || repository.writeCount() != 0 || repository.commitCount() != 0 {
			t.Fatalf("Disconnect(clock overflow) error = %v, changed = %v, writes = %d, commits = %d", err, changed, repository.writeCount(), repository.commitCount())
		}
	})

	t.Run("terminal_settlement_rejects_current_revision_ids", func(t *testing.T) {
		for _, test := range []struct {
			name   string
			mutate func(*reconnectusecase.ReconnectAuthority, *reconnectusecase.SettlementIDs)
			detail string
		}{
			{name: "game_result_already_in_lineage", mutate: func(authority *reconnectusecase.ReconnectAuthority, ids *reconnectusecase.SettlementIDs) {
				ids.GameResultRevisionID = authority.CurrentGameResultRevisionIDs[0]
			}, detail: "Game result revision identity is already current"},
			{name: "score_self_link", mutate: func(authority *reconnectusecase.ReconnectAuthority, ids *reconnectusecase.SettlementIDs) {
				current := domain.SeriesScoreRevisionID(task045ID(780))
				authority.Series.CurrentScoreRevisionID = &current
				ids.ScoreRevisionID = current
			}, detail: "score revision identity self-links"},
			{name: "series_result_self_link", mutate: func(authority *reconnectusecase.ReconnectAuthority, ids *reconnectusecase.SettlementIDs) {
				currentScore := domain.SeriesScoreRevisionID(task045ID(779))
				currentResult := domain.OfficialResultRevisionID(task045ID(780))
				authority.Series.State = domain.SeriesStateCancelled
				authority.Series.CurrentScoreRevisionID = &currentScore
				authority.Series.CurrentResultRevisionID = &currentResult
				ids.SeriesResultRevisionID = currentResult
			}, detail: "Series result revision identity self-links"},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := task045Authority(now, false, false)
				task045AddCompletedRoots(&authority, 0, domain.ReconnectCycleLimit, now)
				ids := task045SettlementIDs(781)
				test.mutate(&authority, &ids)
				repository := newTask045RepositoryHarness(t, authority)
				_, changed, err := reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, now)).Disconnect(t.Context(), reconnectusecase.DisconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(790), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: task045ID(791), Deadline: now.Add(time.Minute), Settlement: ids,
				})
				if !errors.Is(err, reconnectusecase.ErrInvalidMutation) || !strings.Contains(err.Error(), test.detail) ||
					changed || repository.writeCount() != 0 || repository.commitCount() != 0 {
					t.Fatalf("Disconnect(%s) error = %v, changed = %v, writes = %d, commits = %d", test.name, err, changed, repository.writeCount(), repository.commitCount())
				}
			})
		}
	})
}
