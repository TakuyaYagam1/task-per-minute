package game_test

import (
	"errors"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	gameusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game"
)

func testReconnectTimeoutValidation(t *testing.T, base time.Time) {
	t.Helper()

	t.Run("before_deadline_has_no_write", func(t *testing.T) {
		authority := task045Authority(base, true, false)
		command := gameusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(70),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(71)}
		repository := newTask045RepositoryHarness(t, authority)
		useCase := gameusecase.NewTimeoutUseCase(repository, newReconnectClock(t, authority.Reconnect[0].Deadline.Add(-time.Nanosecond)))
		_, changed, err := useCase.Expire(t.Context(), command)
		if !errors.Is(err, gameusecase.ErrDeadline) || changed || repository.writeCount() != 0 {
			t.Fatalf("Expire(before deadline) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("expiry_timestamp_must_advance", func(t *testing.T) {
		authority := task045Authority(base, true, false)
		deadline := authority.Reconnect[0].Deadline
		authority.Reconnect[0].UpdatedAt = deadline
		repository := newTask045RepositoryHarness(t, authority)
		_, changed, err := gameusecase.NewTimeoutUseCase(repository, newReconnectClock(t, deadline)).Expire(t.Context(), gameusecase.TimeoutCommand{
			Scope: authority.Scope, CommandID: task045ID(800), ParticipantID: authority.Series.FirstParticipantID,
			IntervalID: authority.Reconnect[0].ID, Settlement: task045SettlementIDs(801),
		})
		if !errors.Is(err, gameusecase.ErrInvalidMutation) || changed || repository.writeCount() != 0 {
			t.Fatalf("Expire(same interval timestamp) error = %v, changed = %v, writes = %d", err, changed, repository.writeCount())
		}
	})

	t.Run("repository_owned_terminal_receipt_is_deeply_isolated", func(t *testing.T) {
		authority := task045Authority(base, true, false)
		deadline := authority.Reconnect[0].Deadline
		command := gameusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(810),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
			Settlement: task045SettlementIDs(811)}
		repository := newTask045RepositoryHarness(t, authority)
		repository.shareOwned = true
		useCase := gameusecase.NewTimeoutUseCase(repository, newReconnectClock(t, deadline))
		first, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || !changed || first.ScoreRevision == nil || first.ReconnectAuthority.Current == nil {
			t.Fatalf("Expire(repository owned) error = %v, changed = %v, record = %+v", err, changed, first)
		}
		wantID := first.ScoreRevision.GameResultRevisionIDs[0]
		first.ScoreRevision.GameResultRevisionIDs[0] = domain.OfficialResultRevisionID(task045ID(820))
		first.ReconnectAuthority.Current.ScoreRevision.GameResultRevisionIDs[0] = domain.OfficialResultRevisionID(task045ID(821))
		first.ReconnectAuthority.Series.Score = domain.SeriesScore{}
		replayed, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || changed || replayed.ScoreRevision.GameResultRevisionIDs[0] != wantID ||
			replayed.ReconnectAuthority.Current.ScoreRevision.GameResultRevisionIDs[0] != wantID ||
			replayed.ReconnectAuthority.Series.Score.SecondParticipantWins != 1 {
			t.Fatalf("first returned mutation leaked into stored receipt: error = %v, changed = %v, replay = %+v", err, changed, replayed)
		}
		replayed.ScoreRevision.GameResultRevisionIDs[0] = domain.OfficialResultRevisionID(task045ID(822))
		replayed.ReconnectAuthority.Current.ScoreRevision.GameResultRevisionIDs[0] = domain.OfficialResultRevisionID(task045ID(823))
		again, changed, err := useCase.Expire(t.Context(), command)
		if err != nil || changed || again.ScoreRevision.GameResultRevisionIDs[0] != wantID ||
			again.ReconnectAuthority.Current.ScoreRevision.GameResultRevisionIDs[0] != wantID || repository.writeCount() != 1 {
			t.Fatalf("replay returned mutation leaked into stored receipt: error = %v, changed = %v, replay = %+v", err, changed, again)
		}
	})

	t.Run("terminal_receipt_rejects_invalid_previous_links", func(t *testing.T) {
		for index, test := range []struct {
			name   string
			mutate func(*gameusecase.ReconnectRecord)
		}{
			{name: "score_previous_self", mutate: func(record *gameusecase.ReconnectRecord) {
				previous := record.ScoreRevision.ID
				record.ScoreRevision.PreviousRevisionID = &previous
				currentPrevious := record.ReconnectAuthority.Current.ScoreRevision.ID
				record.ReconnectAuthority.Current.ScoreRevision.PreviousRevisionID = &currentPrevious
			}},
			{name: "score_previous_zero", mutate: func(record *gameusecase.ReconnectRecord) {
				previous := domain.SeriesScoreRevisionID{}
				record.ScoreRevision.PreviousRevisionID = &previous
				currentPrevious := domain.SeriesScoreRevisionID{}
				record.ReconnectAuthority.Current.ScoreRevision.PreviousRevisionID = &currentPrevious
			}},
			{name: "series_previous_self", mutate: func(record *gameusecase.ReconnectRecord) {
				previous := record.SeriesResultRevision.ID
				record.SeriesResultRevision.PreviousRevisionID = &previous
				currentPrevious := record.ReconnectAuthority.Current.SeriesResultRevision.ID
				record.ReconnectAuthority.Current.SeriesResultRevision.PreviousRevisionID = &currentPrevious
			}},
			{name: "series_previous_zero", mutate: func(record *gameusecase.ReconnectRecord) {
				previous := domain.OfficialResultRevisionID{}
				record.SeriesResultRevision.PreviousRevisionID = &previous
				currentPrevious := domain.OfficialResultRevisionID{}
				record.ReconnectAuthority.Current.SeriesResultRevision.PreviousRevisionID = &currentPrevious
			}},
			{name: "series_state_not_completed", mutate: func(record *gameusecase.ReconnectRecord) {
				record.SeriesResultRevision.State = domain.SeriesStateCancelled
				record.ReconnectAuthority.Current.SeriesResultRevision.State = domain.SeriesStateCancelled
			}},
		} {
			t.Run(test.name, func(t *testing.T) {
				authority := task045Authority(base, true, false)
				deadline := authority.Reconnect[0].Deadline
				command := gameusecase.TimeoutCommand{Scope: authority.Scope, CommandID: task045ID(900 + index*10),
					ParticipantID: authority.Series.FirstParticipantID, IntervalID: authority.Reconnect[0].ID,
					Settlement: task045SettlementIDs(901 + index*10)}
				repository := newTask045RepositoryHarness(t, authority)
				useCase := gameusecase.NewTimeoutUseCase(repository, newReconnectClock(t, deadline))
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
}
