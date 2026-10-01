package avatar

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/TakuyaYagam1/task-per-minute/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestCleanupWorker_RunRetriesFailedDeleteAndStopsOnCancellation(t *testing.T) {
	baseTime := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	repository := &cleanupWorkerRepositoryFake{
		retryCalls:         make(chan cleanupCall, 1),
		completeCalls:      make(chan cleanupCall, 1),
		secondClaimStarted: make(chan struct{}),
		releaseSecondClaim: make(chan struct{}),
	}
	objects := &cleanupWorkerObjectStorageFake{deleteErrors: []error{
		errors.New("temporary object store failure"),
		nil,
	}}
	worker := NewCleanupWorker(repository, objects, cleanupWorkerClock{now: baseTime}, CleanupConfig{
		Interval:      100 * time.Millisecond,
		ClaimLease:    time.Minute,
		DeleteTimeout: time.Second,
		BatchSize:     1,
	})
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	runFinished := false
	t.Cleanup(func() {
		cancel()
		if runFinished {
			return
		}
		select {
		case <-runResult:
		case <-time.After(3 * time.Second):
			t.Error("worker goroutine did not stop during cleanup")
		}
	})
	go func() {
		runResult <- worker.Run(ctx)
	}()

	var retried cleanupCall
	select {
	case retried = <-repository.retryCalls:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not schedule retry after failed delete")
	}
	require.True(t, worker.Ready())
	require.Equal(t, "avatars/player/object.png", retried.objectKey)
	require.Equal(t, baseTime.Add(30*time.Second), retried.retryAt)
	require.EqualValues(t, 1, repository.retryCount.Load())
	select {
	case <-repository.secondClaimStarted:
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not attempt the next cleanup claim")
	}
	require.Zero(t, repository.completeCount.Load(), "failed storage deletion must not complete the cleanup claim")
	close(repository.releaseSecondClaim)

	select {
	case completed := <-repository.completeCalls:
		require.Equal(t, retried.objectKey, completed.objectKey)
		require.EqualValues(t, 1, repository.completeCount.Load())
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not complete cleanup after the next delete succeeded")
	}
	require.EqualValues(t, 2, objects.deleteCount.Load())

	cancel()
	select {
	case err := <-runResult:
		runFinished = true
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not finish after context cancellation")
	}
	require.False(t, worker.Ready())
}

type cleanupCall struct {
	objectKey string
	retryAt   time.Time
}

type cleanupWorkerRepositoryFake struct {
	claimCount         atomic.Int32
	retryCount         atomic.Int32
	completeCount      atomic.Int32
	retryCalls         chan cleanupCall
	completeCalls      chan cleanupCall
	secondClaimStarted chan struct{}
	releaseSecondClaim chan struct{}
}

func (*cleanupWorkerRepositoryFake) PrepareUpload(context.Context, uuid.UUID, uuid.UUID, string, time.Time) (int64, error) {
	return 0, domain.ErrInternal
}

func (*cleanupWorkerRepositoryFake) GetAvatar(context.Context, uuid.UUID, uuid.UUID) (*domain.PlayerAvatar, error) {
	return nil, domain.ErrInternal
}

func (*cleanupWorkerRepositoryFake) ActivateUpload(
	context.Context,
	uuid.UUID,
	uuid.UUID,
	string,
	string,
	int64,
	[]byte,
	int64,
	time.Time,
) error {
	return domain.ErrInternal
}

func (*cleanupWorkerRepositoryFake) DeleteAvatar(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return domain.ErrInternal
}

func (repository *cleanupWorkerRepositoryFake) ClaimCleanup(
	ctx context.Context,
	_, _ time.Time,
	_ uuid.UUID,
	_ int32,
) ([]CleanupObject, error) {
	claimNumber := repository.claimCount.Add(1)
	if claimNumber > 2 {
		return nil, nil
	}
	if claimNumber == 2 {
		close(repository.secondClaimStarted)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-repository.releaseSecondClaim:
		}
	}
	return []CleanupObject{{
		ObjectKey: "avatars/player/object.png",
		Attempts:  claimNumber - 1,
	}}, nil
}

func (repository *cleanupWorkerRepositoryFake) CompleteCleanup(
	ctx context.Context,
	objectKey string,
	_ uuid.UUID,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.completeCount.Add(1)
	repository.completeCalls <- cleanupCall{objectKey: objectKey}
	return nil
}

func (repository *cleanupWorkerRepositoryFake) RetryCleanup(
	ctx context.Context,
	objectKey string,
	_ uuid.UUID,
	retryAt time.Time,
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	repository.retryCount.Add(1)
	repository.retryCalls <- cleanupCall{objectKey: objectKey, retryAt: retryAt}
	return nil
}

type cleanupWorkerObjectStorageFake struct {
	deleteErrors []error
	deleteCount  atomic.Int32
}

func (*cleanupWorkerObjectStorageFake) PutAvatar(context.Context, string, []byte, string) error {
	return domain.ErrInternal
}

func (*cleanupWorkerObjectStorageFake) GetAvatar(context.Context, string, int64) ([]byte, error) {
	return nil, domain.ErrInternal
}

func (storage *cleanupWorkerObjectStorageFake) DeleteAvatar(ctx context.Context, _ string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	call := storage.deleteCount.Add(1)
	if int(call) > len(storage.deleteErrors) {
		return nil
	}
	return storage.deleteErrors[call-1]
}

type cleanupWorkerClock struct {
	now time.Time
}

func (clock cleanupWorkerClock) Now() time.Time { return clock.now }

var _ Repository = (*cleanupWorkerRepositoryFake)(nil)
var _ ObjectStorage = (*cleanupWorkerObjectStorageFake)(nil)
