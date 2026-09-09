//go:build integration

package integration_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	redisadapter "github.com/TakuyaYagam1/task-per-minute/internal/adapter/outbound/redis"
	"github.com/TakuyaYagam1/task-per-minute/internal/usecase/idempotency"
)

func TestCommandReceiptStoreCoordinatesReplicasAndReplays(t *testing.T) {
	redis := sharedRedis(t)
	first := redisadapter.NewCommandReceiptStore(redis.client, time.Second, time.Minute, time.Second)
	second := redisadapter.NewCommandReceiptStore(redis.client, time.Second, time.Minute, time.Second)
	command := idempotency.Command{
		Namespace: "integration-command",
		ID:        uuid.New(),
		PayloadDigest: [32]byte{
			1,
		},
	}

	var acquired atomic.Int64
	var inFlight atomic.Int64
	var acquiredLease idempotency.LeaseToken
	var leaseLock sync.Mutex
	errors := make(chan error, 16)
	var group sync.WaitGroup
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			store := first
			if index%2 == 1 {
				store = second
			}
			lease, leaseErr := idempotency.NewLeaseToken()
			if leaseErr != nil {
				errors <- leaseErr
				return
			}
			begin, err := store.Begin(context.Background(), command, lease)
			if err != nil {
				errors <- err
				return
			}
			switch begin.Disposition {
			case idempotency.BeginAcquired:
				acquired.Add(1)
				leaseLock.Lock()
				acquiredLease = begin.Lease
				leaseLock.Unlock()
			case idempotency.BeginInFlight:
				inFlight.Add(1)
			}
		}(index)
	}
	group.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.EqualValues(t, 1, acquired.Load())
	require.EqualValues(t, 15, inFlight.Load())

	require.NoError(t, first.MarkFailed(context.Background(), command, acquiredLease))
	secondLease := integrationLease(t)
	begin, err := second.Begin(context.Background(), command, secondLease)
	require.NoError(t, err)
	require.Equal(t, idempotency.BeginAcquired, begin.Disposition)
	require.Equal(t, secondLease, begin.Lease)
	require.NoError(t, second.MarkSucceeded(context.Background(), command, secondLease))

	begin, err = first.Begin(context.Background(), command, integrationLease(t))
	require.NoError(t, err)
	require.Equal(t, idempotency.BeginSucceeded, begin.Disposition)

	conflict := command
	conflict.PayloadDigest[0] = 2
	_, err = first.Begin(context.Background(), conflict, integrationLease(t))
	require.ErrorIs(t, err, idempotency.ErrPayloadConflict)
}

func TestCommandReceiptStoreExpiresInFlightAdmission(t *testing.T) {
	redis := sharedRedis(t)
	store := redisadapter.NewCommandReceiptStore(redis.client, 25*time.Millisecond, time.Minute, time.Second)
	command := idempotency.Command{
		Namespace: "integration-command-expiry",
		ID:        uuid.New(),
		PayloadDigest: [32]byte{
			1,
		},
	}

	lease := integrationLease(t)
	begin, err := store.Begin(context.Background(), command, lease)
	require.NoError(t, err)
	require.Equal(t, idempotency.BeginAcquired, begin.Disposition)
	require.Equal(t, lease, begin.Lease)

	require.Eventually(t, func() bool {
		newLease := integrationLease(t)
		result, beginErr := store.Begin(context.Background(), command, newLease)
		return beginErr == nil && result.Disposition == idempotency.BeginAcquired && result.Lease == newLease
	}, time.Second, 10*time.Millisecond)
}
