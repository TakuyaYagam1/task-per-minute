package reconnect_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	reconnectusecase "github.com/TakuyaYagam1/task-per-minute/internal/usecase/game/reconnect"
)

func TestReconnectTimeoutPreservesCarriedCounterWithoutLocalRoots(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.September, 1, 13, 0, 0, 0, time.UTC)
	authority := task045Authority(base, false, true)
	authority.Counters[0].Used = 1
	authority.Counters[0].Revision = 2
	command := reconnectusecase.TimeoutCommand{
		Scope: authority.Scope, CommandID: task045ID(190),
		ParticipantID: authority.Series.SecondParticipantID, IntervalID: authority.Reconnect[0].ID,
		Settlement: task045SettlementIDs(191),
	}
	repository := newTask045RepositoryHarness(t, authority)
	usecase := reconnectusecase.NewTimeoutUseCase(repository, newReconnectClock(t, authority.Reconnect[0].Deadline))
	record, changed, err := usecase.Expire(t.Context(), command)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, domain.GameStateCompleted, record.ReconnectAuthority.Game.State)
	require.Equal(t, authority.Counters, record.ReconnectAuthority.Counters)
	require.Equal(t, authority.Series.FirstParticipantID, *record.ReconnectAuthority.Game.WinnerID)

	replayed, changed, err := usecase.Expire(t.Context(), command)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, record, replayed)
}

func TestReconnectTimeoutAutoloss(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, time.September, 1, 13, 0, 0, 0, time.UTC)

	testReconnectTimeoutOutcomes(t, base)
	testReconnectTimeoutTiming(t, base)
	testReconnectTimeoutSeries(t, base)
	testReconnectTimeoutValidation(t, base)
	testReconnectTimeoutReceipts(t, base)
}
