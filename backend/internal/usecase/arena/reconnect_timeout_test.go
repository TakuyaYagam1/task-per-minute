package arena_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	arena "github.com/TakuyaYagam1/task-per-minute/internal/usecase/arena"
)

func TestReconnectTimeoutAutoloss(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.September, 1, 13, 0, 0, 0, time.UTC)

	t.Run("one_absent_awards_connected_opponent", func(t *testing.T) {
		authority := task045Authority(base, true, false)
		deadline := authority.Reconnect[0].Deadline
		command := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(40),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(41)}
		repository := newTask045RepositoryFake(authority)
		useCase := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: deadline})

		terminal, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || !changed || repository.writeCount() != 1 {
			t.Fatalf("Expire() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		winner := authority.Series.SecondParticipantID
		if terminal.Authority.Game.State != domain.ArenaGameStateCompleted ||
			terminal.Authority.Game.ResultReason != domain.ArenaGameResultReasonOperatorForfeit ||
			terminal.Authority.Game.WinnerID == nil || *terminal.Authority.Game.WinnerID != winner ||
			terminal.Authority.Game.ResultRevisionID == nil || *terminal.Authority.Game.ResultRevisionID != command.Settlement.GameResultRevisionID {
			t.Fatalf("terminal Game = %+v", terminal.Authority.Game)
		}
		if terminal.GameResultRevision == nil || terminal.GameResultRevision.ID != command.Settlement.GameResultRevisionID ||
			terminal.GameResultRevision.WinnerID != winner || terminal.GameResultRevision.Reason != domain.ArenaGameResultReasonOperatorForfeit ||
			!terminal.GameResultRevision.RecordedAt.Equal(deadline) {
			t.Fatalf("official Game result = %+v", terminal.GameResultRevision)
		}
		series := terminal.Authority.Series
		if series.State != domain.ArenaSeriesStateCompleted || series.Score != (domain.ArenaSeriesScore{SecondParticipantWins: 1}) ||
			series.WinnerID == nil || *series.WinnerID != winner || series.CurrentScoreRevisionID == nil ||
			*series.CurrentScoreRevisionID != command.Settlement.ScoreRevisionID || series.CurrentResultRevisionID == nil ||
			*series.CurrentResultRevisionID != command.Settlement.SeriesResultRevisionID {
			t.Fatalf("settled Series = %+v", series)
		}
		if terminal.GameResultRevision.Ordinal != authority.CurrentOrdinal+1 || terminal.ScoreRevision == nil ||
			terminal.ScoreRevision.Ordinal != authority.CurrentOrdinal+2 ||
			terminal.ScoreRevision.ScoreBefore != (domain.ArenaSeriesScore{}) ||
			terminal.ScoreRevision.ScoreAfter != (domain.ArenaSeriesScore{SecondParticipantWins: 1}) ||
			len(terminal.ScoreRevision.GameResultRevisionIDs) != 2 || terminal.ScoreRevision.GameResultRevisionIDs[0] != authority.CurrentGameResultRevisionIDs[0] ||
			terminal.ScoreRevision.GameResultRevisionIDs[1] != command.Settlement.GameResultRevisionID ||
			terminal.SeriesResultRevision == nil || terminal.SeriesResultRevision.ID != command.Settlement.SeriesResultRevisionID ||
			terminal.SeriesResultRevision.ID == terminal.GameResultRevision.ID ||
			terminal.SeriesResultRevision.Ordinal != terminal.ScoreRevision.Ordinal+1 {
			t.Fatalf("Series score revision = %+v", terminal.ScoreRevision)
		}
		if terminal.Evidence == nil || terminal.Evidence.AuditEventID != command.Settlement.AuditEventID ||
			terminal.Evidence.OutboxEventID != command.Settlement.OutboxEventID ||
			terminal.Evidence.ProjectionRevisionID != command.Settlement.ProjectionRevisionID ||
			terminal.Evidence.SourceProjectionRevision != authority.CurrentProjectionRevision ||
			terminal.Evidence.ProjectionRevision != authority.CurrentProjectionRevision+1 || terminal.Authority.Current == nil {
			t.Fatalf("settlement evidence = %+v", terminal.Evidence)
		}
		if got := task045Interval(t, terminal.Authority, command.IntervalID); got.State != arena.ReconnectStateExpired ||
			got.ClosedAt == nil || !got.ClosedAt.Equal(deadline) {
			t.Fatalf("expired interval = %+v", got)
		}
		if got := task045Presence(t, terminal.Authority, command.ParticipantID); !task045PresenceEqual(got, authority.Presence[0]) {
			t.Fatalf("timeout changed disconnected Presence: got %+v want %+v", got, authority.Presence[0])
		}

		writes := repository.writeCount()
		replayed, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || changed || repository.writeCount() != writes {
			t.Fatalf("Expire(replay) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		replayed.ScoreRevision.GameResultRevisionIDs[0] = domain.ArenaOfficialResultRevisionID(task045ID(99))
		replayed.Authority.Series.Score.SecondParticipantWins = 0
		stored := repository.snapshot()
		if stored.Series.Score.SecondParticipantWins != 1 {
			t.Fatal("mutating timeout receipt changed stored Series")
		}
		terminalSnapshot := repository.snapshot()
		terminalWrites := repository.writeCount()
		terminalCommits := repository.commitCount()
		rollbackReplay, changed, err := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: base.Add(-2 * time.Minute)}).Expire(t.Context(), arena.ReconnectTimeoutCommand{
			Scope: authority.Scope, CommandID: task045ID(1000), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(1001),
		})
		if err != nil || changed || rollbackReplay == nil {
			t.Fatalf("Expire(new command before terminal history) error = %v, changed = %v, record nil = %v", err, changed, rollbackReplay == nil)
		}
		if !reflect.DeepEqual(rollbackReplay.GameResultRevision, terminal.GameResultRevision) ||
			!reflect.DeepEqual(rollbackReplay.VoidGameResultRevision, terminal.VoidGameResultRevision) ||
			!reflect.DeepEqual(rollbackReplay.ScoreRevision, terminal.ScoreRevision) ||
			!reflect.DeepEqual(rollbackReplay.SeriesResultRevision, terminal.SeriesResultRevision) ||
			!reflect.DeepEqual(rollbackReplay.ReplayRoute, terminal.ReplayRoute) ||
			!reflect.DeepEqual(rollbackReplay.Evidence, terminal.Evidence) ||
			!reflect.DeepEqual(rollbackReplay.Authority.Current, terminalSnapshot.Current) {
			t.Fatalf("terminal rollback replay changed outcome: got %+v, want %+v", rollbackReplay.Authority.Current, terminalSnapshot.Current)
		}
		if repository.writeCount() != terminalWrites || repository.commitCount() != terminalCommits ||
			!reflect.DeepEqual(repository.snapshot(), terminalSnapshot) {
			t.Fatalf("terminal rollback replay mutated storage: writes = %d, commits = %d", repository.writeCount(), repository.commitCount())
		}
		postTerminal := []struct {
			name string
			run  func() (*arena.ReconnectRecord, bool, error)
		}{
			{name: "reconnect", run: func() (*arena.ReconnectRecord, bool, error) {
				return arena.NewReconnectUseCase(repository, fixedArenaClock{now: deadline.Add(time.Second)}).Reconnect(t.Context(), arena.ReconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(570), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(571),
				})
			}},
			{name: "disconnect", run: func() (*arena.ReconnectRecord, bool, error) {
				return arena.NewDisconnectUseCase(repository, fixedArenaClock{now: deadline.Add(time.Second)}).Disconnect(t.Context(), arena.DisconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(580), ParticipantID: authority.Series.SecondParticipantID,
					IntervalID: task045ID(581), Deadline: deadline.Add(time.Minute), Settlement: task045SettlementIDs(582),
				})
			}},
			{name: "timeout", run: func() (*arena.ReconnectRecord, bool, error) {
				return arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: deadline.Add(time.Second)}).Expire(t.Context(), arena.ReconnectTimeoutCommand{
					Scope: authority.Scope, CommandID: task045ID(590), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(591),
				})
			}},
		}
		for _, test := range postTerminal {
			replayed, changed, err := test.run()
			if err != nil || changed || replayed == nil || replayed.GameResultRevision == nil ||
				replayed.GameResultRevision.ID != command.Settlement.GameResultRevisionID || replayed.ScoreRevision == nil ||
				replayed.ScoreRevision.ID != command.Settlement.ScoreRevisionID {
				t.Fatalf("%s after terminal error = %v, changed = %v, record = %+v", test.name, err, changed, replayed)
			}
		}
		if repository.writeCount() != terminalWrites || !reflect.DeepEqual(repository.snapshot(), terminalSnapshot) {
			t.Fatal("post-terminal command mutated durable authority")
		}
	})

	t.Run("both_absent_routes_one_void_replay", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		deadline := authority.Reconnect[0].Deadline
		first := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(50),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(51)}
		repository := newTask045RepositoryFake(authority)
		useCase := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: deadline})
		terminal, changed, err := useCase.Expire(t.Context(), first)
		if err != nil || !changed || repository.writeCount() != 1 {
			t.Fatalf("Expire(both absent) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		if terminal.Authority.Game.State != domain.ArenaGameStateVoid ||
			terminal.Authority.Game.ResultReason != domain.ArenaGameResultReasonDisconnect || terminal.Authority.Game.WinnerID != nil ||
			terminal.GameResultRevision != nil || terminal.VoidGameResultRevision == nil ||
			terminal.VoidGameResultRevision.ID != first.Settlement.GameResultRevisionID ||
			terminal.VoidGameResultRevision.Reason != domain.ArenaGameResultReasonDisconnect {
			t.Fatalf("both-absent Game result = Game %+v, winner revision %+v, void revision %+v", terminal.Authority.Game, terminal.GameResultRevision, terminal.VoidGameResultRevision)
		}
		if terminal.Authority.Series.State != domain.ArenaSeriesStateReplayRequired ||
			terminal.Authority.Series.Score != (domain.ArenaSeriesScore{}) || terminal.Authority.Series.WinnerID != nil ||
			terminal.ScoreRevision == nil || terminal.ScoreRevision.ScoreBefore != terminal.ScoreRevision.ScoreAfter ||
			terminal.ReplayRoute == nil || terminal.ReplayRoute.ID != first.Settlement.ReplayRouteID ||
			terminal.ReplayRoute.Category != authority.Series.Slots[0].Category || terminal.Authority.Current == nil {
			t.Fatalf("both-absent Series route = Series %+v, score %+v", terminal.Authority.Series, terminal.ScoreRevision)
		}
		for _, interval := range terminal.Authority.Reconnect {
			if interval.State != arena.ReconnectStateExpired || interval.ClosedAt == nil || !interval.ClosedAt.Equal(deadline) {
				t.Fatalf("both-absent interval = %+v", interval)
			}
		}
		second := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(60),
			ParticipantID: authority.Series.SecondParticipantID, IntervalID: authority.Reconnect[1].ID,
			Settlement: task045SettlementIDs(61)}
		_, changed, err = useCase.Expire(t.Context(), second)
		if err != nil || changed || repository.writeCount() != 1 {
			t.Fatalf("Expire(second deadline) error = %v, changed = %v, writes = %d, want one terminal write", err, changed, repository.writeCount())
		}
	})

	t.Run("staggered_deadlines_defer_until_second_outcome", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		firstDeadline := authority.Reconnect[0].Deadline
		authority.Reconnect[1].Deadline = firstDeadline.Add(20 * time.Second)
		first := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(80),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(81)}
		repository := newTask045RepositoryFake(authority)
		deferred, changed, err := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: firstDeadline}).Expire(t.Context(), first)
		if err != nil || !changed || repository.writeCount() != 1 {
			t.Fatalf("Expire(first staggered) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		if deferred.Authority.Game.State != domain.ArenaGameStatePaused || deferred.Authority.Current != nil ||
			task045Interval(t, deferred.Authority, authority.Reconnect[0].ID).State != arena.ReconnectStateExpired ||
			task045Interval(t, deferred.Authority, authority.Reconnect[1].ID).State != arena.ReconnectStateOpen ||
			deferred.ScoreRevision != nil {
			t.Fatalf("first staggered deadline terminalized early: %+v", deferred)
		}

		reconnectAt := firstDeadline.Add(10 * time.Second)
		reconnected, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: reconnectAt}).Reconnect(t.Context(), arena.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(90), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(91),
		})
		if err != nil || !changed || repository.writeCount() != 2 || reconnected.Authority.Current == nil ||
			reconnected.Authority.Game.State != domain.ArenaGameStateCompleted || reconnected.Authority.Game.WinnerID == nil ||
			*reconnected.Authority.Game.WinnerID != authority.Series.SecondParticipantID {
			t.Fatalf("Reconnect(after opponent expiry) error = %v, changed = %v, record = %+v", err, changed, reconnected)
		}
	})

	t.Run("deferred_expiry_rejects_clock_rollback", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		firstDeadline := authority.Reconnect[0].Deadline
		authority.Reconnect[1].Deadline = firstDeadline.Add(20 * time.Second)
		repository := newTask045RepositoryFake(authority)
		first := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(860),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(861)}
		if _, changed, err := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: firstDeadline}).Expire(t.Context(), first); err != nil || !changed {
			t.Fatalf("Expire(seed deferred) error = %v, changed = %v", err, changed)
		}
		rollbackAt := firstDeadline.Add(-5 * time.Second)
		_, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: rollbackAt}).Reconnect(t.Context(), arena.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(870), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(871),
		})
		if !errors.Is(err, arena.ErrInvalidReconnectMutation) || !strings.Contains(err.Error(), "clock rollback") ||
			changed || repository.writeCount() != 1 || repository.commitCount() != 1 {
			t.Fatalf("Reconnect(deferred rollback) error = %v, changed = %v, writes = %d, commits = %d", err, changed, repository.writeCount(), repository.commitCount())
		}
		stored := repository.snapshot()
		if stored.Current != nil || task045Interval(t, stored, authority.Reconnect[1].ID).State != arena.ReconnectStateOpen {
			t.Fatalf("deferred rollback mutated authority: %+v", stored)
		}
	})

	t.Run("staggered_second_timeout_routes_replay", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		firstDeadline := authority.Reconnect[0].Deadline
		secondDeadline := firstDeadline.Add(20 * time.Second)
		authority.Reconnect[1].Deadline = secondDeadline
		repository := newTask045RepositoryFake(authority)
		first := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(300),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(301)}
		if _, changed, err := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: firstDeadline}).Expire(t.Context(), first); err != nil || !changed {
			t.Fatalf("Expire(first staggered) error = %v, changed = %v", err, changed)
		}
		second := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(310),
			ParticipantID: authority.Series.SecondParticipantID, IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(311)}
		terminal, changed, err := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: secondDeadline}).Expire(t.Context(), second)
		if err != nil || !changed || repository.writeCount() != 2 || terminal.Authority.Current == nil ||
			terminal.Authority.Game.State != domain.ArenaGameStateVoid || terminal.Authority.Series.State != domain.ArenaSeriesStateReplayRequired {
			t.Fatalf("Expire(second staggered) error = %v, changed = %v, record = %+v", err, changed, terminal)
		}
	})

	t.Run("equal_deadline_race_commits_one_replay", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		deadline := authority.Reconnect[0].Deadline
		repository := newTask045RepositoryFake(authority)
		repository.barrier = newTask045Barrier()
		commands := []arena.ReconnectTimeoutCommand{
			{Scope: authority.Scope, CommandID: task045ID(500), ParticipantID: authority.Series.FirstParticipantID,
				IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(501)},
			{Scope: authority.Scope, CommandID: task045ID(510), ParticipantID: authority.Series.SecondParticipantID,
				IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(511)},
		}
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		type result struct {
			record  *arena.ReconnectRecord
			changed bool
			err     error
		}
		results := make(chan result, 2)
		for _, command := range commands {
			go func() {
				record, changed, err := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: deadline}).Expire(ctx, command)
				results <- result{record: record, changed: changed, err: err}
			}()
		}
		if err := repository.barrier.await(ctx); err != nil {
			t.Fatalf("await equal deadlines: %v", err)
		}
		changedCount := 0
		var resultID domain.ArenaOfficialResultRevisionID
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

	t.Run("staggered_timeout_then_reconnect_awards_reconnector", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		at := authority.Reconnect[0].Deadline
		authority.Reconnect[1].Deadline = at.Add(20 * time.Second)
		repository := newTask045RepositoryFake(authority)
		if _, changed, err := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: at}).Expire(t.Context(), arena.ReconnectTimeoutCommand{
			Scope: authority.Scope, CommandID: task045ID(520), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(521),
		}); err != nil || !changed {
			t.Fatalf("Expire(staggered first) error = %v, changed = %v", err, changed)
		}
		reconnectAt := at.Add(time.Nanosecond)
		if _, changed, err := arena.NewReconnectUseCase(repository, fixedArenaClock{now: reconnectAt}).Reconnect(t.Context(), arena.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(530), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(531),
		}); err != nil || !changed {
			t.Fatalf("Reconnect(staggered second) error = %v, changed = %v", err, changed)
		}
		stored := repository.snapshot()
		if repository.writeCount() != 2 || repository.conflictCount() != 0 || stored.Current == nil ||
			stored.Game.WinnerID == nil || *stored.Game.WinnerID != authority.Series.SecondParticipantID ||
			stored.Game.ResultReason != domain.ArenaGameResultReasonOperatorForfeit {
			t.Fatalf("staggered race did not award reconnector: writes = %d, conflicts = %d, authority = %+v", repository.writeCount(), repository.conflictCount(), stored)
		}
	})

	t.Run("bo3_one_one_keeps_cumulative_lineage", func(t *testing.T) {
		authority := task045Authority(base, false, true)
		oldResultID := authority.CurrentGameResultRevisionIDs[0]
		oldScoreID := domain.ArenaSeriesScoreRevisionID(task045ID(540))
		oldWinner := authority.Series.SecondParticipantID
		oldGame := domain.ArenaGame{ID: task045ID(541), SlotID: task045ID(542), AttemptNo: 1,
			State: domain.ArenaGameStateCompleted, ResultReason: domain.ArenaGameResultReasonOperatorForfeit,
			WinnerID: &oldWinner, ResultRevisionID: &oldResultID}
		currentSlot := authority.Series.Slots[0]
		currentSlot.Position = 2
		currentSlot.ScoreBefore = domain.ArenaSeriesScore{SecondParticipantWins: 1}
		authority.Series.Format = domain.ArenaSeriesFormatBO3
		authority.Series.Score = domain.ArenaSeriesScore{SecondParticipantWins: 1}
		authority.Series.CurrentScoreRevisionID = &oldScoreID
		authority.Series.Slots = []domain.ArenaGameSlot{
			{ID: oldGame.SlotID, SeriesID: authority.Series.ID, Position: 1, Category: currentSlot.Category,
				ScoreBefore: domain.ArenaSeriesScore{}, Attempts: []domain.ArenaGame{oldGame}},
			currentSlot,
		}
		authority.CurrentOrdinal = 3
		command := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(550),
			ParticipantID: authority.Series.SecondParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(551)}
		repository := newTask045RepositoryFake(authority)
		terminal, changed, err := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: authority.Reconnect[0].Deadline}).Expire(t.Context(), command)
		if err != nil || !changed || terminal.Authority.Series.State != domain.ArenaSeriesStateActive ||
			terminal.Authority.Series.Score != (domain.ArenaSeriesScore{FirstParticipantWins: 1, SecondParticipantWins: 1}) ||
			terminal.Authority.Game.WinnerID == nil || *terminal.Authority.Game.WinnerID != authority.Series.FirstParticipantID ||
			terminal.GameResultRevision.Ordinal != 4 || terminal.ScoreRevision.Ordinal != 5 || terminal.SeriesResultRevision != nil ||
			len(terminal.ScoreRevision.GameResultRevisionIDs) != 2 || terminal.ScoreRevision.GameResultRevisionIDs[0] != oldResultID ||
			terminal.ScoreRevision.GameResultRevisionIDs[1] != command.Settlement.GameResultRevisionID || terminal.Authority.CurrentOrdinal != 5 ||
			!task045GameEqual(terminal.Authority.Series.Slots[1].Attempts[0], terminal.Authority.Game) {
			t.Fatalf("BO3 cumulative lineage error = %v, changed = %v, record = %+v", err, changed, terminal)
		}
	})

	t.Run("before_deadline_has_no_write", func(t *testing.T) {
		authority := task045Authority(base, true, false)
		command := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(70),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(71)}
		repository := newTask045RepositoryFake(authority)
		useCase := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: authority.Reconnect[0].Deadline.Add(-time.Nanosecond)})
		_, changed, err := useCase.Expire(t.Context(), command)
		if !errors.Is(err, arena.ErrReconnectDeadline) || changed || repository.writeCount() != 0 {
			t.Fatalf("Expire(before deadline) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("expiry_timestamp_must_advance", func(t *testing.T) {
		authority := task045Authority(base, true, false)
		deadline := authority.Reconnect[0].Deadline
		authority.Reconnect[0].UpdatedAt = deadline
		repository := newTask045RepositoryFake(authority)
		_, changed, err := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: deadline}).Expire(t.Context(), arena.ReconnectTimeoutCommand{
			Scope: authority.Scope, CommandID: task045ID(800), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(801),
		})
		if !errors.Is(err, arena.ErrInvalidReconnectMutation) || changed || repository.writeCount() != 0 {
			t.Fatalf("Expire(same interval timestamp) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("repository_owned_terminal_receipt_is_deeply_isolated", func(t *testing.T) {
		authority := task045Authority(base, true, false)
		deadline := authority.Reconnect[0].Deadline
		command := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(810),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(811)}
		repository := newTask045RepositoryFake(authority)
		repository.shareOwned = true
		useCase := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: deadline})
		first, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || !changed || first.ScoreRevision == nil || first.Authority.Current == nil {
			t.Fatalf("Expire(repository owned) error = %v, changed = %v, record = %+v", err, changed, first)
		}
		wantID := first.ScoreRevision.GameResultRevisionIDs[0]
		first.ScoreRevision.GameResultRevisionIDs[0] = domain.ArenaOfficialResultRevisionID(task045ID(820))
		first.Authority.Current.ScoreRevision.GameResultRevisionIDs[0] = domain.ArenaOfficialResultRevisionID(task045ID(821))
		first.Authority.Series.Score = domain.ArenaSeriesScore{}
		replayed, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || changed || replayed.ScoreRevision.GameResultRevisionIDs[0] != wantID ||
			replayed.Authority.Current.ScoreRevision.GameResultRevisionIDs[0] != wantID ||
			replayed.Authority.Series.Score.SecondParticipantWins != 1 {
			t.Fatalf("first returned mutation leaked into stored receipt: error = %v, changed = %v, replay = %+v", err, changed, replayed)
		}
		replayed.ScoreRevision.GameResultRevisionIDs[0] = domain.ArenaOfficialResultRevisionID(task045ID(822))
		replayed.Authority.Current.ScoreRevision.GameResultRevisionIDs[0] = domain.ArenaOfficialResultRevisionID(task045ID(823))
		again, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || changed || again.ScoreRevision.GameResultRevisionIDs[0] != wantID ||
			again.Authority.Current.ScoreRevision.GameResultRevisionIDs[0] != wantID || repository.writeCount() != 1 {
			t.Fatalf("replay returned mutation leaked into stored receipt: error = %v, changed = %v, replay = %+v", err, changed, again)
		}
	})

	t.Run("terminal_receipt_rejects_invalid_previous_links", func(t *testing.T) {
		for index, test := range []struct {
			name   string
			mutate func(*arena.ReconnectRecord)
		}{
			{name: "score_previous_self", mutate: func(record *arena.ReconnectRecord) {
				previous := record.ScoreRevision.ID
				record.ScoreRevision.PreviousRevisionID = &previous
				currentPrevious := record.Authority.Current.ScoreRevision.ID
				record.Authority.Current.ScoreRevision.PreviousRevisionID = &currentPrevious
			}},
			{name: "score_previous_zero", mutate: func(record *arena.ReconnectRecord) {
				previous := domain.ArenaSeriesScoreRevisionID{}
				record.ScoreRevision.PreviousRevisionID = &previous
				currentPrevious := domain.ArenaSeriesScoreRevisionID{}
				record.Authority.Current.ScoreRevision.PreviousRevisionID = &currentPrevious
			}},
			{name: "series_previous_self", mutate: func(record *arena.ReconnectRecord) {
				previous := record.SeriesResultRevision.ID
				record.SeriesResultRevision.PreviousRevisionID = &previous
				currentPrevious := record.Authority.Current.SeriesResultRevision.ID
				record.Authority.Current.SeriesResultRevision.PreviousRevisionID = &currentPrevious
			}},
			{name: "series_previous_zero", mutate: func(record *arena.ReconnectRecord) {
				previous := domain.ArenaOfficialResultRevisionID{}
				record.SeriesResultRevision.PreviousRevisionID = &previous
				currentPrevious := domain.ArenaOfficialResultRevisionID{}
				record.Authority.Current.SeriesResultRevision.PreviousRevisionID = &currentPrevious
			}},
			{name: "series_state_not_completed", mutate: func(record *arena.ReconnectRecord) {
				record.SeriesResultRevision.State = domain.ArenaSeriesStateCancelled
				record.Authority.Current.SeriesResultRevision.State = domain.ArenaSeriesStateCancelled
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := task045Authority(base, true, false)
				deadline := authority.Reconnect[0].Deadline
				command := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(900 + index*10),
					ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
					Settlement: task045SettlementIDs(901 + index*10)}
				repository := newTask045RepositoryFake(authority)
				useCase := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: deadline})
				if _, changed, err := useCase.Expire(t.Context(), command); err != nil || !changed {
					t.Fatalf("Expire(seed %s) error = %v, changed = %v", test.name, err, changed)
				}
				repository.mutateReceipt(command.CommandID, test.mutate)
				_, changed, err := useCase.Expire(t.Context(), command)
				if !errors.Is(err, domain.ErrInternal) || changed || repository.writeCount() != 1 || repository.commitCount() != 1 {
					t.Fatalf("Expire(corrupt %s) error = %v, changed = %v, writes = %d, commits = %d", test.name, err, changed, repository.writeCount(), repository.commitCount())
				}
			})
		}
	})

	t.Run("void_receipt_requires_terminal_result_timestamp", func(t *testing.T) {
		for index, test := range []struct {
			name string
			at   func(time.Time) time.Time
		}{
			{name: "zero", at: func(time.Time) time.Time { return time.Time{} }},
			{name: "different", at: func(at time.Time) time.Time { return at.Add(-time.Nanosecond) }},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := task045Authority(base, true, true)
				deadline := authority.Reconnect[0].Deadline
				command := arena.ReconnectTimeoutCommand{Scope: authority.Scope, CommandID: task045ID(1000 + index*10),
					ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
					Settlement: task045SettlementIDs(1001 + index*10)}
				repository := newTask045RepositoryFake(authority)
				useCase := arena.NewReconnectTimeoutUseCase(repository, fixedArenaClock{now: deadline})
				terminal, changed, err := useCase.Expire(t.Context(), command)
				if err != nil || !changed || terminal.VoidGameResultRevision == nil || terminal.Authority.Current == nil {
					t.Fatalf("Expire(seed void %s) error = %v, changed = %v, record = %+v", test.name, err, changed, terminal)
				}
				repository.mutateReceipt(command.CommandID, func(record *arena.ReconnectRecord) {
					corruptAt := test.at(record.Authority.Current.TerminalizedAt)
					record.VoidGameResultRevision.RecordedAt = corruptAt
					record.Authority.Current.VoidGameResultRevision.RecordedAt = corruptAt
				})
				_, changed, err = useCase.Expire(t.Context(), command)
				if !errors.Is(err, domain.ErrInternal) || changed || repository.writeCount() != 1 || repository.commitCount() != 1 {
					t.Fatalf("Expire(corrupt void %s) error = %v, changed = %v, writes = %d, commits = %d", test.name, err, changed, repository.writeCount(), repository.commitCount())
				}
			})
		}
	})
}
