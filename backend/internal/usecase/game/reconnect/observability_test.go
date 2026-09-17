package reconnect_test

import (
	"context"
	"testing"
	"time"

	gamemocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect/mocks"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestReconnectObservability(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, time.September, 2, 15, 0, 0, 0, time.UTC)
	t.Run("reconnect", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		interval := authority.Reconnect[0]
		command := reconnectusecase.ReconnectCommand{
			Scope: authority.Scope, CommandID: task045ID(7330),
			ParticipantID: authority.Series.FirstParticipantID, IntervalID: interval.ID,
			Settlement: task045SettlementIDs(7331),
		}
		observer := gamemocks.NewMockObserver(t)
		observer.EXPECT().Observe(t.Context(), mock.MatchedBy(func(event reconnectusecase.ReconnectEvent) bool {
			return event.ReconnectEvent == "tournament.command.reconnect" &&
				event.Outcome == reconnectusecase.OutcomeSuccess &&
				event.CommandID == command.CommandID &&
				event.TournamentID == command.Scope.TournamentID &&
				event.ParticipantID == command.ParticipantID &&
				event.Stage == "reconnect" && event.Transition == "reconnect" &&
				event.ReasonCode == "committed" && event.Revision == authority.Revision+1 &&
				event.Duration == 0 && !event.HasDeadlineLag && event.DeadlineLag == 0
		})).Once()

		_, changed, err := reconnectusecase.ReconnectNewUseCase(
			newTask045RepositoryHarness(t, authority),
			newReconnectClock(t, now),
			observer,
		).Reconnect(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
	})

	t.Run("timeout lag", func(t *testing.T) {
		authority := task045Authority(now, true, false)
		deadline := authority.Reconnect[0].Deadline
		command := reconnectusecase.TimeoutCommand{
			Scope: authority.Scope, CommandID: task045ID(7340),
			ParticipantID: authority.Series.FirstParticipantID,
			IntervalID:    authority.Reconnect[0].ID,
			Settlement:    task045SettlementIDs(7341),
		}
		observer := gamemocks.NewMockObserver(t)
		observer.EXPECT().Observe(t.Context(), mock.MatchedBy(func(event reconnectusecase.ReconnectEvent) bool {
			return event.ReconnectEvent == "tournament.command.reconnect_timeout" &&
				event.Outcome == reconnectusecase.OutcomeSuccess &&
				event.CommandID == command.CommandID &&
				event.TournamentID == command.Scope.TournamentID &&
				event.ParticipantID == command.ParticipantID &&
				event.Stage == "deadline" && event.Transition == "expire_reconnect" &&
				event.ReasonCode == "committed" && event.Revision == authority.Revision+1 &&
				event.Duration == 0 && event.HasDeadlineLag &&
				event.DeadlineLag == 500*time.Millisecond
		})).Once()

		_, changed, err := reconnectusecase.NewTimeoutUseCase(
			newTask045RepositoryHarness(t, authority),
			newReconnectClock(t, deadline.Add(500*time.Millisecond)),
			observer,
		).Expire(t.Context(), command)
		require.NoError(t, err)
		require.True(t, changed)
	})
}

func TestObserveEventIsolatesObserverPanic(t *testing.T) {
	t.Parallel()

	observer := gamemocks.NewMockObserver(t)
	observer.EXPECT().Observe(mock.Anything, mock.Anything).
		Run(func(context.Context, reconnectusecase.ReconnectEvent) { panic("observer failed") }).Once()

	require.NotPanics(t, func() {
		reconnectusecase.ObserveEvent(t.Context(), observer, reconnectusecase.ReconnectEvent{})
	})
}
