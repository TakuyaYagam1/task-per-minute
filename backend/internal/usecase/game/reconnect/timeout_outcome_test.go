package reconnect_test

import (
	"reflect"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/TakuyaYagam1/task-per-minute/internal/domain/pause"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

func testReconnectTimeoutOutcomes(t *testing.T, base time.Time) {
	t.Helper()

	t.Run("one_absent_awards_connected_opponent", func(t *testing.T) {
		authority := task045Authority(base, true, false)
		deadline := authority.Reconnect[0].Deadline
		command := reconnectusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(40),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(41)}
		repository := newTask045RepositoryHarness(t, authority)
		useCase := reconnectusecase.NewTimeoutUseCase(repository, newReconnectClock(t, deadline))

		terminal, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || !changed || repository.writeCount() != 1 {
			t.Fatalf("Expire() error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		winner := authority.Series.SecondParticipantID
		if terminal.ReconnectAuthority.Game.State != domain.GameStateCompleted ||
			terminal.ReconnectAuthority.Game.ResultReason != domain.GameResultReasonOperatorForfeit ||
			terminal.ReconnectAuthority.Game.WinnerID == nil || *terminal.ReconnectAuthority.Game.WinnerID != winner ||
			terminal.ReconnectAuthority.Game.ResultRevisionID == nil || *terminal.ReconnectAuthority.Game.ResultRevisionID != command.Settlement.GameResultRevisionID {
			t.Fatalf("terminal Game = %+v", terminal.ReconnectAuthority.Game)
		}
		if terminal.GameResultRevision == nil || terminal.GameResultRevision.ID != command.Settlement.GameResultRevisionID ||
			terminal.GameResultRevision.WinnerID != winner || terminal.GameResultRevision.Reason != domain.GameResultReasonOperatorForfeit ||
			!terminal.GameResultRevision.RecordedAt.Equal(deadline) {
			t.Fatalf("official Game result = %+v", terminal.GameResultRevision)
		}
		series := terminal.ReconnectAuthority.Series
		if series.State != domain.SeriesStateCompleted || series.Score != (domain.SeriesScore{SecondParticipantWins: 1}) ||
			series.WinnerID == nil || *series.WinnerID != winner || series.CurrentScoreRevisionID == nil ||
			*series.CurrentScoreRevisionID != command.Settlement.ScoreRevisionID || series.CurrentResultRevisionID == nil ||
			*series.CurrentResultRevisionID != command.Settlement.SeriesResultRevisionID {
			t.Fatalf("settled Series = %+v", series)
		}
		if terminal.GameResultRevision.Ordinal != authority.CurrentOrdinal+1 || terminal.ScoreRevision == nil ||
			terminal.ScoreRevision.Ordinal != authority.CurrentOrdinal+2 ||
			terminal.ScoreRevision.ScoreBefore != (domain.SeriesScore{}) ||
			terminal.ScoreRevision.ScoreAfter != (domain.SeriesScore{SecondParticipantWins: 1}) ||
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
			terminal.Evidence.ProjectionRevision != authority.CurrentProjectionRevision+1 || terminal.ReconnectAuthority.Current == nil {
			t.Fatalf("settlement evidence = %+v", terminal.Evidence)
		}
		if got := task045Interval(t, terminal.ReconnectAuthority, command.IntervalID); got.State != pause.ReconnectStateExpired ||
			got.ClosedAt == nil || !got.ClosedAt.Equal(deadline) {
			t.Fatalf("expired interval = %+v", got)
		}
		if got := task045Presence(t, terminal.ReconnectAuthority, command.ParticipantID); !task045PresenceEqual(got, authority.Presence[0]) {
			t.Fatalf("timeout changed disconnected Presence: got %+v want %+v", got, authority.Presence[0])
		}

		writes := repository.writeCount()
		replayed, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || changed || repository.writeCount() != writes {
			t.Fatalf("Expire(replay) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		replayed.ScoreRevision.GameResultRevisionIDs[0] = domain.OfficialResultRevisionID(task045ID(99))
		replayed.ReconnectAuthority.Series.Score.SecondParticipantWins = 0
		stored := repository.snapshot()
		if stored.Series.Score.SecondParticipantWins != 1 {
			t.Fatal("mutating timeout receipt changed stored Series")
		}
		terminalSnapshot := repository.snapshot()
		terminalWrites := repository.writeCount()
		terminalCommits := repository.commitCount()
		rollbackReplay, changed, err := reconnectusecase.NewTimeoutUseCase(repository, newReconnectClock(t, base.Add(-2*time.Minute))).Expire(t.Context(), reconnectusecase.TimeoutCommand{
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
			!reflect.DeepEqual(rollbackReplay.ReconnectAuthority.Current, terminalSnapshot.Current) {
			t.Fatalf("terminal rollback replay changed outcome: got %+v, want %+v", rollbackReplay.ReconnectAuthority.Current, terminalSnapshot.Current)
		}
		if repository.writeCount() != terminalWrites || repository.commitCount() != terminalCommits ||
			!reflect.DeepEqual(repository.snapshot(), terminalSnapshot) {
			t.Fatalf("terminal rollback replay mutated storage: writes = %d, commits = %d", repository.writeCount(), repository.commitCount())
		}
		postTerminal := []struct {
			name string
			run  func() (*reconnectusecase.ReconnectRecord, bool, error)
		}{
			{name: "reconnect", run: func() (*reconnectusecase.ReconnectRecord, bool, error) {
				return reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, deadline.Add(time.Second))).Reconnect(t.Context(), reconnectusecase.ReconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(570), ParticipantID: authority.Series.FirstParticipantID,
					IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(571),
				})
			}},
			{name: "disconnect", run: func() (*reconnectusecase.ReconnectRecord, bool, error) {
				return reconnectusecase.NewDisconnectUseCase(repository, newReconnectClock(t, deadline.Add(time.Second))).Disconnect(t.Context(), reconnectusecase.DisconnectCommand{
					Scope: authority.Scope, CommandID: task045ID(580), ParticipantID: authority.Series.SecondParticipantID,
					IntervalID: task045ID(581), Deadline: deadline.Add(time.Minute), Settlement: task045SettlementIDs(582),
				})
			}},
			{name: "timeout", run: func() (*reconnectusecase.ReconnectRecord, bool, error) {
				return reconnectusecase.NewTimeoutUseCase(repository, newReconnectClock(t, deadline.Add(time.Second))).Expire(t.Context(), reconnectusecase.TimeoutCommand{
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
		first := reconnectusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(50),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(51)}
		repository := newTask045RepositoryHarness(t, authority)
		useCase := reconnectusecase.NewTimeoutUseCase(repository, newReconnectClock(t, deadline))
		terminal, changed, err := useCase.Expire(t.Context(), first)
		if err != nil || !changed || repository.writeCount() != 1 {
			t.Fatalf("Expire(both absent) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
		if terminal.ReconnectAuthority.Game.State != domain.GameStateVoid ||
			terminal.ReconnectAuthority.Game.ResultReason != domain.GameResultReasonDisconnect || terminal.ReconnectAuthority.Game.WinnerID != nil ||
			terminal.GameResultRevision != nil || terminal.VoidGameResultRevision == nil ||
			terminal.VoidGameResultRevision.ID != first.Settlement.GameResultRevisionID ||
			terminal.VoidGameResultRevision.Reason != domain.GameResultReasonDisconnect {
			t.Fatalf("both-absent Game result = Game %+v, winner revision %+v, void revision %+v", terminal.ReconnectAuthority.Game, terminal.GameResultRevision, terminal.VoidGameResultRevision)
		}
		if terminal.ReconnectAuthority.Series.State != domain.SeriesStateReplayRequired ||
			terminal.ReconnectAuthority.Series.Score != (domain.SeriesScore{}) || terminal.ReconnectAuthority.Series.WinnerID != nil ||
			terminal.ScoreRevision == nil || terminal.ScoreRevision.ScoreBefore != terminal.ScoreRevision.ScoreAfter ||
			terminal.ReplayRoute == nil || terminal.ReplayRoute.ID != first.Settlement.ReplayRouteID ||
			terminal.ReplayRoute.Category != authority.Series.Slots[0].Category || terminal.ReconnectAuthority.Current == nil {
			t.Fatalf("both-absent Series route = Series %+v, score %+v", terminal.ReconnectAuthority.Series, terminal.ScoreRevision)
		}
		for _, interval := range terminal.ReconnectAuthority.Reconnect {
			if interval.State != pause.ReconnectStateExpired || interval.ClosedAt == nil || !interval.ClosedAt.Equal(deadline) {
				t.Fatalf("both-absent interval = %+v", interval)
			}
		}
		second := reconnectusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(60),
			ParticipantID: authority.Series.SecondParticipantID, IntervalID: authority.Reconnect[1].ID,
			Settlement: task045SettlementIDs(61)}
		_, changed, err = useCase.Expire(t.Context(), second)
		if err != nil || changed || repository.writeCount() != 1 {
			t.Fatalf("Expire(second deadline) error = %v, changed = %v, writes = %d, want one terminal write", err, changed, repository.writeCount())
		}
	})
}
