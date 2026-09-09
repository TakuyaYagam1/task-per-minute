package recovery_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
	recoverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery/mocks"
)

func TestDeadlineSweepEmitsOneTerminalEventPerRearmBoundary(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	first := gameDeadline(at.Add(time.Minute), 3)
	second := readyWindowDeadline(at.Add(2*time.Minute), 5)
	source := recoverymocks.NewMockDeadlineSource(t)
	rearmer := recoverymocks.NewMockDeadlineRearmer(t)
	observer := recoverymocks.NewMockRecoveryObserver(t)
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(2)).
		Return([]recovery.PendingDeadline{first, second}, nil).Once()
	rearmer.EXPECT().RearmDeadline(mock.Anything, first).Return(true, nil).Once()
	rearmer.EXPECT().RearmDeadline(mock.Anything, second).Return(false, nil).Once()
	observer.EXPECT().ObserveRecovery(mock.Anything, mock.MatchedBy(func(event recovery.RecoveryEvent) bool {
		return event.Outcome == recovery.RecoveryOutcomeSuccess &&
			event.Stage == recovery.RecoveryStageDeadline &&
			event.TournamentID == first.TournamentID && event.Transition == "deadline_rearmed" &&
			event.ReasonCode == "work_rearmed" && event.Revision == first.ExpectedRevision
	})).Once()
	observer.EXPECT().ObserveRecovery(mock.Anything, mock.MatchedBy(func(event recovery.RecoveryEvent) bool {
		return event.Outcome == recovery.RecoveryOutcomeRejected &&
			event.Stage == recovery.RecoveryStageDeadline &&
			event.TournamentID == second.TournamentID && event.Transition == "deadline_already_current" &&
			event.ReasonCode == "nothing_to_rearm" && event.Revision == second.ExpectedRevision
	})).Once()

	result, err := recovery.NewDeadlineSweep(source, rearmer, observer).
		Sweep(t.Context(), recovery.DeadlineCursor{}, 2)

	require.NoError(t, err)
	require.Equal(t, 1, result.Rearmed)
}

func TestDeadlineSweepIsolatesObserverPanic(t *testing.T) {
	t.Parallel()

	deadline := gameDeadline(time.Date(2026, 9, 6, 10, 1, 0, 0, time.UTC), 3)
	source := recoverymocks.NewMockDeadlineSource(t)
	rearmer := recoverymocks.NewMockDeadlineRearmer(t)
	observer := recoverymocks.NewMockRecoveryObserver(t)
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(1)).
		Return([]recovery.PendingDeadline{deadline}, nil).Once()
	rearmer.EXPECT().RearmDeadline(mock.Anything, deadline).Return(true, nil).Once()
	observer.EXPECT().ObserveRecovery(mock.Anything, mock.Anything).
		Run(func(context.Context, recovery.RecoveryEvent) { panic("observer failure") }).Once()

	require.NotPanics(t, func() {
		result, err := recovery.NewDeadlineSweep(source, rearmer, observer).
			Sweep(t.Context(), recovery.DeadlineCursor{}, 1)
		require.NoError(t, err)
		require.Equal(t, 1, result.Rearmed)
	})
}
