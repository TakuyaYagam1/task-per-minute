package eventdelivery_test

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	delivery "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery"
	deliverymocks "github.com/TakuyaYagam1/task-per-minute/internal/usecase/eventdelivery/mocks"
)

func TestReceiptRetentionWorkerUsesDefaultsForOneBoundedRequest(t *testing.T) {
	t.Parallel()

	store := deliverymocks.NewMockReceiptRetentionStore(t)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	expected := delivery.ReceiptRetentionRequest{
		CutoffAt:  now.Add(-30 * 24 * time.Hour),
		BatchSize: 128,
	}
	store.EXPECT().PruneClosedSubscribers(mock.Anything, expected).Return(
		delivery.ReceiptRetentionResult{DeletedReceipts: 2, DeletedSubscribers: 1},
		nil,
	).Once()
	worker, err := delivery.NewReceiptRetentionWorker(store, delivery.ReceiptRetentionWorkerConfig{
		Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	result, err := worker.Process(t.Context())

	require.NoError(t, err)
	require.Equal(t, delivery.ReceiptRetentionResult{DeletedReceipts: 2, DeletedSubscribers: 1}, result)
}

func TestReceiptRetentionWorkerRejectsStoreAndResultFailure(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	request := delivery.ReceiptRetentionRequest{CutoffAt: now.Add(-time.Hour), BatchSize: 1}

	t.Run("store failure", func(t *testing.T) {
		store := deliverymocks.NewMockReceiptRetentionStore(t)
		storeErr := errors.New("postgres unavailable")
		store.EXPECT().PruneClosedSubscribers(mock.Anything, request).Return(
			delivery.ReceiptRetentionResult{}, storeErr,
		).Once()
		worker, err := delivery.NewReceiptRetentionWorker(store, delivery.ReceiptRetentionWorkerConfig{
			Retention: time.Hour, BatchSize: 1, Now: func() time.Time { return now },
		})
		require.NoError(t, err)

		_, err = worker.Process(t.Context())

		require.ErrorIs(t, err, storeErr)
	})

	t.Run("invalid result", func(t *testing.T) {
		store := deliverymocks.NewMockReceiptRetentionStore(t)
		store.EXPECT().PruneClosedSubscribers(mock.Anything, request).Return(
			delivery.ReceiptRetentionResult{DeletedReceipts: -1}, nil,
		).Once()
		worker, err := delivery.NewReceiptRetentionWorker(store, delivery.ReceiptRetentionWorkerConfig{
			Retention: time.Hour, BatchSize: 1, Now: func() time.Time { return now },
		})
		require.NoError(t, err)

		_, err = worker.Process(t.Context())

		require.ErrorIs(t, err, delivery.ErrRepositoryState)
	})
}

func TestReceiptRetentionWorkerHonorsCancellationAndReadinessAcrossRuns(t *testing.T) {
	store := deliverymocks.NewMockReceiptRetentionStore(t)
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	request := delivery.ReceiptRetentionRequest{CutoffAt: now.Add(-time.Hour), BatchSize: 1}
	worker, err := delivery.NewReceiptRetentionWorker(store, delivery.ReceiptRetentionWorkerConfig{
		Retention: time.Hour, Interval: time.Hour, BatchSize: 1, Now: func() time.Time { return now },
	})
	require.NoError(t, err)

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = worker.Process(cancelled)
	require.ErrorIs(t, err, context.Canceled)

	firstProcessed := make(chan struct{})
	var calls atomic.Int32
	store.EXPECT().PruneClosedSubscribers(mock.Anything, request).RunAndReturn(
		func(context.Context, delivery.ReceiptRetentionRequest) (delivery.ReceiptRetentionResult, error) {
			if calls.Add(1) == 1 {
				close(firstProcessed)
			}
			return delivery.ReceiptRetentionResult{}, nil
		},
	).Twice()

	firstCtx, firstCancel := context.WithCancel(t.Context())
	firstDone := make(chan error, 1)
	go func() { firstDone <- worker.Run(firstCtx) }()
	awaitEventDeliverySignal(t, firstProcessed)
	require.Eventually(t, worker.Ready, eventDeliveryTestWait, time.Millisecond)
	require.ErrorIs(t, worker.Run(t.Context()), delivery.ErrWorkerRunning)
	firstCancel()
	require.NoError(t, awaitEventDeliveryWorker(t, firstDone))
	require.False(t, worker.Ready())

	secondCtx, secondCancel := context.WithCancel(t.Context())
	secondDone := make(chan error, 1)
	go func() { secondDone <- worker.Run(secondCtx) }()
	require.Eventually(t, worker.Ready, eventDeliveryTestWait, time.Millisecond)
	secondCancel()
	require.NoError(t, awaitEventDeliveryWorker(t, secondDone))
	require.False(t, worker.Ready())
	require.EqualValues(t, 2, calls.Load())
}
