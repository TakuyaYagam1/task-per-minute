package reconnect_test

import (
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

func testReconnectTimeoutSeries(t *testing.T, base time.Time) {
	t.Helper()

	t.Run("staggered_timeout_then_reconnect_awards_reconnector", func(t *testing.T) {
		authority := task045Authority(base, true, true)
		at := authority.Reconnect[0].Deadline
		authority.Reconnect[1].Deadline = at.Add(20 * time.Second)
		repository := newTask045RepositoryHarness(t, authority)
		if _, changed, err := reconnectusecase.NewTimeoutUseCase(repository, newReconnectClock(t, at)).Expire(t.Context(), reconnectusecase.TimeoutCommand{
			Scope: authority.Scope, CommandID: task045ID(520), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(521),
		}); err != nil || !changed {
			t.Fatalf("Expire(staggered first) error = %v, changed = %v", err, changed)
		}
		reconnectAt := at.Add(time.Nanosecond)
		if _, changed, err := reconnectusecase.ReconnectNewUseCase(repository, newReconnectClock(t, reconnectAt)).Reconnect(t.Context(), reconnectusecase.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(530), ParticipantID: authority.Series.SecondParticipantID,
			IntervalID: authority.Reconnect[1].ID, Settlement: task045SettlementIDs(531),
		}); err != nil || !changed {
			t.Fatalf("Reconnect(staggered second) error = %v, changed = %v", err, changed)
		}
		stored := repository.snapshot()
		if repository.writeCount() != 2 || repository.conflictCount() != 0 || stored.Current == nil ||
			stored.Game.WinnerID == nil || *stored.Game.WinnerID != authority.Series.SecondParticipantID ||
			stored.Game.ResultReason != domain.GameResultReasonOperatorForfeit {
			t.Fatalf("staggered race did not award reconnector: writes = %d, conflicts = %d, authority = %+v", repository.writeCount(), repository.conflictCount(), stored)
		}
	})

	t.Run("bo3_one_one_keeps_cumulative_lineage", func(t *testing.T) {
		authority := task045Authority(base, false, true)
		oldResultID := authority.CurrentGameResultRevisionIDs[0]
		oldScoreID := domain.SeriesScoreRevisionID(task045ID(540))
		oldWinner := authority.Series.SecondParticipantID
		oldGame := domain.Game{ID: task045ID(541), SlotID: task045ID(542), AttemptNo: 1,
			State: domain.GameStateCompleted, ResultReason: domain.GameResultReasonOperatorForfeit,
			WinnerID: &oldWinner, ResultRevisionID: &oldResultID}
		currentSlot := authority.Series.Slots[0]
		currentSlot.Position = 2
		currentSlot.ScoreBefore = domain.SeriesScore{SecondParticipantWins: 1}
		authority.Series.Format = domain.SeriesFormatBO3
		authority.Series.Score = domain.SeriesScore{SecondParticipantWins: 1}
		authority.Series.CurrentScoreRevisionID = &oldScoreID
		authority.Series.Slots = []domain.GameSlot{
			{ID: oldGame.SlotID, SeriesID: authority.Series.ID, Position: 1, Category: currentSlot.Category,
				ScoreBefore: domain.SeriesScore{}, Attempts: []domain.Game{oldGame}},
			currentSlot,
		}
		authority.CurrentOrdinal = 3
		command := reconnectusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(550),
			ParticipantID: authority.Series.SecondParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(551)}
		repository := newTask045RepositoryHarness(t, authority)
		terminal, changed, err := reconnectusecase.NewTimeoutUseCase(repository, newReconnectClock(t, authority.Reconnect[0].Deadline)).Expire(t.Context(), command)
		if err != nil || !changed || terminal.ReconnectAuthority.Series.State != domain.SeriesStateActive ||
			terminal.ReconnectAuthority.Series.Score != (domain.SeriesScore{FirstParticipantWins: 1, SecondParticipantWins: 1}) ||
			terminal.ReconnectAuthority.Game.WinnerID == nil || *terminal.ReconnectAuthority.Game.WinnerID != authority.Series.FirstParticipantID ||
			terminal.GameResultRevision.Ordinal != 4 || terminal.ScoreRevision.Ordinal != 5 || terminal.SeriesResultRevision != nil ||
			len(terminal.ScoreRevision.GameResultRevisionIDs) != 2 || terminal.ScoreRevision.GameResultRevisionIDs[0] != oldResultID ||
			terminal.ScoreRevision.GameResultRevisionIDs[1] != command.Settlement.GameResultRevisionID || terminal.ReconnectAuthority.CurrentOrdinal != 5 ||
			!task045GameEqual(terminal.ReconnectAuthority.Series.Slots[1].Attempts[0], terminal.ReconnectAuthority.Game) {
			t.Fatalf("BO3 cumulative lineage error = %v, changed = %v, record = %+v", err, changed, terminal)
		}
	})
}
