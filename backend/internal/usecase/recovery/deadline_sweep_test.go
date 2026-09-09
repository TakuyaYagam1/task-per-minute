package recovery_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/recovery"
)

func TestDeadlineSweepRearmsBoundedOrderedBatch(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	first := gameDeadline(at.Add(time.Minute), 3)
	second := readyWindowDeadline(at.Add(2*time.Minute), 5)
	sweep, source, rearmer := newDeadlineSweep(t)
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(2)).
		Return([]recovery.PendingDeadline{first, second}, nil).Once()
	rearmer.EXPECT().RearmDeadline(mock.Anything, first).Return(true, nil).Once()
	rearmer.EXPECT().RearmDeadline(mock.Anything, second).Return(false, nil).Once()

	result, err := sweep.Sweep(t.Context(), recovery.DeadlineCursor{}, 2)

	require.NoError(t, err)
	require.Equal(t, recovery.SweepResult{
		Scanned: 2, Rearmed: 1, NextCursor: second.Cursor(), Complete: false,
	}, result)
}

func TestDeadlineSweepFailsClosedOnInvalidRepositoryBatch(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		deadlines []recovery.PendingDeadline
	}{
		{name: "out of order", deadlines: []recovery.PendingDeadline{
			readyWindowDeadline(at, 1), gameDeadline(at, 1),
		}},
		{name: "duplicate", deadlines: []recovery.PendingDeadline{
			gameDeadline(at, 1), gameDeadline(at, 1),
		}},
		{name: "invalid shape", deadlines: []recovery.PendingDeadline{
			func() recovery.PendingDeadline {
				deadline := gameDeadline(at, 1)
				deadline.ParticipantID = recoveryDeadlineID(99)
				return deadline
			}(),
		}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			sweep, source, _ := newDeadlineSweep(t)
			source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(8)).
				Return(test.deadlines, nil).Once()

			_, err := sweep.Sweep(t.Context(), recovery.DeadlineCursor{}, 8)

			require.ErrorIs(t, err, recovery.ErrInvalidBatch)
		})
	}
}

func TestDeadlineSweepRetriesWholeBatchAfterRearmFailure(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 6, 10, 0, 0, 0, time.UTC)
	deadline := reconnectDeadline(at, 7)
	wantErr := errors.New("scheduler unavailable")
	sweep, source, rearmer := newDeadlineSweep(t)
	source.EXPECT().ListPendingDeadlines(mock.Anything, recovery.DeadlineCursor{}, int32(1)).
		Return([]recovery.PendingDeadline{deadline}, nil).Once()
	rearmer.EXPECT().RearmDeadline(mock.Anything, deadline).Return(false, wantErr).Once()

	result, err := sweep.Sweep(t.Context(), recovery.DeadlineCursor{}, 1)

	require.Empty(t, result)
	require.ErrorIs(t, err, wantErr)
}
